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
