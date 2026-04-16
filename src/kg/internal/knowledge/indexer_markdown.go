package knowledge

// Markdown extractor for the knowledge graph.
//
// The tree-sitter-markdown grammar uses a dual-tree model (block + inline trees)
// that is incompatible with the generic processWithTreeSitter pipeline.
// This file provides a standalone processMarkdownFile that uses the grammar's
// own markdown.ParseCtx / MarkdownTree.Iter API.
//
// What gets indexed
// -----------------
//   Headings H1–H3       → "topic" entities (architectural concepts / sections)
//   Mermaid code blocks  → "type" entities  (component / node labels extracted
//                          from graph/flowchart/sequence diagrams)
//
// Adding support for more Markdown constructs is typically a one-line change:
//   - New heading level threshold  → change the `level > 3` guard in processMarkdownFile
//   - New diagram language         → add to mdDiagramLangs map
//   - New node-label syntax        → extend mermaidNodeRe in extractMermaidEntities

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/markdown"
)

// mdDiagramLangs lists fenced-code-block language tags whose content should be
// parsed for component/entity names. Keys are lowercase.
var mdDiagramLangs = map[string]bool{
	"mermaid": true,
}

// mermaidNodeRe matches labeled mermaid nodes:
//
//	ID[Label]         →  bracket label (most common)
//	ID((Label))       →  circle / rounded
//	ID{Label}         →  diamond / decision
//	ID([Label])       →  stadium / database
//	ID[[Label]]       →  subroutine / subprocess
//
// Only the label text (first capture group) is used; the ID is discarded.
var mermaidNodeRe = regexp.MustCompile(
	`[A-Za-z_][A-Za-z0-9_]*` + // node ID (discarded)
		`(?:` +
		`\[\[([^\[\]]+)\]\]` + // [[label]]   — capture group 1
		`|\[([^\[\]]+)\]` + //   [label]    — capture group 2
		`|\(\(([^)]+)\)\)` + //   ((label))  — capture group 3
		`|\(\[([^\]]+)\]\)` + //   ([label])  — capture group 4
		`|\{([^}]+)\}` + //       {label}    — capture group 5
		`)`,
)

// processMarkdownFile parses one Markdown file and emits heading topics and
// mermaid diagram component entities into the shared CSV writers.
func (idx *Indexer) processMarkdownFile(
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

	now := time.Now().UTC()

	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seenEntities, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	tree, err := markdown.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", absPath, err)
	}

	tree.Iter(func(node *markdown.Node) bool {
		switch node.Type() {
		case "atx_heading":
			level := mdHeadingLevel(node.Node)
			if level < 1 || level > 3 {
				break
			}
			text := mdHeadingText(node.Node, src)
			if text == "" || !mdIsSignificant(text) {
				break
			}
			eid := fmt.Sprintf("topic:%s:%s", relPath, text)
			if writeEntity(entities, seenEntities, eid, text, EntityTypeTopic, idx.projectID, now) {
				stats.EntitiesCreated++
			}
			*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
			stats.RelationsCreated++

		case "fenced_code_block":
			lang, content := mdExtractCodeBlock(node.Node, src)
			if !mdDiagramLangs[strings.ToLower(lang)] {
				break
			}
			for _, name := range extractMermaidEntities(content) {
				eid := fmt.Sprintf("type:%s:%s", relPath, name)
				if writeEntity(entities, seenEntities, eid, name, EntityTypeType, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}
		}
		return true
	})

	return nil
}

// mdHeadingLevel returns the ATX heading level (1–6) by inspecting the marker
// child type of an atx_heading node. Returns 0 if the level cannot be determined.
func mdHeadingLevel(node *sitter.Node) int {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		switch node.NamedChild(i).Type() {
		case "atx_h1_marker":
			return 1
		case "atx_h2_marker":
			return 2
		case "atx_h3_marker":
			return 3
		case "atx_h4_marker":
			return 4
		case "atx_h5_marker":
			return 5
		case "atx_h6_marker":
			return 6
		}
	}
	return 0
}

// mdHeadingText returns the trimmed text content of the inline child of an
// atx_heading node. Returns "" if no inline child is found.
func mdHeadingText(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		if node.NamedChild(i).Type() == "inline" {
			return strings.TrimSpace(node.NamedChild(i).Content(src))
		}
	}
	return ""
}

// mdExtractCodeBlock returns the language tag and body content of a
// fenced_code_block node by iterating its named children.
func mdExtractCodeBlock(node *sitter.Node, src []byte) (lang, content string) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		switch child.Type() {
		case "info_string":
			for j := 0; j < int(child.NamedChildCount()); j++ {
				lc := child.NamedChild(j)
				if lc.Type() == "language" {
					lang = lc.Content(src)
				}
			}
		case "code_fence_content":
			content = child.Content(src)
		}
	}
	return
}

// extractMermaidEntities scans mermaid diagram source and returns the set of
// distinct node label strings found in node definitions.
func extractMermaidEntities(content string) []string {
	var names []string
	seen := make(map[string]bool)

	for _, match := range mermaidNodeRe.FindAllStringSubmatch(content, -1) {
		// Pick the first non-empty capture group (groups 1–5 map to label variants)
		for _, g := range match[1:] {
			if g == "" {
				continue
			}
			name := strings.TrimSpace(g)
			if !mdIsSignificant(name) || strings.Contains(name, "://") {
				break
			}
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			break
		}
	}
	return names
}

// mdIsSignificant returns true for heading / diagram-node text worth indexing.
// Filters out very short names and purely numeric tokens.
func mdIsSignificant(text string) bool {
	if len(text) < 3 {
		return false
	}
	for _, r := range text {
		if !unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
