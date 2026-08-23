package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

// Indexer.Index starts by deleting every entity belonging to the project, which
// has always taken hand-written entities with it — they carry the same project
// ID as the indexed ones. The journal is the only record of those, so an index
// run has to replay it afterwards. Without that, `kg add entity` followed by
// `kg index` silently loses the entity.
func TestIndexRestoresHandWritesAfterClearingTheProject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "knowledge.db")

	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.EnableJournal()
	entity, err := store.CreateEntity("retry-policy", "decision", "proj")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "three attempts, exponential backoff", "proj"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	// Stand in for an index run: the clear step is what destroys hand-writes.
	indexer, err := knowledge.NewIndexer(store, "proj", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := indexer.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if gone, err := store.GetEntityByNameAndType("retry-policy", "decision", "proj"); err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	} else if gone != nil {
		t.Log("indexing no longer clears hand-written entities; this test's premise has changed")
	}

	var out bytes.Buffer
	stats, err := restoreHandWrites(store, dbPath, kglib.JournalPath(dbPath), &out)
	if err != nil {
		t.Fatalf("restoreHandWrites: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("replay failed: %v", stats.Errors)
	}

	restored, err := store.GetEntityByNameAndType("retry-policy", "decision", "proj")
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if restored == nil {
		t.Fatal("hand-written entity was not restored after indexing cleared the project")
	}
	observations, err := store.GetObservations(restored.ID, "proj")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 || observations[0].Content != "three attempts, exponential backoff" {
		t.Errorf("observations = %+v, want the hand-written note restored", observations)
	}
	store.Close()
}
