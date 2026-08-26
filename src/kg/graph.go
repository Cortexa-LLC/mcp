package main

import (
	"fmt"
	"io"
	"os"

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
		store, projectID, err := openTarget(true, graphScope)
		if err != nil {
			return err
		}
		defer store.Close()

		out := cmd.OutOrStdout()
		if graphOutput != "" {
			f, err := os.Create(graphOutput)
			if err != nil {
				return fmt.Errorf("create %s: %w", graphOutput, err)
			}
			defer f.Close()
			out = f
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
		sub, err := runGraph(store, projectID, settings, out)
		if err != nil {
			return err
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

// runGraph renders one slice of store's graph to out, returning what it drew.
func runGraph(store *knowledge.Store, projectID string, s graphSettings, out io.Writer) (knowledge.Subgraph, error) {
	format, err := knowledge.ParseGraphFormat(s.Format)
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	direction, err := knowledge.ParseDirection(s.Direction)
	if err != nil {
		return knowledge.Subgraph{}, err
	}
	if s.Depth < 0 {
		return knowledge.Subgraph{}, fmt.Errorf("--depth must not be negative")
	}

	graph, err := knowledge.LoadGraph(store, projectID)
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
	registerPersonalFlag(graphCmd)

	rootCmd.AddCommand(graphCmd)
}
