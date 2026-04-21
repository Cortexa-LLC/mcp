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

	// Parse timestamps (Kuzu returns timestamps as int64 microseconds)
	if ts, ok := row[4].(int64); ok {
		entity.CreatedAt = time.UnixMicro(ts).UTC()
	}
	if ts, ok := row[5].(int64); ok {
		entity.UpdatedAt = time.UnixMicro(ts).UTC()
	}

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
	if ts, ok := row[4].(int64); ok {
		entity.CreatedAt = time.UnixMicro(ts).UTC()
	}
	if ts, ok := row[5].(int64); ok {
		entity.UpdatedAt = time.UnixMicro(ts).UTC()
	}
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

		if ts, ok := row[4].(int64); ok {
			entity.CreatedAt = time.UnixMicro(ts).UTC()
		}
		if ts, ok := row[5].(int64); ok {
			entity.UpdatedAt = time.UnixMicro(ts).UTC()
		}

		entities = append(entities, entity)
	}

	return entities, nil
}

// DeleteEntity removes an entity and all its relations
func (s *Store) DeleteEntity(id, projectID string) error {
	// First verify the entity exists and belongs to this project
	_, err := s.GetEntity(id, projectID)
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

	return nil
}
