package kglib

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Journal replay.
//
// Replay is what makes the journal worth writing: after a storage-format bump
// the new binary creates an empty database and walks the JSONL file, re-issuing
// every hand-write against whatever entity IDs the new database hands out.
//
// Two properties matter more than speed here (journals are small — hand-writes
// are rare by construction):
//
//   - Idempotent. A rebuild can be interrupted and retried, and for an indexed
//     graph the re-index may already have recreated some of the entities the
//     journal names. Replaying a record that is already reflected in the
//     database is a skip, not a duplicate and not an error.
//   - Lossless. A record whose entity is missing gets that entity created from
//     the reference rather than being dropped. Hand-written knowledge has no
//     other copy; inventing a bare entity to hang an observation on loses less
//     than discarding the observation.
//
// A single unreadable line does not abort the replay. Corruption in one record
// should cost that record, not every record after it, so failures are counted
// and reported at the end.

// ReplayStats summarises what a replay did.
type ReplayStats struct {
	Records  int // lines read
	Applied  int // records that changed the database
	Skipped  int // records already reflected in the database
	Failed   int // records that could not be applied
	Errors   []error
	Implicit int // entities created to host a record whose target was missing
	// Ambiguous counts records resolved by (name, type) where more than one
	// entity matched, so the target was chosen rather than identified.
	Ambiguous   int
	Ambiguities []string
}

// maxAmbiguityNotes bounds the retained detail; the count stays exact.
const maxAmbiguityNotes = 10

func (r *ReplayStats) noteAmbiguity(ref EntityRef, matches []*Entity) {
	if len(r.Ambiguities) >= maxAmbiguityNotes {
		return
	}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
	}
	r.Ambiguities = append(r.Ambiguities, fmt.Sprintf(
		"%q (%s) matched %d entities, attached to %s: %s",
		ref.Name, ref.Type, len(matches), matches[0].ID, strings.Join(ids, ", ")))
}

// maxReplayErrors bounds how many individual failures are retained. The count
// stays exact; only the detail is capped, so a wholly corrupt journal reports
// its scale without accumulating a million errors in memory.
const maxReplayErrors = 20

func (r *ReplayStats) fail(err error) {
	r.Failed++
	if len(r.Errors) < maxReplayErrors {
		r.Errors = append(r.Errors, err)
	}
}

// ReplayJournal applies this database's own journal after a rebuild.
//
// Journaling is suspended for the duration: the records being applied are
// already in a journal, and recording them again would double the file on every
// rebuild. The prior setting is restored before returning.
//
// A missing journal file is not an error — a database that has only ever been
// indexed has nothing hand-written to replay.
func (s *Store) ReplayJournal(path string) (ReplayStats, error) {
	return s.replay(path, false, "")
}

// ImportRecords applies records that came from somewhere else — an export, a
// backup, another machine's graph — and journals what it applies.
//
// This is the opposite choice from ReplayJournal, for the opposite situation.
// Replay is re-applying writes this database already has a journal for. Import
// is writing knowledge that has no journal entry here yet, and skipping the
// journal would mean the imported knowledge did not survive the next
// storage-format rebuild: exactly the failure the journal exists to prevent.
// projectID, when non-empty, replaces the project on every record.
//
// Records carry the project they were exported from, and a graph is only
// visible to queries for its own project ID. Importing another project's dump
// verbatim therefore writes rows that are present in the database but invisible
// to every search against it — data that is there and unreachable, which is a
// worse outcome than a clear failure. Callers restoring into a given graph pass
// that graph's project ID; passing "" preserves whatever the file says.
func (s *Store) ImportRecords(path, projectID string) (ReplayStats, error) {
	// Importing a database's own journal would append every applied record back
	// into the file the scanner is still reading, and a create/delete pair
	// re-applies forever — an unbounded loop that grows the journal until the
	// disk fills. `kg export --journal` writes a file that is deliberately
	// journal-shaped, so this is a plausible thing to type rather than a
	// contrived one.
	if s.journal != nil {
		same, err := sameFile(path, s.journal.Path())
		if err == nil && same {
			return ReplayStats{}, fmt.Errorf(
				"refusing to import %s into the database it is the journal for: "+
					"every applied record would be appended back to the file being read. "+
					"Replay it with a rebuild instead, or copy it elsewhere first", path)
		}
	}
	return s.replay(path, true, projectID)
}

