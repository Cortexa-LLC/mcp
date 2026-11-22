package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// observationsOn returns the content of every observation hanging off the
// named entity. Queried through the database rather than the in-memory records
// so the test covers the bulk-load path the indexer actually writes through.
func observationsOn(t *testing.T, store *Store, name, entityType string) []string {
	t.Helper()
	q := fmt.Sprintf(`
		MATCH (e:Entity {name: "%s", type: "%s"})-[:HAS_OBSERVATION]->(o:Observation)
		RETURN o.content`, name, entityType)
	result, err := store.Query(q)
	if err != nil {
		t.Fatalf("query observations for %q: %v", name, err)
	}
	defer result.Close()

	var out []string
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			t.Fatalf("result.Next: %v", err)
		}
		content, _ := row.GetValue(uint64(0))
		if s, ok := content.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// joinedObservationsOn is observationsOn flattened for substring assertions.
func joinedObservationsOn(t *testing.T, store *Store, name, entityType string) string {
	t.Helper()
	return strings.Join(observationsOn(t, store, name, entityType), "\n---\n")
}

// TestIndexer_MarkdownProse is the regression test for markdown files reaching
// the graph as headings only. The prose under a heading is where a design
// document's reasoning lives; indexing the heading alone makes the graph able
// to answer "is there a section about retention" and unable to answer "what is
// the retention policy", which is the question anyone actually asks.
func TestIndexer_MarkdownProse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `# Retention Design

This document explains why observations are never deleted on re-index.

## Journal Replay

Hand-written knowledge is replayed from the journal after indexing finishes,
because a re-index clears every code-derived row first and would otherwise
destroy notes that exist nowhere else in the repository.

### Ordering Constraint

Replay has to run after the clear, not before it.

#### Deep Detail

An H4 heading creates no topic of its own, so this paragraph belongs to the
enclosing H3 section and must still be retrievable from it.

## Empty Section

## Open Questions

Whether the journal should be compacted is still undecided.
`
	if err := os.WriteFile(filepath.Join(srcDir, "retention.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A second file whose prose starts above any heading. In retention.md the
	// lead paragraph follows the H1 and therefore belongs to that topic, so it
	// cannot exercise the preamble path at all.
	preamble := `An introduction with no heading above it, stating what this
document is for before any section begins.

# First Heading

Body text under the first heading.
`
	if err := os.WriteFile(filepath.Join(srcDir, "preamble.md"), []byte(preamble), 0o644); err != nil {
		t.Fatalf("WriteFile preamble: %v", err)
	}

	store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

	// Section prose lands on the heading's topic entity.
	journal := joinedObservationsOn(t, store, "Journal Replay", EntityTypeTopic)
	if !strings.Contains(journal, "replayed from the journal after indexing finishes") {
		t.Errorf("H2 section prose missing from its topic; got %q", journal)
	}
	if strings.Contains(journal, "still undecided") {
		t.Error("section body leaked across a following heading boundary")
	}

	// A section ends at the next H1-H3 heading, so an H4 subsection's prose
	// belongs to the enclosing topic — this is how detail below the indexed
	// heading levels stays reachable at all.
	ordering := joinedObservationsOn(t, store, "Ordering Constraint", EntityTypeTopic)
	if !strings.Contains(ordering, "Replay has to run after the clear") {
		t.Errorf("H3 section prose missing; got %q", ordering)
	}
	if !strings.Contains(ordering, "belongs to the\nenclosing H3 section") {
		t.Errorf("prose under an H4 did not land on the enclosing H3 topic; got %q", ordering)
	}

	// The lead paragraph sits under the H1, so it belongs to that topic.
	design := joinedObservationsOn(t, store, "Retention Design", EntityTypeTopic)
	if !strings.Contains(design, "never deleted on re-index") {
		t.Errorf("H1 section prose missing from its topic; got %q", design)
	}

	// Prose above any heading belongs to the document itself, since there is
	// no section to attach it to and it is routinely the only statement of
	// what the document is for.
	file := joinedObservationsOn(t, store, "preamble.md", EntityTypeFile)
	if !strings.Contains(file, "stating what this\ndocument is for") {
		t.Errorf("preamble prose missing from the file entity; got %q", file)
	}
	if strings.Contains(file, "Body text under the first heading") {
		t.Error("preamble observation swallowed the first section's body")
	}

	// A heading with nothing under it must not manufacture an observation:
	// empty notes are indistinguishable from real ones at search time.
	if obs := observationsOn(t, store, "Empty Section", EntityTypeTopic); len(obs) != 0 {
		t.Errorf("empty section produced %d observations: %q", len(obs), obs)
	}

	// The heading entities themselves are unchanged behaviour, asserted here so
	// a regression in the rewritten walk shows up in this test too.
	if !entityExistsByName(t, store, "Retention Design", EntityTypeTopic) {
		t.Error("expected H1 topic entity")
	}
	if entityExistsByName(t, store, "Deep Detail", EntityTypeTopic) {
		t.Error("H4 must not become a topic entity")
	}
}

// TestIndexer_MarkdownProseSurvivesReindex guards the interaction with
// selective clearing. Markdown observations are indexer-written, so a re-index
// must clear and rebuild them rather than accumulate a second copy — the
// behaviour hand-written observations deliberately do NOT have.
func TestIndexer_MarkdownProseSurvivesReindex(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mdPath := filepath.Join(srcDir, "notes.md")
	body := "# Scope Rules\n\nA scope names one database and the layers it federates with.\n"
	if err := os.WriteFile(mdPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	store, _ := runIndexer(t, srcDir, dbPath)

	first := observationsOn(t, store, "Scope Rules", EntityTypeTopic)
	if len(first) != 1 {
		t.Fatalf("expected 1 observation after first index, got %d: %q", len(first), first)
	}

	idx, err := NewIndexer(store, "test-project", srcDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("re-index: %v", err)
	}

	second := observationsOn(t, store, "Scope Rules", EntityTypeTopic)
	if len(second) != 1 {
		t.Errorf("re-index changed observation count to %d (want 1, duplicated prose): %q",
			len(second), second)
	}
}

// TestChunkTextLabeled_SingleChunkUnprefixed pins the property the label
// parameter exists for: text that fits is returned verbatim, and only split
// text carries the ordinal prefix. A prefix on every chunk would be embedded
// along with the prose of almost every observation in the graph.
func TestChunkTextLabeled_SingleChunkUnprefixed(t *testing.T) {
	text := "A short section body."
	chunks := chunkTextLabeled(text, 2000, "section")
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("single chunk was altered: got %q, want %q", chunks[0], text)
	}

	long := strings.Repeat("line of prose\n", 40) // ~560 bytes
	split := chunkTextLabeled(long, 100, "section")
	if len(split) < 2 {
		t.Fatalf("expected the text to split, got %d chunk(s)", len(split))
	}
	if !strings.HasPrefix(split[0], "[section 1] ") {
		t.Errorf("split chunk missing its label: %q", split[0])
	}
	if strings.Contains(split[0], "PDF") {
		t.Errorf("markdown chunk carries the PDF label: %q", split[0])
	}
}
