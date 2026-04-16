package main

import (
	"fmt"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search the knowledge graph",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, projectID, err := openStoreRO()
		if err != nil {
			return err
		}
		defer store.Close()

		query := args[0]
		results, err := store.HybridSearch(projectID, query, nil, knowledge.DefaultSearchConfig())
		if err != nil {
			return err
		}
		for _, res := range results {
			fmt.Printf("%s\t%s\t%s\n", res.Entity.ID, res.Entity.Type, res.Entity.Name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
}
