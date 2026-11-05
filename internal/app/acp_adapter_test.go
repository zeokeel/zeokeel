package app

// ACP adapter end-to-end tests: real acp.Server + real app adapter + real
// headless runtime + a fake OpenAI-compatible SSE provider. No mocks on the
// ACP/transport path; only the model endpoint is fake.
//
// Covers the acceptance flow from internal/acp/doc.go:
//   initialize -> session/new -> session/prompt -> session/update
//   notifications (agent_message_chunk) -> prompt response (end_turn)
//   then a cancelled prompt (session/cancel -> stopReason cancelled)
//   and a failed provider (-> stopReason refusal).
//
// The fake provider helpers (sseServer/writeHeadlessStore) live in
// headless_test.go in this package.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/acp"
)

// acpConn drives one acp.Server over in-memory pipes: stdin is a writer, stdout
// is read line by line (one JSON-RPC frame per line).
type acpConn struct {
	inW  *io.PipeWriter
	line chan string
	err  chan error
}

// startACP starts server in the background and returns the client end.
func startACP(t *testing.T, agent acp.Agent) *acpConn {
	t.Helper()
	srv, err := acp.NewServer(acp.Config{Agent: agent, Version: "test", Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("acp.NewServer: %v", err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, inR, outW)
		_ = outW.Close()
	}()
	conn := &acpConn{inW: inW, line: make(chan string, 512), err: make(chan error, 1)}
	go func() {
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			conn.line <- scanner.Text()
		}
		conn.err <- scanner.Err()
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("acp server did not stop")
		}
	})
	return conn
}

func (c *acpConn) send(t *testing.T, frame map[string]any) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if _, err := c.inW.Write(append(data, '\n')); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// awaitResponse reads frames until the response with wantID arrives and returns
// it together with every notification seen before it, in order.
func (c *acpConn) awaitResponse(t *testing.T, wantID float64, timeout time.Duration) (map[string]any, []map[string]any) {
	t.Helper()
	deadline := time.After(timeout)
	var notifications []map[string]any
	for {
		select {
		case raw := <-c.line:
			var frame map[string]any
			if err := json.Unmarshal([]byte(raw), &frame); err != nil {
				t.Fatalf("stdout line is not JSON: %v\n%s", err, raw)
			}
			if id, ok := frame["id"].(float64); ok && id == wantID {
				return frame, notifications
			}
			notifications = append(notifications, frame)
		case <-deadline:
			t.Fatalf("timed out waiting for response id=%v", wantID)
		}
	}
}

// awaitNotification reads frames until a notification with method matches.
func (c *acpConn) awaitNotification(t *testing.T, method string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case raw := <-c.line:
			var frame map[string]any
			if err := json.Unmarshal([]byte(raw), &frame); err != nil {
				t.Fatalf("stdout line is not JSON: %v\n%s", err, raw)
			}
			if frame["method"] == method {
				return frame
			}
		case <-deadline:
			t.Fatalf("timed out waiting for notification %q", method)
		}
	}
}

// newACPTestAdapter builds the real adapter over a data directory whose provider
// points at baseURL (the fake SSE server).
func newACPTestAdapter(t *testing.T, baseURL string) acp.Agent {
	t.Helper()
	dir := t.TempDir()
	writeHeadlessStore(t, dir, baseURL, map[string]string{"provider.retry_max_retries": "0"})
	agent, err := NewACPAdapter(ACPAgentConfig{DataDir: dir, Logger: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := agent.Close(ctx); err != nil {
			t.Errorf("agent.Close: %v", err)
		}
	})
	return agent
}

// initialize performs the ACP handshake and returns the initialize result.
func initialize(t *testing.T, conn *acpConn) map[string]any {
	t.Helper()
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": 1},
	})
	resp, _ := conn.awaitResponse(t, 1, 10*time.Second)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize response = %v", resp)
	}
	return result
}

// newSession performs session/new and returns the session id.
func newSession(t *testing.T, conn *acpConn, id float64, cwd string) string {
	t.Helper()
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "session/new",
		"params": map[string]any{"cwd": cwd},
	})
	resp, _ := conn.awaitResponse(t, id, 10*time.Second)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("session/new response = %v", resp)
	}
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new returned no sessionId: %v", result)
	}
	return sessionID
}

// acpPrompt sends session/prompt and returns the response plus notifications.
// (Named acpPrompt, not prompt: the package's other test files import a package
// called "prompt", and a package-level func with the same name is illegal.)
func acpPrompt(t *testing.T, conn *acpConn, id float64, sessionID, text string) (map[string]any, []map[string]any) {
	t.Helper()
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": text}},
		},
	})
	return conn.awaitResponse(t, id, 30*time.Second)
}

// chunkText concatenates agent_message_chunk notifications.
func chunkText(notifications []map[string]any) string {
	var b strings.Builder
	for _, frame := range notifications {
		if frame["method"] != "session/update" {
			continue
		}
		params, _ := frame["params"].(map[string]any)
		update, _ := params["update"].(map[string]any)
		if update["sessionUpdate"] != "agent_message_chunk" {
			continue
		}
		content, _ := update["content"].(map[string]any)
		if text, ok := content["text"].(string); ok {
			b.WriteString(text)
		}
	}
	return b.String()
}

