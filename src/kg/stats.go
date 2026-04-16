package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show entity, relation, and observation statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, projectID, err := openStoreRO()
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
}
