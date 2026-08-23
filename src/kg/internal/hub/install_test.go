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

// buildFixtureDBFor builds a fixture database that answers for a caller-chosen
// project. The project ID matters here: the hub searches the database under
// `current` using the ProjectID recorded in the registry, so a graph whose
// registry entry and installed database disagree returns nothing at all.
func buildFixtureDBFor(t *testing.T, entityName, projectID, commit string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	if _, err := store.CreateEntity(entityName, "function", projectID); err != nil {
		t.Fatalf("create fixture entity: %v", err)
	}
	err = store.SetMeta(kglib.KGMeta{ProjectID: projectID, Commit: commit, IndexedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("stamp fixture: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}
	return dbPath
}

func pushFixtureFor(t *testing.T, hubURL, graph, dbPath, commit, projectID string) error {
	t.Helper()
	return Push(PushRequest{
		HubURL:    hubURL,
		Graph:     graph,
		DBPath:    dbPath,
		SeedToken: "s3cret",
		Meta: kglib.KGMeta{
			ProjectID: projectID,
			Commit:    commit,
			RepoURL:   "git@example.com:acme/monorepo.git",
			IndexedAt: time.Now().UTC(),
		},
	})
}

// searchNames returns the entity names a per-graph search produced, plus the
// commit the hub says it is serving.
func searchNames(t *testing.T, hubURL, graph, query string) (names []string, commit string) {
	t.Helper()
	resp, raw := postJSON(t, hubURL+"/v1/graphs/"+graph+"/search", map[string]any{"query": query})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search %s status = %d, body: %s", graph, resp.StatusCode, raw)
	}
	var out struct {
		Commit  string                    `json:"commit"`
		Results []*knowledge.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse search response: %v", err)
	}
	for _, r := range out.Results {
		if r.Entity != nil {
			names = append(names, r.Entity.Name)
		}
	}
	return names, out.Commit
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// A push that cannot record itself in the registry must not change what the
// hub serves. The property under test is agreement, not any single artifact:
// after the failed push the hub must still answer the query it could answer
// before, which is only true if `current` and the registry's ProjectID still
// describe the same, previously installed commit.
//
// Before the install() reordering, `current` was repointed and the stashed
// directory deleted before the registry was written, and a registry error
// returned without undoing either — so the hub served the new database while
// querying it with the old project ID, and every search came back empty.
func TestRegistryWriteFailureLeavesGraphUnchanged(t *testing.T) {
	oldDB := buildFixtureDBFor(t, "OldEntity", "proj-old", commitA)
	newDB := buildFixtureDBFor(t, "NewEntity", "proj-new", commitB)
	ts, dataDir := newTestHub(t, "", "s3cret", "dev")

	if err := pushFixtureFor(t, ts.URL, "g", oldDB, commitA, "proj-old"); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	if names, _ := searchNames(t, ts.URL, "g", "OldEntity"); !contains(names, "OldEntity") {
		t.Fatalf("fixture is not searchable before the failure is injected: got %v", names)
	}

	// saveRegistry writes registry.json.tmp and renames it over registry.json.
	// A directory in that slot makes the open fail, which is the failure the
	// auditor reproduced by other means.
	if err := os.Mkdir(filepath.Join(dataDir, registryFile+".tmp"), 0755); err != nil {
		t.Fatalf("block registry writes: %v", err)
	}

	if err := pushFixtureFor(t, ts.URL, "g", newDB, commitB, "proj-new"); err == nil {
		t.Fatal("push reported success even though the registry could not be written")
	}

	names, commit := searchNames(t, ts.URL, "g", "OldEntity")
	if !contains(names, "OldEntity") {
		t.Errorf("after a failed push the hub no longer answers for the graph it advertises: "+
			"searching %q for OldEntity returned %v — `current` and the registry's ProjectID disagree", commit, names)
	}
	if commit != commitA {
		t.Errorf("advertised commit = %q, want the still-installed %q", commit, commitA)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "graphs", "g", commitB)); err == nil {
		t.Errorf("the rolled-back push left %s behind — a commit directory the registry never mentions", commitB)
	}

	// The rollback must not wedge the graph: once the registry is writable
	// again the same push succeeds and takes effect.
	if err := os.Remove(filepath.Join(dataDir, registryFile+".tmp")); err != nil {
		t.Fatalf("unblock registry writes: %v", err)
	}
	if err := pushFixtureFor(t, ts.URL, "g", newDB, commitB, "proj-new"); err != nil {
		t.Fatalf("push after the registry became writable again: %v", err)
	}
	names, commit = searchNames(t, ts.URL, "g", "NewEntity")
	if !contains(names, "NewEntity") || commit != commitB {
		t.Errorf("retry did not install: commit = %q (want %q), results = %v", commit, commitB, names)
	}
}
