package knowledge

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/mcp"
)

// newPersonalStore creates a personal store and returns its path.
func newPersonalStore(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("create personal store: %v", err)
	}
	store.Close()
	return dbPath
}

func toolNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func call(t *testing.T, handlers map[string]mcp.ToolHandler, name string, args map[string]interface{}) (any, error) {
	t.Helper()
	handler, ok := handlers[name]
	if !ok {
		t.Fatalf("no handler registered for %s", name)
	}
	return handler(&mcp.ToolCallRequest{Name: name, Arguments: args})
}

// With no personal store, no personal tools exist at all.
func TestPersonalTools_NotRegisteredWithoutStore(t *testing.T) {
	tools, handlers := personalTools(PersonalConfig{})
	if len(tools) != 0 || len(handlers) != 0 {
		t.Errorf("expected no tools without a personal store, got %v", toolNames(tools))
	}

	// A path without a project ID is also incomplete.
	tools, _ = personalTools(PersonalConfig{DBPath: "/tmp/whatever.db"})
	if len(tools) != 0 {
		t.Errorf("expected no tools without a project ID, got %v", toolNames(tools))
	}
}

// The read tool is registered whenever a store exists; the write tool is not,
// unless writes are explicitly enabled.
func TestPersonalTools_WriteToolGatedOnAllowWrites(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal"}

	tools, handlers := personalTools(cfg)
	names := toolNames(tools)
	if !hasName(names, "search_personal_knowledge") {
		t.Errorf("read tool should always be registered, got %v", names)
	}
	if hasName(names, "add_personal_knowledge") {
		t.Error("write tool must not be registered unless writes are enabled")
	}
	if _, ok := handlers["add_personal_knowledge"]; ok {
		t.Error("write handler must not exist unless writes are enabled")
	}

	cfg.AllowWrites = true
	tools, handlers = personalTools(cfg)
	names = toolNames(tools)
	if !hasName(names, "add_personal_knowledge") {
		t.Errorf("write tool should be registered when enabled, got %v", names)
	}
	if _, ok := handlers["add_personal_knowledge"]; !ok {
		t.Error("write handler should exist when enabled")
	}
}

// A write must carry the user's own request; an agent acting unprompted has
// nothing to put in that field.
func TestAddPersonalKnowledge_RequiresUserRequest(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal", AllowWrites: true}
	_, handlers := personalTools(cfg)

	_, err := call(t, handlers, "add_personal_knowledge", map[string]interface{}{
		"title":   "unprompted note",
		"content": "something the agent decided to save on its own",
	})
	if err == nil {
		t.Fatal("expected a write without user_request to be refused")
	}
	if !strings.Contains(err.Error(), "user_request") {
		t.Errorf("error should name the missing field, got: %v", err)
	}
}

func TestAddPersonalKnowledge_RejectsUserRequestCopiedFromContent(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal", AllowWrites: true}
	_, handlers := personalTools(cfg)

	_, err := call(t, handlers, "add_personal_knowledge", map[string]interface{}{
		"title":        "lazy note",
		"content":      "identical text",
		"user_request": "identical text",
	})
	if err == nil {
		t.Fatal("expected a write whose user_request duplicates content to be refused")
	}
}

func TestAddPersonalKnowledge_RejectsOversizedContent(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal", AllowWrites: true}
	_, handlers := personalTools(cfg)

	_, err := call(t, handlers, "add_personal_knowledge", map[string]interface{}{
		"title":        "transcript dump",
		"content":      strings.Repeat("x", personalMaxContentBytes+1),
		"user_request": "save this thread",
	})
	if err == nil {
		t.Fatal("expected oversized content to be refused")
	}
	if !strings.Contains(err.Error(), "transcript") {
		t.Errorf("error should explain what to record instead, got: %v", err)
	}
}

func TestAddPersonalKnowledge_RequiresTitleAndContent(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal", AllowWrites: true}
	_, handlers := personalTools(cfg)

	for _, args := range []map[string]interface{}{
		{"title": "", "content": "body", "user_request": "remember this"},
		{"title": "heading", "content": "  ", "user_request": "remember this"},
	} {
		if _, err := call(t, handlers, "add_personal_knowledge", args); err == nil {
			t.Errorf("expected refusal for args %v", args)
		}
	}
}

