package main

import (
	"github.com/spf13/cobra"
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Output the current knowledge graph in GraphML or other formats",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Not yet implemented
		cmd.Println("Not yet implemented: graph export")
		return nil
	},
}

func init() {
	// This is a placeholder for future implementation
}
