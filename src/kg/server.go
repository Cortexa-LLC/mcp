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
	serverCmd.Flags().Bool("personal-writes", false,
		"Allow agents to record entries in the personal knowledge store (also settable with KG_PERSONAL_WRITES=1)")
	rootCmd.AddCommand(serverCmd)
}
