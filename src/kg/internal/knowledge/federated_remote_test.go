// Federation-with-remotes test. This file is an external test package on
// purpose: it exercises knowledge's remote-layer wiring against a real hub
// server, and internal/hub imports internal/knowledge — importing hub from
// package knowledge's own test files would form an import cycle.
package knowledge_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/hub"
	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

const remoteTestCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// buildDB creates a Kuzu database at path with a single named entity.
func buildDB(t *testing.T, path, entityName, projectID string) {
	t.Helper()
	store, err := knowledge.OpenStore(path)
	if err != nil {
		t.Fatalf("open store %s: %v", path, err)
	}
	if _, err := store.CreateEntity(entityName, "function", projectID); err != nil {
		t.Fatalf("create entity %s: %v", entityName, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store %s: %v", path, err)
	}
}

func TestFederatedStoreWithRemotes(t *testing.T) {
	// Hub hosting a "platform" graph containing RemoteThing.
	hubServer := httptest.NewServer(hub.NewServer(t.TempDir(), "", "s3cret", "dev").Handler())
	platformDB := filepath.Join(t.TempDir(), "platform.db")
	buildDB(t, platformDB, "RemoteThing", "platform-proj")
	err := hub.Push(hub.PushRequest{
		HubURL:    hubServer.URL,
		Graph:     "platform",
		DBPath:    platformDB,
		SeedToken: "s3cret",
		Meta: kglib.KGMeta{
			ProjectID: "platform-proj",
			Commit:    remoteTestCommit,
			IndexedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("push platform: %v", err)
	}

	// Local project: aiDir pointing at the hub, scope team-a with a local
	// database containing LocalThing and the platform graph as a remote.
	aiDir := t.TempDir()
	projectID := "consumer-proj"
	hubCfg, _ := json.Marshal(map[string]string{"hub": hubServer.URL})
	if err := os.WriteFile(filepath.Join(aiDir, "config.json"), hubCfg, 0644); err != nil {
		t.Fatal(err)
	}
	buildDB(t, filepath.Join(aiDir, "team-a.db"), "LocalThing", projectID)
	scopeCfg := &knowledge.ScopeConfig{
		Name:     "team-a",
		Database: "team-a.db",
		Remotes:  []string{"platform"},
	}

	openAndSearch := func(t *testing.T, query string) []*knowledge.SearchResult {
		t.Helper()
		fs, err := knowledge.OpenFederatedStoreWithExtra(aiDir, scopeCfg, true, nil)
		if err != nil {
			t.Fatalf("open federated store: %v", err)
		}
		defer fs.Close()
		results, err := fs.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		return results
	}
	hasNamed := func(results []*knowledge.SearchResult, name string) bool {
		for _, r := range results {
			if r.Entity != nil && r.Entity.Name == name {
				return true
			}
		}
		return false
	}

	t.Run("remote entity found via hub", func(t *testing.T) {
		results := openAndSearch(t, "RemoteThing")
		if !hasNamed(results, "RemoteThing") {
			t.Errorf("RemoteThing not in results (%d results)", len(results))
		}
	})

	t.Run("local entity still found", func(t *testing.T) {
		results := openAndSearch(t, "LocalThing")
		if !hasNamed(results, "LocalThing") {
			t.Errorf("LocalThing not in results (%d results)", len(results))
		}
	})

	t.Run("hub down degrades to local results", func(t *testing.T) {
		hubServer.Close()
		results := openAndSearch(t, "LocalThing")
		if !hasNamed(results, "LocalThing") {
			t.Errorf("LocalThing not in results after hub shutdown (%d results)", len(results))
		}
	})
}
