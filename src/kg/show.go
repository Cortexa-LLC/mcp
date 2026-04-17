package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var showScopeName string

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show entity with details (relations, observations)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")
		projectID := projectIDFromCwd(cwd)

		// Determine which scope to use
		scopeName := showScopeName
		if scopeName == "" {
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
			dbPath = filepath.Join(aiDir, "knowledge.db")
		} else {
			cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
			if err != nil {
				return err
			}
			dbPath = filepath.Join(aiDir, cfg.Database)
		}

		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()

		id := args[0]
		entity, err := store.GetEntity(id, projectID)
		if err != nil {
			return err
		}
		relations, _ := store.GetRelations(id, projectID)
		observations, _ := store.GetObservations(id, projectID)
		fmt.Printf("Entity: %s\nType: %s\nID: %s\n", entity.Name, entity.Type, entity.ID)
		fmt.Println("Relations:")
		for _, r := range relations {
			fmt.Printf("  %s --%s--> %s\n", r.FromID, r.Type, r.ToID)
		}
		fmt.Println("Observations:")
		for _, o := range observations {
			fmt.Printf("  - %s\n", o.Content)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
	showCmd.Flags().StringVar(&showScopeName, "scope", "", "Show entity from a specific scope")
}
