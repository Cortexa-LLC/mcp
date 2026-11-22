package kglib

import (
	"fmt"
	"math"
	"testing"
)

// embeddingDims is the width of the embedding columns in the schema
// (FLOAT[1536]); Kuzu rejects a list of any other length on write, so test
// vectors have to be full width.
const embeddingDims = 1536

// vec builds a full-width unit-ish vector whose leading components are the
// given values and whose remainder is zero, so tests can express direction
// with two or three numbers.
func vec(leading ...float32) []float32 {
	v := make([]float32, embeddingDims)
	copy(v, leading)
	return v
}

// unitVec builds a full-width unit vector rotated by theta radians in the
// first two dimensions. Expressing test vectors as angles gives a spread of
// genuinely distinct directions with a known similarity ordering — the shape
// real section embeddings have, and the shape approximate search can traverse.
func unitVec(theta float64) []float32 {
	v := make([]float32, embeddingDims)
	v[0] = float32(math.Cos(theta))
	v[1] = float32(math.Sin(theta))
	return v
}

// TestVectorSearch_ReachesObservationVectors is the regression test for
// semantic search being unable to see prose.
//
// Entity vectors are embeddings of "<type>: <name>" (BatchEmbed), so an index
// built from those alone can only match a query against a symbol name. Every
// piece of prose in the graph — PDF text, markdown section bodies, review
// findings — lives in observations, whose vectors were written by
// SetObservationEmbedding and read by nothing.
func TestVectorSearch_ReachesObservationVectors(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	const pid = "p"

	// The entity's own name says nothing about the query, and it carries no
	// entity vector at all: it is reachable only through its observation.
	doc, err := store.CreateEntity("docs/adr-0007.md", "file", pid)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	obs, err := store.CreateObservation(doc.ID,
		"Kuzu was chosen over SQLite because the traversals are the query.", pid)
	if err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	prose := vec(0.9, 0.1, 0.0)

	// A decoy entity WITH an entity vector pointing elsewhere, so a passing
	// result cannot come from the index being otherwise empty.
	decoy, err := store.CreateEntity("unrelated.go", "file", pid)
	if err != nil {
		t.Fatalf("CreateEntity decoy: %v", err)
	}
	if err := store.SetEmbedding(decoy.ID, pid, vec(0.0, 0.0, 1.0)); err != nil {
		t.Fatalf("SetEmbedding decoy: %v", err)
	}

	// Search BEFORE the observation vector exists, which builds and caches the
	// HNSW index. The write below must invalidate it — an observation vector is
	// a node in that index, so a write that skipped invalidation would leave
	// the prose unsearchable for the life of the process.
	if _, err := store.VectorSearch(pid, prose, 5); err != nil {
		t.Fatalf("VectorSearch (warming the index): %v", err)
	}

	if err := store.SetObservationEmbedding(obs.ID, pid, prose); err != nil {
		t.Fatalf("SetObservationEmbedding: %v", err)
	}

	results, err := store.VectorSearch(pid, prose, 5)
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("vector search found nothing for a query matching an observation vector")
	}
	if results[0].Entity.ID != doc.ID {
		t.Errorf("top hit is %q, want the entity whose observation matched (%q)",
			results[0].Entity.Name, doc.Name)
	}
}

// TestVectorSearch_DeduplicatesByEntity pins the three mechanisms that keep a
// result page useful once one entity owns many vectors. Measured against the
// HNSW index directly, a second document is reachable only when all three
// hold: the per-entity collapse, the widening neighbour search, and an
// EfSearch large enough to explore past the first document's sections. Drop
// any one and this page contains docs/long.md alone.
//
// The section vectors are spread over an angular range rather than clustered
// at one point, which is what real section embeddings look like. Forty
// near-identical vectors form a clique the index cannot see past at any
// setting — that is an inherent limit of approximate search, not something
// this code can fix, and a test built on it would assert the impossible.
func TestVectorSearch_DeduplicatesByEntity(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	const pid = "p"
	query := unitVec(0)

	// A long document: 20 sections, every one of them a closer match to the
	// query than the second document's single section.
	long, err := store.CreateEntity("docs/long.md", "file", pid)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	for i := 0; i < 20; i++ {
		obs, err := store.CreateObservation(long.ID, fmt.Sprintf("section %d prose", i), pid)
		if err != nil {
			t.Fatalf("CreateObservation: %v", err)
		}
		if err := store.SetObservationEmbedding(obs.ID, pid, unitVec(float64(i)*0.02)); err != nil {
			t.Fatalf("SetObservationEmbedding: %v", err)
		}
	}

	// A second document, ranked behind all 20 of them.
	other, err := store.CreateEntity("docs/other.md", "file", pid)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	otherObs, err := store.CreateObservation(other.ID, "the other document", pid)
	if err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.SetObservationEmbedding(otherObs.ID, pid, unitVec(0.45)); err != nil {
		t.Fatalf("SetObservationEmbedding: %v", err)
	}

	results, err := store.VectorSearch(pid, query, 2)
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}

	seen := map[string]int{}
	for _, r := range results {
		seen[r.Entity.ID]++
	}
	if seen[long.ID] > 1 {
		t.Errorf("entity appeared %d times in one result page; results must be per entity",
			seen[long.ID])
	}
	if seen[other.ID] == 0 {
		t.Errorf("the second document was crowded out by the first document's sections (got %d results)",
			len(results))
	}
}

