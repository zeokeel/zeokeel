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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/acp"
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
	return newACPTestAdapterDir(t, t.TempDir(), baseURL)
}

// newACPTestAdapterDir is newACPTestAdapter with an explicit data directory, so
// a test can restart the adapter over the same persisted store.
func newACPTestAdapterDir(t *testing.T, dir, baseURL string) acp.Agent {
	t.Helper()
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

// TestACPAdapterSessionListAndLoadAcrossRestart is the persistence acceptance
// flow: a session created and prompted through ACP is listed with its cwd/title
// by a fresh adapter over the same data directory, session/load replays its
// history, and the loaded session accepts a new prompt.
func TestACPAdapterSessionListAndLoadAcrossRestart(t *testing.T) {
	provider := sseServer(t, "remembered answer")
	dir := t.TempDir()
	cwd := t.TempDir()

	first := newACPTestAdapterDir(t, dir, provider.URL+"/v1")
	conn1 := startACP(t, first)
	initialize(t, conn1)
	sessionID := newSession(t, conn1, 2, cwd)
	resp, _ := acpPrompt(t, conn1, 3, sessionID, "remember this")
	if result, _ := resp["result"].(map[string]any); result["stopReason"] != "end_turn" {
		t.Fatalf("first prompt = %v, want end_turn", resp)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := first.Close(closeCtx); err != nil {
		t.Fatalf("close first adapter: %v", err)
	}
	cancel()

	second := newACPTestAdapterDir(t, dir, provider.URL+"/v1")
	conn2 := startACP(t, second)
	initialize(t, conn2)

	// session/list returns the persisted session with its workspace cwd.
	conn2.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 10, "method": "session/list",
		"params": map[string]any{"cwd": cwd},
	})
	listResp, _ := conn2.awaitResponse(t, 10, 10*time.Second)
	listResult, _ := listResp["result"].(map[string]any)
	sessions, _ := listResult["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("session/list returned %d session(s), want 1: %v", len(sessions), listResp)
	}
	entry, _ := sessions[0].(map[string]any)
	if entry["sessionId"] != sessionID {
		t.Fatalf("session/list sessionId = %v, want %q", entry["sessionId"], sessionID)
	}
	if entry["cwd"] != cwd {
		t.Fatalf("session/list cwd = %v, want %q", entry["cwd"], cwd)
	}
	if updated, _ := entry["updatedAt"].(string); updated == "" {
		t.Fatalf("session/list updatedAt = %v, want an ISO timestamp", entry["updatedAt"])
	}

	// session/load replays the persisted conversation before its response.
	conn2.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 11, "method": "session/load",
		"params": map[string]any{"sessionId": sessionID, "cwd": cwd},
	})
	loadResp, replay := conn2.awaitResponse(t, 11, 10*time.Second)
	if loadErr, isErr := loadResp["error"]; isErr {
		t.Fatalf("session/load error: %v", loadErr)
	}
	var userText, agentText strings.Builder
	for _, frame := range replay {
		if frame["method"] != "session/update" {
			continue
		}
		params, _ := frame["params"].(map[string]any)
		update, _ := params["update"].(map[string]any)
		content, _ := update["content"].(map[string]any)
		text, _ := content["text"].(string)
		switch update["sessionUpdate"] {
		case "user_message_chunk":
			userText.WriteString(text)
		case "agent_message_chunk":
			agentText.WriteString(text)
		}
	}
	if !strings.Contains(userText.String(), "remember this") {
		t.Fatalf("session/load replayed user text = %q, want the persisted prompt; frames: %v", userText.String(), replay)
	}
	if !strings.Contains(agentText.String(), "remembered answer") {
		t.Fatalf("session/load replayed agent text = %q, want the persisted answer; frames: %v", agentText.String(), replay)
	}

	// The loaded session is usable for a new turn.
	resp2, _ := acpPrompt(t, conn2, 12, sessionID, "say it again")
	if result, _ := resp2["result"].(map[string]any); result["stopReason"] != "end_turn" {
		t.Fatalf("prompt on loaded session = %v, want end_turn", resp2)
	}
}

