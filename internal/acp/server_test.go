//go:build tdd_red

package acp

// TDD RED tests for the minimum ACP v1 server (see doc.go).
//
// These tests are the acceptance criteria for the next round. They currently
// fail because Server.Serve returns ErrNotImplemented; every failure message
// names the missing behavior so the implementation round has a checklist.
//
// Run: go test -count=1 ./internal/acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeAgent is the seam a real internal/app runtime will provide next round.
// It records calls so the tests can assert the protocol layer drove it.
type fakeAgent struct {
	mu      sync.Mutex
	closed  bool
	news    []NewSessionRequest
	prompts []PromptRequest
	cancels []string
}

var _ Agent = (*fakeAgent)(nil)

func (f *fakeAgent) NewSession(_ context.Context, req NewSessionRequest) (NewSessionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.news = append(f.news, req)
	return NewSessionResponse{SessionID: "session-1"}, nil
}

func (f *fakeAgent) Prompt(_ context.Context, req PromptRequest, updates UpdateSink) (PromptResponse, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, req)
	f.mu.Unlock()
	if err := updates(SessionUpdateNotification{
		SessionID: req.SessionID,
		Update:    SessionUpdate{SessionUpdate: UpdateAgentMessageChunk, Content: &ContentBlock{Type: "text", Text: "hello from agent"}},
	}); err != nil {
		return PromptResponse{}, err
	}
	return PromptResponse{StopReason: StopReasonEndTurn}, nil
}

func (f *fakeAgent) Cancel(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, sessionID)
	return nil
}

