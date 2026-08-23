package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commitC = "cccccccccccccccccccccccccccccccccccccccc"
)

// buildFixtureDB creates a small Kuzu database containing one entity and a
// provenance stamp. Kuzu opens are ~1s, so callers share one fixture.
func buildFixtureDB(t *testing.T) string {
	return buildFixtureDBNamed(t, "AuthService")
}

// buildFixtureDBNamed is buildFixtureDB with a caller-chosen entity name, for
// tests that need to tell two graphs' results apart.
func buildFixtureDBNamed(t *testing.T, entityName string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "platform.db")
	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	if _, err := store.CreateEntity(entityName, "function", "monorepo"); err != nil {
		t.Fatalf("create fixture entity: %v", err)
	}
	err = store.SetMeta(kglib.KGMeta{ProjectID: "monorepo", Commit: commitA, IndexedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("stamp fixture: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close fixture store: %v", err)
	}
	return dbPath
}

func newTestHub(t *testing.T, readToken, seedToken, kgVersion string) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	ts := httptest.NewServer(NewServer(dataDir, readToken, seedToken, kgVersion).Handler())
	t.Cleanup(ts.Close)
	return ts, dataDir
}

func pushFixture(t *testing.T, hubURL, graph, dbPath, commit string, layers []string) error {
	t.Helper()
	return Push(PushRequest{
		HubURL:    hubURL,
		Graph:     graph,
		DBPath:    dbPath,
		SeedToken: "s3cret",
		Meta: kglib.KGMeta{
			ProjectID: "monorepo",
			Commit:    commit,
			RepoURL:   "git@example.com:acme/monorepo.git",
			IndexedAt: time.Now().UTC(),
		},
		Layers: layers,
	})
}

func postJSON(t *testing.T, url string, body any) (*http.Response, []byte) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, buf.Bytes()
}

