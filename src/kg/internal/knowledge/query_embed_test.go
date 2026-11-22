package knowledge

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/cortexa-llc/mcp/kglib"
)

// stubEmbedSource stands in for a Store so the gate can be tested without a
// Kuzu database.
type stubEmbedSource struct {
	has      bool
	hasErr   error
	meta     *kglib.KGMeta
	metaErr  error
	probes   int
	metaRead int
}

func (s *stubEmbedSource) HasEmbeddings(string) (bool, error) {
	s.probes++
	return s.has, s.hasErr
}

func (s *stubEmbedSource) GetMeta(string) (*kglib.KGMeta, error) {
	s.metaRead++
	return s.meta, s.metaErr
}

// fakeEmbedder records what it was asked to embed and returns a fixed vector,
// so the tests below observe the decision rather than a network outcome.
type fakeEmbedder struct {
	model  string
	calls  [][]string
	err    error
	vector []float32
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls = append(f.calls, texts)
	vector := f.vector
	if vector == nil {
		vector = []float32{0.1, 0.2, 0.3}
	}
	// A vector alongside the error is deliberate for the failure cases: with
	// an empty result the error check and the length check below it are
	// indistinguishable, and a test cannot tell which one is doing the work.
	return [][]float32{vector}, f.err
}

func (f *fakeEmbedder) Dimensions() int { return 3 }
func (f *fakeEmbedder) Model() string   { return f.model }

// withFakeEmbedder substitutes the provider for the duration of a test and
// returns the recorder, along with the model names it was constructed for.
func withFakeEmbedder(t *testing.T, fake *fakeEmbedder, ctorErr error) *[]string {
	t.Helper()
	var models []string
	original := newEmbedderForModel
	newEmbedderForModel = func(model string) (Embedder, error) {
		models = append(models, model)
		if ctorErr != nil {
			return nil, ctorErr
		}
		fake.model = model
		return fake, nil
	}
	t.Cleanup(func() { newEmbedderForModel = original })
	return &models
}

// TestSearchEmbedding_SkipsUnembeddedGraph is the cost gate: a graph with no
// vectors can only produce an empty vector search, so the query must never
// reach a paid embedding API.
func TestSearchEmbedding_SkipsUnembeddedGraph(t *testing.T) {
	fake := &fakeEmbedder{}
	models := withFakeEmbedder(t, fake, nil)

	src := &stubEmbedSource{has: false}
	if got := SearchEmbedding(src, "p", "why kuzu"); got != nil {
		t.Errorf("embedded a query against a graph with no vectors (got %d dims)", len(got))
	}
	if len(*models) != 0 {
		t.Errorf("constructed a provider for an unembedded graph: %v", *models)
	}
	if len(fake.calls) != 0 {
		t.Errorf("sent %d embedding request(s) for an unembedded graph", len(fake.calls))
	}
	if src.probes != 1 {
		t.Errorf("expected exactly 1 embedding-presence probe, got %d", src.probes)
	}
}

// TestSearchEmbedding_EmbedsWhenTheGraphHasVectors is the positive case: the
// query is embedded once, with the graph's own model, and the vector is
// returned for VectorSearch to use.
func TestSearchEmbedding_EmbedsWhenTheGraphHasVectors(t *testing.T) {
	fake := &fakeEmbedder{vector: []float32{0.4, 0.5, 0.6}}
	models := withFakeEmbedder(t, fake, nil)

	src := &stubEmbedSource{has: true, meta: &kglib.KGMeta{EmbedModel: "text-embedding-3-small"}}
	got := SearchEmbedding(src, "p", "why kuzu")
	if len(got) != 3 || got[0] != 0.4 {
		t.Fatalf("expected the provider's vector, got %v", got)
	}
	if len(fake.calls) != 1 || len(fake.calls[0]) != 1 || fake.calls[0][0] != "why kuzu" {
		t.Errorf("expected one request carrying the query, got %v", fake.calls)
	}
	if len(*models) != 1 || (*models)[0] != "text-embedding-3-small" {
		t.Errorf("provider built for %v; want the model the graph was built with", *models)
	}
}

// TestSearchEmbedding_NoProviderIsNotAnError covers the default state of most
// checkouts: no API key. Search must fall back to keyword silently rather than
// failing or reporting on every query.
func TestSearchEmbedding_NoProviderIsNotAnError(t *testing.T) {
	withFakeEmbedder(t, &fakeEmbedder{}, errors.New("no API key"))

	src := &stubEmbedSource{has: true, meta: &kglib.KGMeta{EmbedModel: "text-embedding-3-small"}}
	if got := SearchEmbedding(src, "p", "why kuzu"); got != nil {
		t.Errorf("returned a vector with no provider configured (got %d dims)", len(got))
	}
}

