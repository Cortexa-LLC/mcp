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
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")
		projectID := projectIDFromCwd(cwd)

		// Determine which scope to use
		scopeName := statsScopeName
		if scopeName == "" {
			// Use default scope
			defaultScope, err := knowledge.GetDefaultScope(aiDir)
			if err != nil {
				return err
			}
			scopeName = defaultScope
		}

		// Open appropriate store
		var dbPath string
		configs, err := knowledge.ListScopeConfigs(aiDir)
		if err != nil {
			return err
		}

		if len(configs) == 0 || scopeName == "" {
			// Legacy mode
			dbPath = filepath.Join(aiDir, "knowledge.db")
		} else {
			// Load scope config
			cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
			if err != nil {
				return err
			}
			dbPath = filepath.Join(aiDir, cfg.Database)
			fmt.Printf("Stats for scope: %s\n", scopeName)
		}

		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()

		entities, err := store.ListEntities(projectID, "")
		if err != nil {
			return err
		}
		entityCount := len(entities)
		relationCount := 0
		observationCount := 0
		for _, e := range entities {
			rels, _ := store.GetRelations(e.ID, projectID)
			relationCount += len(rels)
			obs, _ := store.GetObservations(e.ID, projectID)
			observationCount += len(obs)
		}
		fmt.Printf("Entities: %d\n", entityCount)
		fmt.Printf("Relations: %d\n", relationCount)
		fmt.Printf("Observations: %d\n", observationCount)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statsCmd)
	statsCmd.Flags().StringVar(&statsScopeName, "scope", "", "Show stats for a specific scope")
}
