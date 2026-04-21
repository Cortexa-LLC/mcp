package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/upk/internal/upk"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage project integrations",
	Long:  "Add, remove, and list projects to federate with upk searches",
}

var projectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured projects",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := upk.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		if len(cfg.Projects) == 0 {
			fmt.Println("No projects configured")
			fmt.Println("\nAdd a project with: upk projects add --name <name> --path <path>")
			return
		}

		fmt.Printf("Configured projects (%d):\n\n", len(cfg.Projects))
		for i, proj := range cfg.Projects {
			fmt.Printf("%d. %s\n", i+1, proj.Name)
			fmt.Printf("   Path: %s\n", proj.Path)
			fmt.Printf("   KG:   %s\n", proj.KGDatabase)
			fmt.Println()
		}
	},
}

var projectsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a project to federate",
	Long:  "Add a project with its kg database to enable federated searches",
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		path, _ := cmd.Flags().GetString("path")
		kgDB, _ := cmd.Flags().GetString("kg-db")

		if name == "" {
			fmt.Fprintln(os.Stderr, "Error: --name is required")
			os.Exit(1)
		}
		if path == "" {
			fmt.Fprintln(os.Stderr, "Error: --path is required")
			os.Exit(1)
		}

		// Make path absolute
		absPath, err := filepath.Abs(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
			os.Exit(1)
		}

		// Default kg database location
		if kgDB == "" {
			kgDB = filepath.Join(absPath, ".ai", "knowledge.db")
		} else {
			kgDB, err = filepath.Abs(kgDB)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving kg-db path: %v\n", err)
				os.Exit(1)
			}
		}

		// Check if kg database exists
		if _, err := os.Stat(kgDB); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: kg database not found at %s\n", kgDB)
			fmt.Fprintln(os.Stderr, "Project will be added but may not work until kg is initialized")
		}

		// Load config
		cfg, err := upk.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Check for duplicate
		for _, proj := range cfg.Projects {
			if proj.Name == name {
				fmt.Fprintf(os.Stderr, "Error: project %s already exists\n", name)
				os.Exit(1)
			}
		}

		// Add project
		cfg.Projects = append(cfg.Projects, upk.ProjectConfig{
			Name:       name,
			Path:       absPath,
			KGDatabase: kgDB,
		})

		// Save config
		if err := upk.SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Added project %s\n", name)
		fmt.Printf("  Path: %s\n", absPath)
		fmt.Printf("  KG:   %s\n", kgDB)
	},
}

var projectsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a project",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		cfg, err := upk.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}

		// Find and remove project
		found := false
		newProjects := make([]upk.ProjectConfig, 0, len(cfg.Projects))
		for _, proj := range cfg.Projects {
			if proj.Name == name {
				found = true
				continue
			}
			newProjects = append(newProjects, proj)
		}

		if !found {
			fmt.Fprintf(os.Stderr, "Error: project %s not found\n", name)
			os.Exit(1)
		}

		cfg.Projects = newProjects

		// Save config
		if err := upk.SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Removed project %s\n", name)
	},
}

func init() {
	projectsCmd.AddCommand(projectsListCmd)
	projectsCmd.AddCommand(projectsAddCmd)
	projectsCmd.AddCommand(projectsRemoveCmd)

	// Add flags
	projectsAddCmd.Flags().StringP("name", "n", "", "Project name (required)")
	projectsAddCmd.Flags().StringP("path", "p", "", "Project path (required)")
	projectsAddCmd.Flags().String("kg-db", "", "Path to kg database (default: <path>/.ai/knowledge.db)")
}
