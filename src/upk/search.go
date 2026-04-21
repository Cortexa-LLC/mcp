package main

import (
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/kglib"
	"github.com/cortexa-llc/mcp/upk/internal/upk"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search user knowledge",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := args[0]
		limit, _ := cmd.Flags().GetInt("limit")

		// Open user store
		dbPath, err := upk.GetUserDBPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		store, err := kglib.OpenStoreReadOnly(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run 'upk init' first to initialize the database")
			os.Exit(1)
		}
		defer store.Close()

		// Perform search
		results, err := store.KeywordSearch("user", query, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error searching: %v\n", err)
			os.Exit(1)
		}

		// Display results
		if len(results) == 0 {
			fmt.Println("No results found")
			return
		}

		fmt.Printf("Found %d results:\n\n", len(results))
		for i, result := range results {
			fmt.Printf("%d. %s (%s) [score: %.2f]\n", i+1, result.Entity.Name, result.Entity.Type, result.Score)
			if len(result.Observations) > 0 {
				for _, obs := range result.Observations {
					fmt.Printf("   - %s\n", obs.Content)
				}
			}
			fmt.Println()
		}
	},
}

func init() {
	searchCmd.Flags().IntP("limit", "l", 10, "Maximum number of results")
}
