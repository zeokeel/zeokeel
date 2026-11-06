package acp

// Protocol-level tests for the ACP v1 session persistence and authentication
// surface: session/list, session/load and authenticate, including the
// capability advertisement, validation errors and unsupported-agent paths.
//
// Run: go test -count=1 -run 'SessionList|SessionLoad|Authenticate' ./internal/acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// opsAgent is a fake Agent that implements the optional SessionLister and
// SessionLoader seams the server advertises in initialize.
type opsAgent struct {
	mu sync.Mutex

	listResp ListSessionsResponse
	listErr  error
	loadErr  error

	listReqs []ListSessionsRequest
	loadReqs []LoadSessionRequest
	prompts  []PromptRequest
}

var _ Agent = (*opsAgent)(nil)
var _ SessionLister = (*opsAgent)(nil)
var _ SessionLoader = (*opsAgent)(nil)

func (a *opsAgent) NewSession(context.Context, NewSessionRequest) (NewSessionResponse, error) {
	return NewSessionResponse{SessionID: "session-new"}, nil
}

func (a *opsAgent) Prompt(_ context.Context, req PromptRequest, _ UpdateSink) (PromptResponse, error) {
	a.mu.Lock()
	a.prompts = append(a.prompts, req)
	a.mu.Unlock()
	return PromptResponse{StopReason: StopReasonEndTurn}, nil
}

func (a *opsAgent) Cancel(context.Context, string) error { return nil }
func (a *opsAgent) Close(context.Context) error          { return nil }

func (a *opsAgent) ListSessions(_ context.Context, req ListSessionsRequest) (ListSessionsResponse, error) {
	a.mu.Lock()
	a.listReqs = append(a.listReqs, req)
	a.mu.Unlock()
	return a.listResp, a.listErr
}

func (a *opsAgent) LoadSession(_ context.Context, req LoadSessionRequest, updates UpdateSink) error {
	a.mu.Lock()
	a.loadReqs = append(a.loadReqs, req)
	a.mu.Unlock()
	if a.loadErr != nil {
		return a.loadErr
	}
	return updates(SessionUpdateNotification{
		SessionID: req.SessionID,
		Update: SessionUpdate{
			SessionUpdate: UpdateAgentMessageChunk,
			Content:       &ContentBlock{Type: "text", Text: "replayed history"},
		},
	})
}

// promptCount returns how many prompts the fake agent ran.
func (a *opsAgent) promptCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.prompts)
}

// decodeResult unmarshals a response result into v.
func decodeResult[T any](t *testing.T, resp wireMessage) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode response result: %v (raw %s)", err, resp.Result)
	}
	return out
}

// TestInitializeAdvertisesFullSurface: an agent implementing the optional
// session seams advertises loadSession, session/list and the no-op auth method.
func TestInitializeAdvertisesFullSurface(t *testing.T) {
	srv, err := NewServer(Config{Agent: &opsAgent{}, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	msgs := serveLines(t, srv, rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion})+"\n")
	resp := findResponse(t, msgs, 1)
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	result := decodeResult[InitializeResponse](t, resp)
	if !result.AgentCapabilities.LoadSession {
		t.Fatal("initialize must advertise loadSession when the agent implements session/load")
	}
	if result.AgentCapabilities.SessionCapabilities == nil || result.AgentCapabilities.SessionCapabilities.List == nil {
		t.Fatalf("initialize must advertise sessionCapabilities.list: %+v", result.AgentCapabilities)
	}
	if len(result.AuthMethods) == 0 {
		t.Fatal("initialize must advertise at least one authentication method")
	}
	if result.AuthMethods[0].ID != "none" || result.AuthMethods[0].Name == "" {
		t.Fatalf("default authMethods = %+v, want the no-op method", result.AuthMethods)
	}
}

// TestInitializeHidesUnsupportedSurface: the baseline Agent (no optional
// interfaces) keeps the old capability advertisement: no loadSession and no
// sessionCapabilities. This pins the backward-compatible case.
func TestInitializeHidesUnsupportedSurface(t *testing.T) {
	srv := newTestServer(t)
	msgs := serveLines(t, srv, rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion})+"\n")
	resp := findResponse(t, msgs, 1)
	result := decodeResult[InitializeResponse](t, resp)
	if result.AgentCapabilities.LoadSession {
		t.Fatal("initialize advertised loadSession for an agent that cannot load sessions")
	}
	if result.AgentCapabilities.SessionCapabilities != nil && result.AgentCapabilities.SessionCapabilities.List != nil {
		t.Fatal("initialize advertised session/list for an agent that cannot list sessions")
	}
}

