package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
)

// statsFixture makes a project rooted at a temp dir with a legacy
// knowledge.db, chdirs into it, and returns the root. The chdir is what lets
// statsCmd's RunE — which resolves everything from the working directory —
// be exercised directly.
func statsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	aiDir := filepath.Join(root, ".ai")
	if err := os.MkdirAll(aiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store, err := knowledge.OpenStore(filepath.Join(aiDir, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := store.CreateEntity("stats entity", "topic", projectIDFromCwd(root)); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	store.Close()
	t.Chdir(root)
	return root
}

// runStats invokes the command's own RunE, so the test breaks if stats stops
// routing through resolveScopeDB — a test against the helper alone cannot
// tell that the caller still uses it.
func runStats(t *testing.T, scope string) error {
	t.Helper()
	prev := statsScopeName
	statsScopeName = scope
	t.Cleanup(func() { statsScopeName = prev })
	return statsCmd.RunE(statsCmd, nil)
}

// A scope named on the command line that cannot be loaded must fail, in stats
// exactly as in health — the parity this change is about.
func TestStatsErrorsOnUnloadableNamedScope(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	statsFixture(t)

	err := runStats(t, "no-such-scope")
	if err == nil {
		t.Fatal("kg stats --scope no-such-scope succeeded, want an error rather than a silent legacy fallback")
	}
	if !strings.Contains(err.Error(), "no-such-scope") {
		t.Errorf("error %q does not name the scope that failed", err)
	}
}

// The regression guard for the fallback path: config.json can carry a
// defaultScope naming a scope that does not exist (SetDefaultScope does not
// verify, and config.json travels with a repo). That is a configuration
// leftover, not a request, so stats must still report the legacy database
// instead of failing — which is how it behaved before the shared resolver.
func TestStatsFallsBackWhenInheritedDefaultScopeHasNoConfigs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	root := statsFixture(t)

	cfg := map[string]string{"defaultScope": "team"}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ai", "config.json"), data, 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	if err := runStats(t, ""); err != nil {
		t.Errorf("kg stats with a stale defaultScope and no scope configs: %v; want the legacy database", err)
	}
}
