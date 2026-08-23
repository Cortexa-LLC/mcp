package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// countRelationsOfType returns how many edges of relType this project holds.
// It reads the table by name, so it can see tables the current AllowedRelTypes
// no longer lists.
func countRelationsOfType(t *testing.T, store *Store, relType, projectID string) int {
	t.Helper()
	q := fmt.Sprintf(
		`MATCH (a:Entity {project_id: $project_id})-[r:%s]->(b:Entity {project_id: $project_id}) RETURN count(r)`,
		relType,
	)
	result, err := store.QueryParams(q, map[string]any{"project_id": projectID})
	if err != nil {
		t.Fatalf("count %s: %v", relType, err)
	}
	defer result.Close()
	if !result.HasNext() {
		return 0
	}
	row, err := result.Next()
	if err != nil {
		t.Fatalf("count %s: %v", relType, err)
	}
	v, _ := row.GetValue(0)
	switch n := v.(type) {
	case int64:
		return int(n)
	case uint64:
		return int(n)
	}
	return 0
}

// retiredRelType names a relation table that a previous SchemaConfig created and
// AllowedRelTypes no longer lists — the case a sweep driven by the compiled-in
// list cannot see.
const retiredRelType = "LEGACY_DEPENDS"

// seedRetiredRelation creates the retired relation table and one edge of that
// type between two entities of the project, returning the endpoint IDs. It
// checks the edge is really there, so a fixture that cannot express the failure
// fails loudly instead of yielding a test that always passes.
func seedRetiredRelation(t *testing.T, store *Store, projectID string) (fromID, toID string) {
	t.Helper()
	if _, err := store.Query(fmt.Sprintf("CREATE REL TABLE IF NOT EXISTS %s(FROM Entity TO Entity)", retiredRelType)); err != nil {
		t.Fatalf("create retired rel table: %v", err)
	}
	from, err := store.CreateEntity("legacy-from", EntityTypeTopic, projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	to, err := store.CreateEntity("legacy-to", EntityTypeTopic, projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	res, err := store.QueryParams(
		fmt.Sprintf(`MATCH (a:Entity {id: $from}), (b:Entity {id: $to}) CREATE (a)-[:%s]->(b)`, retiredRelType),
		map[string]any{"from": from.ID, "to": to.ID})
	if err != nil {
		t.Fatalf("create retired edge: %v", err)
	}
	res.Close()

	if got := countRelationsOfType(t, store, retiredRelType, projectID); got != 1 {
		t.Fatalf("fixture did not create the retired edge: count = %d, want 1", got)
	}
	return from.ID, to.ID
}

// The relation sweep that a re-index starts with must clear every relation the
// project holds, including ones in a table a previous version of the schema
// created and this one no longer lists. Those edges have no source behind them,
// and a sweep driven by the list compiled into this binary cannot see them.
//
// The sweep is exercised on its own here, with the entities left in place, so
// that the assertion is about the sweep and not about the entity deletion that
// follows it in clearProjectData.
func TestRelationSweepClearsRetiredRelationTables(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	store, err := OpenStore(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	const projectID = "test-project"
	from, to := seedRetiredRelation(t, store, projectID)

	idx, err := NewIndexer(store, projectID, tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if err := idx.deleteProjectRelations(); err != nil {
		t.Fatalf("deleteProjectRelations: %v", err)
	}

	if got := countRelationsOfType(t, store, retiredRelType, projectID); got != 0 {
		t.Errorf("%d edge(s) of retired type %s survived the relation sweep; a sweep that only knows the relation types compiled into this binary can never remove them",
			got, retiredRelType)
	}
	// The endpoints are untouched, which is what makes the result above a
	// statement about the sweep rather than about deleting nodes out from under
	// their edges.
	for _, id := range []string{from, to} {
		e, err := store.GetEntity(id, projectID)
		if err != nil {
			t.Fatalf("GetEntity: %v", err)
		}
		if e == nil {
			t.Fatalf("entity %s was deleted by the relation sweep", id)
		}
	}
}

// A re-index rebuilds the graph from the source tree, so what the source tree
// no longer supports must not survive it — including edges in a relation table
// that a previous version of the schema created and this one no longer lists.
// Those edges have no source behind them and nothing else will ever remove
// them, so a graph that keeps them disagrees with the tree permanently.
//
// Note this test cannot fail on the sweep alone: clearProjectData follows the
// sweep with a DETACH DELETE of the project's entities, which takes these edges
// down with their endpoints. It states the end-to-end invariant;
// TestRelationSweepClearsRetiredRelationTables is the one that holds the sweep
// itself to it.
func TestReindexRemovesRelationsOfRetiredTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.go"), []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	const projectID = "test-project"
	seedRetiredRelation(t, store, projectID)

	idx, err := NewIndexer(store, projectID, srcDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if got := countRelationsOfType(t, store, retiredRelType, projectID); got != 0 {
		t.Errorf("%d edge(s) of retired type %s survived a full re-index; nothing in the source tree supports them and no later run will remove them",
			got, retiredRelType)
	}
}
