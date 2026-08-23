package kglib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenStoreWritesFormatStamp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	stamp, err := ReadFormatStamp(dbPath)
	if err != nil {
		t.Fatalf("ReadFormatStamp: %v", err)
	}
	if stamp == nil {
		t.Fatal("write-mode open left no format stamp, so a later version check has nothing to compare")
	}
	if stamp.KuzuVersion != KuzuVersion() {
		t.Errorf("stamp records kuzu %q, want the running %q", stamp.KuzuVersion, KuzuVersion())
	}
	if stamp.StampedAt.IsZero() {
		t.Error("stamp has no timestamp")
	}
}

func TestKuzuVersionIsResolved(t *testing.T) {
	// If this ever returns "unknown", every database stamps as unknown and the
	// mismatch check silently stops working.
	if got := KuzuVersion(); got == "unknown" || got == "" {
		t.Fatalf("KuzuVersion() = %q; the format check depends on resolving it", got)
	}
}

func TestCheckFormatMissingDatabase(t *testing.T) {
	status, _, err := CheckFormat(filepath.Join(t.TempDir(), "absent.db"))
	if err != nil {
		t.Fatalf("CheckFormat: %v", err)
	}
	if status != FormatMissing {
		t.Errorf("status = %v, want %v", status, FormatMissing)
	}
}

func TestCheckFormatUnstampedDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	// A database written before stamping existed.
	if err := os.Remove(FormatStampPath(dbPath)); err != nil {
		t.Fatalf("remove stamp: %v", err)
	}

	status, _, err := CheckFormat(dbPath)
	if err != nil {
		t.Fatalf("CheckFormat: %v", err)
	}
	if status != FormatUnstamped {
		t.Errorf("status = %v, want %v — an unstamped database must not be reported as a mismatch, "+
			"or every pre-journal store would be rebuilt on first run", status, FormatUnstamped)
	}
}

// The case the whole mechanism exists for: a database written by a different
// engine build is recognised without opening it.
func TestCheckFormatDetectsMismatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	// Simulate a future go-kuzu bump by rewriting the stamp.
	stamp := FormatStamp{KuzuVersion: "v99.0.0", KGVersion: "v0.1.0"}
	data, err := json.Marshal(stamp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(FormatStampPath(dbPath), data, 0600); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	status, got, err := CheckFormat(dbPath)
	if err != nil {
		t.Fatalf("CheckFormat: %v", err)
	}
	if status != FormatMismatch {
		t.Fatalf("status = %v, want %v", status, FormatMismatch)
	}
	if got.KuzuVersion != "v99.0.0" {
		t.Errorf("stamp version = %q, want v99.0.0", got.KuzuVersion)
	}
}

// A stamp corrupted by a crash must not send a healthy database down the
// rebuild path.
func TestCheckFormatTreatsCorruptStampAsUnstamped(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	if err := os.WriteFile(FormatStampPath(dbPath), []byte(`{"kuzu_ver`), 0600); err != nil {
		t.Fatalf("write stamp: %v", err)
	}

	status, _, err := CheckFormat(dbPath)
	if err != nil {
		t.Fatalf("CheckFormat: %v", err)
	}
	if status != FormatUnstamped {
		t.Errorf("status = %v, want %v", status, FormatUnstamped)
	}
}

// Opening a database that is not there used to be reported as a lock conflict,
// which sent anyone debugging it looking for a process that did not exist.
func TestReadOnlyOpenOfMissingDatabaseSaysSo(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "absent.db")
	_, err := OpenStoreReadOnly(dbPath)
	if err == nil {
		t.Fatal("expected an error opening a database that does not exist")
	}
	msg := err.Error()
	if strings.Contains(msg, "locked by another process") {
		t.Errorf("a missing database is still reported as locked: %s", msg)
	}
	if !strings.Contains(msg, "no knowledge graph database") {
		t.Errorf("error does not say the database is missing: %s", msg)
	}
}

func TestArchiveDatabaseMovesJournalAndStamp(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "knowledge.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.EnableJournal()
	if _, err := store.CreateEntity("kept", "topic", "p"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	store.Close()

	aside, err := ArchiveDatabase(dbPath)
	if err != nil {
		t.Fatalf("ArchiveDatabase: %v", err)
	}

	// Nothing is destroyed: an unopenable database may still be the only copy.
	if _, err := os.Stat(aside); err != nil {
		t.Errorf("archived database missing: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("original database still in place; stat returned %v", err)
	}

	// The journal must travel with the database it describes. Left behind, the
	// next rebuild would replay it a second time.
	if _, err := os.Stat(JournalPath(aside)); err != nil {
		t.Errorf("journal did not move with the database: %v", err)
	}
	if _, err := os.Stat(JournalPath(dbPath)); !os.IsNotExist(err) {
		t.Errorf("journal left beside the new database; stat returned %v", err)
	}
	if _, err := os.Stat(FormatStampPath(aside)); err != nil {
		t.Errorf("format stamp did not move with the database: %v", err)
	}
}

func TestArchiveDatabaseDoesNotClobberEarlierArchive(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "knowledge.db")

	for i := 0; i < 2; i++ {
		store, err := OpenStore(dbPath, testSchemaConfig())
		if err != nil {
			t.Fatalf("OpenStore %d: %v", i, err)
		}
		store.Close()
		if _, err := ArchiveDatabase(dbPath); err != nil {
			t.Fatalf("ArchiveDatabase %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	archives := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "knowledge.db.old-") {
			archives++
		}
	}
	if archives < 2 {
		t.Errorf("found %d archived databases, want 2 — the second archive overwrote the first", archives)
	}
}
