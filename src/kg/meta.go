package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var metaScopeName string

var metaCmd = &cobra.Command{
	Use:   "meta",
	Short: "Show the provenance stamp of an indexed database",
	Long: `Show the provenance stamp recorded by kg index: which repository and
commit the database was built from, when, and with which embedding model
configured — plus a staleness check against the local HEAD.

By default, shows the default scope. Use --scope to specify a different scope.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")
		projectID := projectIDFromCwd(cwd)

		// Determine which scope to use
		scopeName := metaScopeName
		if scopeName == "" {
			defaultScope, err := knowledge.GetDefaultScope(aiDir)
			if err != nil {
				return err
			}
			scopeName = defaultScope
		}

		// Resolve the database path
		var dbPath string
		configs, err := knowledge.ListScopeConfigs(aiDir)
		if err != nil {
			return err
		}

		if len(configs) == 0 || scopeName == "" {
			// Legacy mode
			dbPath = filepath.Join(aiDir, "knowledge.db")
		} else {
			cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
			if err != nil {
				return err
			}
			dbPath = filepath.Join(aiDir, cfg.Database)
		}

		store, err := knowledge.OpenStoreReadOnly(dbPath)
		if err != nil {
			return fmt.Errorf("open %s: %w (run `kg index` first to create the database)", dbPath, err)
		}
		defer store.Close()

		meta, err := store.GetMeta(projectID)
		if err != nil {
			return err
		}
		if meta == nil {
			fmt.Println("No provenance stamp found — run `kg index` to record one.")
			return nil
		}

		repo := meta.RepoURL
		if repo == "" {
			repo = "(none)"
		}
		commit := meta.Commit
		if meta.Dirty {
			commit += " (dirty)"
		}
		embedModel := meta.EmbedModel
		if embedModel == "" {
			embedModel = "(not set)"
		}

		fmt.Printf("Project:     %s\n", meta.ProjectID)
		fmt.Printf("Database:    %s\n", dbPath)
		fmt.Printf("Repo:        %s\n", repo)
		fmt.Printf("Commit:      %s\n", commit)
		fmt.Printf("Indexed at:  %s\n", meta.IndexedAt.Local().Format("2006-01-02 15:04:05 MST"))
		fmt.Printf("Embed model: %s\n", embedModel)
		fmt.Printf("kg version:  %s\n", meta.KGVersion)

		if meta.Commit != "" {
			if n, err := knowledge.GitAheadCount(root, meta.Commit); err != nil {
				fmt.Println("Staleness:   indexed commit not found in local history")
			} else if n == 0 {
				fmt.Println("Staleness:   up to date with local HEAD")
			} else {
				fmt.Printf("Staleness:   local HEAD is %d commit(s) ahead of the indexed commit\n", n)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(metaCmd)
	metaCmd.Flags().StringVar(&metaScopeName, "scope", "", "Show the stamp for a specific scope")
}
