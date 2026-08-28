package knowledge

import (
	"fmt"
	"path/filepath"
	"sort"
)

// Federated graph loading — one Graph built from a scope and every layer it
// federates with.
//
// kg's federation is search-only by design (see SearchLayer in
// kglib/federated.go): layers merge query *results*, and nothing enumerates
// entities across databases. A picture of the federated estate therefore has
// to be assembled here, and that raises a question searching never has to
// answer — when are two rows in two databases the same thing?
//
// They are joined on (name, type), with a guard. Measured against a real
// 59-layer estate, an unguarded join is actively misleading: the entities most
// widely shared between layers are markdown headings from a documentation
// template ("Service Overview", "Deployment", "Known Issues and Failure
// Modes") and Helm values keys indexed as types ("chart", "autoscaling",
// "accountName"). Joining on those fuses every unrelated service into one hub
// and the resulting graph says nothing true. A name appearing in more than
// MaxJoinLayers layers is treated as boilerplate and left unjoined — and the
// report says which names those were, so the threshold is visible rather than
// magic.

// DefaultMaxJoinLayers is the layer count above which a shared (name, type) is
// read as boilerplate rather than as one shared symbol. Three is deliberately
// low: a genuine shared symbol lives in one library and is used from a handful
// of places, while template text appears in everything.
const DefaultMaxJoinLayers = 3

// FederatedGraphOptions describes which databases to assemble into one graph.
type FederatedGraphOptions struct {
	// AIDir is the .ai directory holding the scope databases.
	AIDir string
	// Scope is the primary scope; its Layers are loaded alongside it.
	Scope *ScopeConfig
	// ProjectID is the project whose rows are read from every layer.
	ProjectID string
	// OnlyLayers restricts the load to these scope names. Empty means the
	// primary scope and all of its layers.
	OnlyLayers []string
	// MaxJoinLayers is the boilerplate guard; zero means DefaultMaxJoinLayers.
	MaxJoinLayers int
}

// LayerLoad is one layer's contribution to a federated graph.
type LayerLoad struct {
	Name   string `json:"name"`
	Nodes  int    `json:"nodes"`
	Edges  int    `json:"edges"`
	Failed string `json:"failed,omitempty"`
}

// SuppressedJoin is a (name, type) that appears in too many layers to be one
// shared symbol, and so was left unjoined.
type SuppressedJoin struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Layers int    `json:"layers"`
}

// FederationReport says what was assembled, and — more usefully — what was
// left out and why.
type FederationReport struct {
	Layers []LayerLoad `json:"layers"`
	// Joined is how many (name, type) identities were merged across layers.
	Joined int `json:"joined"`
	// MergedNodes is how many per-layer rows those joins collapsed away.
	MergedNodes int `json:"merged_nodes"`
	// Suppressed lists the boilerplate identities left unjoined, worst first.
	Suppressed []SuppressedJoin `json:"suppressed,omitempty"`
	// MaxJoinLayers is the guard that produced Suppressed.
	MaxJoinLayers int `json:"max_join_layers"`
	// IDCollisions counts nodes whose ID already existed in another layer and
	// were renamed to keep them distinct.
	IDCollisions int `json:"id_collisions,omitempty"`
}

// FailedLayers returns the layers that could not be read. A federated render
// is still useful with one database missing, so a failure is reported rather
// than fatal — but it must be reported, or the picture silently loses a layer.
func (r *FederationReport) FailedLayers() []LayerLoad {
	var failed []LayerLoad
	for _, l := range r.Layers {
		if l.Failed != "" {
			failed = append(failed, l)
		}
	}
	return failed
}

// ntKey identifies an entity across databases.
type ntKey struct{ Name, Type string }

