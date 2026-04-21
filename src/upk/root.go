package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "upk",
	Short: "Unified Personal Knowledge - User-global knowledge graph",
	Long: `upk (Unified Personal Knowledge) manages a user-global knowledge graph
that stores conversations, learnings, insights, and their relationships to projects.

It can federate searches across your personal knowledge and multiple project
knowledge graphs created by the kg MCP server.`,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(searchCmd)
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
		fmt.Printf("upk version %s\n", ver)
		fmt.Printf("Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Go:        %s\n", runtime.Version())
	},
}
