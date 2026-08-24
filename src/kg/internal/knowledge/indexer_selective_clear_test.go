package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

// selectiveClearFixture indexes a one-file Go project and then hangs
// hand-written knowledge off the graph the way agents and `kg add` do:
// entities with UUID IDs, an observation, a hand↔hand relation, and a
// hand→code relation onto a source-derived entity the next re-index will
// rebuild. It returns the store, the indexer, and the two hand entities.
func selectiveClearFixture(t *testing.T) (store *Store, idx *Indexer, decision, followup *Entity) {
	t.Helper()

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.go"), []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	store, err := OpenStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	idx, err = NewIndexer(store, selectiveClearProject, srcDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if _, err := store.GetEntity(codeEntityID, selectiveClearProject); err != nil {
		t.Fatalf("fixture did not index the code entity %s: %v", codeEntityID, err)
	}

	decision, err = store.CreateEntity("selective clear decision", "decision", selectiveClearProject)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(decision.ID, "re-index must preserve this note", selectiveClearProject); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	followup, err = store.CreateEntity("selective clear follow-up", "topic", selectiveClearProject)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := store.CreateRelation(decision.ID, followup.ID, RelRelatesTo, selectiveClearProject); err != nil {
		t.Fatalf("CreateRelation hand→hand: %v", err)
	}
	if err := store.CreateRelation(decision.ID, codeEntityID, RelDocuments, selectiveClearProject); err != nil {
		t.Fatalf("CreateRelation hand→code: %v", err)
	}
	return store, idx, decision, followup
}

const (
	selectiveClearProject = "test-project"
	// The ID the tree-sitter indexer derives for func F in a.go — stable by
	// construction, so the second index recreates the entity under it.
	codeEntityID = "function:a.go:F"
)

// A re-index rebuilds what the source tree can regenerate and must not touch
// what it cannot: hand-written entities (UUID IDs — MCP add_entity, `kg add`),
// their observations, and the relations between them. A relation from a hand
// entity onto a code entity loses its endpoint in the clear and goes with it;
// the hand entity itself stays.
func TestReindexPreservesHandWrittenKnowledge(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	store, idx, decision, followup := selectiveClearFixture(t)

	if _, err := idx.Index(); err != nil {
		t.Fatalf("re-Index: %v", err)
	}

	// Hand-written knowledge survived in place.
	got, err := store.GetEntity(decision.ID, selectiveClearProject)
	if err != nil {
		t.Fatalf("hand-written entity did not survive the re-index: %v", err)
	}
	if got.Name != decision.Name {
		t.Errorf("surviving entity name = %q, want %q", got.Name, decision.Name)
	}
	obs, err := store.GetObservations(decision.ID, selectiveClearProject)
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(obs) != 1 {
		t.Errorf("hand-written observations after re-index = %d, want 1", len(obs))
	}
	if got := countRelationsOfType(t, store, RelRelatesTo, selectiveClearProject); got != 1 {
		t.Errorf("hand↔hand relations after re-index = %d, want 1", got)
	}
	if _, err := store.GetEntity(followup.ID, selectiveClearProject); err != nil {
		t.Errorf("second hand-written entity did not survive: %v", err)
	}

	// The code side was rebuilt from the tree.
	if _, err := store.GetEntity(codeEntityID, selectiveClearProject); err != nil {
		t.Errorf("code entity was not rebuilt: %v", err)
	}

	// The hand→code relation dangled when the clear removed its code endpoint;
	// it is deleted, not left pointing at nothing, and the hand entity stays.
	if got := countRelationsOfType(t, store, RelDocuments, selectiveClearProject); got != 0 {
		t.Errorf("hand→code relations after re-index = %d, want 0 (dangling edge must go with its endpoint)", got)
	}
}

// --wipe restores the historical semantics: everything belonging to the
// project goes, hand-written knowledge included, and the tree alone rebuilds
// the graph.
func TestReindexWipeClearsHandWrittenKnowledge(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	store, idx, decision, _ := selectiveClearFixture(t)

	idx.SetWipe(true)
	if _, err := idx.Index(); err != nil {
		t.Fatalf("wipe re-Index: %v", err)
	}

	if _, err := store.GetEntity(decision.ID, selectiveClearProject); err == nil {
		t.Error("hand-written entity survived a --wipe re-index")
	}
	if got := countRelationsOfType(t, store, RelRelatesTo, selectiveClearProject); got != 0 {
		t.Errorf("hand↔hand relations after wipe = %d, want 0", got)
	}

	// The code side is still rebuilt.
	if _, err := store.GetEntity(codeEntityID, selectiveClearProject); err != nil {
		t.Errorf("code entity was not rebuilt after wipe: %v", err)
	}
}
