package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	kuzu "github.com/kuzudb/go-kuzu"
)

// Store manages the Kuzu knowledge graph database.
//
// Concurrency model: KuzuDB connections are not goroutine-safe.
// mu serialises all calls that touch s.conn (Query, Prepare, Execute).
// QueryResult iteration (HasNext/Next) operates on a materialised C struct
// and does not call back into the connection, so it is safe after mu is
// released.
type Store struct {
	db      *kuzu.Database
	conn    *kuzu.Connection
	mu      sync.Mutex // guards all s.conn calls
	path    string
	hnswIdx *vectorIndexCache // per-project lazy HNSW index
}

// OpenStore opens or creates a Kuzu database in read-write mode.
// Use OpenStoreReadOnly for concurrent read access.
func OpenStore(dbPath string) (*Store, error) {
	return openStoreWithConfig(dbPath, false)
}

// OpenStoreReadOnly opens a Kuzu database in read-only mode.
// Multiple processes can hold read-only opens simultaneously.
// The database must already exist (read-only mode cannot create/migrate schema).
func OpenStoreReadOnly(dbPath string) (*Store, error) {
	return openStoreWithConfig(dbPath, true)
}

func openStoreWithConfig(dbPath string, readOnly bool) (*Store, error) {
	// Ensure parent directory exists (only needed for write mode)
	if !readOnly {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	cfg := kuzu.DefaultSystemConfig()
	cfg.ReadOnly = readOnly

	// Open database
	db, err := kuzu.OpenDatabase(dbPath, cfg)
	if err != nil {
		// "status 1" is Kuzu's lock-acquisition failure — give a human-readable hint
		if strings.Contains(err.Error(), "status 1") {
			return nil, fmt.Errorf("knowledge graph database is locked by another process "+
				"(is `kg index` running?): %w", err)
		}
		return nil, fmt.Errorf("open kuzu database: %w", err)
	}

	// Create connection
	conn, err := kuzu.OpenConnection(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create connection: %w", err)
	}

	store := &Store{
		db:      db,
		conn:    conn,
		path:    dbPath,
		hnswIdx: newVectorIndexCache(),
	}

	// Initialize schema (DDL) only in read-write mode; read-only mode assumes
	// the schema was already created by a prior write-mode open.
	if !readOnly {
		if err := store.initSchema(); err != nil {
			store.Close()
			return nil, fmt.Errorf("initialize schema: %w", err)
		}
	}

	return store, nil
}

// Close closes the database connection.
// It acquires mu to ensure no query is in flight while closing.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
	return nil
}

// query runs a raw Cypher statement and returns the Kuzu result handle.
// Use only for schema DDL and other statements that contain no user-supplied values.
// For queries containing user input, use queryParams instead.
// mu is held for the duration of the call; result iteration is safe after release.
func (s *Store) query(stmt string) (*kuzu.QueryResult, error) {
	s.mu.Lock()
	result, err := s.conn.Query(stmt)
	s.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	return result, nil
}

// queryParams prepares a Cypher statement and executes it with bound parameters,
// preventing Cypher injection from user-supplied string values.
// Use $paramName placeholders in stmt and provide matching keys in params.
// mu is held for the prepare→execute sequence; result iteration is safe after release.
func (s *Store) queryParams(stmt string, params map[string]any) (*kuzu.QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prepared, err := s.conn.Prepare(stmt)
	if err != nil {
		return nil, fmt.Errorf("prepare query: %w", err)
	}
	defer prepared.Close()
	result, err := s.conn.Execute(prepared, params)
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	return result, nil
}

// CountEntities returns the total number of entities for a project
func (s *Store) CountEntities(projectID string) (int, error) {
	query := `MATCH (e:Entity {project_id: $project_id}) RETURN count(*) AS count`
	result, err := s.queryParams(query, map[string]any{"project_id": projectID})
	if err != nil {
		return 0, err
	}
	defer result.Close()

	if result.HasNext() {
		row, err := result.Next()
		if err != nil {
			return 0, err
		}
		countVal, _ := row.GetValue(0)
		count, _ := countVal.(int64)
		return int(count), nil
	}
	return 0, nil
}

// CountRelations returns the total number of relations for a project
func (s *Store) CountRelations(projectID string) (int, error) {
	// Count all typed relations
	totalCount := 0
	for _, relType := range AllowedRelTypes {
		query := fmt.Sprintf(`
			MATCH (from:Entity {project_id: $project_id})-[r:%s]->(to:Entity {project_id: $project_id})
			RETURN count(*) AS count
		`, relType)
		result, err := s.queryParams(query, map[string]any{"project_id": projectID})
		if err != nil {
			return 0, err
		}

		if result.HasNext() {
			row, err := result.Next()
			if err != nil {
				result.Close()
				return 0, err
			}
			countVal, _ := row.GetValue(0)
			count, _ := countVal.(int64)
			totalCount += int(count)
		}
		result.Close()
	}
	return totalCount, nil
}

// CountObservations returns the total number of observations for a project
func (s *Store) CountObservations(projectID string) (int, error) {
	query := `
		MATCH (e:Entity {project_id: $project_id})-[:HAS_OBSERVATION]->(o:Observation)
		RETURN count(*) AS count
	`
	result, err := s.queryParams(query, map[string]any{"project_id": projectID})
	if err != nil {
		return 0, err
	}
	defer result.Close()

	if result.HasNext() {
		row, err := result.Next()
		if err != nil {
			return 0, err
		}
		countVal, _ := row.GetValue(0)
		count, _ := countVal.(int64)
		return int(count), nil
	}
	return 0, nil
}
