package kglib

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer store.Close()

	if store.db == nil {
		t.Fatal("Database not initialized")
	}
	if store.conn == nil {
		t.Fatal("Connection not initialized")
	}
	if store.path != dbPath {
		t.Errorf("Expected path %s, got %s", dbPath, store.path)
	}
}

func TestStoreSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer store.Close()

	// Verify Entity table exists by trying to query it
	result, err := store.Query("MATCH (e:Entity) RETURN count(e)")
	if err != nil {
		t.Fatalf("Entity table not created: %v", err)
	}
	defer result.Close()

	// Verify Observation table exists
	result, err = store.Query("MATCH (o:Observation) RETURN count(o)")
	if err != nil {
		t.Fatalf("Observation table not created: %v", err)
	}
	defer result.Close()
}

func TestStoreClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	err = store.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

// TestStoreConcurrentAccess verifies that concurrent goroutines can call Store
// methods simultaneously without data races or panics.  Run with -race to
// exercise the mutex path.
func TestStoreConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := OpenStore(filepath.Join(tmpDir, "concurrent.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	const goroutines = 10
	const opsEach = 5

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsEach; j++ {
				e, err := store.CreateEntity("concurrent-entity", "test", "proj-concurrent")
				if err != nil {
					t.Errorf("CreateEntity: %v", err)
					return
				}
				_, err = store.GetEntity(e.ID, "proj-concurrent")
				if err != nil {
					t.Errorf("GetEntity: %v", err)
					return
				}
				_, err = store.ListEntities("proj-concurrent", "")
				if err != nil {
					t.Errorf("ListEntities: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestStoreCreateDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nested", "dir", "test.db")

	// Parent directory should be created automatically
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer store.Close()

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(dbPath)); os.IsNotExist(err) {
		t.Error("Parent directory was not created")
	}
}

// TestOpenStoreReadOnly_ManySimultaneous guards the limit that federated
// queries depend on: one read-only open per layer, all held at once.
//
// Kuzu's default MaxDbSize is unlimited, which reserves ~8 TiB of virtual
// address space per database; on a 47-bit address space the 16th concurrent
// open used to fail with a bare "status 1" that surfaced as a bogus "database
// is locked" error. 20 exceeds that old ceiling of 15.
func TestOpenStoreReadOnly_ManySimultaneous(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const n = 20
	tmpDir := t.TempDir()

	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("layer-%02d.db", i))
		store, err := OpenStore(dbPath, testSchemaConfig())
		if err != nil {
			t.Fatalf("OpenStore(%s): %v", dbPath, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close(%s): %v", dbPath, err)
		}
		paths = append(paths, dbPath)
	}

	open := make([]*Store, 0, n)
	defer func() {
		for _, s := range open {
			s.Close()
		}
	}()

	for i, p := range paths {
		s, err := OpenStoreReadOnly(p)
		if err != nil {
			t.Fatalf("OpenStoreReadOnly #%d of %d (%s): %v", i+1, n, p, err)
		}
		open = append(open, s)
	}

	if len(open) != n {
		t.Errorf("held %d simultaneous read-only opens, want %d", len(open), n)
	}
}

// TestCountRelations_ReadOnlyStore is the regression test for `kg stats`
// reporting "Relations: 0" on every scope while the relations were in fact
// present. CountRelations used to iterate s.allowedRelTypes, which initSchema
// only populates on read-write opens — so on a read-only Store the loop body
// never ran and the count was always zero. It now reads the relation tables
// from the catalog, which works in both modes.
func TestCountRelations_ReadOnlyStore(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const projectID = "test-project"
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "counts.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	a, err := store.CreateEntity("alpha", "file", projectID)
	if err != nil {
		t.Fatalf("CreateEntity alpha: %v", err)
	}
	b, err := store.CreateEntity("beta", "function", projectID)
	if err != nil {
		t.Fatalf("CreateEntity beta: %v", err)
	}
	c, err := store.CreateEntity("gamma", "type", projectID)
	if err != nil {
		t.Fatalf("CreateEntity gamma: %v", err)
	}

	// Three relations spanning two different relation tables, so the count has
	// to aggregate across tables rather than read just one.
	for _, rel := range []struct{ from, to, relType string }{
		{a.ID, b.ID, "CONTAINS"},
		{a.ID, c.ID, "CONTAINS"},
		{b.ID, c.ID, "CALLS"},
	} {
		if err := store.CreateRelation(rel.from, rel.to, rel.relType, projectID); err != nil {
			t.Fatalf("CreateRelation %s: %v", rel.relType, err)
		}
	}

	// The write-mode count is the baseline — this path always worked.
	rw, err := store.CountRelations(projectID)
	if err != nil {
		t.Fatalf("CountRelations (read-write): %v", err)
	}
	if rw != 3 {
		t.Errorf("CountRelations (read-write) = %d, want 3", rw)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The regression: same database, opened read-only as `kg stats` does.
	ro, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenStoreReadOnly: %v", err)
	}
	defer ro.Close()

	if len(ro.allowedRelTypes) != 0 {
		t.Fatalf("expected a read-only store to have no configured rel types, got %v; "+
			"this test no longer covers the original bug", ro.allowedRelTypes)
	}

	got, err := ro.CountRelations(projectID)
	if err != nil {
		t.Fatalf("CountRelations (read-only): %v", err)
	}
	if got != 3 {
		t.Errorf("CountRelations (read-only) = %d, want 3", got)
	}

	// A different project must not pick up these relations.
	other, err := ro.CountRelations("some-other-project")
	if err != nil {
		t.Fatalf("CountRelations (other project): %v", err)
	}
	if other != 0 {
		t.Errorf("CountRelations for an unrelated project = %d, want 0", other)
	}
}

// TestRelationTables_ExcludesObservationEdge verifies the catalog scan returns
// the Entity→Entity tables and omits the structural HAS_OBSERVATION edge, which
// CountObservations owns.
func TestRelationTables_ExcludesObservationEdge(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	store, err := OpenStore(filepath.Join(tmpDir, "tables.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	tables, err := store.relationTables()
	if err != nil {
		t.Fatalf("relationTables: %v", err)
	}

	found := map[string]bool{}
	for _, name := range tables {
		found[name] = true
	}

	if found["HAS_OBSERVATION"] {
		t.Error("relationTables included HAS_OBSERVATION; it is counted as an observation")
	}
	if found["Entity"] || found["Observation"] {
		t.Error("relationTables included a NODE table")
	}
	for _, want := range testRelTypes {
		if !found[want] {
			t.Errorf("relationTables missing configured relation type %q", want)
		}
	}
}
