package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show entity with details (relations, observations)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, projectID, err := openStoreRO()
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
}
