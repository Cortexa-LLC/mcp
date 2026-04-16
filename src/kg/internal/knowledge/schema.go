package knowledge

import (
	"fmt"
	"strings"
)

// AllowedRelTypes is the canonical whitelist for relation type labels.
// relType is interpolated directly into Cypher as a label name (e.g. [r:CALLS]),
// which cannot be parameterised, so every caller must guard it with validateRelType.
//
// This slice is the single source of truth – schema DDL in initSchema() and
// the validation check in validateRelType() are both derived from it.
var AllowedRelTypes = []string{
	RelCalls,
	RelImports,
	RelContains,
	RelBelongsTo,
	RelFixes,
	RelSupersedes,
	RelCausedBy,
	RelDependsOn,
	RelImplements,
	RelRelatesTo,
	RelTests,
	RelDocuments,
}

// validateRelType returns nil if relType is in AllowedRelTypes, otherwise an
// error that names the invalid value and lists the valid choices.
func validateRelType(relType string) error {
	for _, allowed := range AllowedRelTypes {
		if relType == allowed {
			return nil
		}
	}
	return fmt.Errorf("invalid relation type %q: must be one of [%s]",
		relType, strings.Join(AllowedRelTypes, ", "))
}

// initSchema creates node and relationship tables if they don't exist.
// It is called once by OpenStore immediately after the connection is established.
func (s *Store) initSchema() error {
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
			embedding FLOAT[1536]
		)`,

		// Observation node table
		`CREATE NODE TABLE IF NOT EXISTS Observation(
			id STRING PRIMARY KEY,
			entity_id STRING,
			content STRING,
			created_at TIMESTAMP,
			embedding FLOAT[1536]
		)`,

		// Structural edge (not in AllowedRelTypes – it is managed internally)
		`CREATE REL TABLE IF NOT EXISTS HAS_OBSERVATION(FROM Entity TO Observation)`,
	}

	// Derive relationship-table DDL from the single source of truth.
	for _, relType := range AllowedRelTypes {
		staticStatements = append(staticStatements,
			fmt.Sprintf("CREATE REL TABLE IF NOT EXISTS %s(FROM Entity TO Entity)", relType),
		)
	}

	for _, stmt := range staticStatements {
		result, err := s.query(stmt)
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
	}

	for _, stmt := range migrations {
		result, err := s.query(stmt)
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
