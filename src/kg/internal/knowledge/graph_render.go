package knowledge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Graph rendering — the output half. The traversal that produces a Subgraph
// lives in graph.go.
//
// Node IDs in the rendered output are positional (n0, n1, …) rather than the
// entity's own ID. Entity IDs are UUIDs or source-derived strings like
// "function:src/a.go:F", and neither mermaid nor DOT accepts those unquoted;
// numbering sidesteps every escaping question about identifiers and leaves
// only the label text to escape.

// GraphFormat is an output format for a rendered Subgraph.
type GraphFormat string

const (
	// FormatMermaid renders a mermaid flowchart, readable as text and
	// rendered inline by most markdown viewers.
	FormatMermaid GraphFormat = "mermaid"
	// FormatDOT renders Graphviz DOT, for `dot -Tsvg` and friends.
	FormatDOT GraphFormat = "dot"
	// FormatJSON renders the Subgraph itself, for scripts and for callers
	// that want to lay the graph out themselves.
	FormatJSON GraphFormat = "json"
)

// ParseGraphFormat validates a --format flag value.
func ParseGraphFormat(s string) (GraphFormat, error) {
	switch GraphFormat(strings.ToLower(strings.TrimSpace(s))) {
	case FormatMermaid, "":
		return FormatMermaid, nil
	case FormatDOT:
		return FormatDOT, nil
	case FormatJSON:
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unknown format %q: want mermaid, dot, or json", s)
	}
}

// RenderGraph renders a Subgraph in the requested format.
func RenderGraph(sub Subgraph, format GraphFormat) (string, error) {
	switch format {
	case FormatMermaid:
		return RenderMermaid(sub), nil
	case FormatDOT:
		return RenderDOT(sub), nil
	case FormatJSON:
		out, err := json.MarshalIndent(sub, "", "  ")
		if err != nil {
			return "", fmt.Errorf("encode graph: %w", err)
		}
		return string(out) + "\n", nil
	default:
		return "", fmt.Errorf("unknown format %q", format)
	}
}

// mermaidShapes gives each kg entity type a distinguishable outline, so a
// picture can be read without cross-referencing a legend. Types with no entry
// — hand-written knowledge, log-derived entities — get a plain box.
var mermaidShapes = map[string][2]string{
	EntityTypeFile:     {`["`, `"]`},   // rectangle
	EntityTypeFunction: {`(["`, `"])`}, // stadium
	EntityTypeType:     {`{{"`, `"}}`}, // hexagon
	EntityTypePackage:  {`[["`, `"]]`}, // subroutine
	EntityTypeImport:   {`[/"`, `"/]`}, // parallelogram
	EntityTypeTopic:    {`("`, `")`},   // rounded
}

// RenderMermaid renders a Subgraph as a mermaid flowchart.
func RenderMermaid(sub Subgraph) string {
	var b strings.Builder
	writeHeader(&b, sub, "%%")
	b.WriteString("graph LR\n")

	ids := renderIDs(sub)
	groups := layerGroups(sub)
	if len(groups) == 0 {
		for _, n := range sub.Nodes {
			writeMermaidNode(&b, ids, sub.RootID, n, "    ")
		}
	} else {
		// A federated render without this grouping is unreadable: nodes from
		// sixty databases in one flat list, with nothing saying which came
		// from where.
		for i, g := range groups {
			fmt.Fprintf(&b, "    subgraph layer%d[\"%s\"]\n", i, mermaidLabel(g.name))
			for _, n := range g.nodes {
				writeMermaidNode(&b, ids, sub.RootID, n, "        ")
			}
			b.WriteString("    end\n")
		}
	}

	for _, e := range sub.Edges {
		fmt.Fprintf(&b, "    %s -->|%s| %s\n", ids[e.FromID], mermaidLabel(e.Type), ids[e.ToID])
	}

	if sub.RootID != "" {
		b.WriteString("    classDef kgroot stroke-width:3px\n")
	}
	return b.String()
}

