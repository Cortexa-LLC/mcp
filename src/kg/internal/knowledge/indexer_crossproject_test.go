package knowledge

import (
	"fmt"
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

// rawInsertEntity writes an entity directly, the way an indexer bulk path does,
// so a test can place a project-B-only neighbour with a chosen id.
func rawInsertEntity(t *testing.T, store *Store, projectID, id, name, typ string) {
	t.Helper()
	r, err := store.QueryParams(`
		CREATE (e:Entity {pk: $pk, id: $id, name: $name, type: $type, project_id: $project_id,
		                  created_at: timestamp('2020-01-01'), updated_at: timestamp('2020-01-01')})
	`, map[string]any{"pk": PK(projectID, id), "id": id, "name": name, "type": typ, "project_id": projectID})
	if err != nil {
		t.Fatalf("rawInsertEntity %s/%s: %v", projectID, id, err)
	}
	r.Close()
}

// rawCreateCrossEdge creates an edge between two nodes addressed by pk, with no
// project guard — simulating exactly the future/legacy buggy write path the
// reviewer warns about: a cross-project edge that the project-scoped reads must
// refuse to surface or delete.
func rawCreateCrossEdge(t *testing.T, store *Store, fromProject, fromID, toProject, toID, relType string) {
	t.Helper()
	r, err := store.QueryParams(fmt.Sprintf(`
		MATCH (from:Entity {pk: $from_pk}), (to:Entity {pk: $to_pk})
		CREATE (from)-[:%s]->(to)
	`, relType), map[string]any{"from_pk": PK(fromProject, fromID), "to_pk": PK(toProject, toID)})
	if err != nil {
		t.Fatalf("rawCreateCrossEdge: %v", err)
	}
	r.Close()
}

// countCrossEdge returns how many relType edges run from fromProject's fromID to
// toProject's toID, addressed by pk so it sees a cross-project edge the scoped
// APIs hide.
func countCrossEdge(t *testing.T, store *Store, fromProject, fromID, toProject, toID, relType string) int {
	t.Helper()
	r, err := store.QueryParams(fmt.Sprintf(`
		MATCH (from:Entity {pk: $from_pk})-[e:%s]->(to:Entity {pk: $to_pk})
		RETURN count(e)
	`, relType), map[string]any{"from_pk": PK(fromProject, fromID), "to_pk": PK(toProject, toID)})
	if err != nil {
		t.Fatalf("countCrossEdge: %v", err)
	}
	defer r.Close()
	if !r.HasNext() {
		return 0
	}
	row, err := r.Next()
	if err != nil {
		t.Fatalf("countCrossEdge next: %v", err)
	}
	v, _ := row.GetValue(0)
	n, _ := v.(int64)
	return int(n)
}

// The read/delete siblings of CreateRelation (GetRelations, TraverseRelations,
// DeleteRelation) pinned only `from` by project_id, leaving the `to`/neighbour
// endpoint project-blind. That is masked while every edge is same-project, but a
// single cross-project edge (constructed here directly) must not leak through a
// project-scoped read, nor be deleted by a project-scoped delete.
func TestRelationReadsAndDeletesAreProjectScopedOnBothEnds(t *testing.T) {
	const projA, projB = "rel-project-a", "rel-project-b"

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

	indexInto(t, store, projA, dirA) // both get file:common.go and import:logging
	indexInto(t, store, projB, dirB)

	const shared = "file:common.go"

	// A project-B-only neighbour, reachable only by a cross-project edge from A.
	rawInsertEntity(t, store, projB, "topic:bneighbor", "bneighbor", "topic")
	rawCreateCrossEdge(t, store, projA, shared, projB, "topic:bneighbor", RelRelatesTo)

	// --- GetRelations: must not surface A→B.bneighbor ------------------------
	relsA, err := store.GetRelations(shared, projA)
	if err != nil {
		t.Fatalf("GetRelations(A): %v", err)
	}
	for _, r := range relsA {
		if r.ToID == "topic:bneighbor" {
			t.Errorf("GetRelations(A) returned project B's neighbour %q (project-blind `to`)", r.ToID)
		}
	}

	// --- TraverseRelations: must not cross into B ---------------------------
	neighbours, err := store.TraverseRelations(shared, RelRelatesTo, projA)
	if err != nil {
		t.Fatalf("TraverseRelations(A): %v", err)
	}
	for _, n := range neighbours {
		if n.ProjectID != projA {
			t.Errorf("TraverseRelations(A) crossed into project %q via neighbour %q", n.ProjectID, n.ID)
		}
	}

	// --- DeleteRelation: a project-A delete must not remove an edge whose far
	// end is project B's node. Give A both a same-project edge and a cross edge
	// to B's import:logging (same id in both projects), then delete in A. ------
	if err := store.CreateRelation(shared, "import:logging", RelDependsOn, projA); err != nil {
		t.Fatalf("CreateRelation A→A: %v", err)
	}
	rawCreateCrossEdge(t, store, projA, shared, projB, "import:logging", RelDependsOn)
	if before := countCrossEdge(t, store, projA, shared, projB, "import:logging", RelDependsOn); before != 1 {
		t.Fatalf("setup: expected 1 cross edge before delete, got %d", before)
	}

	if err := store.DeleteRelation(shared, "import:logging", RelDependsOn, projA); err != nil {
		t.Fatalf("DeleteRelation(A): %v", err)
	}

	// The cross edge terminates at project B's node, so a project-A delete must
	// leave it intact (pre-fix it was swept because `to` was project-blind).
	if after := countCrossEdge(t, store, projA, shared, projB, "import:logging", RelDependsOn); after != 1 {
		t.Errorf("DeleteRelation(A) touched the edge into project B's node: cross edges after = %d, want 1", after)
	}
	// A's own same-project edge was the intended target and should be gone.
	if n := countCrossEdge(t, store, projA, shared, projA, "import:logging", RelDependsOn); n != 0 {
		t.Errorf("DeleteRelation(A) did not remove A's own edge: count = %d, want 0", n)
	}
}
