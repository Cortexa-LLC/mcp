package knowledge

import (
	"fmt"
	"strconv"
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

	// ObservationAge summarizes stored created_at across observations that
	// carry a real timestamp (ADR-009: newest/oldest/median observation age).
	// Zero-timestamp rows are excluded — they are what
	// ZeroTimestampObservations counts, and including them would report every
	// legacy-bearing graph as centuries old. Nil when no timestamped
	// observations exist.
	ObservationAge *ObservationAge `json:"observation_age,omitempty"`

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

	if m.ObservationAge, err = collectObservationAge(store, projectID); err != nil {
		return nil, fmt.Errorf("collect observation age: %w", err)
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

// ObservationAge holds the stored created_at of the newest, oldest, and
// median timestamped observation. Timestamps rather than durations, so the
// values are stable in snapshots; the CLI renders them as ages.
type ObservationAge struct {
	Newest time.Time `json:"newest"`
	Oldest time.Time `json:"oldest"`
	Median time.Time `json:"median"`
}

// timestampedObsFilter excludes NULL and stored-zero created_at rows — the
// same literal-not-parameter rule as the zero-timestamp count above.
const timestampedObsFilter = `o.created_at IS NOT NULL AND o.created_at <> timestamp("0001-01-01 00:00:00")`

// collectObservationAge computes newest/oldest/median stored created_at over
// the project's timestamped observations. Nil when there are none.
//
// The count driving the median's offset is taken here, over the same
// predicate as the median query itself, rather than derived by the caller
// from the total and zero-timestamp counts. Those agree in a quiet database,
// but they are separate queries on a read-only connection with no snapshot
// across them: a concurrent `kg index` deleting rows in between would make
// the offset overshoot the result set, and an off-by-one there silently
// shifts the median. One predicate, one count, one query.
func collectObservationAge(store *Store, projectID string) (*ObservationAge, error) {
	timestamped, err := countQuery(store, `
		MATCH (e:Entity {project_id: $project_id})-[:HAS_OBSERVATION]->(o:Observation)
		WHERE `+timestampedObsFilter+`
		RETURN count(*)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, err
	}
	if timestamped <= 0 {
		return nil, nil
	}

	age := &ObservationAge{}
	result, err := store.QueryParams(`
		MATCH (e:Entity {project_id: $project_id})-[:HAS_OBSERVATION]->(o:Observation)
		WHERE `+timestampedObsFilter+`
		RETURN max(o.created_at), min(o.created_at)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, err
	}
	if !result.HasNext() {
		// Same race the median query below guards against, and the same
		// answer: a writer deleted the rows between the count and this query.
		//
		// Falling through instead would leave Newest and Oldest at the zero
		// time, which printHealth renders via humanAge(gen.Sub(oa.Newest)) as
		// an age of roughly two thousand years — the precise "every graph
		// reads as centuries old" symptom this metric was added to expose.
		result.Close()
		return nil, nil
	}
	row, err := result.Next()
	if err != nil {
		result.Close()
		return nil, err
	}
	if age.Newest, err = timeCell(row, 0); err != nil {
		result.Close()
		return nil, err
	}
	if age.Oldest, err = timeCell(row, 1); err != nil {
		result.Close()
		return nil, err
	}
	result.Close()

	// Median by position: the middle row (lower of the two for even counts)
	// of the timestamped observations in created_at order.
	// Plain concatenation, not Sprintf: the format string would embed
	// timestampedObsFilter, so a '%' ever appearing in that shared constant
	// would silently corrupt this query.
	result, err = store.QueryParams(`
		MATCH (e:Entity {project_id: $project_id})-[:HAS_OBSERVATION]->(o:Observation)
		WHERE `+timestampedObsFilter+`
		RETURN o.created_at
		ORDER BY o.created_at
		SKIP `+strconv.Itoa((timestamped-1)/2)+` LIMIT 1
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, err
	}
	defer result.Close()
	if !result.HasNext() {
		// A writer deleted rows between the count and this query. The age
		// stats are one line of a report — losing them is not worth failing
		// the whole run over, so report no age rather than an error.
		return nil, nil
	}
	// `=` not `:=`: row is already declared by the max/min read above.
	row, err = result.Next()
	if err != nil {
		return nil, err
	}
	if age.Median, err = timeCell(row, 0); err != nil {
		return nil, err
	}
	return age, nil
}

// timeCell reads a timestamp cell. An unexpected type is an error, not a zero
// time — the silent-fallback rule from intFromCount applies doubly to the
// metric that exists because timestamps were once silently zeroed.
func timeCell(row interface{ GetValue(uint64) (any, error) }, col uint64) (time.Time, error) {
	v, err := row.GetValue(col)
	if err != nil {
		return time.Time{}, err
	}
	t, ok := v.(time.Time)
	if !ok {
		return time.Time{}, fmt.Errorf("timestamp cell has unexpected type %T", v)
	}
	return t.UTC(), nil
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
