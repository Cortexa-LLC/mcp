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
Use --personal to search the personal knowledge store instead of this project,
or --with-personal to search this project's graph and the personal store together.

Examples:
  kg search "authentication"              # Search default scope + layers
  kg search "api endpoint" --scope team-a # Search team-a scope + layers
  kg search "database" --all              # Search all scopes
  kg search "retention" --personal          # Search the personal store only
  kg search "retention" --with-personal     # This project's graph plus personal`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if usePersonal && withPersonal {
			return fmt.Errorf("--personal and --with-personal are mutually exclusive: " +
				"--personal searches only the personal store, --with-personal adds it to this project's search")
		}

		if usePersonal {
			store, projectID, err := openPersonalStore(true)
			if err != nil {
				return err
			}
			defer store.Close()

			results, err := store.HybridSearch(projectID, args[0], nil, knowledge.DefaultSearchConfig())
			if err != nil {
				return err
			}
			printResults(results)
			return nil
		}

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

	// Resolve the scope being searched. Legacy projects (no scope configs, or no
	// scope selected) get a synthetic config pointing at .ai/knowledge.db so the
	// federation path below works the same either way.
	var scopeConfig *knowledge.ScopeConfig
	if len(configs) == 0 || scopeName == "" {
		scopeConfig = &knowledge.ScopeConfig{Name: "knowledge", Database: "knowledge.db"}
	} else {
		scopeConfig, err = knowledge.LoadScopeConfig(aiDir, scopeName)
		if err != nil {
			return err
		}
	}

	// --with-personal adds the personal store as a lowest-priority layer, so
	// project knowledge outranks it wherever both match.
	var extra []knowledge.LayerConfig
	if withPersonal {
		layer, err := personalLayer()
		if err != nil {
			return err
		}
		if layer == nil {
			path, _ := personalDBPath()
			fmt.Fprintf(os.Stderr,
				"Note: no personal knowledge store at %s — searching this project only (create it with 'kg personal init')\n",
				path)
		} else {
			extra = append(extra, *layer)
		}
	}

	// Nothing to merge: read the single database directly.
	if len(scopeConfig.Layers) == 0 && len(scopeConfig.Remotes) == 0 && len(extra) == 0 {
		store, err := knowledge.OpenStoreReadOnly(filepath.Join(aiDir, scopeConfig.Database))
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

	fs, err := knowledge.OpenFederatedStoreWithExtra(aiDir, scopeConfig, true, extra)
	if err != nil {
		return err
	}
	defer fs.Close()

	results, err := fs.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
	if err != nil {
		return err
	}
	printResults(results)
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
	searchCmd.Flags().BoolVar(&withPersonal, "with-personal", false,
		"Also search your personal knowledge store, ranked below project results")
	registerPersonalFlag(searchCmd)
}
