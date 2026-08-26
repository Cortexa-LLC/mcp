package knowledge

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// testGraph builds this fixture without touching a database:
//
//	baz --CALLS--> app.go --CONTAINS--> foo --CALLS----> bar
//	                  |                  |               |
//	                  +---CONTAINS-------+  --IMPORTS--> fmt
//	                                                     |
//	                                        bar --RELATES_TO--> Widget
//
// Depth from app.go: foo, bar, baz at 1; fmt, Widget at 2.
// foo→bar is the cross-link between two nodes the walk reaches separately.
func testGraph() *Graph {
	g := &Graph{
		nodes: map[string]GraphNode{},
		out:   map[string][]GraphEdge{},
		in:    map[string][]GraphEdge{},
	}
	add := func(id, name, entityType string) {
		g.nodes[id] = GraphNode{ID: id, Name: name, Type: entityType}
	}
	add("file:app.go", "app.go", EntityTypeFile)
	add("function:foo", "foo", EntityTypeFunction)
	add("function:bar", "bar", EntityTypeFunction)
	add("function:baz", "baz", EntityTypeFunction)
	add("import:fmt", "fmt", EntityTypeImport)
	add("type:Widget", "Widget", EntityTypeType)

	link := func(from, relType, to string) {
		e := GraphEdge{FromID: from, ToID: to, Type: relType}
		g.out[from] = append(g.out[from], e)
		g.in[to] = append(g.in[to], e)
		g.edgeCount++
	}
	link("file:app.go", RelContains, "function:foo")
	link("file:app.go", RelContains, "function:bar")
	link("function:foo", RelCalls, "function:bar")
	link("function:foo", RelImports, "import:fmt")
	link("function:baz", RelCalls, "file:app.go")
	link("function:bar", RelRelatesTo, "type:Widget")
	return g
}

// nodeNames returns the subgraph's node names in render order.
func nodeNames(sub Subgraph) []string {
	names := make([]string, len(sub.Nodes))
	for i, n := range sub.Nodes {
		names[i] = n.Name
	}
	return names
}