// sameFile reports whether two paths refer to the same file on disk, following
// symlinks and surviving "./x" versus "x" — string comparison would not.
func sameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}

func (s *Store) replay(path string, keepJournaling bool, projectOverride string) (ReplayStats, error) {
	var stats ReplayStats

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return stats, nil
	}
	if err != nil {
		return stats, fmt.Errorf("open journal %s: %w", path, err)
	}
	defer f.Close()

	if !keepJournaling {
		s.mu.Lock()
		saved := s.journal
		s.journal = nil
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.journal = saved
			s.mu.Unlock()
		}()
	}

	scanner := bufio.NewScanner(f)
	// Observation content can be long — the design caps personal entries in the
	// kilobytes, but a CLI-written observation has no such limit. Give the
	// scanner room rather than failing a replay on a long line.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		stats.Records++

		var rec JournalRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			stats.fail(fmt.Errorf("line %d: parse: %w", line, err))
			continue
		}
		if projectOverride != "" {
			rec.ProjectID = projectOverride
		}
		if rec.Version > JournalVersion {
			stats.fail(fmt.Errorf("line %d: journal version %d is newer than this build understands (%d)",
				line, rec.Version, JournalVersion))
			continue
		}
		if err := s.applyRecord(rec, &stats); err != nil {
			stats.fail(fmt.Errorf("line %d (%s): %w", line, rec.Op, err))
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return stats, fmt.Errorf("read journal %s: %w", path, err)
	}

	return stats, nil
}

// applyRecord replays one record. It reports an error only for that record;
// the caller keeps going.
func (s *Store) applyRecord(rec JournalRecord, stats *ReplayStats) error {
	switch rec.Op {
	case OpCreateEntity:
		if rec.Entity == nil {
			return errors.New("record has no entity")
		}
		existing, err := s.resolveRef(*rec.Entity, rec.ProjectID, stats)
		if err != nil {
			return err
		}
		if existing != nil {
			stats.Skipped++
			return nil
		}
		if _, err := s.CreateEntity(rec.Entity.Name, rec.Entity.Type, rec.ProjectID); err != nil {
			return err
		}
		stats.Applied++
		return nil

	case OpDeleteEntity:
		if rec.Entity == nil {
			return errors.New("record has no entity")
		}
		existing, err := s.resolveRef(*rec.Entity, rec.ProjectID, stats)
		if err != nil {
			return err
		}
		if existing == nil {
			stats.Skipped++
			return nil
		}
		if err := s.DeleteEntity(existing.ID, rec.ProjectID); err != nil {
			return err
		}
		stats.Applied++
		return nil

	case OpCreateObservation:
		if rec.Entity == nil {
			return errors.New("record has no entity")
		}
		entity, err := s.resolveOrCreate(*rec.Entity, rec.ProjectID, stats)
		if err != nil {
			return err
		}
		dup, err := s.hasObservation(entity.ID, rec.ProjectID, rec.Content)
		if err != nil {
			return err
		}
		if dup {
			stats.Skipped++
			return nil
		}
		if _, err := s.CreateObservation(entity.ID, rec.Content, rec.ProjectID); err != nil {
			return err
		}
		stats.Applied++
		return nil

	case OpDeleteObservation:
		if rec.Entity == nil {
			return errors.New("record has no entity")
		}
		entity, err := s.resolveRef(*rec.Entity, rec.ProjectID, stats)
		if err != nil {
			return err
		}
		if entity == nil {
			stats.Skipped++
			return nil
		}
		observations, err := s.GetObservations(entity.ID, rec.ProjectID)
		if err != nil {
			return err
		}
		for _, o := range observations {
			if o.Content == rec.Content {
				if err := s.DeleteObservation(o.ID, entity.ID, rec.ProjectID); err != nil {
					return err
				}
				stats.Applied++
				return nil
			}
		}
		stats.Skipped++
		return nil

	case OpCreateRelation:
		from, to, err := s.resolveEnds(rec, stats, true)
		if err != nil {
			return err
		}
		// CreateRelation is not idempotent — Kuzu will happily store the same
		// edge twice — so an existing edge has to be detected before writing.
		dup, err := s.hasRelation(from.ID, to.ID, rec.RelType, rec.ProjectID)
		if err != nil {
			return err
		}
		if dup {
			stats.Skipped++
			return nil
		}
		if err := s.CreateRelation(from.ID, to.ID, rec.RelType, rec.ProjectID); err != nil {
			return err
		}
		stats.Applied++
		return nil

	case OpDeleteRelation:
		from, to, err := s.resolveEnds(rec, stats, false)
		if err != nil {
			return err
		}
		if from == nil || to == nil {
			stats.Skipped++
			return nil
		}
		if err := s.DeleteRelation(from.ID, to.ID, rec.RelType, rec.ProjectID); err != nil {
			return err
		}
		stats.Applied++
		return nil

	default:
		return fmt.Errorf("unknown op %q", rec.Op)
	}
}

