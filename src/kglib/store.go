package kglib

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
// Bounds applied to read-only opens so that many databases can be open at once
// (see openStoreWithConfig). Knowledge graphs are tiny relative to both values.
const (
	readOnlyBufferPoolSize = 256 * 1024 * 1024 // 256 MiB
	readOnlyMaxDBSize      = 1 << 37           // 128 GiB of reserved address space
)

type Store struct {
	db              *kuzu.Database
	conn            *kuzu.Connection
	mu              sync.Mutex // guards all s.conn calls
	path            string
	hnswIdx         *vectorIndexCache // per-project lazy HNSW index
	allowedRelTypes []string          // configured relation types for validation
	journal         *Journal          // hand-write journal; nil unless EnableJournal was called
	hasVisibility   bool              // Entity.visibility exists in this database (see entityColumns)
}

// OpenStore opens or creates a Kuzu database in read-write mode with the given schema configuration.
// Use OpenStoreReadOnly for concurrent read access.
func OpenStore(dbPath string, cfg *SchemaConfig) (*Store, error) {
	return openStoreWithConfig(dbPath, false, cfg)
}

// OpenStoreReadOnly opens a Kuzu database in read-only mode.
// Multiple processes can hold read-only opens simultaneously.
// The database must already exist (read-only mode cannot create/migrate schema).
func OpenStoreReadOnly(dbPath string) (*Store, error) {
	return openStoreWithConfig(dbPath, true, nil)
}

