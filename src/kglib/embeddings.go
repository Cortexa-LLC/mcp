package kglib

import (
	"context"
	"fmt"
)

// BatchEmbed processes all un-embedded entities and observations in a project
func (s *Store) BatchEmbed(ctx context.Context, projectID string, embedder Embedder) error {
	// Get un-embedded entities
	entities, err := s.GetUnembeddedEntities(projectID)
	if err != nil {
		return fmt.Errorf("get un-embedded entities: %w", err)
	}

	if len(entities) > 0 {
		// Prepare texts for batch embedding
		texts := make([]string, len(entities))
		for i, entity := range entities {
			texts[i] = fmt.Sprintf("%s: %s", entity.Type, entity.Name)
		}

		// Generate embeddings
		embeddings, err := embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("generate embeddings: %w", err)
		}
		// Embed talks to an external service (or any third-party Embedder
		// implementation), so the returned slice is untrusted input, not a
		// language-level guarantee. A short response used to be an
		// index-out-of-range panic in the loop below -- a remote party crashing
		// the process. Length is the whole contract here: embeddings[i] is only
		// the vector for entities[i] if the two line up positionally.
		if len(embeddings) != len(texts) {
			return fmt.Errorf("embedder returned %d embeddings for %d entities: "+
				"results are matched by position, so a mismatched count cannot be used", len(embeddings), len(texts))
		}

		// Store embeddings. entity.ProjectID equals the projectID being embedded
		// (GetUnembeddedEntities filters on it); passing it keeps SetEmbedding on
		// this project's node and invalidates this project's index.
		for i, entity := range entities {
			if err := s.SetEmbedding(entity.ID, entity.ProjectID, embeddings[i]); err != nil {
				return fmt.Errorf("set embedding for entity %s: %w", entity.ID, err)
			}
		}
	}

	// Get un-embedded observations
	observations, err := s.GetUnembeddedObservations(projectID)
	if err != nil {
		return fmt.Errorf("get un-embedded observations: %w", err)
	}

	if len(observations) == 0 {
		return nil
	}

	// Prepare observation texts
	obsTexts := make([]string, len(observations))
	for i, obs := range observations {
		obsTexts[i] = obs.Content
	}

	// Generate observation embeddings
	obsEmbeddings, err := embedder.Embed(ctx, obsTexts)
	if err != nil {
		return fmt.Errorf("generate observation embeddings: %w", err)
	}
	if len(obsEmbeddings) != len(obsTexts) {
		return fmt.Errorf("embedder returned %d embeddings for %d observations: "+
			"results are matched by position, so a mismatched count cannot be used", len(obsEmbeddings), len(obsTexts))
	}

	// Store observation embeddings. projectID is the project BatchEmbed is
	// embedding (GetUnembeddedObservations filtered on it), so it scopes the
	// write to this project's observation rows.
	for i, obs := range observations {
		if err := s.SetObservationEmbedding(obs.ID, projectID, obsEmbeddings[i]); err != nil {
			return fmt.Errorf("set embedding for observation %s: %w", obs.ID, err)
		}
	}

	return nil
}

// GetUnembeddedEntities returns all entities without embeddings
func (s *Store) GetUnembeddedEntities(projectID string) ([]Entity, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.project_id = $project_id AND e.embedding IS NULL
		RETURN `+s.entityColumns()+`
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query un-embedded entities: %w", err)
	}
	defer result.Close()

	var entities []Entity
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next row: %w", err)
		}

		id, _ := row.GetValue(0)
		name, _ := row.GetValue(1)
		typ, _ := row.GetValue(2)
		projID, _ := row.GetValue(3)

		// GetValue already swallows its own error into the discarded second
		// return, so these values can be nil; a bare .(string) would panic on the
		// first NULL column instead of yielding an empty string.
		entity := Entity{
			ID:        stringOrEmpty(id),
			Name:      stringOrEmpty(name),
			Type:      stringOrEmpty(typ),
			ProjectID: stringOrEmpty(projID),
		}
		entities = append(entities, entity)
	}

	return entities, nil
}

// GetUnembeddedObservations returns all observations without embeddings
func (s *Store) GetUnembeddedObservations(projectID string) ([]Observation, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE e.project_id = $project_id AND o.embedding IS NULL
		RETURN o.id, o.entity_id, o.content, o.created_at
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query un-embedded observations: %w", err)
	}
	defer result.Close()

	var observations []Observation
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next row: %w", err)
		}

		id, _ := row.GetValue(0)
		entityID, _ := row.GetValue(1)
		content, _ := row.GetValue(2)

		obs := Observation{
			ID:       stringOrEmpty(id),
			EntityID: stringOrEmpty(entityID),
			Content:  stringOrEmpty(content),
		}
		observations = append(observations, obs)
	}

	return observations, nil
}

// SetEmbedding stores an embedding vector for an entity and invalidates the
// in-memory HNSW index for the entity's project so that the next VectorSearch
// call rebuilds the index with the new vector.
//
// The entity is matched by its project-namespaced pk, not by id alone: under
// multi-project-per-DB a bare-id MATCH would set the embedding on every
// project's node sharing that id (and the old project_id lookup returned an
// arbitrary one of them, invalidating the wrong project's HNSW index). projectID
// is required and is the project whose index is invalidated.
func (s *Store) SetEmbedding(entityID, projectID string, embedding []float32) error {
	result, err := s.QueryParams(`
		MATCH (e:Entity {pk: $pk})
		SET e.embedding = $embedding
	`, map[string]any{"pk": PK(projectID, entityID), "embedding": embedding})
	if err != nil {
		return fmt.Errorf("set embedding: %w", err)
	}
	defer result.Close()

	// Invalidate the HNSW index so the next VectorSearch rebuilds it.
	if projectID != "" {
		s.hnswIdx.invalidate(projectID)
	}

	return nil
}

// SetObservationEmbedding stores an embedding vector for an observation.
//
// Matched by the project-namespaced pk, consistent with SetEmbedding and every
// other write: an observation id is a UUID so a cross-project collision is
// unlikely, but a bare-id match is the same project-blind pattern this change
// removes elsewhere, and there is no reason to leave one write path on it. No
// !hasPK fallback is needed — writes only happen on a read-write open, which
// refuses a pre-pk database outright (see openStoreWithConfig).
func (s *Store) SetObservationEmbedding(observationID, projectID string, embedding []float32) error {
	result, err := s.QueryParams(`
		MATCH (o:Observation {pk: $pk})
		SET o.embedding = $embedding
	`, map[string]any{"pk": PK(projectID, observationID), "embedding": embedding})
	if err != nil {
		return fmt.Errorf("set observation embedding: %w", err)
	}
	defer result.Close()

	return nil
}
