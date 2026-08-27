package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var statsScopeName string

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show entity, relation, and observation statistics",
	Long: `Show statistics for the knowledge graph.

By default, shows stats for the default scope. Use --scope to specify a different scope.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if usePersonal {
			store, projectID, err := openPersonalStore(true)
			if err != nil {
				return err
			}
			defer store.Close()
			fmt.Println("Stats for the personal knowledge store")
			return printStats(store, projectID)
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")
		projectID := projectIDFromCwd(cwd)

		// Shared with kg health: a scope named with --scope that cannot be
		// loaded is an error rather than a silent fallback to the legacy
		// database; an inherited default scope still falls back (resolveScopeDB).
		dbPath, scopeName, err := resolveScopeDB(aiDir, statsScopeName)
		if err != nil {
			return err
		}
		if scopeName != "" {
			fmt.Printf("Stats for scope: %s\n", scopeName)
		}

		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()

		return printStats(store, projectID)
	},
}

// printStats prints entity, relation, and observation counts for one store.
// Uses count queries rather than iterating entities.
func printStats(store *knowledge.Store, projectID string) error {
	entityCount, err := store.CountEntities(projectID)
	if err != nil {
		return err
	}

	relationCount, err := store.CountRelations(projectID)
	if err != nil {
		return err
	}

	observationCount, err := store.CountObservations(projectID)
	if err != nil {
		return err
	}

	fmt.Printf("Entities: %d\n", entityCount)
	fmt.Printf("Relations: %d\n", relationCount)
	fmt.Printf("Observations: %d\n", observationCount)
	return nil
}

func init() {
	rootCmd.AddCommand(statsCmd)
	registerPersonalFlag(statsCmd)
	statsCmd.Flags().StringVar(&statsScopeName, "scope", "", "Show stats for a specific scope")
}
