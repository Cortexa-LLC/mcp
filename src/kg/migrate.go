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

// archiveIfMismatch moves a database aside when this build cannot read its
// storage format, returning the path it was archived to (empty when nothing was
// done). The check happens before any open, because a database in the wrong
// format cannot be opened to be asked about itself.
//
// An unstamped database — one written before stamping shipped — is left alone.
// There is no evidence against it, and rebuilding every pre-existing graph on
// first run would be precisely the forced breakage this exists to avoid.
func archiveIfMismatch(dbPath string, out io.Writer) (string, error) {
	status, stamp, err := kglib.CheckFormat(dbPath)
	if err != nil {
		return "", err
	}
	if status != kglib.FormatMismatch {
		return "", nil
	}

	fmt.Fprintf(out, "🔄 %s was written by kuzu %s; this build uses %s. Rebuilding.\n",
		dbPath, stamp.KuzuVersion, kglib.KuzuVersion())

	aside, err := kglib.ArchiveDatabase(dbPath)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "   Archived:          %s\n", aside)
	return aside, nil
}

// rebuildHandWritesOnly repairs a database that has no source tree behind it —
// the personal store. Archive, create an empty replacement, replay. There is
// nothing to index, so the ordering hazard above does not apply.
func rebuildHandWritesOnly(dbPath string, out io.Writer) (bool, error) {
	aside, err := archiveIfMismatch(dbPath, out)
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

		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get cwd: %w", err)
		}
		root := findProjectRoot(cwd)
		aiDir := root + "/.ai"

		// Project scopes: rebuildable by re-indexing, so a mismatch is repaired
		// by indexing rather than by replay alone.
		scopes, err := determineScopesToIndex(aiDir, "", true)
		if err == nil {
			for _, scope := range scopes {
				dbPath := aiDir + "/knowledge.db"
				if scope != "" {
					cfg, err := knowledge.LoadScopeConfig(aiDir, scope)
					if err != nil {
						fmt.Fprintf(out, "⚠️  scope %s: %v\n", scope, err)
						continue
					}
					dbPath = aiDir + "/" + cfg.Database
				}
				checked++
				status, _, err := kglib.CheckFormat(dbPath)
				if err != nil || status != kglib.FormatMismatch {
					continue
				}
				if err := indexScopeDB(root, aiDir, scope); err != nil {
					fmt.Fprintf(out, "⚠️  scope %s: %v\n", scope, err)
					continue
				}
				rebuilt++
			}
		}

		// The personal store has no source tree; replay is the whole rebuild.
		if personalStoreExists() {
			dbPath, err := personalDBPath()
			if err != nil {
				return err
			}
			checked++
			did, err := rebuildHandWritesOnly(dbPath, out)
			if err != nil {
				return fmt.Errorf("personal store: %w", err)
			}
			if did {
				rebuilt++
			}
		}

		if rebuilt == 0 {
			fmt.Fprintf(out, "✅ %d database(s) checked; all readable by this build (kuzu %s).\n",
				checked, kglib.KuzuVersion())
		} else {
			fmt.Fprintf(out, "✅ %d of %d database(s) rebuilt.\n", rebuilt, checked)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
