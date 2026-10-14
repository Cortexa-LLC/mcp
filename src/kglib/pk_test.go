package kglib

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPKSeparatorSurvivesCGoRoundTrip pins the invariant the whole
// project-namespaced primary key depends on: the Unit Separator (0x1F) joining
// project_id and id must survive go-kuzu's cgo string marshalling intact, where
// a NUL would truncate it. The comment in pk.go asserts this; this test proves
// it against the real Store write+read path, not the Go PK() function alone.
func TestPKSeparatorSurvivesCGoRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pk.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	const projectID = "proj-alpha"
	entity, err := store.CreateEntity("main.go", "file", projectID)
	if err != nil {
		store.Close()
		t.Fatalf("CreateEntity: %v", err)
	}
	wantPK := PK(projectID, entity.ID)

	// Sanity: the value we expect to be stored actually contains the separator,
	// so a passing round-trip is meaningful rather than vacuous.
	if !strings.Contains(wantPK, pkSeparator) {
		t.Fatalf("derived pk %q does not contain the 0x1F separator", wantPK)
	}
	store.Close()

	// Reopen (read-only, a fresh C database handle) so the assertions exercise a
	// genuine marshal-out / marshal-in across the cgo boundary, not an in-memory
	// cache from the writing connection.
	ro, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenStoreReadOnly: %v", err)
	}
	defer ro.Close()

	// (a) The raw stored pk is byte-identical — separator present, not truncated
	// to just the project prefix the way a NUL join would collapse to.
	gotPK := queryOneString(t, ro,
		`MATCH (e:Entity) WHERE e.id = $id RETURN e.pk`,
		map[string]any{"id": entity.ID})
	if gotPK != wantPK {
		t.Fatalf("stored pk mismatch:\n  got  %q (len %d)\n  want %q (len %d)",
			gotPK, len(gotPK), wantPK, len(wantPK))
	}
	if !strings.Contains(gotPK, pkSeparator) {
		t.Errorf("stored pk %q lost the 0x1F separator across the cgo round trip", gotPK)
	}
	if gotPK == projectID {
		t.Errorf("stored pk collapsed to the project prefix %q (NUL-style truncation)", projectID)
	}

	// (b) A MATCH by the exact pk string — separator byte and all — resolves the
	// row, confirming the key is usable as the primary key it stands in for.
	gotID := queryOneString(t, ro,
		`MATCH (e:Entity) WHERE e.pk = $pk RETURN e.id`,
		map[string]any{"pk": wantPK})
	if gotID != entity.ID {
		t.Fatalf("MATCH by exact pk resolved id %q, want %q", gotID, entity.ID)
	}
}

// queryOneString runs a parameterised query expected to yield exactly one row
// whose first column is a string, and returns that string.
func queryOneString(t *testing.T, s *Store, stmt string, params map[string]any) string {
	t.Helper()
	result, err := s.QueryParams(stmt, params)
	if err != nil {
		t.Fatalf("query %q: %v", stmt, err)
	}
	defer result.Close()

	if !result.HasNext() {
		t.Fatalf("query %q returned no rows", stmt)
	}
	tuple, err := result.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	defer tuple.Close()

	row, err := tuple.GetAsSlice()
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	if len(row) == 0 {
		t.Fatalf("query %q returned an empty row", stmt)
	}
	return stringOrEmpty(row[0])
}
