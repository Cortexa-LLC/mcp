package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cortexa-llc/mcp/kglib"
)

// Indexed data is rebuilt from source after a storage-format upgrade, not
// replayed, so the indexer must not journal. This is the load-bearing half of
// the opt-in: indexers and hand-writes share the same Store methods, and a
// blanket hook would put hundreds of thousands of derived rows into a file whose
// only purpose is to preserve what cannot be regenerated.
func TestIndexerDoesNotJournal(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	source := "package main\n\nfunc main() {}\n\nfunc helper() {}\n"
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(source), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	stats, err := idx.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if stats.EntitiesCreated == 0 {
		t.Fatal("index produced no entities, so the test proves nothing")
	}

	if _, err := os.Stat(kglib.JournalPath(dbPath)); !os.IsNotExist(err) {
		t.Errorf("indexing wrote a journal file; stat returned %v", err)
	}
}

// The other half: a store that opted in records its writes, and the record
// names the entity by (name, type) so it still resolves after a rebuild.
func TestHandWriteStoreJournals(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	store.EnableJournal()

	if _, err := store.CreateEntity("retry-policy", "topic", "test-project"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	if _, err := os.Stat(kglib.JournalPath(dbPath)); err != nil {
		t.Fatalf("hand-write was not journaled: %v", err)
	}
}
