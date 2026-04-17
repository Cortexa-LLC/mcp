package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

func handleServer(cmd *cobra.Command) {
	useStdio, _ := cmd.Flags().GetBool("stdio")
	if !useStdio {
		fmt.Fprintln(os.Stderr, "kg server: --stdio flag required (MCP mode)")
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kg server: get cwd: %v\n", err)
		os.Exit(1)
	}
	projectRoot := findProjectRoot(cwd)
	aiDir := filepath.Join(projectRoot, ".ai")
	projectID := filepath.Base(projectRoot)

	// Determine which scope to use for the MCP server
	// Priority: default scope > legacy mode (knowledge.db)
	var scopeConfig *knowledge.ScopeConfig
	defaultScope, err := knowledge.GetDefaultScope(aiDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kg server: get default scope: %v\n", err)
		os.Exit(1)
	}

	if defaultScope != "" {
		scopeConfig, err = knowledge.LoadScopeConfig(aiDir, defaultScope)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kg server: load scope %s: %v\n", defaultScope, err)
			os.Exit(1)
		}
	}

	// Each MCP tool call opens the DB, operates, and closes it — no lock is
	// held between calls, so `kg index` and other CLI commands can run freely.
	if err := knowledge.RunMCPServer(aiDir, scopeConfig, projectID, projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "kg server: MCP server error: %v\n", err)
		os.Exit(2)
	}
}
