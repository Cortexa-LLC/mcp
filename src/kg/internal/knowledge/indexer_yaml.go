package knowledge

// YAML extractor for the knowledge graph.
//
// What gets indexed
// -----------------
//   Top-level mapping keys        → "type" entities (schema fields like apiVersion,
//                                   kind, services, jobs — the document's structure)
//   Values of recognised name keys → "type" entities (e.g. kind: Deployment → "Deployment",
//                                   metadata.name: my-app → "my-app")
//
// "Recognised name keys" are keys that typically contain a meaningful identifier:
// kind, name, image, module, resource, service, stage, job, component, target.
//
// Extensibility
// -------------
//   - Add more "name keys" → extend yamlNameKeys below
//   - Parse sequences (e.g. Helm values) → inspect block_sequence nodes

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/yaml"
)

// yamlNameKeys is the set of YAML mapping keys whose VALUES are worth indexing
// as named entities (e.g. `kind: Deployment` → entity "Deployment").
var yamlNameKeys = map[string]bool{
	"kind":      true,
	"name":      true,
	"image":     true,
	"module":    true,
	"resource":  true,
	"service":   true,
	"stage":     true,
	"job":       true,
	"component": true,
	"target":    true,
	"plugin":    true,
	"extends":   true,
}

// processYAMLFile parses one YAML file with tree-sitter and indexes:
//   - Top-level mapping keys as "type" entities (document structure)
//   - Values of yamlNameKeys at any depth as "type" entities
func (idx *Indexer) processYAMLFile(
	absPath, relPath string,
	entities *[]entityRecord,
	seenEntities map[string]bool,
	relations *[]relationRecord,
	stats *IndexStats,
) error {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", absPath, err)
	}

	parser := sitter.NewParser()
	parser.SetLanguage(yaml.GetLanguage())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", absPath, err)
	}
	defer tree.Close()

	now := time.Now().UTC()

	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seenEntities, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	emit := func(name string) {
		if !yamlIsSignificant(name) {
			return
		}
		eid := fmt.Sprintf("type:%s:%s", relPath, name)
		if writeEntity(entities, seenEntities, eid, name, EntityTypeType, idx.projectID, now) {
			stats.EntitiesCreated++
		}
		*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
		stats.RelationsCreated++
	}

	root := tree.RootNode()
	yamlWalk(root, src, emit)
	return nil
}

// yamlWalk recursively traverses the YAML parse tree:
//   - At depth 1 (directly under stream→document→block_node→block_mapping),
//     indexes each block_mapping_pair key as a "type" entity
//   - At any depth, if a block_mapping_pair key matches yamlNameKeys,
//     indexes the pair's value as a "type" entity
func yamlWalk(node *sitter.Node, src []byte, emit func(string)) {
	if node == nil {
		return
	}
	ntype := node.Type()
	if ntype == "block_mapping" {
		// Check if this is the root-level mapping (parent chain: stream→document→block_node)
		isRoot := yamlIsRootMapping(node)
		for i := 0; i < int(node.NamedChildCount()); i++ {
			pair := node.NamedChild(i)
			if pair.Type() != "block_mapping_pair" {
				continue
			}
			key, val := yamlPairKeyValue(pair, src)
			// Always emit root-level keys (document structure)
			if isRoot && key != "" {
				emit(key)
			}
			// Emit value when key is a recognised "name" key
			if yamlNameKeys[strings.ToLower(key)] && val != "" {
				emit(val)
			}
		}
	}
	// Recurse
	for i := 0; i < int(node.NamedChildCount()); i++ {
		yamlWalk(node.NamedChild(i), src, emit)
	}
}

// yamlIsRootMapping returns true if node is the top-level block_mapping,
// i.e. its grandparent chain is block_node → document → stream.
func yamlIsRootMapping(node *sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	gp := parent.Parent()
	if gp == nil {
		return false
	}
	ggp := gp.Parent()
	if ggp == nil {
		return false
	}
	return parent.Type() == "block_node" && gp.Type() == "document" && ggp.Type() == "stream"
}

// yamlPairKeyValue extracts the key and value plain-text strings from a
// block_mapping_pair node.  Returns ("", "") for complex/nested values.
func yamlPairKeyValue(pair *sitter.Node, src []byte) (key, val string) {
	// block_mapping_pair children: key_node (flow_node), value_node (flow_node or block_node)
	children := pair.NamedChildCount()
	if children < 1 {
		return
	}
	keyNode := pair.NamedChild(0)
	key = yamlScalarText(keyNode, src)

	if children >= 2 {
		valNode := pair.NamedChild(1)
		// Only capture scalar values (block_node means nested mapping/sequence)
		if valNode.Type() == "flow_node" {
			val = yamlScalarText(valNode, src)
		}
	}
	return
}

// yamlScalarText returns the plain text of a flow_node containing a scalar.
// Returns "" for nodes without a string_scalar or integer_scalar descendant.
func yamlScalarText(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	// DFS looking for string_scalar or integer_scalar leaf
	var find func(*sitter.Node) string
	find = func(n *sitter.Node) string {
		t := n.Type()
		if t == "string_scalar" || t == "integer_scalar" || t == "float_scalar" {
			return strings.TrimSpace(n.Content(src))
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if v := find(n.NamedChild(i)); v != "" {
				return v
			}
		}
		return ""
	}
	return find(node)
}

// yamlIsSignificant returns true for YAML values worth indexing.
// Filters out: short tokens, purely numeric values, known generic words.
func yamlIsSignificant(s string) bool {
	if len(s) < 3 {
		return false
	}
	// Skip purely numeric values
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return false
	}
	// Skip common YAML structural keywords that are not real entity names
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off":
		return false
	}
	// Skip values that look like version strings (v1, apps/v1, v1beta1)
	if strings.HasPrefix(s, "v") && len(s) <= 8 {
		return false
	}
	return true
}
