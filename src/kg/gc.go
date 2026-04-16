package main

import (
	"github.com/spf13/cobra"
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Remove orphaned or unreferenced nodes, observations, or relations",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Println("Not yet implemented: gc command")
		return nil
	},
}

func init() {
	// placeholder
}