func openStoreWithConfig(dbPath string, readOnly bool, cfg *SchemaConfig) (*Store, error) {
	// Ensure parent directory exists (only needed for write mode)
	if !readOnly {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	kuzuCfg := kuzu.DefaultSystemConfig()
	kuzuCfg.ReadOnly = readOnly

	// A federated query opens one database per layer simultaneously, and Kuzu's
	// defaults are sized for a process holding a single database open:
	//
	//   - MaxDbSize defaults to unlimited, which reserves ~8 TiB of virtual
	//     address space per database. On a 47-bit address space (arm64 macOS)
	//     the 16th open exhausts it and fails.
	//   - BufferPoolSize defaults to 80% of total system memory per database.
	//
	// Either way Kuzu returns a bare "status 1", which the caller below cannot
	// distinguish from genuine lock contention. Read-only layers only need
	// enough pool to cache the pages they touch, so bound both and let
	// federation scale to hundreds of layers.
	if readOnly {
		kuzuCfg.BufferPoolSize = readOnlyBufferPoolSize
		kuzuCfg.MaxDbSize = readOnlyMaxDBSize
	}

	// Open database
	db, err := kuzu.OpenDatabase(dbPath, kuzuCfg)
	if err != nil {
		return nil, openDiagnostic(dbPath, readOnly, err)
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
		if err := store.initSchema(cfg); err != nil {
			store.Close()
			return nil, fmt.Errorf("initialize schema: %w", err)
		}

		// Record which engine build wrote this database. Reaching this point
		// proves the running engine can read the format, so the stamp is
		// truthful by construction. It is a sidecar rather than a row in the
		// database because its whole job is to be readable when the database is
		// not. A failure to stamp is not fatal — the database is fine, it just
		// falls back to the unstamped path on a later version check.
		_ = WriteFormatStamp(dbPath, buildVersion)
	}

	// Which optional columns this database actually has. After initSchema on a
	// write open, so a freshly-migrated database is detected correctly; and on
	// read-only opens too, which never migrate and so may be reading a graph
	// written before a column existed.
	store.detectColumns()

	return store, nil
}

// buildVersion is the kg version recorded in format stamps for diagnostics.
// Set by the kg binary through SetBuildVersion; empty in library use.
var buildVersion string

// SetBuildVersion tells kglib which kg build is running, for provenance
// recorded in format stamps.
func SetBuildVersion(v string) { buildVersion = v }

// openDiagnostic turns go-kuzu's opaque open failure into something actionable.
//
// go-kuzu reports every failure as "failed to open database with status N",
// having discarded the engine's own message, so a missing database, a held
// lock, and a storage-format mismatch are indistinguishable from the error
// alone. The previous version of this code assumed a lock, which is why opening
// a database that did not exist reported it as locked by another process.
//
// What can be determined without the engine is checked directly: whether the
// file is there, and what the format sidecar says wrote it.
func openDiagnostic(dbPath string, readOnly bool, err error) error {
	status, stamp, checkErr := CheckFormat(dbPath)
	if checkErr == nil {
		switch status {
		case FormatMissing:
			if readOnly {
				// Read-only opens cannot create a database, so this is the whole
				// explanation rather than a symptom of something else.
				return fmt.Errorf("no knowledge graph database at %s "+
					"(create one with 'kg index'): %w", dbPath, err)
			}
		case FormatMismatch:
			return fmt.Errorf("knowledge graph database at %s was written by kuzu %s "+
				"but this build uses %s; it needs rebuilding "+
				"(run 'kg index' to rebuild it, or 'kg migrate' to rebuild every graph): %w",
				dbPath, stamp.KuzuVersion, KuzuVersion(), err)
		}
	}

	if strings.Contains(err.Error(), "status 1") {
		return fmt.Errorf("could not open the knowledge graph database at %s — "+
			"it is most likely locked by another process (is indexing running?), "+
			"but a corrupt database reports the same way: %w", dbPath, err)
	}
	return fmt.Errorf("open kuzu database: %w", err)
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

// Query runs a raw Cypher statement and returns the Kuzu result handle.
// Use only for schema DDL and other statements that contain no user-supplied values.
// For queries containing user input, use QueryParams instead.
// mu is held for the duration of the call; result iteration is safe after release.
func (s *Store) Query(stmt string) (*kuzu.QueryResult, error) {
	s.mu.Lock()
	result, err := s.conn.Query(stmt)
	s.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	return result, nil
}

// QueryParams prepares a Cypher statement and executes it with bound parameters,
// preventing Cypher injection from user-supplied string values.
// Use $paramName placeholders in stmt and provide matching keys in params.
// mu is held for the prepare→execute sequence; result iteration is safe after release.
func (s *Store) QueryParams(stmt string, params map[string]any) (*kuzu.QueryResult, error) {
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
	result, err := s.QueryParams(query, map[string]any{"project_id": projectID})
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

// relationTables returns every Entity→Entity relationship table present in the
// database, read from the catalog rather than from s.allowedRelTypes.
//
// allowedRelTypes is populated by initSchema, which only runs on read-write
// opens — a read-only Store therefore has an empty list. Anything deriving
// relation types from it silently sees zero tables. The catalog is also the more
// truthful source generally: it reflects tables a previous SchemaConfig created,
// which the current config may no longer list.
//
// HAS_OBSERVATION is excluded: it is the structural Entity→Observation edge and
// is counted by CountObservations instead.
func (s *Store) relationTables() ([]string, error) {
	result, err := s.Query("CALL show_tables() RETURN name, type")
	if err != nil {
		return nil, fmt.Errorf("list relation tables: %w", err)
	}
	defer result.Close()

	var tables []string
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("list relation tables: %w", err)
		}
		nameVal, _ := row.GetValue(0)
		typeVal, _ := row.GetValue(1)
		name, _ := nameVal.(string)
		tableType, _ := typeVal.(string)
		if tableType != "REL" || name == "HAS_OBSERVATION" {
			continue
		}
		tables = append(tables, name)
	}
	return tables, nil
}

// CountRelations returns the total number of relations for a project
func (s *Store) CountRelations(projectID string) (int, error) {
	relTypes, err := s.relationTables()
	if err != nil {
		return 0, err
	}

	totalCount := 0
	for _, relType := range relTypes {
		// Relationship table names come from the catalog, not from user input,
		// so interpolating them is safe; Kuzu cannot parameterise a label.
		query := fmt.Sprintf(`
			MATCH (from:Entity {project_id: $project_id})-[r:%s]->(to:Entity {project_id: $project_id})
			RETURN count(*) AS count
		`, relType)
		result, err := s.QueryParams(query, map[string]any{"project_id": projectID})
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
	result, err := s.QueryParams(query, map[string]any{"project_id": projectID})
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
