package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleSubgraph is a small rooted graph with one derived edge and one layer,
// so a render exercises every branch the page has special handling for.
func sampleSubgraph() Subgraph {
	return Subgraph{
		Nodes: []GraphNode{
			{ID: "file:app.go", Name: "app.go", Type: EntityTypeFile, Depth: 0},
			{ID: "func:main", Name: "main", Type: EntityTypeFunction, Depth: 1},
			{ID: "pkg:remote", Name: "remote", Type: EntityTypePackage, Depth: 2, Layers: []string{"other"}},
		},
		Edges: []GraphEdge{
			{FromID: "file:app.go", ToID: "func:main", Type: "CONTAINS"},
			{FromID: "func:main", ToID: "pkg:remote", Type: "DEPENDS_ON", Derived: true},
		},
		RootID:     "file:app.go",
		TotalNodes: 9,
	}
}

func TestRenderHTMLIsASelfContainedPage(t *testing.T) {
	page, err := RenderHTML(sampleSubgraph())
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	for _, want := range []string{"<!doctype html>", "</html>", "id=\"kg-data\""} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
	// The no-dependencies promise: nothing may be fetched at view time, or a
	// rendered file stops working offline and behind a CSP. Checked by naming
	// the constructs that can load a resource, rather than by banning the
	// substring "http" — the SVG namespace URI is spelled with one and is
	// never dereferenced by a browser.
	for _, forbidden := range []string{"<link", "<script src", "@import", "url(http", "src=\"//", "src=\"http"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("page reaches outside itself: found %q", forbidden)
		}
	}
	for _, occurrence := range strings.Split(page, "http")[1:] {
		if !strings.HasPrefix(occurrence, "://www.w3.org/2000/svg") {
			t.Errorf("unexpected external URL in page: http%.40s", occurrence)
		}
	}
	if strings.Contains(page, htmlDataPlaceholder) {
		t.Error("placeholder survived substitution, so the page carries no data")
	}
}

func TestRenderHTMLEmbedsTheSubgraphVerbatim(t *testing.T) {
	sub := sampleSubgraph()
	page, err := RenderHTML(sub)
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}

	// Pull the payload back out of the page and decode it: the page is only
	// useful if what it carries round-trips to the Subgraph that went in.
	payload := payloadOf(t, page)
	var got Subgraph
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, payload)
	}
	if len(got.Nodes) != len(sub.Nodes) || len(got.Edges) != len(sub.Edges) {
		t.Fatalf("payload has %d nodes/%d edges, want %d/%d",
			len(got.Nodes), len(got.Edges), len(sub.Nodes), len(sub.Edges))
	}
	// Counts alone would not notice a renderer that kept the right number of
	// nodes but corrupted what is in them, so compare the fields the page
	// actually draws from.
	for i, want := range sub.Nodes {
		if got.Nodes[i].ID != want.ID || got.Nodes[i].Name != want.Name ||
			got.Nodes[i].Type != want.Type || got.Nodes[i].Depth != want.Depth {
			t.Errorf("node %d = %+v, want %+v", i, got.Nodes[i], want)
		}
	}
	for i, want := range sub.Edges {
		if got.Edges[i].FromID != want.FromID || got.Edges[i].ToID != want.ToID ||
			got.Edges[i].Type != want.Type {
			t.Errorf("edge %d = %+v, want %+v", i, got.Edges[i], want)
		}
	}
	if got.RootID != sub.RootID {
		t.Errorf("payload root = %q, want %q", got.RootID, sub.RootID)
	}
	if got.TotalNodes != sub.TotalNodes {
		t.Errorf("payload total_nodes = %d, want %d", got.TotalNodes, sub.TotalNodes)
	}
	if !got.Edges[1].Derived {
		t.Error("derived flag was lost, so the page cannot draw that edge dashed")
	}
	if len(got.Nodes[2].Layers) != 1 {
		t.Error("layer membership was lost")
	}
}

// The injection site is the whole security surface of this format, so it gets
// a test of its own: a hostile entity name must not be able to close the
// <script> element that holds it.
func TestRenderHTMLNeutralisesScriptClosingInNames(t *testing.T) {
	hostile := "</script><img src=x onerror=alert(1)>"
	page, err := RenderHTML(Subgraph{
		Nodes: []GraphNode{{ID: "x", Name: hostile, Type: EntityTypeTopic}},
	})
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}

	// The template itself closes two script elements. A name that added a
	// third would have escaped its container.
	if n := strings.Count(page, "</script>"); n != 2 {
		t.Errorf("page has %d </script> tags, want 2 — the payload broke out", n)
	}
	if strings.Contains(page, hostile) {
		t.Error("hostile name appears unescaped in the page")
	}
	// ...and it must still be *recoverable*, escaped, rather than dropped or
	// mangled. The two checks above are both satisfied by a renderer that
	// silently discarded the name, so the property worth pinning is that the
	// escaping is lossless: the name the page carries decodes back to the one
	// that went in.
	var got Subgraph
	if err := json.Unmarshal([]byte(payloadOf(t, page)), &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("payload has %d nodes, want 1", len(got.Nodes))
	}
	if got.Nodes[0].Name != hostile {
		t.Errorf("escaping was not lossless: payload decodes to %q, want %q",
			got.Nodes[0].Name, hostile)
	}
}

func TestRenderHTMLIsDeterministic(t *testing.T) {
	first, err := RenderHTML(sampleSubgraph())
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	second, err := RenderHTML(sampleSubgraph())
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if first != second {
		t.Error("two renders of an unchanged graph differ, so output is not diffable")
	}
}

func TestRenderHTMLEmptyAndTruncated(t *testing.T) {
	page, err := RenderHTML(Subgraph{})
	if err != nil {
		t.Fatalf("RenderHTML(empty): %v", err)
	}
	if !strings.Contains(page, "<!doctype html>") || !strings.Contains(page, "</html>") {
		t.Error("empty render is not a whole page")
	}

	// Truncation has to reach the page, because it is the one thing a reader
	// cannot infer from the picture.
	page, err = RenderHTML(Subgraph{Truncated: true, TotalNodes: 5000})
	if err != nil {
		t.Fatalf("RenderHTML(truncated): %v", err)
	}
	var got Subgraph
	if err := json.Unmarshal([]byte(payloadOf(t, page)), &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if !got.Truncated {
		t.Error("truncation flag did not reach the page")
	}
}

func TestRenderGraphDispatchesHTML(t *testing.T) {
	out, err := RenderGraph(sampleSubgraph(), FormatHTML)
	if err != nil {
		t.Fatalf("RenderGraph(html): %v", err)
	}
	if !strings.HasPrefix(out, "<!doctype html>") {
		t.Errorf("RenderGraph(html) did not return a page:\n%.80s", out)
	}
}

// payloadOf returns the JSON text held in the page's data element.
func payloadOf(t *testing.T, page string) string {
	t.Helper()
	const open = `<script type="application/json" id="kg-data">`
	start := strings.Index(page, open)
	if start < 0 {
		t.Fatal("page has no data element")
	}
	start += len(open)
	end := strings.Index(page[start:], "</script>")
	if end < 0 {
		t.Fatal("data element is not closed")
	}
	return page[start : start+end]
}
