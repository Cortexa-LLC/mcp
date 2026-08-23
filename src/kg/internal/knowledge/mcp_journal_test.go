package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/mcp"
	"github.com/cortexa-llc/mcp/kglib"
)

// mcpTestEnv builds the real MCP handler map against a temporary project, and
// returns the handlers alongside the database path so the journal beside it can
// be inspected.
func mcpTestEnv(t *testing.T) (map[string]mcp.ToolHandler, string) {
	t.Helper()
	root := t.TempDir()
	aiDir := filepath.Join(root, ".ai")
	if err := os.MkdirAll(aiDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A source file, so index_project has something to find.
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Run() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dbPath := filepath.Join(aiDir, "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	store.Close()

	_, handlers := buildTools(aiDir, nil, "testproj", root, PersonalConfig{})
	return handlers, dbPath
}

func mcpCall(t *testing.T, handlers map[string]mcp.ToolHandler, name string, args map[string]interface{}) (any, error) {
	t.Helper()
	handler, ok := handlers[name]
	if !ok {
		t.Fatalf("no handler registered for %s", name)
	}
	return handler(&mcp.ToolCallRequest{Name: name, Arguments: args})
}

// An agent recording a finding through MCP is doing a hand-write: nothing in the
// tree can regenerate it. Each of these tools must journal, or the knowledge is
// lost at the next storage-format rebuild — and, until Phase 4, at the next
// `kg index`.
func TestMCPHandWriteToolsAreJournaled(t *testing.T) {
	handlers, dbPath := mcpTestEnv(t)

	entity, err := mcpCall(t, handlers, "add_entity", map[string]interface{}{
		"name": "retry-policy",
		"type": "decision",
	})
	if err != nil {
		t.Fatalf("add_entity: %v", err)
	}
	created, ok := entity.(*Entity)
	if !ok {
		t.Fatalf("add_entity returned %T, want *Entity", entity)
	}

	other, err := mcpCall(t, handlers, "add_entity", map[string]interface{}{
		"name": "backoff",
		"type": "decision",
	})
	if err != nil {
		t.Fatalf("add_entity: %v", err)
	}
	second := other.(*Entity)

	if _, err := mcpCall(t, handlers, "add_observation", map[string]interface{}{
		"entity_id": created.ID,
		"content":   "three attempts, then give up",
	}); err != nil {
		t.Fatalf("add_observation: %v", err)
	}

	if _, err := mcpCall(t, handlers, "link_entities", map[string]interface{}{
		"from_id":  created.ID,
		"relation": "RELATES_TO",
		"to_id":    second.ID,
	}); err != nil {
		t.Fatalf("link_entities: %v", err)
	}

	stats, err := readJournalRecords(t, dbPath)
	if err != nil {
		t.Fatalf("MCP hand-writes left no journal: %v", err)
	}
	if len(stats) != 4 {
		t.Fatalf("expected 4 journal records (2 entities, 1 observation, 1 relation), got %d: %+v",
			len(stats), stats)
	}

	byOp := map[string]int{}
	for _, rec := range stats {
		byOp[rec.Op]++
	}
	for op, want := range map[string]int{
		kglib.OpCreateEntity:      2,
		kglib.OpCreateObservation: 1,
		kglib.OpCreateRelation:    1,
	} {
		if byOp[op] != want {
			t.Errorf("journal holds %d %s records, want %d", byOp[op], op, want)
		}
	}

	// Records must name entities by (name, type) — the UUIDs the tools returned
	// do not survive a re-index.
	for _, rec := range stats {
		if rec.Entity != nil && rec.Entity.Name == "" {
			t.Errorf("record %+v has no entity name to resolve on replay", rec)
		}
	}
}

// index_project writes hundreds of thousands of derived rows that are rebuilt
// from the tree, not replayed. It must not journal, or the file whose whole
// purpose is preserving what cannot be regenerated fills up with what can.
func TestMCPIndexProjectIsNotJournaled(t *testing.T) {
	handlers, dbPath := mcpTestEnv(t)

	if _, err := mcpCall(t, handlers, "index_project", map[string]interface{}{}); err != nil {
		t.Fatalf("index_project: %v", err)
	}

	if _, err := os.Stat(kglib.JournalPath(dbPath)); !os.IsNotExist(err) {
		t.Errorf("index_project wrote a journal; stat returned %v", err)
	}
}

// The property that actually matters: knowledge an agent recorded survives the
// agent re-indexing.
//
// The earlier version of this test asserted on the journal FILE after
// index_project and passed while the graph was being wiped — indexing clears
// every row carrying the project ID, hand-written ones included, and the MCP
// path had no replay. A test that checks the artifact the code produces rather
// than the property the code promises certifies nothing.
func TestMCPIndexProjectPreservesHandWrittenKnowledge(t *testing.T) {
	handlers, dbPath := mcpTestEnv(t)

	created, err := mcpCall(t, handlers, "add_entity", map[string]interface{}{
		"name": "retry-policy",
		"type": "decision",
	})
	if err != nil {
		t.Fatalf("add_entity: %v", err)
	}
	entity := created.(*Entity)
	if _, err := mcpCall(t, handlers, "add_observation", map[string]interface{}{
		"entity_id": entity.ID,
		"content":   "three attempts, exponential backoff",
	}); err != nil {
		t.Fatalf("add_observation: %v", err)
	}

	if _, err := mcpCall(t, handlers, "index_project", map[string]interface{}{}); err != nil {
		t.Fatalf("index_project: %v", err)
	}

	// The graph, not the journal.
	store, err := OpenStoreReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenStoreReadOnly: %v", err)
	}
	defer store.Close()

	restored, err := store.GetEntityByNameAndType("retry-policy", "decision", "testproj")
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if restored == nil {
		t.Fatal("indexing through MCP destroyed the agent's hand-written entity")
	}
	observations, err := store.GetObservations(restored.ID, "testproj")
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(observations) != 1 || observations[0].Content != "three attempts, exponential backoff" {
		t.Errorf("observations = %+v, want the hand-written note", observations)
	}

	// And it is searchable, which is how an agent would actually reach it.
	found, err := store.KeywordSearch("testproj", "retry-policy", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(found) == 0 {
		t.Error("the restored entity is in the graph but not searchable")
	}
}

// The journal is still the mechanism, so it must hold exactly the hand-write
// and none of the indexed rows.
func TestMCPJournalHoldsOnlyHandWrites(t *testing.T) {
	handlers, dbPath := mcpTestEnv(t)

	if _, err := mcpCall(t, handlers, "add_entity", map[string]interface{}{
		"name": "retry-policy",
		"type": "decision",
	}); err != nil {
		t.Fatalf("add_entity: %v", err)
	}
	if _, err := mcpCall(t, handlers, "index_project", map[string]interface{}{}); err != nil {
		t.Fatalf("index_project: %v", err)
	}

	records, err := readJournalRecords(t, dbPath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("journal holds %d records after indexing, want just the hand-written one: %+v",
			len(records), records)
	}
	if records[0].Entity == nil || records[0].Entity.Name != "retry-policy" {
		t.Errorf("journal record = %+v, want the hand-written retry-policy entity", records[0])
	}
}

// readJournalRecords decodes the journal beside a database.
func readJournalRecords(t *testing.T, dbPath string) ([]kglib.JournalRecord, error) {
	t.Helper()
	data, err := os.ReadFile(kglib.JournalPath(dbPath))
	if err != nil {
		return nil, err
	}
	var records []kglib.JournalRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec kglib.JournalRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode journal line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records, nil
}
