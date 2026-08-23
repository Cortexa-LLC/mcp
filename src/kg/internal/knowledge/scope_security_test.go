package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeSandbox lays out a realistic victim machine: a home directory holding a
// pre-existing file worth stealing or clobbering, and inside it a cloned
// repository whose .ai directory is the only place kg is entitled to touch.
type scopeSandbox struct {
	t      *testing.T
	home   string // outside the blast radius
	aiDir  string // the only directory kg may read or write
	secret string // an existing file in home/
}

func newScopeSandbox(t *testing.T) *scopeSandbox {
	t.Helper()

	home := t.TempDir()
	aiDir := filepath.Join(home, "repo", ".ai")
	if err := os.MkdirAll(filepath.Join(aiDir, "scope"), 0o755); err != nil {
		t.Fatalf("mkdir scope dir: %v", err)
	}

	// Files kg must never read through a scope name. They are planted at the exact
	// paths the traversal payloads below resolve to -- LoadScopeConfig appends
	// ".json" before joining, so a decoy without that suffix would make the test
	// pass for the wrong reason (the read would simply miss). Their contents are a
	// sentinel: a successful traversal carries "leaked-secret.db" back out as the
	// parsed Database.
	secret := filepath.Join(home, "repo", "secret.json")
	for _, p := range []string{secret, filepath.Join(home, "secret.json")} {
		if err := os.WriteFile(p, []byte(`{"name":"stolen","database":"leaked-secret.db"}`), 0o600); err != nil {
			t.Fatalf("write secret %s: %v", p, err)
		}
	}

	return &scopeSandbox{t: t, home: home, aiDir: aiDir, secret: secret}
}

// writeScope plants a scope config in the repository, exactly as a hostile repo
// would ship one.
func (s *scopeSandbox) writeScope(name string, cfg map[string]any) {
	s.t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		s.t.Fatalf("marshal scope %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(s.aiDir, "scope", name+".json"), data, 0o644); err != nil {
		s.t.Fatalf("write scope %s: %v", name, err)
	}
}

// snapshot records every path under home/ that is not inside .ai, so a later call
// can prove nothing appeared outside the sandbox.
func (s *scopeSandbox) snapshot() map[string]bool {
	s.t.Helper()
	seen := map[string]bool{}
	err := filepath.Walk(s.home, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasPrefix(p, s.aiDir) {
			seen[p] = true
		}
		return nil
	})
	if err != nil {
		s.t.Fatalf("walk sandbox: %v", err)
	}
	return seen
}

// useDatabase reproduces what the ~13 call sites do with a loaded config: join
// Database onto aiDir and create a database there. index.go does this with plain
// string concatenation ("aiDir + \"/\" + cfg.Database"), so filepath.Join's own
// Clean is not even in the way. Emulating the write is the point -- the assertion
// downstream is that nothing landed outside .ai, not that the guard returned some
// particular error string.
func (s *scopeSandbox) useDatabase(cfg *ScopeConfig) {
	s.t.Helper()
	dbPath := s.aiDir + "/" + cfg.Database
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(dbPath, []byte("kuzu"), 0o644)
}

// assertNothingEscaped fails if any path outside .ai was created since the
// snapshot was taken.
func (s *scopeSandbox) assertNothingEscaped(before map[string]bool) {
	s.t.Helper()
	for p := range s.snapshot() {
		if !before[p] {
			s.t.Errorf("a repo-supplied scope config caused %s to be created outside the .ai directory (%s)", p, s.aiDir)
		}
	}
}

// TestLoadScopeConfig_DatabaseCannotEscapeAIDir asserts the property that matters:
// whatever a cloned repository puts in its scope config, no file is created outside
// .ai. It does not assert which error text comes back, because a guard that
// produces a nice error while still handing out an escaping path would pass that
// weaker test.
func TestLoadScopeConfig_DatabaseCannotEscapeAIDir(t *testing.T) {
	payloads := map[string]string{
		"parent traversal":    "../../../pwned.db",
		"deep traversal":      "../../../../../../../../tmp/pwned.db",
		"absolute path":       "/tmp/kg-scope-traversal-pwned.db",
		"nested traversal":    "sub/../../../pwned.db",
		"subdirectory":        "sub/pwned.db",
		"dot dot alone":       "..",
		"dot alone":           ".",
		"trailing separator":  "../pwned.db/",
		"backslash separator": `..\..\pwned.db`,
		"newline smuggled":    "ok.db\n../../pwned.db",
	}

	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			s := newScopeSandbox(t)
			s.writeScope("evil", map[string]any{"name": "evil", "database": payload})

			before := s.snapshot()

			cfg, err := LoadScopeConfig(s.aiDir, "evil")
			if err == nil {
				// The guard let it through; now find out whether that mattered.
				s.useDatabase(cfg)
			}
			s.assertNothingEscaped(before)

			if err == nil {
				t.Errorf("LoadScopeConfig accepted database %q from a repo-supplied config; "+
					"it is joined into a filesystem path by every call site", payload)
			}
		})
	}
}

