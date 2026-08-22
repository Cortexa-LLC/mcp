package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/hub"
	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/cortexa-llc/mcp/kglib"
	"github.com/spf13/cobra"
)

var (
	pushHubURL     string
	pushScopeName  string
	pushAllScopes  bool
	pushGraphName  string
	pushCommit     string
	pushAllowDirty bool
)

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push knowledge graph databases to a shared hub",
	Long: `Push already-indexed knowledge graph databases to a shared hub.

Each pushed graph carries its provenance stamp (git commit, repo URL, dirty
flag) and its scope's layer topology so the hub can answer federated queries.
Requires KG_HUB_SEED_TOKEN in the environment.

Examples:
  kg push --hub http://hub.internal:7411              # push default scope
  kg push --all-scopes                                # push every scope (hub from .ai/config.json)
  kg push --scope platform --graph monorepo-platform  # rename the graph on the hub`,
	RunE: func(cmd *cobra.Command, args []string) error {
		seedToken := os.Getenv("KG_HUB_SEED_TOKEN")
		if seedToken == "" {
			return fmt.Errorf("KG_HUB_SEED_TOKEN is not set; the hub requires it for seeding")
		}
		hubURL, err := resolveHubURL(pushHubURL)
		if err != nil {
			return err
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")
		projectID := projectIDFromCwd(root)

		scopes, err := determineScopesToIndex(aiDir, pushScopeName, pushAllScopes)
		if err != nil {
			return err
		}
		if pushGraphName != "" && len(scopes) > 1 {
			return fmt.Errorf("--graph is only valid when pushing exactly one database (selected %d scopes)", len(scopes))
		}

		for _, scopeName := range scopes {
			if err := pushScopeDB(root, aiDir, hubURL, seedToken, projectID, scopeName); err != nil {
				return err
			}
		}
		fmt.Printf("✅ Pushed %d graph(s)\n", len(scopes))
		return nil
	},
}

func pushScopeDB(root, aiDir, hubURL, seedToken, projectID, scopeName string) error {
	var dbPath, graph string
	var layers []string
	if scopeName != "" {
		cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
		if err != nil {
			return err
		}
		dbPath = filepath.Join(aiDir, cfg.Database)
		graph = cfg.Name
		layers = cfg.Layers
	} else {
		dbPath = filepath.Join(aiDir, "knowledge.db")
		graph = projectID
	}
	if pushGraphName != "" {
		graph = pushGraphName
	}

	meta, err := loadPushMeta(dbPath, root, projectID)
	if err != nil {
		return fmt.Errorf("push %s: %w", graph, err)
	}
	if pushCommit != "" {
		meta.Commit = pushCommit
	}
	if meta.Commit == "" {
		return fmt.Errorf("push %s: no commit available (no provenance stamp and not a git repository); pass --commit", graph)
	}
	if meta.Dirty && !pushAllowDirty {
		return fmt.Errorf("refusing to push a dirty-tree database for %s; commit your changes and re-run kg index, or pass --allow-dirty", graph)
	}

	short := meta.Commit
	if len(short) > 12 {
		short = short[:12]
	}
	dirty := ""
	if meta.Dirty {
		dirty = " (dirty)"
	}
	fmt.Printf("⬆️  %s @ %s%s → %s\n", graph, short, dirty, hubURL)

	return hub.Push(hub.PushRequest{
		HubURL:    hubURL,
		Graph:     graph,
		DBPath:    dbPath,
		SeedToken: seedToken,
		Meta:      *meta,
		Layers:    layers,
	})
}

// loadPushMeta reads the provenance stamp from the database, falling back to
// current git state when the database was indexed before stamping existed.
// The store is closed before returning so no handle is open while packing.
func loadPushMeta(dbPath, root, projectID string) (*kglib.KGMeta, error) {
	store, err := knowledge.OpenStoreReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", dbPath, err)
	}
	meta, err := store.GetMeta(projectID)
	if cerr := store.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close database: %w", cerr)
	}
	if err != nil {
		return nil, err
	}
	if meta == nil {
		fmt.Printf("Warning: %s has no provenance stamp; using current HEAD (re-run kg index to stamp)\n", dbPath)
		commit, repoURL, dirty := knowledge.GitProvenance(root)
		meta = &kglib.KGMeta{
			ProjectID: projectID,
			Commit:    commit,
			RepoURL:   repoURL,
			Dirty:     dirty,
		}
	}
	if meta.KGVersion == "" {
		meta.KGVersion = Version
	}
	return meta, nil
}

func init() {
	rootCmd.AddCommand(pushCmd)
	pushCmd.Flags().StringVar(&pushHubURL, "hub", "", "Hub URL (default: \"hub\" key in .ai/config.json)")
	pushCmd.Flags().StringVar(&pushScopeName, "scope", "", "Push a specific scope's database")
	pushCmd.Flags().BoolVar(&pushAllScopes, "all-scopes", false, "Push every defined scope")
	pushCmd.Flags().StringVar(&pushGraphName, "graph", "", "Override the graph name on the hub (single database only)")
	pushCmd.Flags().StringVar(&pushCommit, "commit", "", "Override the commit recorded for this push")
	pushCmd.Flags().BoolVar(&pushAllowDirty, "allow-dirty", false, "Allow pushing a database indexed from a dirty working tree")
}
