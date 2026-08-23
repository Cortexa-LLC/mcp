package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

// stampAs rewrites a database's format sidecar to claim a different engine
// build wrote it, which is how a future go-kuzu bump is simulated without one.
func stampAs(t *testing.T, dbPath, kuzuVersion string) {
	t.Helper()
	data, err := json.Marshal(kglib.FormatStamp{KuzuVersion: kuzuVersion, KGVersion: "test"})
	if err != nil {
		t.Fatalf("marshal stamp: %v", err)
	}
	if err := os.WriteFile(kglib.FormatStampPath(dbPath), data, 0600); err != nil {
		t.Fatalf("write stamp: %v", err)
	}
}

// The whole point of Phase 4, end to end: hand-written knowledge survives a
// storage-format change with no old binary involved and no user action.
func TestEnsureFormatRebuildsAndRestoresHandWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")

	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.EnableJournal()
	entity, err := store.CreateEntity("retry-policy", "decision", "proj")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "three attempts, exponential backoff", "proj"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	store.Close()

	stampAs(t, dbPath, "v0.0.1-ancient")

	var out bytes.Buffer
	rebuilt, err := rebuildHandWritesOnly(dbPath, &out)
	if err != nil {
		t.Fatalf("rebuildHandWritesOnly: %v", err)
	}
	if !rebuilt {
		t.Fatal("a database stamped with a different engine version was not rebuilt")
	}
	if !strings.Contains(out.String(), "Rebuilding") {
		t.Errorf("rebuild was silent; the user should see why the run is slow:\n%s", out.String())
	}

	// The rebuilt database carries the hand-written knowledge.
	fresh, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open rebuilt store: %v", err)
	}
	defer fresh.Close()

	restored, err := fresh.GetEntityByNameAndType("retry-policy", "decision", "proj")
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if restored == nil {
		t.Fatal("hand-written entity did not survive the rebuild")
	}
	observations, err := fresh.GetObservations(restored.ID, "proj")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 || observations[0].Content != "three attempts, exponential backoff" {
		t.Errorf("observations = %+v, want the hand-written note", observations)
	}

	// The rebuilt database is stamped for this build, so it is not rebuilt again.
	status, _, err := kglib.CheckFormat(dbPath)
	if err != nil {
		t.Fatalf("CheckFormat: %v", err)
	}
	if status != kglib.FormatOK {
		t.Errorf("status after rebuild = %v, want %v", status, kglib.FormatOK)
	}

	// And it carries a journal forward, so the *next* format bump has one.
	if _, err := os.Stat(kglib.JournalPath(dbPath)); err != nil {
		t.Errorf("rebuilt database has no journal, so the next bump would lose these writes: %v", err)
	}
}

// A rebuild must not run twice for the same bump.
func TestEnsureFormatIsANoOpWhenFormatMatches(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	var out bytes.Buffer
	rebuilt, err := rebuildHandWritesOnly(dbPath, &out)
	if err != nil {
		t.Fatalf("rebuildHandWritesOnly: %v", err)
	}
	if rebuilt {
		t.Error("a database written by this build was rebuilt anyway")
	}
	if out.Len() != 0 {
		t.Errorf("no-op rebuild printed output: %s", out.String())
	}
}

// Databases written before stamping shipped must be left alone. Rebuilding them
// on sight would be precisely the forced breakage this is meant to prevent.
func TestEnsureFormatLeavesUnstampedDatabasesAlone(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()
	if err := os.Remove(kglib.FormatStampPath(dbPath)); err != nil {
		t.Fatalf("remove stamp: %v", err)
	}

	var out bytes.Buffer
	rebuilt, err := rebuildHandWritesOnly(dbPath, &out)
	if err != nil {
		t.Fatalf("rebuildHandWritesOnly: %v", err)
	}
	if rebuilt {
		t.Error("an unstamped (pre-existing) database was rebuilt")
	}
}

// The archived database is kept: a rebuild that goes wrong should be
// recoverable, and an unopenable database may still be someone's only copy.
func TestRebuildKeepsTheOldDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.EnableJournal()
	if _, err := store.CreateEntity("kept", "topic", "proj"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	store.Close()
	stampAs(t, dbPath, "v0.0.1-ancient")

	var out bytes.Buffer
	aside, err := archiveIfMismatch(dbPath, &out)
	if err != nil {
		t.Fatalf("archiveIfMismatch: %v", err)
	}
	if aside == "" {
		t.Fatal("rebuild reported no archive")
	}
	if _, err := os.Stat(aside); err != nil {
		t.Errorf("archived database is gone: %v", err)
	}
	if !strings.Contains(aside, "v0.0.1-ancient") {
		t.Errorf("archive name %q does not record which format it holds", aside)
	}
}

