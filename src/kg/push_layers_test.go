package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/hub"
	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

const (
	testCommit      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDecoyCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// seedDB builds a real database at dbPath holding one distinctively named
// entity, stamped for projectID at commit.
func seedDB(t *testing.T, dbPath, entityName, projectID, commit string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore(%s): %v", dbPath, err)
	}
	if _, err := store.CreateEntity(entityName, "function", projectID); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	err = store.SetMeta(kglib.KGMeta{ProjectID: projectID, Commit: commit, IndexedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

type layerSearchResult struct {
	Graph          string                    `json:"graph"`
	LayersSearched []string                  `json:"layers_searched"`
	Results        []*knowledge.SearchResult `json:"results"`
}

func searchWithLayers(t *testing.T, hubURL, graph, query string) layerSearchResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "include_layers": true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(hubURL+"/v1/graphs/"+graph+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST search: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search %s: status %d, body %s", graph, resp.StatusCode, buf.String())
	}
	var out layerSearchResult
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("parse search response: %v", err)
	}
	return out
}

func (r layerSearchResult) has(name string) bool {
	for _, res := range r.Results {
		if res.Entity != nil && res.Entity.Name == name {
			return true
		}
	}
	return false
}

// The end-to-end property "a pushed graph's layers resolve on the hub", run
// against the real name derivation and the real hub-side expansion rather than
// hand-written names on both sides — which is how the namespace mismatch
// survived the existing hub tests.
//
// The repo `depop` has a default scope `platform` (published as `depop`) and a
// scope `checkout` layered on it (published as `depop.checkout`). An unrelated
// repo owns the bare name `platform` on the same hub, which is the collision
// the naming rule exists to avoid — and the trap a raw scope name falls into.
func TestPushedLayersResolveOnTheHub(t *testing.T) {
	t.Cleanup(func() { pushGraphName = "" })

	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")
	writeScope(t, aiDir, "platform", map[string]any{
		"name": "platform", "database": "platform.db",
	})
	writeScope(t, aiDir, "checkout", map[string]any{
		"name": "checkout", "database": "checkout.db", "layers": []string{"platform"},
	})
	if err := os.WriteFile(filepath.Join(aiDir, "config.json"),
		[]byte(`{"defaultScope":"platform"}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	projectID := projectIDFromCwd(root)
	seedDB(t, filepath.Join(aiDir, "platform.db"), "PlatformCore", projectID, testCommit)
	seedDB(t, filepath.Join(aiDir, "checkout.db"), "CheckoutFlow", projectID, testCommit)

	ts := httptest.NewServer(hub.NewServer(t.TempDir(), "", "s3cret", "dev").Handler())
	t.Cleanup(ts.Close)

	// An unrelated repo already holds the bare scope name on this hub.
	decoyDB := filepath.Join(t.TempDir(), "harvana.db")
	seedDB(t, decoyDB, "HarvanaSecret", "harvana", testDecoyCommit)
	err := hub.Push(hub.PushRequest{
		HubURL: ts.URL, Graph: "platform", DBPath: decoyDB, SeedToken: "s3cret",
		Meta: kglib.KGMeta{
			ProjectID: "harvana", Commit: testDecoyCommit,
			RepoURL: "git@github.com:Cortexa-LLC/harvana.git", IndexedAt: time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatalf("push decoy: %v", err)
	}

	for _, scope := range []string{"platform", "checkout"} {
		if err := pushScopeDB(root, aiDir, ts.URL, "s3cret", projectID, scope); err != nil {
			t.Fatalf("push scope %s: %v", scope, err)
		}
	}

	t.Run("the layer resolves to this repo's graph", func(t *testing.T) {
		out := searchWithLayers(t, ts.URL, "depop.checkout", "PlatformCore")
		if !out.has("PlatformCore") {
			t.Errorf("include_layers on depop.checkout did not reach the platform layer: "+
				"layers_searched = %v, results = %d — the layer reference never resolved on the hub",
				out.LayersSearched, len(out.Results))
		}
		if !slices.Equal(out.LayersSearched, []string{"depop"}) {
			t.Errorf("layers_searched = %v, want [depop] (the hub name of the platform scope)", out.LayersSearched)
		}
	})

	t.Run("the layer does not resolve to an unrelated repo's graph", func(t *testing.T) {
		out := searchWithLayers(t, ts.URL, "depop.checkout", "HarvanaSecret")
		if out.has("HarvanaSecret") {
			t.Errorf("depop.checkout federated harvana's graph: the layer reference resolved to the "+
				"bare name %q owned by another repo (layers_searched = %v)", "platform", out.LayersSearched)
		}
	})

	// --graph renames the database being pushed. It cannot rename the graphs
	// that database points at, because those were registered by their own
	// pushes under their own derived names — so the layers must still resolve.
	t.Run("--graph renames the push, not its layers", func(t *testing.T) {
		pushGraphName = "depop-payments"
		defer func() { pushGraphName = "" }()
		if err := pushScopeDB(root, aiDir, ts.URL, "s3cret", projectID, "checkout"); err != nil {
			t.Fatalf("push checkout under an override name: %v", err)
		}
		out := searchWithLayers(t, ts.URL, "depop-payments", "PlatformCore")
		if !slices.Equal(out.LayersSearched, []string{"depop"}) {
			t.Errorf("layers_searched = %v, want [depop]: --graph must not rename layer references",
				out.LayersSearched)
		}
		if !out.has("PlatformCore") {
			t.Errorf("layers stopped resolving when the graph was renamed with --graph")
		}
	})
}

// hubGraph pins a scope's published name, and a layer pointing at that scope
// has to follow it — otherwise the pin silently breaks federation for every
// scope that layers on the pinned one.
func TestLayerNamesFollowHubGraphOverrides(t *testing.T) {
	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")
	writeScope(t, aiDir, "platform", map[string]any{
		"name": "platform", "database": "platform.db", "hubGraph": "depop-core",
	})
	writeScope(t, aiDir, "checkout", map[string]any{
		"name": "checkout", "database": "checkout.db", "layers": []string{"platform"},
	})

	got := hubLayerNames(root, aiDir, []string{"platform"})
	if !slices.Equal(got, []string{"depop-core"}) {
		t.Errorf("hubLayerNames = %v, want [depop-core]", got)
	}
}

// Two scopes pinned to one published graph must not make the hub search it
// twice.
func TestHubLayerNamesDeduplicates(t *testing.T) {
	root := gitRepo(t, "git@github.com:Cortexa-LLC/depop.git")
	aiDir := filepath.Join(root, ".ai")
	for _, s := range []string{"a", "b"} {
		writeScope(t, aiDir, s, map[string]any{
			"name": s, "database": s + ".db", "hubGraph": "depop-shared",
		})
	}
	if got := hubLayerNames(root, aiDir, []string{"a", "b"}); !slices.Equal(got, []string{"depop-shared"}) {
		t.Errorf("hubLayerNames = %v, want [depop-shared]", got)
	}
}
