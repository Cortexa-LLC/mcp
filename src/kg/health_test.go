package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
)

// seedHealthFixture creates <root>/.ai/knowledge.db with a small graph:
// three entities (one orphaned), one relation, and six observations — two
// written normally (one [OBSOLETE-marked), two aged into legacy state (NULL
// and stored-zero created_at), and two back-dated to 2020 and 2022 to pin the
// age stats. The store is closed before returning so runHealth can open the
// database read-only.
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

	// Four observations with STORED timestamps this writer cannot produce
	// directly — two legacy (NULL and stored zero), two back-dated — written
	// the only way it can: by mutating created_at after the fact. The legacy
	// pair is what the zero-timestamp metric exists to count; a fixture whose
	// every row carries a real timestamp cannot fail a broken counter. The stored-zero row in
	// particular guards against binding the zero time as a Go parameter,
	// which go-kuzu mangles through UnixNano into a 1754 date that matches
	// nothing.
	for _, mutation := range []string{
		"SET o.created_at = NULL",
		`SET o.created_at = timestamp("0001-01-01 00:00:00")`,
		// Two dated rows pin the age stats: with the two freshly-written
		// observations above, the four timestamped rows sort as
		// [2020, 2022, now, now], so oldest must be 2020 and the (lower)
		// median must be 2022 — a median query that grabs the first, last,
		// or a legacy row cannot pass.
		`SET o.created_at = timestamp("2020-01-01 00:00:00")`,
		`SET o.created_at = timestamp("2022-06-15 00:00:00")`,
	} {
		obs, err := store.CreateObservation(e2.ID, "legacy-era note", projectID)
		if err != nil {
			t.Fatalf("CreateObservation: %v", err)
		}
		result, err := store.QueryParams(
			"MATCH (o:Observation {id: $id}) "+mutation, map[string]any{"id": obs.ID})
		if err != nil {
			t.Fatalf("age observation (%s): %v", mutation, err)
		}
		result.Close()
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
	if out.Current.Observations != 6 {
		t.Errorf("observations = %d, want 6", out.Current.Observations)
	}
	// Exactly the two legacy rows — not 0 (metric dead) and not 6 (metric
	// counting scan artifacts instead of stored values).
	if out.Current.ZeroTimestampObservations != 2 {
		t.Errorf("zero-timestamp observations = %d, want 2 (one NULL + one stored zero)",
			out.Current.ZeroTimestampObservations)
	}
	if out.Current.OrphanedEntities != 1 {
		t.Errorf("orphaned entities = %d, want 1", out.Current.OrphanedEntities)
	}
	if oa := out.Current.ObservationAge; oa == nil {
		t.Error("observation age missing with 4 timestamped observations")
	} else {
		if oa.Oldest.Year() != 2020 {
			t.Errorf("oldest observation year = %d, want 2020", oa.Oldest.Year())
		}
		if oa.Median.Year() != 2022 {
			t.Errorf("median observation year = %d, want 2022 (lower median of [2020 2022 now now])", oa.Median.Year())
		}
		if time.Since(oa.Newest) > time.Minute {
			t.Errorf("newest observation = %v, want within the last minute", oa.Newest)
		}
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

	// Grow the graph and check the deltas' magnitude AND sign — a zero-growth
	// assertion alone cannot tell current-minus-previous from its inverse.
	store, err := knowledge.OpenStore(filepath.Join(root, ".ai", "knowledge.db"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	projectID := projectIDFromCwd(root)
	g1, err := store.CreateEntity("growth entity one", "topic", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	g2, err := store.CreateEntity("growth entity two", "topic", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(g1.ID, "growth note", projectID); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.CreateRelation(g1.ID, g2.ID, knowledge.RelRelatesTo, projectID); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	store.Close()

	buf.Reset()
	if err := runHealth(root, "", true, &buf); err != nil {
		t.Fatalf("third runHealth: %v", err)
	}
	var third healthOutput
	if err := json.Unmarshal(buf.Bytes(), &third); err != nil {
		t.Fatalf("third health --json output does not parse: %v", err)
	}
	if third.Growth == nil {
		t.Fatal("third run has no growth")
	}
	if third.Growth.Entities != 2 || third.Growth.Relations != 1 || third.Growth.Observations != 1 {
		t.Errorf("growth after adding 2 entities, 1 relation, 1 observation = %+v, want {2 1 1}", *third.Growth)
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
	for _, want := range []string{"legacy, age unknown", "Orphaned entities", "No previous snapshot", "Observation age: newest"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("human output missing %q:\n%s", want, buf.String())
		}
	}
}

// An explicitly named scope that cannot be loaded is an error for both
// report commands (kg health and kg stats share resolveScopeDB) — never a
// silent fallback to the legacy database, which answers the wrong question.
func TestResolveScopeDBErrorsOnUnloadableScope(t *testing.T) {
	aiDir := filepath.Join(t.TempDir(), ".ai")
	if err := os.MkdirAll(aiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, _, err := resolveScopeDB(aiDir, "no-such-scope"); err == nil {
		t.Error("resolveScopeDB with an unloadable scope returned nil error, want failure")
	}
	// No scope named and none configured: the legacy database, no error.
	dbPath, scopeName, err := resolveScopeDB(aiDir, "")
	if err != nil || scopeName != "" || filepath.Base(dbPath) != "knowledge.db" {
		t.Errorf("legacy resolution = (%q, %q, %v), want knowledge.db path, empty scope, nil", dbPath, scopeName, err)
	}
}

// humanAge's thresholds, which the report's only age line depends on. The
// human-output test can pass on an empty or misordered rendering, so the
// boundaries are pinned here directly.
func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{time.Minute, "1m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{23 * time.Hour, "23h"},
		{24 * time.Hour, "1d"},
		{29 * 24 * time.Hour, "29d"},
		{30 * 24 * time.Hour, "1mo"},
		// The month/year boundary is 12 months of 30 days, so the ladder goes
		// 11mo -> 1y with no "12mo" step.
		{359 * 24 * time.Hour, "11mo"},
		{360 * 24 * time.Hour, "1y"},
		{364 * 24 * time.Hour, "1y"},
		{365 * 24 * time.Hour, "1y"},
		{800 * 24 * time.Hour, "2y"},
		// A stored timestamp ahead of the report's clock (machine skew, or a
		// hand-set created_at) must be named, not rendered as "just now".
		{-time.Hour, "in the future"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// The age line names newest, median, and oldest in that order with real
// values — an assertion on the label alone passes even if humanAge returns
// empty strings or the three are rendered in the wrong order.
func TestHealthHumanOutputAgeLine(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	root := t.TempDir()
	seedHealthFixture(t, root)

	var buf bytes.Buffer
	if err := runHealth(root, "", false, &buf); err != nil {
		t.Fatalf("runHealth: %v", err)
	}
	// The fixture's timestamped rows are [2020, 2022, now, now], so the line
	// must read newest "just now", median ~4y, oldest ~6y — ordered oldest
	// last. Years drift with the wall clock, so match the shape and the
	// ordering rather than exact ages.
	line := ""
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(l, "Observation age:") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no observation-age line in report:\n%s", buf.String())
	}
	newestIdx := strings.Index(line, "newest just now")
	medianIdx := strings.Index(line, "median ")
	oldestIdx := strings.Index(line, "oldest ")
	if newestIdx < 0 || medianIdx < 0 || oldestIdx < 0 {
		t.Fatalf("age line missing a labelled value: %q", line)
	}
	if !(newestIdx < medianIdx && medianIdx < oldestIdx) {
		t.Errorf("age line out of order (newest, median, oldest): %q", line)
	}
	if strings.Contains(line, "median y") || strings.Contains(line, "median ,") {
		t.Errorf("age line has an empty median value: %q", line)
	}
	// Median (2022) must read as a smaller age than oldest (2020).
	var medianYears, oldestYears int
	if _, err := fmt.Sscanf(line[medianIdx:], "median %dy", &medianYears); err != nil {
		t.Fatalf("median value is not a year age in %q: %v", line, err)
	}
	if _, err := fmt.Sscanf(line[oldestIdx:], "oldest %dy", &oldestYears); err != nil {
		t.Fatalf("oldest value is not a year age in %q: %v", line, err)
	}
	if medianYears >= oldestYears {
		t.Errorf("median age %dy is not younger than oldest %dy: %q", medianYears, oldestYears, line)
	}
}
