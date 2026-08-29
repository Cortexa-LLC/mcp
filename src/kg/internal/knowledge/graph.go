package knowledge

import (
	"fmt"
	"sort"
	"strings"
)

// Graph rendering — the data half. Renderers live in graph_render.go.
//
// A whole project's nodes and edges are loaded in one pass and traversed in
// memory rather than walked hop-by-hop through Cypher. Two reasons, in order
// of importance:
//
//   - The traversal becomes a pure function over an adjacency list, so depth,
//     filtering and truncation are testable without a database at all.
//   - A hop-by-hop walk costs one query per frontier node. Loading everything
//     costs two queries total, and the graphs kg builds are small enough
//     (thousands of nodes) that holding one in memory is cheaper than the
//     round trips would be.
//
// The tradeoff is that a graph too large for memory would need the walk
// pushed back into Cypher. Nothing here assumes it never will be: LoadGraph is
// the only part that touches the store.

// GraphNode is one entity as it appears in a rendered graph. It carries only
// what a renderer needs — the full entity stays in the store.
type GraphNode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Depth int    `json:"depth"` // hops from the root; 0 when there is no root

	// Layers names the scopes this node was read from, set only by a federated
	// load. More than one means the same (name, type) was found in several
	// layers and joined into this node.
	Layers []string `json:"layers,omitempty"`
}

// GraphEdge is one directed relation between two nodes in a Subgraph. Both
// endpoints are guaranteed to be present in the Subgraph's Nodes.
type GraphEdge struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Type   string `json:"type"`
}

// Graph is a whole project's entities and relations held in memory.
type Graph struct {
	nodes map[string]GraphNode
	// out and in are adjacency lists keyed by entity ID. An edge appears in
	// both, so a bidirectional walk reads one map per direction rather than
	// scanning every edge.
	out map[string][]GraphEdge
	in  map[string][]GraphEdge
	// edgeCount is the number of distinct relations loaded, not the sum of the
	// adjacency lists (which counts each edge twice).
	edgeCount int
}

// Direction controls which way a traversal follows edges.
type Direction string

const (
	// DirectionBoth follows relations in either direction — the default, and
	// the one that answers "what is this connected to".
	DirectionBoth Direction = "both"
	// DirectionOut follows only outgoing relations: what this entity depends
	// on, calls, or contains.
	DirectionOut Direction = "out"
	// DirectionIn follows only incoming relations: what depends on, calls, or
	// contains this entity.
	DirectionIn Direction = "in"
)

// ParseDirection validates a --direction flag value.
func ParseDirection(s string) (Direction, error) {
	switch Direction(strings.ToLower(strings.TrimSpace(s))) {
	case DirectionBoth, "":
		return DirectionBoth, nil
	case DirectionOut:
		return DirectionOut, nil
	case DirectionIn:
		return DirectionIn, nil
	default:
		return "", fmt.Errorf("unknown direction %q: want both, out, or in", s)
	}
}

// GraphOptions selects the part of a Graph to render.
type GraphOptions struct {
	// RootID is where the walk starts. Empty means "the whole graph", capped
	// by MaxNodes.
	RootID string
	// Depth is how many hops from the root to follow. Ignored without a root.
	Depth int
	// Direction is which way edges are followed. The zero value walks both.
	Direction Direction
	// NodeTypes limits which entity types are visited. Empty means all types.
	// The root is always included even when its type is excluded — asking for
	// a root and getting a graph without it in would be a surprise.
	NodeTypes []string
	// RelTypes limits which relation labels are followed. Empty means all.
	// Because filtered relations are not followed, this constrains
	// reachability and not just what is drawn.
	RelTypes []string
	// MaxNodes caps the result. Zero or negative means no cap.
	MaxNodes int
}

// Subgraph is the selected part of a Graph, ready to render. Nodes and Edges
// are ordered deterministically so that two runs over an unchanged graph
// produce byte-identical output — diffable, and safe to commit.
type Subgraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	// RootID is the node the walk started from, empty for a whole-graph
	// render. Renderers mark it.
	RootID string `json:"root_id,omitempty"`
	// Truncated reports that MaxNodes stopped the walk before it ran out of
	// reachable nodes, so the picture is incomplete.
	Truncated bool `json:"truncated"`
	// TotalNodes is how many nodes the whole project holds, for context when
	// the result is truncated or filtered.
	TotalNodes int `json:"total_nodes"`
}

