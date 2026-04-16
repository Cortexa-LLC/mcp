package main

import (
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the kg MCP server over stdio (for Claude Code and agent sessions)",
	RunE: func(cmd *cobra.Command, args []string) error {
		handleServer(cmd)
		return nil
	},
}

func init() {
	serverCmd.Flags().Bool("stdio", false, "Enable MCP stdio mode (required)")
	rootCmd.AddCommand(serverCmd)
}