// resolveRef finds the entity a record refers to, or nil when it is gone.
//
// The stable ID comes first. Indexer-derived IDs encode source position, so
// they survive a rebuild exactly and pick the right symbol out of the many that
// can share a name — which (name, type) cannot: kg's own graph has 55 colliding
// pairs, one name appearing 21 times.
//
// Falling back to (name, type) keeps journals written before the hint existed
// replayable, and covers entities whose file moved. When that fallback is
// itself ambiguous the choice is recorded, because silently attaching a
// hand-written note to an arbitrary same-named symbol is worse than saying so.
func (s *Store) resolveRef(ref EntityRef, projectID string, stats *ReplayStats) (*Entity, error) {
	if ref.ID != "" {
		entity, err := s.GetEntity(ref.ID, projectID)
		if err == nil && entity != nil {
			return entity, nil
		}
		// Not an error: the symbol may have moved or gone, and the name
		// fallback below is exactly what that case is for.
	}

	matches, err := s.FindEntitiesByNameAndType(ref.Name, ref.Type, projectID)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		stats.Ambiguous++
		stats.noteAmbiguity(ref, matches)
		return matches[0], nil
	}
}

// resolveEnds looks up both ends of a relation record. With create set, missing
// endpoints are created so the edge survives; without it, a missing endpoint
// yields nil and the caller skips (there is nothing to delete).
func (s *Store) resolveEnds(rec JournalRecord, stats *ReplayStats, create bool) (*Entity, *Entity, error) {
	if rec.From == nil || rec.To == nil {
		return nil, nil, errors.New("relation record is missing an endpoint")
	}
	if create {
		from, err := s.resolveOrCreate(*rec.From, rec.ProjectID, stats)
		if err != nil {
			return nil, nil, err
		}
		to, err := s.resolveOrCreate(*rec.To, rec.ProjectID, stats)
		if err != nil {
			return nil, nil, err
		}
		return from, to, nil
	}
	from, err := s.resolveRef(*rec.From, rec.ProjectID, stats)
	if err != nil {
		return nil, nil, err
	}
	to, err := s.resolveRef(*rec.To, rec.ProjectID, stats)
	if err != nil {
		return nil, nil, err
	}
	return from, to, nil
}

// resolveOrCreate finds the entity a record hangs off, creating a bare one if
// the graph no longer holds it. See the "lossless" note at the top of the file.
func (s *Store) resolveOrCreate(ref EntityRef, projectID string, stats *ReplayStats) (*Entity, error) {
	existing, err := s.resolveRef(ref, projectID, stats)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	created, err := s.CreateEntity(ref.Name, ref.Type, projectID)
	if err != nil {
		return nil, fmt.Errorf("create missing entity %q (%s): %w", ref.Name, ref.Type, err)
	}
	stats.Implicit++
	return created, nil
}

// hasObservation reports whether an entity already carries this exact content.
func (s *Store) hasObservation(entityID, projectID, content string) (bool, error) {
	observations, err := s.GetObservations(entityID, projectID)
	if err != nil {
		return false, err
	}
	for _, o := range observations {
		if o.Content == content {
			return true, nil
		}
	}
	return false, nil
}

// hasRelation reports whether the edge already exists.
func (s *Store) hasRelation(fromID, toID, relType, projectID string) (bool, error) {
	if err := s.validateRelType(relType); err != nil {
		return false, err
	}
	relations, err := s.GetRelations(fromID, projectID)
	if err != nil {
		return false, err
	}
	for _, r := range relations {
		if r.ToID == toID && r.Type == relType {
			return true, nil
		}
	}
	return false, nil
}
