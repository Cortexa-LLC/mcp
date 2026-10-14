package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kglib"
)

// runMigrateInDir runs `kg migrate` with dir as the working directory and
// returns everything it printed plus the error it exited with.
func runMigrateInDir(t *testing.T, dir string) (string, error) {
	t.Helper()
	t.Chdir(dir)

	var out bytes.Buffer
	migrateCmd.SetOut(&out)
	migrateCmd.SetErr(&out)
	t.Cleanup(func() { migrateCmd.SetOut(nil); migrateCmd.SetErr(nil) })

	err := migrateCmd.RunE(migrateCmd, nil)
	return out.String(), err
}

// kg migrate exists to answer one question — "can this build read your
// knowledge graphs?" — so the answer it must never give is a confident yes for
// graphs it never opened. Here the scope list itself is unreadable, which puts
// every project database out of reach.
func TestMigrateDoesNotReportSuccessWhenScopesCannotBeEnumerated(t *testing.T) {
	// No personal store, so the project scopes are the whole population.
	withTempPersonalStore(t)

	root := t.TempDir()
	scopeDir := filepath.Join(root, ".ai", "scope")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A scope config that cannot be parsed: whatever database it names is now
	// unreachable, and there is no way to know whether it is readable.
	if err := os.WriteFile(filepath.Join(scopeDir, "platform.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write scope config: %v", err)
	}

	out, err := runMigrateInDir(t, root)

	if err == nil {
		t.Error("kg migrate exited 0 after failing to enumerate any scope; a script or a user would take that as an all-clear")
	}
	if strings.Contains(out, "readable by this build") {
		t.Errorf("kg migrate claimed every database is readable without checking one:\n%s", out)
	}
	if !strings.Contains(out, "platform.json") {
		t.Errorf("output does not name what could not be read, so there is nothing to act on:\n%s", out)
	}
}

// A scope whose database cannot have its format determined is likewise
// unchecked, and must not be counted among the ones reported clean.
func TestMigrateDoesNotCountUncheckableScopeDatabases(t *testing.T) {
	withTempPersonalStore(t)

	root := t.TempDir()
	scopeDir := filepath.Join(root, ".ai", "scope")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Valid config, but the database sits behind a directory this process
	// cannot traverse, so its storage format cannot be determined at all.
	cfg := `{"name":"platform","database":"locked/platform.db"}`
	if err := os.WriteFile(filepath.Join(scopeDir, "platform.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write scope config: %v", err)
	}
	locked := filepath.Join(root, ".ai", "locked")
	if err := os.MkdirAll(filepath.Join(locked, "platform.db"), 0o755); err != nil {
		t.Fatalf("MkdirAll db: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	out, err := runMigrateInDir(t, root)

	if err == nil {
		t.Error("kg migrate exited 0 for a database whose storage format it could not determine")
	}
	if strings.Contains(out, "readable by this build") {
		t.Errorf("kg migrate reported an all-clear for a database it could not read:\n%s", out)
	}
}

// An unstamped scope database whose pk-column introspection fails is likewise
// unchecked. CheckFormat only reads the sidecar stamp, so it still reports
// FormatUnstamped for a database it never opened; databaseMissingPK then opens
// it read-only and fails. A regression made that error fall through with
// needsRebuild=false, so the loop continued and the run could still print an
// all-clear — a false yes for a database it could not resolve.
func TestMigrateDoesNotCountScopeDatabasesWhosePKCannotBeDetermined(t *testing.T) {
	withTempPersonalStore(t)

	root := t.TempDir()
	scopeDir := filepath.Join(root, ".ai", "scope")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := `{"name":"platform","database":"platform.db"}`
	if err := os.WriteFile(filepath.Join(scopeDir, "platform.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write scope config: %v", err)
	}
	// The database exists (so CheckFormat stats it) and carries no stamp sidecar
	// (so CheckFormat returns FormatUnstamped, reaching the pk check), but is
	// unreadable, so the read-only open inside databaseMissingPK fails.
	dbPath := filepath.Join(root, ".ai", "platform.db")
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatalf("MkdirAll db: %v", err)
	}
	if err := os.Chmod(dbPath, 0o000); err != nil {
		t.Fatalf("chmod db: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o755) })

	// Precondition: CheckFormat must see this as unstamped (not an error of its
	// own), or the test would exercise the CheckFormat-error path instead of the
	// databaseMissingPK-error path this regression is about.
	status, _, err := kglib.CheckFormat(dbPath)
	if err != nil {
		t.Fatalf("CheckFormat precondition failed: %v", err)
	}
	if status != kglib.FormatUnstamped {
		t.Fatalf("fixture is not FormatUnstamped (got %v); test would not reach the pk check", status)
	}

	out, err := runMigrateInDir(t, root)

	if err == nil {
		t.Error("kg migrate exited 0 for a database whose pk column it could not introspect")
	}
	if strings.Contains(out, "readable by this build") {
		t.Errorf("kg migrate reported an all-clear for a database it could not resolve:\n%s", out)
	}
	// Prove it was the databaseMissingPK path, not the CheckFormat path, that
	// flagged it — the new failf is the only source of this phrasing.
	if !strings.Contains(out, "predates the primary key") {
		t.Errorf("output does not show the pk-introspection failure was the reason it was flagged:\n%s", out)
	}
}
