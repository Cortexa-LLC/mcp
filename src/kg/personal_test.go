package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withKGHome points the personal store at a temporary directory for one test.
func withKGHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(personalDirEnv, dir)
	return dir
}

func TestPersonalDir_HonoursEnvOverride(t *testing.T) {
	dir := withKGHome(t)

	got, err := personalDir()
	if err != nil {
		t.Fatalf("personalDir: %v", err)
	}
	if got != dir {
		t.Errorf("personalDir() = %q, want %q", got, dir)
	}
}

func TestPersonalDir_DefaultsToHomeAI(t *testing.T) {
	t.Setenv(personalDirEnv, "")

	got, err := personalDir()
	if err != nil {
		t.Fatalf("personalDir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	if want := filepath.Join(home, ".ai"); got != want {
		t.Errorf("personalDir() = %q, want %q", got, want)
	}
}

func TestPersonalDBPath_IsKnowledgeDBInsideGlobalDir(t *testing.T) {
	dir := withKGHome(t)

	got, err := personalDBPath()
	if err != nil {
		t.Fatalf("personalDBPath: %v", err)
	}
	if want := filepath.Join(dir, "knowledge.db"); got != want {
		t.Errorf("personalDBPath() = %q, want %q", got, want)
	}
}

func TestPersonalStoreExists_FalseBeforeInitTrueAfter(t *testing.T) {
	withKGHome(t)

	if personalStoreExists() {
		t.Fatal("personal store should not exist in a fresh KG_HOME")
	}

	store, _, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("create personal store: %v", err)
	}
	store.Close()

	if !personalStoreExists() {
		t.Error("personal store should exist after a read-write open")
	}
}

// A read-only open of a store that was never created must say so, rather than
// surfacing Kuzu's misleading "database is locked" message.
func TestOpenPersonalStore_ReadOnlyMissingStoreExplainsItself(t *testing.T) {
	withKGHome(t)

	_, _, err := openPersonalStore(true)
	if err == nil {
		t.Fatal("expected an error opening a nonexistent personal store read-only")
	}
	if !strings.Contains(err.Error(), "kg personal init") {
		t.Errorf("error should point at 'kg personal init', got: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Errorf("error should not blame locking: %v", err)
	}
}

// A write-mode open creates the store, so recording knowledge never requires an
// explicit init step first.
func TestOpenPersonalStore_WriteModeCreatesStore(t *testing.T) {
	withKGHome(t)

	store, projectID, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("openPersonalStore: %v", err)
	}
	defer store.Close()

	if projectID != personalProjectID {
		t.Errorf("projectID = %q, want %q", projectID, personalProjectID)
	}

	entity, err := store.CreateEntity("test-learning", "learning", projectID)
	if err != nil {
		t.Fatalf("CreateEntity in personal store: %v", err)
	}
	if entity.ProjectID != personalProjectID {
		t.Errorf("entity project ID = %q, want %q", entity.ProjectID, personalProjectID)
	}
}

func TestPersonalLayer_NilWhenStoreMissing(t *testing.T) {
	withKGHome(t)

	layer, err := personalLayer()
	if err != nil {
		t.Fatalf("personalLayer: %v", err)
	}
	if layer != nil {
		t.Error("expected no layer when the personal store does not exist")
	}
}

// The personal store federates in at the lowest priority and carries its own
// project ID, so project results outrank it and its entities are still found.
func TestPersonalLayer_ConfiguredForFederation(t *testing.T) {
	withKGHome(t)

	store, _, err := openPersonalStore(false)
	if err != nil {
		t.Fatalf("create personal store: %v", err)
	}
	store.Close()

	layer, err := personalLayer()
	if err != nil {
		t.Fatalf("personalLayer: %v", err)
	}
	if layer == nil {
		t.Fatal("expected a layer once the personal store exists")
	}
	defer layer.Store.Close()

	if layer.Priority != 0 {
		t.Errorf("priority = %d, want 0 (below every scope layer)", layer.Priority)
	}
	if layer.ProjectID != personalProjectID {
		t.Errorf("ProjectID = %q, want %q", layer.ProjectID, personalProjectID)
	}
	if layer.Name != "personal" {
		t.Errorf("Name = %q, want \"personal\"", layer.Name)
	}
}

// openTarget routes to the personal store when --personal is set.
func TestOpenTarget_PersonalFlagRoutesToPersonalStore(t *testing.T) {
	withKGHome(t)

	usePersonal = true
	t.Cleanup(func() { usePersonal = false })

	store, projectID, err := openTarget(false, "")
	if err != nil {
		t.Fatalf("openTarget: %v", err)
	}
	defer store.Close()

	if projectID != personalProjectID {
		t.Errorf("projectID = %q, want %q", projectID, personalProjectID)
	}
	if !personalStoreExists() {
		t.Error("openTarget with --personal should have created the personal store")
	}
}

// Personal writes are off unless explicitly enabled, and the environment
// variable is honoured so the mode can be turned on without editing an MCP
// client's configuration.
func TestPersonalWritesEnabled_OffByDefault(t *testing.T) {
	t.Setenv(personalWritesEnv, "")
	if personalWritesEnabled(nil) {
		t.Error("personal writes must be off when nothing enables them")
	}
}

func TestPersonalWritesEnabled_EnvVarForms(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", true},
		{"yes", true},
		{"TRUE", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"", false},
		{"   ", false},
	} {
		t.Setenv(personalWritesEnv, tc.value)
		if got := personalWritesEnabled(nil); got != tc.want {
			t.Errorf("KG_PERSONAL_WRITES=%q: got %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestPersonalWritesEnabled_FlagOverridesUnsetEnv(t *testing.T) {
	t.Setenv(personalWritesEnv, "")

	cmd := &cobra.Command{Use: "server"}
	cmd.Flags().Bool("personal-writes", false, "")

	if personalWritesEnabled(cmd) {
		t.Error("flag unset should leave writes off")
	}
	if err := cmd.Flags().Set("personal-writes", "true"); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	if !personalWritesEnabled(cmd) {
		t.Error("--personal-writes should enable writes")
	}
}
