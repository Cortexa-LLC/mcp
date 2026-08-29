package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

// kg graph — draw a slice of the knowledge graph as mermaid, DOT, or JSON.
//
// The whole graph is almost never the useful picture: a real project holds
// thousands of entities, and every renderer turns that into a hairball. The
// unit that reads well is a neighbourhood — one entity and a couple of hops
// around it — so --root/--depth is the path this command is built around, and
// the whole-graph render is the fallback rather than the default shape.

var (
	graphRoot      string
	graphDepth     int
	graphDirection string
	graphFormat    string
	graphNodeTypes []string
	graphRelTypes  []string
	graphLimit     int
	graphOutput    string
	graphScope     string
	graphFederated bool
	graphLayers    []string
	graphMaxJoin   int
)

// graphSettings is one invocation's flags, kept separate from the flag
// variables so the command body can be exercised without cobra.
type graphSettings struct {
	Root      string
	Depth     int
	Direction string
	Format    string
	NodeTypes []string
	RelTypes  []string
	Limit     int
}

// validate checks the settings that do not depend on the graph, so a bad
// invocation fails before any database is opened.
func (s graphSettings) validate() (knowledge.GraphFormat, knowledge.Direction, error) {
	format, err := knowledge.ParseGraphFormat(s.Format)
	if err != nil {
		return "", "", err
	}
	direction, err := knowledge.ParseDirection(s.Direction)
	if err != nil {
		return "", "", err
	}
	if s.Depth < 0 {
		return "", "", fmt.Errorf("--depth must not be negative")
	}
	return format, direction, nil
}

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Render the knowledge graph as mermaid, Graphviz DOT, or JSON",
	Long: `Render part of the knowledge graph as a diagram.

Without --root the whole graph is rendered, capped at --limit nodes. That is
rarely readable for a real project: prefer picking a --root entity and a small
--depth, which draws that entity's neighbourhood, including the relations
between the neighbours themselves.

--root takes an entity ID or a name; a name that matches more than one entity
is an error listing the candidates. Find one with 'kg search'.

Output goes to stdout unless -o is given.

Examples:
  kg graph --root config.go                      # neighbourhood, 2 hops, mermaid
  kg graph --root LoadGraph --depth 3            # wider
  kg graph --root Store --direction in           # what depends on Store
  kg graph --root app.go --rel CALLS,CONTAINS    # only those relations
  kg graph --type file,package --limit 60        # whole graph, structure only
  kg graph --format dot | dot -Tsvg -o graph.svg # render an image
  kg graph --personal --format json              # personal store, machine-readable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Silently ignoring these would render one database and look like it
		// had honoured the request.
		if !graphFederated {
			for _, flag := range []string{"layer", "join-max-layers"} {
				if cmd.Flags().Changed(flag) {
					return fmt.Errorf("--%s only applies with --federated", flag)
				}
			}
		}

		// Render into memory when writing to a file, and only touch the target
		// once everything has succeeded. Opening it here instead would truncate
		// it immediately — os.Create is O_TRUNC — and the render can still fail
		// afterwards on flag validation, an unresolvable --root, or the store
		// open. The docs recommend committing these diagrams and refreshing them
		// with the same command, so a mistyped flag must not be able to empty
		// one; the file is either replaced by a complete render or left alone.
		out := cmd.OutOrStdout()
		var rendered *bytes.Buffer
		if graphOutput != "" {
			rendered = &bytes.Buffer{}
			out = rendered
		}

		settings := graphSettings{
			Root:      graphRoot,
			Depth:     graphDepth,
			Direction: graphDirection,
			Format:    graphFormat,
			NodeTypes: graphNodeTypes,
			RelTypes:  graphRelTypes,
			Limit:     graphLimit,
		}

		// Validate up front, before the store is opened. runGraph and
		// runFederatedGraph both validate too — they are called directly by
		// tests — but the federated path validated before opening anything
		// while the non-federated path opened a store first, so
		// `kg graph --format bogus` paid for a Kuzu open before failing.
		// graphSettings.validate documents that a bad invocation "fails before
		// any database is opened"; this is what makes that true of the command.
		if _, _, err := settings.validate(); err != nil {
			return err
		}

		var sub knowledge.Subgraph
		var err error
		if graphFederated {
			sub, err = runFederatedGraph(cmd, settings, out)
		} else {
			var store *knowledge.Store
			var projectID string
			store, projectID, err = openTarget(true, graphScope)
			if err != nil {
				return err
			}
			defer store.Close()
			sub, err = runGraph(store, projectID, settings, out)
		}
		if err != nil {
			return err
		}

		if rendered != nil {
			if err := writeFileAtomic(graphOutput, rendered.Bytes()); err != nil {
				return err
			}
		}

		// Notes go to stderr so that a piped render stays a clean document.
		if graphOutput != "" {
			cmd.PrintErrf("Wrote %d node(s) and %d relation(s) to %s\n",
				len(sub.Nodes), len(sub.Edges), graphOutput)
		}
		if sub.Truncated {
			cmd.PrintErrf("Truncated at %d nodes — raise --limit to see more.\n", settings.Limit)
		}
		if len(sub.Nodes) == 0 {
			cmd.PrintErrln("Nothing matched: the graph is empty, or the filters excluded everything.")
		}
		return nil
	},
}

// writeFileAtomic replaces path with data by writing a temporary file in the
// same directory and renaming it into place.
//
// The rename is what makes it safe: a plain write truncates the target first,
// so a failure part-way through (a full disk, a killed process) would leave a
// half-written diagram where a complete one used to be. Renaming within one
// directory is atomic, so the target is only ever the old contents or the new.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file beside %s: %w", path, err)
	}
	tmp := f.Name()
	// Cleans up on every failure path below; a no-op once the rename lands.
	defer os.Remove(tmp)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	// CreateTemp makes the file 0600; a rendered diagram is meant to be a
	// readable, committable artifact like any other output file.
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	// Close before renaming, and report its error: buffered writes can fail
	// here (a full disk surfacing late), and ignoring that would rename a
	// truncated file over a good one.
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// runGraph renders one slice of store's graph to out, returning what it drew.
func runGraph(store *knowledge.Store, projectID string, s graphSettings, out io.Writer) (knowledge.Subgraph, error) {
	if _, _, err := s.validate(); err != nil {
		return knowledge.Subgraph{}, err
	}
	graph, err := knowledge.LoadGraph(store, projectID)
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	return renderLoadedGraph(graph, s, out)
}

// renderLoadedGraph selects and renders from a graph that is already in memory,
// whichever way it was loaded — one database or sixty.
func renderLoadedGraph(graph *knowledge.Graph, s graphSettings, out io.Writer) (knowledge.Subgraph, error) {
	format, direction, err := s.validate()
	if err != nil {
		return knowledge.Subgraph{}, err
	}

	var rootID string
	if s.Root != "" {
		if rootID, err = graph.ResolveRoot(s.Root); err != nil {
			return knowledge.Subgraph{}, err
		}
	}

	sub, err := graph.Subgraph(knowledge.GraphOptions{
		RootID:    rootID,
		Depth:     s.Depth,
		Direction: direction,
		NodeTypes: s.NodeTypes,
		RelTypes:  s.RelTypes,
		MaxNodes:  s.Limit,
	})
	if err != nil {
		return knowledge.Subgraph{}, err
	}

	rendered, err := knowledge.RenderGraph(sub, format)
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	if _, err := io.WriteString(out, rendered); err != nil {
		return knowledge.Subgraph{}, fmt.Errorf("write graph: %w", err)
	}
	return sub, nil
}

// runFederatedGraph renders across a scope and every layer it federates with.
func runFederatedGraph(cmd *cobra.Command, s graphSettings, out io.Writer) (knowledge.Subgraph, error) {
	if usePersonal {
		return knowledge.Subgraph{}, fmt.Errorf("--personal and --federated are mutually exclusive: the personal store has no layers")
	}
	if _, _, err := s.validate(); err != nil {
		return knowledge.Subgraph{}, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	root := findProjectRoot(cwd)
	aiDir := filepath.Join(root, ".ai")

	scopeName := graphScope
	if scopeName == "" {
		if scopeName, err = knowledge.GetDefaultScope(aiDir); err != nil {
			return knowledge.Subgraph{}, err
		}
	}
	if scopeName == "" {
		return knowledge.Subgraph{}, fmt.Errorf("--federated needs scopes: this project has a single knowledge.db with no layers")
	}
	scope, err := knowledge.LoadScopeConfig(aiDir, scopeName)
	if err != nil {
		return knowledge.Subgraph{}, err
	}

	graph, report, err := knowledge.LoadFederatedGraph(knowledge.FederatedGraphOptions{
		AIDir:         aiDir,
		Scope:         scope,
		ProjectID:     filepath.Base(root),
		OnlyLayers:    graphLayers,
		MaxJoinLayers: graphMaxJoin,
	})
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	printFederationReport(cmd, report)

	return renderLoadedGraph(graph, s, out)
}

// printFederationReport writes what the federated load did to stderr, keeping
// stdout a clean document. A merged graph hides two things a reader would want
// to know — a layer that failed to open, and identities too widespread to
// join — so both are said out loud rather than left to be inferred.
func printFederationReport(cmd *cobra.Command, report *knowledge.FederationReport) {
	nodes, edges := 0, 0
	for _, l := range report.Layers {
		nodes += l.Nodes
		edges += l.Edges
	}
	cmd.PrintErrf("Federated %d layer(s): %d node(s), %d relation(s).\n", len(report.Layers), nodes, edges)

	if report.Joined > 0 {
		cmd.PrintErrf("Joined %d identit%s across layers, merging %d duplicate row(s).\n",
			report.Joined, plural(report.Joined, "y", "ies"), report.MergedNodes)
	}
	if n := len(report.Suppressed); n > 0 {
		cmd.PrintErrf("Left %d name(s) unjoined: they appear in more than %d layers, which reads as "+
			"boilerplate rather than one shared symbol. Raise --join-max-layers to join them anyway. Worst:\n", n, report.MaxJoinLayers)
		for i, sup := range report.Suppressed {
			if i == 5 {
				break
			}
			cmd.PrintErrf("  %s (%s) in %d layers\n", sup.Name, sup.Type, sup.Layers)
		}
	}
	if report.IDCollisions > 0 {
		cmd.PrintErrf("Renamed %d node(s) whose ID existed in more than one layer — the indexers derive "+
			"IDs from repo-relative paths, so the same ID can mean different things in different layers.\n",
			report.IDCollisions)
	}
	for _, failed := range report.FailedLayers() {
		cmd.PrintErrf("Warning: layer %s could not be read and is missing from this graph: %s\n",
			failed.Name, failed.Failed)
	}
}

func init() {
	// A root that does not resolve, or a filter that matches nothing, is a
	// runtime answer rather than a misuse of the command — printing the whole
	// flag list underneath those messages buries them. Same call as migrate.
	graphCmd.SilenceUsage = true

	graphCmd.Flags().StringVarP(&graphRoot, "root", "r", "",
		"Entity ID or name to centre the graph on (default: the whole graph)")
	graphCmd.Flags().IntVarP(&graphDepth, "depth", "d", 2,
		"How many hops from --root to follow")
	graphCmd.Flags().StringVar(&graphDirection, "direction", "both",
		"Which relations to follow from --root: both, out (depends on), or in (depended on by)")
	graphCmd.Flags().StringVarP(&graphFormat, "format", "f", "mermaid",
		"Output format: mermaid, dot, or json")
	graphCmd.Flags().StringSliceVar(&graphNodeTypes, "type", nil,
		"Only include entities of these types (e.g. file,function); --root is always included")
	graphCmd.Flags().StringSliceVar(&graphRelTypes, "rel", nil,
		"Only follow these relation types (e.g. CALLS,CONTAINS)")
	graphCmd.Flags().IntVar(&graphLimit, "limit", 200,
		"Maximum nodes to render; 0 for no limit")
	graphCmd.Flags().StringVarP(&graphOutput, "output", "o", "",
		"Write to a file instead of stdout")
	graphCmd.Flags().StringVar(&graphScope, "scope", "",
		"Scope to render (default: default scope)")
	graphCmd.Flags().BoolVar(&graphFederated, "federated", false,
		"Render the scope together with every local layer it federates with, joining shared identities across them (remote hub layers are search-only)")
	graphCmd.Flags().StringSliceVar(&graphLayers, "layer", nil,
		"With --federated, restrict the load to these scopes instead of all layers")
	graphCmd.Flags().IntVar(&graphMaxJoin, "join-max-layers", knowledge.DefaultMaxJoinLayers,
		"With --federated, a name found in more than this many layers is boilerplate, not one shared symbol, and is left unjoined")
	registerPersonalFlag(graphCmd)

	rootCmd.AddCommand(graphCmd)
}
