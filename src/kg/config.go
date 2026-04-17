package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage knowledge graph configuration",
}

var configSetDefaultCmd = &cobra.Command{
	Use:   "set-default-scope <scope-name>",
	Short: "Set the default scope for kg commands",
	Long: `Set the default scope that will be used by kg commands when no --scope flag is provided.

Examples:
  kg config set-default-scope selling    # Use selling scope by default
  kg config set-default-scope ""         # Clear default (index all scopes)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")

		scopeName := args[0]

		// Validate scope exists if not empty
		if scopeName != "" {
			if _, err := knowledge.LoadScopeConfig(aiDir, scopeName); err != nil {
				return fmt.Errorf("scope '%s' not found: %w", scopeName, err)
			}
		}

		if err := knowledge.SetDefaultScope(aiDir, scopeName); err != nil {
			return err
		}

		if scopeName == "" {
			fmt.Println("✅ Default scope cleared (will index all scopes)")
		} else {
			fmt.Printf("✅ Default scope set to: %s\n", scopeName)
		}
		return nil
	},
}

var configListScopesCmd = &cobra.Command{
	Use:   "list-scopes",
	Short: "List all defined scopes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")

		configs, err := knowledge.ListScopeConfigs(aiDir)
		if err != nil {
			return err
		}

		if len(configs) == 0 {
			fmt.Println("No scopes defined (using legacy single-DB mode)")
			return nil
		}

		defaultScope, err := knowledge.GetDefaultScope(aiDir)
		if err != nil {
			return err
		}

		fmt.Println("Defined scopes:")
		for _, cfg := range configs {
			marker := " "
			if cfg.Name == defaultScope {
				marker = "*"
			}
			fmt.Printf("  %s %s (database: %s)\n", marker, cfg.Name, cfg.Database)
			if len(cfg.Layers) > 0 {
				fmt.Printf("    layers: %v\n", cfg.Layers)
			}
			if len(cfg.IncludeModules) > 0 {
				fmt.Printf("    modules: %v\n", cfg.IncludeModules)
			}
		}

		if defaultScope != "" {
			fmt.Printf("\n* = default scope\n")
		} else {
			fmt.Println("\nNo default scope set (will index all scopes)")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configSetDefaultCmd)
	configCmd.AddCommand(configListScopesCmd)
}