func (f *fakeAgent) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(Config{Agent: &fakeAgent{}, ServerName: "zeokeel", Version: "test", Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

// wireMessage decodes one line of the protocol stream. A line is either a
// response (id + result/error) or a notification (method, no id).
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

func decodeFrames(data []byte) ([]wireMessage, error) {
	var out []wireMessage
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var msg wireMessage
		if err := json.Unmarshal(trimmed, &msg); err != nil {
			return nil, errors.New("protocol emitted a non-JSON line: " + string(trimmed))
		}
		if msg.JSONRPC != JSONRPCVersion {
			return nil, errors.New("frame has wrong jsonrpc version: " + string(trimmed))
		}
		out = append(out, msg)
	}
	return out, nil
}

func decodeWireLines(t *testing.T, data []byte) []wireMessage {
	t.Helper()
	out, err := decodeFrames(data)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return out
}

// serveLines runs one Serve call over a fixed input and returns the decoded
// output lines. The next implementation must return nil here; while Serve is
// the RED stub this fails with an explicit message.
func serveLines(t *testing.T, srv *Server, input string) []wireMessage {
	t.Helper()
	var out bytes.Buffer
	err := srv.Serve(context.Background(), strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("RED (TDD acceptance not yet implemented): Serve returned %v; implement the loop described in doc.go", err)
	}
	return decodeWireLines(t, out.Bytes())
}

func rpcRequest(t *testing.T, id int, method string, params any) string {
	t.Helper()
	frame := map[string]any{"jsonrpc": JSONRPCVersion, "id": id, "method": method}
	if params != nil {
		frame["params"] = params
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func rpcNotification(t *testing.T, method string, params any) string {
	t.Helper()
	frame := map[string]any{"jsonrpc": JSONRPCVersion, "method": method, "params": params}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func findResponse(t *testing.T, msgs []wireMessage, id int) wireMessage {
	t.Helper()
	want := []byte(jsonInt(id))
	for _, msg := range msgs {
		if msg.Method != "" || msg.ID == nil {
			continue // notification
		}
		if bytes.Equal(bytes.TrimSpace(msg.ID), want) {
			return msg
		}
	}
	t.Fatalf("no response for id %d in %d frame(s): %+v", id, len(msgs), msgs)
	return wireMessage{}
}

func findNotifications(msgs []wireMessage, method string) []wireMessage {
	var out []wireMessage
	for _, msg := range msgs {
		if msg.Method == method {
			out = append(out, msg)
		}
	}
	return out
}

func jsonInt(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// TestServerInitializeReturnsCapabilities is the initialize handshake:
// protocolVersion + agentCapabilities + stable agent info.
func TestServerInitializeReturnsCapabilities(t *testing.T) {
	srv := newTestServer(t)
	input := rpcRequest(t, 1, MethodInitialize, InitializeRequest{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      Implementation{Name: "test-client", Version: "1"},
	}) + "\n"
	msgs := serveLines(t, srv, input)
	resp := findResponse(t, msgs, 1)
	if resp.Error != nil {
		t.Fatalf("initialize returned error %+v", resp.Error)
	}
	var result InitializeResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocolVersion = %d, want %d", result.ProtocolVersion, ProtocolVersion)
	}
	if result.AgentInfo.Name != "zeokeel" || result.AgentInfo.Version != "test" {
		t.Fatalf("agentInfo = %+v, want the configured name/version", result.AgentInfo)
	}
	// The minimum surface does not promise loadSession; text prompts are the
	// only prompt capability.
	if result.AgentCapabilities.LoadSession {
		t.Fatalf("agentCapabilities.loadSession = true, want false for the minimum server")
	}
	if result.AgentCapabilities.PromptCapabilities.Image {
		t.Fatalf("minimum server must not advertise image prompt capability")
	}
}

// TestServerNewSessionPromptAndCancel covers the session lifecycle and ordered
// streaming: session/new -> session/prompt (update notification before the
// response) -> session/cancel.
func TestServerNewSessionPromptAndCancel(t *testing.T) {
	agent := &fakeAgent{}
	srv, err := NewServer(Config{Agent: agent, ServerName: "zeokeel", Version: "test", Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cwd, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: cwd}),
		rpcRequest(t, 3, MethodSessionPrompt, PromptRequest{SessionID: "session-1", Prompt: []ContentBlock{{Type: "text", Text: "hi"}}}),
		rpcNotification(t, MethodSessionCancel, CancelNotification{SessionID: "session-1"}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)

	newResp := findResponse(t, msgs, 2)
	if newResp.Error != nil {
		t.Fatalf("session/new error: %+v", newResp.Error)
	}
	var newResult NewSessionResponse
	if err := json.Unmarshal(newResp.Result, &newResult); err != nil || newResult.SessionID == "" {
		t.Fatalf("session/new result = %s (err=%v), want a sessionId", newResp.Result, err)
	}
	agent.mu.Lock()
	news := append([]NewSessionRequest(nil), agent.news...)
	agent.mu.Unlock()
	if len(news) != 1 || news[0].Cwd != cwd {
		t.Fatalf("agent.NewSession calls = %+v, want one with cwd %q", news, cwd)
	}

	promptResp := findResponse(t, msgs, 3)
	if promptResp.Error != nil {
		t.Fatalf("session/prompt error: %+v", promptResp.Error)
	}
	var promptResult PromptResponse
	if err := json.Unmarshal(promptResp.Result, &promptResult); err != nil {
		t.Fatalf("session/prompt result: %v", err)
	}
	if promptResult.StopReason != StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want %q", promptResult.StopReason, StopReasonEndTurn)
	}
	updates := findNotifications(msgs, MethodSessionUpdate)
	if len(updates) == 0 {
		t.Fatalf("session/prompt produced no session/update notifications")
	}
	var updateParams SessionUpdateNotification
	if err := json.Unmarshal(updates[0].Params, &updateParams); err != nil {
		t.Fatalf("session/update params: %v", err)
	}
	if updateParams.SessionID != "session-1" || updateParams.Update.SessionUpdate != UpdateAgentMessageChunk {
		t.Fatalf("session/update = %+v, want an agent_message_chunk for session-1", updateParams)
	}
	// Ordered delivery: the update notification must precede the prompt
	// response on the wire.
	updateIdx, responseIdx := -1, -1
	for i, msg := range msgs {
		if msg.Method == MethodSessionUpdate && updateIdx < 0 {
			updateIdx = i
		}
		if msg.Method == "" && bytes.Equal(bytes.TrimSpace(msg.ID), []byte("3")) {
			responseIdx = i
		}
	}
	if updateIdx < 0 || responseIdx < 0 || updateIdx > responseIdx {
		t.Fatalf("session/update must be delivered before the session/prompt response (update=%d response=%d)", updateIdx, responseIdx)
	}

	agent.mu.Lock()
	cancels := append([]string(nil), agent.cancels...)
	agent.mu.Unlock()
	if len(cancels) != 1 || cancels[0] != "session-1" {
		t.Fatalf("agent.Cancel calls = %v, want [session-1]", cancels)
	}
}

// TestServerRejectsUnknownMethodAndBadJSON pins the JSON-RPC error contract:
// unknown method -> -32601 (correlated to the request id), malformed JSON line
// -> -32700 with a null id, and the connection keeps serving after both.
func TestServerRejectsUnknownMethodAndBadJSON(t *testing.T) {
	srv := newTestServer(t)
	input := strings.Join([]string{
		rpcRequest(t, 7, "session/does-not-exist", map[string]any{}),
		`{"jsonrpc":"2.0","id":8,`,
		rpcRequest(t, 9, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)

	unknown := findResponse(t, msgs, 7)
	if unknown.Error == nil || unknown.Error.Code != CodeMethodNotFound {
		t.Fatalf("unknown method error = %+v, want code %d", unknown.Error, CodeMethodNotFound)
	}
	var parseErr *RPCError
	for _, msg := range msgs {
		if msg.Error != nil && msg.Error.Code == CodeParseError {
			parseErr = msg.Error
			if len(bytes.TrimSpace(msg.ID)) != 0 && string(bytes.TrimSpace(msg.ID)) != "null" {
				t.Fatalf("parse error must carry a null id, got %s", msg.ID)
			}
		}
	}
	if parseErr == nil {
		t.Fatalf("malformed JSON produced no -%d parse error: %+v", -CodeParseError, msgs)
	}
	// The connection survives a parse error: the following initialize must be
	// answered normally.
	initResp := findResponse(t, msgs, 9)
	if initResp.Error != nil {
		t.Fatalf("initialize after bad JSON returned error %+v", initResp.Error)
	}
}

// TestServerRequiresInitializeBeforePrompt is the ACP lifecycle gate: a prompt
// before initialize must fail with -32602/-32603 style error (not silently
// run).
func TestServerRequiresInitializeBeforePrompt(t *testing.T) {
	srv := newTestServer(t)
	input := rpcRequest(t, 1, MethodSessionPrompt, PromptRequest{
		SessionID: "session-x",
		Prompt:    []ContentBlock{{Type: "text", Text: "hi"}},
	}) + "\n"
	msgs := serveLines(t, srv, input)
	resp := findResponse(t, msgs, 1)
	if resp.Error == nil {
		t.Fatalf("session/prompt before initialize succeeded, want a JSON-RPC error")
	}
	if resp.Error.Code != CodeInvalidRequest && resp.Error.Code != CodeInvalidParams && resp.Error.Code != CodeInternalError {
		t.Fatalf("prompt-before-initialize error code = %d, want an invalid-request/params/internal error", resp.Error.Code)
	}
}

// TestServerRejectsRelativeCwd pins session/new input validation: ACP requires
// an absolute workspace path.
func TestServerRejectsRelativeCwd(t *testing.T) {
	srv := newTestServer(t)
	input := strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: "relative/dir"}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)
	resp := findResponse(t, msgs, 2)
	if resp.Error == nil || (resp.Error.Code != CodeInvalidParams && resp.Error.Code != CodeInvalidRequest) {
		t.Fatalf("session/new with relative cwd = %+v, want invalid-params/invalid-request error", resp)
	}
}

// TestServerReturnsErrorWhenAgentFails maps a handler error to -32603 (the
// agent's own error text must not crash the loop).
func TestServerReturnsErrorWhenAgentFails(t *testing.T) {
	srv, err := NewServer(Config{Agent: &failingAgent{}, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	input := strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)
	resp := findResponse(t, msgs, 2)
	if resp.Error == nil || resp.Error.Code != CodeInternalError {
		t.Fatalf("agent failure = %+v, want code %d", resp, CodeInternalError)
	}
}

// failingAgent always fails NewSession.
type failingAgent struct{}

func (failingAgent) NewSession(context.Context, NewSessionRequest) (NewSessionResponse, error) {
	return NewSessionResponse{}, errors.New("provider unavailable")
}
func (failingAgent) Prompt(context.Context, PromptRequest, UpdateSink) (PromptResponse, error) {
	return PromptResponse{}, errors.New("provider unavailable")
}
func (failingAgent) Cancel(context.Context, string) error { return nil }
func (failingAgent) Close(context.Context) error          { return nil }