func TestHubPushListSearchPrune(t *testing.T) {
	dbPath := buildFixtureDB(t)
	ts, dataDir := newTestHub(t, "", "s3cret", "dev")

	if err := pushFixture(t, ts.URL, "platform", dbPath, commitA, nil); err != nil {
		t.Fatalf("push platform: %v", err)
	}
	if err := pushFixture(t, ts.URL, "team-a", dbPath, commitA, []string{"platform"}); err != nil {
		t.Fatalf("push team-a: %v", err)
	}

	t.Run("list", func(t *testing.T) {
		reg, err := ListGraphs(ts.URL, "")
		if err != nil {
			t.Fatalf("ListGraphs: %v", err)
		}
		platform, ok := reg.Graphs["platform"]
		if !ok {
			t.Fatal("platform missing from registry")
		}
		if platform.Commit != commitA || platform.ProjectID != "monorepo" {
			t.Errorf("platform info wrong: %+v", platform)
		}
		teamA, ok := reg.Graphs["team-a"]
		if !ok {
			t.Fatal("team-a missing from registry")
		}
		if len(teamA.Layers) != 1 || teamA.Layers[0] != "platform" {
			t.Errorf("team-a layers = %v, want [platform]", teamA.Layers)
		}
	})

	t.Run("storage layout", func(t *testing.T) {
		db := filepath.Join(dataDir, "graphs", "platform", commitA, "knowledge.db")
		if _, err := os.Stat(db); err != nil {
			t.Errorf("stored database missing: %v", err)
		}
		target, err := os.Readlink(filepath.Join(dataDir, "graphs", "platform", "current"))
		if err != nil {
			t.Fatalf("current symlink: %v", err)
		}
		if target != commitA {
			t.Errorf("current -> %q, want %q", target, commitA)
		}
	})

	t.Run("graph search", func(t *testing.T) {
		resp, body := postJSON(t, ts.URL+"/v1/graphs/team-a/search", map[string]any{"query": "AuthService"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search status = %d, body: %s", resp.StatusCode, body)
		}
		var out struct {
			Graph   string `json:"graph"`
			Commit  string `json:"commit"`
			Results []*knowledge.SearchResult
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("parse search response: %v", err)
		}
		if len(out.Results) == 0 {
			t.Fatal("expected at least one search result")
		}
		if out.Results[0].Entity.Name != "AuthService" {
			t.Errorf("result entity = %q, want AuthService", out.Results[0].Entity.Name)
		}

		resp, _ = postJSON(t, ts.URL+"/v1/graphs/nope/search", map[string]any{"query": "x"})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("unknown graph search status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("federated search", func(t *testing.T) {
		resp, body := postJSON(t, ts.URL+"/v1/search", map[string]any{"query": "AuthService"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("federated search status = %d, body: %s", resp.StatusCode, body)
		}
		var out struct {
			Results map[string]json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("parse federated response: %v", err)
		}
		if _, ok := out.Results["platform"]; !ok {
			t.Error("federated results missing platform")
		}
		if _, ok := out.Results["team-a"]; !ok {
			t.Error("federated results missing team-a")
		}

		resp, body = postJSON(t, ts.URL+"/v1/search", map[string]any{"query": "AuthService", "graphs": []string{"platform"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("restricted search status = %d, body: %s", resp.StatusCode, body)
		}
		out.Results = nil // json.Unmarshal merges into an existing map
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("parse restricted response: %v", err)
		}
		if len(out.Results) != 1 {
			t.Errorf("restricted results = %d graphs, want 1", len(out.Results))
		}
		if _, ok := out.Results["platform"]; !ok {
			t.Error("restricted results missing platform")
		}
	})

	t.Run("repush and prune", func(t *testing.T) {
		if err := pushFixture(t, ts.URL, "platform", dbPath, commitB, nil); err != nil {
			t.Fatalf("push commitB: %v", err)
		}
		target, err := os.Readlink(filepath.Join(dataDir, "graphs", "platform", "current"))
		if err != nil {
			t.Fatalf("current symlink: %v", err)
		}
		if target != commitB {
			t.Errorf("current -> %q, want %q", target, commitB)
		}

		if err := pushFixture(t, ts.URL, "platform", dbPath, commitC, nil); err != nil {
			t.Fatalf("push commitC: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(dataDir, "graphs", "platform"))
		if err != nil {
			t.Fatal(err)
		}
		var commitDirs []string
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				commitDirs = append(commitDirs, e.Name())
			}
		}
		sort.Strings(commitDirs)
		want := []string{commitB, commitC}
		sort.Strings(want)
		if !slices.Equal(commitDirs, want) {
			t.Errorf("commit dirs after third push = %v, want exactly %v", commitDirs, want)
		}
	})
}

// TestGraphSearchIncludeLayers verifies that include_layers expands a
// per-graph search to the graph's registered hub-side layers.
func TestGraphSearchIncludeLayers(t *testing.T) {
	platformDB := buildFixtureDBNamed(t, "PlatformCore")
	teamADB := buildFixtureDBNamed(t, "AuthService")
	ts, _ := newTestHub(t, "", "s3cret", "dev")

	if err := pushFixture(t, ts.URL, "platform", platformDB, commitA, nil); err != nil {
		t.Fatalf("push platform: %v", err)
	}
	if err := pushFixture(t, ts.URL, "team-a", teamADB, commitA, []string{"platform"}); err != nil {
		t.Fatalf("push team-a: %v", err)
	}

	type searchOut struct {
		Graph          string                    `json:"graph"`
		Results        []*knowledge.SearchResult `json:"results"`
		LayersSearched []string                  `json:"layers_searched"`
	}
	search := func(t *testing.T, body map[string]any) searchOut {
		t.Helper()
		resp, raw := postJSON(t, ts.URL+"/v1/graphs/team-a/search", body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search status = %d, body: %s", resp.StatusCode, raw)
		}
		var out searchOut
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("parse search response: %v", err)
		}
		return out
	}
	countNamed := func(results []*knowledge.SearchResult, name string) int {
		n := 0
		for _, r := range results {
			if r.Entity != nil && r.Entity.Name == name {
				n++
			}
		}
		return n
	}

	t.Run("include_layers reaches the layer graph", func(t *testing.T) {
		out := search(t, map[string]any{"query": "PlatformCore", "include_layers": true})
		if countNamed(out.Results, "PlatformCore") == 0 {
			t.Errorf("expected PlatformCore from the platform layer, got %d results", len(out.Results))
		}
		if !slices.Equal(out.LayersSearched, []string{"platform"}) {
			t.Errorf("layers_searched = %v, want [platform]", out.LayersSearched)
		}
	})

	t.Run("without include_layers only the graph itself is searched", func(t *testing.T) {
		out := search(t, map[string]any{"query": "PlatformCore"})
		if n := countNamed(out.Results, "PlatformCore"); n != 0 {
			t.Errorf("got %d PlatformCore results without include_layers, want 0", n)
		}
		if len(out.LayersSearched) != 0 {
			t.Errorf("layers_searched = %v, want empty", out.LayersSearched)
		}
	})
}

func TestHubAuth(t *testing.T) {
	dbPath := buildFixtureDB(t)

	t.Run("wrong seed token", func(t *testing.T) {
		ts, _ := newTestHub(t, "", "s3cret", "dev")
		err := Push(PushRequest{
			HubURL: ts.URL, Graph: "g", DBPath: dbPath, SeedToken: "wrong",
			Meta: kglib.KGMeta{ProjectID: "monorepo", Commit: commitA},
		})
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("push with wrong token: err = %v, want 401", err)
		}
	})

	t.Run("seeding disabled", func(t *testing.T) {
		ts, _ := newTestHub(t, "", "", "dev")
		err := Push(PushRequest{
			HubURL: ts.URL, Graph: "g", DBPath: dbPath, SeedToken: "anything",
			Meta: kglib.KGMeta{ProjectID: "monorepo", Commit: commitA},
		})
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("push to seed-disabled hub: err = %v, want 403", err)
		}
	})

	t.Run("read token", func(t *testing.T) {
		ts, _ := newTestHub(t, "r3ad", "s3cret", "dev")

		resp, err := http.Get(ts.URL + "/v1/graphs")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("no bearer: status = %d, want 401", resp.StatusCode)
		}

		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/graphs", nil)
		req.Header.Set("Authorization", "Bearer r3ad")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("with bearer: status = %d, want 200", resp.StatusCode)
		}

		resp, err = http.Get(ts.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("/healthz status = %d, want 200", resp.StatusCode)
		}
	})
}