// LoadGraph reads every entity and relation for one project into memory.
// Read-only: safe against a store other processes are using.
func LoadGraph(store *Store, projectID string) (*Graph, error) {
	entities, err := store.ListEntities(projectID, "")
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}

	g := &Graph{
		nodes: make(map[string]GraphNode, len(entities)),
		out:   make(map[string][]GraphEdge),
		in:    make(map[string][]GraphEdge),
	}
	for _, e := range entities {
		g.nodes[e.ID] = GraphNode{ID: e.ID, Name: e.Name, Type: e.Type}
	}

	result, err := store.QueryParams(`
		MATCH (from:Entity)-[r]->(to:Entity)
		WHERE from.project_id = $project_id
		RETURN from.id, to.id, label(r)
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query relations: %w", err)
	}
	defer result.Close()

	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next relation: %w", err)
		}
		row, err := tuple.GetAsSlice()
		tuple.Close()
		if err != nil {
			return nil, fmt.Errorf("get relation row: %w", err)
		}

		edge := GraphEdge{
			FromID: asString(row[0]),
			ToID:   asString(row[1]),
			Type:   asString(row[2]),
		}
		// An edge whose far end belongs to another project is dropped rather
		// than drawn as a dangling arrow: only from.project_id is constrained
		// above, matching GetRelations, so a cross-project relation can come
		// back with a target this graph knows nothing about.
		if _, ok := g.nodes[edge.FromID]; !ok {
			continue
		}
		if _, ok := g.nodes[edge.ToID]; !ok {
			continue
		}
		g.out[edge.FromID] = append(g.out[edge.FromID], edge)
		g.in[edge.ToID] = append(g.in[edge.ToID], edge)
		g.edgeCount++
	}

	return g, nil
}

// asString reads a Kuzu column value that is known to hold a string, treating
// an unexpected type as empty rather than failing the whole render.
func asString(v any) string {
	s, _ := v.(string)
	return s
}

// NodeCount returns how many entities the graph holds.
func (g *Graph) NodeCount() int { return len(g.nodes) }

// EdgeCount returns how many relations the graph holds.
func (g *Graph) EdgeCount() int { return g.edgeCount }

// ResolveRoot turns a user-supplied --root value into an entity ID. An exact
// ID wins, then an exact name, then a unique case-insensitive name match.
//
// Names are matched at all because the IDs kg mints are either UUIDs or
// source-derived strings like "function:src/a.go:F" — neither is something
// anyone wants to type. An ambiguous name is an error listing the candidates
// rather than an arbitrary pick.
func (g *Graph) ResolveRoot(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty root reference")
	}
	if _, ok := g.nodes[ref]; ok {
		return ref, nil
	}

	var exact, fold []GraphNode
	for _, n := range g.nodes {
		switch {
		case n.Name == ref:
			exact = append(exact, n)
		case strings.EqualFold(n.Name, ref):
			fold = append(fold, n)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = fold
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no entity with ID or name %q", ref)
	case 1:
		return matches[0].ID, nil
	default:
		sortNodes(matches)
		return "", fmt.Errorf("%q matches %d entities — use an ID instead:\n%s",
			ref, len(matches), candidateList(matches))
	}
}

// candidateList formats up to five ambiguous matches for an error message.
func candidateList(matches []GraphNode) string {
	var b strings.Builder
	for i, n := range matches {
		if i == 5 {
			fmt.Fprintf(&b, "  … and %d more\n", len(matches)-i)
			break
		}
		fmt.Fprintf(&b, "  %s (%s) %s\n", n.Name, n.Type, n.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Subgraph selects the part of the graph described by opts.
func (g *Graph) Subgraph(opts GraphOptions) (Subgraph, error) {
	if opts.RootID != "" {
		if _, ok := g.nodes[opts.RootID]; !ok {
			return Subgraph{}, fmt.Errorf("root entity %q is not in this graph", opts.RootID)
		}
	}
	dir := opts.Direction
	if dir == "" {
		dir = DirectionBoth
	}
	nodeTypes := lowerSet(opts.NodeTypes)
	relTypes := upperSet(opts.RelTypes)

	sub := Subgraph{RootID: opts.RootID, TotalNodes: len(g.nodes)}
	selected := make(map[string]GraphNode)

	// admit adds a node if it passes the type filter and there is room, and
	// reports whether it is now in the result. It decides membership only —
	// whether the walk continues *through* a node is a separate question, see
	// walk.
	admit := func(id string, depth int) bool {
		if _, ok := selected[id]; ok {
			return true
		}
		node, ok := g.nodes[id]
		if !ok {
			return false
		}
		// The root is exempt from the type filter; see GraphOptions.NodeTypes.
		if id != opts.RootID && !typeAllowed(node.Type, nodeTypes) {
			return false
		}
		if opts.MaxNodes > 0 && len(selected) >= opts.MaxNodes {
			sub.Truncated = true
			return false
		}
		node.Depth = depth
		selected[id] = node
		return true
	}

	// full reports that the node budget is spent, which is the one condition
	// that should stop the walk early: with nothing left to admit, expanding
	// further would tour the rest of the graph to no effect.
	full := func() bool {
		return opts.MaxNodes > 0 && len(selected) >= opts.MaxNodes
	}

	if opts.RootID == "" {
		g.selectAll(admit, full)
	} else {
		g.walk(opts.RootID, opts.Depth, dir, relTypes, admit, full)
	}

	sub.Nodes = make([]GraphNode, 0, len(selected))
	for _, n := range selected {
		sub.Nodes = append(sub.Nodes, n)
	}
	sortNodes(sub.Nodes)
	sub.Edges = g.edgesWithin(selected, relTypes)
	return sub, nil
}

// selectAll admits every node in the graph, in a stable order so that a
// truncated whole-graph render keeps the same nodes between runs.
func (g *Graph) selectAll(admit func(string, int) bool, full func() bool) {
	all := make([]GraphNode, 0, len(g.nodes))
	for _, n := range g.nodes {
		all = append(all, n)
	}
	sortNodes(all)
	for _, n := range all {
		// Same shape as walk: a rejected node plus an exhausted budget means
		// nothing further can be admitted, so stop rather than walk the rest
		// of the graph. Checking after the call matters — admit is what sets
		// Truncated when it finds the budget spent.
		if !admit(n.ID, 0) && full() {
			return
		}
	}
}

// walk does a breadth-first traversal from rootID out to depth hops.
//
// Neighbours are visited in sorted order, which matters for more than tidy
// output: when MaxNodes truncates the walk, the order decides which nodes
// survive, and an unstable order would mean a different picture each run.
func (g *Graph) walk(rootID string, depth int, dir Direction, relTypes map[string]bool, admit func(string, int) bool, full func() bool) {
	if !admit(rootID, 0) {
		return
	}
	visited := map[string]bool{rootID: true}
	frontier := []string{rootID}

	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		var next []string
		for _, id := range frontier {
			for _, neighbour := range g.neighbours(id, dir, relTypes) {
				if visited[neighbour] {
					continue
				}
				visited[neighbour] = true
				// Admission and reachability are separate decisions. A node the
				// type filter rejects is still a legitimate route to nodes that
				// pass it — --type documents itself as excluding nodes from the
				// drawing, not as pruning the walk the way --rel does — so the
				// traversal continues through it either way.
				if !admit(neighbour, hop) && full() {
					return
				}
				next = append(next, neighbour)
			}
		}
		frontier = next
	}
}

// neighbours returns the IDs reachable from id in one hop, sorted by the name
// of the entity they belong to so the walk order is stable and readable.
func (g *Graph) neighbours(id string, dir Direction, relTypes map[string]bool) []string {
	var ids []string
	if dir == DirectionOut || dir == DirectionBoth {
		for _, e := range g.out[id] {
			if relAllowed(e.Type, relTypes) {
				ids = append(ids, e.ToID)
			}
		}
	}
	if dir == DirectionIn || dir == DirectionBoth {
		for _, e := range g.in[id] {
			if relAllowed(e.Type, relTypes) {
				ids = append(ids, e.FromID)
			}
		}
	}
	nodes := make([]GraphNode, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, nid := range ids {
		if seen[nid] {
			continue
		}
		seen[nid] = true
		if n, ok := g.nodes[nid]; ok {
			nodes = append(nodes, n)
		}
	}
	sortNodes(nodes)
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

// edgesWithin returns every allowed edge whose endpoints are both selected.
//
// Edges are collected after node selection rather than during the walk so that
// a render also shows the relations between nodes the walk reached separately
// — the cross-links that make a neighbourhood a graph rather than a tree.
func (g *Graph) edgesWithin(selected map[string]GraphNode, relTypes map[string]bool) []GraphEdge {
	var edges []GraphEdge
	for id := range selected {
		for _, e := range g.out[id] {
			if _, ok := selected[e.ToID]; !ok {
				continue
			}
			if !relAllowed(e.Type, relTypes) {
				continue
			}
			edges = append(edges, e)
		}
	}
	sortEdges(edges, selected)
	return edges
}

// sortNodes orders nodes by depth, then name, then ID — shallow first, and
// alphabetical within a hop.
func sortNodes(nodes []GraphNode) {
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

// sortEdges orders edges the way a reader scans them: by source node name,
// then relation type, then target name, with IDs as the final tiebreak so
// duplicate names cannot make the order ambiguous.
func sortEdges(edges []GraphEdge, nodes map[string]GraphNode) {
	name := func(id string) string { return nodes[id].Name }
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if an, bn := name(a.FromID), name(b.FromID); an != bn {
			return an < bn
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if an, bn := name(a.ToID), name(b.ToID); an != bn {
			return an < bn
		}
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		return a.ToID < b.ToID
	})
}

// typeAllowed reports whether an entity type passes the node filter. An empty
// filter allows everything.
func typeAllowed(entityType string, allowed map[string]bool) bool {
	if len(allowed) == 0 {
		return true
	}
	return allowed[strings.ToLower(entityType)]
}

// relAllowed reports whether a relation label passes the relation filter. An
// empty filter allows everything.
func relAllowed(relType string, allowed map[string]bool) bool {
	if len(allowed) == 0 {
		return true
	}
	return allowed[strings.ToUpper(relType)]
}

// lowerSet builds a lookup set of lowercased, comma-split values. Entity types
// are stored lowercase, so "--type File,Function" is accepted as written.
func lowerSet(values []string) map[string]bool {
	return splitSet(values, strings.ToLower)
}

// upperSet builds a lookup set of uppercased, comma-split values. Relation
// labels are stored uppercase, so "--rel calls" is accepted as written.
func upperSet(values []string) map[string]bool {
	return splitSet(values, strings.ToUpper)
}

func splitSet(values []string, normalise func(string) string) map[string]bool {
	set := make(map[string]bool)
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				set[normalise(part)] = true
			}
		}
	}
	return set
}
