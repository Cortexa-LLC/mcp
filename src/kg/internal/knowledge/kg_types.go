package knowledge

// Entity type constants used by kg indexers.
const (
	EntityTypeFile     = "file"
	EntityTypeFunction = "function"
	EntityTypeType     = "type"
	EntityTypeImport   = "import"
	EntityTypePackage  = "package"
	EntityTypeTopic    = "topic" // markdown headings, architectural concepts
)

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
