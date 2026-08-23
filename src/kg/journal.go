package main

import (
	"fmt"
	"io"
	"os"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
)

// The hand-write journal, from the CLI's side.
//
// kglib writes the journal; this is where kg decides when to read it back. That
// is a narrower question than it sounds, and the answer is "after every index",
// for a reason documented on restoreHandWrites.

// restoreHandWrites replays the hand-write journal into a store.
//
// This runs after indexing, never before, for a reason that is easy to get
// backwards: Indexer.Index begins by calling clearProjectData, which deletes
// every entity, observation, and relation belonging to the project — including
// hand-written ones, which share the project ID. Replaying first would put the
// journal's contents in exactly the place indexing is about to erase.
//
// Running it after every index, not only after a format migration, is what
// makes `kg add entity` durable against `kg index` at all.
//
// journalSrc is the journal to read: the archived one when a migration just
// moved it aside, otherwise the database's own.
func restoreHandWrites(store *knowledge.Store, dbPath, journalSrc string, out io.Writer) (kglib.ReplayStats, error) {
	stats, replayErr := store.ReplayJournal(journalSrc)

	// Carry the journal forward BEFORE reacting to a replay failure.
	//
	// After a migration the journal lives beside the archived database, and this
	// copy is the only thing that puts one beside the new one. Returning early
	// on a replay error skipped it, so the next run — seeing no format mismatch,
	// and therefore reading the new database's own journal — found a file that
	// was never written, replayed nothing, and the hand-written knowledge was
	// gone from every path the tool would look at again. The failure that
	// stranded it was one line of warning output.
	if journalSrc != kglib.JournalPath(dbPath) {
		if err := copyFile(journalSrc, kglib.JournalPath(dbPath)); err != nil {
			return stats, fmt.Errorf("carry journal forward from %s: %w (replay error, if any: %v)",
				journalSrc, err, replayErr)
		}
	}
	if replayErr != nil {
		return stats, replayErr
	}

	if stats.Applied > 0 || stats.Failed > 0 {
		fmt.Fprintf(out, "📝 Hand-written knowledge restored from the journal\n")
		fmt.Fprintf(out, "   Journal records:   %d\n", stats.Records)
		fmt.Fprintf(out, "   Restored:          %d\n", stats.Applied)
		if stats.Implicit > 0 {
			fmt.Fprintf(out, "   Entities recreated to host orphaned notes: %d\n", stats.Implicit)
		}
		if stats.Failed > 0 {
			fmt.Fprintf(out, "   ⚠️  Unreplayable:   %d\n", stats.Failed)
			for _, e := range stats.Errors {
				fmt.Fprintf(out, "        %v\n", e)
			}
		}
	}
	return stats, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}
