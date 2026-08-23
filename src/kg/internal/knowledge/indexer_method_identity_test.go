package knowledge

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

// twoMethodsOneName is a file whose two methods share a name and call different
// helpers. Everything a caller could use to tell them apart is in the receiver.
const twoMethodsOneName = `package p

type Store struct{}

type Cache struct{}

func (s *Store) get() { storeHelper() }

func (c *Cache) get() { cacheHelper() }

func storeHelper() {}

func cacheHelper() {}
`

// callTargetsByCaller returns, for every function entity with the given name,
// the names of the functions it calls. Keyed by entity ID so two functions that
// share a name stay apart — which is the whole question here.
func callTargetsByCaller(t *testing.T, store *Store, callerName string) map[string][]string {
	t.Helper()
	result, err := store.QueryParams(`
		MATCH (a:Entity {name: $name, type: 'function'})
		OPTIONAL MATCH (a)-[:CALLS]->(b:Entity)
		RETURN a.id, b.name
	`, map[string]any{"name": callerName})
	if err != nil {
		t.Fatalf("query call graph: %v", err)
	}
	defer result.Close()

	targets := make(map[string][]string)
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			t.Fatalf("result.Next: %v", err)
		}
		idVal, _ := row.GetValue(0)
		calleeVal, _ := row.GetValue(1)
		id, _ := idVal.(string)
		if id == "" {
			continue
		}
		if _, ok := targets[id]; !ok {
			targets[id] = nil
		}
		if callee, ok := calleeVal.(string); ok && callee != "" {
			targets[id] = append(targets[id], callee)
		}
	}
	for id := range targets {
		sort.Strings(targets[id])
	}
	return targets
}

// Two methods of the same name in one file are two functions, and each makes
// only the calls that are written in its own body. Attributing one method's
// calls to another invents a dependency that does not exist — worse than
// missing it, because a reader has no way to tell it apart from a real one.
func TestSameNamedMethodsKeepTheirOwnCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.go"), []byte(twoMethodsOneName), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	targets := callTargetsByCaller(t, store, "get")

	if len(targets) != 2 {
		t.Fatalf("file declares two methods named get, graph holds %d: %v", len(targets), targets)
	}

	// Each get calls exactly the helper written in its own body, and the two
	// sets are different. Asserted without naming which entity is which: the
	// property is the partition, not the ID scheme that produces it.
	var got []string
	for id, callees := range targets {
		if len(callees) != 1 {
			t.Fatalf("%s calls %v; each get calls exactly one helper in this file, the one written in its own body", id, callees)
		}
		got = append(got, callees[0])
	}
	sort.Strings(got)

	want := []string{"cacheHelper", "storeHelper"}
	if !slices.Equal(got, want) {
		t.Errorf("the two get methods call %v, want %v — one helper each, from its own body", got, want)
	}

	// The display name stays bare, so someone searching for "get" still finds
	// both. Qualification belongs in the identity, not in what a human reads.
	if !entityExistsByName(t, store, "get", EntityTypeFunction) {
		t.Error(`no function entity is named "get"; a search for the method name would find nothing`)
	}
}

// Entity IDs are the journal's stability hint: kglib records an indexer ID for
// a hand-written entity on the promise that a re-index of the same declaration
// produces the same ID (see stableIDHint in kglib/journal.go). Editing an
// unrelated part of the file must therefore not renumber anything — which rules
// out disambiguating same-named methods by line or offset.
func TestFunctionIDsSurviveUnrelatedEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	srcFile := filepath.Join(srcDir, "a.go")
	if err := os.WriteFile(srcFile, []byte(twoMethodsOneName), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))
	before := functionIDs(t, store)
	if len(before) == 0 {
		t.Fatal("no function entities indexed; the fixture cannot express the failure")
	}

	// An edit above the declarations: same code, different positions.
	edited := "package p\n\n// A comment added at the top of the file.\n// It shifts every declaration below it down two lines.\n" + twoMethodsOneName[len("package p\n"):]
	if err := os.WriteFile(srcFile, []byte(edited), 0o644); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}

	idx, err := NewIndexer(store, "test-project", srcDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("re-Index: %v", err)
	}
	after := functionIDs(t, store)

	if len(before) != len(after) {
		t.Fatalf("function count changed across an edit that added only comments: %d then %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("function IDs changed after a comment-only edit:\n before: %v\n after:  %v\nthe journal records these IDs as stable hints, so a hand-written note attached to one of them would no longer resolve",
				before, after)
		}
	}
}

// functionIDs returns every function entity ID in the store, sorted.
func functionIDs(t *testing.T, store *Store) []string {
	t.Helper()
	result, err := store.Query(`MATCH (e:Entity {type: 'function'}) RETURN e.id`)
	if err != nil {
		t.Fatalf("query function ids: %v", err)
	}
	defer result.Close()

	var ids []string
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			t.Fatalf("result.Next: %v", err)
		}
		v, _ := row.GetValue(0)
		if id, ok := v.(string); ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
