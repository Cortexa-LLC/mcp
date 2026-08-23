package kglib

import (
	"fmt"
	"os"
	"time"
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

// isPrimary reports whether a layer is the project's own graph rather than a
// supporting one.
//
// Priorities are relative — personal store 0, remotes 1..R, local layers above
// those, primary highest — so there is no constant to compare against. Layers
// are ordered lowest to highest with the primary last, which is the same
// assumption PrimaryStore makes.
func (fs *FederatedStore) isPrimary(l *layeredStore) bool {
	return len(fs.layers) > 0 && l.name == fs.layers[len(fs.layers)-1].name
}

// HybridSearch performs hybrid search across all layers and merges results.
// Results from higher-priority layers override lower-priority duplicates.
func (fs *FederatedStore) HybridSearch(projectID, query string, queryEmbedding []float32, config SearchConfig) ([]*SearchResult, error) {
	if config.Limit == 0 {
		config = DefaultSearchConfig()
	}
	// One clock for the whole federated query. Each layer would otherwise
	// sample its own, and results queried microseconds apart would carry
	// recency scores that differ in their last digits — enough to reorder
	// results that should tie, differently on every run.
	if config.Now.IsZero() {
		config.Now = time.Now().UTC()
	}

	// Collect results from all layers
	allResults := make(map[string]*SearchResult) // entityID -> result
	layerSources := make(map[string]string)      // entityID -> layer name
	var failures []string

	for _, layer := range fs.layers {
		// Query this layer, honouring a per-layer project ID override
		layerProjectID := projectID
		if layer.projectID != "" {
			layerProjectID = layer.projectID
		}

		results, err := layer.store.HybridSearch(layerProjectID, query, queryEmbedding, config)
		if err != nil {
			// A supporting layer that fails degrades the answer; the primary
			// one failing means the answer is not an answer.
			//
			// Treating both as a warning made a locked, mid-migration or
			// format-incompatible primary database indistinguishable from a
			// project with no knowledge in it — and in MCP stdio mode the
			// warning goes to a stderr the client never reads, so the agent is
			// told, in effect, that the project is empty.
			if fs.isPrimary(layer) {
				return nil, fmt.Errorf("search in primary layer %s: %w", layer.name, err)
			}
			// Supporting layers still degrade rather than fail: a hub being
			// unreachable should not stop local search from answering.
			failures = append(failures, fmt.Sprintf("%s: %v", layer.name, err))
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

	merged := make([]*SearchResult, 0, len(allResults))
	for _, result := range allResults {
		merged = append(merged, result)
	}

	// rankResults, not a bare score sort: this path skipped the visibility
	// ordering entirely, so a federated search ranked unexported symbols
	// exactly as a non-federated one did not. It is also a total order, which
	// is what keeps the merge deterministic despite being built from a map.
	rankResults(merged, config.visibilityPenalty())

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
