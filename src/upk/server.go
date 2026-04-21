package main

import (
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/upk/internal/upk"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the upk MCP server",
	Run: func(cmd *cobra.Command, args []string) {
		useStdio, _ := cmd.Flags().GetBool("stdio")
		if !useStdio {
			fmt.Fprintln(os.Stderr, "upk server: --stdio flag required (MCP mode)")
			os.Exit(1)
		}

		// Load config
		cfg, err := upk.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "upk server: failed to load config: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run 'upk init' first to initialize upk")
			os.Exit(1)
		}

		// Get user DB path
		dbPath, err := upk.GetUserDBPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "upk server: %v\n", err)
			os.Exit(1)
		}

		// Run MCP server
		if err := upk.RunMCPServer(dbPath, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "upk server: %v\n", err)
			os.Exit(2)
		}
	},
}

func init() {
	serverCmd.Flags().Bool("stdio", false, "Run in stdio mode (required for MCP)")
}