// TestSearchEmbedding_ProviderFailureFallsBack covers a configured provider
// that does not answer — an unreachable Ollama host, a rate-limited API. The
// search degrades to keyword rather than failing.
func TestSearchEmbedding_ProviderFailureFallsBack(t *testing.T) {
	fake := &fakeEmbedder{err: errors.New("connection refused")}
	withFakeEmbedder(t, fake, nil)

	src := &stubEmbedSource{has: true}
	if got := SearchEmbedding(src, "p", "why kuzu"); got != nil {
		t.Error("a failing provider should degrade to keyword search, not produce a vector")
	}
}

// TestSearchEmbedding_EmptyQuery guards against spending a provider call on
// nothing.
func TestSearchEmbedding_EmptyQuery(t *testing.T) {
	fake := &fakeEmbedder{}
	withFakeEmbedder(t, fake, nil)

	src := &stubEmbedSource{has: true}
	if got := SearchEmbedding(src, "p", ""); got != nil {
		t.Error("embedded an empty query")
	}
	if src.probes != 0 {
		t.Errorf("probed the graph for an empty query (%d probes)", src.probes)
	}
	if len(fake.calls) != 0 {
		t.Errorf("sent %d embedding request(s) for an empty query", len(fake.calls))
	}
}

// TestSearchEmbedding_ProbeFailureFallsBack keeps a broken or legacy database
// searchable: if the presence probe errors, keyword search still runs and no
// provider call is made.
func TestSearchEmbedding_ProbeFailureFallsBack(t *testing.T) {
	fake := &fakeEmbedder{}
	withFakeEmbedder(t, fake, nil)

	// has=true alongside the error isolates the error check: with has=false
	// the fallback would happen anyway and the test would prove nothing.
	src := &stubEmbedSource{has: true, hasErr: os.ErrInvalid}
	if got := SearchEmbedding(src, "p", "anything"); got != nil {
		t.Error("probe failure should degrade to keyword search, not produce a vector")
	}
	if len(fake.calls) != 0 {
		t.Errorf("sent %d embedding request(s) after a failed probe", len(fake.calls))
	}
}

// TestSearchEmbedding_ChecksTheGraphBeforeTheModel pins the order of the two
// store reads: a graph with nothing embedded must cost one cheap probe and
// nothing else.
func TestSearchEmbedding_ChecksTheGraphBeforeTheModel(t *testing.T) {
	withFakeEmbedder(t, &fakeEmbedder{}, nil)

	src := &stubEmbedSource{has: false, meta: &kglib.KGMeta{EmbedModel: "text-embedding-3-small"}}
	SearchEmbedding(src, "p", "why kuzu")
	if src.metaRead != 0 {
		t.Error("read the model from meta before establishing there is anything to search")
	}
}

// TestSearchEmbedding_NilStoreUsesTheEnvironment covers a surface with no
// handle on the primary database: no probe is attempted and the environment
// names the model.
func TestSearchEmbedding_NilStoreUsesTheEnvironment(t *testing.T) {
	t.Setenv("KNOWLEDGE_EMBED_MODEL", "ollama:nomic-embed-text")
	fake := &fakeEmbedder{}
	models := withFakeEmbedder(t, fake, nil)

	if got := SearchEmbedding(nil, "p", "retention"); got == nil {
		t.Fatal("expected a vector: a nil store means the probe is skipped, not that search degrades")
	}
	if len(*models) != 1 || (*models)[0] != "ollama:nomic-embed-text" {
		t.Errorf("provider built for %v; want the environment's model", *models)
	}
}

// TestResolveEmbedModel_PrefersTheStoredModel pins model precedence. A query
// vector built with a different model than the stored vectors is noise that
// ranks like signal, so the graph's own record must beat the environment.
func TestResolveEmbedModel_PrefersTheStoredModel(t *testing.T) {
	t.Setenv("KNOWLEDGE_EMBED_MODEL", "ollama:nomic-embed-text")

	src := &stubEmbedSource{has: true, meta: &kglib.KGMeta{EmbedModel: "text-embedding-3-small"}}
	if got := resolveEmbedModel(src, "p"); got != "text-embedding-3-small" {
		t.Errorf("resolved %q; want the model the graph was built with", got)
	}
	if src.metaRead != 1 {
		t.Errorf("expected the stored model to be consulted once, got %d reads", src.metaRead)
	}
}

// TestResolveEmbedModel_FallsBackToEnv covers databases that predate the meta
// stamp, and stores that never recorded a model.
func TestResolveEmbedModel_FallsBackToEnv(t *testing.T) {
	t.Setenv("KNOWLEDGE_EMBED_MODEL", "ollama:nomic-embed-text")

	for name, src := range map[string]*stubEmbedSource{
		"no meta row":     {has: true, meta: nil},
		"empty model":     {has: true, meta: &kglib.KGMeta{}},
		"meta read error": {has: true, metaErr: os.ErrInvalid},
	} {
		if got := resolveEmbedModel(src, "p"); got != "ollama:nomic-embed-text" {
			t.Errorf("%s: resolved %q; want the environment's model", name, got)
		}
	}

	if got := resolveEmbedModel(nil, "p"); got != "ollama:nomic-embed-text" {
		t.Errorf("nil store: resolved %q; want the environment's model", got)
	}
}
