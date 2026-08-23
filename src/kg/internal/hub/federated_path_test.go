package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

// plantEscapedGraph writes a real database at a path that is *outside* the
// hub's data directory but reachable from graphs/<name>/current when <name> is
// allowed to navigate, and registers it in registry.json under that name.
//
// registry.json is a plain file an operator edits by hand, which is exactly why
// expandLayers revalidates names it reads from it. This sets up the same trap
// for the fan-out endpoint.
func plantEscapedGraph(t *testing.T, dataDir, name, entityName string) {
	t.Helper()

	dbDir := filepath.Dir(filepath.Join(dataDir, "graphs", name, "current", canonicalDBName))
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatalf("create escaped graph dir: %v", err)
	}
	store, err := knowledge.OpenStore(filepath.Join(dbDir, canonicalDBName))
	if err != nil {
		t.Fatalf("open escaped store: %v", err)
	}
	if _, err := store.CreateEntity(entityName, "function", "monorepo"); err != nil {
		t.Fatalf("create escaped entity: %v", err)
	}
	if err := store.SetMeta(kglib.KGMeta{ProjectID: "monorepo", Commit: commitC, IndexedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("stamp escaped store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close escaped store: %v", err)
	}

	reg, err := loadRegistry(dataDir)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	reg.Graphs[name] = &GraphInfo{
		Commit:    commitC,
		ProjectID: "monorepo",
		IndexedAt: time.Now().UTC(),
		PushedAt:  time.Now().UTC(),
	}
	if err := saveRegistry(dataDir, reg); err != nil {
		t.Fatalf("save registry: %v", err)
	}
}

func federatedSearch(t *testing.T, hubURL string, body map[string]any) (*http.Response, map[string][]string) {
	t.Helper()
	resp, raw := postJSON(t, hubURL+"/v1/search", body)
	if resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	var out struct {
		Results map[string]struct {
			Results []*knowledge.SearchResult `json:"results"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse federated response: %v", err)
	}
	byGraph := make(map[string][]string, len(out.Results))
	for graph, g := range out.Results {
		for _, r := range g.Results {
			if r.Entity != nil {
				byGraph[graph] = append(byGraph[graph], r.Entity.Name)
			}
		}
	}
	return resp, byGraph
}

// The fan-out endpoint joins every graph name it handles into a filesystem
// path, so it must validate them the way handleGraphSearch and expandLayers do.
// The property asserted is the one that matters: the hub never reads a database
// from outside its graphs directory, whatever registry.json says.
func TestFederatedSearchNeverReadsOutsideTheGraphsDirectory(t *testing.T) {
	db := buildFixtureDBNamed(t, "GoodEntity")
	ts, dataDir := newTestHub(t, "", "s3cret", "dev")

	if err := pushFixture(t, ts.URL, "good", db, commitA, nil); err != nil {
		t.Fatalf("push good: %v", err)
	}

	const escapeName = "../../escape"
	plantEscapedGraph(t, dataDir, escapeName, "SecretEntity")

	t.Run("fan-out over the whole registry skips the navigating name", func(t *testing.T) {
		resp, byGraph := federatedSearch(t, ts.URL, map[string]any{"query": "SecretEntity"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("federated search status = %d", resp.StatusCode)
		}
		for graph, names := range byGraph {
			for _, n := range names {
				if n == "SecretEntity" {
					t.Fatalf("the hub read a database outside its graphs directory: %q surfaced under graph %q",
						n, graph)
				}
			}
		}
		if _, ok := byGraph[escapeName]; ok {
			t.Errorf("graph %q appeared in the fan-out results at all", escapeName)
		}
	})

	t.Run("an explicitly requested navigating name is refused", func(t *testing.T) {
		resp, byGraph := federatedSearch(t, ts.URL, map[string]any{
			"query":  "SecretEntity",
			"graphs": []string{escapeName},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 for graph name %q (results: %v)", resp.StatusCode, escapeName, byGraph)
		}
	})

	t.Run("valid names still work", func(t *testing.T) {
		resp, byGraph := federatedSearch(t, ts.URL, map[string]any{
			"query":  "GoodEntity",
			"graphs": []string{"good"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("federated search over a valid graph returned %d", resp.StatusCode)
		}
		if !contains(byGraph["good"], "GoodEntity") {
			t.Errorf("validation broke the ordinary path: results = %v", byGraph)
		}
	})
}
