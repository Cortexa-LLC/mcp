package knowledge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/mcp"
)

// testStore creates a temporary Store for testing and returns a cleanup function.
func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("testStore: OpenStore failed: %v", err)
	}
	return store, func() { store.Close() }
}

func TestGetPreflightContext(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()
	projectID := "testproj"
	if _, err := store.CreateEntity("TestEntity", "file", projectID); err != nil {
		t.Fatalf("CreateEntity failed: %v", err)
	}

	in := new(bytes.Buffer)
	out := new(bytes.Buffer)

	tools := []mcp.Tool{
		{
			Name:        "get_preflight_context",
			Description: "",
			InputSchema: map[string]interface{}{"task": "string"},
		},
	}
	handlers := map[string]mcp.ToolHandler{
		"get_preflight_context": func(req *mcp.ToolCallRequest) (any, error) {
			task, _ := req.Arguments["task"].(string)
			entities, err := store.KeywordSearch(projectID, task, 16)
			if err != nil {
				return nil, err
			}
			res := "---\nRelevant Knowledge Entities for Task\n---\n"
			for _, e := range entities {
				if e.Entity != nil {
					res += "- " + e.Entity.Name + " (" + e.Entity.Type + ")\n"
				}
			}
			return res, nil
		},
	}
	server := mcp.NewServer(tools, handlers, bufio.NewReader(in), out)

	// Encode a callTool JSON-RPC request into the input buffer.
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "get_preflight_context",
			"arguments": map[string]interface{}{"task": "TestEntity"},
		},
	}
	if err := json.NewEncoder(in).Encode(msg); err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}

	// Serve processes the buffer and returns when it reaches EOF.
	if err := server.Serve(); err != nil {
		t.Fatalf("server.Serve() error: %v", err)
	}

	// The server writes a capabilities announcement first, then the tool response.
	// Decode both messages and find the one with "result".
	dec := json.NewDecoder(out)
	var result string
	for dec.More() {
		var response map[string]interface{}
		if err := dec.Decode(&response); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if r, ok := response["result"]; ok {
			// result is a CallToolResult: {"content":[{"type":"text","text":"..."}]}
			if resultMap, ok := r.(map[string]interface{}); ok {
				if content, ok := resultMap["content"].([]interface{}); ok && len(content) > 0 {
					if block, ok := content[0].(map[string]interface{}); ok {
						result, _ = block["text"].(string)
					}
				}
			}
			if result != "" {
				break
			}
		}
	}

	if result == "" {
		t.Fatalf("expected non-empty result from get_preflight_context")
	}
	if !bytes.Contains([]byte(result), []byte("TestEntity")) {
		t.Fatalf("expected context to include entity name, got: %s", result)
	}
}
