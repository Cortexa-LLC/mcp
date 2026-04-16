package knowledge

// HTML extractor for the knowledge graph.
//
// What gets indexed
// -----------------
//   <title> text         → "topic" entity  (page title)
//   <h1>–<h3> text      → "topic" entities (section headings)
//   id="…" attributes   → "type"  entities (structural anchors / component IDs)
//
// Extensibility
// -------------
//   - Change heading depth limit → adjust the htmlHeadingTags set
//   - Index class attributes     → add handling for "class" attribute_name

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/html"
)

// htmlHeadingTags is the set of HTML heading tag names we index as topics.
var htmlHeadingTags = map[string]bool{
	"h1": true,
	"h2": true,
	"h3": true,
}

// processHTMLFile parses one HTML file with tree-sitter and indexes headings
// and element IDs as knowledge-graph entities.
func (idx *Indexer) processHTMLFile(
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
	parser.SetLanguage(html.GetLanguage())

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

	emitTopic := func(name string) {
		if !mdIsSignificant(name) {
			return
		}
		eid := fmt.Sprintf("topic:%s:%s", relPath, name)
		if writeEntity(entities, seenEntities, eid, name, EntityTypeTopic, idx.projectID, now) {
			stats.EntitiesCreated++
		}
		*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
		stats.RelationsCreated++
	}
	emitType := func(name string) {
		if !mdIsSignificant(name) {
			return
		}
		eid := fmt.Sprintf("type:%s:%s", relPath, name)
		if writeEntity(entities, seenEntities, eid, name, EntityTypeType, idx.projectID, now) {
			stats.EntitiesCreated++
		}
		*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
		stats.RelationsCreated++
	}

	htmlWalk(tree.RootNode(), src, emitTopic, emitType)
	return nil
}

// htmlWalk recursively traverses the HTML parse tree looking for:
//   - element nodes with heading tags  → emit heading text as topic
//   - element nodes with title tag     → emit title text as topic
//   - attribute nodes with id name     → emit id value as type
func htmlWalk(node *sitter.Node, src []byte, emitTopic, emitType func(string)) {
	if node == nil {
		return
	}
	switch node.Type() {
	case "element":
		tagName := htmlTagName(node, src)
		switch {
		case tagName == "title":
			if text := htmlElementText(node, src); text != "" {
				emitTopic(text)
			}
		case htmlHeadingTags[tagName]:
			if text := htmlElementText(node, src); text != "" {
				emitTopic(text)
			}
		}
	case "attribute":
		// id="value" → index the value as a structural anchor
		if name := htmlAttrName(node, src); name == "id" {
			if val := htmlAttrValue(node, src); val != "" {
				emitType(val)
			}
		}
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		htmlWalk(node.NamedChild(i), src, emitTopic, emitType)
	}
}

// htmlTagName returns the lowercase tag name of an element node.
func htmlTagName(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "start_tag" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				if child.NamedChild(j).Type() == "tag_name" {
					return strings.ToLower(child.NamedChild(j).Content(src))
				}
			}
		}
	}
	return ""
}

// htmlElementText returns the concatenated text content of an element,
// stripping child tags. Returns the first text node found (not recursive).
func htmlElementText(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "text" {
			return strings.TrimSpace(child.Content(src))
		}
	}
	return ""
}

// htmlAttrName returns the lowercase attribute name from an attribute node.
func htmlAttrName(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		if node.NamedChild(i).Type() == "attribute_name" {
			return strings.ToLower(node.NamedChild(i).Content(src))
		}
	}
	return ""
}

// htmlAttrValue returns the attribute value from an attribute node
// (strips surrounding quotes).
func htmlAttrValue(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "quoted_attribute_value" {
			for j := 0; j < int(child.NamedChildCount()); j++ {
				if child.NamedChild(j).Type() == "attribute_value" {
					return strings.TrimSpace(child.NamedChild(j).Content(src))
				}
			}
		}
	}
	return ""
}