// TestSessionListAndLoadHappyPath: list returns the agent's summaries; load
// streams replay updates before its (empty) response and registers the session
// so a following prompt runs on it.
func TestSessionListAndLoadHappyPath(t *testing.T) {
	cwd := t.TempDir()
	agent := &opsAgent{
		listResp: ListSessionsResponse{Sessions: []SessionInfo{
			{SessionID: "session-old", Cwd: cwd, Title: "old talk", UpdatedAt: "2026-01-02T03:04:05Z"},
			{SessionID: "session-new", Cwd: cwd, Title: "new talk", UpdatedAt: "2026-02-03T04:05:06Z"},
		}},
	}
	srv, err := NewServer(Config{Agent: agent, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	input := strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionList, ListSessionsRequest{Cwd: cwd}),
		rpcRequest(t, 3, MethodSessionLoad, LoadSessionRequest{SessionID: "session-new", Cwd: cwd}),
		rpcRequest(t, 4, MethodSessionPrompt, PromptRequest{
			SessionID: "session-new",
			Prompt:    []ContentBlock{{Type: "text", Text: "continue"}},
		}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)

	listResp := findResponse(t, msgs, 2)
	if listResp.Error != nil {
		t.Fatalf("session/list error: %+v", listResp.Error)
	}
	listResult := decodeResult[ListSessionsResponse](t, listResp)
	if len(listResult.Sessions) != 2 || listResult.Sessions[0].SessionID != "session-old" {
		t.Fatalf("session/list sessions = %+v", listResult.Sessions)
	}
	if listResult.Sessions[0].Cwd != cwd || listResult.Sessions[1].Title != "new talk" {
		t.Fatalf("session/list summaries = %+v", listResult.Sessions)
	}
	agent.mu.Lock()
	listReqs := append([]ListSessionsRequest(nil), agent.listReqs...)
	agent.mu.Unlock()
	if len(listReqs) != 1 || listReqs[0].Cwd != cwd {
		t.Fatalf("agent.ListSessions requests = %+v, want the cwd filter", listReqs)
	}

	loadResp := findResponse(t, msgs, 3)
	if loadResp.Error != nil {
		t.Fatalf("session/load error: %+v", loadResp.Error)
	}
	if got := strings.TrimSpace(string(loadResp.Result)); got != "{}" {
		t.Fatalf("session/load result = %s, want an empty object", got)
	}
	// The replayed history update must precede the load response.
	loadIdx, replayIdx := -1, -1
	for i, msg := range msgs {
		if msg.Method == MethodSessionUpdate {
			var params SessionUpdateNotification
			if err := json.Unmarshal(msg.Params, &params); err == nil && params.Update.Content != nil && params.Update.Content.Text == "replayed history" {
				replayIdx = i
			}
		}
		if msg.Method == "" && bytes.Equal(bytes.TrimSpace(msg.ID), []byte("3")) {
			loadIdx = i
		}
	}
	if replayIdx < 0 || loadIdx < 0 || replayIdx > loadIdx {
		t.Fatalf("history replay must be delivered before the session/load response (replay=%d response=%d)", replayIdx, loadIdx)
	}

	promptResp := findResponse(t, msgs, 4)
	if promptResp.Error != nil {
		t.Fatalf("session/prompt on a loaded session error: %+v", promptResp.Error)
	}
	agent.mu.Lock()
	loadReqs := append([]LoadSessionRequest(nil), agent.loadReqs...)
	agent.mu.Unlock()
	if len(loadReqs) != 1 || loadReqs[0].SessionID != "session-new" || loadReqs[0].Cwd != cwd {
		t.Fatalf("agent.LoadSession requests = %+v", loadReqs)
	}
	if agent.promptCount() != 1 {
		t.Fatalf("prompt on the loaded session did not reach the agent")
	}
}

// TestSessionListUnsupported: the baseline agent answers -32601 for list/load.
func TestSessionListUnsupported(t *testing.T) {
	srv := newTestServer(t) // fakeAgent: no SessionLister/SessionLoader
	msgs := serveLines(t, srv, strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionList, map[string]any{}),
		rpcRequest(t, 3, MethodSessionLoad, LoadSessionRequest{SessionID: "s", Cwd: t.TempDir()}),
	}, "\n")+"\n")
	for _, id := range []int{2, 3} {
		resp := findResponse(t, msgs, id)
		if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
			t.Fatalf("unsupported method %d = %+v, want -32601", id, resp)
		}
	}
}

