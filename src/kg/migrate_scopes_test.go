package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
