package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var addEntityType string
var addEntityName string
var addEntitySummary string
var addScopeName string

var addEntityCmd = &cobra.Command{
	Use:   "entity",
	Short: "Add a new entity",
	Long:  `Add a new entity to the knowledge graph. Writes to the default scope unless --scope is specified.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")

		scopeName := addScopeName
		if scopeName == "" {
			defaultScope, _ := knowledge.GetDefaultScope(aiDir)
			scopeName = defaultScope
		}

		store, projectID, err := openStoreModeWithScope(false, scopeName)
		if err != nil {
			return err
		}
		defer store.Close()

		entity, err := store.CreateEntity(addEntityName, addEntityType, projectID)
		if err != nil {
			return err
		}
		if addEntitySummary != "" {
			obs, err := store.CreateObservation(entity.ID, addEntitySummary, projectID)
			if err != nil {
				return err
			}
			fmt.Printf("Entity %s created with summary observation: %s\n", entity.ID, obs.ID)
		} else {
			fmt.Printf("Entity %s created.\n", entity.ID)
		}
		return nil
	},
}

var addObservationCmd = &cobra.Command{
	Use:   "observation <entity-id> <content>",
	Short: "Add an observation to an entity",
	Long:  `Add an observation to an entity. Writes to the default scope unless --scope is specified.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")

		scopeName := addScopeName
		if scopeName == "" {
			defaultScope, _ := knowledge.GetDefaultScope(aiDir)
			scopeName = defaultScope
		}

		store, projectID, err := openStoreModeWithScope(false, scopeName)
		if err != nil {
			return err
		}
		defer store.Close()

		obs, err := store.CreateObservation(args[0], args[1], projectID)
		if err != nil {
			return err
		}
		fmt.Printf("Added observation %s\n", obs.ID)
		return nil
	},
}

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add entity or observation",
}

func init() {
	addEntityCmd.Flags().StringVar(&addEntityType, "type", "concept", "Entity type")
	addEntityCmd.Flags().StringVar(&addEntityName, "name", "", "Entity name")
	addEntityCmd.Flags().StringVar(&addEntitySummary, "summary", "", "Entity summary")
	addEntityCmd.MarkFlagRequired("name")
	addEntityCmd.MarkFlagRequired("type")

	addCmd.PersistentFlags().StringVar(&addScopeName, "scope", "", "Scope to write to (default: default scope)")

	addCmd.AddCommand(addEntityCmd)
	addCmd.AddCommand(addObservationCmd)
	rootCmd.AddCommand(addCmd)
}