// TestSessionListAndLoadValidation: malformed params and bad cwd paths are
// -32602 invalid-params errors, and a load before initialize is -32600.
func TestSessionListAndLoadValidation(t *testing.T) {
	agent := &opsAgent{loadErr: &RPCError{Code: CodeResourceNotFound, Message: "unknown session"}}
	srv, err := NewServer(Config{Agent: agent, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	msgs := serveLines(t, srv, strings.Join([]string{
		rpcRequest(t, 1, MethodSessionList, map[string]any{}), // before initialize
		rpcRequest(t, 2, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 3, MethodSessionList, "not-an-object"),
		rpcRequest(t, 4, MethodSessionList, ListSessionsRequest{Cwd: "relative/dir"}),
		rpcRequest(t, 5, MethodSessionLoad, map[string]any{}),
		rpcRequest(t, 6, MethodSessionLoad, LoadSessionRequest{SessionID: "s1", Cwd: "relative/dir"}),
		rpcRequest(t, 7, MethodSessionLoad, LoadSessionRequest{SessionID: "s1", Cwd: t.TempDir()}),
	}, "\n")+"\n")

	beforeInit := findResponse(t, msgs, 1)
	if beforeInit.Error == nil || beforeInit.Error.Code != CodeInvalidRequest {
		t.Fatalf("session/list before initialize = %+v, want -32600", beforeInit)
	}
	for id, want := range map[int]int{
		3: CodeInvalidParams,
		4: CodeInvalidParams,
		5: CodeInvalidParams,
		6: CodeInvalidParams,
	} {
		resp := findResponse(t, msgs, id)
		if resp.Error == nil || resp.Error.Code != want {
			t.Fatalf("request %d = %+v, want code %d", id, resp, want)
		}
	}
	// The resource-not-found error from the agent is passed through with its
	// own ACP code instead of being flattened to -32603.
	unknown := findResponse(t, msgs, 7)
	if unknown.Error == nil || unknown.Error.Code != CodeResourceNotFound {
		t.Fatalf("session/load unknown session = %+v, want -32002 passthrough", unknown)
	}
}

// TestSessionLoadBusySessionRejected: loading a session with an in-flight
// prompt is rejected with an internal error and does not disturb the running
// turn.
func TestSessionLoadBusySessionRejected(t *testing.T) {
	ops := &blockingOpsAgent{started: make(chan struct{}, 1)}
	srv, err := NewServer(Config{Agent: ops, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cwd := t.TempDir()
	ic := startInteractive(t, srv)
	ic.send(rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}))
	if resp := ic.readResponse(1, 3*time.Second); resp.Error != nil {
		t.Fatalf("initialize: %+v", resp.Error)
	}
	ic.send(rpcRequest(t, 2, MethodSessionLoad, LoadSessionRequest{SessionID: "s-busy", Cwd: cwd}))
	if resp := ic.readResponse(2, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/load: %+v", resp.Error)
	}
	ic.send(rpcRequest(t, 3, MethodSessionPrompt, PromptRequest{
		SessionID: "s-busy",
		Prompt:    []ContentBlock{{Type: "text", Text: "run"}},
	}))
	select {
	case <-ops.started:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not start")
	}
	// Loading the busy session is rejected while the turn is in flight.
	ic.send(rpcRequest(t, 4, MethodSessionLoad, LoadSessionRequest{SessionID: "s-busy", Cwd: cwd}))
	if resp := ic.readResponse(4, 3*time.Second); resp.Error == nil || resp.Error.Code != CodeInternalError {
		t.Fatalf("session/load of a busy session = %+v, want -32603", resp)
	}
	ic.send(rpcNotification(t, MethodSessionCancel, CancelNotification{SessionID: "s-busy"}))
	if resp := ic.readResponse(3, 3*time.Second); resp.Error != nil {
		t.Fatalf("cancelled prompt returned an error: %+v", resp.Error)
	}
}

// blockingOpsAgent is an opsAgent whose Prompt blocks until it is cancelled.
type blockingOpsAgent struct {
	opsAgent
	started chan struct{}
}

func (b *blockingOpsAgent) Prompt(ctx context.Context, _ PromptRequest, _ UpdateSink) (PromptResponse, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return PromptResponse{}, ctx.Err()
}

// TestAuthenticateNoOp: the default server advertises the no-op method,
// authenticates it, and keeps serving (backward-compatible: no auth gate).
func TestAuthenticateNoOp(t *testing.T) {
	srv := newTestServer(t) // fakeAgent
	msgs := serveLines(t, srv, strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodAuthenticate, AuthenticateRequest{MethodID: "no-such-method"}),
		rpcRequest(t, 3, MethodAuthenticate, AuthenticateRequest{MethodID: "none"}),
		rpcRequest(t, 4, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}),
	}, "\n")+"\n")

	unknown := findResponse(t, msgs, 2)
	if unknown.Error == nil || unknown.Error.Code != CodeInvalidParams {
		t.Fatalf("authenticate(unknown method) = %+v, want -32602", unknown)
	}
	ok := findResponse(t, msgs, 3)
	if ok.Error != nil {
		t.Fatalf("authenticate(none) error: %+v", ok.Error)
	}
	if got := strings.TrimSpace(string(ok.Result)); got != "{}" {
		t.Fatalf("authenticate result = %s, want an empty object", got)
	}
	if resp := findResponse(t, msgs, 4); resp.Error != nil {
		t.Fatalf("session/new after no-op authenticate error: %+v", resp.Error)
	}
}

