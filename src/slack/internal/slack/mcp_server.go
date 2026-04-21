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
func RunMCPServer(client *Client) error {
	tools := []MCPTool{
		{
			Name:        "list_channels",
			Description: "List all Slack channels you have access to",
			InputSchema: jsonSchema(map[string]string{}, ""),
		},
		{
			Name:        "get_thread",
			Description: "Get full thread/conversation by channel ID and thread timestamp",
			InputSchema: jsonSchema(map[string]string{
				"channel_id": "string",
				"thread_ts":  "string",
			}, "channel_id", "thread_ts"),
		},
		{
			Name:        "get_channel_history",
			Description: "Get recent messages from a channel",
			InputSchema: jsonSchema(map[string]string{
				"channel_id": "string",
				"limit":      "integer",
			}, "channel_id"),
		},
		{
			Name:        "search_messages",
			Description: "Search for messages across all channels",
			InputSchema: jsonSchema(map[string]string{
				"query": "string",
				"limit": "integer",
			}, "query"),
		},
		{
			Name:        "get_channel_info",
			Description: "Get information about a specific channel",
			InputSchema: jsonSchema(map[string]string{
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

			result, err := handleToolCall(name, arguments, client)
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

func handleToolCall(name string, args map[string]interface{}, client *Client) (string, error) {
	switch name {
	case "list_channels":
		return handleListChannels(client)
	case "get_thread":
		return handleGetThread(args, client)
	case "get_channel_history":
		return handleGetChannelHistory(args, client)
	case "search_messages":
		return handleSearchMessages(args, client)
	case "get_channel_info":
		return handleGetChannelInfo(args, client)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func handleListChannels(client *Client) (string, error) {
	channels, err := client.GetConversations()
	if err != nil {
		return "", fmt.Errorf("list channels: %w", err)
	}

	var output strings.Builder
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

func handleGetThread(args map[string]interface{}, client *Client) (string, error) {
	channelID, _ := args["channel_id"].(string)
	threadTS, _ := args["thread_ts"].(string)

	if channelID == "" || threadTS == "" {
		return "", fmt.Errorf("channel_id and thread_ts are required")
	}

	messages, err := client.GetConversationReplies(channelID, threadTS)
	if err != nil {
		return "", fmt.Errorf("get thread: %w", err)
	}

	return formatMessages(messages, client)
}

func handleGetChannelHistory(args map[string]interface{}, client *Client) (string, error) {
	channelID, _ := args["channel_id"].(string)
	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit == 0 {
		limit = 20
	}

	if channelID == "" {
		return "", fmt.Errorf("channel_id is required")
	}

	history, err := client.GetConversationHistory(channelID, limit)
	if err != nil {
		return "", fmt.Errorf("get history: %w", err)
	}

	return formatMessages(history.Messages, client)
}

func handleSearchMessages(args map[string]interface{}, client *Client) (string, error) {
	query, _ := args["query"].(string)
	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit == 0 {
		limit = 20
	}

	if query == "" {
		return "", fmt.Errorf("query is required")
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

func handleGetChannelInfo(args map[string]interface{}, client *Client) (string, error) {
	channelID, _ := args["channel_id"].(string)

	if channelID == "" {
		return "", fmt.Errorf("channel_id is required")
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
