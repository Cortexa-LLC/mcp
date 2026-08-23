package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// rpcResponse is the subset of a JSON-RPC reply these tests care about.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// runServer feeds input to a server over stdin and returns everything it wrote to
// the protocol stream, everything it wrote to its diagnostic stream, and Serve's
// return value.
func runServer(t *testing.T, input string, handlers map[string]ToolHandler) (replies []rpcResponse, protocolOut string, diag string, serveErr error) {
	t.Helper()

	var out, errLog bytes.Buffer
	tools := []Tool{{Name: "query_graph", InputSchema: map[string]any{}}}
	s := NewServer(tools, handlers, bufio.NewReader(strings.NewReader(input)), &out)
	s.errLog = &errLog

	serveErr = s.Serve()

	protocolOut = out.String()
	for _, l := range strings.Split(strings.TrimSpace(protocolOut), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("server wrote a non-JSON line to the protocol stream: %q (%v)", l, err)
		}
		replies = append(replies, r)
	}
	return replies, protocolOut, errLog.String(), serveErr
}

func toolCall(id int, name string) string {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": map[string]any{}},
	})
	return string(b)
}

// TestServe_PanicInHandlerBecomesToolError asserts the property: a panicking tool
// costs the caller that one tool call. The session must survive, which is why the
// second request is the real assertion -- a test that only checked the error
// response would pass against a server that then died.
func TestServe_PanicInHandlerBecomesToolError(t *testing.T) {
	handlers := map[string]ToolHandler{
		"boom": func(*ToolCallRequest) (any, error) {
			var m map[string]string
			// Deliberate: a nil-map write is a realistic runtime panic, and the
			// point of the test is that it does not end the session.
			m["nil map write"] = "x" //nolint:staticcheck // SA5000 is the scenario under test
			return nil, nil
		},
		"fine": func(*ToolCallRequest) (any, error) { return "still here", nil },
	}

	input := toolCall(1, "boom") + "\n" + toolCall(2, "fine") + "\n"
	replies, _, diag, err := runServer(t, input, handlers)
	if err != nil {
		t.Fatalf("Serve returned an error after a handler panicked: %v", err)
	}

	if len(replies) != 2 {
		t.Fatalf("expected a reply to both requests, got %d; the panic ended the session "+
			"instead of failing one tool call", len(replies))
	}

	// The panicking call is reported as a failed tool call, not a dead server.
	var first CallToolResult
	if replies[0].Result == nil {
		t.Fatalf("expected a tool result for the panicking call, got error %+v", replies[0].Error)
	}
	if err := json.Unmarshal(replies[0].Result, &first); err != nil {
		t.Fatalf("unmarshal first result: %v", err)
	}
	if !first.IsError {
		t.Errorf("expected the panicking tool call to be reported with isError=true, got %+v", first)
	}
	if len(first.Content) == 0 || !strings.Contains(first.Content[0].Text, "boom") {
		t.Errorf("expected the error text to name the tool that panicked, got %+v", first.Content)
	}

	// The next request is served normally.
	var second CallToolResult
	if err := json.Unmarshal(replies[1].Result, &second); err != nil {
		t.Fatalf("unmarshal second result: %v", err)
	}
	if second.IsError || len(second.Content) == 0 || second.Content[0].Text != "still here" {
		t.Errorf("the request after the panic was not served normally: %+v", second)
	}

	if !strings.Contains(diag, "panic") {
		t.Errorf("expected the panic to be logged for diagnosis, got diagnostics: %q", diag)
	}
}

// TestServe_PanicDiagnosticsStayOffTheProtocolStream: this is a stdio JSON-RPC
// server, so stdout belongs to the protocol. A stack trace written there would
// desynchronise the client -- the diagnostic has to go somewhere else to be useful.
func TestServe_PanicDiagnosticsStayOffTheProtocolStream(t *testing.T) {
	handlers := map[string]ToolHandler{
		"boom": func(*ToolCallRequest) (any, error) { panic("sentinel-panic-marker") },
	}

	_, protocolOut, diag, _ := runServer(t, toolCall(1, "boom")+"\n", handlers)

	if strings.Contains(protocolOut, "goroutine") || strings.Contains(protocolOut, ".go:") {
		t.Errorf("a stack trace was written to the JSON-RPC stream, which corrupts the session:\n%s", protocolOut)
	}
	if !strings.Contains(diag, "sentinel-panic-marker") {
		t.Errorf("the panic value was not recorded on the diagnostic stream; got %q", diag)
	}
	if !strings.Contains(diag, "goroutine") {
		t.Errorf("expected a stack trace on the diagnostic stream so the panic can be located; got %q", diag)
	}
	// runServer already asserts every protocol line parses as JSON, which is the
	// stronger form of the same claim.
}

