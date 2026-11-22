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
//   Section prose        → observations on the heading's topic entity, chunked
//                          like PDF text so each chunk stays inside an
//                          embedding model's context window
//   Preamble prose       → observations on the file entity (text above the
//                          first heading)
//   Mermaid code blocks  → "type" entities  (component / node labels extracted
//                          from graph/flowchart/sequence diagrams)
//
// Indexing the prose, not just the headings, is the point: a design document's
// rationale lives in its paragraphs, and a graph that holds only its table of
// contents can answer "is there a section called X" but never "why was X
// decided". Headings alone was the original behaviour and it made every
// markdown file in a repository — ADRs, CHANGELOG, CLAUDE.md — invisible to
// search below the heading line.
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
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
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

const (
	// mdChunkMaxBytes bounds one section-prose observation, matching
	// pdfChunkMaxBytes for the same reason: an observation is embedded as a
	// single text, so it has to fit an embedding model's context window.
	mdChunkMaxBytes = 2000

	// mdMinSectionLen is the shortest section body worth recording, in runes.
	// Above pdfMinChunkLen because a markdown section legitimately contains a
	// one-word body ("TBD", "None", a bare link) far more often than a page of
	// extracted PDF text does, and those carry no retrievable meaning.
	mdMinSectionLen = 32
)

// mdSection is one heading found during the block-tree walk: the topic entity
// it produced, and the byte range of the heading line itself.
//
// A section's body is everything between the end of its heading and the start
// of the next one, which is not knowable until the whole file has been walked
// — hence collecting sections first and slicing the source afterwards, rather
// than accumulating text inside the tree callback.
type mdSection struct {
	entityID  string
	headingAt int // start byte of the heading line
	bodyAt    int // end byte of the heading line; the body starts here
}

// processMarkdownFile parses one Markdown file and emits heading topics,
// mermaid diagram component entities, and one observation per chunk of the
// prose under each heading.
func (idx *Indexer) processMarkdownFile(
	absPath, relPath string,
	entities *[]entityRecord,
	seenEntities map[string]bool,
	relations *[]relationRecord,
	observations *[]obsRecord,
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

	var sections []mdSection

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

			// Recorded even when writeEntity reports the entity as already
			// seen. A file that repeats a heading still has distinct prose
			// under the second one, and it belongs on the same topic; skipping
			// it would drop the body silently.
			sections = append(sections, mdSection{
				entityID:  eid,
				headingAt: int(node.Node.StartByte()),
				bodyAt:    int(node.Node.EndByte()),
			})

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
			}
		}
		return true
	})

	// Iter walks the block tree, and nothing in its contract promises the
	// callback fires in source order. Section bodies are cut from the byte
	// offsets of adjacent headings, so an out-of-order slice would hand one
	// section another's prose, or a negative range.
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].headingAt < sections[j].headingAt
	})

	// Prose above the first heading belongs to the document, not to any
	// section: the lead paragraph of a design doc is routinely the only place
	// its purpose is stated.
	preambleEnd := len(src)
	if len(sections) > 0 {
		preambleEnd = sections[0].headingAt
	}
	idx.emitMarkdownProse(fileID, string(src[:preambleEnd]), observations, now)

	for i, sec := range sections {
		bodyEnd := len(src)
		if i+1 < len(sections) {
			bodyEnd = sections[i+1].headingAt
		}
		// A heading nested inside another block (a list item, a block quote)
		// can end after the next heading starts. Slicing that range would
		// panic; the section simply has no body of its own.
		if sec.bodyAt >= bodyEnd {
			continue
		}
		idx.emitMarkdownProse(sec.entityID, string(src[sec.bodyAt:bodyEnd]), observations, now)
	}

	return nil
}

// emitMarkdownProse chunks one section body and appends an observation per
// chunk to entityID. A heading with nothing under it produces nothing.
func (idx *Indexer) emitMarkdownProse(
	entityID, body string,
	observations *[]obsRecord,
	now time.Time,
) {
	text := normaliseWhitespace(body)
	if utf8.RuneCountInString(text) < mdMinSectionLen {
		return
	}
	for _, chunk := range chunkTextLabeled(text, mdChunkMaxBytes, "section") {
		if utf8.RuneCountInString(chunk) < mdMinSectionLen {
			continue
		}
		*observations = append(*observations, obsRecord{
			id:        uuid.NewString(),
			entityID:  entityID,
			content:   chunk,
			created:   now,
			projectID: idx.projectID,
		})
	}
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
