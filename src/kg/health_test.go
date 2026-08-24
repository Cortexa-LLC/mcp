package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
)

// seedHealthFixture creates <root>/.ai/knowledge.db with a small graph:
// three entities (one orphaned), two observations (one [OBSOLETE-marked),
// and one relation. The store is closed before returning so runHealth can
// open the database read-only.
func seedHealthFixture(t *testing.T, root string) {
	t.Helper()

	aiDir := filepath.Join(root, ".ai")
	if err := os.MkdirAll(aiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	store, err := knowledge.OpenStore(filepath.Join(aiDir, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	projectID := projectIDFromCwd(root)
	e1, err := store.CreateEntity("healthy entity", "decision", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(e1.ID, "a current note", projectID); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	e2, err := store.CreateEntity("curated entity", "topic", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(e2.ID, "[OBSOLETE — server era] old guidance", projectID); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.CreateRelation(e1.ID, e2.ID, knowledge.RelRelatesTo, projectID); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	// No observations, no relations: the orphan the report must count.
	if _, err := store.CreateEntity("orphaned entity", "topic", projectID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
}

// kg health runs against a database without error, its JSON output parses,
// the metrics reflect the graph, and the second run reports growth against
// the snapshot the first run persisted.
func TestHealthCommandReportsMetricsAndGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	root := t.TempDir()
	seedHealthFixture(t, root)

	var buf bytes.Buffer
	if err := runHealth(root, "", true, &buf); err != nil {
		t.Fatalf("runHealth: %v", err)
	}

	var out healthOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("health --json output does not parse: %v\n%s", err, buf.String())
	}

	if out.Current == nil {
		t.Fatal("health output has no current metrics")
	}
	if out.Current.Entities != 3 {
		t.Errorf("entities = %d, want 3", out.Current.Entities)
	}
	if got := out.Current.EntitiesByType["topic"]; got != 2 {
		t.Errorf("topic entities = %d, want 2", got)
	}
	if out.Current.Relations != 1 {
		t.Errorf("relations = %d, want 1", out.Current.Relations)
	}
	if out.Current.Observations != 2 {
		t.Errorf("observations = %d, want 2", out.Current.Observations)
	}
	if out.Current.ZeroTimestampObservations != 0 {
		t.Errorf("zero-timestamp observations = %d, want 0 (this writer always stores real timestamps)",
			out.Current.ZeroTimestampObservations)
	}
	if out.Current.OrphanedEntities != 1 {
		t.Errorf("orphaned entities = %d, want 1", out.Current.OrphanedEntities)
	}
	if out.Current.ObsoleteObservations != 1 {
		t.Errorf("[OBSOLETE observations = %d, want 1", out.Current.ObsoleteObservations)
	}
	if out.Growth != nil {
		t.Error("first run reported growth with no previous snapshot")
	}

	// The first run persisted a snapshot; the second must report zero growth
	// against it.
	if _, err := os.Stat(filepath.Join(root, ".ai", healthSnapshotFile)); err != nil {
		t.Fatalf("snapshot was not written: %v", err)
	}
	buf.Reset()
	if err := runHealth(root, "", true, &buf); err != nil {
		t.Fatalf("second runHealth: %v", err)
	}
	var second healthOutput
	if err := json.Unmarshal(buf.Bytes(), &second); err != nil {
		t.Fatalf("second health --json output does not parse: %v", err)
	}
	if second.Previous == nil || second.Growth == nil {
		t.Fatal("second run has no previous snapshot / growth")
	}
	if second.Growth.Entities != 0 || second.Growth.Relations != 0 || second.Growth.Observations != 0 {
		t.Errorf("growth on unchanged graph = %+v, want all zero", *second.Growth)
	}
}

// The default human-readable report renders without error and labels the
// zero-timestamp share the way the curation policy names it.
func TestHealthCommandHumanOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	root := t.TempDir()
	seedHealthFixture(t, root)

	var buf bytes.Buffer
	if err := runHealth(root, "", false, &buf); err != nil {
		t.Fatalf("runHealth: %v", err)
	}
	for _, want := range []string{"legacy, age unknown", "Orphaned entities", "No previous snapshot"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("human output missing %q:\n%s", want, buf.String())
		}
	}
}
