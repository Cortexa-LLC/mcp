package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// ToolHandler is a function that takes a tool call (name, arguments) and returns a result or error.
type ToolHandler func(req *ToolCallRequest) (any, error)

type Server struct {
	tools    []Tool
	handlers map[string]ToolHandler
	in       *bufio.Reader
	out      io.Writer
}

func NewServer(tools []Tool, handlers map[string]ToolHandler, in *bufio.Reader, out io.Writer) *Server {
	return &Server{
		tools:    tools,
		handlers: handlers,
		in:       in,
		out:      out,
	}
}

func (s *Server) send(v any) {
	json.NewEncoder(s.out).Encode(v)
}

func (s *Server) sendResult(id any, result any) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) sendError(id any, code int, msg string) {
	s.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	})
}

// Serve runs the MCP server loop: reads JSON-RPC requests over stdin, handles
// the standard MCP handshake (initialize / notifications/initialized / tools/list)
// and dispatches tools/call to registered handlers.
func (s *Server) Serve() error {
	for {
		line, err := s.in.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32600, "Malformed JSON")
			continue
		}

		switch req.Method {

		case "initialize":
			s.sendResult(req.ID, InitializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities: ServerCapabilities{
					Tools: &ToolsCapability{},
				},
				ServerInfo: ServerInfo{Name: "kg", Version: "1.0.0"},
			})

		case "notifications/initialized":
			// Notification — no response required.

		case "tools/list":
			s.sendResult(req.ID, ListToolsResult{Tools: s.tools})

		case "tools/call":
			var params CallToolParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				s.sendError(req.ID, -32600, "Invalid params: "+err.Error())
				continue
			}
			handler, ok := s.handlers[params.Name]
			if !ok {
				s.sendError(req.ID, -32601, "Unknown tool: "+params.Name)
				continue
			}
			resp, err := handler(&ToolCallRequest{Name: params.Name, Arguments: params.Arguments})
			if err != nil {
				s.sendResult(req.ID, CallToolResult{
					Content: []ContentBlock{{Type: "text", Text: err.Error()}},
					IsError: true,
				})
				continue
			}
			var text string
			switch v := resp.(type) {
			case string:
				text = v
			default:
				b, merr := json.Marshal(v)
				if merr != nil {
					text = fmt.Sprintf("%v", v)
				} else {
					text = string(b)
				}
			}
			s.sendResult(req.ID, CallToolResult{
				Content: []ContentBlock{{Type: "text", Text: text}},
			})

		default:
			// Ignore notifications (no ID); return error for requests.
			if req.ID != nil {
				s.sendError(req.ID, -32601, "Unknown method: "+req.Method)
			}
		}
	}
}
