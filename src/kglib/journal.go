package kglib

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// The hand-write journal.
//
// Kuzu pins its storage format to the library version, so a future go-kuzu bump
// can leave an existing .db file unopenable. Indexed graphs survive that by
// being rebuilt from source, but hand-written knowledge — `kg add`, `kg link`,
// the MCP write tools, everything in the personal store — has no source to
// rebuild from. The journal is that source: a plain JSONL file beside the
// database, engine-format-independent, replayed into a fresh database after a
// format bump so no old binary is ever needed.
//
// Two constraints shape the record format:
//
//   - Entities are identified by (name, type, project), never by UUID. IDs are
//     regenerated whenever a graph is re-indexed, so a UUID written today names
//     nothing after a rebuild. Replay re-resolves names to whatever IDs the new
//     database assigned.
//   - Deletes are journaled too. `kg personal forget` removes an entry the user
//     asked to be gone; a replay that only knew about creates would resurrect it.
//
// Only hand-writes are journaled. Indexers share the same Store methods but
// write derived data by the hundred thousand, which is rebuilt rather than
// replayed — hence the per-Store opt-in (EnableJournal) rather than an
// unconditional hook.

// JournalVersion is the schema version stamped on every record. Replay refuses
// versions it does not understand rather than guessing at unknown fields.
const JournalVersion = 1

// Journal operation names.
const (
	OpCreateEntity      = "entity.create"
	OpDeleteEntity      = "entity.delete"
	OpCreateObservation = "observation.create"
	OpDeleteObservation = "observation.delete"
	OpCreateRelation    = "relation.create"
	OpDeleteRelation    = "relation.delete"
)

// EntityRef names an entity the way the journal identifies it: by the tuple
// that survives a re-index, not by the UUID that does not.
type EntityRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// JournalRecord is one line of <db>.journal.jsonl.
type JournalRecord struct {
	Version int `json:"v"`
	// omitzero, not omitempty: the indexer's bulk-load path leaves created_at
	// unset on some rows, and an export of those should omit the field rather
	// than claim the year 1.
	Timestamp time.Time  `json:"ts,omitzero"`
	Op        string     `json:"op"`
	ProjectID string     `json:"project"`
	Entity    *EntityRef `json:"entity,omitempty"`
	From      *EntityRef `json:"from,omitempty"`
	To        *EntityRef `json:"to,omitempty"`
	RelType   string     `json:"rel,omitempty"`
	Content   string     `json:"content,omitempty"`
}

// Journal appends hand-write records to a JSONL file beside a database.
//
// The file is opened per append rather than held open: hand-writes are rare and
// small, and a short-lived O_APPEND handle keeps concurrent kg processes from
// interleaving partial lines. Each append is one write syscall followed by an
// fsync, so a record that returns without error is on disk.
type Journal struct {
	path string
	mu   sync.Mutex
}

// JournalPath returns the journal file that belongs to a database path.
func JournalPath(dbPath string) string {
	return dbPath + ".journal.jsonl"
}

// NewJournal returns a journal writing beside dbPath. The file is created on
// first append, so opening a journal for a read-only database costs nothing.
func NewJournal(dbPath string) *Journal {
	return &Journal{path: JournalPath(dbPath)}
}

// Path returns the journal file's location.
func (j *Journal) Path() string { return j.path }

// Append writes one record. The caller is expected to have already committed
// the corresponding database write: a journal entry for a write that failed
// would resurrect a phantom entity at replay, which is worse than the narrow
// window in which a crash loses a just-written record.
func (j *Journal) Append(rec JournalRecord) error {
	rec.Version = JournalVersion
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	line = append(line, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()

	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open journal %s: %w", j.path, err)
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("append to journal %s: %w", j.path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync journal %s: %w", j.path, err)
	}
	return nil
}

// EnableJournal turns on hand-write journaling for this Store. Call it on
// stores opened by a hand-write path (the add/link commands, the MCP write
// tools, the personal store); leave it off for indexers, whose output is
// rebuilt from source rather than replayed.
//
// Enabling on a read-only store is a no-op in practice — nothing writes — but
// is harmless.
func (s *Store) EnableJournal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journal == nil {
		s.journal = NewJournal(s.path)
	}
}

// JournalEnabled reports whether hand-write journaling is on.
func (s *Store) JournalEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.journal != nil
}

// journalWriter returns the active journal, or nil when journaling is off.
// Separate from the exported accessor so the write paths take mu only once.
func (s *Store) journalWriter() *Journal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.journal
}

// appendJournal records a hand-write, if journaling is enabled for this store.
//
// A failure here is returned to the caller even though the database write has
// already succeeded: the whole point of the journal is that the write survives
// a storage-format bump, and silently dropping the record would leave the user
// believing knowledge is safe when it is not. Callers wrap the error to say the
// write landed.
func (s *Store) appendJournal(rec JournalRecord) error {
	j := s.journalWriter()
	if j == nil {
		return nil
	}
	return j.Append(rec)
}

// errJournalNote explains the split outcome when the database write committed
// but the journal append did not.
func errJournalNote(err error) error {
	return fmt.Errorf("write succeeded but was not journaled, so it will not "+
		"survive a storage-format upgrade: %w", err)
}
