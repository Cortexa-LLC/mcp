package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
	"github.com/spf13/cobra"
)

// export / import.
//
// One JSONL format serves three jobs, because they are the same job: a backup,
// a portable dump, and the hand-write journal are all "the writes needed to
// reconstruct this graph". Import is therefore literally journal replay — see
// kglib/export.go for why that falls out rather than being contrived.

var (
	exportOutput  string
	exportScope   string
	exportJournal bool
	importInput   string
	importScope   string
	importDryRun  bool
	// importPreserve keeps the file's own project ID instead of re-projecting
	// records onto the graph they are being imported into.
	importPreserve bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export a knowledge graph to JSONL",
	Long: `Write a knowledge graph to JSONL, one record per line.

The format is the same one the hand-write journal uses, so an export can be fed
back through 'kg import' or dropped in as a journal. Entities are identified by
name and type rather than by ID, so a dump stays valid across re-indexing.

Writes to stdout unless -o is given.

Examples:
  kg export -o backup.jsonl              # this project's default scope
  kg export --personal -o personal.jsonl # the personal knowledge store
  kg export --scope selling              # a named scope
  kg export --journal -o compacted.jsonl # hand-written knowledge only, compacted`,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, projectID, err := openTarget(true, exportScope)
		if err != nil {
			return err
		}
		defer store.Close()

		out := cmd.OutOrStdout()
		if exportOutput != "" {
			f, err := os.Create(exportOutput)
			if err != nil {
				return fmt.Errorf("create %s: %w", exportOutput, err)
			}
			defer f.Close()
			out = f
		}

		writer := bufio.NewWriter(out)
		defer writer.Flush()

		if exportJournal {
			return exportCompactedJournal(cmd, writer, projectID)
		}

		count, err := writeExport(writer, store, projectID)
		if err != nil {
			return err
		}
		if exportOutput != "" {
			cmd.PrintErrf("Exported %d records to %s\n", count, exportOutput)
		}
		return nil
	},
}

// writeExport streams a project's state to w as JSONL, returning the record count.
func writeExport(w io.Writer, store *knowledge.Store, projectID string) (int, error) {
	encoder := json.NewEncoder(w)
	count := 0
	err := store.ExportProject(projectID, func(rec kglib.JournalRecord) error {
		rec.Version = kglib.JournalVersion
		count++
		return encoder.Encode(rec)
	})
	return count, err
}

