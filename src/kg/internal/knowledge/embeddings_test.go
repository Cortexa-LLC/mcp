package knowledge

import (
	"context"
	"testing"
)

// embeddingDim matches the FLOAT[1536] schema column used by the knowledge store.
const embeddingDim = 1536

// mockEmbedder is a simple Embedder implementation for tests that returns
// deterministic fixed-length vectors.
type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, m.dim)
		// Produce a simple non-zero vector so HNSW accepts the node.
		for j := range vec {
			vec[j] = float32(i+1) * 0.01
		}
		out[i] = vec
	}
	return out, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dim }
func (m *mockEmbedder) Model() string   { return "mock" }

// TestBatchEmbed_HNSWInvalidation verifies that after BatchEmbed writes entity
// embeddings the HNSW vector index cache for that project is cleared, so the
// next VectorSearch builds a fresh index rather than serving stale results.
func TestBatchEmbed_HNSWInvalidation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-invalidation-test"
	ctx := context.Background()
	embedder := &mockEmbedder{dim: embeddingDim}

	// Create two entities (no embeddings yet).
	e1, err := store.CreateEntity("auth.go", "file", projectID)
	if err != nil {
		t.Fatalf("CreateEntity e1: %v", err)
	}
	e2, err := store.CreateEntity("handler.go", "file", projectID)
	if err != nil {
		t.Fatalf("CreateEntity e2: %v", err)
	}

	// Warm up the HNSW cache (with empty index – no embeddings exist yet).
	// After this, an entry exists in hnswIdx.indices for projectID.
	store.hnswIdx.get(projectID, func() (*projectIndex, error) {
		return store.buildIndex(projectID)
	})

	// Sanity-check: cache entry exists before BatchEmbed.
	store.hnswIdx.mu.Lock()
	_, existsBefore := store.hnswIdx.indices[projectID]
	store.hnswIdx.mu.Unlock()
	if !existsBefore {
		t.Fatal("expected HNSW cache entry to exist before BatchEmbed")
	}

	// Run BatchEmbed – this should persist embeddings for e1 and e2 and then
	// call hnswIdx.invalidate(projectID) exactly once.
	if err := store.BatchEmbed(ctx, projectID, embedder); err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}

	// After BatchEmbed the cache entry must have been removed (invalidated).
	store.hnswIdx.mu.Lock()
	_, existsAfter := store.hnswIdx.indices[projectID]
	store.hnswIdx.mu.Unlock()
	if existsAfter {
		t.Error("expected HNSW cache to be invalidated after BatchEmbed, but entry still present")
	}

	// Verify both entities now have embeddings persisted.
	e1Updated, err := store.GetEntity(e1.ID, projectID)
	if err != nil {
		t.Fatalf("GetEntity e1: %v", err)
	}
	if e1Updated == nil {
		t.Fatal("entity e1 not found after BatchEmbed")
	}

	e2Updated, err := store.GetEntity(e2.ID, projectID)
	if err != nil {
		t.Fatalf("GetEntity e2: %v", err)
	}
	if e2Updated == nil {
		t.Fatal("entity e2 not found after BatchEmbed")
	}
}

// TestBatchEmbed_NoInvalidationWhenNoEntities verifies that when BatchEmbed
// finds no un-embedded entities the HNSW cache is NOT touched, since entity
// embeddings did not change.
func TestBatchEmbed_NoInvalidationWhenNoEntities(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-no-entity-test"
	ctx := context.Background()
	embedder := &mockEmbedder{dim: embeddingDim}

	// Warm up the HNSW cache with an empty index.
	store.hnswIdx.get(projectID, func() (*projectIndex, error) {
		return store.buildIndex(projectID)
	})

	// Sanity-check: cache entry exists.
	store.hnswIdx.mu.Lock()
	_, existsBefore := store.hnswIdx.indices[projectID]
	store.hnswIdx.mu.Unlock()
	if !existsBefore {
		t.Fatal("expected HNSW cache entry before BatchEmbed")
	}

	// BatchEmbed on a project with no entities should be a no-op for the cache.
	if err := store.BatchEmbed(ctx, projectID, embedder); err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}

	// Cache entry should still be present – no entity embeddings were changed.
	store.hnswIdx.mu.Lock()
	_, existsAfter := store.hnswIdx.indices[projectID]
	store.hnswIdx.mu.Unlock()
	if !existsAfter {
		t.Error("HNSW cache should NOT be invalidated when no entity embeddings were written")
	}
}

// TestSetEmbedding_HNSWInvalidation verifies that the single-entity
// SetEmbedding path also invalidates the HNSW cache after persisting an
// embedding.
func TestSetEmbedding_HNSWInvalidation(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	projectID := "proj-set-embedding-test"

	// Create an entity.
	entity, err := store.CreateEntity("service.go", "file", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	// Warm up the HNSW cache.
	store.hnswIdx.get(projectID, func() (*projectIndex, error) {
		return store.buildIndex(projectID)
	})

	store.hnswIdx.mu.Lock()
	_, existsBefore := store.hnswIdx.indices[projectID]
	store.hnswIdx.mu.Unlock()
	if !existsBefore {
		t.Fatal("expected HNSW cache entry before SetEmbedding")
	}

	// Call SetEmbedding – it should invalidate the cache.
	embedding := make([]float32, embeddingDim)
	for i := range embedding {
		embedding[i] = float32(i) * 0.1
	}
	if err := store.SetEmbedding(entity.ID, embedding); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}

	store.hnswIdx.mu.Lock()
	_, existsAfter := store.hnswIdx.indices[projectID]
	store.hnswIdx.mu.Unlock()
	if existsAfter {
		t.Error("expected HNSW cache to be invalidated after SetEmbedding")
	}
}
