package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize upk user directory at ~/.upk",
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot determine home directory: %v\n", err)
			os.Exit(1)
		}

		upkDir := filepath.Join(home, ".upk")

		// Create .upk directory
		if err := os.MkdirAll(upkDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot create %s: %v\n", upkDir, err)
			os.Exit(1)
		}

		// Create default config.json if it doesn't exist
		configPath := filepath.Join(upkDir, "config.json")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			defaultConfig := map[string]interface{}{
				"defaultEmbedModel": "text-embedding-3-small",
				"projects":          []interface{}{},
			}

			data, err := json.MarshalIndent(defaultConfig, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot marshal config: %v\n", err)
				os.Exit(1)
			}

			if err := os.WriteFile(configPath, data, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot write %s: %v\n", configPath, err)
				os.Exit(1)
			}

			fmt.Printf("✅ Created %s\n", configPath)
		} else {
			fmt.Printf("⏭️  Config already exists: %s\n", configPath)
		}

		// Database will be created automatically when first used
		dbPath := filepath.Join(upkDir, "knowledge.db")
		fmt.Printf("📁 User knowledge directory: %s\n", upkDir)
		fmt.Printf("🗄️  Database will be created at: %s\n", dbPath)
		fmt.Println()
		fmt.Println("Next steps:")
		fmt.Println("  1. Add projects: upk projects add --name <name> --path <path>")
		fmt.Println("  2. Start MCP server: upk server --stdio")
		fmt.Println("  3. Add to Claude settings.json:")
		fmt.Println(`     { "mcpServers": { "upk": { "command": "upk", "args": ["server", "--stdio"] } } }`)
	},
}
