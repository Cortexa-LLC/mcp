package main

import (
	"fmt"
	"os"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var (
	indexScopeName string
	indexAll       bool
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index the codebase and populate the knowledge graph with structural data",
	Long: `Index the codebase into one or more knowledge graph scopes.

By default, indexes the default scope (or all scopes if no default is set).
Use --scope to index a specific scope, or --all to index all defined scopes.

Examples:
  kg index                  # Index default scope (or legacy knowledge.db)
  kg index --scope selling  # Index only the selling scope
  kg index --all            # Index all scopes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := root + "/.ai"

		// Determine which scopes to index
		scopes, err := determineScopesToIndex(aiDir, indexScopeName, indexAll)
		if err != nil {
			return err
		}

		// Index each scope
		totalStart := time.Now()
		for _, scopeName := range scopes {
			if err := indexScopeDB(root, aiDir, scopeName); err != nil {
				return fmt.Errorf("index scope %s: %w", scopeName, err)
			}
		}

		if len(scopes) > 1 {
			fmt.Printf("✅ All scopes indexed in %.3fs\n", time.Since(totalStart).Seconds())
		}
		return nil
	},
}

func determineScopesToIndex(aiDir, requestedScope string, all bool) ([]string, error) {
	// Check if scopes are defined
	configs, err := knowledge.ListScopeConfigs(aiDir)
	if err != nil {
		return nil, err
	}

	// No scopes defined - use legacy mode
	if len(configs) == 0 {
		return []string{""}, nil // Empty string = legacy knowledge.db
	}

	// Explicit --all flag
	if all {
		names := make([]string, len(configs))
		for i, cfg := range configs {
			names[i] = cfg.Name
		}
		return names, nil
	}

	// Explicit --scope flag
	if requestedScope != "" {
		return []string{requestedScope}, nil
	}

	// Check for default scope
	defaultScope, err := knowledge.GetDefaultScope(aiDir)
	if err != nil {
		return nil, err
	}
	if defaultScope != "" {
		return []string{defaultScope}, nil
	}

	// No default set - index all scopes
	names := make([]string, len(configs))
	for i, cfg := range configs {
		names[i] = cfg.Name
	}
	return names, nil
}

func indexScopeDB(root, aiDir, scopeName string) error {
	var dbPath string
	var scopeConfig *knowledge.ScopeConfig

	if scopeName != "" {
		cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
		if err != nil {
			return err
		}
		scopeConfig = cfg
		dbPath = aiDir + "/" + cfg.Database
		fmt.Printf("🔍 Indexing scope '%s' at %s...\n", scopeName, root)
	} else {
		dbPath = aiDir + "/knowledge.db"
		fmt.Printf("🔍 Indexing codebase at %s...\n", root)
	}

	store, err := knowledge.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open Kuzu store: %w", err)
	}
	defer store.Close()

	projectID := projectIDFromCwd(root)
	indexer, err := knowledge.NewIndexer(store, projectID, root)
	if err != nil {
		return err
	}

	// Set scope filter if defined
	if scopeConfig != nil {
		indexer.SetScopeFilter(scopeConfig)
	}

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
}

func init() {
	rootCmd.AddCommand(indexCmd)
	indexCmd.Flags().StringVar(&indexScopeName, "scope", "", "Index a specific scope")
	indexCmd.Flags().BoolVar(&indexAll, "all", false, "Index all defined scopes")
}