// craftArchive builds a gzip'd tar with the given entries for attack tests.
func craftArchive(t *testing.T, entries []*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, hdr := range entries {
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(hdr.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestUnpackDBRejectsMaliciousArchives(t *testing.T) {
	t.Run("path traversal", func(t *testing.T) {
		buf := craftArchive(t, []*tar.Header{
			{Name: "../escape", Typeflag: tar.TypeReg, Size: 4, Mode: 0644},
		})
		err := UnpackDB(buf, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "escape") {
			t.Errorf("traversal archive: err = %v, want escape rejection", err)
		}
	})

	t.Run("symlink entry", func(t *testing.T) {
		buf := craftArchive(t, []*tar.Header{
			{Name: "knowledge.db", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0777},
		})
		err := UnpackDB(buf, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "unsupported type") {
			t.Errorf("symlink archive: err = %v, want type rejection", err)
		}
	})
}

func TestVersionGate(t *testing.T) {
	dbPath := buildFixtureDB(t)
	ts, _ := newTestHub(t, "", "s3cret", "1.4.0")

	err := Push(PushRequest{
		HubURL: ts.URL, Graph: "g", DBPath: dbPath, SeedToken: "s3cret",
		Meta: kglib.KGMeta{ProjectID: "monorepo", Commit: commitA, KGVersion: "1.5.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("mismatched version push: err = %v, want 409", err)
	}
	if !strings.Contains(err.Error(), "1.4.0") || !strings.Contains(err.Error(), "1.5.0") {
		t.Errorf("409 message should name both versions, got: %v", err)
	}

	// Same major.minor passes the gate.
	err = Push(PushRequest{
		HubURL: ts.URL, Graph: "g", DBPath: dbPath, SeedToken: "s3cret",
		Meta: kglib.KGMeta{ProjectID: "monorepo", Commit: commitA, KGVersion: "1.4.7"},
	})
	if err != nil {
		t.Errorf("matching major.minor push failed: %v", err)
	}
}

func TestPackUnpackRoundTrip(t *testing.T) {
	src := t.TempDir()
	dbFile := filepath.Join(src, "platform.db")
	if err := os.WriteFile(dbFile, []byte("kuzu-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbFile+".wal", []byte("wal-bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := PackDB(&buf, dbFile); err != nil {
		t.Fatalf("PackDB: %v", err)
	}
	dest := t.TempDir()
	if err := UnpackDB(&buf, dest); err != nil {
		t.Fatalf("UnpackDB: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "knowledge.db"))
	if err != nil || string(got) != "kuzu-bytes" {
		t.Errorf("knowledge.db = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dest, "knowledge.db.wal"))
	if err != nil || string(got) != "wal-bytes" {
		t.Errorf("knowledge.db.wal = %q, %v", got, err)
	}
}

// rawSeed sends a PUT seed request with arbitrary graph path segment and
// headers, bypassing the client's own validation, to exercise the server's
// hostile-metadata handling. The body is irrelevant: validation must reject
// the request before staging.
func rawSeed(t *testing.T, hubURL, graphSegment, commit string, extraHeaders map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, hubURL+"/v1/graphs/"+graphSegment, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("X-KG-Commit", commit)
	req.Header.Set("X-KG-Project-ID", "monorepo")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", graphSegment, err)
	}
	resp.Body.Close()
	return resp
}

// TestSeedRejectsHostileMetadata covers the metadata-as-path attacks: commit
// hashes and graph/layer names are joined into filesystem paths and symlink
// targets, so traversal sequences must be rejected before any path is formed.
func TestSeedRejectsHostileMetadata(t *testing.T) {
	ts, dataDir := newTestHub(t, "", "s3cret", "dev")

	assertDataDirIntact := func(t *testing.T) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(dataDir, "evil")); err == nil {
			t.Fatal("traversal escaped: dataDir/evil exists")
		}
		if entries, err := os.ReadDir(dataDir); err == nil {
			for _, e := range entries {
				if e.Name() != "graphs" && e.Name() != "registry.json" {
					t.Errorf("unexpected entry in data dir: %s", e.Name())
				}
			}
		}
	}

	t.Run("commit traversal", func(t *testing.T) {
		for _, commit := range []string{"../../evil", "..", ".", "a/b", "%2e%2e"} {
			resp := rawSeed(t, ts.URL, "g", commit, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("commit %q: status = %d, want 400", commit, resp.StatusCode)
			}
		}
		assertDataDirIntact(t)
	})

	t.Run("dot-dot graph name", func(t *testing.T) {
		// %2e%2e survives URL cleaning and decodes to ".." in PathValue —
		// the literal "../" form is normalized away by path cleaning before
		// routing, so the encoded form is the one that reaches the handler.
		for _, seg := range []string{"%2e%2e", "%2e", "..%2fx"} {
			resp := rawSeed(t, ts.URL, seg, commitA, nil)
			if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound &&
				resp.StatusCode != http.StatusMovedPermanently {
				t.Errorf("graph segment %q: status = %d, want 400/404/301", seg, resp.StatusCode)
			}
		}
		// Whatever the router did, nothing may have escaped or destroyed data.
		assertDataDirIntact(t)
		if _, err := os.Stat(filepath.Join(dataDir, "graphs")); os.IsNotExist(err) {
			// graphs/ may legitimately not exist yet (no successful seed);
			// the point is the registry must not have been deleted by prune.
			t.Log("graphs/ absent (no successful seed) — acceptable")
		}
	})

	t.Run("hostile layer names", func(t *testing.T) {
		resp := rawSeed(t, ts.URL, "g", commitA, map[string]string{"X-KG-Layers": "ok,../bad"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("hostile layer name: status = %d, want 400", resp.StatusCode)
		}
		assertDataDirIntact(t)
	})

	t.Run("search path validation", func(t *testing.T) {
		resp, _ := postJSON(t, ts.URL+"/v1/graphs/%2e%2e/search", map[string]any{"query": "x"})
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("search on ..: status = %d, want 400/404", resp.StatusCode)
		}
	})
}

// TestSweepStaging verifies that stranded staging directories are removed at
// server construction.
func TestSweepStaging(t *testing.T) {
	dataDir := t.TempDir()
	gdir := filepath.Join(dataDir, "graphs", "g")
	for _, n := range []string{".tmp-dead", ".old-dead", "current.tmp-dead", commitA} {
		if err := os.MkdirAll(filepath.Join(gdir, n), 0755); err != nil {
			t.Fatal(err)
		}
	}
	_ = NewServer(dataDir, "", "s3cret", "dev")
	entries, err := os.ReadDir(gdir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{commitA}) {
		t.Errorf("after sweep: %v, want only [%s]", names, commitA)
	}
}

// TestIncludeLayersCycleAndDedup pins two expansion behaviors the happy-path
// test misses: mutually-referential layer registrations must terminate, and a
// duplicate entity (same ID via the same pushed database) must appear once in
// the merged results.
func TestIncludeLayersCycleAndDedup(t *testing.T) {
	dbPath := buildFixtureDBNamed(t, "CycleEntity")
	ts, _ := newTestHub(t, "", "s3cret", "dev")

	// a and b are the same database pushed twice, each registered as the
	// other's layer: a cycle with guaranteed duplicate entity IDs.
	if err := pushFixture(t, ts.URL, "cyc-a", dbPath, commitA, []string{"cyc-b"}); err != nil {
		t.Fatalf("push cyc-a: %v", err)
	}
	if err := pushFixture(t, ts.URL, "cyc-b", dbPath, commitA, []string{"cyc-a"}); err != nil {
		t.Fatalf("push cyc-b: %v", err)
	}

	resp, body := postJSON(t, ts.URL+"/v1/graphs/cyc-a/search",
		map[string]any{"query": "CycleEntity", "include_layers": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d, body: %s", resp.StatusCode, body)
	}
	var out struct {
		Results        []*knowledge.SearchResult `json:"results"`
		LayersSearched []string                  `json:"layers_searched"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	// Expansion terminated (we got an answer at all) and visited cyc-b once.
	if !slices.Equal(out.LayersSearched, []string{"cyc-b"}) {
		t.Errorf("layers_searched = %v, want [cyc-b]", out.LayersSearched)
	}
	// Same entity ID in both graphs must merge to a single result.
	seen := map[string]int{}
	for _, r := range out.Results {
		seen[r.Entity.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("entity %s appears %d times, want deduplicated to 1", id, n)
		}
	}
	if len(out.Results) == 0 {
		t.Fatal("expected at least one result")
	}
}
