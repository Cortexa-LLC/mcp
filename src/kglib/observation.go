package kglib

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateObservation adds a new observation to an entity.
// The Observation node and its HAS_OBSERVATION edge are created in a single
// Cypher statement so the write is atomic – there is no window where an
// orphaned Observation node can exist if the relationship step fails.
func (s *Store) CreateObservation(entityID, content, projectID string) (*Observation, error) {
	// Verify entity exists and belongs to this project. The entity is kept
	// because the journal names its parent by (name, type), not by UUID.
	entity, err := s.GetEntity(entityID, projectID)
	if err != nil {
		return nil, fmt.Errorf("entity: %w", err)
	}

	obs := &Observation{
		ID:        uuid.New().String(),
		EntityID:  entityID,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}

	// Single atomic statement: match the parent Entity, create the Observation
	// node, and link them in one go.  Because everything happens in one Cypher
	// statement there is no risk of an orphaned Observation node if the edge
	// creation were to fail.
	result, err := s.QueryParams(`
		MATCH (e:Entity {id: $entity_id, project_id: $project_id})
		CREATE (o:Observation {
			id: $id,
			entity_id: $entity_id,
			content: $content,
			created_at: $created_at
		})
		CREATE (e)-[:HAS_OBSERVATION]->(o)
	`, map[string]any{
		"entity_id":  entityID,
		"project_id": projectID,
		"id":         obs.ID,
		"content":    content,
		"created_at": obs.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create observation: %w", err)
	}
	result.Close()

	if err := s.appendJournal(JournalRecord{
		Op:        OpCreateObservation,
		ProjectID: projectID,
		Entity:    entityRef(entity),
		Content:   content,
	}); err != nil {
		return obs, errJournalNote(err)
	}

	return obs, nil
}

// GetObservations retrieves all observations for an entity
func (s *Store) GetObservations(entityID, projectID string) ([]*Observation, error) {
	// Verify entity exists and belongs to this project
	_, err := s.GetEntity(entityID, projectID)
	if err != nil {
		return nil, err
	}

	result, err := s.QueryParams(`
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE e.id = $entity_id AND e.project_id = $project_id
		RETURN o.id, o.entity_id, o.content, o.created_at
	`, map[string]any{"entity_id": entityID, "project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer result.Close()

	var observations []*Observation
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

		obs := &Observation{
			ID:       stringOrEmpty(row[0]),
			EntityID: stringOrEmpty(row[1]),
			Content:  stringOrEmpty(row[2]),
		}

		obs.CreatedAt = timeOrZero(row[3])

		observations = append(observations, obs)
	}

	return observations, nil
}

// DeleteObservation removes an observation
func (s *Store) DeleteObservation(obsID, entityID, projectID string) error {
	// Verify entity exists and belongs to this project
	entity, err := s.GetEntity(entityID, projectID)
	if err != nil {
		return err
	}

	// Observations have no name — the journal identifies one by its content and
	// parent, so the content has to be read before the node goes away. Only
	// needed when journaling is on; deletes are rare enough that the extra read
	// costs nothing worth optimising.
	var deletedContent string
	if s.JournalEnabled() {
		existing, err := s.GetObservations(entityID, projectID)
		if err != nil {
			return fmt.Errorf("read observation before delete: %w", err)
		}
		for _, o := range existing {
			if o.ID == obsID {
				deletedContent = o.Content
				break
			}
		}
	}

	result, err := s.QueryParams(`
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE o.id = $obs_id AND e.id = $entity_id AND e.project_id = $project_id
		DETACH DELETE o
	`, map[string]any{"obs_id": obsID, "entity_id": entityID, "project_id": projectID})
	if err != nil {
		return fmt.Errorf("delete observation: %w", err)
	}
	defer result.Close()

	if err := s.appendJournal(JournalRecord{
		Op:        OpDeleteObservation,
		ProjectID: projectID,
		Entity:    entityRef(entity),
		Content:   deletedContent,
	}); err != nil {
		return errJournalNote(err)
	}

	return nil
}
