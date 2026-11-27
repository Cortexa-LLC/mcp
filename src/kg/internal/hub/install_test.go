package hub

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

// buildFixtureDBFor builds a fixture database that answers for a caller-chosen
// project. The project ID matters here: the hub resolves the database from the
// commit recorded in the registry and searches it with that entry's ProjectID,
// so a graph whose registry entry and installed database disagree returns
// nothing at all.
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

// registeredCommit returns the commit registry.json records for a graph. That
// is the hub's only record of which commit is live, and what the database path
// is derived from, so it is what these tests assert against.
func registeredCommit(t *testing.T, dataDir, graph string) string {
	t.Helper()
	reg, err := loadRegistry(dataDir)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	info, ok := reg.graph(graph)
	if !ok {
		t.Fatalf("registry has no entry for %q", graph)
	}
	return info.Commit
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
// hub serves. After the failed push the hub must still answer the query it
// could answer before, which holds only if the registry still names the
// previously installed commit and the staged directory has been rolled back.
//
// This used to be a test about two records agreeing. install() repointed a
// `current` symlink and deleted the stashed directory before writing the
// registry, and a registry error returned without undoing either — so the hub
// served the new database while querying it with the old project ID, and every
// search came back empty. The registry is now the only record of the live
// commit, which makes that particular split unrepresentable; what remains worth
// testing is that a failed registry write leaves the filesystem as it was.
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
			"searching %q for OldEntity returned %v — the registry no longer names the "+
			"commit whose database is installed", commit, names)
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

// A hard kill mid-install — the new commit's directory moved into place, the
// registry not yet written — used to leave a repointed `current` naming a
// commit the registry never recorded, so search read one commit's database
// with another commit's ProjectID: HTTP 200 with zero results, permanently.
//
// With the registry as the only record of which commit is live, that state is
// unreachable. The orphaned directory is inert: the registered commit still
// names the database that was serving before the crash, so the graph keeps
// answering with no repair pass involved. Reconciliation deliberately leaves
// the orphan alone — deleting data is not a job for a constructor — and the
// next successful install's prune collects it.
func TestInterruptedSeedKeepsServingRegisteredCommit(t *testing.T) {
	dataDir := t.TempDir()
	ts := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())

	commit := strings.Repeat("a", 40)
	dbPath := buildFixtureDBFor(t, "ReconcileEntity", "proj-one", commit)
	if err := pushFixtureFor(t, ts.URL, "recon", dbPath, commit, "proj-one"); err != nil {
		t.Fatalf("push fixture: %v", err)
	}
	if names, _ := searchNames(t, ts.URL, "recon", "ReconcileEntity"); len(names) == 0 {
		t.Fatal("fixture graph does not answer before the simulated crash")
	}
	ts.Close()

	// Simulate the crash: a new commit's directory exists, but the process died
	// before the registry recorded it. The directory is left empty on purpose —
	// it is a tripwire. If anything resolved the database from the newest
	// directory on disk rather than from the registry, the search below would
	// fail against a missing database instead of silently passing.
	gdir := filepath.Join(dataDir, "graphs", "recon")
	orphan := strings.Repeat("b", 40)
	if err := os.MkdirAll(filepath.Join(gdir, orphan), 0o755); err != nil {
		t.Fatalf("create orphan commit dir: %v", err)
	}

	// Restart the hub over the same data dir.
	ts2 := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())
	defer ts2.Close()

	if got := registeredCommit(t, dataDir, "recon"); got != commit {
		t.Errorf("registered commit = %q, want the pre-crash %q", got, commit)
	}
	names, gotCommit := searchNames(t, ts2.URL, "recon", "ReconcileEntity")
	if len(names) == 0 || names[0] != "ReconcileEntity" {
		t.Errorf("search after restart = %v, want [ReconcileEntity]", names)
	}
	if gotCommit != commit {
		t.Errorf("served commit = %q, want %q", gotCommit, commit)
	}
	if _, err := os.Stat(filepath.Join(gdir, orphan)); err != nil {
		t.Errorf("orphaned commit directory was removed at startup (%v); prune owns that, not reconcile", err)
	}
}