// An accepted write stores the content, records provenance, and is findable
// through the read tool.
func TestAddPersonalKnowledge_StoresContentWithProvenance(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal", AllowWrites: true}
	_, handlers := personalTools(cfg)

	result, err := call(t, handlers, "add_personal_knowledge", map[string]interface{}{
		"title":        "kafka-retention",
		"type":         "decision",
		"content":      "7-day retention on events: replay window beats storage cost.",
		"user_request": "remember this decision in my personal knowledge",
	})
	if err != nil {
		t.Fatalf("add_personal_knowledge: %v", err)
	}

	// The response tells the user how to review and undo it.
	message, _ := result.(string)
	for _, want := range []string{"kg personal review", "kg personal forget"} {
		if !strings.Contains(message, want) {
			t.Errorf("response should mention %q, got: %s", want, message)
		}
	}

	found, err := call(t, handlers, "search_personal_knowledge", map[string]interface{}{
		"query": "retention",
	})
	if err != nil {
		t.Fatalf("search_personal_knowledge: %v", err)
	}
	results, ok := found.([]*SearchResult)
	if !ok {
		t.Fatalf("unexpected search result type %T", found)
	}
	if len(results) == 0 {
		t.Fatal("entry written through MCP is not searchable")
	}

	var entry *SearchResult
	for _, r := range results {
		if r.Entity.Name == "kafka-retention" {
			entry = r
		}
	}
	if entry == nil {
		t.Fatalf("expected to find kafka-retention, got %d results", len(results))
	}
	if entry.Entity.Type != "decision" {
		t.Errorf("type = %q, want \"decision\"", entry.Entity.Type)
	}

	var sawContent, sawProvenance bool
	for _, obs := range entry.Observations {
		if strings.Contains(obs.Content, "replay window") {
			sawContent = true
		}
		if strings.Contains(obs.Content, PersonalViaMCPMarker) &&
			strings.Contains(obs.Content, "remember this decision") {
			sawProvenance = true
		}
	}
	if !sawContent {
		t.Error("content observation missing")
	}
	if !sawProvenance {
		t.Error("provenance observation missing — an agent-written entry must be identifiable")
	}
}

// The default entity type keeps entries usable when the caller omits it.
func TestAddPersonalKnowledge_DefaultsType(t *testing.T) {
	cfg := PersonalConfig{DBPath: newPersonalStore(t), ProjectID: "personal", AllowWrites: true}
	_, handlers := personalTools(cfg)

	if _, err := call(t, handlers, "add_personal_knowledge", map[string]interface{}{
		"title":        "untyped entry",
		"content":      "some distilled knowledge",
		"user_request": "remember this",
	}); err != nil {
		t.Fatalf("add_personal_knowledge: %v", err)
	}

	store, err := OpenStoreReadOnly(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	entities, err := store.ListEntities("personal", "note")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(entities) != 1 {
		t.Fatalf("expected 1 entity of default type \"note\", got %d", len(entities))
	}
}

// The read tool is scoped to the personal store's project ID, so it cannot
// return entities belonging to a project graph that shares the database.
func TestSearchPersonalKnowledge_ScopedToPersonalProjectID(t *testing.T) {
	dbPath := newPersonalStore(t)

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.CreateEntity("personalNote", "note", "personal"); err != nil {
		t.Fatalf("create personal entity: %v", err)
	}
	if _, err := store.CreateEntity("someProjectNote", "note", "other-project"); err != nil {
		t.Fatalf("create foreign entity: %v", err)
	}
	store.Close()

	_, handlers := personalTools(PersonalConfig{DBPath: dbPath, ProjectID: "personal"})
	found, err := call(t, handlers, "search_personal_knowledge", map[string]interface{}{"query": "Note"})
	if err != nil {
		t.Fatalf("search_personal_knowledge: %v", err)
	}
	results, _ := found.([]*SearchResult)
	for _, r := range results {
		if r.Entity.ProjectID != "personal" {
			t.Errorf("leaked an entity scoped to %q", r.Entity.ProjectID)
		}
	}
}
