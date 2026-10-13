package knowledge

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCommonProject creates a one-file Go project whose source-derived IDs are
// identical regardless of which directory it lives in: "file:common.go" and
// "function:common.go:Shared". Two such projects indexed into one database share
// every entity ID, which is exactly the collision the project-namespaced pk must
// keep from fusing them.
func writeCommonProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src := "package common\n\nimport \"logging\"\n\nfunc Shared() { logging.Init() }\n"
	if err := os.WriteFile(filepath.Join(dir, "common.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
}

// indexInto runs a fresh indexer for projectID over srcDir against store.
func indexInto(t *testing.T, store *Store, projectID, srcDir string) *IndexStats {
	t.Helper()
	idx, err := NewIndexer(store, projectID, srcDir)
	if err != nil {
		t.Fatalf("NewIndexer(%s): %v", projectID, err)
	}
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index(%s): %v", projectID, err)
	}
	return stats
}

// Two projects sharing one database file and sharing entity IDs must each keep
// their own entities. Before the pk fix, the second project's write collided
// with the first's identically-id'd (but project-scoped-clear-surviving) row and
// was silently dropped; the project under the live project_id then under-counted.
func TestIndexCrossProjectSharedIDsRetainsBoth(t *testing.T) {
	const projA, projB = "project-a", "project-b"

	tmp := t.TempDir()
	dirA := filepath.Join(tmp, "a")
	dirB := filepath.Join(tmp, "b")
	writeCommonProject(t, dirA)
	writeCommonProject(t, dirB)

	store, err := OpenStore(filepath.Join(tmp, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// Index A, then B — B shares every id A just wrote.
	indexInto(t, store, projA, dirA)
	indexInto(t, store, projB, dirB)

	countA, err := store.CountEntities(projA)
	if err != nil {
		t.Fatalf("CountEntities(A): %v", err)
	}
	countB, err := store.CountEntities(projB)
	if err != nil {
		t.Fatalf("CountEntities(B): %v", err)
	}
	if countA == 0 || countB == 0 {
		t.Fatalf("a project lost all its entities: A=%d B=%d", countA, countB)
	}
	if countA != countB {
		t.Errorf("projects sharing ids got different counts: A=%d B=%d (cross-project drop)", countA, countB)
	}

	// The shared ids resolve to each project's OWN row, not the other's.
	for _, proj := range []string{projA, projB} {
		got, err := store.GetEntity("file:common.go", proj)
		if err != nil {
			t.Fatalf("GetEntity(file:common.go, %s): %v", proj, err)
		}
		if got.ProjectID != proj {
			t.Errorf("file:common.go under %s resolved to project_id %q", proj, got.ProjectID)
		}
		if _, err := store.GetEntity("import:logging", proj); err != nil {
			t.Errorf("import:logging missing for %s: %v", proj, err)
		}
	}

	// Re-indexing A must not disturb B — the selective clear is project-scoped and
	// the pk keeps B's identically-id'd rows out of A's COPY collision set.
	indexInto(t, store, projA, dirA)
	reCountB, err := store.CountEntities(projB)
	if err != nil {
		t.Fatalf("CountEntities(B) after re-index A: %v", err)
	}
	if reCountB != countB {
		t.Errorf("re-indexing A changed B's entity count: before=%d after=%d", countB, reCountB)
	}

	// Relations must not bleed across projects: A's edges stay A's.
	relsB, err := store.GetRelations("file:common.go", projB)
	if err != nil {
		t.Fatalf("GetRelations(file:common.go, B): %v", err)
	}
	for _, r := range relsB {
		// Every endpoint GetRelations returns is addressed by local id; confirm it
		// resolves within B rather than pointing at A's graph.
		if _, err := store.GetEntity(r.ToID, projB); err != nil {
			t.Errorf("relation from B points at %q which is not in B: %v", r.ToID, err)
		}
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what was
// printed. The indexer reports fallbacks and collisions via fmt.Printf to stdout,
// so this is how a test sees whether the warning path was taken.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

// Re-indexing an already-indexed project must be clean: the pk makes the COPY
// idempotent (the selective clear empties this project's code-derived rows
// first, and no other project's rows share a pk), so neither the duplicate-key
// warning nor the COPY-unavailable fallback note should appear.
func TestReindexEmitsNoDuplicateKeyWarnings(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "src")
	writeCommonProject(t, dir)

	store, err := OpenStore(filepath.Join(tmp, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	indexInto(t, store, "proj", dir) // first pass primes the graph

	out := captureStdout(t, func() {
		indexInto(t, store, "proj", dir) // the re-index under test
	})

	for _, bad := range []string{"duplicated primary key", "COPY FROM JSON unavailable"} {
		if strings.Contains(out, bad) {
			t.Errorf("re-index emitted %q; output:\n%s", bad, out)
		}
	}
}

// Observation has no project_id column, so an observation read that filters only
// on entity_id pulls in rows from every project sharing that (code-derived)
// entity id. A per-project search must return only that project's observations.
// This exercises the search observation-fetch path (batchGetObservations), which
// before the fix matched o.entity_id with no project scope.
func TestSearchObservationsDoNotBleedAcrossProjects(t *testing.T) {
	const projA, projB = "obs-project-a", "obs-project-b"

	tmp := t.TempDir()
	dirA := filepath.Join(tmp, "a")
	dirB := filepath.Join(tmp, "b")
	writeCommonProject(t, dirA)
	writeCommonProject(t, dirB)

	store, err := OpenStore(filepath.Join(tmp, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	indexInto(t, store, projA, dirA)
	indexInto(t, store, projB, dirB)

	// Hand-write an observation onto the SAME shared entity id in each project.
	const idShared = "file:common.go"
	if _, err := store.CreateObservation(idShared, "A-only note", projA); err != nil {
		t.Fatalf("CreateObservation A: %v", err)
	}
	if _, err := store.CreateObservation(idShared, "B-only note", projB); err != nil {
		t.Fatalf("CreateObservation B: %v", err)
	}

	// A search scoped to project A, matching the shared entity by name, must
	// surface only A's observation — never B's.
	results, err := store.KeywordSearch(projA, "common", 10)
	if err != nil {
		t.Fatalf("KeywordSearch(A): %v", err)
	}
	var checked bool
	for _, r := range results {
		if r.Entity.ID != idShared {
			continue
		}
		checked = true
		for _, o := range r.Observations {
			if o.Content == "B-only note" {
				t.Errorf("project A search returned project B's observation %q (cross-project bleed)", o.Content)
			}
		}
		var sawA bool
		for _, o := range r.Observations {
			if o.Content == "A-only note" {
				sawA = true
			}
		}
		if !sawA {
			t.Errorf("project A search missing its own observation; got %+v", r.Observations)
		}
	}
	if !checked {
		t.Fatalf("search did not return the shared entity %q, so the bleed assertion never ran", idShared)
	}
}
