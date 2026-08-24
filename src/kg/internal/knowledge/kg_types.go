package knowledge

import (
	"fmt"
	"strings"
)

// Entity type constants used by kg indexers.
const (
	EntityTypeFile     = "file"
	EntityTypeFunction = "function"
	EntityTypeType     = "type"
	EntityTypeImport   = "import"
	EntityTypePackage  = "package"
	EntityTypeTopic    = "topic" // markdown headings, architectural concepts
)

// codeDerivedIDPrefixes identifies entities the indexers create. Every
// structural indexer mints source-derived IDs of the form "<kind>:<rest>"
// (e.g. "file:src/a.go", "function:src/a.go:F"), while hand-written knowledge
// — `kg add`, the MCP add_entity tool, journal replay of hand-writes — gets a
// UUID from CreateEntity, which never contains a colon. The ID shape is
// therefore the discriminator between data a re-index can rebuild from the
// tree and knowledge that exists nowhere else.
//
// Kept next to the entity type constants above so a new indexer kind cannot be
// added without extending this list: an ID prefix missing here means a
// re-index will never clear those entities and every run will pile up stale
// copies.
//
// Log-derived entities (IndexExecutionLogs, IndexApplicationLogs) carry UUIDs
// and are deliberately NOT in this list: their producers are idempotent by
// entity name, so preserving them across a re-index creates no duplicates.
var codeDerivedIDPrefixes = []string{
	EntityTypeFile + ":",
	EntityTypeFunction + ":",
	EntityTypeType + ":",
	EntityTypeImport + ":",
	EntityTypePackage + ":",
	EntityTypeTopic + ":",
}

// codeDerivedIDClause builds a Cypher predicate that is true when the entity
// bound to varName was created by an indexer. varName is a query-internal
// variable name, never user input — safe to interpolate.
func codeDerivedIDClause(varName string) string {
	conds := make([]string, len(codeDerivedIDPrefixes))
	for i, p := range codeDerivedIDPrefixes {
		conds[i] = fmt.Sprintf("%s.id STARTS WITH '%s'", varName, p)
	}
	return "(" + strings.Join(conds, " OR ") + ")"
}

// Relation type constants for kg.
const (
	RelContains   = "CONTAINS"
	RelImports    = "IMPORTS"
	RelBelongsTo  = "BELONGS_TO"
	RelCalls      = "CALLS"
	RelFixes      = "FIXES"
	RelSupersedes = "SUPERSEDES"
	RelCausedBy   = "CAUSED_BY"
	RelDependsOn  = "DEPENDS_ON"
	RelImplements = "IMPLEMENTS"
	RelRelatesTo  = "RELATES_TO"
	RelTests      = "TESTS"
	RelDocuments  = "DOCUMENTS"
)

// AllowedRelTypes is the list of all relation types used by kg.
// This is used to initialize the kglib schema.
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
