package main

import (
	"github.com/spf13/cobra"
)

var embedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Generate and attach vector embeddings for entities and observations",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Println("Not yet implemented: embed command")
		return nil
	},
}

func init() {
	// placeholder
}
