package main

import (
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/slack/internal/slack"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the Slack MCP server",
	Long:  "Starts the MCP server over stdio, communicating via JSON-RPC",
	Run: func(cmd *cobra.Command, args []string) {
		// Get tokens from environment
		accessToken := os.Getenv("SLACK_ACCESS_TOKEN")
		refreshToken := os.Getenv("SLACK_REFRESH_TOKEN")
		clientID := os.Getenv("SLACK_CLIENT_ID")
		clientSecret := os.Getenv("SLACK_CLIENT_SECRET")

		if accessToken == "" {
			fmt.Fprintln(os.Stderr, "Error: SLACK_ACCESS_TOKEN environment variable is required")
			fmt.Fprintln(os.Stderr, "Set it to your Slack user token (xoxe.xoxp-...)")
			os.Exit(1)
		}

		// Create Slack client with token management
		client := slack.NewClient(accessToken, refreshToken, clientID, clientSecret)

		// Run MCP server
		if err := slack.RunMCPServer(client); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	serverCmd.Flags().String("stdio", "", "Run in stdio mode (default)")
}
