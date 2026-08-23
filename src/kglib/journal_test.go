package kglib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newJournaledStore opens a store at dbPath with hand-write journaling on.
func newJournaledStore(t *testing.T, dbPath string) *Store {
	t.Helper()
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore(%s): %v", dbPath, err)
	}
	store.EnableJournal()
	return store
}

// readJournal returns the decoded records written beside dbPath.
func readJournal(t *testing.T, dbPath string) []JournalRecord {
	t.Helper()
	data, err := os.ReadFile(JournalPath(dbPath))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	var recs []JournalRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec JournalRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode journal line %q: %v", line, err)
		}
		recs = append(recs, rec)
	}
	return recs
}

func TestJournalDisabledByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := OpenStore(dbPath, testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	if store.JournalEnabled() {
		t.Fatal("journaling should be off unless a hand-write path enables it")
	}
	if _, err := store.CreateEntity("main.go", "file", "p"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	// The indexer writes through these same methods; a journal file appearing
	// here would mean derived data is being recorded for replay.
	if _, err := os.Stat(JournalPath(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("expected no journal file, stat returned %v", err)
	}
}

func TestJournalRecordsHandWrites(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store := newJournaledStore(t, dbPath)
	defer store.Close()

	from, err := store.CreateEntity("auth", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	to, err := store.CreateEntity("session", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(from.ID, "tokens expire after an hour", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.CreateRelation(from.ID, to.ID, "RELATES_TO", "p"); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}

	recs := readJournal(t, dbPath)
	if len(recs) != 4 {
		t.Fatalf("expected 4 records, got %d: %+v", len(recs), recs)
	}

	for i, rec := range recs {
		if rec.Version != JournalVersion {
			t.Errorf("record %d: version = %d, want %d", i, rec.Version, JournalVersion)
		}
		if rec.Timestamp.IsZero() {
			t.Errorf("record %d: timestamp not stamped", i)
		}
		if rec.ProjectID != "p" {
			t.Errorf("record %d: project = %q, want %q", i, rec.ProjectID, "p")
		}
	}

	if recs[0].Op != OpCreateEntity || recs[0].Entity.Name != "auth" || recs[0].Entity.Type != "topic" {
		t.Errorf("record 0 = %+v, want create of auth/topic", recs[0])
	}
	if recs[2].Op != OpCreateObservation || recs[2].Content != "tokens expire after an hour" {
		t.Errorf("record 2 = %+v, want observation create", recs[2])
	}
	if recs[2].Entity.Name != "auth" {
		t.Errorf("observation record names %q, want its parent entity auth", recs[2].Entity.Name)
	}
	if recs[3].Op != OpCreateRelation || recs[3].From.Name != "auth" || recs[3].To.Name != "session" || recs[3].RelType != "RELATES_TO" {
		t.Errorf("record 3 = %+v, want auth -RELATES_TO-> session", recs[3])
	}
}

// The journal must never carry a UUID as an entity's identity: IDs are
// regenerated on re-index, so a replay that trusted them would resolve nothing.
func TestJournalCarriesNoEntityIDs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store := newJournaledStore(t, dbPath)
	defer store.Close()

	entity, err := store.CreateEntity("auth", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := store.CreateObservation(entity.ID, "note", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}

	raw, err := os.ReadFile(JournalPath(dbPath))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if strings.Contains(string(raw), entity.ID) {
		t.Errorf("journal leaks entity UUID %s, which will not resolve after a rebuild:\n%s", entity.ID, raw)
	}
}

func TestJournalRecordsDeletes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store := newJournaledStore(t, dbPath)
	defer store.Close()

	entity, err := store.CreateEntity("secret", "note", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	obs, err := store.CreateObservation(entity.ID, "forget me", "p")
	if err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := store.DeleteObservation(obs.ID, entity.ID, "p"); err != nil {
		t.Fatalf("DeleteObservation: %v", err)
	}
	if err := store.DeleteEntity(entity.ID, "p"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	recs := readJournal(t, dbPath)
	if len(recs) != 4 {
		t.Fatalf("expected 4 records, got %d", len(recs))
	}
	// The delete of an observation has to carry its content: content is the only
	// handle replay has on an observation, which has no name.
	if recs[2].Op != OpDeleteObservation || recs[2].Content != "forget me" {
		t.Errorf("record 2 = %+v, want observation delete carrying its content", recs[2])
	}
	if recs[3].Op != OpDeleteEntity || recs[3].Entity.Name != "secret" {
		t.Errorf("record 3 = %+v, want entity delete naming secret", recs[3])
	}
}

// The point of the whole exercise: hand-written knowledge lands in a database
// created from scratch, with no access to the database that recorded it.
func TestReplayIntoFreshDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "old.db")

	old := newJournaledStore(t, oldPath)
	auth, err := old.CreateEntity("auth", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	session, err := old.CreateEntity("session", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := old.CreateObservation(auth.ID, "tokens expire after an hour", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := old.CreateRelation(auth.ID, session.ID, "RELATES_TO", "p"); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	old.Close()

	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()

	stats, err := fresh.ReplayJournal(JournalPath(oldPath))
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("replay reported %d failures: %v", stats.Failed, stats.Errors)
	}
	if stats.Applied != 4 {
		t.Errorf("applied = %d, want 4 (stats: %+v)", stats.Applied, stats)
	}

	replayedAuth, err := fresh.GetEntityByNameAndType("auth", "topic", "p")
	if err != nil || replayedAuth == nil {
		t.Fatalf("auth entity not replayed: %v", err)
	}
	if replayedAuth.ID == auth.ID {
		t.Error("replayed entity reused the old UUID; it should have been assigned a fresh one")
	}

	observations, err := fresh.GetObservations(replayedAuth.ID, "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 || observations[0].Content != "tokens expire after an hour" {
		t.Errorf("observations = %+v, want the one hand-written note", observations)
	}

	relations, err := fresh.GetRelations(replayedAuth.ID, "p")
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].Type != "RELATES_TO" {
		t.Errorf("relations = %+v, want one RELATES_TO edge", relations)
	}
}

// A rebuild can be interrupted and retried, so replaying twice must not double
// the graph.
func TestReplayIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "old.db")

	old := newJournaledStore(t, oldPath)
	auth, err := old.CreateEntity("auth", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	session, err := old.CreateEntity("session", "topic", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := old.CreateObservation(auth.ID, "note", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := old.CreateRelation(auth.ID, session.ID, "RELATES_TO", "p"); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	old.Close()

	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()

	if _, err := fresh.ReplayJournal(JournalPath(oldPath)); err != nil {
		t.Fatalf("first replay: %v", err)
	}
	stats, err := fresh.ReplayJournal(JournalPath(oldPath))
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if stats.Applied != 0 {
		t.Errorf("second replay applied %d records, want 0 (stats: %+v)", stats.Applied, stats)
	}
	if stats.Skipped != 4 {
		t.Errorf("second replay skipped %d records, want 4", stats.Skipped)
	}

	entities, err := fresh.ListEntities("p", "")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Errorf("entity count = %d after two replays, want 2", len(entities))
	}
	replayedAuth, err := fresh.GetEntityByNameAndType("auth", "topic", "p")
	if err != nil || replayedAuth == nil {
		t.Fatalf("auth missing: %v", err)
	}
	observations, err := fresh.GetObservations(replayedAuth.ID, "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 {
		t.Errorf("observation count = %d after two replays, want 1", len(observations))
	}
	relations, err := fresh.GetRelations(replayedAuth.ID, "p")
	if err != nil {
		t.Fatalf("GetRelations: %v", err)
	}
	if len(relations) != 1 {
		t.Errorf("relation count = %d after two replays, want 1", len(relations))
	}
}

// Replay must not append to the journal it is reading from, or every rebuild
// would double the file.
func TestReplayDoesNotRejournal(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "old.db")

	old := newJournaledStore(t, oldPath)
	if _, err := old.CreateEntity("auth", "topic", "p"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	old.Close()

	newPath := filepath.Join(tmpDir, "new.db")
	fresh := newJournaledStore(t, newPath)
	defer fresh.Close()

	if _, err := fresh.ReplayJournal(JournalPath(oldPath)); err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}

	if _, err := os.Stat(JournalPath(newPath)); !os.IsNotExist(err) {
		t.Errorf("replay wrote to the new store's journal, stat returned %v", err)
	}
	if got := len(readJournal(t, oldPath)); got != 1 {
		t.Errorf("source journal grew to %d records during replay, want 1", got)
	}

	// Journaling is restored once the replay is done.
	if !fresh.JournalEnabled() {
		t.Error("journaling stayed off after replay")
	}
	if _, err := fresh.CreateEntity("later", "topic", "p"); err != nil {
		t.Fatalf("CreateEntity after replay: %v", err)
	}
	if got := len(readJournal(t, newPath)); got != 1 {
		t.Errorf("post-replay write produced %d records, want 1", got)
	}
}

// A delete recorded in the journal has to survive the round trip, or
// `kg personal forget` would be undone by the next storage-format upgrade.
func TestReplayHonoursDeletes(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := filepath.Join(tmpDir, "old.db")

	old := newJournaledStore(t, oldPath)
	entity, err := old.CreateEntity("secret", "note", "p")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if _, err := old.CreateObservation(entity.ID, "forget me", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	if err := old.DeleteEntity(entity.ID, "p"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	old.Close()

	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()

	stats, err := fresh.ReplayJournal(JournalPath(oldPath))
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("replay reported failures: %v", stats.Errors)
	}

	gone, err := fresh.GetEntityByNameAndType("secret", "note", "p")
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if gone != nil {
		t.Error("a forgotten entity came back after replay")
	}
}

// An observation whose parent the re-index did not recreate still has to land:
// there is no other copy of hand-written knowledge.
func TestReplayCreatesMissingParent(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "orphan.db.journal.jsonl")
	line := `{"v":1,"ts":"2026-08-22T10:00:00Z","op":"observation.create","project":"p",` +
		`"entity":{"name":"vanished","type":"topic"},"content":"still worth keeping"}` + "\n"
	if err := os.WriteFile(journalPath, []byte(line), 0600); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()

	stats, err := fresh.ReplayJournal(journalPath)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("replay reported failures: %v", stats.Errors)
	}
	if stats.Implicit != 1 {
		t.Errorf("implicit entity count = %d, want 1", stats.Implicit)
	}

	parent, err := fresh.GetEntityByNameAndType("vanished", "topic", "p")
	if err != nil || parent == nil {
		t.Fatalf("missing parent was not created: %v", err)
	}
	observations, err := fresh.GetObservations(parent.ID, "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 || observations[0].Content != "still worth keeping" {
		t.Errorf("observations = %+v, want the orphaned note", observations)
	}
}

// One corrupt line costs that line, not the rest of the file.
func TestReplaySurvivesCorruptLines(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "mixed.db.journal.jsonl")
	content := strings.Join([]string{
		`{"v":1,"op":"entity.create","project":"p","entity":{"name":"first","type":"topic"}}`,
		`{"v":1,"op":"entity.crea`, // truncated by a crash mid-append
		`{"v":1,"op":"nonsense","project":"p"}`,
		`{"v":99,"op":"entity.create","project":"p","entity":{"name":"future","type":"topic"}}`,
		`{"v":1,"op":"entity.create","project":"p","entity":{"name":"last","type":"topic"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(journalPath, []byte(content), 0600); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()

	stats, err := fresh.ReplayJournal(journalPath)
	if err != nil {
		t.Fatalf("ReplayJournal returned a hard error on a partly corrupt journal: %v", err)
	}
	if stats.Applied != 2 {
		t.Errorf("applied = %d, want 2 (the two readable creates)", stats.Applied)
	}
	if stats.Failed != 3 {
		t.Errorf("failed = %d, want 3 (truncated, unknown op, future version)", stats.Failed)
	}

	// The record after the corruption is what matters: replay kept going.
	last, err := fresh.GetEntityByNameAndType("last", "topic", "p")
	if err != nil || last == nil {
		t.Fatalf("records after the corrupt line were dropped: %v", err)
	}
}

func TestReplayMissingJournalIsNotAnError(t *testing.T) {
	tmpDir := t.TempDir()
	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()

	stats, err := fresh.ReplayJournal(filepath.Join(tmpDir, "absent.db.journal.jsonl"))
	if err != nil {
		t.Fatalf("a database that was only ever indexed has nothing to replay: %v", err)
	}
	if stats.Records != 0 {
		t.Errorf("records = %d, want 0", stats.Records)
	}
}

// The collision the audit found: indexed code symbols routinely share
// (name, type) across files. kg's own graph holds 1460 entities covering only
// 1370 distinct pairs, with `init` appearing 21 times.
//
// Resolving a hand-written note on that tuple alone attaches it to whichever
// row the database yields first — silently, and permanently, because the
// duplicate check then reports it as already present on every later replay.
func TestReplayResolvesCollidingNamesByStableID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store := newJournaledStore(t, dbPath)

	// Two same-named symbols, the way an indexer writes them: IDs derived from
	// source position, so they are stable across a rebuild.
	mustCreateWithID(t, store, "function:cmd/main.go:Run", "Run", "function", "p")
	mustCreateWithID(t, store, "function:auth/handler.go:Run", "Run", "function", "p")

	target, err := store.GetEntity("function:auth/handler.go:Run", "p")
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if _, err := store.CreateObservation(target.ID, "rate-limits at 100rps", "p"); err != nil {
		t.Fatalf("CreateObservation: %v", err)
	}
	store.Close()

	// The record must name the specific symbol, not just "Run".
	recs := readJournal(t, dbPath)
	var obs *JournalRecord
	for i := range recs {
		if recs[i].Op == OpCreateObservation {
			obs = &recs[i]
		}
	}
	if obs == nil {
		t.Fatal("no observation record in the journal")
	}
	if obs.Entity.ID != "function:auth/handler.go:Run" {
		t.Fatalf("record identifies the parent as %q; without the source-derived ID "+
			"replay cannot tell the two Run functions apart", obs.Entity.ID)
	}

	// Rebuild: same two symbols, same deterministic IDs, no observations.
	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()
	mustCreateWithID(t, fresh, "function:cmd/main.go:Run", "Run", "function", "p")
	mustCreateWithID(t, fresh, "function:auth/handler.go:Run", "Run", "function", "p")

	stats, err := fresh.ReplayJournal(JournalPath(dbPath))
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if stats.Failed != 0 {
		t.Fatalf("replay failures: %v", stats.Errors)
	}
	if stats.Ambiguous != 0 {
		t.Errorf("replay resolved %d record(s) ambiguously: %v", stats.Ambiguous, stats.Ambiguities)
	}

	// The note landed on the right Run.
	right, err := fresh.GetObservations("function:auth/handler.go:Run", "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(right) != 1 || right[0].Content != "rate-limits at 100rps" {
		t.Errorf("auth/handler.go:Run has %+v, want the hand-written note", right)
	}

	wrong, err := fresh.GetObservations("function:cmd/main.go:Run", "p")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(wrong) != 0 {
		t.Errorf("the note was attached to the wrong Run: %+v", wrong)
	}
}

// When the ID cannot resolve — a journal written before the hint existed, or a
// symbol whose file moved — replay still falls back to (name, type), but says
// when that choice was arbitrary rather than making it silently.
func TestReplayReportsAmbiguousNameFallback(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "old.db.journal.jsonl")
	// No "id" field: the shape written before the hint existed.
	line := `{"v":1,"op":"observation.create","project":"p",` +
		`"entity":{"name":"Run","type":"function"},"content":"a note"}` + "\n"
	if err := os.WriteFile(journalPath, []byte(line), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fresh, err := OpenStore(filepath.Join(tmpDir, "new.db"), testSchemaConfig())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer fresh.Close()
	mustCreateWithID(t, fresh, "function:cmd/main.go:Run", "Run", "function", "p")
	mustCreateWithID(t, fresh, "function:auth/handler.go:Run", "Run", "function", "p")

	stats, err := fresh.ReplayJournal(journalPath)
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if stats.Applied != 1 {
		t.Errorf("applied = %d, want 1 — the note should still land somewhere", stats.Applied)
	}
	if stats.Ambiguous != 1 {
		t.Errorf("ambiguous = %d, want 1 — an arbitrary choice was made and not reported", stats.Ambiguous)
	}
	if len(stats.Ambiguities) == 0 {
		t.Error("no detail recorded for the ambiguous resolution")
	}
}

// A hand-created entity has a generated UUID, which does not survive a rebuild.
// Recording it would put an unresolvable identifier in the journal, which is
// what the format was designed to avoid.
func TestJournalOmitsUnstableIDs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store := newJournaledStore(t, dbPath)
	defer store.Close()

	if _, err := store.CreateEntity("retry-policy", "decision", "p"); err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	recs := readJournal(t, dbPath)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Entity.ID != "" {
		t.Errorf("journal recorded a generated UUID %q, which cannot resolve after a rebuild",
			recs[0].Entity.ID)
	}
}

// mustCreateWithID writes an entity with an indexer-style deterministic ID.
func mustCreateWithID(t *testing.T, store *Store, id, name, entityType, projectID string) {
	t.Helper()
	result, err := store.QueryParams(`
		CREATE (e:Entity {id: $id, name: $name, type: $type, project_id: $project_id,
		                  created_at: $ts, updated_at: $ts})
	`, map[string]any{
		"id": id, "name": name, "type": entityType, "project_id": projectID,
		"ts": time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	result.Close()
}
