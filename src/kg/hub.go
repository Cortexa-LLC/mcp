package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/cortexa-llc/mcp/kg/internal/hub"
	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var hubCmd = &cobra.Command{
	Use:   "hub",
	Short: "Run or inspect a shared knowledge hub",
}

var (
	hubServeListen string
	hubServeData   string
)

var hubServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve read-only knowledge graphs over HTTP",
	Long: `Serve read-only knowledge graphs over HTTP.

Graphs are seeded with 'kg push' and stored under the data directory.
Tokens come from the environment:
  KG_HUB_READ_TOKEN   bearer token required for reads (unset = open reads)
  KG_HUB_SEED_TOKEN   bearer token required for 'kg push' (unset = seeding disabled)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir := hubServeData
		if dataDir == "" {
			dataDir = os.Getenv("KG_HUB_HOME")
		}
		if dataDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}
			dataDir = filepath.Join(home, ".kg-hub")
		}
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}

		srv := hub.NewServer(dataDir, os.Getenv("KG_HUB_READ_TOKEN"), os.Getenv("KG_HUB_SEED_TOKEN"), Version)
		readAuth := "open"
		if srv.ReadAuthEnabled() {
			readAuth = "token required"
		}
		seeding := "disabled"
		if srv.SeedingEnabled() {
			seeding = "enabled"
		}
		fmt.Fprintf(os.Stderr, "kg hub listening on %s (data: %s, reads: %s, seeding: %s)\n",
			hubServeListen, dataDir, readAuth, seeding)
		return http.ListenAndServe(hubServeListen, srv.Handler())
	},
}

var hubListHubURL string

var hubListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the graphs hosted on a hub",
	RunE: func(cmd *cobra.Command, args []string) error {
		hubURL, err := resolveHubURL(hubListHubURL)
		if err != nil {
			return err
		}
		reg, err := hub.ListGraphs(hubURL, os.Getenv("KG_HUB_READ_TOKEN"))
		if err != nil {
			return err
		}

		names := make([]string, 0, len(reg.Graphs))
		for name := range reg.Graphs {
			names = append(names, name)
		}
		sort.Strings(names)

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCOMMIT\tINDEXED\tLAYERS\tPROJECT")
		for _, name := range names {
			info := reg.Graphs[name]
			commit := info.Commit
			if len(commit) > 12 {
				commit = commit[:12]
			}
			if info.Dirty {
				commit += " (dirty)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				name, commit,
				info.IndexedAt.Local().Format("2006-01-02 15:04"),
				strings.Join(info.Layers, ","),
				info.ProjectID)
		}
		return w.Flush()
	},
}

// resolveHubURL returns the hub URL from the flag, falling back to the "hub"
// key in .ai/config.json.
func resolveHubURL(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	aiDir := filepath.Join(findProjectRoot(cwd), ".ai")
	hubURL, err := knowledge.GetHubURL(aiDir)
	if err != nil {
		return "", err
	}
	if hubURL == "" {
		return "", fmt.Errorf("no hub configured: pass --hub or set \"hub\" in .ai/config.json")
	}
	return hubURL, nil
}

func init() {
	rootCmd.AddCommand(hubCmd)
	hubCmd.AddCommand(hubServeCmd)
	hubCmd.AddCommand(hubListCmd)

	hubServeCmd.Flags().StringVar(&hubServeListen, "listen", ":7411", "Address to listen on")
	hubServeCmd.Flags().StringVar(&hubServeData, "data", "", "Data directory (default: $KG_HUB_HOME, else ~/.kg-hub)")

	hubListCmd.Flags().StringVar(&hubListHubURL, "hub", "", "Hub URL (default: \"hub\" key in .ai/config.json)")
}
