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
	// Commands without their own init() registration:
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(gcCmd)
	rootCmd.AddCommand(embedCmd)
	rootCmd.AddCommand(graphCmd)
	rootCmd.AddCommand(perfCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		ver := kgVersion
		if kgBuildTime != "unknown" {
			ver = fmt.Sprintf("%s built %s", kgVersion, kgBuildTime)
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

	// Set version and custom template for --version flag
	rootCmd.Version = version
	if kgBuildTime != "unknown" {
		rootCmd.Version = fmt.Sprintf("%s built %s", version, kgBuildTime)
	}
	rootCmd.SetVersionTemplate(fmt.Sprintf("kg version {{.Version}}\nPlatform:  %s/%s\nGo:        %s\n",
		runtime.GOOS, runtime.GOARCH, runtime.Version()))

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
