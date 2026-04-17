package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var (
	searchScopeName string
	searchAll       bool
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search the knowledge graph",
	Long: `Search the knowledge graph across one or more scopes.

By default, searches the default scope (including its layers).
Use --scope to search a specific scope, or --all to search all scopes.

Examples:
  kg search "authentication"              # Search default scope + layers
  kg search "api endpoint" --scope team-a # Search team-a scope + layers
  kg search "database" --all              # Search all scopes`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")
		projectID := projectIDFromCwd(cwd)

		query := args[0]

		// Determine which scope to search
		scopeName := searchScopeName
		if scopeName == "" && !searchAll {
			// Use default scope
			defaultScope, err := knowledge.GetDefaultScope(aiDir)
			if err != nil {
				return err
			}
			scopeName = defaultScope
		}

		if searchAll {
			return searchAllScopes(aiDir, projectID, query)
		}

		return searchScope(aiDir, projectID, scopeName, query)
	},
}

func searchScope(aiDir, projectID, scopeName, query string) error {
	// Check if scopes are defined
	configs, err := knowledge.ListScopeConfigs(aiDir)
	if err != nil {
		return err
	}

	// Legacy mode or no scope specified
	if len(configs) == 0 || scopeName == "" {
		// Use legacy single store
		dbPath := filepath.Join(aiDir, "knowledge.db")
		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()

		results, err := store.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
		if err != nil {
			return err
		}
		printResults(results)
		return nil
	}

	// Load scope config
	scopeConfig, err := knowledge.LoadScopeConfig(aiDir, scopeName)
	if err != nil {
		return err
	}

	// Check if this scope has layers
	if len(scopeConfig.Layers) > 0 {
		// Use federated store
		fs, err := knowledge.OpenFederatedStore(aiDir, scopeConfig, true)
		if err != nil {
			return err
		}
		defer fs.Close()

		results, err := fs.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
		if err != nil {
			return err
		}
		printResults(results)
	} else {
		// Single scope, no federation needed
		dbPath := filepath.Join(aiDir, scopeConfig.Database)
		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()

		results, err := store.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
		if err != nil {
			return err
		}
		printResults(results)
	}

	return nil
}

func searchAllScopes(aiDir, projectID, query string) error {
	configs, err := knowledge.ListScopeConfigs(aiDir)
	if err != nil {
		return err
	}

	if len(configs) == 0 {
		return fmt.Errorf("no scopes defined")
	}

	// Collect results from all scopes
	allResults := make(map[string]*knowledge.SearchResult)

	for _, cfg := range configs {
		dbPath := filepath.Join(aiDir, cfg.Database)
		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			fmt.Printf("Warning: failed to open %s: %v\n", cfg.Name, err)
			continue
		}

		results, err := store.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
		store.Close()

		if err != nil {
			fmt.Printf("Warning: search in %s failed: %v\n", cfg.Name, err)
			continue
		}

		// Merge results (simple dedup by entity ID)
		for _, result := range results {
			if existing, found := allResults[result.Entity.ID]; found {
				// Combine scores
				existing.Score += result.Score
			} else {
				allResults[result.Entity.ID] = result
			}
		}
	}

	// Convert to slice and print
	merged := make([]*knowledge.SearchResult, 0, len(allResults))
	for _, result := range allResults {
		merged = append(merged, result)
	}

	printResults(merged)
	return nil
}

func printResults(results []*knowledge.SearchResult) {
	for _, res := range results {
		fmt.Printf("%s\t%s\t%s\n", res.Entity.ID, res.Entity.Type, res.Entity.Name)
	}
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().StringVar(&searchScopeName, "scope", "", "Search a specific scope")
	searchCmd.Flags().BoolVar(&searchAll, "all", false, "Search all scopes")
}
