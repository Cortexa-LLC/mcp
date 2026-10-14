package main

import (
	"fmt"
	"io"
	"os"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
	"github.com/spf13/cobra"
)

// Storage-format migration.
//
// Kuzu pins its on-disk format to the library version, so bumping go-kuzu can
// leave every existing database unopenable. The promise to anyone who has
// adopted kg is that this costs them a slower run, never an error and never a
// manual recovery, so a rebuild is split by what the data is made of:
//
//   - Indexed data is derived from the tree and is re-indexed from scratch.
//   - Hand-written knowledge has no source, and comes back from the journal
//     beside the database (see kglib/journal.go).
//
// The old database is archived rather than deleted. It is never read again by
// this code — replay reads the journal, not the database — but a rebuild that
// turns out worse than what it replaced should be recoverable.

// archiveIfMismatch moves a database aside when this build cannot read or use
// its on-disk shape, returning the path it was archived to (empty when nothing
// was done). The stamp check happens before any open, because a database in the
// wrong storage format cannot be opened to be asked about itself.
//
// An unstamped database — one written before stamping shipped — is normally left
// alone: there is no evidence against it, and rebuilding every pre-existing
// graph on first run would be the forced breakage this exists to avoid. The one
// exception is an unstamped database that ALSO predates the pk column: it cannot
// be opened read-write (the pk gate in OpenStore hard-errors) and neither `kg
// index` nor `kg migrate` would otherwise repair it, so it is a dead end unless
// it is archived and rebuilt here. That case is detected by a read-only open —
// a pre-pk schema is still readable — so a current unstamped database (which has
// pk) is never falsely archived; it simply gets stamped on the next write open.
//
// missingPKHint lets a caller that has already run databaseMissingPK for this
// database pass the result in, so a `kg migrate` run does not re-derive it with a
// second read-only open. It is nil on the standalone `kg index` path, where the
// value is computed here on demand.
func archiveIfMismatch(dbPath string, out io.Writer, missingPKHint *bool) (string, error) {
	status, stamp, err := kglib.CheckFormat(dbPath)
	if err != nil {
		return "", err
	}

	var reason string
	switch status {
	case kglib.FormatMismatch:
		// A mismatch is either a storage-format (Kuzu) bump or a kg schema bump;
		// the message names whichever actually differs so a same-Kuzu schema
		// change does not print the confusing "written by kuzu X; this build uses X".
		if stamp.KuzuVersion != kglib.KuzuVersion() {
			reason = fmt.Sprintf("was written by kuzu %s; this build uses %s",
				stamp.KuzuVersion, kglib.KuzuVersion())
		} else {
			reason = fmt.Sprintf("uses kg schema v%d; this build needs v%d",
				stamp.SchemaVersion, kglib.CurrentSchemaVersion)
		}
	case kglib.FormatUnstamped:
		var missing bool
		if missingPKHint != nil {
			missing = *missingPKHint
		} else {
			m, err := databaseMissingPK(dbPath)
			if err != nil {
				// Cannot introspect the columns (e.g. the database will not even open
				// read-only). Fall back to the historical leave-alone behavior rather
				// than archiving on a guess.
				return "", nil
			}
			missing = m
		}
		if !missing {
			// Unstamped but already current — leave it alone; the next write open
			// stamps it.
			return "", nil
		}
		reason = "is unstamped and predates the project-namespaced primary key"
	default:
		return "", nil
	}

	fmt.Fprintf(out, "🔄 %s %s. Rebuilding.\n", dbPath, reason)

	aside, err := kglib.ArchiveDatabase(dbPath)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "   Archived:          %s\n", aside)
	return aside, nil
}

// databaseMissingPK reports whether an existing database lacks the pk column,
// i.e. predates the project-namespaced primary key. It opens the database
// read-only purely to introspect its columns — a pre-pk schema is still readable,
// and a read-only open neither migrates the schema nor trips the write-open pk
// gate, so this is a safe question to ask of any database.
func databaseMissingPK(dbPath string) (bool, error) {
	store, err := knowledge.OpenStoreReadOnly(dbPath)
	if err != nil {
		return false, err
	}
	defer store.Close()
	return !store.HasPK(), nil
}

