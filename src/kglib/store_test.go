package kglib

import (
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
