package knowledge

import (
	"fmt"
	"strings"

	"github.com/cortexa-llc/mcp/kg/internal/mcp"
)

// Provenance markers written alongside every agent-recorded entry in the
// personal store. They are plain observations so that `kg search`, `kg show`,
// and `kg personal review` all surface them without special handling — an entry
// can never lose the record of how it got there.
const (
	// PersonalViaMCPMarker identifies an entry written by an agent through the
	// MCP server, as opposed to one the user wrote themselves via the CLI.
	PersonalViaMCPMarker = "[VIA:mcp]"

	// PersonalRequestMarker prefixes the user's own words that asked for the
	// entry to be saved.
	PersonalRequestMarker = "[REQUESTED]"
)

// personalMaxContentBytes caps a single agent-written entry. The personal store
// is for distilled knowledge; a cap keeps an agent from dumping a transcript
// into a graph that has no re-index to clean up after it.
const personalMaxContentBytes = 8 * 1024

// PersonalConfig describes the user-level personal knowledge store as seen by
// the MCP server.
type PersonalConfig struct {
	// DBPath is the personal store's database. Empty means there is no personal
	// store, and no personal tools are registered at all.
	DBPath string

	// ProjectID scopes entities in the personal store.
	ProjectID string

	// AllowWrites registers the write tool. Off by default: without it an agent
	// has no way to add to the personal store, however it is asked.
	AllowWrites bool
}

// Enabled reports whether a personal store is available to the server.
func (pc PersonalConfig) Enabled() bool {
	return pc.DBPath != "" && pc.ProjectID != ""
}

// personalTools returns the personal-knowledge tools and their handlers. The
// read tool appears whenever a personal store exists; the write tool only when
// writes are explicitly enabled. When no store exists, nothing is registered —
// a session with no personal store sees no personal tools.
func personalTools(cfg PersonalConfig) ([]mcp.Tool, map[string]mcp.ToolHandler) {
	if !cfg.Enabled() {
		return nil, nil
	}

	schema := func(props map[string]string, required ...string) map[string]interface{} {
		properties := map[string]interface{}{}
		for name, typ := range props {
			properties[name] = map[string]string{"type": typ}
		}
		req := make([]string, len(required))
		copy(req, required)
		return map[string]interface{}{
			"type":       "object",
			"properties": properties,
			"required":   req,
		}
	}

	withPersonalRO := func(fn func(*Store) (any, error)) (any, error) {
		store, err := OpenStoreReadOnly(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("open personal store: %w", err)
		}
		defer store.Close()
		return fn(store)
	}

	tools := []mcp.Tool{
		{
			Name: "search_personal_knowledge",
			Description: "Search the user's personal knowledge store — decisions, conversations, and learnings they have recorded across all projects, not code from this repository. " +
				"Use it when the question is about what the user already knows, decided, or discussed (\"did we decide anything about retention?\", \"what do I know about this vendor?\"), or when this project's graph has no answer. " +
				"Results are hand-written notes rather than indexed source, so treat them as the user's recollection: they may predate the current code. Use short, specific terms (1–3 words).",
			InputSchema: schema(map[string]string{"query": "string", "limit": "integer"}, "query"),
		},
	}

	handlers := map[string]mcp.ToolHandler{
		"search_personal_knowledge": func(req *mcp.ToolCallRequest) (any, error) {
			return withPersonalRO(func(s *Store) (any, error) {
				query, _ := req.Arguments["query"].(string)
				limit, _ := req.Arguments["limit"].(float64)
				if limit == 0 {
					limit = 12
				}
				return s.KeywordSearch(cfg.ProjectID, query, int(limit))
			})
		},
	}

	if !cfg.AllowWrites {
		return tools, handlers
	}

	tools = append(tools, mcp.Tool{
		Name: "add_personal_knowledge",
		Description: "Record something in the user's personal knowledge store, which persists across every project. " +
			"ONLY call this when the user has explicitly asked for it — \"remember this\", \"add this to my personal knowledge\", \"save this decision\". " +
			"Never call it on your own initiative, and never for findings about this codebase: those belong in add_observation on the project graph. " +
			"Record distilled knowledge (the decision and its reasoning), not transcripts. Summarise around personal data, credentials, and confidential detail rather than copying them. " +
			"user_request must quote the user's own words asking for this, and is stored with the entry.",
		InputSchema: schema(map[string]string{
			"title":        "string",
			"content":      "string",
			"type":         "string",
			"user_request": "string",
		}, "title", "content", "user_request"),
	})

	handlers["add_personal_knowledge"] = func(req *mcp.ToolCallRequest) (any, error) {
		title := strings.TrimSpace(argString(req, "title"))
		content := strings.TrimSpace(argString(req, "content"))
		userRequest := strings.TrimSpace(argString(req, "user_request"))
		entityType := strings.TrimSpace(argString(req, "type"))
		if entityType == "" {
			entityType = "note"
		}

		if title == "" || content == "" {
			return nil, fmt.Errorf("title and content are both required")
		}
		if len(content) > personalMaxContentBytes {
			return nil, fmt.Errorf("content is %d bytes, over the %d-byte limit for a personal entry — "+
				"record the decision and its reasoning rather than a transcript",
				len(content), personalMaxContentBytes)
		}

		// The guard that makes this tool safe to expose: the caller must supply
		// the user's own request. An agent acting on its own initiative has
		// nothing to put here.
		if userRequest == "" {
			return nil, fmt.Errorf("user_request is required: quote the words the user used to ask for this")
		}
		if userRequest == content {
			return nil, fmt.Errorf("user_request must be the user's request to save this, not a copy of the content")
		}

		store, err := OpenStore(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("open personal store: %w", err)
		}
		defer store.Close()

		entity, err := store.CreateEntity(title, entityType, cfg.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("create personal entity: %w", err)
		}
		if _, err := store.CreateObservation(entity.ID, content, cfg.ProjectID); err != nil {
			return nil, fmt.Errorf("record content: %w", err)
		}

		// Provenance, always. Distinguishes agent-written entries from the
		// user's own for the rest of the entry's life.
		provenance := fmt.Sprintf("%s %s %q", PersonalViaMCPMarker, PersonalRequestMarker, userRequest)
		if _, err := store.CreateObservation(entity.ID, provenance, cfg.ProjectID); err != nil {
			return nil, fmt.Errorf("record provenance: %w", err)
		}

		return fmt.Sprintf("Recorded %q (%s) in your personal knowledge store as %s.\n"+
			"Review with: kg personal review\nRemove with: kg personal forget %s",
			title, entityType, entity.ID, entity.ID), nil
	}

	return tools, handlers
}

// argString reads a string argument, tolerating absence.
func argString(req *mcp.ToolCallRequest, name string) string {
	v, _ := req.Arguments[name].(string)
	return v
}