// LoadFederatedGraph assembles one Graph from a scope and its layers.
//
// Databases are read one at a time and released before the next is opened, so
// peak memory is the merged graph plus the largest single layer rather than
// every layer at once. That costs a second pass over each database — the first
// only reads (name, type) pairs, which is what the join guard needs before any
// merging can start.
func LoadFederatedGraph(opts FederatedGraphOptions) (*Graph, *FederationReport, error) {
	if opts.Scope == nil {
		return nil, nil, fmt.Errorf("no scope given")
	}
	maxJoin := opts.MaxJoinLayers
	if maxJoin <= 0 {
		maxJoin = DefaultMaxJoinLayers
	}

	names, err := federationOrder(opts)
	if err != nil {
		return nil, nil, err
	}
	report := &FederationReport{MaxJoinLayers: maxJoin}

	// Pass one: how many layers does each (name, type) appear in?
	layerCount := make(map[ntKey]int)
	for _, name := range names {
		keys, err := layerIdentities(opts.AIDir, name, opts.ProjectID)
		if err != nil {
			// Recorded here and again in pass two; pass two's entry is the one
			// that reaches the report, so only note it and carry on.
			continue
		}
		for key := range keys {
			layerCount[key]++
		}
	}

	joinable := make(map[ntKey]bool)
	for key, count := range layerCount {
		switch {
		case count <= 1:
			// Nothing to join.
		case count <= maxJoin:
			joinable[key] = true
		default:
			report.Suppressed = append(report.Suppressed, SuppressedJoin{
				Name: key.Name, Type: key.Type, Layers: count,
			})
		}
	}
	sort.Slice(report.Suppressed, func(i, j int) bool {
		a, b := report.Suppressed[i], report.Suppressed[j]
		if a.Layers != b.Layers {
			return a.Layers > b.Layers
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Type < b.Type
	})

	// Pass two: merge, highest-priority layer first so that a joined node keeps
	// the ID of the layer that would win a federated search.
	merged := &Graph{
		nodes: make(map[string]GraphNode),
		out:   make(map[string][]GraphEdge),
		in:    make(map[string][]GraphEdge),
	}
	canonical := make(map[ntKey]string) // joined identity → the ID it kept
	seenEdge := make(map[GraphEdge]bool)

	for _, name := range names {
		load := LayerLoad{Name: name}
		layer, err := loadLayerGraph(opts.AIDir, name, opts.ProjectID)
		if err != nil {
			load.Failed = err.Error()
			report.Layers = append(report.Layers, load)
			continue
		}

		// localID maps this layer's IDs onto the merged graph's IDs. Only
		// joined nodes move; everything else keeps the ID it arrived with.
		localID := make(map[string]string, len(layer.nodes))
		for id, node := range layer.nodes {
			key := ntKey{node.Name, node.Type}
			if joinable[key] {
				if existing, ok := canonical[key]; ok {
					localID[id] = existing
					target := merged.nodes[existing]
					target.Layers = appendLayer(target.Layers, name)
					merged.nodes[existing] = target
					report.MergedNodes++
					continue
				}
			}

			// Two layers can mint the same ID for different things — the
			// indexers derive IDs from repo-relative paths, so "import:.."
			// exists in every layer that has a relative import. Reusing the ID
			// would fuse them, which is exactly what the join guard decided
			// not to do, so a collision is renamed rather than merged.
			mergedID := id
			if _, taken := merged.nodes[mergedID]; taken {
				mergedID = name + "::" + id
				report.IDCollisions++
			}

			if joinable[key] {
				canonical[key] = mergedID
				report.Joined++
			}
			localID[id] = mergedID
			node.ID = mergedID
			node.Layers = []string{name}
			merged.nodes[mergedID] = node
			load.Nodes++
		}

		for from, edges := range layer.out {
			for _, e := range edges {
				edge := GraphEdge{
					FromID: localID[from],
					ToID:   localID[e.ToID],
					Type:   e.Type,
				}
				// A join can collapse both ends of an edge onto one node. That
				// self-loop is an artifact of merging, not a relation anyone
				// recorded, so it is dropped.
				if edge.FromID == "" || edge.ToID == "" || edge.FromID == edge.ToID {
					continue
				}
				if seenEdge[edge] {
					continue
				}
				seenEdge[edge] = true
				merged.out[edge.FromID] = append(merged.out[edge.FromID], edge)
				merged.in[edge.ToID] = append(merged.in[edge.ToID], edge)
				merged.edgeCount++
				load.Edges++
			}
		}

		report.Layers = append(report.Layers, load)
	}

	// Joined nodes accumulate layers in load order; sorting makes the rendered
	// provenance stable.
	for id, node := range merged.nodes {
		if len(node.Layers) > 1 {
			sort.Strings(node.Layers)
			merged.nodes[id] = node
		}
	}

	return merged, report, nil
}

// federationOrder returns the scopes to load, highest priority first: the
// primary scope, then its layers in reverse configuration order, matching the
// priority OpenFederatedStoreWithExtra assigns them.
func federationOrder(opts FederatedGraphOptions) ([]string, error) {
	// Deduped: a name repeated in Layers (or a layer sharing the scope's own
	// name) would load the same database twice, and the second pass would then
	// hit the ID-collision rename path against its own nodes. Misconfiguration
	// rather than an expected input, but the guard is one map.
	seen := map[string]bool{opts.Scope.Name: true}
	ordered := []string{opts.Scope.Name}
	for i := len(opts.Scope.Layers) - 1; i >= 0; i-- {
		name := opts.Scope.Layers[i]
		if seen[name] {
			continue
		}
		seen[name] = true
		ordered = append(ordered, name)
	}

	if len(opts.OnlyLayers) == 0 {
		return ordered, nil
	}

	wanted := make(map[string]bool, len(opts.OnlyLayers))
	for _, name := range opts.OnlyLayers {
		wanted[name] = true
	}
	var filtered []string
	for _, name := range ordered {
		if wanted[name] {
			filtered = append(filtered, name)
			delete(wanted, name)
		}
	}
	if len(wanted) > 0 {
		unknown := make([]string, 0, len(wanted))
		for name := range wanted {
			unknown = append(unknown, name)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("scope %q does not federate with: %v", opts.Scope.Name, unknown)
	}
	return filtered, nil
}

// layerDBPath resolves one scope's database file.
func layerDBPath(aiDir, scopeName string) (string, error) {
	cfg, err := LoadScopeConfig(aiDir, scopeName)
	if err != nil {
		return "", fmt.Errorf("load scope %s: %w", scopeName, err)
	}
	return filepath.Join(aiDir, cfg.Database), nil
}

// layerIdentities reads just the (name, type) pairs one layer holds — the
// cheapest read that can answer "is this identity shared across layers".
func layerIdentities(aiDir, scopeName, projectID string) (map[ntKey]bool, error) {
	dbPath, err := layerDBPath(aiDir, scopeName)
	if err != nil {
		return nil, err
	}
	store, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", scopeName, err)
	}
	defer store.Close()

	result, err := store.QueryParams(`
		MATCH (e:Entity {project_id: $project_id})
		RETURN DISTINCT e.name, e.type
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("read identities from %s: %w", scopeName, err)
	}
	defer result.Close()

	keys := make(map[ntKey]bool)
	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("read identities from %s: %w", scopeName, err)
		}
		row, err := tuple.GetAsSlice()
		tuple.Close()
		if err != nil {
			return nil, fmt.Errorf("read identities from %s: %w", scopeName, err)
		}
		keys[ntKey{asString(row[0]), asString(row[1])}] = true
	}
	return keys, nil
}

// loadLayerGraph loads one layer's whole graph, closing the database before
// returning so that only the merged graph and one layer are ever held at once.
func loadLayerGraph(aiDir, scopeName, projectID string) (*Graph, error) {
	dbPath, err := layerDBPath(aiDir, scopeName)
	if err != nil {
		return nil, err
	}
	store, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", scopeName, err)
	}
	defer store.Close()

	return LoadGraph(store, projectID)
}

// appendLayer adds a layer name to a node's provenance, keeping it unique.
func appendLayer(layers []string, name string) []string {
	for _, existing := range layers {
		if existing == name {
			return layers
		}
	}
	return append(layers, name)
}
