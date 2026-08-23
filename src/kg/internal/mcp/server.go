package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// maxRequestBytes bounds a single JSON-RPC request line.
//
// bufio.Reader.ReadBytes grows without limit, so before this an unterminated
// stream could drive the server to allocate until the process died -- and this
// server's stdin is whatever the host wired up, not necessarily a well-behaved MCP
// client. The cap is generous relative to real traffic (arguments here are Cypher
// queries, file paths and short prose) while still being a bound.
const maxRequestBytes = 8 << 20 // 8 MiB

// ToolHandler is a function that takes a tool call (name, arguments) and returns a result or error.
type ToolHandler func(req *ToolCallRequest) (any, error)

type Server struct {
	tools    []Tool
	handlers map[string]ToolHandler
	in       *bufio.Reader
	out      io.Writer

	// errLog receives diagnostics. This is a stdio JSON-RPC server: out carries
	// the protocol and anything else written there corrupts the session, so
	// panics and stack traces go here instead. Defaults to os.Stderr; tests
	// substitute a buffer.
	errLog io.Writer
}

func NewServer(tools []Tool, handlers map[string]ToolHandler, in *bufio.Reader, out io.Writer) *Server {
	return &Server{
		tools:    tools,
		handlers: handlers,
		in:       in,
		out:      out,
		errLog:   os.Stderr,
	}
}

func (s *Server) send(v any) {
	// A failed write means the client will never see this reply. Nothing here can
	// repair that, but it must not vanish: it is the difference between "the tool
	// misbehaved" and "the pipe closed under us".
	if err := json.NewEncoder(s.out).Encode(v); err != nil {
		s.logf("kg mcp: failed to write response: %v\n", err)
	}
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

func (s *Server) logf(format string, args ...any) {
	if s.errLog == nil {
		return
	}
	// Best-effort: if the diagnostic stream is itself broken there is nowhere
	// left to report that, and it must not take the session with it.
	_, _ = fmt.Fprintf(s.errLog, format, args...)
}

// Serve runs the MCP server loop: reads JSON-RPC requests over stdin, handles
// the standard MCP handshake (initialize / notifications/initialized / tools/list)
// and dispatches tools/call to registered handlers.
func (s *Server) Serve() error {
	for {
		line, tooLarge, readErr := s.readRequest()

		switch {
		case tooLarge:
			s.logf("kg mcp: dropped a request larger than %d bytes\n", maxRequestBytes)
			s.sendError(nil, -32600, fmt.Sprintf("Request exceeds the %d byte limit", maxRequestBytes))
		case len(bytes.TrimSpace(line)) > 0:
			// Dispatched before the error is examined so that a final request
			// arriving without a trailing newline is still answered. Returning on
			// io.EOF first silently discarded it, which looks to the client like
			// the server hung.
			s.dispatch(line)
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

// readRequest reads one newline-delimited request, refusing to buffer more than
// maxRequestBytes. An over-long request is drained up to its newline and reported
// via tooLarge, so the connection resynchronises on the next request instead of
// misparsing the tail of the oversized one as a fresh message.
//
// The returned line may be non-empty alongside a non-nil error: that is the
// unterminated final request, and the caller is expected to handle it before
// acting on the error.
func (s *Server) readRequest() (line []byte, tooLarge bool, err error) {
	var buf []byte
	for {
		// ReadSlice, not ReadBytes: it returns what is already buffered rather
		// than growing an unbounded slice, which is what makes the cap below
		// enforceable. Its result aliases the reader's buffer, so it must be
		// copied out via append before the next read.
		chunk, rerr := s.in.ReadSlice('\n')

		if !tooLarge && len(buf)+len(chunk) > maxRequestBytes {
			tooLarge = true
			buf = nil // release it; the request will not be answered
		}
		if !tooLarge {
			buf = append(buf, chunk...)
		}

		if rerr == nil {
			return buf, tooLarge, nil
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		return buf, tooLarge, rerr
	}
}

// dispatch handles one request. The recover here is the outer net: whatever goes
// wrong while handling a message, the read loop keeps running, because this process
// is an agent's MCP server and killing it ends the agent's whole session rather
// than one call.
func (s *Server) dispatch(line []byte) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      any             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		s.sendError(nil, -32600, "Malformed JSON")
		return
	}

	defer func() {
		if r := recover(); r != nil {
			s.logf("kg mcp: panic handling method %q: %v\n%s\n", req.Method, r, debug.Stack())
			if req.ID != nil {
				s.sendError(req.ID, -32603, fmt.Sprintf("Internal error handling %q: %v", req.Method, r))
			}
		}
	}()

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
		s.handleToolCall(req.ID, req.Params)

	default:
		// Ignore notifications (no ID); return error for requests.
		if req.ID != nil {
			s.sendError(req.ID, -32601, "Unknown method: "+req.Method)
		}
	}
}

func (s *Server) handleToolCall(id any, rawParams json.RawMessage) {
	var params CallToolParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		s.sendError(id, -32600, "Invalid params: "+err.Error())
		return
	}
	handler, ok := s.handlers[params.Name]
	if !ok {
		s.sendError(id, -32601, "Unknown tool: "+params.Name)
		return
	}

	resp, err := s.callHandler(handler, &ToolCallRequest{Name: params.Name, Arguments: params.Arguments})
	if err != nil {
		s.sendResult(id, CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
		return
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
	s.sendResult(id, CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	})
}

// callHandler invokes a tool handler and converts a panic into an ordinary tool
// error, so one bad row or one nil map costs the caller a single failed tool call
// instead of the session.
//
// The graph readers this server fronts scan Kuzu result rows, where a NULL column
// arrives as a nil interface; handlers are dispatched synchronously from the read
// loop, so without this the panic unwinds straight through Serve and out of main.
// The stack goes to errLog rather than to the client: the client gets a message it
// can act on, and whoever is debugging gets the trace on stderr.
func (s *Server) callHandler(handler ToolHandler, req *ToolCallRequest) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("kg mcp: panic in tool %q: %v\n%s\n", req.Name, r, debug.Stack())
			resp = nil
			err = fmt.Errorf("internal error in tool %q: %v (stack trace on the kg MCP server's stderr)", req.Name, r)
		}
	}()
	return handler(req)
}
