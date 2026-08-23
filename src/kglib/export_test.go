package kglib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportToFile writes a store's project to a JSONL file, the way `kg export`
// does, and returns the path and record count.
func exportToFile(t *testing.T, store *Store, projectID, path string) int {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	journal := &Journal{path: path}
	count := 0
	err = store.ExportProject(projectID, func(rec JournalRecord) error {
		count++
		return journal.Append(rec)
	})
	if err != nil {
		t.Fatalf("ExportProject: %v", err)
	}
	return count
}

func seedGraph(t *testing.T, store *Store, projectID string) {
	t.Helper()
	auth, err := store.CreateEntity("auth", "topic", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	session, err := store.CreateEntity("session", "topic", projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(auth.ID, "tokens expire after an hour", projectID); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if _, err := store.CreateObservation(auth.ID, "refresh is single-use", projectID); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.CreateRelation(auth.ID, session.ID, "RELATES_TO", projectID); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	seedGraph(t, source, "p")

	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	count := exportToFile(t, source, "p", dumpPath)
	source.Close()

	// 2 entities + 2 observations + 1 relation.
	if count != 5 {
		t.Fatalf("exported %d records, want 5", count)
	}

	target, err := OpenStore(filepath.Join(tmpDir, "target.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()

	stats, err := target.ImportRecords(dumpPath, "")
	if err != nil {
		t.Fatalf("ImportRecords: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("import failures: %v", stats.Errors)
	}
	if stats.Applied != 5 {
		t.Errorf("applied = %d, want 5 (stats %+v)", stats.Applied, stats)
	}
	// Every entity is created by its own record, so none should be conjured
	// implicitly — that would mean the export emitted things out of order.
	if stats.Implicit != 0 {
		t.Errorf("import created %d entities implicitly; export should list entities before they are referenced", stats.Implicit)
	}

	auth, err := target.GetEntityByNameAndType("auth", "topic", "p")
	if err != nil || auth == nil {
		t.Fatalf("auth not imported: %v", err)
	}
	observations, err := target.GetObservations(auth.ID, "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 2 {
		t.Errorf("observation count = %d, want 2", len(observations))
	}
	relations, err := target.GetRelations(auth.ID, "p")
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].Type != "RELATES_TO" {
		t.Errorf("relations = %+v, want one RELATES_TO", relations)
	}
}

// Re-importing the same file must not duplicate anything.
func TestImportIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	seedGraph(t, source, "p")
	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "p", dumpPath)
	source.Close()

	target, err := OpenStore(filepath.Join(tmpDir, "target.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()

	if _, err := target.ImportRecords(dumpPath, ""); err != nil {
		t.Fatalf("first import: %v", err)
	}
	stats, err := target.ImportRecords(dumpPath, "")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if stats.Applied != 0 {
		t.Errorf("second import applied %d records, want 0", stats.Applied)
	}

	entities, err := target.ListEntities("p", "")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Errorf("entity count = %d after two imports, want 2", len(entities))
	}
	auth, err := target.GetEntityByNameAndType("auth", "topic", "p")
	if err != nil || auth == nil {
		t.Fatalf("auth missing: %v", err)
	}
	relations, err := target.GetRelations(auth.ID, "p")
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Errorf("relation count = %d after two imports, want 1 — the edge was duplicated", len(relations))
	}
}

// Imported knowledge came from outside this database and has no journal entry
// here, so it must be journaled or it will not survive the next rebuild.
func TestImportIsJournaled(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	seedGraph(t, source, "p")
	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "p", dumpPath)
	source.Close()

	targetPath := filepath.Join(tmpDir, "target.db")
	target, err := OpenStore(targetPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()
	target.EnableJournal()

	if _, err := target.ImportRecords(dumpPath, ""); err != nil {
		t.Fatalf("ImportRecords: %v", err)
	}

	data, err := os.ReadFile(JournalPath(targetPath))
	if err != nil {
		t.Fatalf("import left no journal, so a later rebuild would lose it: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("journal is empty after import")
	}
}

// Replay, by contrast, is re-applying records the journal already holds, so it
// must not write them a second time.
func TestReplayStillDoesNotJournal(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	seedGraph(t, source, "p")
	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "p", dumpPath)
	source.Close()

	targetPath := filepath.Join(tmpDir, "target.db")
	target, err := OpenStore(targetPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()
	target.EnableJournal()

	if _, err := target.ReplayJournal(dumpPath); err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if _, err := os.Stat(JournalPath(targetPath)); !os.IsNotExist(err) {
		t.Errorf("replay wrote to the journal; stat returned %v", err)
	}
}

// Two exports of the same graph must be byte-identical, or a backup cannot be
// diffed or version-controlled usefully.
func TestExportIsDeterministic(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	seedGraph(t, store, "p")

	first := filepath.Join(tmpDir, "first.jsonl")
	second := filepath.Join(tmpDir, "second.jsonl")
	exportToFile(t, store, "p", first)
	exportToFile(t, store, "p", second)

	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Timestamps differ by design; compare the records without them.
	if stripTimestamps(string(a)) != stripTimestamps(string(b)) {
		t.Errorf("two exports of the same graph differ:\n%s\n---\n%s", a, b)
	}
}

// An export scoped to one project must not leak another project's rows.
func TestExportIsScopedToProject(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	seedGraph(t, store, "mine")
	if _, err := store.CreateEntity("someone-elses-secret", "note", "theirs"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, store, "mine", dumpPath)

	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "someone-elses-secret") {
		t.Errorf("export leaked another project's entity:\n%s", data)
	}
}

func stripTimestamps(s string) string {
	var out []byte
	skip := false
	for i := 0; i < len(s); i++ {
		if !skip && i+6 <= len(s) && s[i:i+6] == `"ts":"` {
			skip = true
			i += 5
			continue
		}
		if skip {
			if s[i] == '"' {
				skip = false
			}
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

// A dump taken from one project has to be searchable after being imported into
// another. Without re-projection the rows land in the database but no query
// against the target project ever returns them.
func TestImportReprojectsOntoTargetProject(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	seedGraph(t, source, "origin-project")
	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "origin-project", dumpPath)
	source.Close()

	target, err := OpenStore(filepath.Join(tmpDir, "target.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()

	if _, err := target.ImportRecords(dumpPath, "receiving-project"); err != nil {
		t.Fatalf("ImportRecords: %v", err)
	}

	found, err := target.GetEntityByNameAndType("auth", "topic", "receiving-project")
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if found == nil {
		t.Fatal("imported entity is not visible under the target project ID")
	}

	// And nothing was left behind under the original project.
	stranded, err := target.ListEntities("origin-project", "")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(stranded) != 0 {
		t.Errorf("%d row(s) landed under the source project and are unreachable", len(stranded))
	}
}

// Restoring a backup into the graph it came from must not depend on the flag.
func TestImportPreservesProjectWhenNotOverridden(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	seedGraph(t, source, "p")
	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "p", dumpPath)
	source.Close()

	target, err := OpenStore(filepath.Join(tmpDir, "target.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()

	if _, err := target.ImportRecords(dumpPath, ""); err != nil {
		t.Fatalf("ImportRecords: %v", err)
	}
	found, err := target.GetEntityByNameAndType("auth", "topic", "p")
	if err != nil || found == nil {
		t.Fatalf("entity not imported under its recorded project: %v", err)
	}
}

// Importing a database's own journal would append every applied record back
// into the file being read; a create/delete pair then re-applies forever.
// `kg export --journal` produces a deliberately journal-shaped file, so this is
// a plausible thing to type.
func TestImportRefusesTheDatabasesOwnJournal(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "knowledge.db")

	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	store.EnableJournal()

	entity, err := store.CreateEntity("retry-policy", "decision", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := store.DeleteEntity(entity.ID, "p"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	before, err := os.ReadFile(JournalPath(dbPath))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}

	_, err = store.ImportRecords(JournalPath(dbPath), "p")
	if err == nil {
		t.Fatal("importing the database's own journal was allowed; that loop does not terminate")
	}
	if !strings.Contains(err.Error(), "journal") {
		t.Errorf("error does not explain the problem: %v", err)
	}

	after, err := os.ReadFile(JournalPath(dbPath))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("journal grew from %d to %d bytes despite the refusal", len(before), len(after))
	}
}

// A journal belonging to a *different* database is a legitimate import.
func TestImportAcceptsAnotherDatabasesJournal(t *testing.T) {
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.db")
	source, err := OpenStore(sourcePath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	source.EnableJournal()
	if _, err := source.CreateEntity("retry-policy", "decision", "p"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	source.Close()

	targetPath := filepath.Join(tmpDir, "target.db")
	target, err := OpenStore(targetPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()
	target.EnableJournal()

	stats, err := target.ImportRecords(JournalPath(sourcePath), "p")
	if err != nil {
		t.Fatalf("importing another database's journal was refused: %v", err)
	}
	if stats.Applied != 1 {
		t.Errorf("applied = %d, want 1", stats.Applied)
	}
}

// An export must carry the same stable ID hint a journal record does, or a
// restored backup resolves on (name, type) — which is not unique. kg's own
// graph has 55 colliding pairs, so this is the normal case, not an edge case.
func TestExportCarriesStableIDsSoRestoreDoesNotMisattach(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Two same-named symbols with indexer-style ids, and a note on one of them.
	mustCreateWithID(t, source, "function:cmd/main.go:Run", "Run", "function", "p")
	mustCreateWithID(t, source, "function:auth/handler.go:Run", "Run", "function", "p")
	if _, err := source.CreateObservation("function:auth/handler.go:Run", "rate-limits at 100rps", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "p", dumpPath)
	source.Close()

	target, err := OpenStore(filepath.Join(tmpDir, "target.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()

	stats, err := target.ImportRecords(dumpPath, "p")
	if err != nil {
		t.Fatalf("ImportRecords: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("import failures: %v", stats.Errors)
	}
	if stats.Ambiguous != 0 {
		t.Errorf("import resolved %d record(s) by guessing between same-named entities: %v",
			stats.Ambiguous, stats.Ambiguities)
	}

	// The note is on the symbol it was written against.
	right, err := target.GetObservations("function:auth/handler.go:Run", "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(right) != 1 || right[0].Content != "rate-limits at 100rps" {
		t.Errorf("auth/handler.go:Run has %+v, want the restored note", right)
	}
	wrong, err := target.GetObservations("function:cmd/main.go:Run", "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(wrong) != 0 {
		t.Errorf("restore attached the note to the wrong Run: %+v", wrong)
	}
}

// A restored backup must rank the same way the original did, which means
// visibility has to survive the round trip.
func TestExportRoundTripPreservesVisibility(t *testing.T) {
	tmpDir := t.TempDir()

	source, err := OpenStore(filepath.Join(tmpDir, "source.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	exported, err := source.CreateEntity("ExportedFunc", "function", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := setVisibility(source, exported.ID, VisibilityPublic); err != nil {
		t.Fatalf("setVisibility: %v", err)
	}
	unexported, err := source.CreateEntity("unexportedFunc", "function", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := setVisibility(source, unexported.ID, VisibilityPrivate); err != nil {
		t.Fatalf("setVisibility: %v", err)
	}
	// A hand-written entity has no source symbol and so no visibility.
	if _, err := source.CreateEntity("retry-policy", "decision", "p"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	dumpPath := filepath.Join(tmpDir, "dump.jsonl")
	exportToFile(t, source, "p", dumpPath)
	source.Close()

	target, err := OpenStore(filepath.Join(tmpDir, "target.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer target.Close()
	if _, err := target.ImportRecords(dumpPath, "p"); err != nil {
		t.Fatalf("ImportRecords: %v", err)
	}

	for _, tc := range []struct{ name, entityType, want string }{
		{"ExportedFunc", "function", VisibilityPublic},
		{"unexportedFunc", "function", VisibilityPrivate},
		{"retry-policy", "decision", ""},
	} {
		got, err := target.GetEntityByNameAndType(tc.name, tc.entityType, "p")
		if err != nil || got == nil {
			t.Fatalf("%s not restored: %v", tc.name, err)
		}
		if got.Visibility != tc.want {
			t.Errorf("%s visibility = %q after round trip, want %q", tc.name, got.Visibility, tc.want)
		}
	}

	// And the restored graph filters the way the original would.
	public, err := target.KeywordSearchFiltered("p", "Func", 10, SearchFilter{PublicOnly: true})
	if err != nil {
		t.Fatalf("KeywordSearchFiltered: %v", err)
	}
	for _, r := range public {
		if r.Entity.Name == "unexportedFunc" {
			t.Error("--public-only returned an unexported symbol after a restore: visibility was lost")
		}
	}
}
