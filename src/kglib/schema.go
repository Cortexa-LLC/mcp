package kglib

import (
	"fmt"
	"strings"
)

// SchemaConfig allows extending the knowledge graph schema with custom entity
// and relation types beyond the base types.
type SchemaConfig struct {
	// AdditionalRelTypes are relation types to add to the schema beyond the base types.
	// These will be validated alongside base types in validateRelType.
	AdditionalRelTypes []string
}

// validateRelType returns nil if relType is in the store's allowed relation types,
// otherwise an error that names the invalid value and lists the valid choices.
func (s *Store) validateRelType(relType string) error {
	for _, allowed := range s.allowedRelTypes {
		if relType == allowed {
			return nil
		}
	}
	return fmt.Errorf("invalid relation type %q: must be one of [%s]",
		relType, strings.Join(s.allowedRelTypes, ", "))
}

// initSchema creates node and relationship tables if they don't exist.
// It is called once by OpenStore immediately after the connection is established.
// Custom relation types can be provided via SchemaConfig.
func (s *Store) initSchema(cfg *SchemaConfig) error {
	// Build complete list of relation types (base + custom)
	relTypes := make([]string, 0)
	if cfg != nil && len(cfg.AdditionalRelTypes) > 0 {
		relTypes = append(relTypes, cfg.AdditionalRelTypes...)
	}

	// Store for validation
	s.allowedRelTypes = relTypes

	// Fixed node tables and the HAS_OBSERVATION edge
	staticStatements := []string{
		// Entity node table
		`CREATE NODE TABLE IF NOT EXISTS Entity(
			id STRING PRIMARY KEY,
			name STRING,
			type STRING,
			project_id STRING,
			created_at TIMESTAMP,
			updated_at TIMESTAMP,
			embedding FLOAT[1536],
			visibility STRING
		)`,

		// Observation node table
		`CREATE NODE TABLE IF NOT EXISTS Observation(
			id STRING PRIMARY KEY,
			entity_id STRING,
			content STRING,
			created_at TIMESTAMP,
			embedding FLOAT[1536]
		)`,

		// Structural edge (not in allowedRelTypes – it is managed internally)
		`CREATE REL TABLE IF NOT EXISTS HAS_OBSERVATION(FROM Entity TO Observation)`,

		// Provenance stamp: which commit/repo this database was indexed from.
		// One row per project_id. Written by SetMeta at index time.
		`CREATE NODE TABLE IF NOT EXISTS KGMeta(
			project_id STRING PRIMARY KEY,
			repo_url STRING,
			commit_hash STRING,
			dirty BOOLEAN,
			indexed_at TIMESTAMP,
			embed_model STRING,
			kg_version STRING
		)`,
	}

	// Derive relationship-table DDL from the configured relation types
	for _, relType := range relTypes {
		staticStatements = append(staticStatements,
			fmt.Sprintf("CREATE REL TABLE IF NOT EXISTS %s(FROM Entity TO Entity)", relType),
		)
	}

	for _, stmt := range staticStatements {
		result, err := s.Query(stmt)
		if err != nil {
			return fmt.Errorf("execute schema statement: %w", err)
		}
		result.Close()
	}

	// Migrate existing tables to add embedding column if missing
	if err := s.migrateEmbeddings(); err != nil {
		return fmt.Errorf("migrate embeddings: %w", err)
	}

	return nil
}

// migrateEmbeddings adds embedding columns to existing tables if they don't
// exist yet. Only "already has property" errors are suppressed (Kuzu's signal
// that the column was created on a previous Open); all other errors are
// returned so genuine failures are not silently swallowed.
func (s *Store) migrateEmbeddings() error {
	migrations := []string{
		`ALTER TABLE Entity ADD embedding FLOAT[1536]`,
		`ALTER TABLE Observation ADD embedding FLOAT[1536]`,
		// Source-language visibility (see Entity.Visibility). Empty on rows
		// written before this column existed, and on entities that have no
		// such concept — read paths must treat empty as "unknown", never as
		// "private", or a not-yet-reindexed graph looks entirely private.
		`ALTER TABLE Entity ADD visibility STRING`,
	}

	for _, stmt := range migrations {
		result, err := s.Query(stmt)
		if err != nil {
			// Kuzu returns "already has property <name>" when the column already exists,
			// which is the normal case on all but the very first Open.
			if strings.Contains(err.Error(), "already has property") {
				continue
			}
			return fmt.Errorf("migration %q: %w", stmt, err)
		}
		result.Close()
	}

	return nil
}
