package knowledge

import (
	"sort"
	"strings"
	"testing"
)

// linkGraph builds a merged-graph fixture directly: LinkPackages reads only
// nodes and layers, so none of this needs a database.
func linkGraph(nodes ...GraphNode) *Graph {
	g := &Graph{
		nodes: make(map[string]GraphNode, len(nodes)),
		out:   make(map[string][]GraphEdge),
		in:    make(map[string][]GraphEdge),
	}
	for _, n := range nodes {
		g.nodes[n.ID] = n
	}
	return g
}

func pkgNode(id, name string, layers ...string) GraphNode {
	return GraphNode{ID: id, Name: name, Type: EntityTypePackage, Layers: layers}
}

func importNode(id, name string, layers ...string) GraphNode {
	return GraphNode{ID: id, Name: name, Type: EntityTypeImport, Layers: layers}
}

// derivedEdges returns every derived edge as "from -TYPE-> to", sorted.
func derivedEdges(g *Graph) []string {
	var out []string
	for _, edges := range g.out {
		for _, e := range edges {
			if e.Derived {
				out = append(out, e.FromID+" -"+e.Type+"-> "+e.ToID)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestLinkPackagesDerivesCrossLayerDependency(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "com.depop.auth.client", "libraries"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
	)

	report := LinkPackages(g)

	if report.Derived != 1 {
		t.Fatalf("Derived = %d, want 1", report.Derived)
	}
	if got := derivedEdges(g); len(got) != 1 || got[0] != "imp1 -DEPENDS_ON-> pkg1" {
		t.Errorf("edges = %v, want [imp1 -DEPENDS_ON-> pkg1]", got)
	}
	if g.EdgeCount() != 1 {
		t.Errorf("EdgeCount = %d, want 1", g.EdgeCount())
	}
	// The edge has to be reachable in both directions, or "who depends on this
	// library" — the question this feature exists to answer — cannot be asked.
	if len(g.in["pkg1"]) != 1 {
		t.Errorf("derived edge missing from the incoming adjacency list")
	}
}

// The most specific package that prefixes the import wins: com.depop.auth.client
// tells you which library, com.depop.auth barely narrows it down.
func TestLinkPackagesPrefersTheLongestPrefix(t *testing.T) {
	g := linkGraph(
		pkgNode("short", "com.depop.auth", "libraries"),
		pkgNode("long", "com.depop.auth.client", "libraries"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
	)

	LinkPackages(g)

	if got := derivedEdges(g); len(got) != 1 || !strings.HasSuffix(got[0], "-> long") {
		t.Errorf("edges = %v, want the link to resolve to the longer package", got)
	}
}

// Without a floor on specificity, "com.depop" claims every JVM import in the
// estate and the graph gains one hub node instead of a dependency map.
func TestLinkPackagesIgnoresUnspecificPackages(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "com.depop", "libraries"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
	)

	report := LinkPackages(g)

	if report.Derived != 0 {
		t.Errorf("Derived = %d, want 0 for a two-segment package", report.Derived)
	}
}

// A package defined in several layers cannot say which one an import meant, and
// guessing would invent exactly the kind of edge this feature replaces.
func TestLinkPackagesSkipsAmbiguousTargets(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "com.depop.auth.client", "libraries", "tooling"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
	)

	report := LinkPackages(g)

	if report.Derived != 0 {
		t.Errorf("Derived = %d, want 0", report.Derived)
	}
	if report.Ambiguous != 1 {
		t.Errorf("Ambiguous = %d, want 1", report.Ambiguous)
	}
	if len(report.AmbiguousNames) != 1 || report.AmbiguousNames[0] != "com.depop.auth.client" {
		t.Errorf("AmbiguousNames = %v, want [com.depop.auth.client]", report.AmbiguousNames)
	}
}

// The join guard can leave one package name as several nodes; that is the same
// ambiguity arriving by a different route and must be treated the same way.
func TestLinkPackagesTreatsSplitNodesAsAmbiguous(t *testing.T) {
	g := linkGraph(
		pkgNode("pkgA", "com.depop.auth.client", "libraries"),
		pkgNode("pkgB", "com.depop.auth.client", "tooling"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
	)

	report := LinkPackages(g)

	if report.Derived != 0 || report.Ambiguous != 1 {
		t.Errorf("Derived = %d, Ambiguous = %d; want 0 and 1", report.Derived, report.Ambiguous)
	}
}

// An import used only where the package is defined describes that layer's own
// structure, which its graph already holds.
func TestLinkPackagesSkipsSameLayerResolution(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "com.depop.auth.client", "libraries"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "libraries"),
	)

	report := LinkPackages(g)

	if report.Derived != 0 {
		t.Errorf("Derived = %d, want 0", report.Derived)
	}
	if report.SameLayer != 1 {
		t.Errorf("SameLayer = %d, want 1", report.SameLayer)
	}
}

// A joined import spanning several layers still depends on the package from the
// layers that are not the defining one.
func TestLinkPackagesLinksWhenAnyLayerIsForeign(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "com.depop.auth.client", "libraries"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "libraries", "martech"),
	)

	report := LinkPackages(g)

	if report.Derived != 1 {
		t.Errorf("Derived = %d, want 1 — martech imports it from outside libraries", report.Derived)
	}
}

// Documents a known gap rather than a design choice: no indexer mints package
// entities for npm or Go module paths, so slash-style imports resolve to
// nothing. If that changes upstream, this test is the place it surfaces.
func TestLinkPackagesDoesNotResolveSlashNamespaces(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "@depop/auth-client", "libraries"),
		importNode("imp1", "@depop/auth-client/dist/index", "martech"),
	)

	report := LinkPackages(g)

	if report.Derived != 0 {
		t.Errorf("Derived = %d; slash namespaces are not matched today", report.Derived)
	}
}

func TestLinkPackagesIsDeterministic(t *testing.T) {
	build := func() *Graph {
		return linkGraph(
			pkgNode("pkgA", "com.depop.auth.client", "libraries"),
			pkgNode("pkgB", "com.depop.data.store", "data"),
			importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
			importNode("imp2", "com.depop.data.store.Reader", "product"),
			importNode("imp3", "com.depop.auth.client.Jwt", "ads"),
		)
	}

	first := build()
	LinkPackages(first)
	want := derivedEdges(first)
	if len(want) != 3 {
		t.Fatalf("got %d derived edges, want 3", len(want))
	}

	for i := 0; i < 20; i++ {
		g := build()
		LinkPackages(g)
		got := derivedEdges(g)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("run %d produced %v, first run produced %v", i, got, want)
		}
	}
}

// Derived edges must be distinguishable in the output, or a reader takes an
// inference on the same footing as a recorded fact.
func TestDerivedEdgesRenderDistinctly(t *testing.T) {
	g := linkGraph(
		pkgNode("pkg1", "com.depop.auth.client", "libraries"),
		importNode("imp1", "com.depop.auth.client.AuthClient", "martech"),
	)
	LinkPackages(g)

	sub, err := g.Subgraph(GraphOptions{})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	mermaid := RenderMermaid(sub)
	if !strings.Contains(mermaid, "-.->|DEPENDS_ON|") {
		t.Errorf("derived edge is not dashed in mermaid:\n%s", mermaid)
	}
	if !strings.Contains(mermaid, "1 of those are derived") {
		t.Errorf("header does not count derived edges:\n%s", mermaid)
	}

	dot := RenderDOT(sub)
	if !strings.Contains(dot, `[label="DEPENDS_ON", style=dashed]`) {
		t.Errorf("derived edge is not dashed in dot:\n%s", dot)
	}
}
