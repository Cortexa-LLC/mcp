package knowledge

import (
	"fmt"
	"time"
)

// HealthMetrics is one measurement of a project's knowledge graph, as reported
// by `kg health`. Everything here is computed read-only; the CLI persists the
// previous measurement to report growth between runs.
type HealthMetrics struct {
	Entities       int            `json:"entities"`
	EntitiesByType map[string]int `json:"entities_by_type"`
	Relations      int            `json:"relations"`
	Observations   int            `json:"observations"`

	// ZeroTimestampObservations counts observations whose STORED created_at is
	// NULL or the zero time — "legacy, age unknown". This is a property of the
	// data, not of the Go scan: a database written by this tool always stores
	// real timestamps (see timeOrZero for the read-side bug this survived), so
	// a non-zero count here means some other or older writer produced the rows.
	ZeroTimestampObservations int `json:"zero_timestamp_observations"`

	// OrphanedEntities counts entities with no observations and no relations
	// in either direction — nodes nothing points at and that say nothing.
	OrphanedEntities int `json:"orphaned_entities"`

	// ObsoleteObservations counts observations whose content starts with
	// "[OBSOLETE" — knowledge curated into history rather than deleted.
	ObsoleteObservations int `json:"obsolete_observations"`

	GeneratedAt time.Time `json:"generated_at"`
}

// CollectHealthMetrics computes HealthMetrics for one project in one store.
// Read-only: safe against a store other processes are using.
func CollectHealthMetrics(store *Store, projectID string) (*HealthMetrics, error) {
	m := &HealthMetrics{
		EntitiesByType: make(map[string]int),
		GeneratedAt:    time.Now().UTC(),
	}

	var err error
	if m.Entities, err = store.CountEntities(projectID); err != nil {
		return nil, fmt.Errorf("count entities: %w", err)
	}
	if m.Relations, err = store.CountRelations(projectID); err != nil {
		return nil, fmt.Errorf("count relations: %w", err)
	}
	if m.Observations, err = store.CountObservations(projectID); err != nil {
		return nil, fmt.Errorf("count observations: %w", err)
	}

	result, err := store.QueryParams(`
		MATCH (e:Entity {project_id: $project_id})
		RETURN e.type, count(*)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("count entities by type: %w", err)
	}
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("count entities by type: %w", err)
		}
		typeVal, _ := row.GetValue(0)
		countVal, err := row.GetValue(1)
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("count entities by type: %w", err)
		}
		entityType, _ := typeVal.(string)
		if entityType == "" {
			entityType = "(untyped)"
		}
		n, err := intFromCount(countVal)
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("count entities by type %q: %w", entityType, err)
		}
		m.EntitiesByType[entityType] = n
	}
	result.Close()

	// Stored zeros, not scan artifacts: the comparison happens inside Kuzu.
	// The zero time is a Cypher LITERAL, deliberately: binding time.Time{} as
	// a parameter does not work — go-kuzu converts parameters through
	// UnixNano, which overflows for year 1 and arrives as a 1754 date, so a
	// bound "zero" never equals a stored zero. (Same int64 family of bug as
	// timeOrZero on the read side.)
	m.ZeroTimestampObservations, err = countQuery(store, `
		MATCH (e:Entity {project_id: $project_id})-[:HAS_OBSERVATION]->(o:Observation)
		WHERE o.created_at IS NULL OR o.created_at = timestamp("0001-01-01 00:00:00")
		RETURN count(*)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("count zero-timestamp observations: %w", err)
	}

	// The unlabeled patterns match every relationship table, HAS_OBSERVATION
	// included, so one predicate covers "no observations AND no relations".
	m.OrphanedEntities, err = countQuery(store, `
		MATCH (e:Entity {project_id: $project_id})
		WHERE NOT EXISTS { MATCH (e)-[]->() } AND NOT EXISTS { MATCH (e)<-[]-() }
		RETURN count(*)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("count orphaned entities: %w", err)
	}

	m.ObsoleteObservations, err = countQuery(store, `
		MATCH (e:Entity {project_id: $project_id})-[:HAS_OBSERVATION]->(o:Observation)
		WHERE o.content STARTS WITH '[OBSOLETE'
		RETURN count(*)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("count [OBSOLETE observations: %w", err)
	}

	return m, nil
}

// countQuery runs a single-row count(*) query and returns the count.
func countQuery(store *Store, query string, params map[string]any) (int, error) {
	result, err := store.QueryParams(query, params)
	if err != nil {
		return 0, err
	}
	defer result.Close()

	if !result.HasNext() {
		return 0, nil
	}
	row, err := result.Next()
	if err != nil {
		return 0, err
	}
	v, err := row.GetValue(0)
	if err != nil {
		return 0, err
	}
	return intFromCount(v)
}

// intFromCount converts a Kuzu count(*) cell to int. Kuzu returns int64;
// uint64 is accepted defensively. Anything else is an error, not a silent 0 —
// a health metric that quietly reads as zero is the exact failure mode this
// command exists to expose.
func intFromCount(v any) (int, error) {
	switch n := v.(type) {
	case int64:
		return int(n), nil
	case uint64:
		return int(n), nil
	}
	return 0, fmt.Errorf("count cell has unexpected type %T", v)
}
