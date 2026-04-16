package knowledge

import "time"

// Entity type constants used by all indexers.
// These are the values stored in Entity.Type and used to identify entities
// in the knowledge graph.
const (
	EntityTypeFile     = "file"
	EntityTypeFunction = "function"
	EntityTypeType     = "type"
	EntityTypeImport   = "import"
	EntityTypePackage  = "package"
	EntityTypeTopic    = "topic" // markdown headings, architectural concepts
)

// Relation type constants.  Every value must appear in AllowedRelTypes in
// schema.go, which is the authoritative list used to build the Cypher schema.
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

// Entity represents a knowledge graph node (function, file, bug, etc.)
type Entity struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"` // "function", "file", "bug", etc.
	ProjectID    string    `json:"project_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Observations []string  `json:"observations,omitempty"`
}

// Relation represents a directed edge between two entities
type Relation struct {
	FromID   string `json:"from_id"`
	ToID     string `json:"to_id"`
	Type     string `json:"type"`               // "CALLS", "IMPORTS", "FIXES", etc.
	Metadata string `json:"metadata,omitempty"` // Optional JSON
}

// Observation represents a note/fact attached to an entity
type Observation struct {
	ID        string    `json:"id"`
	EntityID  string    `json:"entity_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