// edgeStrings renders the subgraph's edges as "from -REL-> to" in render order.
func edgeStrings(sub Subgraph, g *Graph) []string {
	out := make([]string, len(sub.Edges))
	for i, e := range sub.Edges {
		out[i] = g.nodes[e.FromID].Name + " -" + e.Type + "-> " + g.nodes[e.ToID].Name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSubgraphTraversal(t *testing.T) {
	g := testGraph()

	tests := []struct {
		name  string
		opts  GraphOptions
		want  []string // expected node names, in render order
		depth map[string]int
	}{
		{
			name: "depth 1 both directions",
			opts: GraphOptions{RootID: "file:app.go", Depth: 1},
			want: []string{"app.go", "bar", "baz", "foo"},
		},
		{
			name: "depth 2 reaches the far side",
			opts: GraphOptions{RootID: "file:app.go", Depth: 2},
			want: []string{"app.go", "bar", "baz", "foo", "Widget", "fmt"},
			depth: map[string]int{
				"app.go": 0, "bar": 1, "baz": 1, "foo": 1, "Widget": 2, "fmt": 2,
			},
		},
		{
			name: "depth 0 is the root alone",
			opts: GraphOptions{RootID: "file:app.go", Depth: 0},
			want: []string{"app.go"},
		},
		{
			name: "outgoing only drops the caller",
			opts: GraphOptions{RootID: "file:app.go", Depth: 1, Direction: DirectionOut},
			want: []string{"app.go", "bar", "foo"},
		},
		{
			name: "incoming only keeps just the caller",
			opts: GraphOptions{RootID: "file:app.go", Depth: 1, Direction: DirectionIn},
			want: []string{"app.go", "baz"},
		},
		{
			name: "relation filter constrains reachability",
			opts: GraphOptions{RootID: "file:app.go", Depth: 2, RelTypes: []string{"CONTAINS"}},
			want: []string{"app.go", "bar", "foo"},
		},
		{
			name: "relation filter is case-insensitive and comma-split",
			opts: GraphOptions{RootID: "file:app.go", Depth: 2, RelTypes: []string{"contains,calls"}},
			want: []string{"app.go", "bar", "baz", "foo"},
		},
		{
			name: "node type filter keeps the root even when excluded",
			opts: GraphOptions{RootID: "file:app.go", Depth: 2, NodeTypes: []string{"function"}},
			want: []string{"app.go", "bar", "baz", "foo"},
		},
		{
			name: "whole graph when no root is given",
			opts: GraphOptions{},
			want: []string{"Widget", "app.go", "bar", "baz", "fmt", "foo"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := g.Subgraph(tc.opts)
			if err != nil {
				t.Fatalf("Subgraph: %v", err)
			}
			if got := nodeNames(sub); !equalStrings(got, tc.want) {
				t.Errorf("nodes = %v, want %v", got, tc.want)
			}
			if sub.Truncated {
				t.Errorf("Truncated = true, want false")
			}
			if sub.TotalNodes != 6 {
				t.Errorf("TotalNodes = %d, want 6", sub.TotalNodes)
			}
			for _, n := range sub.Nodes {
				if want, ok := tc.depth[n.Name]; ok && n.Depth != want {
					t.Errorf("%s depth = %d, want %d", n.Name, n.Depth, want)
				}
			}
		})
	}
}

// The walk reaches foo and bar independently from the root; the edge between
// them is part of the neighbourhood and has to survive into the render, or the
// picture is a tree rather than a graph.
func TestSubgraphKeepsCrossLinksBetweenSelectedNodes(t *testing.T) {
	g := testGraph()

	sub, err := g.Subgraph(GraphOptions{RootID: "file:app.go", Depth: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	want := []string{
		"app.go -CONTAINS-> bar",
		"app.go -CONTAINS-> foo",
		"baz -CALLS-> app.go",
		"foo -CALLS-> bar",
	}
	if got := edgeStrings(sub, g); !equalStrings(got, want) {
		t.Errorf("edges = %v, want %v", got, want)
	}
}

// An edge is only drawn when both of its endpoints made it into the result;
// a filtered-out node must not leave a dangling arrow behind.
func TestSubgraphDropsEdgesToUnselectedNodes(t *testing.T) {
	g := testGraph()

	sub, err := g.Subgraph(GraphOptions{RootID: "file:app.go", Depth: 1, Direction: DirectionOut})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	for _, e := range sub.Edges {
		if e.FromID == "function:baz" || e.ToID == "import:fmt" {
			t.Errorf("edge to unselected node survived: %+v", e)
		}
	}
}

func TestSubgraphTruncatesDeterministically(t *testing.T) {
	g := testGraph()
	opts := GraphOptions{RootID: "file:app.go", Depth: 2, MaxNodes: 3}

	sub, err := g.Subgraph(opts)
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if !sub.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(sub.Nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(sub.Nodes))
	}

	// Map iteration order differs run to run; the cap must not.
	for i := 0; i < 20; i++ {
		again, err := g.Subgraph(opts)
		if err != nil {
			t.Fatalf("Subgraph: %v", err)
		}
		if got, want := nodeNames(again), nodeNames(sub); !equalStrings(got, want) {
			t.Fatalf("run %d selected %v, first run selected %v", i, got, want)
		}
	}
}

func TestSubgraphUnknownRoot(t *testing.T) {
	g := testGraph()
	if _, err := g.Subgraph(GraphOptions{RootID: "function:nope"}); err == nil {
		t.Error("expected an error for a root that is not in the graph")
	}
}

func TestResolveRoot(t *testing.T) {
	g := testGraph()
	// Two entities sharing a name, differing in type: the ambiguity case.
	g.nodes["type:bar"] = GraphNode{ID: "type:bar", Name: "bar", Type: EntityTypeType}

	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr string
	}{
		{name: "by id", ref: "function:foo", want: "function:foo"},
		{name: "by exact name", ref: "app.go", want: "file:app.go"},
		{name: "by name ignoring case", ref: "APP.GO", want: "file:app.go"},
		{name: "unknown", ref: "nosuchthing", wantErr: "no entity with ID or name"},
		{name: "ambiguous name", ref: "bar", wantErr: "matches 2 entities"},
		{name: "empty", ref: "  ", wantErr: "empty root reference"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := g.ResolveRoot(tc.ref)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("got %q, want error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRoot: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// An ID always wins over a name, even when some other entity is named after
// this one's ID — otherwise the exact thing the user pasted could lose.
func TestResolveRootPrefersIDOverName(t *testing.T) {
	g := testGraph()
	g.nodes["decoy"] = GraphNode{ID: "decoy", Name: "function:foo", Type: EntityTypeTopic}

	got, err := g.ResolveRoot("function:foo")
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got != "function:foo" {
		t.Errorf("got %q, want the entity whose ID matched", got)
	}
}

func TestParseDirection(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    Direction
		wantErr bool
	}{
		{in: "", want: DirectionBoth},
		{in: "both", want: DirectionBoth},
		{in: "OUT", want: DirectionOut},
		{in: " in ", want: DirectionIn},
		{in: "sideways", wantErr: true},
	} {
		got, err := ParseDirection(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseDirection(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDirection(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseDirection(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseGraphFormat(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    GraphFormat
		wantErr bool
	}{
		{in: "", want: FormatMermaid},
		{in: "Mermaid", want: FormatMermaid},
		{in: "dot", want: FormatDOT},
		{in: "json", want: FormatJSON},
		{in: "svg", wantErr: true},
	} {
		got, err := ParseGraphFormat(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseGraphFormat(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseGraphFormat(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseGraphFormat(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// LoadGraph is the only part of the feature that touches Kuzu, so it gets the
// only test that opens one.
func TestLoadGraphFromStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	const projectID = "graphtest"
	file, err := store.CreateEntity("app.go", EntityTypeFile, projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	fn, err := store.CreateEntity("foo", EntityTypeFunction, projectID)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := store.CreateRelation(file.ID, fn.ID, RelContains, projectID); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	// A second project in the same store: none of it may leak into the graph.
	other, err := store.CreateEntity("other.go", EntityTypeFile, "someoneelse")
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}

	g, err := LoadGraph(store, projectID)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if g.NodeCount() != 2 {
		t.Errorf("NodeCount = %d, want 2", g.NodeCount())
	}
	if g.EdgeCount() != 1 {
		t.Errorf("EdgeCount = %d, want 1", g.EdgeCount())
	}
	if _, err := g.ResolveRoot(other.ID); err == nil {
		t.Error("an entity from another project resolved as a root")
	}

	sub, err := g.Subgraph(GraphOptions{RootID: file.ID, Depth: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if got := nodeNames(sub); !equalStrings(got, []string{"app.go", "foo"}) {
		t.Errorf("nodes = %v, want [app.go foo]", got)
	}
}

func TestRenderMermaid(t *testing.T) {
	g := testGraph()
	sub, err := g.Subgraph(GraphOptions{RootID: "file:app.go", Depth: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	out := RenderMermaid(sub)
	for _, want := range []string{
		"%% kg graph: 4 node(s), 4 relation(s) of 6 entities in the project\n",
		"graph LR\n",
		`n0["app.go"]:::kgroot`, // root: file shape, marked
		`n1(["bar"])`,           // function shape
		"n0 -->|CONTAINS| n1",
		"classDef kgroot stroke-width:3px",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mermaid output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TRUNCATED") {
		t.Errorf("unexpected truncation notice:\n%s", out)
	}
}

func TestRenderMermaidEscapesLabels(t *testing.T) {
	g := &Graph{
		nodes: map[string]GraphNode{
			"a": {ID: "a", Name: "say \"hi\"", Type: EntityTypeTopic},
			"b": {ID: "b", Name: "issue #42\nsecond line", Type: EntityTypeTopic},
		},
		out: map[string][]GraphEdge{},
		in:  map[string][]GraphEdge{},
	}
	sub, err := g.Subgraph(GraphOptions{})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	out := RenderMermaid(sub)
	if strings.Contains(out, `"hi"`) {
		t.Errorf("raw quotes survived into a mermaid label:\n%s", out)
	}
	if !strings.Contains(out, "#quot;hi#quot;") {
		t.Errorf("quotes not escaped as mermaid entity codes:\n%s", out)
	}
	// '#' must be escaped before the quote escape is introduced, or "#quot;"
	// itself gets mangled into "#35;quot;".
	if !strings.Contains(out, "issue #35;42 second line") {
		t.Errorf("'#' or newline not handled:\n%s", out)
	}
	if strings.Contains(out, "#35;quot;") {
		t.Errorf("escape order mangled a quote escape:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Count(line, "[") != strings.Count(line, "]") {
			t.Errorf("unbalanced node syntax on line %q", line)
		}
	}
}

func TestRenderDOT(t *testing.T) {
	g := testGraph()
	sub, err := g.Subgraph(GraphOptions{RootID: "file:app.go", Depth: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	out := RenderDOT(sub)
	for _, want := range []string{
		"digraph kg {",
		"rankdir=LR;",
		`n0 [label="app.go", tooltip="file", penwidth=3];`,
		`n0 -> n1 [label="CONTAINS"];`,
		"}\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dot output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDOTEscapesLabels(t *testing.T) {
	g := &Graph{
		nodes: map[string]GraphNode{
			"a": {ID: "a", Name: `C:\path "quoted"`, Type: EntityTypeFile},
		},
		out: map[string][]GraphEdge{},
		in:  map[string][]GraphEdge{},
	}
	sub, err := g.Subgraph(GraphOptions{})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	out := RenderDOT(sub)
	if !strings.Contains(out, `label="C:\\path \"quoted\""`) {
		t.Errorf("backslash or quote not escaped for DOT:\n%s", out)
	}
}

func TestRenderTruncationNotice(t *testing.T) {
	g := testGraph()
	sub, err := g.Subgraph(GraphOptions{RootID: "file:app.go", Depth: 2, MaxNodes: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	for name, out := range map[string]string{
		"mermaid": RenderMermaid(sub),
		"dot":     RenderDOT(sub),
	} {
		if !strings.Contains(out, "TRUNCATED") {
			t.Errorf("%s output does not say it is incomplete:\n%s", name, out)
		}
	}
}

func TestRenderGraphJSON(t *testing.T) {
	g := testGraph()
	sub, err := g.Subgraph(GraphOptions{RootID: "file:app.go", Depth: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	out, err := RenderGraph(sub, FormatJSON)
	if err != nil {
		t.Fatalf("RenderGraph: %v", err)
	}

	var decoded Subgraph
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if decoded.RootID != "file:app.go" {
		t.Errorf("RootID = %q, want file:app.go", decoded.RootID)
	}
	if len(decoded.Nodes) != len(sub.Nodes) || len(decoded.Edges) != len(sub.Edges) {
		t.Errorf("round trip changed the graph: %d/%d nodes, %d/%d edges",
			len(decoded.Nodes), len(sub.Nodes), len(decoded.Edges), len(sub.Edges))
	}
}

func TestRenderGraphUnknownFormat(t *testing.T) {
	if _, err := RenderGraph(Subgraph{}, GraphFormat("svg")); err == nil {
		t.Error("expected an error for an unknown format")
	}
}

// Rendering an empty result must still produce a valid, parseable document
// rather than a bare header — an empty graph is a normal answer to a filter
// that matched nothing.
func TestRenderEmptySubgraph(t *testing.T) {
	empty := Subgraph{}
	if !strings.Contains(RenderMermaid(empty), "graph LR") {
		t.Error("empty mermaid render is not a valid diagram")
	}
	dot := RenderDOT(empty)
	if !strings.HasSuffix(dot, "}\n") {
		t.Errorf("empty dot render is not closed:\n%s", dot)
	}
}