// TestLoadScopeConfig_ScopeNameCannotEscapeScopeDir covers the read half. scopeName
// reaches LoadScopeConfig from the --scope flag and from layer names inside
// repo-supplied config, and is concatenated with ".json" before the join, so it can
// address any *.json on the disk.
func TestLoadScopeConfig_ScopeNameCannotEscapeScopeDir(t *testing.T) {
	// Each payload resolves onto a planted secret.json, so an unguarded
	// LoadScopeConfig genuinely returns its contents rather than merely erroring on
	// a missing file. Verified by removing the guard: every case below then reported
	// "read the file at ... and returned its contents".
	payloads := []string{
		"../../secret",        // -> <home>/repo/secret.json
		"../../../secret",     // -> <home>/secret.json
		"sub/../../../secret", // Clean() collapses the decoy segment first
	}

	for _, payload := range payloads {
		t.Run(payload, func(t *testing.T) {
			s := newScopeSandbox(t)

			cfg, err := LoadScopeConfig(s.aiDir, payload)
			if err == nil {
				t.Fatalf("LoadScopeConfig(%q) read the file at %s and returned its contents "+
					"(database=%q); the scope name resolved outside %s/scope",
					payload, filepath.Join(s.aiDir, "scope", payload+".json"), cfg.Database, s.aiDir)
			}
			if cfg != nil {
				t.Fatalf("LoadScopeConfig returned a config for out-of-tree scope name %q", payload)
			}
			// The sentinel proves the file was never parsed, independent of whether
			// the failure came from validation or from a missing file.
			if strings.Contains(err.Error(), "leaked-secret.db") {
				t.Fatalf("error leaked the contents of a file outside the scope directory: %v", err)
			}
		})
	}
}

// TestLoadScopeConfig_RejectsUnusableScopeNames covers names that are not
// traversals in themselves but must not be accepted either -- "." and ".." are path
// navigation rather than names, and a separator means the caller is addressing a
// directory tree instead of naming a scope.
func TestLoadScopeConfig_RejectsUnusableScopeNames(t *testing.T) {
	for _, payload := range []string{"", ".", "..", "a/b", `a\b`, "with space", strings.Repeat("x", 65)} {
		t.Run("name="+payload, func(t *testing.T) {
			s := newScopeSandbox(t)
			if _, err := LoadScopeConfig(s.aiDir, payload); err == nil {
				t.Fatalf("LoadScopeConfig accepted scope name %q", payload)
			}
		})
	}
}

// TestLoadScopeConfig_LayersAndRemotesAreValidated: layers are fed straight back
// into LoadScopeConfig and joined into database paths by federated.go, and remotes
// become path segments of a hub request. Both arrive from the same untrusted file
// as Database.
func TestLoadScopeConfig_LayersAndRemotesAreValidated(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{
			name: "traversal in layers",
			cfg:  map[string]any{"database": "ok.db", "layers": []string{"../../secret"}},
		},
		{
			name: "dotdot layer",
			cfg:  map[string]any{"database": "ok.db", "layers": []string{".."}},
		},
		{
			name: "traversal in remotes",
			cfg:  map[string]any{"database": "ok.db", "remotes": []string{"../../../etc/passwd"}},
		},
		{
			name: "url injection in remotes",
			cfg:  map[string]any{"database": "ok.db", "remotes": []string{"graph?token=x"}},
		},
		{
			name: "traversal in name",
			cfg:  map[string]any{"name": "../../evil", "database": "ok.db"},
		},
		{
			name: "traversal in hubGraph",
			cfg:  map[string]any{"database": "ok.db", "hubGraph": "../../evil"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newScopeSandbox(t)
			s.writeScope("evil", tc.cfg)

			if _, err := LoadScopeConfig(s.aiDir, "evil"); err == nil {
				t.Fatalf("LoadScopeConfig accepted %v; every one of these fields is joined into a path or a hub URL", tc.cfg)
			}
		})
	}
}

// TestLoadScopeConfig_AcceptsOrdinaryConfigs guards the other direction: the
// validation must not break the shapes real repositories use, or it will simply be
// removed by whoever hits it next.
func TestLoadScopeConfig_AcceptsOrdinaryConfigs(t *testing.T) {
	s := newScopeSandbox(t)
	s.writeScope("platform", map[string]any{"database": "platform.db"})
	s.writeScope("selling", map[string]any{
		"name":     "selling",
		"database": "selling-module_v2.db",
		"hubGraph": "acme.selling",
		"layers":   []string{"platform"},
		"remotes":  []string{"acme.platform"},
		"include":  []string{"modules/**/*"},
	})

	for _, name := range []string{"platform", "selling"} {
		cfg, err := LoadScopeConfig(s.aiDir, name)
		if err != nil {
			t.Fatalf("LoadScopeConfig(%q) rejected an ordinary config: %v", name, err)
		}
		if cfg.Name != name {
			t.Errorf("scope %q: got Name %q", name, cfg.Name)
		}
	}

	// And the derived path stays inside .ai, which is the whole point.
	cfg, err := LoadScopeConfig(s.aiDir, "selling")
	if err != nil {
		t.Fatalf("load selling: %v", err)
	}
	dbPath := filepath.Clean(s.aiDir + "/" + cfg.Database)
	if !strings.HasPrefix(dbPath, s.aiDir+string(filepath.Separator)) {
		t.Errorf("derived database path %q escaped %q", dbPath, s.aiDir)
	}
}
