package main

import (
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the knowledge graph to GraphML, JSON, or other supported formats",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Println("Not yet implemented: export command")
		return nil
	},
}

func init() {
	// placeholder
}