// A registry entry whose commit directory is absent — a partial copy, a
// restored backup, an operator with a delete key — is the one inconsistency
// reconciliation can still see and cannot fix. It must be reported at startup
// rather than discovered one failed search at a time, and it must not stop the
// hub from serving the graphs that are intact.
//
// This replaced a test for a missing `current` symlink, which the registry-only
// layout makes unrepresentable.
func TestReconcileReportsMissingCommitDirectory(t *testing.T) {
	dataDir := t.TempDir()
	ts := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())

	commit := strings.Repeat("c", 40)
	dbPath := buildFixtureDBFor(t, "MissingDirEntity", "proj-mc", commit)
	if err := pushFixtureFor(t, ts.URL, "mc", dbPath, commit, "proj-mc"); err != nil {
		t.Fatalf("push fixture: %v", err)
	}

	intact := strings.Repeat("d", 40)
	intactDB := buildFixtureDBFor(t, "IntactEntity", "proj-ok", intact)
	if err := pushFixtureFor(t, ts.URL, "ok", intactDB, intact, "proj-ok"); err != nil {
		t.Fatalf("push intact fixture: %v", err)
	}
	ts.Close()

	if err := os.RemoveAll(filepath.Join(dataDir, "graphs", "mc", commit)); err != nil {
		t.Fatalf("remove commit directory: %v", err)
	}

	// reconcileInstalls runs in the constructor and reports through the log.
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	ts2 := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())
	log.SetOutput(prev)
	defer ts2.Close()

	out := logged.String()
	if !strings.Contains(out, "mc") || !strings.Contains(out, "missing") {
		t.Errorf("startup log does not report the missing directory for %q:\n%s", "mc", out)
	}

	// The damaged graph must not take the healthy one down with it.
	if names, _ := searchNames(t, ts2.URL, "ok", "IntactEntity"); !contains(names, "IntactEntity") {
		t.Errorf("intact graph = %v, want it still served alongside a damaged one", names)
	}
}

// registry.json is hand-editable, and a null entry unmarshals to a nil
// *GraphInfo. reconcileInstalls runs in the constructor, so dereferencing it
// there does not fail one request — it aborts `kg hub serve` before any graph
// is served. The bad entry must be skipped, and every other graph must still
// answer.
func TestReconcileSurvivesNullRegistryEntry(t *testing.T) {
	dataDir := t.TempDir()
	ts := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())

	commit := strings.Repeat("d", 40)
	dbPath := buildFixtureDBFor(t, "SurvivorEntity", "proj-null", commit)
	if err := pushFixtureFor(t, ts.URL, "survivor", dbPath, commit, "proj-null"); err != nil {
		t.Fatalf("push fixture: %v", err)
	}
	ts.Close()

	// Hand-edit the registry to hold a null entry alongside the good one.
	regPath := filepath.Join(dataDir, registryFile)
	raw, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var reg map[string]map[string]any
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	reg["graphs"]["broken"] = nil
	edited, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.WriteFile(regPath, edited, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	// Construction must not panic...
	ts2 := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())
	defer ts2.Close()

	// ...and the healthy graph must still be served.
	if names, _ := searchNames(t, ts2.URL, "survivor", "SurvivorEntity"); len(names) == 0 {
		t.Error("healthy graph stopped answering after a null registry entry was introduced")
	}
}

// The constructor-time guard above is only half the hazard. A null entry for a
// name a client actually asks for reaches the request handlers, where the
// lookups checked `ok` but not the pointer — so `{"graphs":{"broken":null}}`
// would panic on the first dereference rather than answering "unknown graph".
//
// Searching the null graph directly must fail cleanly, and searching a healthy
// graph in the same request must be unaffected.
func TestRequestHandlersSurviveNullRegistryEntry(t *testing.T) {
	dataDir := t.TempDir()
	ts := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())

	commit := strings.Repeat("e", 40)
	dbPath := buildFixtureDBFor(t, "SurvivorEntity", "proj-null2", commit)
	if err := pushFixtureFor(t, ts.URL, "survivor", dbPath, commit, "proj-null2"); err != nil {
		t.Fatalf("push fixture: %v", err)
	}
	ts.Close()

	regPath := filepath.Join(dataDir, registryFile)
	raw, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var reg map[string]map[string]any
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	reg["graphs"]["broken"] = nil
	edited, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.WriteFile(regPath, edited, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	ts2 := httptest.NewServer(NewServer(dataDir, "", "s3cret", "dev").Handler())
	defer ts2.Close()

	// Asking for the null graph by name must produce 404 "unknown graph".
	//
	// Asserting 404 specifically, not merely "not 200": net/http recovers a
	// handler panic into a 500, so a weaker check passes whether the lookup is
	// nil-safe or crashes. 404 is reachable only by the guard actually
	// treating a null entry as absent.
	resp, body := postJSON(t, ts2.URL+"/v1/graphs/broken/search", map[string]any{"query": "anything"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("null graph answered %d, want %d (unknown graph). A 500 means the "+
			"handler panicked and net/http recovered it. Body: %s",
			resp.StatusCode, http.StatusNotFound, strings.TrimSpace(string(body)))
	}

	// The healthy graph must still answer afterwards.
	if names, _ := searchNames(t, ts2.URL, "survivor", "SurvivorEntity"); len(names) == 0 {
		t.Error("healthy graph stopped answering after a null entry was requested")
	}
}
