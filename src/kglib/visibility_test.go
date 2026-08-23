package kglib

import (
	"path/filepath"
	"testing"
	"time"
)

// Empty visibility must never be treated as private. Hand-written entities have
// no source-language visibility, and neither do rows written before the column
// existed — demoting either would be a silent regression in search quality.
func TestEmptyVisibilityIsNotPrivate(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	entity, err := store.CreateEntity("retry-policy", "decision", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if entity.Visibility != "" {
		t.Errorf("hand-written entity has visibility %q, want empty", entity.Visibility)
	}

	fetched, err := store.GetEntity(entity.ID, "p")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if fetched.Visibility != "" {
		t.Errorf("round-tripped visibility = %q, want empty", fetched.Visibility)
	}

	results := []*SearchResult{{Entity: fetched}}
	if visibilityRank(results[0]) != 0 {
		t.Error("an entity with no visibility ranks as private")
	}
}

func TestVisibilityRoundTrips(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := OpenStore(filepath.Join(tmpDir, "test.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// Write directly, the way the indexer's bulk path does.
	result, err := store.QueryParams(`
		CREATE (e:Entity {
			id: $id, name: $name, type: $type, project_id: $project_id,
			created_at: $ts, updated_at: $ts, visibility: $visibility
		})
	`, map[string]any{
		"id": "function:main.go:helper", "name": "helper", "type": "function",
		"project_id": "p", "ts": time.Now().UTC(), "visibility": VisibilityPrivate,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	result.Close()

	entity, err := store.GetEntityByNameAndType("helper", "function", "p")
	if err != nil || entity == nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if entity.Visibility != VisibilityPrivate {
		t.Errorf("visibility = %q, want %q", entity.Visibility, VisibilityPrivate)
	}

	// Every projection has to carry the column, not just the one that was
	// convenient to test — they share entityColumns precisely so they cannot drift.
	listed, err := store.ListEntities("p", "function")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(listed) != 1 || listed[0].Visibility != VisibilityPrivate {
		t.Errorf("ListEntities lost visibility: %+v", listed)
	}

	found, err := store.KeywordSearch("p", "helper", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(found) != 1 || found[0].Entity.Visibility != VisibilityPrivate {
		t.Errorf("KeywordSearch lost visibility: %+v", found)
	}
}

// Unexported symbols are indexed, not dropped — they just sink below their
// equally-scoring peers.
func TestRankByVisibilityDemotesPrivateWithoutDropping(t *testing.T) {
	results := []*SearchResult{
		{Entity: &Entity{Name: "unexportedHelper", Visibility: VisibilityPrivate}, Score: 1.0},
		{Entity: &Entity{Name: "ExportedFunc", Visibility: VisibilityPublic}, Score: 1.0},
		{Entity: &Entity{Name: "a-hand-written-note", Visibility: ""}, Score: 1.0},
	}
	rankResults(results)

	if len(results) != 3 {
		t.Fatalf("ranking dropped results: %d left", len(results))
	}
	if results[len(results)-1].Entity.Name != "unexportedHelper" {
		t.Errorf("order = %s, %s, %s; want the unexported symbol last",
			results[0].Entity.Name, results[1].Entity.Name, results[2].Entity.Name)
	}
	// Hand-written knowledge must not be demoted alongside private symbols.
	if results[0].Entity.Name != "ExportedFunc" && results[0].Entity.Name != "a-hand-written-note" {
		t.Errorf("first result = %s; empty visibility should rank with public", results[0].Entity.Name)
	}
}

// A higher-scoring unexported symbol still beats a lower-scoring exported one:
// visibility breaks ties, it does not override relevance.
func TestVisibilityOnlyBreaksTies(t *testing.T) {
	results := []*SearchResult{
		{Entity: &Entity{Name: "ExportedButWeak", Visibility: VisibilityPublic}, Score: 0.2},
		{Entity: &Entity{Name: "unexportedButStrong", Visibility: VisibilityPrivate}, Score: 0.9},
	}
	rankResults(results)

	if results[0].Entity.Name != "unexportedButStrong" {
		t.Errorf("visibility overrode relevance: got %s first, want the higher-scoring match",
			results[0].Entity.Name)
	}
}

// Read-only opens never run initSchema, so a graph written before the
// visibility column existed will not have gained it. Kuzu rejects a query that
// names a missing property outright — "Cannot find property visibility for e" —
// rather than returning null, so an unconditional projection breaks every read
// path on any graph that has not been re-indexed since the upgrade: search,
// federation, remote layers, and the MCP server.
//
// Simulated by dropping the column, since this codebase can no longer create a
// database without it.
func TestReadPathsWorkWithoutVisibilityColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	entity, err := store.CreateEntity("Run", "function", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "does the thing", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	result, err := store.Query("ALTER TABLE Entity DROP visibility")
	if err != nil {
		t.Fatalf("could not drop the column to simulate an old database: %v", err)
	}
	result.Close()
	store.Close()

	// Read-only, so nothing migrates it back.
	legacy, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenStoreReadOnly: %v", err)
	}
	defer legacy.Close()

	if legacy.hasVisibility {
		t.Error("a database without the column reports having it")
	}

	found, err := legacy.KeywordSearch("p", "Run", 10)
	if err != nil {
		t.Fatalf("KeywordSearch against a pre-visibility database: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("search returned nothing")
	}
	if found[0].Entity.Visibility != "" {
		t.Errorf("visibility = %q, want empty", found[0].Entity.Visibility)
	}

	if _, err := legacy.ListEntities("p", ""); err != nil {
		t.Errorf("ListEntities: %v", err)
	}
	if _, err := legacy.GetEntity(entity.ID, "p"); err != nil {
		t.Errorf("GetEntity: %v", err)
	}
	if _, err := legacy.GetEntityByNameAndType("Run", "function", "p"); err != nil {
		t.Errorf("GetEntityByNameAndType: %v", err)
	}
	if _, err := legacy.GetEntityByName("Run", "p"); err != nil {
		t.Errorf("GetEntityByName: %v", err)
	}
}

// And a write-mode open migrates it, so the column arrives without the user
// doing anything.
func TestWriteOpenAddsVisibilityColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	result, err := store.Query("ALTER TABLE Entity DROP visibility")
	if err != nil {
		t.Fatalf("drop column: %v", err)
	}
	result.Close()
	store.Close()

	migrated, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer migrated.Close()

	if !migrated.hasVisibility {
		t.Error("a write-mode open did not migrate the visibility column back")
	}
}
