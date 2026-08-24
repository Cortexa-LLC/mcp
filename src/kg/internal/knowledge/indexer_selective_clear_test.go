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

// Upgrade path: databases indexed before PDF entities carried source-derived
// ids hold UUID file entities the selective clear would otherwise preserve
// forever, duplicated by the file:<relPath> entity each re-index creates. The
// post-index cleanup removes exactly the shadowed ones; a hand-written
// file-typed entity about a PDF the run did not index stays protected.
func TestReindexRemovesLegacyUUIDPDFEntities(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pdfBytes := buildMinimalPDF("Selective clear legacy PDF upgrade-path test document content.")
	if err := os.WriteFile(filepath.Join(srcDir, "doc.pdf"), pdfBytes, 0o644); err != nil {
		t.Fatalf("write PDF: %v", err)
	}
	// Uppercase extension: the walker dispatches on the lowercased extension
	// but keeps the path's case, so the cleanup's name filter must be
	// case-insensitive too or this legacy row escapes forever.
	if err := os.WriteFile(filepath.Join(srcDir, "REPORT.PDF"), pdfBytes, 0o644); err != nil {
		t.Fatalf("write PDF: %v", err)
	}

	store, err := OpenStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Simulate the pre-upgrade state: the old PDF indexer's output was a
	// UUID-id file entity named by relPath, with chunk observations.
	legacy, err := store.CreateEntity("doc.pdf", EntityTypeFile, selectiveClearProject)
	if err != nil {
		t.Fatalf("CreateEntity legacy: %v", err)
	}
	if _, err := store.CreateObservation(legacy.ID, "stale chunk from the old indexer", selectiveClearProject); err != nil {
		t.Fatalf("CreateObservation legacy: %v", err)
	}
	legacyUpper, err := store.CreateEntity("REPORT.PDF", EntityTypeFile, selectiveClearProject)
	if err != nil {
		t.Fatalf("CreateEntity legacy uppercase: %v", err)
	}
	// A hand-written file-typed note about a PDF that is NOT in the tree —
	// no indexed counterpart, so it must survive.
	handNote, err := store.CreateEntity("design-notes.pdf", EntityTypeFile, selectiveClearProject)
	if err != nil {
		t.Fatalf("CreateEntity hand note: %v", err)
	}

	idx, err := NewIndexer(store, selectiveClearProject, srcDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// The indexed counterpart exists under its source-derived id…
	if _, err := store.GetEntity("file:doc.pdf", selectiveClearProject); err != nil {
		t.Fatalf("indexed PDF entity missing (extraction fallback failed?): %v", err)
	}
	// …the shadowed legacy duplicate and its observation are gone…
	if _, err := store.GetEntity(legacy.ID, selectiveClearProject); err == nil {
		t.Error("legacy UUID PDF entity survived the re-index; it now duplicates file:doc.pdf forever")
	}
	if obs, err := store.GetObservations(legacy.ID, selectiveClearProject); err == nil && len(obs) != 0 {
		t.Errorf("legacy PDF observations survived: %d, want 0", len(obs))
	}
	if _, err := store.GetEntity("file:REPORT.PDF", selectiveClearProject); err != nil {
		t.Fatalf("indexed uppercase PDF entity missing: %v", err)
	}
	if _, err := store.GetEntity(legacyUpper.ID, selectiveClearProject); err == nil {
		t.Error("legacy UUID PDF entity with uppercase extension survived the re-index")
	}
	// …and the unshadowed hand-written note is untouched.
	if _, err := store.GetEntity(handNote.ID, selectiveClearProject); err != nil {
		t.Errorf("hand-written file-typed entity without an indexed counterpart was deleted: %v", err)
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