// withTempPersonalStore points KG_HOME at a scratch directory and returns the
// personal database path.
func withTempPersonalStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(personalDirEnv, home)
	return filepath.Join(home, "knowledge.db")
}

// The personal store has no source tree, so it repairs itself on the next write
// rather than waiting for an index run the user may never do.
func TestPersonalStoreRebuildsItselfOnWriteOpen(t *testing.T) {
	dbPath := withTempPersonalStore(t)

	store, _, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("openPersonalStore: %v", err)
	}
	store.EnableJournal()
	entity, err := store.CreateEntity("kafka-retention", "decision", personalProjectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "7-day retention", personalProjectID); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	store.Close()

	stampAs(t, dbPath, "v0.0.1-ancient")

	// Simply opening for write is enough to trigger the rebuild.
	reopened, projectID, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("openPersonalStore after format change: %v", err)
	}
	defer reopened.Close()

	restored, err := reopened.GetEntityByNameAndType("kafka-retention", "decision", projectID)
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if restored == nil {
		t.Fatal("personal entry did not survive the storage-format rebuild")
	}
	observations, err := reopened.GetObservations(restored.ID, projectID)
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 {
		t.Errorf("observation count = %d, want 1", len(observations))
	}

	// The old database is kept alongside.
	matches, err := filepath.Glob(dbPath + ".old-*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) == 0 {
		t.Error("no archived copy of the pre-rebuild personal store")
	}
}

// A read-only open must not attempt a rebuild: it cannot create the replacement,
// and silently rewriting a store on a read path would be the wrong call anyway.
func TestPersonalStoreReadOnlyOpenDoesNotRebuild(t *testing.T) {
	dbPath := withTempPersonalStore(t)

	store, _, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("openPersonalStore: %v", err)
	}
	store.Close()
	stampAs(t, dbPath, "v0.0.1-ancient")

	// The open itself may fail — the point is that nothing was archived.
	if ro, _, err := openPersonalStore(true); err == nil {
		ro.Close()
	}

	matches, err := filepath.Glob(dbPath + ".old-*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("a read-only open archived the store: %v", matches)
	}
}

// kg migrate reports cleanly when every database is already readable.
func TestMigrateCommandReportsCleanWhenNothingToDo(t *testing.T) {
	withTempPersonalStore(t)
	store, _, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("openPersonalStore: %v", err)
	}
	store.Close()

	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	migrateCmd.SetOut(&out)
	migrateCmd.SetErr(&out)
	t.Cleanup(func() { migrateCmd.SetOut(nil); migrateCmd.SetErr(nil) })

	if err := migrateCmd.RunE(migrateCmd, nil); err != nil {
		t.Fatalf("kg migrate: %v", err)
	}
	if !strings.Contains(out.String(), "readable by this build") {
		t.Errorf("expected an all-clear report, got:\n%s", out.String())
	}
}

// And rebuilds the personal store when it needs it.
func TestMigrateCommandRebuildsPersonalStore(t *testing.T) {
	dbPath := withTempPersonalStore(t)

	store, _, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("openPersonalStore: %v", err)
	}
	store.EnableJournal()
	if _, err := store.CreateEntity("kafka-retention", "decision", personalProjectID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	store.Close()
	stampAs(t, dbPath, "v0.0.1-ancient")

	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	migrateCmd.SetOut(&out)
	migrateCmd.SetErr(&out)
	t.Cleanup(func() { migrateCmd.SetOut(nil); migrateCmd.SetErr(nil) })

	if err := migrateCmd.RunE(migrateCmd, nil); err != nil {
		t.Fatalf("kg migrate: %v", err)
	}
	if !strings.Contains(out.String(), "rebuilt") {
		t.Errorf("expected a rebuild report, got:\n%s", out.String())
	}

	reopened, projectID, err := openPersonalStore(true)
	if err != nil {
		t.Fatalf("open rebuilt personal store: %v", err)
	}
	defer reopened.Close()
	restored, err := reopened.GetEntityByNameAndType("kafka-retention", "decision", projectID)
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if restored == nil {
		t.Fatal("kg migrate did not restore the personal entry")
	}
}
