package kglib

import (
	"context"
	"strings"
	"testing"
)

// nullOutObservationContent sets content to NULL on every Observation row,
// reproducing what reaches the database in practice.
//
// Nothing in the Go API can produce this state, which is exactly why it kept
// slipping past review: bulk loading writes these columns through COPY FROM over
// generated JSON (see indexer.go), and a JSON document with a missing or null key
// lands as a SQL NULL without any Go type ever being involved. Once the row exists,
// every reader that does row[N].(string) panics on it.
//
// Only content is nulled, deliberately. id is the primary key and entity_id is the
// predicate both readers filter on, so a NULL there drops the row from the result
// set instead of reaching the scanner -- nulling them makes the test pass against
// unfixed code by returning nothing at all. content is the column that can be NULL
// *and* be read, which is the case worth pinning.
func nullOutObservationContent(t *testing.T, store *Store) {
	t.Helper()
	res, err := store.Query("MATCH (o:Observation) SET o.content = NULL")
	if err != nil {
		t.Fatalf("could not null out observation columns, so this test cannot "+
			"express the failure it exists to catch: %v", err)
	}
	res.Close()

	// Prove the setup actually produced a NULL rather than an empty string --
	// otherwise the test would pass against unfixed code for the wrong reason.
	check, err := store.Query("MATCH (o:Observation) WHERE o.content IS NULL RETURN count(o)")
	if err != nil {
		t.Fatalf("verify NULL: %v", err)
	}
	defer check.Close()
	if !check.HasNext() {
		t.Fatal("verify NULL: no count row")
	}
	tuple, err := check.Next()
	if err != nil {
		t.Fatalf("verify NULL: %v", err)
	}
	row, err := tuple.GetAsSlice()
	if err != nil {
		t.Fatalf("verify NULL: %v", err)
	}
	tuple.Close()
	n, _ := row[0].(int64)
	if n == 0 {
		t.Fatal("setup did not create any NULL content column; the test cannot detect the panic it targets")
	}
}

func seedObservation(t *testing.T, store *Store, projectID string) *Entity {
	t.Helper()
	entity, err := store.CreateEntity("auth.go", "file", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "handles login", projectID); err != nil {
		t.Fatalf("AddObservation: %v", err)
	}
	return entity
}

// TestKeywordSearch_SurvivesNullObservationColumns asserts the property: a NULL
// column is data the reader has to cope with, not a reason to take the process
// down. batchGetObservations runs on every keyword search, and the MCP server
// dispatches handlers synchronously with no recover anywhere in either module, so
// a panic here ends the agent's whole session rather than one tool call.
func TestKeywordSearch_SurvivesNullObservationColumns(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-null-columns"
	seedObservation(t, store, projectID)
	nullOutObservationContent(t, store)

	results, err := store.KeywordSearch(projectID, "auth", 10)
	if err != nil {
		t.Fatalf("KeywordSearch returned an error on NULL columns: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected the entity to still be found; a NULL observation column must not hide it")
	}
	for _, r := range results {
		for _, o := range r.Observations {
			if o.Content != "" {
				t.Errorf("expected NULL content to read back as the empty string, got %q", o.Content)
			}
		}
	}
}

// TestGetTopObservations_SurvivesNullColumns covers the other reader of the same
// three columns.
func TestGetTopObservations_SurvivesNullColumns(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-null-top-obs"
	entity := seedObservation(t, store, projectID)
	nullOutObservationContent(t, store)

	obs, err := store.GetTopObservations(entity.ID, projectID, 10)
	if err != nil {
		t.Fatalf("GetTopObservations returned an error on NULL columns: %v", err)
	}
	for _, o := range obs {
		if o.Content != "" {
			t.Errorf("expected NULL content to read back as the empty string, got %q", o.Content)
		}
	}
}

// TestGetUnembedded_SurvivesNullColumns covers the embedding-backfill readers,
// which scan the same columns on the way to the embedding service.
func TestGetUnembedded_SurvivesNullColumns(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-null-unembedded"
	seedObservation(t, store, projectID)

	res, err := store.Query("MATCH (e:Entity) SET e.name = NULL, e.type = NULL")
	if err != nil {
		t.Fatalf("null out entity columns: %v", err)
	}
	res.Close()

	if _, err := store.GetUnembeddedEntities(projectID); err != nil {
		t.Fatalf("GetUnembeddedEntities returned an error on NULL columns: %v", err)
	}
	if _, err := store.GetUnembeddedObservations(projectID); err != nil {
		t.Fatalf("GetUnembeddedObservations returned an error on NULL columns: %v", err)
	}
}

// shortEmbedder returns fewer vectors than it was given texts, which is what a
// remote embedding backend does when it drops, truncates, or errors on part of a
// batch. The response is untrusted input; BatchEmbed pairs it with its inputs by
// position, so a short response must be refused rather than indexed into.
type shortEmbedder struct {
	dim  int
	keep int
}

func (m *shortEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	n := m.keep
	if n > len(texts) {
		n = len(texts)
	}
	out := make([][]float32, n)
	for i := range out {
		vec := make([]float32, m.dim)
		vec[0] = float32(i+1) * 0.01
		out[i] = vec
	}
	return out, nil
}

func (m *shortEmbedder) Dimensions() int { return m.dim }
func (m *shortEmbedder) Model() string   { return "short" }

// TestBatchEmbed_RejectsShortEmbedderResponse asserts BatchEmbed reports an error
// rather than panicking with index out of range when the backend under-delivers.
func TestBatchEmbed_RejectsShortEmbedderResponse(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-short-embedder"
	for _, name := range []string{"auth.go", "handler.go", "router.go"} {
		entity, err := store.CreateEntity(name, "file", projectID)
		if err != nil {
			t.Fatalf("CreateEntity %s: %v", name, err)
		}
		if _, err := store.CreateObservation(entity.ID, "observation for "+name, projectID); err != nil {
			t.Fatalf("AddObservation %s: %v", name, err)
		}
	}

	err := store.BatchEmbed(context.Background(), projectID, &shortEmbedder{dim: embeddingDim, keep: 1})
	if err == nil {
		t.Fatal("BatchEmbed accepted an embedder response shorter than its input; " +
			"vectors are matched to entities by position, so the surplus entities would " +
			"either be skipped silently or read out of range")
	}
	if !strings.Contains(err.Error(), "embedder returned") {
		t.Errorf("expected an error naming the count mismatch, got: %v", err)
	}
}

// TestBatchEmbed_RejectsShortObservationResponse exercises the observation half,
// which is a separate Embed call with its own indexing loop.
func TestBatchEmbed_RejectsShortObservationResponse(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-short-embedder-obs"
	entity, err := store.CreateEntity("auth.go", "file", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	for _, c := range []string{"one", "two", "three"} {
		if _, err := store.CreateObservation(entity.ID, c, projectID); err != nil {
			t.Fatalf("AddObservation: %v", err)
		}
	}
	// Give the single entity its embedding so BatchEmbed reaches the observation
	// stage, then under-deliver there.
	if err := store.SetEmbedding(entity.ID, make([]float32, embeddingDim)); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}

	err = store.BatchEmbed(context.Background(), projectID, &shortEmbedder{dim: embeddingDim, keep: 2})
	if err == nil {
		t.Fatal("BatchEmbed accepted a short embedder response for observations")
	}
	if !strings.Contains(err.Error(), "observations") {
		t.Errorf("expected the error to identify the observation batch, got: %v", err)
	}
}
