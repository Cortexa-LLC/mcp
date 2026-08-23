package remote

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cortexa-llc/mcp/kglib"
)

// fakeHub records the last request and serves canned results.
type fakeHub struct {
	t           *testing.T
	lastPath    string
	lastMethod  string
	lastAuth    string
	lastBody    map[string]any
	status      int
	responseRaw string
}

func (f *fakeHub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.lastPath = r.URL.Path
		f.lastMethod = r.Method
		f.lastAuth = r.Header.Get("Authorization")
		f.lastBody = map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&f.lastBody); err != nil {
			f.t.Errorf("decode request body: %v", err)
		}
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(f.responseRaw))
	}
}

const cannedResults = `{
	"graph": "platform",
	"commit": "abc123",
	"project_id": "platform-proj",
	"results": [
		{"entity": {"id": "e1", "name": "PlatformCore"}, "score": 0.9, "match_type": "keyword"},
		{"entity": {"id": "e2", "name": "PlatformUtil"}, "score": 0.5, "match_type": "keyword"}
	]
}`

func TestHybridSearchRequestAndDecode(t *testing.T) {
	fake := &fakeHub{t: t, responseRaw: cannedResults}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	l := NewLayer(srv.URL, "platform", "s3cret")
	cfg := kglib.DefaultSearchConfig()
	cfg.Limit = 7
	results, err := l.HybridSearch("ignored-project", "PlatformCore", nil, cfg)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}

	if fake.lastPath != "/v1/graphs/platform/search" {
		t.Errorf("path = %q, want /v1/graphs/platform/search", fake.lastPath)
	}
	if fake.lastMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", fake.lastMethod)
	}
	if fake.lastAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want \"Bearer s3cret\"", fake.lastAuth)
	}
	if q := fake.lastBody["query"]; q != "PlatformCore" {
		t.Errorf("body query = %v, want PlatformCore", q)
	}
	if lim := fake.lastBody["limit"]; lim != float64(7) {
		t.Errorf("body limit = %v, want 7", lim)
	}
	if il := fake.lastBody["include_layers"]; il != true {
		t.Errorf("body include_layers = %v, want true", il)
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Entity == nil || results[0].Entity.Name != "PlatformCore" {
		t.Errorf("results[0] = %+v, want entity PlatformCore", results[0])
	}
	if results[0].Score != 0.9 {
		t.Errorf("results[0].Score = %v, want 0.9", results[0].Score)
	}
}

func TestHybridSearchNoTokenOmitsAuthHeader(t *testing.T) {
	fake := &fakeHub{t: t, responseRaw: `{"results":[]}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	l := NewLayer(srv.URL, "platform", "")
	if _, err := l.HybridSearch("", "x", nil, kglib.DefaultSearchConfig()); err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if fake.lastAuth != "" {
		t.Errorf("Authorization = %q, want absent", fake.lastAuth)
	}
}

func TestHybridSearchZeroLimitOmitted(t *testing.T) {
	fake := &fakeHub{t: t, responseRaw: `{"results":[]}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	l := NewLayer(srv.URL, "platform", "")
	cfg := kglib.SearchConfig{} // Limit 0 → let the server default
	if _, err := l.HybridSearch("", "x", nil, cfg); err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if _, present := fake.lastBody["limit"]; present {
		t.Errorf("body limit present (%v), want omitted for zero limit", fake.lastBody["limit"])
	}
}

func TestHybridSearchServerError(t *testing.T) {
	fake := &fakeHub{t: t, status: http.StatusInternalServerError}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	l := NewLayer(srv.URL, "platform", "")
	if _, err := l.HybridSearch("", "x", nil, kglib.DefaultSearchConfig()); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestHybridSearchConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens here any more

	l := NewLayer(url, "platform", "")
	if _, err := l.HybridSearch("", "x", nil, kglib.DefaultSearchConfig()); err == nil {
		t.Fatal("expected error on connection refused, got nil")
	}
}

func TestHybridSearchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the request past the client timeout
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	l := NewLayer(srv.URL, "platform", "")
	l.client.Timeout = 100 * time.Millisecond // same-package: shorten for the test

	start := time.Now()
	_, err := l.HybridSearch("", "x", nil, kglib.DefaultSearchConfig())
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timed out after %v; the client timeout did not bound the request", elapsed)
	}
}

func TestNewLayerNormalizesURL(t *testing.T) {
	cases := map[string]string{
		"hub.example.com":         "http://hub.example.com",
		"hub.example.com/":        "http://hub.example.com",
		"https://hub.example.com": "https://hub.example.com",
		"http://hub:7411/":        "http://hub:7411",
	}
	for in, want := range cases {
		if got := NewLayer(in, "g", "").hubURL; got != want {
			t.Errorf("NewLayer(%q).hubURL = %q, want %q", in, got, want)
		}
	}
}

// TestHybridSearchFiltersNilEntities pins the guard against a buggy or hostile
// hub: results lacking an entity must be dropped, not passed to the federation
// merge (which dereferences Entity unconditionally and would panic every
// consuming client).
func TestHybridSearchFiltersNilEntities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"score":1},null,{"entity":null,"score":2},{"entity":{"id":"e1","name":"Real"},"score":3}]}`)
	}))
	defer srv.Close()

	l := NewLayer(srv.URL, "g", "")
	results, err := l.HybridSearch("pid", "q", nil, kglib.SearchConfig{Limit: 10})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (nil-entity entries dropped)", len(results))
	}
	if results[0].Entity.Name != "Real" {
		t.Errorf("surviving result = %q, want Real", results[0].Entity.Name)
	}
}
