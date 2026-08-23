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

var configSetHubCmd = &cobra.Command{
	Use:   "set-hub <url>",
	Short: "Trust a shared knowledge hub for this user",
	Long: `Record which shared knowledge hub this user trusts.

Stored per user (in $KG_HOME/config.json, default ~/.kg/config.json), not in the
repository — deliberately. Federated search sends your KG_HUB_READ_TOKEN to the
hub as a bearer token, so whoever chooses the URL chooses where that credential
goes. A cloned repository must not be able to make that choice.

A project can still say which graphs to federate, through a scope's "remotes".
Naming graphs on a hub you already trust carries no such authority.

Examples:
  kg config set-hub https://kg.internal:7411
  kg config set-hub ""                       # stop trusting any hub`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := knowledge.SetUserHubURL(args[0])
		if err != nil {
			return err
		}
		if args[0] == "" {
			cmd.Printf("Cleared the trusted hub in %s\n", path)
			return nil
		}
		cmd.Printf("Trusting hub %s (recorded in %s)\n", args[0], path)
		return nil
	},
}

var configShowHubCmd = &cobra.Command{
	Use:   "show-hub",
	Short: "Show the trusted hub, and any this project asks for",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		aiDir := filepath.Join(findProjectRoot(cwd), ".ai")

		trusted, err := knowledge.GetHubURL(aiDir)
		if err != nil {
			return err
		}
		if trusted == "" {
			cmd.Println("Trusted hub: none (set one with 'kg config set-hub <url>')")
		} else {
			cmd.Printf("Trusted hub: %s\n", trusted)
		}

		if suggested := knowledge.RepoSuggestedHubURL(aiDir); suggested != "" && suggested != trusted {
			cmd.Printf("\nThis project names %s in .ai/config.json, which is NOT used.\n", suggested)
			cmd.Printf("A hub named by a repository would decide where your KG_HUB_READ_TOKEN is sent.\n")
			cmd.Printf("If you recognise it: kg config set-hub %s\n", suggested)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configSetDefaultCmd)
	configCmd.AddCommand(configListScopesCmd)
	configCmd.AddCommand(configSetHubCmd)
	configCmd.AddCommand(configShowHubCmd)
}
