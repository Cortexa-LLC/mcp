package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
Authentication comes from the environment, selected per surface:
  KG_HUB_READ_AUTH    "token" (default) or "oidc"
  KG_HUB_SEED_AUTH    "token" (default) or "oidc"

token mode (shared secrets, the original scheme):
  KG_HUB_READ_TOKEN   bearer token required for reads (unset = open reads)
  KG_HUB_SEED_TOKEN   bearer token required for 'kg push' (unset = seeding disabled)

oidc mode (per-user identity; clients send an access token from the issuer):
  KG_HUB_OIDC_ISSUER    issuer URL; its discovery document supplies the JWKS
  KG_HUB_OIDC_AUDIENCE  required "aud" claim
  KG_HUB_SEED_SUBJECTS  comma-separated subjects/emails allowed to push
                        (unset = any authenticated identity may push)

Mixing modes is expected during migration: KG_HUB_READ_AUTH=oidc with seeding
left on the CI seed token is a valid configuration.`,
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

		readVerifier, readDesc, err := hubVerifier("read")
		if err != nil {
			return err
		}
		seedVerifier, seedDesc, err := hubVerifier("seed")
		if err != nil {
			return err
		}

		srv := hub.NewServerWithAuth(dataDir, readVerifier, seedVerifier, Version)
		fmt.Fprintf(os.Stderr, "kg hub listening on %s (data: %s, reads: %s, seeding: %s)\n",
			hubServeListen, dataDir, readDesc, seedDesc)
		return http.ListenAndServe(hubServeListen, srv.Handler())
	},
}

// oidcVerifierOnce builds the OIDC verifier at most once even when both
// surfaces use it: one discovery fetch, one shared JWKS cache.
var oidcVerifierOnce = sync.OnceValues(func() (*hub.OIDCVerifier, error) {
	return hub.NewOIDCVerifier(context.Background(),
		os.Getenv("KG_HUB_OIDC_ISSUER"), os.Getenv("KG_HUB_OIDC_AUDIENCE"))
})

// hubVerifier resolves the verifier for one auth surface ("read" or "seed")
// from the environment. A nil verifier with nil error means the surface's
// token-mode default: open reads, or seeding disabled.
func hubVerifier(surface string) (hub.Verifier, string, error) {
	envMode, envToken := "KG_HUB_READ_AUTH", "KG_HUB_READ_TOKEN"
	if surface == "seed" {
		envMode, envToken = "KG_HUB_SEED_AUTH", "KG_HUB_SEED_TOKEN"
	}
	switch mode := os.Getenv(envMode); mode {
	case "", "token":
		token := os.Getenv(envToken)
		if token == "" {
			if surface == "read" {
				return nil, "open", nil
			}
			return nil, "disabled", nil
		}
		return hub.NewTokenVerifier(token), "token required", nil
	case "oidc":
		v, err := oidcVerifierOnce()
		if err != nil {
			return nil, "", err
		}
		desc := fmt.Sprintf("oidc (%s)", v.Issuer())
		if surface == "seed" {
			if subjects := splitCommaList(os.Getenv("KG_HUB_SEED_SUBJECTS")); len(subjects) > 0 {
				return hub.RequireSubjects(v, subjects), fmt.Sprintf("%s, %d allowed subject(s)", desc, len(subjects)), nil
			}
		}
		return v, desc, nil
	default:
		return nil, "", fmt.Errorf("%s: unknown auth mode %q (want token or oidc)", envMode, mode)
	}
}

func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
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

var (
	hubStatusHubURL string
	hubStatusScope  string
)

var hubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Compare hub graphs related to this project against local HEAD",
	RunE: func(cmd *cobra.Command, args []string) error {
		hubURL, err := resolveHubURL(hubStatusHubURL)
		if err != nil {
			return err
		}

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		aiDir := filepath.Join(root, ".ai")

		scopeName := hubStatusScope
		if scopeName == "" {
			scopeName, _ = knowledge.GetDefaultScope(aiDir)
		}

		reg, err := hub.ListGraphs(hubURL, os.Getenv("KG_HUB_READ_TOKEN"))
		if err != nil {
			return err
		}

		// Graphs to report: the resolved scope's remotes, every local scope
		// name the hub also hosts, and (legacy mode) this project's ID.
		seen := map[string]bool{}
		var names []string
		add := func(name string) {
			if name == "" || seen[name] {
				return
			}
			seen[name] = true
			if _, ok := reg.Graphs[name]; ok {
				names = append(names, name)
			}
		}
		if scopeName != "" {
			scopeCfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
			if err != nil {
				// An explicitly requested scope that fails to load is an
				// error; a broken default scope only loses its remotes.
				if hubStatusScope != "" {
					return fmt.Errorf("load scope %s: %w", scopeName, err)
				}
			} else {
				for _, r := range scopeCfg.Remotes {
					add(r)
				}
			}
		}
		if scopes, err := knowledge.ListScopeConfigs(aiDir); err == nil {
			for _, sc := range scopes {
				add(sc.Name)
			}
		}
		add(projectIDFromCwd(cwd))
		sort.Strings(names)

		if len(names) == 0 {
			fmt.Println("No hub graphs relate to this project. Graphs on the hub:")
			all := make([]string, 0, len(reg.Graphs))
			for name := range reg.Graphs {
				all = append(all, name)
			}
			sort.Strings(all)
			for _, name := range all {
				fmt.Printf("  %s\n", name)
			}
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCOMMIT\tINDEXED\tSTATUS")
		for _, name := range names {
			info := reg.Graphs[name]
			commit := info.Commit
			if len(commit) > 12 {
				commit = commit[:12]
			}
			if info.Dirty {
				commit += " (dirty)"
			}
			var status string
			if info.Commit == "" {
				// rev-list with an empty left side would compare HEAD..HEAD
				// and misreport "up to date" — an unstamped push is unknown.
				status = "unknown (graph pushed without a commit)"
			} else {
				switch n, err := knowledge.GitAheadCount(root, info.Commit); {
				case err != nil:
					status = "not in local history (different repo?)"
				case n == 0:
					status = "up to date with local HEAD"
				default:
					status = fmt.Sprintf("hub is %d commit(s) behind local HEAD", n)
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				name, commit,
				info.IndexedAt.Local().Format("2006-01-02 15:04"),
				status)
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
		if suggested := knowledge.RepoSuggestedHubURL(aiDir); suggested != "" {
			return "", fmt.Errorf("no trusted hub configured. This project names %q, but a hub named by "+
				"a repository is not trusted — it would decide where your KG_HUB_READ_TOKEN is sent. "+
				"If you recognise it: kg config set-hub %s", suggested, suggested)
		}
		return "", fmt.Errorf("no hub configured: pass --hub, run 'kg config set-hub <url>', or set KG_HUB_URL")
	}
	return hubURL, nil
}

func init() {
	rootCmd.AddCommand(hubCmd)
	hubCmd.AddCommand(hubServeCmd)
	hubCmd.AddCommand(hubListCmd)
	hubCmd.AddCommand(hubStatusCmd)

	hubServeCmd.Flags().StringVar(&hubServeListen, "listen", ":7411", "Address to listen on")
	hubServeCmd.Flags().StringVar(&hubServeData, "data", "", "Data directory (default: $KG_HUB_HOME, else ~/.kg-hub)")

	hubListCmd.Flags().StringVar(&hubListHubURL, "hub", "", "Hub URL (default: \"hub\" key in .ai/config.json)")

	hubStatusCmd.Flags().StringVar(&hubStatusHubURL, "hub", "", "Hub URL (default: \"hub\" key in .ai/config.json)")
	hubStatusCmd.Flags().StringVar(&hubStatusScope, "scope", "", "Scope whose remotes to report (default: the default scope)")
}
