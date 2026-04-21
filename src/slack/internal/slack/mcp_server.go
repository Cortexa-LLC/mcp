package slack

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	slackapi "github.com/slack-go/slack"
)

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

// MCPTool represents an MCP tool definition
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// RunMCPServer runs the Slack MCP server over stdio
func RunMCPServer(multiClient *MultiClient) error {
	tools := []MCPTool{
		{
			Name:        "list_workspaces",
			Description: "List all configured Slack workspaces",
			InputSchema: jsonSchema(map[string]string{}, ""),
		},
		{
			Name:        "list_channels",
			Description: "List all Slack channels you have access to in a workspace",
			InputSchema: jsonSchema(map[string]string{
				"workspace": "string",
			}, ""),
		},
		{
			Name:        "get_thread",
			Description: "Get full thread/conversation by channel ID and thread timestamp",
			InputSchema: jsonSchema(map[string]string{
				"workspace":  "string",
				"channel_id": "string",
				"thread_ts":  "string",
			}, "channel_id", "thread_ts"),
		},
		{
			Name:        "get_channel_history",
			Description: "Get recent messages from a channel",
			InputSchema: jsonSchema(map[string]string{
				"workspace":  "string",
				"channel_id": "string",
				"limit":      "integer",
			}, "channel_id"),
		},
		{
			Name:        "search_messages",
			Description: "Search for messages across all channels",
			InputSchema: jsonSchema(map[string]string{
				"workspace": "string",
				"query":     "string",
				"limit":     "integer",
			}, "query"),
		},
		{
			Name:        "get_channel_info",
			Description: "Get information about a specific channel",
			InputSchema: jsonSchema(map[string]string{
				"workspace":  "string",
				"channel_id": "string",
			}, "channel_id"),
		},
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
					"name":    "slack",
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

			result, err := handleToolCall(name, arguments, multiClient)
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

func jsonSchema(props map[string]string, required ...string) map[string]interface{} {
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

func handleToolCall(name string, args map[string]interface{}, multiClient *MultiClient) (string, error) {
	switch name {
	case "list_workspaces":
		return handleListWorkspaces(multiClient)
	case "list_channels":
		return handleListChannels(args, multiClient)
	case "get_thread":
		return handleGetThread(args, multiClient)
	case "get_channel_history":
		return handleGetChannelHistory(args, multiClient)
	case "search_messages":
		return handleSearchMessages(args, multiClient)
	case "get_channel_info":
		return handleGetChannelInfo(args, multiClient)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func handleListWorkspaces(multiClient *MultiClient) (string, error) {
	workspaces := multiClient.ListWorkspaces()
	defaultWS := multiClient.GetDefaultWorkspace()

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Configured workspaces (%d):\n\n", len(workspaces)))
	for _, ws := range workspaces {
		if ws == defaultWS {
			output.WriteString(fmt.Sprintf("- %s (default)\n", ws))
		} else {
			output.WriteString(fmt.Sprintf("- %s\n", ws))
		}
	}

	return output.String(), nil
}

func handleListChannels(args map[string]interface{}, multiClient *MultiClient) (string, error) {
	workspace, _ := args["workspace"].(string)

	client, err := multiClient.GetClient(workspace)
	if err != nil {
		return "", err
	}

	channels, err := client.GetConversations()
	if err != nil {
		return "", fmt.Errorf("list channels: %w", err)
	}

	wsName := workspace
	if wsName == "" {
		wsName = multiClient.GetDefaultWorkspace()
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Workspace: %s\n", wsName))
	output.WriteString(fmt.Sprintf("Found %d channels:\n\n", len(channels)))

	for _, ch := range channels {
		chType := "channel"
		if ch.IsIM {
			chType = "DM"
		} else if ch.IsGroup {
			chType = "group"
		}
		output.WriteString(fmt.Sprintf("- %s (%s) [%s]\n", ch.Name, ch.ID, chType))
		if ch.Topic.Value != "" {
			output.WriteString(fmt.Sprintf("  Topic: %s\n", ch.Topic.Value))
		}
	}

	return output.String(), nil
}

func handleGetThread(args map[string]interface{}, multiClient *MultiClient) (string, error) {
	workspace, _ := args["workspace"].(string)
	channelID, _ := args["channel_id"].(string)
	threadTS, _ := args["thread_ts"].(string)

	if channelID == "" || threadTS == "" {
		return "", fmt.Errorf("channel_id and thread_ts are required")
	}

	client, err := multiClient.GetClient(workspace)
	if err != nil {
		return "", err
	}

	messages, err := client.GetConversationReplies(channelID, threadTS)
	if err != nil {
		return "", fmt.Errorf("get thread: %w", err)
	}

	return formatMessages(messages, client)
}

func handleGetChannelHistory(args map[string]interface{}, multiClient *MultiClient) (string, error) {
	workspace, _ := args["workspace"].(string)
	channelID, _ := args["channel_id"].(string)
	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit == 0 {
		limit = 20
	}

	if channelID == "" {
		return "", fmt.Errorf("channel_id is required")
	}

	client, err := multiClient.GetClient(workspace)
	if err != nil {
		return "", err
	}

	history, err := client.GetConversationHistory(channelID, limit)
	if err != nil {
		return "", fmt.Errorf("get history: %w", err)
	}

	return formatMessages(history.Messages, client)
}

func handleSearchMessages(args map[string]interface{}, multiClient *MultiClient) (string, error) {
	workspace, _ := args["workspace"].(string)
	query, _ := args["query"].(string)
	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit == 0 {
		limit = 20
	}

	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	client, err := multiClient.GetClient(workspace)
	if err != nil {
		return "", err
	}

	results, err := client.SearchMessages(query, limit)
	if err != nil {
		return "", fmt.Errorf("search messages: %w", err)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d messages:\n\n", results.Total))

	for i, match := range results.Matches {
		output.WriteString(fmt.Sprintf("%d. %s\n", i+1, match.Text))
		output.WriteString(fmt.Sprintf("   Channel: %s | User: %s | TS: %s\n", match.Channel.Name, match.Username, match.Timestamp))
		if match.Permalink != "" {
			output.WriteString(fmt.Sprintf("   Link: %s\n", match.Permalink))
		}
		output.WriteString("\n")
	}

	return output.String(), nil
}

func handleGetChannelInfo(args map[string]interface{}, multiClient *MultiClient) (string, error) {
	workspace, _ := args["workspace"].(string)
	channelID, _ := args["channel_id"].(string)

	if channelID == "" {
		return "", fmt.Errorf("channel_id is required")
	}

	client, err := multiClient.GetClient(workspace)
	if err != nil {
		return "", err
	}

	channel, err := client.GetChannelInfo(channelID)
	if err != nil {
		return "", fmt.Errorf("get channel info: %w", err)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Channel: %s (%s)\n", channel.Name, channel.ID))
	output.WriteString(fmt.Sprintf("Members: %d\n", channel.NumMembers))
	if channel.Topic.Value != "" {
		output.WriteString(fmt.Sprintf("Topic: %s\n", channel.Topic.Value))
	}
	if channel.Purpose.Value != "" {
		output.WriteString(fmt.Sprintf("Purpose: %s\n", channel.Purpose.Value))
	}

	return output.String(), nil
}

func formatMessages(messages []slackapi.Message, client *Client) (string, error) {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Thread with %d messages:\n\n", len(messages)))

	for i, msg := range messages {
		// Get user info
		username := msg.User
		if user, err := client.GetUserInfo(msg.User); err == nil {
			username = user.RealName
			if username == "" {
				username = user.Name
			}
		}

		output.WriteString(fmt.Sprintf("%d. %s (%s):\n", i+1, username, msg.Timestamp))
		output.WriteString(fmt.Sprintf("   %s\n", msg.Text))

		// Show reactions if any
		if len(msg.Reactions) > 0 {
			output.WriteString("   Reactions: ")
			for _, r := range msg.Reactions {
				output.WriteString(fmt.Sprintf("%s:%d ", r.Name, r.Count))
			}
			output.WriteString("\n")
		}
		output.WriteString("\n")
	}

	return output.String(), nil
}
