package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "slack",
	Short: "Slack MCP server - Read Slack conversations via MCP protocol",
	Long: `The Slack MCP server provides tools to read Slack channels, threads, and messages.
It supports user tokens with OAuth refresh for seamless authentication.`,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		ver := Version
		if BuildTime != "unknown" {
			ver = fmt.Sprintf("%s built %s", Version, BuildTime)
		}
		fmt.Printf("slack version %s\n", ver)
		fmt.Printf("Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Go:        %s\n", runtime.Version())
	},
}