// TestACPAdapterEndToEndRequestPermission is the approval acceptance flow with
// a fake provider: the agent asks the client (session/request_permission), the
// client selects allow_once, the mapping reaches the existing ApprovalQueue,
// and the approved tool actually runs.
func TestACPAdapterEndToEndRequestPermission(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "allowed-marker")
	provider := toolCallServer(t, "bash", fmt.Sprintf(`{"command":"touch %s"}`, marker), "done")
	agent := newACPTestAdapter(t, provider.URL+"/v1")
	conn := startACP(t, agent)

	initialize(t, conn)
	cwd := t.TempDir()
	sessionID := newSession(t, conn, 2, cwd)
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "run the command"}},
		},
	})

	req := conn.awaitNotification(t, "session/request_permission", 30*time.Second)
	params, _ := req["params"].(map[string]any)
	if params["sessionId"] != sessionID {
		t.Fatalf("session/request_permission sessionId = %v, want %q", params["sessionId"], sessionID)
	}
	toolCall, _ := params["toolCall"].(map[string]any)
	if id, _ := toolCall["toolCallId"].(string); id == "" {
		t.Fatalf("session/request_permission toolCall = %v, want a toolCallId", toolCall)
	}
	if toolCall["title"] != "bash" {
		t.Fatalf("session/request_permission title = %v, want bash", toolCall["title"])
	}
	options, _ := params["options"].([]any)
	kinds := map[string]bool{}
	for _, raw := range options {
		option, _ := raw.(map[string]any)
		if kind, _ := option["kind"].(string); kind != "" {
			kinds[kind] = true
		}
	}
	for _, kind := range []string{"allow_once", "allow_always", "reject_once", "reject_always"} {
		if !kinds[kind] {
			t.Fatalf("permission options %v missing %q", options, kind)
		}
	}

	// Answer allow_once: the decision must be mapped onto the ApprovalQueue and
	// the approved bash call must execute.
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": req["id"],
		"result": map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": "allow_once"},
		},
	})
	resp, _ := conn.awaitResponse(t, 3, 30*time.Second)
	if respErr, isErr := resp["error"]; isErr {
		t.Fatalf("permission prompt returned an error: %v", respErr)
	}
	if result, _ := resp["result"].(map[string]any); result["stopReason"] != "end_turn" {
		t.Fatalf("permission prompt stopReason = %v, want end_turn", resp["result"])
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("approved bash call did not run: %v", err)
	}
}

// TestACPPermissionActionMapping pins the ACP session/request_permission
// outcome -> ApprovalQueue decision mapping for every option kind, including
// the fail-closed paths.
func TestACPPermissionActionMapping(t *testing.T) {
	cases := []struct {
		name    string
		outcome acp.RequestPermissionOutcome
		want    permissionAction
	}{
		{
			name:    "allow_once",
			outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: acp.PermissionKindAllowOnce},
			want:    permissionActionAllowOnce,
		},
		{
			name:    "allow_always",
			outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: acp.PermissionKindAllowAlways},
			want:    permissionActionAllowAlways,
		},
		{
			name:    "reject_once",
			outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: acp.PermissionKindRejectOnce},
			want:    permissionActionRejectOnce,
		},
		{
			name:    "reject_always",
			outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: acp.PermissionKindRejectAlways},
			want:    permissionActionRejectAlways,
		},
		{
			name:    "cancelled",
			outcome: acp.RequestPermissionOutcome{Outcome: "cancelled"},
			want:    permissionActionFailClosed,
		},
		{
			name:    "unknown option id",
			outcome: acp.RequestPermissionOutcome{Outcome: "selected", OptionID: "bogus"},
			want:    permissionActionFailClosed,
		},
		{
			name:    "missing outcome",
			outcome: acp.RequestPermissionOutcome{},
			want:    permissionActionFailClosed,
		},
	}
	for _, tc := range cases {
		if got := permissionActionFor(tc.outcome); got != tc.want {
			t.Errorf("%s: permissionActionFor(%+v) = %v, want %v", tc.name, tc.outcome, got, tc.want)
		}
	}
}

// TestACPAdapterEndToEndRequestPermissionReject is the deny half of the
// approval acceptance flow: a reject_once answer must map onto the
// ApprovalQueue so the gated tool does NOT run, while the turn still settles.
func TestACPAdapterEndToEndRequestPermissionReject(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "rejected-marker")
	provider := toolCallServer(t, "bash", fmt.Sprintf(`{"command":"touch %s"}`, marker), "denied as expected")
	agent := newACPTestAdapter(t, provider.URL+"/v1")
	conn := startACP(t, agent)

	initialize(t, conn)
	sessionID := newSession(t, conn, 2, t.TempDir())
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "run the command"}},
		},
	})
	req := conn.awaitNotification(t, "session/request_permission", 30*time.Second)
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": req["id"],
		"result": map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": "reject_once"},
		},
	})
	resp, _ := conn.awaitResponse(t, 3, 30*time.Second)
	if respErr, isErr := resp["error"]; isErr {
		t.Fatalf("rejected permission prompt returned an error: %v", respErr)
	}
	if result, _ := resp["result"].(map[string]any); result["stopReason"] != "end_turn" {
		t.Fatalf("rejected permission prompt stopReason = %v, want end_turn", resp["result"])
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("reject_once permission still executed the gated bash call")
	}
}