// TestACPAdapterEndToEndPrompt is the acceptance flow against a fake provider:
// initialize -> session/new -> prompt streams agent_message_chunk(s) and
// settles end_turn, then an invalid relative cwd is rejected with -32602.
func TestACPAdapterEndToEndPrompt(t *testing.T) {
	provider := sseServer(t, "acp answer")
	agent := newACPTestAdapter(t, provider.URL+"/v1")
	conn := startACP(t, agent)

	init := initialize(t, conn)
	if init["protocolVersion"] != float64(1) {
		t.Fatalf("initialize protocolVersion = %v", init["protocolVersion"])
	}
	agentInfo, _ := init["agentInfo"].(map[string]any)
	if agentInfo["name"] != "zeokeel" {
		t.Fatalf("agentInfo = %v", agentInfo)
	}

	sessionID := newSession(t, conn, 2, t.TempDir())
	resp, notifications := acpPrompt(t, conn, 3, sessionID, "say hello")
	result, _ := resp["result"].(map[string]any)
	if result["stopReason"] != "end_turn" {
		t.Fatalf("stopReason = %v, want end_turn (response %v)", result["stopReason"], resp)
	}
	if got := chunkText(notifications); !strings.Contains(got, "acp answer") {
		t.Fatalf("streamed agent_message_chunk text = %q, want the provider answer; frames: %v", got, notifications)
	}

	// The protocol requires an absolute, existing cwd: a relative path is a
	// -32602 invalid-params error (server gate; adapter validates as well).
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "session/new",
		"params": map[string]any{"cwd": "relative/dir"},
	})
	resp4, _ := conn.awaitResponse(t, 4, 10*time.Second)
	acpErr, _ := resp4["error"].(map[string]any)
	if acpErr["code"] != float64(-32602) {
		t.Fatalf("relative cwd response = %v, want -32602", resp4)
	}
}

// TestACPAdapterEndToEndCancel: an in-flight prompt is cancelled through the
// protocol notification and settles with stopReason cancelled while the
// server keeps serving.
func TestACPAdapterEndToEndCancel(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"working\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // hold the turn open until it is cancelled
	}))
	t.Cleanup(provider.Close)
	agent := newACPTestAdapter(t, provider.URL+"/v1")
	conn := startACP(t, agent)

	initialize(t, conn)
	sessionID := newSession(t, conn, 2, t.TempDir())

	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "slow prompt"}},
		},
	})
	// Wait until the turn is actually streaming, then cancel it by notification.
	conn.awaitNotification(t, "session/update", 20*time.Second)
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "method": "session/cancel",
		"params": map[string]any{"sessionId": sessionID},
	})
	resp, _ := conn.awaitResponse(t, 3, 30*time.Second)
	result, _ := resp["result"].(map[string]any)
	if result["stopReason"] != "cancelled" {
		t.Fatalf("cancelled prompt stopReason = %v, want cancelled (response %v)", result["stopReason"], resp)
	}

	// The connection/server survives the cancellation: a new session works.
	newSession(t, conn, 4, t.TempDir())
}

// TestACPAdapterEndToEndFailedTurn: a provider error settles the prompt with
// stopReason refusal (agent error stays in the server log, not stdout).
func TestACPAdapterEndToEndFailedTurn(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"provider down"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(provider.Close)
	agent := newACPTestAdapter(t, provider.URL+"/v1")
	conn := startACP(t, agent)

	initialize(t, conn)
	sessionID := newSession(t, conn, 2, t.TempDir())
	resp, _ := acpPrompt(t, conn, 3, sessionID, "will fail")
	result, _ := resp["result"].(map[string]any)
	if result["stopReason"] != "refusal" {
		t.Fatalf("failed turn stopReason = %v, want refusal (response %v)", result["stopReason"], resp)
	}
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("failed turn should settle with a stopReason, not a JSON-RPC error: %v", resp)
	}
}

// TestACPAdapterSeams covers the adapter's own contract without a provider:
// unknown session, empty prompt and single-in-flight are rejected; Close is
// idempotent. These are the cases a client can trigger directly.
func TestACPAdapterSeams(t *testing.T) {
	ctx := context.Background()
	provider := sseServer(t, "seam answer")
	agent := newACPTestAdapter(t, provider.URL+"/v1")

	if _, err := agent.Prompt(ctx, acp.PromptRequest{SessionID: "missing", Prompt: []acp.ContentBlock{{Type: "text", Text: "hi"}}}, func(acp.SessionUpdateNotification) error { return nil }); err == nil {
		t.Fatal("Prompt(unknown session) = nil error")
	}
	if _, err := agent.NewSession(ctx, acp.NewSessionRequest{Cwd: "relative"}); err == nil {
		t.Fatal("NewSession(relative cwd) = nil error")
	}
	if _, err := agent.NewSession(ctx, acp.NewSessionRequest{Cwd: fmt.Sprintf("%s/missing", t.TempDir())}); err == nil {
		t.Fatal("NewSession(missing cwd) = nil error")
	}
	if err := agent.Close(ctx); err != nil {
		t.Fatalf("second Close = %v, want nil (idempotent)", err)
	}
}