// TestHasEmbeddings covers the gate that decides whether a query is worth
// embedding at all: a graph that was never embedded must not cost an API call
// per search.
func TestHasEmbeddings(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	const pid = "p"
	e, err := store.CreateEntity("thing.go", "file", pid)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	obs, err := store.CreateObservation(e.ID, "a note", pid)
	if err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	has, err := store.HasEmbeddings(pid)
	if err != nil {
		t.Fatalf("HasEmbeddings: %v", err)
	}
	if has {
		t.Error("reported embeddings in a graph with none")
	}

	// An observation vector alone must count: a PDF or markdown file's entity
	// carries no useful vector of its own, and gating on entity vectors only
	// would skip semantic search for exactly the documents it serves best.
	if err := store.SetObservationEmbedding(obs.ID, pid, vec(0.1, 0.2, 0.3)); err != nil {
		t.Fatalf("SetObservationEmbedding: %v", err)
	}
	has, err = store.HasEmbeddings(pid)
	if err != nil {
		t.Fatalf("HasEmbeddings: %v", err)
	}
	if !has {
		t.Error("an embedded observation was not counted as an embedding")
	}

	// Both probes must be project-scoped, so an entity vector is set too:
	// with only an observation vector present, a project-blind entity probe
	// would never be reached and its scoping would go untested.
	if err := store.SetEmbedding(e.ID, pid, vec(0.4, 0.5, 0.6)); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}
	has, err = store.HasEmbeddings("other-project")
	if err != nil {
		t.Fatalf("HasEmbeddings: %v", err)
	}
	if has {
		t.Error("embeddings leaked across project IDs")
	}
}

// TestHybridSearch_KeepsFilterWhenLimitUnset guards a config-handling trap:
// an unset Limit used to replace the entire SearchConfig with the default,
// silently discarding the caller's Filter. A public-only search then returned
// unexported symbols.
func TestHybridSearch_KeepsFilterWhenLimitUnset(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	const pid = "p"
	pub, err := store.CreateEntity("ExportedHandler", "function", pid)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := setVisibility(store, pub.ID, VisibilityPublic); err != nil {
		t.Fatalf("setVisibility: %v", err)
	}
	priv, err := store.CreateEntity("internalHandler", "function", pid)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := setVisibility(store, priv.ID, VisibilityPrivate); err != nil {
		t.Fatalf("setVisibility: %v", err)
	}

	// Limit deliberately left at zero — the only thing the config is missing.
	results, err := store.HybridSearch(pid, "handler", nil,
		SearchConfig{Filter: SearchFilter{PublicOnly: true}})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("public-only search returned nothing while an exported match exists")
	}
	for _, r := range results {
		if r.Entity.Visibility == VisibilityPrivate {
			t.Errorf("public-only search returned unexported %s: the filter was dropped",
				r.Entity.Name)
		}
	}
}

// TestVectorSearch_RespectsLimit pins the cap on the over-fetch. Widening the
// neighbour search is how the page gets filled; it must not become a way to
// return more than the caller asked for, which would silently inflate every
// agent's context window.
func TestVectorSearch_RespectsLimit(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	const pid = "p"
	query := vec(1.0, 0.0, 0.0)

	// Five separate documents, one matching section each: the over-fetch
	// budget (limit*4 = 8) covers all five, so collect yields five candidates
	// for a limit of two.
	for i := 0; i < 5; i++ {
		e, err := store.CreateEntity(fmt.Sprintf("docs/d%d.md", i), "file", pid)
		if err != nil {
			t.Fatalf("CreateEntity: %v", err)
		}
		obs, err := store.CreateObservation(e.ID, fmt.Sprintf("prose %d", i), pid)
		if err != nil {
			t.Fatalf("CreateObservation: %v", err)
		}
		if err := store.SetObservationEmbedding(obs.ID, pid,
			vec(1.0, float32(i)/100, 0.0)); err != nil {
			t.Fatalf("SetObservationEmbedding: %v", err)
		}
	}

	results, err := store.VectorSearch(pid, query, 2)
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results for limit 2", len(results))
	}
}
