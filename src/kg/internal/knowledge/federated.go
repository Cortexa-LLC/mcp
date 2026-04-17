package knowledge

import (
	"fmt"
	"path/filepath"
	"sort"
)

// FederatedStore wraps multiple stores to enable cross-layer queries.
// Each layer is a separate KG database, with priority determining precedence
// when merging results (higher priority wins for duplicate entities).
type FederatedStore struct {
	layers []*layeredStore
}

type layeredStore struct {
	name     string
	store    *Store
	priority int
}

// OpenFederatedStore opens a store with its configured layers.
// The primary scope's store is opened read-write, layers are read-only.
func OpenFederatedStore(aiDir string, scopeConfig *ScopeConfig, readOnly bool) (*FederatedStore, error) {
	fs := &FederatedStore{
		layers: make([]*layeredStore, 0, len(scopeConfig.Layers)+1),
	}

	// Open layer stores first (read-only)
	for i, layerName := range scopeConfig.Layers {
		layerCfg, err := LoadScopeConfig(aiDir, layerName)
		if err != nil {
			return nil, fmt.Errorf("load layer %s: %w", layerName, err)
		}

		dbPath := filepath.Join(aiDir, layerCfg.Database)
		store, err := OpenStoreReadOnly(dbPath)
		if err != nil {
			// Clean up previously opened stores
			fs.Close()
			return nil, fmt.Errorf("open layer %s: %w", layerName, err)
		}

		fs.layers = append(fs.layers, &layeredStore{
			name:     layerName,
			store:    store,
			priority: i + 1, // Lower priority for base layers
		})
	}

	// Open primary scope store (read-write or read-only)
	primaryPath := filepath.Join(aiDir, scopeConfig.Database)
	var primaryStore *Store
	var err error
	if readOnly {
		primaryStore, err = OpenStoreReadOnly(primaryPath)
	} else {
		primaryStore, err = OpenStore(primaryPath)
	}
	if err != nil {
		fs.Close()
		return nil, fmt.Errorf("open primary store: %w", err)
	}

	fs.layers = append(fs.layers, &layeredStore{
		name:     scopeConfig.Name,
		store:    primaryStore,
		priority: len(scopeConfig.Layers) + 10, // Highest priority
	})

	return fs, nil
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

// PrimaryStore returns the primary (highest priority) store for write operations
func (fs *FederatedStore) PrimaryStore() *Store {
	if len(fs.layers) == 0 {
		return nil
	}
	return fs.layers[len(fs.layers)-1].store
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
		// Query this layer
		results, err := layer.store.HybridSearch(projectID, query, queryEmbedding, config)
		if err != nil {
			// Log warning but continue with other layers
			fmt.Printf("Warning: search in layer %s failed: %v\n", layer.name, err)
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
	return fs.HybridSearch(projectID, query, nil, SearchConfig{Limit: limit})
}

// VectorSearch performs vector search across all layers and merges results
func (fs *FederatedStore) VectorSearch(projectID string, embedding []float32, limit int) ([]*SearchResult, error) {
	return fs.HybridSearch(projectID, "", embedding, SearchConfig{Limit: limit})
}
