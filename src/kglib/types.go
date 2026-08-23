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

	// Visibility is the symbol's source-language visibility: VisibilityPublic
	// or VisibilityPrivate for code symbols, empty for everything else.
	//
	// Empty means "not applicable or not known", and covers three cases that
	// must not be confused with private: hand-written entities, which have no
	// source-language visibility at all; entity kinds that have none (files,
	// markdown topics, Makefile targets); and rows written before the column
	// existed, which fill in on the next index.
	Visibility string `json:"visibility,omitempty"`
}

// Symbol visibility values. Deliberately a small open vocabulary rather than a
// bool: Go and Python collapse to two, but Java has protected and
// package-private and Kotlin has internal, and the column should not force
// those languages to lie.
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

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
