package knowledge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HTML rendering — an interactive version of the same Subgraph the mermaid and
// DOT renderers draw.
//
// Mermaid and DOT both hand the picture to someone else to lay out, which is
// why they stop at a few hundred nodes: the layout engine has no idea which
// nodes the reader cares about, so it spends its effort equally on all of them.
// This format keeps the layout client-side and lets the reader steer it —
// click a node to isolate its neighbourhood, filter by type, search by name.
//
// Two properties are deliberate:
//
//   - No dependencies. The page carries its own force layout rather than
//     pulling cytoscape or d3 from a CDN, so a rendered file still works
//     offline, in five years, and inside a repo that forbids third-party
//     script origins. At neighbourhood scale (--limit defaults to 200) a
//     grid-bucketed force simulation is a few dozen lines and converges in
//     well under a second.
//   - The only part of the file that varies is one JSON payload. Everything
//     else is a fixed template. That matters because it means there is exactly
//     one injection site to reason about, and encoding/json has already
//     escaped <, > and & to <, > and & by default — so the
//     payload cannot close the <script> element that holds it. The page then
//     builds every label with textContent, never innerHTML, so entity names
//     are data on both sides of the boundary. See TestRenderHTMLNeutralises...
//
// Like the other renderers, output is deterministic: node order is already
// fixed by the Subgraph, initial positions are derived from node index rather
// than from a random seed, and nothing timestamps the file. Two runs over an
// unchanged graph produce byte-identical HTML.

// htmlDataPlaceholder is the single injection site in htmlTemplate. It is a
// JS/CSS-neutral comment so that the template remains a syntactically valid
// page even before substitution, which keeps it editable in a browser.
const htmlDataPlaceholder = "/*KG_DATA*/"

// RenderHTML renders a Subgraph as a self-contained interactive HTML page.
func RenderHTML(sub Subgraph) (string, error) {
	// Marshalled, not MarshalIndent: this is machine payload rather than
	// something a reader diffs, and the indented form triples the file size on
	// a 200-node graph for no benefit.
	payload, err := json.Marshal(sub)
	if err != nil {
		return "", fmt.Errorf("encode graph: %w", err)
	}
	if !strings.Contains(htmlTemplate, htmlDataPlaceholder) {
		// Unreachable unless the template is edited badly; a page that
		// silently carried no data would look like an empty graph.
		return "", fmt.Errorf("html template has no %s placeholder", htmlDataPlaceholder)
	}
	return strings.Replace(htmlTemplate, htmlDataPlaceholder, string(payload), 1), nil
}
