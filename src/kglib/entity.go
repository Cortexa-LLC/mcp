package kglib

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateEntity adds a new entity to the knowledge graph
func (s *Store) CreateEntity(name, entityType, projectID string) (*Entity, error) {
	entity := &Entity{
		ID:        uuid.New().String(),
		Name:      name,
		Type:      entityType,
		ProjectID: projectID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	result, err := s.QueryParams(`
		CREATE (e:Entity {
			id: $id,
			name: $name,
			type: $type,
			project_id: $project_id,
			created_at: $created_at,
			updated_at: $updated_at
		})
	`, map[string]any{
		"id":         entity.ID,
		"name":       name,
		"type":       entityType,
		"project_id": projectID,
		"created_at": entity.CreatedAt,
		"updated_at": entity.UpdatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create entity: %w", err)
	}
	defer result.Close()

	if err := s.appendJournal(JournalRecord{
		Op:        OpCreateEntity,
		ProjectID: projectID,
		Entity:    &EntityRef{Name: name, Type: entityType},
	}); err != nil {
		return entity, errJournalNote(err)
	}

	return entity, nil
}

// GetEntity retrieves an entity by ID for a specific project
func (s *Store) GetEntity(id, projectID string) (*Entity, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.id = $id AND e.project_id = $project_id
		RETURN e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at
	`, map[string]any{"id": id, "project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query entity: %w", err)
	}
	defer result.Close()

	if !result.HasNext() {
		return nil, fmt.Errorf("entity not found: %s", id)
	}

	tuple, err := result.Next()
	if err != nil {
		return nil, fmt.Errorf("get next: %w", err)
	}
	defer tuple.Close()

	row, err := tuple.GetAsSlice()
	if err != nil {
		return nil, fmt.Errorf("get row: %w", err)
	}

	entity := &Entity{
		ID:        stringOrEmpty(row[0]),
		Name:      stringOrEmpty(row[1]),
		Type:      stringOrEmpty(row[2]),
		ProjectID: stringOrEmpty(row[3]),
	}

	entity.CreatedAt = timeOrZero(row[4])
	entity.UpdatedAt = timeOrZero(row[5])

	return entity, nil
}

// GetEntityByName retrieves an entity by name for a specific project.
// Returns (nil, nil) when no entity with that name exists.
func (s *Store) GetEntityByName(name, projectID string) (*Entity, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.name = $name AND e.project_id = $project_id
		RETURN e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at
		LIMIT 1
	`, map[string]any{"name": name, "project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query entity by name: %w", err)
	}
	defer result.Close()

	if !result.HasNext() {
		return nil, nil
	}

	tuple, err := result.Next()
	if err != nil {
		return nil, fmt.Errorf("get next: %w", err)
	}
	defer tuple.Close()

	row, err := tuple.GetAsSlice()
	if err != nil {
		return nil, fmt.Errorf("get row: %w", err)
	}

	entity := &Entity{
		ID:        stringOrEmpty(row[0]),
		Name:      stringOrEmpty(row[1]),
		Type:      stringOrEmpty(row[2]),
		ProjectID: stringOrEmpty(row[3]),
	}
	entity.CreatedAt = timeOrZero(row[4])
	entity.UpdatedAt = timeOrZero(row[5])
	return entity, nil
}

// GetEntityByNameAndType retrieves an entity by the tuple the journal and the
// export format use to identify it. Name alone is not unique — an indexed graph
// can hold a file and a topic of the same name — so replay and import resolve
// on both. Returns (nil, nil) when there is no match.
func (s *Store) GetEntityByNameAndType(name, entityType, projectID string) (*Entity, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.name = $name AND e.type = $type AND e.project_id = $project_id
		RETURN e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at
		LIMIT 1
	`, map[string]any{"name": name, "type": entityType, "project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query entity by name and type: %w", err)
	}
	defer result.Close()

	if !result.HasNext() {
		return nil, nil
	}

	tuple, err := result.Next()
	if err != nil {
		return nil, fmt.Errorf("get next: %w", err)
	}
	defer tuple.Close()

	row, err := tuple.GetAsSlice()
	if err != nil {
		return nil, fmt.Errorf("get row: %w", err)
	}

	entity := &Entity{
		ID:        stringOrEmpty(row[0]),
		Name:      stringOrEmpty(row[1]),
		Type:      stringOrEmpty(row[2]),
		ProjectID: stringOrEmpty(row[3]),
	}
	entity.CreatedAt = timeOrZero(row[4])
	entity.UpdatedAt = timeOrZero(row[5])
	return entity, nil
}

// ListEntities retrieves all entities for a project, optionally filtered by type
func (s *Store) ListEntities(projectID, entityType string) ([]*Entity, error) {
	stmt := `
		MATCH (e:Entity)
		WHERE e.project_id = $project_id
		RETURN e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at
	`
	params := map[string]any{"project_id": projectID}
	if entityType != "" {
		stmt = `
			MATCH (e:Entity)
			WHERE e.project_id = $project_id AND e.type = $type
			RETURN e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at
		`
		params["type"] = entityType
	}

	result, err := s.QueryParams(stmt, params)
	if err != nil {
		return nil, fmt.Errorf("query entities: %w", err)
	}
	defer result.Close()

	var entities []*Entity
	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next: %w", err)
		}

		row, err := tuple.GetAsSlice()
		tuple.Close()
		if err != nil {
			return nil, fmt.Errorf("get row: %w", err)
		}

		entity := &Entity{
			ID:        stringOrEmpty(row[0]),
			Name:      stringOrEmpty(row[1]),
			Type:      stringOrEmpty(row[2]),
			ProjectID: stringOrEmpty(row[3]),
		}

		entity.CreatedAt = timeOrZero(row[4])
		entity.UpdatedAt = timeOrZero(row[5])

		entities = append(entities, entity)
	}

	return entities, nil
}

// DeleteEntity removes an entity and all its relations
func (s *Store) DeleteEntity(id, projectID string) error {
	// First verify the entity exists and belongs to this project.
	// The entity is kept rather than discarded: the journal identifies it by
	// name and type, which are unreadable once the node is gone.
	entity, err := s.GetEntity(id, projectID)
	if err != nil {
		return err
	}

	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.id = $id AND e.project_id = $project_id
		DETACH DELETE e
	`, map[string]any{"id": id, "project_id": projectID})
	if err != nil {
		return fmt.Errorf("delete entity: %w", err)
	}
	defer result.Close()

	if err := s.appendJournal(JournalRecord{
		Op:        OpDeleteEntity,
		ProjectID: projectID,
		Entity:    &EntityRef{Name: entity.Name, Type: entity.Type},
	}); err != nil {
		return errJournalNote(err)
	}

	return nil
}
