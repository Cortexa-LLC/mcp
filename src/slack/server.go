package main

import (
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/slack/internal/slack"
	"github.com/spf13/cobra"
)

var (
	configPath string
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the Slack MCP server",
	Long:  "Starts the MCP server over stdio, communicating via JSON-RPC",
	Run: func(cmd *cobra.Command, args []string) {
		// Use default config path if not specified
		if configPath == "" {
			configPath = slack.GetDefaultConfigPath()
		}

		// Load workspace configuration
		config, err := slack.LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config from %s: %v\n", configPath, err)
			fmt.Fprintln(os.Stderr, "\nCreate a config file at ~/.slack-mcp/config.json with:")
			fmt.Fprintln(os.Stderr, `{
  "default_workspace": "my-workspace",
  "workspaces": {
    "my-workspace": {
      "access_token": "xoxe.xoxp-...",
      "refresh_token": "xoxe-...",
      "client_id": "optional",
      "client_secret": "optional"
    }
  }
}`)
			os.Exit(1)
		}

		// Create multi-client for all workspaces
		multiClient := slack.NewMultiClient(config)

		// Run MCP server
		if err := slack.RunMCPServer(multiClient); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	serverCmd.Flags().StringVar(&configPath, "config", "", "Path to config file (default: ~/.slack-mcp/config.json)")
}
