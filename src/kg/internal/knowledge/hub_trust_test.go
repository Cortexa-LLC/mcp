package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes a config.json containing a hub key.
func writeConfig(t *testing.T, dir, hubURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := `{"hub": "` + hubURL + `"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// The property: a repository cannot choose where kg sends KG_HUB_READ_TOKEN.
//
// Scope configs are meant to be committed and shared, so a cloned repo ships its
// own .ai/config.json. When that file chose the hub, any search in the checkout
// sent the query text and the user's bearer token to a host the repo named.
func TestRepoCannotChooseTheHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KG_HOME", home)
	t.Setenv("KG_HUB_URL", "")

	repo := t.TempDir()
	aiDir := filepath.Join(repo, ".ai")
	writeConfig(t, aiDir, "https://attacker.example.com")

	got, err := GetHubURL(aiDir)
	if err != nil {
		t.Fatalf("GetHubURL: %v", err)
	}
	if got != "" {
		t.Fatalf("a repository's config.json set the hub to %q — it can aim the user's "+
			"KG_HUB_READ_TOKEN at a host of its choosing", got)
	}

	// It is still reportable, so a command can tell the user what the project
	// wanted without acting on it.
	if suggested := RepoSuggestedHubURL(aiDir); suggested != "https://attacker.example.com" {
		t.Errorf("RepoSuggestedHubURL = %q, want the repo's value for reporting", suggested)
	}
}

// The user's own choice is honoured, and outranks anything the repo says.
func TestUserConfiguredHubIsUsed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KG_HOME", home)
	t.Setenv("KG_HUB_URL", "")

	repo := t.TempDir()
	aiDir := filepath.Join(repo, ".ai")
	writeConfig(t, aiDir, "https://attacker.example.com")

	path, err := SetUserHubURL("https://kg.internal:7411")
	if err != nil {
		t.Fatalf("SetUserHubURL: %v", err)
	}
	if filepath.Dir(path) != home {
		t.Errorf("user hub recorded at %s, want it under KG_HOME %s", path, home)
	}

	got, err := GetHubURL(aiDir)
	if err != nil {
		t.Fatalf("GetHubURL: %v", err)
	}
	if got != "https://kg.internal:7411" {
		t.Errorf("hub = %q, want the user's choice — a repo must not override it", got)
	}
}

// The env var is a user-controlled override and wins over the stored value.
func TestHubEnvOverridesUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KG_HOME", home)
	if _, err := SetUserHubURL("https://stored.example"); err != nil {
		t.Fatalf("SetUserHubURL: %v", err)
	}
	t.Setenv("KG_HUB_URL", "https://override.example")

	got, err := GetHubURL(t.TempDir())
	if err != nil {
		t.Fatalf("GetHubURL: %v", err)
	}
	if got != "https://override.example" {
		t.Errorf("hub = %q, want the KG_HUB_URL override", got)
	}
}

// Federation must not build a remote layer against an untrusted hub, which is
// where the credential would actually be sent.
func TestFederationSkipsRemotesWithoutATrustedHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KG_HOME", home)
	t.Setenv("KG_HUB_URL", "")
	t.Setenv("KG_HUB_READ_TOKEN", "super-secret-token")

	repo := t.TempDir()
	aiDir := filepath.Join(repo, ".ai")
	writeConfig(t, aiDir, "https://attacker.example.com")

	dbPath := filepath.Join(aiDir, "primary.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	cfg := &ScopeConfig{Name: "primary", Database: "primary.db", Remotes: []string{"someone-elses-graph"}}
	fs, err := OpenFederatedStore(aiDir, cfg, true)
	if err != nil {
		t.Fatalf("OpenFederatedStore: %v", err)
	}
	defer fs.Close()

	for _, name := range fs.LayerNames() {
		if len(name) >= 7 && name[:7] == "remote:" {
			t.Errorf("federation built %s against a hub the repository chose — "+
				"a search would send KG_HUB_READ_TOKEN to it", name)
		}
	}
}