// TestServe_FinalRequestWithoutNewlineIsAnswered. A client that writes its last
// request and closes the pipe without a trailing newline is well-formed JSON-RPC
// over stdio; dropping that request looks to the caller like the server hung.
func TestServe_FinalRequestWithoutNewlineIsAnswered(t *testing.T) {
	handlers := map[string]ToolHandler{
		"fine": func(*ToolCallRequest) (any, error) { return "answered", nil },
	}

	// Note: no trailing "\n".
	replies, _, _, err := runServer(t, toolCall(7, "fine"), handlers)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(replies) != 1 {
		t.Fatalf("the final request was discarded because the stream ended without a newline; got %d replies", len(replies))
	}
	var res CallToolResult
	if err := json.Unmarshal(replies[0].Result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "answered" {
		t.Errorf("unexpected result for the unterminated final request: %+v", res)
	}
}

// TestServe_OversizedRequestIsBoundedAndResynchronises asserts two things at once,
// because either alone is insufficient: the server must not buffer an unbounded
// line, and after refusing one it must still parse the next request rather than
// treating the tail of the oversized line as a new message.
func TestServe_OversizedRequestIsBoundedAndResynchronises(t *testing.T) {
	handlers := map[string]ToolHandler{
		"fine": func(*ToolCallRequest) (any, error) { return "recovered", nil },
	}

	// A single line comfortably over the cap, containing text that would itself
	// parse as a request if the reader lost its place.
	huge := `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"fine","arguments":{"q":"` +
		strings.Repeat("A", maxRequestBytes+1024) + `"}}}`

	input := huge + "\n" + toolCall(2, "fine") + "\n"
	replies, _, _, err := runServer(t, input, handlers)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if len(replies) != 2 {
		t.Fatalf("expected a rejection then a normal reply, got %d replies", len(replies))
	}
	if replies[0].Error == nil {
		t.Errorf("expected the oversized request to be rejected with an error, got result %s", replies[0].Result)
	}

	// Resynchronisation: the following request is served, and served once.
	var res CallToolResult
	if replies[1].Result == nil {
		t.Fatalf("the request after the oversized one was not served: %+v", replies[1])
	}
	if err := json.Unmarshal(replies[1].Result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "recovered" {
		t.Errorf("unexpected result after the oversized request: %+v", res)
	}
}

// TestReadRequest_DoesNotBufferBeyondCap pins the bound directly, at the level
// where it is enforced. The end-to-end test above can only observe the rejection;
// this one observes that nothing oversized was retained.
func TestReadRequest_DoesNotBufferBeyondCap(t *testing.T) {
	line := strings.Repeat("B", maxRequestBytes*2) + "\n"
	s := NewServer(nil, nil, bufio.NewReader(strings.NewReader(line+"after\n")), &bytes.Buffer{})

	got, tooLarge, err := s.readRequest()
	if err != nil {
		t.Fatalf("readRequest: %v", err)
	}
	if !tooLarge {
		t.Fatalf("a %d byte line was not reported as oversized (cap is %d)", len(line), maxRequestBytes)
	}
	if len(got) != 0 {
		t.Errorf("readRequest retained %d bytes of an oversized request; it must not buffer it", len(got))
	}

	next, tooLarge, err := s.readRequest()
	if err != nil {
		t.Fatalf("readRequest (second): %v", err)
	}
	if tooLarge {
		t.Fatal("the request after an oversized one was misreported as oversized")
	}
	if strings.TrimSpace(string(next)) != "after" {
		t.Errorf("reader lost its place after an oversized request: got %q", strings.TrimSpace(string(next)))
	}
}

// TestServe_MalformedJSONDoesNotEndTheSession keeps the pre-existing contract
// honest through the refactor.
func TestServe_MalformedJSONDoesNotEndTheSession(t *testing.T) {
	handlers := map[string]ToolHandler{
		"fine": func(*ToolCallRequest) (any, error) { return "ok", nil },
	}
	replies, _, _, err := runServer(t, "{not json\n"+toolCall(3, "fine")+"\n", handlers)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("expected an error then a normal reply, got %d", len(replies))
	}
	if replies[0].Error == nil {
		t.Error("expected an error reply for malformed JSON")
	}
}

// TestServe_BlankLinesAreIgnored: a blank line is not a request and must not draw a
// "Malformed JSON" reply, which would confuse a client that pads its stream.
func TestServe_BlankLinesAreIgnored(t *testing.T) {
	replies, _, _, err := runServer(t, "\n\n   \n", nil)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(replies) != 0 {
		t.Errorf("expected no replies to blank lines, got %d: %+v", len(replies), replies)
	}
}
