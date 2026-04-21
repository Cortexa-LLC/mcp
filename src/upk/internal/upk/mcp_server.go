package upk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/kglib"
)

// MCPTool represents an MCP tool definition
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPRequest represents an incoming MCP request
type MCPRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// MCPResponse represents an outgoing MCP response
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents an error in MCP protocol
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RunMCPServer runs the upk MCP server over stdio
func RunMCPServer(dbPath string, cfg *Config) error {
	// JSON schema helper
	jsonSchema := func(props map[string]string, required ...string) map[string]interface{} {
		properties := map[string]interface{}{}
		for k, typ := range props {
			properties[k] = map[string]string{"type": typ}
		}
		req := make([]string, len(required))
		copy(req, required)
		return map[string]interface{}{
			"type":       "object",
			"properties": properties,
			"required":   req,
		}
	}

	tools := []MCPTool{
		{
			Name:        "add_conversation",
			Description: "Record a conversation with topics, participants, and learnings",
			InputSchema: jsonSchema(map[string]string{
				"title":   "string",
				"summary": "string",
			}, "title", "summary"),
		},
		{
			Name:        "add_learning",
			Description: "Record a learning or insight from conversations, reading, or work",
			InputSchema: jsonSchema(map[string]string{
				"content": "string",
				"source":  "string",
			}, "content"),
		},
		{
			Name:        "search_knowledge",
			Description: "Search across user knowledge and optionally project codebases",
			InputSchema: jsonSchema(map[string]string{
				"query": "string",
				"limit": "integer",
			}, "query"),
		},
	}

	// Helper to open store with upk schema
	openStore := func() (*kglib.Store, error) {
		schemaCfg := &kglib.SchemaConfig{
			AdditionalRelTypes: AllRelTypes,
		}
		return kglib.OpenStore(dbPath, schemaCfg)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()

		var req MCPRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		var resp MCPResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "initialize":
			resp.Result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]string{
					"name":    "upk",
					"version": "0.1.0",
				},
				"capabilities": map[string]interface{}{
					"tools": map[string]bool{},
				},
			}

		case "notifications/initialized":
			continue

		case "tools/list":
			resp.Result = map[string]interface{}{
				"tools": tools,
			}

		case "tools/call":
			params := req.Params
			name, _ := params["name"].(string)
			arguments, _ := params["arguments"].(map[string]interface{})

			result, err := handleToolCall(name, arguments, openStore)
			if err != nil {
				resp.Error = &MCPError{
					Code:    -32603,
					Message: err.Error(),
				}
			} else {
				resp.Result = map[string]interface{}{
					"content": []map[string]interface{}{
						{
							"type": "text",
							"text": result,
						},
					},
				}
			}

		default:
			resp.Error = &MCPError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			}
		}

		respJSON, _ := json.Marshal(resp)
		fmt.Println(string(respJSON))
	}

	return scanner.Err()
}

func handleToolCall(name string, args map[string]interface{}, openStore func() (*kglib.Store, error)) (string, error) {
	switch name {
	case "add_conversation":
		return handleAddConversation(args, openStore)
	case "add_learning":
		return handleAddLearning(args, openStore)
	case "search_knowledge":
		return handleSearchKnowledge(args, openStore)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func handleAddConversation(args map[string]interface{}, openStore func() (*kglib.Store, error)) (string, error) {
	title, _ := args["title"].(string)
	summary, _ := args["summary"].(string)

	store, err := openStore()
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	// Create conversation entity
	conv, err := store.CreateEntity(title, EntityTypeConversation, "user")
	if err != nil {
		return "", fmt.Errorf("create conversation: %w", err)
	}

	// Add summary as observation
	if _, err := store.CreateObservation(conv.ID, summary, "user"); err != nil {
		return "", fmt.Errorf("add summary: %w", err)
	}

	return fmt.Sprintf("Created conversation %s (%s)", title, conv.ID), nil
}

func handleAddLearning(args map[string]interface{}, openStore func() (*kglib.Store, error)) (string, error) {
	content, _ := args["content"].(string)
	source, _ := args["source"].(string)

	store, err := openStore()
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	// Create learning entity
	learning, err := store.CreateEntity(content, EntityTypeLearning, "user")
	if err != nil {
		return "", fmt.Errorf("create learning: %w", err)
	}

	// Add source as observation if provided
	if source != "" {
		if _, err := store.CreateObservation(learning.ID, fmt.Sprintf("Source: %s", source), "user"); err != nil {
			return "", fmt.Errorf("add source: %w", err)
		}
	}

	return fmt.Sprintf("Created learning %s", learning.ID), nil
}

func handleSearchKnowledge(args map[string]interface{}, openStore func() (*kglib.Store, error)) (string, error) {
	query, _ := args["query"].(string)
	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit == 0 {
		limit = 10
	}

	store, err := openStore()
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	results, err := store.KeywordSearch("user", query, limit)
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		return "No results found", nil
	}

	output := fmt.Sprintf("Found %d results:\n\n", len(results))
	for i, result := range results {
		output += fmt.Sprintf("%d. %s (%s) [score: %.2f]\n", i+1, result.Entity.Name, result.Entity.Type, result.Score)
		if len(result.Observations) > 0 {
			for _, obs := range result.Observations {
				output += fmt.Sprintf("   - %s\n", obs.Content)
			}
		}
		output += "\n"
	}

	return output, nil
}