// rebuildHandWritesOnly repairs a database that has no source tree behind it —
// the personal store. Archive, create an empty replacement, replay. There is
// nothing to index, so the ordering hazard above does not apply.
func rebuildHandWritesOnly(dbPath string, out io.Writer) (bool, error) {
	aside, err := archiveIfMismatch(dbPath, out, nil)
	if err != nil || aside == "" {
		return false, err
	}

	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		return false, fmt.Errorf("create replacement database: %w", err)
	}
	defer store.Close()

	if _, err := restoreHandWrites(store, dbPath, kglib.JournalPath(aside), out); err != nil {
		return false, err
	}
	return true, nil
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Rebuild knowledge graphs whose storage format this build cannot read",
	Long: `Rebuild any knowledge graph written by a different Kuzu storage format.

Indexed content is rebuilt by re-indexing; hand-written knowledge is restored
from the journal kept beside each database. The previous database is archived
next to the new one rather than deleted.

Normally this runs by itself when it is needed. Run it explicitly to migrate
every scope and the personal store in one pass, or to see what would happen.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		checked := 0
		rebuilt := 0

		// unchecked counts everything this run could not account for: a scope
		// list it could not read, a database whose format it could not
		// determine, a rebuild that failed. It gates the summary, because the
		// one thing this command must never do is print an all-clear for a
		// database it never looked at — the user reads that as "my graphs are
		// fine" and stops thinking about it.
		unchecked := 0
		failf := func(format string, args ...any) {
			unchecked++
			fmt.Fprintf(out, "⚠️  "+format+"\n", args...)
		}

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get cwd: %w", err)
		}
		root := findProjectRoot(cwd)
		aiDir := root + "/.ai"

		// Project scopes: rebuildable by re-indexing, so a mismatch is repaired
		// by indexing rather than by replay alone.
		scopes, err := determineScopesToIndex(aiDir, "", true)
		if err != nil {
			// Every project database lives behind this list, so failing to read
			// it means none of them were examined.
			failf("cannot enumerate scopes in %s, so no project database was checked: %v", aiDir, err)
		}
		for _, scope := range scopes {
			label := scope
			if label == "" {
				label = "(default)"
			}
			dbPath := aiDir + "/knowledge.db"
			if scope != "" {
				cfg, err := knowledge.LoadScopeConfig(aiDir, scope)
				if err != nil {
					failf("scope %s: cannot read scope config, database not checked: %v", label, err)
					continue
				}
				dbPath = aiDir + "/" + cfg.Database
			}
			status, _, err := kglib.CheckFormat(dbPath)
			if err != nil {
				failf("scope %s: cannot determine storage format of %s: %v", label, dbPath, err)
				continue
			}
			// Counted only once the format is actually known: "checked" is a
			// claim about evidence, not about how many paths were visited.
			checked++
			// A stamped mismatch always rebuilds. An unstamped database is left
			// alone UNLESS it also predates the pk column, which is a dead end
			// (write-open refuses it) that only an archive+rebuild can repair —
			// the same exception archiveIfMismatch makes. A current unstamped
			// database (has pk) is still skipped.
			needsRebuild := status == kglib.FormatMismatch
			// missingHint carries the single databaseMissingPK result for this
			// database into archiveIfMismatch (reached via indexScopeDB below), so
			// the read-only open it does is not repeated there.
			var missingHint *bool
			if !needsRebuild && status == kglib.FormatUnstamped {
				missing, mErr := databaseMissingPK(dbPath)
				if mErr != nil {
					// Mirror the CheckFormat-error path above: an introspection we
					// could not complete is not evidence that this build can read
					// the database, so it must count as unchecked (failf) rather
					// than fall through with needsRebuild=false and reach the
					// all-clear summary for a database we never resolved.
					failf("scope %s: cannot determine whether %s predates the primary key: %v", label, dbPath, mErr)
					continue
				}
				if missing {
					needsRebuild = true
					missingHint = &missing
				}
			}
			if !needsRebuild {
				continue
			}
			if err := indexScopeDB(root, aiDir, scope, missingHint); err != nil {
				failf("scope %s: rebuild failed, %s is still unreadable: %v", label, dbPath, err)
				continue
			}
			rebuilt++
		}

		// The personal store has no source tree; replay is the whole rebuild.
		if personalStoreExists() {
			dbPath, err := personalDBPath()
			if err != nil {
				return err
			}
			did, err := rebuildHandWritesOnly(dbPath, out)
			switch {
			case err != nil:
				failf("personal store: %v", err)
			case did:
				checked++
				rebuilt++
			default:
				checked++
			}
		}

		switch {
		case unchecked > 0:
			fmt.Fprintf(out, "❌ %d database(s) checked, %d rebuilt; %d could not be checked (see above).\n",
				checked, rebuilt, unchecked)
			return fmt.Errorf("%d database(s) could not be checked", unchecked)
		case rebuilt == 0:
			fmt.Fprintf(out, "✅ %d database(s) checked; all readable by this build (kuzu %s).\n",
				checked, kglib.KuzuVersion())
		default:
			fmt.Fprintf(out, "✅ %d of %d database(s) rebuilt.\n", rebuilt, checked)
		}
		return nil
	},
}

func init() {
	// A database that could not be checked is an operational failure, not a
	// misuse of the command; printing the usage text after it buries the
	// warnings that say which database is at risk.
	migrateCmd.SilenceUsage = true
	rootCmd.AddCommand(migrateCmd)
}