// RenderDOT renders a Subgraph as a Graphviz digraph.
func RenderDOT(sub Subgraph) string {
	var b strings.Builder
	writeHeader(&b, sub, "//")
	b.WriteString("digraph kg {\n")
	b.WriteString("    rankdir=LR;\n")
	b.WriteString("    node [shape=box, fontname=\"Helvetica\"];\n")
	b.WriteString("    edge [fontname=\"Helvetica\", fontsize=10];\n")

	ids := renderIDs(sub)
	for _, n := range sub.Nodes {
		tooltip := n.Type
		if len(n.Layers) > 0 {
			tooltip += " · " + strings.Join(n.Layers, ", ")
		}
		attrs := fmt.Sprintf("label=%s, tooltip=%s", dotQuote(n.Name), dotQuote(tooltip))
		if n.ID == sub.RootID {
			attrs += ", penwidth=3"
		}
		fmt.Fprintf(&b, "    %s [%s];\n", ids[n.ID], attrs)
	}

	for _, e := range sub.Edges {
		fmt.Fprintf(&b, "    %s -> %s [label=%s];\n", ids[e.FromID], ids[e.ToID], dotQuote(e.Type))
	}

	b.WriteString("}\n")
	return b.String()
}

// writeHeader writes the provenance comment both formats carry: what is in the
// picture, and — the part that matters — whether anything was left out of it.
func writeHeader(b *strings.Builder, sub Subgraph, comment string) {
	fmt.Fprintf(b, "%s kg graph: %d node(s), %d relation(s) of %d entities in the project\n",
		comment, len(sub.Nodes), len(sub.Edges), sub.TotalNodes)
	if sub.Truncated {
		fmt.Fprintf(b, "%s TRUNCATED at the node limit — raise --limit to see the rest\n", comment)
	}
}

// renderIDs assigns each node its positional render ID. Node order is already
// deterministic, so these are stable across runs.
func renderIDs(sub Subgraph) map[string]string {
	ids := make(map[string]string, len(sub.Nodes))
	for i, n := range sub.Nodes {
		ids[n.ID] = fmt.Sprintf("n%d", i)
	}
	return ids
}

// writeMermaidNode writes one node declaration at the given indent.
func writeMermaidNode(b *strings.Builder, ids map[string]string, rootID string, n GraphNode, indent string) {
	openTag, closeTag := `["`, `"]`
	if shape, ok := mermaidShapes[strings.ToLower(n.Type)]; ok {
		openTag, closeTag = shape[0], shape[1]
	}
	fmt.Fprintf(b, "%s%s%s%s%s", indent, ids[n.ID], openTag, mermaidLabel(n.Name), closeTag)
	if n.ID == rootID {
		b.WriteString(":::kgroot")
	}
	b.WriteString("\n")
}

// layerGroup is one scope's nodes in a federated render.
type layerGroup struct {
	name  string
	nodes []GraphNode
}

// layerGroups splits a federated subgraph into per-layer groups, in the node
// order the subgraph already fixed. It returns nil for a single-database
// render, or when a render spans only one layer — grouping is only worth the
// extra syntax when there is more than one group to tell apart.
//
// A node joined across layers is filed under the first of them, so it appears
// exactly once; its other layers stay on the node for JSON and DOT tooltips.
func layerGroups(sub Subgraph) []layerGroup {
	order := make([]string, 0, 8)
	byLayer := make(map[string][]GraphNode)
	for _, n := range sub.Nodes {
		if len(n.Layers) == 0 {
			return nil
		}
		layer := n.Layers[0]
		if _, seen := byLayer[layer]; !seen {
			order = append(order, layer)
		}
		byLayer[layer] = append(byLayer[layer], n)
	}
	if len(order) < 2 {
		return nil
	}

	groups := make([]layerGroup, 0, len(order))
	for _, name := range order {
		groups = append(groups, layerGroup{name: name, nodes: byLayer[name]})
	}
	return groups
}

// mermaidLabel makes text safe inside a quoted mermaid label. '#' goes first:
// it opens mermaid's entity-code escape, so escaping it after introducing
// '#quot;' would mangle the escape we just wrote.
func mermaidLabel(s string) string {
	s = collapseWhitespace(s)
	s = strings.ReplaceAll(s, "#", "#35;")
	s = strings.ReplaceAll(s, `"`, "#quot;")
	return s
}

// dotQuote returns s as a quoted DOT string literal.
func dotQuote(s string) string {
	s = collapseWhitespace(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// collapseWhitespace flattens a label onto one line. Both formats are
// line-oriented, so an embedded newline would truncate the statement.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