// exportCompactedJournal rewrites the hand-write journal as a current-state
// dump: a create followed by ten edits and a delete becomes whatever the graph
// actually holds now.
//
// Compaction works by replaying the journal into a scratch database and
// exporting the result, rather than by reasoning about the records directly.
// Replay already knows how records combine — that logic exists once, and a
// second copy of it in a compactor would be a second copy that could disagree.
func exportCompactedJournal(cmd *cobra.Command, w io.Writer, projectID string) error {
	dbPath, err := targetDBPath(exportScope)
	if err != nil {
		return err
	}
	journalPath := kglib.JournalPath(dbPath)
	if _, err := os.Stat(journalPath); os.IsNotExist(err) {
		cmd.PrintErrf("No journal at %s — nothing hand-written to compact.\n", journalPath)
		return nil
	}

	scratchDir, err := os.MkdirTemp("", "kg-compact-")
	if err != nil {
		return fmt.Errorf("create scratch directory: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	scratch, err := knowledge.OpenStore(filepath.Join(scratchDir, "compact.db"))
	if err != nil {
		return fmt.Errorf("create scratch database: %w", err)
	}
	defer scratch.Close()

	stats, err := scratch.ReplayJournal(journalPath)
	if err != nil {
		return fmt.Errorf("replay journal: %w", err)
	}
	if stats.Failed > 0 {
		cmd.PrintErrf("Warning: %d journal record(s) could not be replayed and are omitted:\n", stats.Failed)
		for _, e := range stats.Errors {
			cmd.PrintErrf("  %v\n", e)
		}
	}

	count, err := writeExport(w, scratch, projectID)
	if err != nil {
		return err
	}
	cmd.PrintErrf("Compacted %d journal record(s) to %d.\n", stats.Records, count)
	return nil
}

var importCmd = &cobra.Command{
	Use:   "import [file]",
	Short: "Import a JSONL knowledge graph export",
	Long: `Read a JSONL export (or a journal) and apply it to a knowledge graph.

Importing is idempotent: entities are matched by name and type, and records
already reflected in the graph are skipped rather than duplicated. Importing the
same file twice leaves the same graph.

Records are re-projected onto the graph being imported into, so a dump taken
from one project is searchable after being imported into another. Pass
--preserve-project to keep the project ID the file records instead.

Reads stdin when no file is given.

Examples:
  kg import backup.jsonl
  kg import --personal personal.jsonl
  kg import --dry-run backup.jsonl       # report what would change`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		source := importInput
		if len(args) == 1 {
			source = args[0]
		}

		// ReplayJournal reads a path, so stdin is spooled to a temporary file.
		// Imports are small enough that this is simpler than a second reader
		// path through the replay logic.
		if source == "" || source == "-" {
			spooled, cleanup, err := spoolStdin(cmd.InOrStdin())
			if err != nil {
				return err
			}
			defer cleanup()
			source = spooled
		}

		if importDryRun {
			return reportImportPlan(cmd, source)
		}

		store, projectID, err := openTarget(false, importScope)
		if err != nil {
			return err
		}
		defer store.Close()

		// Records are re-projected onto the graph being imported into, unless
		// asked otherwise. A dump carries the project it came from, and a graph
		// only answers queries for its own project ID, so importing verbatim
		// into a different project writes rows that no search will ever return.
		target := projectID
		if importPreserve {
			target = ""
		}

		// ImportRecords, not ReplayJournal: these records are arriving from
		// outside this database and have no journal entry here yet, so they must
		// be journaled to survive the next storage-format rebuild.
		stats, err := store.ImportRecords(source, target)
		if err != nil {
			return err
		}

		cmd.Printf("Imported %s\n", source)
		cmd.Printf("   Records read:      %d\n", stats.Records)
		cmd.Printf("   Applied:           %d\n", stats.Applied)
		if stats.Skipped > 0 {
			cmd.Printf("   Already present:   %d\n", stats.Skipped)
		}
		if stats.Implicit > 0 {
			cmd.Printf("   Entities created to host orphaned records: %d\n", stats.Implicit)
		}
		if stats.Failed > 0 {
			cmd.Printf("   ⚠️  Rejected:       %d\n", stats.Failed)
			for _, e := range stats.Errors {
				cmd.Printf("        %v\n", e)
			}
		}
		return nil
	},
}

// reportImportPlan applies the import to a scratch database and reports the
// outcome without touching the real graph.
func reportImportPlan(cmd *cobra.Command, source string) error {
	scratchDir, err := os.MkdirTemp("", "kg-import-dryrun-")
	if err != nil {
		return fmt.Errorf("create scratch directory: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	scratch, err := knowledge.OpenStore(filepath.Join(scratchDir, "dryrun.db"))
	if err != nil {
		return fmt.Errorf("create scratch database: %w", err)
	}
	defer scratch.Close()

	stats, err := scratch.ImportRecords(source, "")
	if err != nil {
		return err
	}

	cmd.Printf("Dry run of %s — nothing was written.\n", source)
	cmd.Printf("   Records read:      %d\n", stats.Records)
	cmd.Printf("   Would apply:       %d\n", stats.Applied)
	if stats.Failed > 0 {
		cmd.Printf("   ⚠️  Would reject:   %d\n", stats.Failed)
		for _, e := range stats.Errors {
			cmd.Printf("        %v\n", e)
		}
	}
	cmd.Printf("\nCounts are against an empty graph; against your existing graph, " +
		"records already present would be skipped instead of applied.\n")
	return nil
}

func spoolStdin(r io.Reader) (string, func(), error) {
	tmp, err := os.CreateTemp("", "kg-import-*.jsonl")
	if err != nil {
		return "", func() {}, fmt.Errorf("spool stdin: %w", err)
	}
	cleanup := func() { os.Remove(tmp.Name()) }
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("read stdin: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tmp.Name(), cleanup, nil
}

// targetDBPath resolves the database file a command is acting on, mirroring
// openTarget's resolution so the journal beside it can be found.
func targetDBPath(scopeFlag string) (string, error) {
	if usePersonal {
		return personalDBPath()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	aiDir := filepath.Join(findProjectRoot(cwd), ".ai")

	scopeName := scopeFlag
	if scopeName == "" {
		scopeName, _ = knowledge.GetDefaultScope(aiDir)
	}
	if scopeName == "" {
		return filepath.Join(aiDir, "knowledge.db"), nil
	}

	cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
	if err != nil {
		return "", fmt.Errorf("load scope %s: %w", scopeName, err)
	}
	return filepath.Join(aiDir, cfg.Database), nil
}

func init() {
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Write to a file instead of stdout")
	exportCmd.Flags().StringVar(&exportScope, "scope", "", "Scope to export (default: default scope)")
	exportCmd.Flags().BoolVar(&exportJournal, "journal", false,
		"Export the compacted hand-write journal instead of the whole graph")
	registerPersonalFlag(exportCmd)

	importCmd.Flags().StringVarP(&importInput, "input", "i", "", "Read from a file instead of stdin")
	importCmd.Flags().StringVar(&importScope, "scope", "", "Scope to import into (default: default scope)")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "Report what would be imported without writing")
	importCmd.Flags().BoolVar(&importPreserve, "preserve-project", false,
		"Keep the project ID recorded in the file instead of re-projecting onto the target graph")
	registerPersonalFlag(importCmd)

	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(importCmd)
}
