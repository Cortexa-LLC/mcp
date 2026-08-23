package kglib

import (
	"fmt"
)

// Export.
//
// An export is the graph's current state written in the journal's own record
// vocabulary — the same JSONL lines, all of them creates. That choice makes
// three things fall out for free:
//
//   - Import is replay. ReplayJournal already resolves entities by name and
//     type, skips what is already present, and creates missing parents, so
//     importing is idempotent without a second implementation.
//   - Export is journal compaction. A journal that has accumulated a create,
//     six edits and a delete collapses to whatever the graph actually holds now.
//   - A backup and a journal are interchangeable files, so the personal store's
//     backup story and its migration story are the same story.
//
// Records are emitted entities-first, then observations, then relations, so a
// replay never has to invent a parent. Within each kind they are ordered by
// name so that two exports of the same graph are byte-identical and diffable.
//
// Each record carries the row's real creation time rather than the time of the
// export. That keeps exports deterministic — re-exporting an unchanged graph
// produces an identical file — and means a dump dropped in as a journal
// describes when the knowledge was actually recorded.

// ExportProject streams a project's full state as journal records.
//
// The callback is handed each record in turn rather than a slice: a project
// graph can hold a hundred thousand entities, and the caller is invariably
// writing them straight to a file.
func (s *Store) ExportProject(projectID string, emit func(JournalRecord) error) error {
	if err := s.exportEntities(projectID, emit); err != nil {
		return err
	}
	if err := s.exportObservations(projectID, emit); err != nil {
		return err
	}
	return s.exportRelations(projectID, emit)
}

func (s *Store) exportEntities(projectID string, emit func(JournalRecord) error) error {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.project_id = $project_id
		RETURN e.name, e.type, e.created_at, e.id`+s.visibilityColumn()+`
		ORDER BY e.name, e.type, e.id
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return fmt.Errorf("export entities: %w", err)
	}
	defer result.Close()

	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return fmt.Errorf("export entities: %w", err)
		}
		row, err := tuple.GetAsSlice()
		tuple.Close()
		if err != nil {
			return fmt.Errorf("export entities: %w", err)
		}
		visibility := ""
		if len(row) > 4 {
			visibility = stringOrEmpty(row[4])
		}
		if err := emit(JournalRecord{
			Timestamp:  timeOrZero(row[2]),
			Op:         OpCreateEntity,
			ProjectID:  projectID,
			Entity:     newEntityRef(stringOrEmpty(row[3]), stringOrEmpty(row[0]), stringOrEmpty(row[1])),
			Visibility: visibility,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) exportObservations(projectID string, emit func(JournalRecord) error) error {
	result, err := s.QueryParams(`
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE e.project_id = $project_id
		RETURN e.name, e.type, o.content, o.created_at, e.id
		ORDER BY e.name, e.type, e.id, o.created_at, o.content
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return fmt.Errorf("export observations: %w", err)
	}
	defer result.Close()

	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return fmt.Errorf("export observations: %w", err)
		}
		row, err := tuple.GetAsSlice()
		tuple.Close()
		if err != nil {
			return fmt.Errorf("export observations: %w", err)
		}
		if err := emit(JournalRecord{
			Timestamp: timeOrZero(row[3]),
			Op:        OpCreateObservation,
			ProjectID: projectID,
			Entity:    newEntityRef(stringOrEmpty(row[4]), stringOrEmpty(row[0]), stringOrEmpty(row[1])),
			Content:   stringOrEmpty(row[2]),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) exportRelations(projectID string, emit func(JournalRecord) error) error {
	// Relation types come from the catalog, not from the configured list: a
	// read-only Store never runs initSchema and so has no configured types, and
	// the catalog also still holds tables an older config created.
	relTypes, err := s.relationTables()
	if err != nil {
		return err
	}

	for _, relType := range relTypes {
		// relType is a table name read from the catalog, not user input.
		query := fmt.Sprintf(`
			MATCH (from:Entity)-[:%s]->(to:Entity)
			WHERE from.project_id = $project_id AND to.project_id = $project_id
			RETURN from.name, from.type, to.name, to.type, from.created_at, from.id, to.id
			ORDER BY from.name, from.id, to.name, to.id
		`, relType)

		result, err := s.QueryParams(query, map[string]any{"project_id": projectID})
		if err != nil {
			return fmt.Errorf("export %s relations: %w", relType, err)
		}

		for result.HasNext() {
			tuple, err := result.Next()
			if err != nil {
				result.Close()
				return fmt.Errorf("export %s relations: %w", relType, err)
			}
			row, err := tuple.GetAsSlice()
			tuple.Close()
			if err != nil {
				result.Close()
				return fmt.Errorf("export %s relations: %w", relType, err)
			}
			if err := emit(JournalRecord{
				// A relation has no creation time of its own; the source
				// entity's is the closest honest stand-in.
				Timestamp: timeOrZero(row[4]),
				Op:        OpCreateRelation,
				ProjectID: projectID,
				From:      newEntityRef(stringOrEmpty(row[5]), stringOrEmpty(row[0]), stringOrEmpty(row[1])),
				To:        newEntityRef(stringOrEmpty(row[6]), stringOrEmpty(row[2]), stringOrEmpty(row[3])),
				RelType:   relType,
			}); err != nil {
				result.Close()
				return err
			}
		}
		result.Close()
	}
	return nil
}
