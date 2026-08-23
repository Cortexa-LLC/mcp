package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// hubGraphNameRE mirrors the hub's own graphNameRE. Duplicated rather than
// imported because internal/hub is a separate concern; if the two drift, the
// test that matters is the one asserting names the hub will actually accept.
var hubGraphNameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

func writeScope(t *testing.T, aiDir, name string, cfg map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(aiDir, "scope"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(aiDir, "scope", name+".json"), data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func gitRepo(t *testing.T, remote string) string {
	t.Helper()
	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	if remote != "" {
		if err := exec.Command("git", "-C", root, "remote", "add", "origin", remote).Run(); err != nil {
			t.Fatalf("git remote add: %v", err)
		}
	}
	return root
}

func TestRepoNameFromRemote(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"git@github.com:Cortexa-LLC/mcp.git", "mcp"},
		{"https://github.com/Cortexa-LLC/mcp.git", "mcp"},
		{"https://github.com/Cortexa-LLC/mcp", "mcp"},
		{"ssh://git@github.com/Cortexa-LLC/depop.git", "depop"},
		{"https://github.com/Cortexa-LLC/www.cortexa.com.git", "www.cortexa.com"},
		{"", ""},
	} {
		if got := repoNameFromRemote(tc.remote); got != tc.want {
			t.Errorf("repoNameFromRemote(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

// A repo that is one project — the meta-repo case — gets a bare name, not a
// redundant suffix.
func TestSingleProjectRepoGetsBareName(t *testing.T) {
	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")

	if got := defaultGraphName(root, aiDir, ""); got != "depop" {
		t.Errorf("unscoped graph name = %q, want depop", got)
	}
}

// The default scope also keeps the bare name, so adding a second scope later
// does not rename the graph the first one was already pushing to.
func TestDefaultScopeKeepsBareRepoName(t *testing.T) {
	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")
	writeScope(t, aiDir, "platform", map[string]any{"name": "platform", "database": "platform.db"})
	writeScope(t, aiDir, "checkout", map[string]any{"name": "checkout", "database": "checkout.db"})
	if err := os.WriteFile(filepath.Join(aiDir, "config.json"),
		[]byte(`{"defaultScope":"platform"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if got := defaultGraphName(root, aiDir, "platform"); got != "depop" {
		t.Errorf("default scope graph name = %q, want depop", got)
	}
	if got := defaultGraphName(root, aiDir, "checkout"); got != "depop.checkout" {
		t.Errorf("non-default scope graph name = %q, want depop.checkout", got)
	}
}

// The collision this exists to prevent: the same scope name in two repos must
// not produce the same graph name.
func TestSameScopeInDifferentReposDoesNotCollide(t *testing.T) {
	depop := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	harvana := gitRepo(t, "git@github.com:Cortexa-LLC/harvana.git")

	for _, root := range []string{depop, harvana} {
		aiDir := filepath.Join(root, ".ai")
		writeScope(t, aiDir, "platform", map[string]any{"name": "platform", "database": "platform.db"})
	}

	a := defaultGraphName(depop, filepath.Join(depop, ".ai"), "platform")
	b := defaultGraphName(harvana, filepath.Join(harvana, ".ai"), "platform")
	if a == b {
		t.Fatalf("both repos push to %q — the collision is still there", a)
	}
	if a != "depop.platform" || b != "harvana.platform" {
		t.Errorf("got %q and %q, want depop.platform and harvana.platform", a, b)
	}
}

// A monorepo that wants to pin its published names can, since --graph cannot
// express per-scope names during --all-scopes.
func TestHubGraphOverrideWins(t *testing.T) {
	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")
	writeScope(t, aiDir, "checkout", map[string]any{
		"name": "checkout", "database": "checkout.db", "hubGraph": "depop-payments",
	})

	if got := defaultGraphName(root, aiDir, "checkout"); got != "depop-payments" {
		t.Errorf("graph name = %q, want the configured depop-payments", got)
	}
}

// Every name this produces has to be one the hub will accept, or push fails
// with a regex error at the far end of the wire.
func TestGeneratedNamesAreValidOnTheHub(t *testing.T) {
	for _, remote := range []string{
		"git@github.com:Cortexa-LLC/depop.git",
		"https://github.com/Cortexa-LLC/www.cortexa.com.git",
		"https://github.com/Cortexa-LLC/some_repo.git",
	} {
		root := gitRepo(t, remote)
		aiDir := filepath.Join(root, ".ai")
		writeScope(t, aiDir, "team a", map[string]any{"name": "team a", "database": "a.db"})

		for _, scope := range []string{"", "team a"} {
			name := defaultGraphName(root, aiDir, scope)
			if !hubGraphNameRE.MatchString(name) {
				t.Errorf("remote %q scope %q produced %q, which the hub rejects", remote, scope, name)
			}
			if name == "." || name == ".." {
				t.Errorf("remote %q scope %q produced a path-navigation name %q", remote, scope, name)
			}
		}
	}
}

// A slash would make the name more than one path component, which is exactly
// what the hub's validation forbids.
func TestGraphNamesNeverContainSlashes(t *testing.T) {
	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")
	writeScope(t, aiDir, "checkout", map[string]any{"name": "checkout", "database": "checkout.db"})

	name := defaultGraphName(root, aiDir, "checkout")
	for _, r := range name {
		if r == '/' || r == '\\' {
			t.Fatalf("graph name %q contains a path separator", name)
		}
	}
}

// No remote is not an error: a repo that has never been pushed anywhere still
// needs a name.
func TestFallsBackToDirectoryName(t *testing.T) {
	root := gitRepo(t, "")
	if got := defaultGraphName(root, filepath.Join(root, ".ai"), ""); got == "" {
		t.Error("a repo with no remote produced an empty graph name")
	}
}

func TestSanitizeGraphComponent(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"team a", "team-a"},
		{"feature/branch", "feature-branch"},
		{"  padded  ", "padded"},
		{"..", ""},
		{"-leading-trailing-", "leading-trailing"},
	} {
		if got := sanitizeGraphComponent(tc.in); got != tc.want {
			t.Errorf("sanitizeGraphComponent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
