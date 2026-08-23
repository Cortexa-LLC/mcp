package kglib

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadOnlyModeDoesNotBlockFileWrites is the evidence behind the filesystem
// half of writeMutatingKeywords, kept executable rather than written down as a
// claim in a comment.
//
// The tempting assumption is that opening the database read-only makes the
// query_graph tool safe on its own, and that the Cypher guard is belt-and-braces.
// It is not: Kuzu's read-only flag protects the database, not the host. COPY ... TO
// runs happily on a read-only handle and writes wherever the caller points it.
//
// If this test ever fails, Kuzu's behaviour has changed for the better -- confirm
// that, then update the comment on writeMutatingKeywords. Do not use it as a reason
// to shorten the list; the deny-list is only one of the guard's two checks.
func TestReadOnlyModeDoesNotBlockFileWrites(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "probe.db")
	target := filepath.Join(tmpDir, "written-by-a-read-only-handle.csv")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := store.CreateEntity("probe-ent", "Function", "probe-proj"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	_ = store.Close()

	payload := "COPY (MATCH (e:Entity) RETURN e.name) TO '" + target + "'"

	// The property that actually protects users.
	if err := IsReadOnlyCypher(payload); err == nil {
		t.Fatalf("IsReadOnlyCypher accepted %q, which writes an arbitrary file", payload)
	}

	// The reason it has to: the engine underneath will not stop it.
	ro, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenStoreReadOnly: %v", err)
	}
	defer func() { _ = ro.Close() }()

	res, qerr := ro.Query(payload)
	if res != nil {
		res.Close()
	}
	if _, serr := os.Stat(target); serr != nil {
		t.Errorf("expected Kuzu read-only mode to permit COPY ... TO (query err: %v, stat: %v).\n"+
			"If Kuzu now blocks filesystem writes from read-only handles this is good news, "+
			"but the comment on writeMutatingKeywords must be corrected -- and the guard kept.", qerr, serr)
	}
}
