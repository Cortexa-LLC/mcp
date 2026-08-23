package kglib

import (
	"fmt"
	"os"
	"sort"
)

// SearchLayer is anything that can answer a federated search: a local Kuzu
// *Store, or (in future phases) a remote hub graph queried over HTTP. Only
// search federates — writes always go to the primary local store — so the
// interface is deliberately this small.
type SearchLayer interface {
	HybridSearch(projectID, query string, queryEmbedding []float32, config SearchConfig) ([]*SearchResult, error)
	Close() error
}

// compile-time check: a local store is a valid federation layer
var _ SearchLayer = (*Store)(nil)

// FederatedStore wraps multiple stores to enable cross-layer queries.
// Each layer is a separate KG database, with priority determining precedence
// when merging results (higher priority wins for duplicate entities).
type FederatedStore struct {
	layers []*layeredStore
}

type layeredStore struct {
	name      string
	store     SearchLayer
	priority  int
	projectID string
}

// LayerConfig describes a database layer for federation
type LayerConfig struct {
	Name     string
	Store    SearchLayer
	Priority int

	// ProjectID optionally overrides the project ID used when querying this
	// layer. Layers written by a different tool (or under a different
	// convention) do not necessarily share the caller's project ID — a
	// user-global personal graph, for example, files everything under
	// "personal" regardless of which project is being searched. Empty means
	// "use the project ID passed to the search call".
	ProjectID string
}

// NewFederatedStore creates a federated store from a list of configured layers.
// Layers should be ordered from lowest to highest priority (primary/read-write last).
func NewFederatedStore(layers []LayerConfig) *FederatedStore {
	fs := &FederatedStore{
		layers: make([]*layeredStore, 0, len(layers)),
	}

	for _, layer := range layers {
		fs.layers = append(fs.layers, &layeredStore{
			name:      layer.Name,
			store:     layer.Store,
			priority:  layer.Priority,
			projectID: layer.ProjectID,
		})
	}

	return fs
}

// Close closes all layer stores
func (fs *FederatedStore) Close() error {
	var firstErr error
	for _, layer := range fs.layers {
		if err := layer.store.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PrimaryStore returns the primary (highest priority) layer's local store for
// write operations, or nil if there are no layers or the primary layer is not
// a local *Store. Remote layers are read-only by design and are never the
// write target, so callers configure a local store as the final layer.
func (fs *FederatedStore) PrimaryStore() *Store {
	if len(fs.layers) == 0 {
		return nil
	}
	store, _ := fs.layers[len(fs.layers)-1].store.(*Store)
	return store
}

// HybridSearch performs hybrid search across all layers and merges results.
// Results from higher-priority layers override lower-priority duplicates.
func (fs *FederatedStore) HybridSearch(projectID, query string, queryEmbedding []float32, config SearchConfig) ([]*SearchResult, error) {
	if config.Limit == 0 {
		config = DefaultSearchConfig()
	}

	// Collect results from all layers
	allResults := make(map[string]*SearchResult) // entityID -> result
	layerSources := make(map[string]string)      // entityID -> layer name

	for _, layer := range fs.layers {
		// Query this layer, honouring a per-layer project ID override
		layerProjectID := projectID
		if layer.projectID != "" {
			layerProjectID = layer.projectID
		}

		results, err := layer.store.HybridSearch(layerProjectID, query, queryEmbedding, config)
		if err != nil {
			// Warn and continue with the other layers. This goes to stderr:
			// callers embedded in an MCP server own stdout for JSON-RPC.
			fmt.Fprintf(os.Stderr, "Warning: search in layer %s failed: %v\n", layer.name, err)
			continue
		}

		// Merge results - higher priority wins
		for _, result := range results {
			entityID := result.Entity.ID
			existing, exists := allResults[entityID]

			if !exists {
				// New entity
				allResults[entityID] = result
				layerSources[entityID] = layer.name
			} else {
				// Duplicate - check priority
				existingLayer := layerSources[entityID]
				var existingPriority int
				for _, l := range fs.layers {
					if l.name == existingLayer {
						existingPriority = l.priority
						break
					}
				}

				if layer.priority > existingPriority {
					// Higher priority layer - replace
					allResults[entityID] = result
					layerSources[entityID] = layer.name
				} else if layer.priority == existingPriority {
					// Same priority - combine scores
					existing.Score += result.Score
				}
				// Lower priority - ignore
			}
		}
	}

	// Convert map to sorted slice
	merged := make([]*SearchResult, 0, len(allResults))
	for _, result := range allResults {
		merged = append(merged, result)
	}

	// Sort by score descending
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	// Apply limit
	if len(merged) > config.Limit {
		merged = merged[:config.Limit]
	}

	return merged, nil
}

// KeywordSearch performs keyword search across all layers and merges results
func (fs *FederatedStore) KeywordSearch(projectID, query string, limit int) ([]*SearchResult, error) {
	return fs.KeywordSearchFiltered(projectID, query, limit, SearchFilter{})
}

// KeywordSearchFiltered is KeywordSearch with the filter pushed down to every
// layer, so each one spends its share of the limit on rows the caller wants
// rather than on rows the caller will discard.
func (fs *FederatedStore) KeywordSearchFiltered(projectID, query string, limit int, filter SearchFilter) ([]*SearchResult, error) {
	// DefaultSearchConfig, not a bare Limit: a zero RecencyWeight leaves every
	// local result at exactly the score KeywordSearch assigns, which made local
	// and remote scores incomparable at the merge.
	cfg := DefaultSearchConfig()
	cfg.Limit = limit
	cfg.Filter = filter
	return fs.HybridSearch(projectID, query, nil, cfg)
}

// VectorSearch performs vector search across all layers and merges results
func (fs *FederatedStore) VectorSearch(projectID string, embedding []float32, limit int) ([]*SearchResult, error) {
	return fs.HybridSearch(projectID, "", embedding, SearchConfig{Limit: limit})
}
