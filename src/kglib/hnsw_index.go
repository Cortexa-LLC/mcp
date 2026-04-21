package kglib

import (
	"fmt"
	"sync"
	"time"

	"github.com/coder/hnsw"
)

// projectIndex holds a lazily built HNSW graph for a single project plus a
// reverse map from entity-ID to *Entity so we can reconstruct SearchResult
// without a second round-trip to Kuzu.
type projectIndex struct {
	graph    *hnsw.Graph[string]
	entities map[string]*Entity // entity ID → Entity metadata
	builtAt  time.Time
}

// vectorIndexCache manages per-project HNSW indices.
// It is embedded in Store and is safe for concurrent use.
type vectorIndexCache struct {
	mu      sync.RWMutex
	indices map[string]*projectIndex // project ID → index
}

func newVectorIndexCache() *vectorIndexCache {
	return &vectorIndexCache{
		indices: make(map[string]*projectIndex),
	}
}

// invalidate marks the index for projectID as stale so it will be rebuilt on
// the next call to VectorSearch.
func (c *vectorIndexCache) invalidate(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.indices, projectID)
}

// get returns the cached index for projectID, building it if necessary.
// It uses double-checked locking: a fast read-lock path for cache hits and a
// write-lock path (with a second check) to ensure only one goroutine triggers
// the expensive KuzuDB rebuild for any given project.
func (c *vectorIndexCache) get(projectID string, build func() (*projectIndex, error)) (*projectIndex, error) {
	// Fast path: allow concurrent reads.
	c.mu.RLock()
	idx, ok := c.indices[projectID]
	c.mu.RUnlock()
	if ok {
		return idx, nil
	}

	// Slow path: acquire write lock and double-check before building.
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx, ok = c.indices[projectID]; ok {
		return idx, nil // another goroutine built it while we waited
	}
	idx, err := build()
	if err != nil {
		return nil, err
	}
	c.indices[projectID] = idx
	return idx, nil
}

// buildIndex fetches all embedded entities for projectID from Kuzu and
// constructs an HNSW graph.  It is called lazily on the first VectorSearch
// after a cache miss or invalidation.
func (s *Store) buildIndex(projectID string) (*projectIndex, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.project_id = $project_id AND e.embedding IS NOT NULL
		RETURN e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at, e.embedding
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query entities for index build: %w", err)
	}
	defer result.Close()

	g := hnsw.NewGraph[string]()
	g.Distance = hnsw.CosineDistance // smaller = more similar

	entities := make(map[string]*Entity)
	nodes := make([]hnsw.Node[string], 0, 256)

	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("index build next: %w", err)
		}

		row, err := tuple.GetAsSlice()
		if err != nil {
			tuple.Close()
			return nil, fmt.Errorf("index build row: %w", err)
		}
		tuple.Close()

		entity := &Entity{
			ID:        row[0].(string),
			Name:      row[1].(string),
			Type:      row[2].(string),
			ProjectID: row[3].(string),
		}

		if ts, ok := row[4].(int64); ok {
			entity.CreatedAt = time.UnixMicro(ts).UTC()
		}
		if ts, ok := row[5].(int64); ok {
			entity.UpdatedAt = time.UnixMicro(ts).UTC()
		}

		rawEmb, ok := row[6].([]any)
		if !ok || len(rawEmb) == 0 {
			continue
		}
		emb := make([]float32, len(rawEmb))
		for i, v := range rawEmb {
			if f, ok := v.(float32); ok {
				emb[i] = f
			}
		}

		entities[entity.ID] = entity
		nodes = append(nodes, hnsw.MakeNode(entity.ID, emb))
	}

	if len(nodes) > 0 {
		g.Add(nodes...)
	}

	return &projectIndex{
		graph:    g,
		entities: entities,
		builtAt:  time.Now().UTC(),
	}, nil
}
