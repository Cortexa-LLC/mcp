package kglib

import "time"

// Entity represents a knowledge graph node (function, file, bug, conversation, learning, etc.)
type Entity struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"` // "function", "file", "conversation", etc.
	ProjectID    string    `json:"project_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Observations []string  `json:"observations,omitempty"`
}

// Relation represents a directed edge between two entities
type Relation struct {
	FromID   string `json:"from_id"`
	ToID     string `json:"to_id"`
	Type     string `json:"type"`               // "CALLS", "IMPORTS", "DISCUSSED_IN", etc.
	Metadata string `json:"metadata,omitempty"` // Optional JSON
}

// Observation represents a note/fact attached to an entity
type Observation struct {
	ID        string    `json:"id"`
	EntityID  string    `json:"entity_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}
