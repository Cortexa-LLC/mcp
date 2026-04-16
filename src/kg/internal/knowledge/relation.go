package knowledge

import (
	"fmt"
	"time"
)

// CreateRelation creates a directed relationship between two entities
func (s *Store) CreateRelation(fromID, toID, relType, projectID string) error {
	// Verify both entities exist and belong to this project
	_, err := s.GetEntity(fromID, projectID)
	if err != nil {
		return fmt.Errorf("source entity: %w", err)
	}

	_, err = s.GetEntity(toID, projectID)
	if err != nil {
		return fmt.Errorf("target entity: %w", err)
	}

	if err := validateRelType(relType); err != nil {
		return err
	}

	// relType is a relationship label (not a value) and cannot be parameterised
	// in Cypher. It is validated against AllowedRelTypes above before use.
	query := fmt.Sprintf(`
		MATCH (from:Entity), (to:Entity)
		WHERE from.id = $from_id AND to.id = $to_id
		CREATE (from)-[:%s]->(to)
	`, relType)

	result, err := s.queryParams(query, map[string]any{
		"from_id": fromID,
		"to_id":   toID,
	})
	if err != nil {
		return fmt.Errorf("create relation: %w", err)
	}
	defer result.Close()

	return nil
}

// GetRelations retrieves all outgoing relations from an entity
func (s *Store) GetRelations(entityID, projectID string) ([]*Relation, error) {
	// Verify entity exists and belongs to this project
	_, err := s.GetEntity(entityID, projectID)
	if err != nil {
		return nil, err
	}

	result, err := s.queryParams(`
		MATCH (from:Entity)-[r]->(to:Entity)
		WHERE from.id = $entity_id AND from.project_id = $project_id
		RETURN from.id, to.id, label(r)
	`, map[string]any{"entity_id": entityID, "project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query relations: %w", err)
	}
	defer result.Close()

	var relations []*Relation
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

		relation := &Relation{
			FromID: stringOrEmpty(row[0]),
			ToID:   stringOrEmpty(row[1]),
			Type:   stringOrEmpty(row[2]),
		}

		relations = append(relations, relation)
	}

	return relations, nil
}

// DeleteRelation removes a specific relation between two entities
func (s *Store) DeleteRelation(fromID, toID, relType, projectID string) error {
	if err := validateRelType(relType); err != nil {
		return err
	}

	// Verify both entities exist and belong to this project
	_, err := s.GetEntity(fromID, projectID)
	if err != nil {
		return fmt.Errorf("source entity: %w", err)
	}

	_, err = s.GetEntity(toID, projectID)
	if err != nil {
		return fmt.Errorf("target entity: %w", err)
	}

	// relType is a relationship label validated against AllowedRelTypes above.
	query := fmt.Sprintf(`
		MATCH (from:Entity)-[r:%s]->(to:Entity)
		WHERE from.id = $from_id AND to.id = $to_id AND from.project_id = $project_id
		DELETE r
	`, relType)

	result, err := s.queryParams(query, map[string]any{
		"from_id":    fromID,
		"to_id":      toID,
		"project_id": projectID,
	})
	if err != nil {
		return fmt.Errorf("delete relation: %w", err)
	}
	defer result.Close()

	return nil
}

// TraverseRelations follows a relation type from an entity and returns connected entities
func (s *Store) TraverseRelations(entityID, relType, projectID string) ([]*Entity, error) {
	if err := validateRelType(relType); err != nil {
		return nil, err
	}

	// Verify source entity exists and belongs to this project
	_, err := s.GetEntity(entityID, projectID)
	if err != nil {
		return nil, err
	}

	// relType is a relationship label validated against AllowedRelTypes above.
	query := fmt.Sprintf(`
		MATCH (from:Entity)-[:%s]->(to:Entity)
		WHERE from.id = $entity_id AND from.project_id = $project_id
		RETURN to.id, to.name, to.type, to.project_id, to.created_at, to.updated_at
	`, relType)

	result, err := s.queryParams(query, map[string]any{
		"entity_id":  entityID,
		"project_id": projectID,
	})
	if err != nil {
		return nil, fmt.Errorf("traverse relations: %w", err)
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