// TestAuthenticateRequired: a configured authenticator gates session methods
// behind authenticate (-32000 before, success after) and reports bad
// credentials with -32000.
func TestAuthenticateRequired(t *testing.T) {
	srv, err := NewServer(Config{
		Agent:       &fakeAgent{},
		AuthMethods: []AuthMethod{{ID: "api_key", Name: "API key"}},
		Authenticate: func(_ context.Context, methodID string) error {
			if methodID != "api_key" {
				return errors.New("unsupported method")
			}
			return nil
		},
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	msgs := serveLines(t, srv, strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}),
		rpcRequest(t, 3, MethodAuthenticate, AuthenticateRequest{MethodID: "nope"}),
		rpcRequest(t, 4, MethodAuthenticate, AuthenticateRequest{MethodID: "api_key"}),
		rpcRequest(t, 5, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}),
	}, "\n")+"\n")

	initResp := findResponse(t, msgs, 1)
	initResult := decodeResult[InitializeResponse](t, initResp)
	if len(initResult.AuthMethods) != 1 || initResult.AuthMethods[0].ID != "api_key" {
		t.Fatalf("configured authMethods = %+v", initResult.AuthMethods)
	}
	gated := findResponse(t, msgs, 2)
	if gated.Error == nil || gated.Error.Code != CodeAuthRequired {
		t.Fatalf("session/new before authenticate = %+v, want -32000", gated)
	}
	badMethod := findResponse(t, msgs, 3)
	if badMethod.Error == nil || badMethod.Error.Code != CodeInvalidParams {
		t.Fatalf("authenticate(unadvertised method) = %+v, want -32602", badMethod)
	}
	if ok := findResponse(t, msgs, 4); ok.Error != nil {
		t.Fatalf("authenticate(api_key) error: %+v", ok.Error)
	}
	if resp := findResponse(t, msgs, 5); resp.Error != nil {
		t.Fatalf("session/new after authenticate error: %+v", resp.Error)
	}
}

// TestAuthenticateFailure: a rejected credential is a -32000 auth error, not an
// internal error.
func TestAuthenticateFailure(t *testing.T) {
	srv, err := NewServer(Config{
		Agent:        &fakeAgent{},
		AuthMethods:  []AuthMethod{{ID: "api_key", Name: "API key"}},
		Authenticate: func(context.Context, string) error { return errors.New("invalid key") },
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	msgs := serveLines(t, srv, strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{ProtocolVersion: ProtocolVersion}),
		rpcRequest(t, 2, MethodAuthenticate, AuthenticateRequest{MethodID: "api_key"}),
	}, "\n")+"\n")
	resp := findResponse(t, msgs, 2)
	if resp.Error == nil || resp.Error.Code != CodeAuthRequired {
		t.Fatalf("failed authenticate = %+v, want -32000", resp)
	}
	if !strings.Contains(strings.ToLower(resp.Error.Message), "auth") {
		t.Fatalf("failed authenticate message = %q, want it to mention authentication", resp.Error.Message)
	}
}
