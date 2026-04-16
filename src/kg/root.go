package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// version vars are set by main.go (injected via ldflags at build time).
var (
	kgVersion   string
	kgCommit    string
	kgBuildTime string
)

var rootCmd = &cobra.Command{
	Use:   "kg",
	Short: "Knowledge graph CLI for Kuzu store",
	Long:  "The kg CLI provides commands to interact with a Kuzu-backed knowledge graph.",
}

func init() {
	rootCmd.AddCommand(addEntityCmd)
	addCmd.AddCommand(addObservationCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(linkCmd)
	rootCmd.AddCommand(indexCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(gcCmd)
	rootCmd.AddCommand(embedCmd)
	rootCmd.AddCommand(graphCmd)
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(perfCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		ver := kgVersion
		if kgCommit != "unknown" && len(kgCommit) >= 7 {
			ver = fmt.Sprintf("%s (%s) built %s", kgVersion, kgCommit[:7], kgBuildTime)
		}
		fmt.Printf("kg version %s\n", ver)
		fmt.Printf("Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
		fmt.Printf("Go:        %s\n", runtime.Version())
	},
}

func Execute(version, commit, buildTime string) {
	kgVersion = version
	kgCommit = commit
	kgBuildTime = buildTime
	rootCmd.Version = version

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
