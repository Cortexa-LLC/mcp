package knowledge

import (
	"sort"
	"strings"
)

// Cross-layer package linking — deriving real dependency edges between layers.
//
// Federated layers are genuinely disconnected: relations live inside one
// database and no indexer writes one across repositories. Joining identities
// by name bridges them, but measurement showed that bridge is made of
// coincidences — Foundation, CodingKeys, print, map — and not of dependencies.
//
// A dependency is recoverable without inventing anything, because the indexers
// already record both ends: layer A holds an `import` entity naming
// com.depop.auth.client.AuthClient, and layer B holds a `package` entity named
// com.depop.auth.client. That is a real edge, and it is a relation rather than
// an identity, so it is drawn as one and marked derived.
//
// Measured on a 61-layer estate: exact name matching finds 71 cross-layer
// links, nearly all of them generic fragments like "auth" and "clients".
// Longest-prefix matching with a three-segment floor finds 845 unambiguous
// ones, and they read as a dependency map. See docs/kg-graph-linking-design.md.

// minPackageSegments is how specific a package name must be before an import
// may resolve to it. Without a floor, "com.depop" claims every JVM import in
// the estate and the graph gains one hub node instead of a dependency map.
const minPackageSegments = 3

// packageSeparator is the namespace separator prefix matching understands.
//
// Only dotted namespaces resolve today. Not one package entity in the measured
// estate had a "/" in its name — the indexers do not mint package entities for
// npm or Go module paths — so slash-style imports have nothing to resolve
// against. That is a gap in what is indexed, not in this matching rule.
const packageSeparator = "."

// LinkReport records what package linking derived, and what it refused to.
type LinkReport struct {
	// Derived is the number of DEPENDS_ON edges added.
	Derived int `json:"derived"`
	// Ambiguous counts imports that matched a package name defined in more
	// than one layer. Those are skipped rather than guessed at; on a real
	// estate this discards more matches than it keeps.
	Ambiguous int `json:"ambiguous"`
	// AmbiguousNames samples the package names responsible, worst first.
	AmbiguousNames []string `json:"ambiguous_names,omitempty"`
	// SameLayer counts imports that resolved to a package in their own layer.
	// That structure is already in the layer's own graph, so no edge is added.
	SameLayer int `json:"same_layer"`
}

// packageTarget is one package name's presence across the merged graph.
type packageTarget struct {
	// nodeID is the package node an import should point at. Meaningful only
	// when the name resolves to a single layer.
	nodeID string
	layers map[string]bool
}

// LinkPackages derives DEPENDS_ON edges from imports to the packages they
// name, and adds them to g. Pure with respect to the store: it reads only the
// merged graph, so it is testable without a database.
func LinkPackages(g *Graph) LinkReport {
	report := LinkReport{}
	targets := packageIndex(g)
	if len(targets) == 0 {
		return report
	}

	ambiguous := map[string]bool{}

	// Imports are visited in ID order so that the derived edges — and the
	// samples in the report — are the same on every run.
	for _, node := range sortedNodesOfType(g, EntityTypeImport) {
		name, target := resolvePackage(node.Name, targets)
		if target == nil {
			continue
		}

		if len(target.layers) > 1 {
			report.Ambiguous++
			ambiguous[name] = true
			continue
		}

		// An import that only ever appears in the layer defining the package
		// is describing that layer's own structure, which its graph already
		// holds.
		if !crossesLayer(node.Layers, target.layers) {
			report.SameLayer++
			continue
		}

		edge := GraphEdge{
			FromID:  node.ID,
			ToID:    target.nodeID,
			Type:    RelDependsOn,
			Derived: true,
		}
		if edge.FromID == edge.ToID {
			continue
		}
		g.out[edge.FromID] = append(g.out[edge.FromID], edge)
		g.in[edge.ToID] = append(g.in[edge.ToID], edge)
		g.edgeCount++
		report.Derived++
	}

	report.AmbiguousNames = make([]string, 0, len(ambiguous))
	for name := range ambiguous {
		report.AmbiguousNames = append(report.AmbiguousNames, name)
	}
	sort.Strings(report.AmbiguousNames)
	if len(report.AmbiguousNames) > 10 {
		report.AmbiguousNames = report.AmbiguousNames[:10]
	}
	return report
}

// packageIndex collects every package entity by name, with the layers it is
// defined in. A name can arrive as several nodes when the join guard kept them
// apart, which is itself a form of ambiguity and is recorded as such.
func packageIndex(g *Graph) map[string]*packageTarget {
	targets := make(map[string]*packageTarget)
	for _, node := range g.nodes {
		if !strings.EqualFold(node.Type, EntityTypePackage) {
			continue
		}
		if segments(node.Name) < minPackageSegments {
			continue
		}
		target, ok := targets[node.Name]
		if !ok {
			target = &packageTarget{nodeID: node.ID, layers: map[string]bool{}}
			targets[node.Name] = target
		}
		// Lowest node ID wins, so the chosen target does not depend on map
		// iteration order.
		if node.ID < target.nodeID {
			target.nodeID = node.ID
		}
		for _, layer := range node.Layers {
			target.layers[layer] = true
		}
		if len(node.Layers) == 0 {
			// A non-federated graph has no layer provenance; treat the node's
			// own presence as one layer so single-database use is well-defined.
			target.layers[""] = true
		}
	}
	return targets
}

// resolvePackage finds the longest package name that prefixes importName,
// returning the name and its target. Longest-first means
// com.depop.auth.client beats com.depop.auth, which is the specific answer.
//
// The loop starts at len(parts), not len(parts)-1: a string is a prefix of
// itself, and an import equal to a package name is the ordinary shape of a
// Java wildcard import. extractImportPath keeps only the scoped_identifier and
// drops the trailing asterisk, so `import com.depop.auth.client.*;` arrives as
// "com.depop.auth.client" — exactly the package name. Testing only proper
// prefixes dropped every one of those silently. (Kotlin escaped it by accident:
// its extractor keeps the ".*" verbatim, leaving a longer string.)
func resolvePackage(importName string, targets map[string]*packageTarget) (string, *packageTarget) {
	parts := strings.Split(importName, packageSeparator)
	for cut := len(parts); cut >= minPackageSegments; cut-- {
		candidate := strings.Join(parts[:cut], packageSeparator)
		if target, ok := targets[candidate]; ok {
			return candidate, target
		}
	}
	return "", nil
}

// crossesLayer reports whether the import is used anywhere the package is not
// defined — the condition that makes the dependency a cross-layer one.
func crossesLayer(importLayers []string, packageLayers map[string]bool) bool {
	if len(importLayers) == 0 {
		return false
	}
	for _, layer := range importLayers {
		if !packageLayers[layer] {
			return true
		}
	}
	return false
}

// segments counts the namespace segments in a package name.
func segments(name string) int {
	if name == "" {
		return 0
	}
	return strings.Count(name, packageSeparator) + 1
}

// sortedNodesOfType returns every node of one type in ID order.
func sortedNodesOfType(g *Graph, entityType string) []GraphNode {
	var nodes []GraphNode
	for _, node := range g.nodes {
		if strings.EqualFold(node.Type, entityType) {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}
