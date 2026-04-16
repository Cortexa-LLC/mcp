package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var addEntityType string
var addEntityName string
var addEntitySummary string

var addEntityCmd = &cobra.Command{
	Use:   "entity",
	Short: "Add a new entity",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, projectID, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()

		entity, err := store.CreateEntity(addEntityName, addEntityType, projectID)
		if err != nil {
			return err
		}
		if addEntitySummary != "" {
			obs, err := store.CreateObservation(entity.ID, addEntitySummary, "")
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
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()

		obs, err := store.CreateObservation(args[0], args[1], "")
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
	addCmd.AddCommand(addEntityCmd)
	addCmd.AddCommand(addObservationCmd)
	rootCmd.AddCommand(addCmd)
}
