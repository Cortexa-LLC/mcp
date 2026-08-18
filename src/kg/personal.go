package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

// The personal knowledge store is a single user-level graph, separate from any
// project's `.ai/knowledge.db`. It holds the knowledge that follows you between
// repositories — conversations, decisions, learnings, people — and is reachable
// from anywhere on disk rather than only from inside a project tree.
const (
	// personalProjectID scopes every entity in the personal store. It is fixed so
	// that a project search can federate the personal graph in as a layer via
	// LayerConfig.ProjectID, whatever the project's own ID happens to be.
	personalProjectID = "personal"

	// personalDirEnv overrides the location of the personal store, mainly for
	// tests and for keeping it on synced storage.
	personalDirEnv = "KG_HOME"
)

// usePersonal is set by the --personal flag on commands that support it.
var usePersonal bool

// withPersonal is set by --with-personal on `kg search`.
var withPersonal bool

// registerPersonalFlag adds --personal to commands that can act on the personal store.
func registerPersonalFlag(cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		cmd.Flags().BoolVar(&usePersonal, "personal", false,
			"Act on your personal knowledge store (shared across all projects) instead of this project's graph")
	}
}

// personalDir returns the directory holding the personal store: $KG_HOME, or ~/.ai.
func personalDir() (string, error) {
	if dir := os.Getenv(personalDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory (set %s to override): %w", personalDirEnv, err)
	}
	return filepath.Join(home, ".ai"), nil
}

// personalDBPath returns the path to the personal store's database.
func personalDBPath() (string, error) {
	dir, err := personalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "knowledge.db"), nil
}

// personalStoreExists reports whether the personal store has been created.
// Read-only opens cannot create a database, so callers that federate it in as a
// layer must check first rather than surfacing a misleading "locked" error.
func personalStoreExists() bool {
	path, err := personalDBPath()
	if err != nil {
		return false
	}
	_, statErr := os.Stat(path)
	return statErr == nil
}

// openPersonalStore opens the personal store. Write mode creates it on first use,
// so `kg add entity --personal` works without an explicit init step.
func openPersonalStore(readOnly bool) (*knowledge.Store, string, error) {
	dbPath, err := personalDBPath()
	if err != nil {
		return nil, "", err
	}

	if readOnly && !personalStoreExists() {
		return nil, "", fmt.Errorf("no personal knowledge store at %s yet — create it with 'kg personal init'", dbPath)
	}

	var store *knowledge.Store
	if readOnly {
		store, err = knowledge.OpenStoreReadOnly(dbPath)
	} else {
		store, err = knowledge.OpenStore(dbPath)
	}
	if err != nil {
		return nil, "", err
	}
	return store, personalProjectID, nil
}

// personalLayer returns the personal store as a lowest-priority read-only layer,
// or nil if it does not exist yet. Project knowledge outranks it on conflicts,
// and ProjectID pins the layer to the personal graph's own scope.
func personalLayer() (*knowledge.LayerConfig, error) {
	if !personalStoreExists() {
		return nil, nil
	}
	dbPath, err := personalDBPath()
	if err != nil {
		return nil, err
	}
	store, err := knowledge.OpenStoreReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open personal store: %w", err)
	}
	return &knowledge.LayerConfig{
		Name:      "personal",
		Store:     store,
		Priority:  0,
		ProjectID: personalProjectID,
	}, nil
}

var personalCmd = &cobra.Command{
	Use:   "personal",
	Short: "Manage your personal knowledge store (shared across all projects)",
	Long: `Manage the personal knowledge store — one graph per user, held outside any
project, for knowledge that follows you between repositories.

Most commands accept --personal to act on it:

  kg personal init
  kg add entity --personal --name "kafka-retention-decision" --type decision \
    --summary "[DECISION] 7-day retention: replay window vs storage cost"
  kg search "retention" --personal
  kg search "retention" --with-personal    # this project's graph plus personal

Location: $KG_HOME if set, otherwise ~/.ai/knowledge.db.`,
}

var personalInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create the personal knowledge store",
	Long: `Create the personal knowledge store and its schema. Safe to re-run — an
existing store is left untouched.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath, err := personalDBPath()
		if err != nil {
			return err
		}

		existed := personalStoreExists()

		// Opening read-write creates the directory, database, and schema.
		store, _, err := openPersonalStore(false)
		if err != nil {
			return err
		}
		defer store.Close()

		if existed {
			cmd.Printf("Personal knowledge store already exists: %s\n", dbPath)
		} else {
			cmd.Printf("✅ Created personal knowledge store: %s\n", dbPath)
		}

		cmd.Println()
		cmd.Println("Record something:")
		cmd.Println(`  kg add entity --personal --name "<title>" --type conversation --summary "<text>"`)
		cmd.Println()
		cmd.Println("Read it back:")
		cmd.Println(`  kg search "<query>" --personal          # personal store only`)
		cmd.Println(`  kg search "<query>" --with-personal     # this project's graph plus personal`)
		return nil
	},
}

var personalPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the personal knowledge store's database path",
	Long: `Print the path to the personal store's database. Useful in scripts and
skills that need to check whether it exists before writing to it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath, err := personalDBPath()
		if err != nil {
			return err
		}
		cmd.Println(dbPath)
		return nil
	},
}

func init() {
	personalCmd.AddCommand(personalInitCmd)
	personalCmd.AddCommand(personalPathCmd)
	personalCmd.AddCommand(personalReviewCmd)
	personalCmd.AddCommand(personalForgetCmd)

	personalReviewCmd.Flags().Int("limit", 20, "Maximum entries to show")
	personalReviewCmd.Flags().Bool("agent-only", false, "Show only entries recorded by an agent via MCP")
}

// openTarget resolves which store a command should act on. With --personal it is
// the personal store; otherwise the named scope, or the project's default scope
// when the flag is empty.
func openTarget(readOnly bool, scopeFlag string) (*knowledge.Store, string, error) {
	if usePersonal {
		return openPersonalStore(readOnly)
	}

	scopeName := scopeFlag
	if scopeName == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("get cwd: %w", err)
		}
		aiDir := filepath.Join(findProjectRoot(cwd), ".ai")
		defaultScope, _ := knowledge.GetDefaultScope(aiDir)
		scopeName = defaultScope
	}

	return openStoreModeWithScope(readOnly, scopeName)
}

// personalWritesEnv opts the MCP server into recording personal knowledge
// without editing the client's MCP configuration.
const personalWritesEnv = "KG_PERSONAL_WRITES"

// personalWritesEnabled reports whether agents may write to the personal store.
// Off unless asked for: either --personal-writes on `kg server`, or
// KG_PERSONAL_WRITES set to something other than 0/false/"".
func personalWritesEnabled(cmd *cobra.Command) bool {
	if cmd != nil {
		if enabled, err := cmd.Flags().GetBool("personal-writes"); err == nil && enabled {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(personalWritesEnv))) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

var personalReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "List recent entries in the personal knowledge store",
	Long: `List the most recently updated entries in the personal knowledge store, newest
first, flagging the ones an agent recorded through the MCP server so they can be
checked and removed if unwanted.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		agentOnly, _ := cmd.Flags().GetBool("agent-only")

		store, projectID, err := openPersonalStore(true)
		if err != nil {
			return err
		}
		defer store.Close()

		entities, err := store.ListEntities(projectID, "")
		if err != nil {
			return err
		}

		sort.Slice(entities, func(i, j int) bool {
			return entities[i].UpdatedAt.After(entities[j].UpdatedAt)
		})

		shown := 0
		for _, entity := range entities {
			if shown >= limit {
				break
			}

			observations, _ := store.GetObservations(entity.ID, projectID)
			viaAgent := false
			for _, obs := range observations {
				if strings.Contains(obs.Content, knowledge.PersonalViaMCPMarker) {
					viaAgent = true
					break
				}
			}
			if agentOnly && !viaAgent {
				continue
			}

			origin := "you"
			if viaAgent {
				origin = "agent"
			}
			cmd.Printf("%s  %s (%s) — recorded by %s\n",
				entity.UpdatedAt.Local().Format("2006-01-02 15:04"), entity.Name, entity.Type, origin)
			cmd.Printf("  id: %s\n", entity.ID)
			for _, obs := range observations {
				cmd.Printf("  - %s\n", obs.Content)
			}
			cmd.Println()
			shown++
		}

		if shown == 0 {
			if agentOnly {
				cmd.Println("No agent-recorded entries in the personal knowledge store.")
			} else {
				cmd.Println("Personal knowledge store is empty.")
			}
			return nil
		}
		cmd.Printf("Remove an entry with: kg personal forget <id>\n")
		return nil
	},
}

var personalForgetCmd = &cobra.Command{
	Use:   "forget <entity-id>",
	Short: "Delete an entry from the personal knowledge store",
	Long: `Delete an entry and its observations from the personal knowledge store. Use
'kg personal review' to find the ID.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, projectID, err := openPersonalStore(false)
		if err != nil {
			return err
		}
		defer store.Close()

		// Report what is being removed, so an accidental ID is obvious.
		entity, err := store.GetEntity(args[0], projectID)
		if err != nil {
			return fmt.Errorf("no personal entry with ID %s: %w", args[0], err)
		}

		if err := store.DeleteEntity(entity.ID, projectID); err != nil {
			return err
		}
		cmd.Printf("Removed %q (%s) from the personal knowledge store.\n", entity.Name, entity.Type)
		return nil
	},
}
