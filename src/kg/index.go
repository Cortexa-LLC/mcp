package main

import (
	"fmt"
	"os"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index the codebase and populate the knowledge graph with structural data",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		dbPath := root + "/.ai/knowledge.db"
		store, err := knowledge.OpenStore(dbPath)
		if err != nil {
			return fmt.Errorf("open Kuzu store: %w", err)
		}
		defer store.Close()

		projectID := projectIDFromCwd(cwd)
		indexer, err := knowledge.NewIndexer(store, projectID, root)
		if err != nil {
			return err
		}
		fmt.Printf("🔍 Indexing codebase at %s...\n", root)
		start := time.Now()
		stats, err := indexer.Index()
		if err != nil {
			return err
		}
		codesDur := time.Since(start)
		fmt.Println("✅ Source indexing complete!")
		fmt.Printf("   Files scanned:     %d\n", stats.FilesScanned)
		fmt.Printf("   Entities created:  %d\n", stats.EntitiesCreated)
		fmt.Printf("   Relations created: %d\n", stats.RelationsCreated)
		fmt.Printf("   Duration:          %.3fs\n", codesDur.Seconds())

		// Second pass: index agent execution logs from .beads/tasks/
		fmt.Println("📋 Indexing execution logs...")
		logStats, err := knowledge.IndexExecutionLogs(store, projectID, root)
		logsDur := time.Since(start) - codesDur
		if err != nil {
			fmt.Printf("Warning: execution log indexing failed: %v\n", err)
		} else {
			fmt.Printf("   Logs found:        %d\n", logStats.LogsFound)
			fmt.Printf("   Logs indexed:      %d\n", logStats.LogsIndexed)
			if logStats.LogsSkipped > 0 {
				fmt.Printf("   Logs skipped:      %d\n", logStats.LogsSkipped)
			}
			fmt.Printf("   Observations:      %d\n", logStats.Observations)
			fmt.Printf("   Duration:          %.3fs\n", logsDur.Seconds())
		}

		fmt.Printf("✅ Total duration: %.3fs\n", time.Since(start).Seconds())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(indexCmd)
}
