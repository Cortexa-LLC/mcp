package kglib

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// entityColumnsLegacy is the projection every database has had since the
// beginning. entityColumns adds whatever this database actually carries on top.
//
// Kept in one place because the columns and the scanner below have to agree,
// and they were previously duplicated across six queries — a column added to
// one and missed in another fails at runtime, not at compile time.
const entityColumnsLegacy = "e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at"

// entityColumns returns the RETURN clause for this database.
//
// It has to be per-database rather than a constant because read-only opens
// never run initSchema, so a database written before a column existed will not
// have gained it. Kuzu rejects the whole query with "Cannot find property
// visibility for e" rather than returning null, so asking for a column that is
// not there breaks every read path — search, federation, remote layers — on any
// graph that has not been re-indexed since the upgrade.
func (s *Store) entityColumns() string {
	if s.hasVisibility {
		return entityColumnsLegacy + ", e.visibility"
	}
	return entityColumnsLegacy
}

// createEntityWithID creates an entity under a caller-supplied id.
//
// Used by replay to restore an entity under the same source-derived id it had
// before — "function:<path>:<name>" and friends, which the indexer computes
// from source text and so reproduces exactly. Without this, an import mints a
// fresh UUID and every ID hint in the file becomes unresolvable, which throws
// the restore back onto (name, type) resolution and the collisions it exists to
// avoid.
//
// Only for ids that are stable by construction. Hand-created entities keep
// CreateEntity's generated UUID, which is meaningless to preserve.
func (s *Store) createEntityWithID(id, name, entityType, projectID string) (*Entity, error) {
	entity := &Entity{
		ID:        id,
		Name:      name,
		Type:      entityType,
		ProjectID: projectID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	result, err := s.QueryParams(`
		CREATE (e:Entity {
			id: $id, name: $name, type: $type, project_id: $project_id,
			created_at: $created_at, updated_at: $updated_at
		})
	`, map[string]any{
		"id": id, "name": name, "type": entityType, "project_id": projectID,
		"created_at": entity.CreatedAt, "updated_at": entity.UpdatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create entity %s: %w", id, err)
	}
	result.Close()
	return entity, nil
}

// setEntityVisibility records a symbol's source-language visibility on an entity
// that already exists. Used by replay to restore what an export captured; the
// indexer sets the column directly during bulk load.
func (s *Store) setEntityVisibility(id, visibility, projectID string) error {
	if !s.hasVisibility {
		// A database predating the column cannot store it. Not an error: the
		// entity and its knowledge are intact, only the ranking hint is lost.
		return nil
	}
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.id = $id AND e.project_id = $project_id
		SET e.visibility = $visibility
	`, map[string]any{"id": id, "project_id": projectID, "visibility": visibility})
	if err != nil {
		return fmt.Errorf("set visibility: %w", err)
	}
	result.Close()
	return nil
}

// visibilityColumn returns ", e.visibility" when this database has the column,
// and "" when it does not — Kuzu rejects a whole query that names a missing
// property, so a projection cannot assume it.
func (s *Store) visibilityColumn() string {
	if s.hasVisibility {
		return ", e.visibility"
	}
	return ""
}

// detectColumns records which optional columns this database carries. Called
// once at open, for both read-only and read-write opens.
func (s *Store) detectColumns() {
	result, err := s.Query("CALL table_info('Entity') RETURN *")
	if err != nil {
		// A database too old or too odd to introspect is assumed to lack the
		// column, which is the safe direction: the legacy projection works
		// everywhere, it just returns less.
		return
	}
	defer result.Close()

	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return
		}
		row, err := tuple.GetAsSlice()
		tuple.Close()
		if err != nil {
			return
		}
		if len(row) > 1 && stringOrEmpty(row[1]) == "visibility" {
			s.hasVisibility = true
			return
		}
	}
}

// entityFromRow builds an Entity from a row shaped by entityColumns.
func entityFromRow(row []any) *Entity {
	entity := &Entity{
		ID:        stringOrEmpty(row[0]),
		Name:      stringOrEmpty(row[1]),
		Type:      stringOrEmpty(row[2]),
		ProjectID: stringOrEmpty(row[3]),
		CreatedAt: timeOrZero(row[4]),
		UpdatedAt: timeOrZero(row[5]),
	}
	// Older rows predate the column entirely, so the slice may be short.
	if len(row) > 6 {
		entity.Visibility = stringOrEmpty(row[6])
	}
	return entity
}

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
		Entity:    &EntityRef{Name: name, Type: entityType, ID: stableIDHint(entity.ID)},
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
		RETURN `+s.entityColumns()+`
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

	entity := entityFromRow(row)

	return entity, nil
}

// GetEntityByName retrieves an entity by name for a specific project.
// Returns (nil, nil) when no entity with that name exists.
func (s *Store) GetEntityByName(name, projectID string) (*Entity, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.name = $name AND e.project_id = $project_id
		RETURN `+s.entityColumns()+`
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

	entity := entityFromRow(row)
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
		RETURN `+s.entityColumns()+`
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

	entity := entityFromRow(row)
	return entity, nil
}

// FindEntitiesByNameAndType returns every entity matching the tuple.
//
// Separate from GetEntityByNameAndType, which takes the first match, because
// the tuple is not unique — indexed code symbols share it across files — and a
// caller resolving a journal record needs to know when it is choosing
// arbitrarily rather than resolving.
func (s *Store) FindEntitiesByNameAndType(name, entityType, projectID string) ([]*Entity, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.name = $name AND e.type = $type AND e.project_id = $project_id
		RETURN `+s.entityColumns()+`
		ORDER BY e.id
	`, map[string]any{"name": name, "type": entityType, "project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query entities by name and type: %w", err)
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
		entities = append(entities, entityFromRow(row))
	}
	return entities, nil
}

// ListEntities retrieves all entities for a project, optionally filtered by type
func (s *Store) ListEntities(projectID, entityType string) ([]*Entity, error) {
	stmt := `
		MATCH (e:Entity)
		WHERE e.project_id = $project_id
		RETURN ` + s.entityColumns() + `
	`
	params := map[string]any{"project_id": projectID}
	if entityType != "" {
		stmt = `
			MATCH (e:Entity)
			WHERE e.project_id = $project_id AND e.type = $type
			RETURN ` + s.entityColumns() + `
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

		entity := entityFromRow(row)

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
		Entity:    entityRef(entity),
	}); err != nil {
		return errJournalNote(err)
	}

	return nil
}
