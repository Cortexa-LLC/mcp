package knowledge

import "github.com/cortexa-llc/mcp/kglib"

// Type aliases for kglib types to keep kg code readable
type (
	Store        = kglib.Store
	Entity       = kglib.Entity
	Observation  = kglib.Observation
	Relation     = kglib.Relation
	SearchResult = kglib.SearchResult
	SearchConfig = kglib.SearchConfig
	Embedder     = kglib.Embedder
)

// OpenStore opens a kg store with the kg-specific schema configuration
func OpenStore(dbPath string) (*kglib.Store, error) {
	cfg := &kglib.SchemaConfig{
		AdditionalRelTypes: AllowedRelTypes,
	}
	return kglib.OpenStore(dbPath, cfg)
}

// OpenStoreReadOnly opens a kg store in read-only mode
func OpenStoreReadOnly(dbPath string) (*kglib.Store, error) {
	return kglib.OpenStoreReadOnly(dbPath)
}

// NewEmbedderFromEnv creates an embedder from environment variables
var NewEmbedderFromEnv = kglib.NewEmbedderFromEnv

// DefaultSearchConfig returns the default search configuration
var DefaultSearchConfig = kglib.DefaultSearchConfig
