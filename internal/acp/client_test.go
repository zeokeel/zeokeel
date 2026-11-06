package acp

// Protocol-level tests for the ACP v1 server -> client request seam:
// fs/read_text_file, fs/write_text_file, terminal/create, terminal/output,
// terminal/wait_for_exit, terminal/kill, terminal/release and
// session/request_permission.
//
// The "client" here is the test end of a real in-memory newline-framed pipe
// pair: it reads the JSON-RPC requests the server writes, asserts their wire
// shape (method, id, params) and answers them, so the real request/response
// correlation and capability gating are exercised, not a mock.
//
// Run: go test -count=1 -run 'Client|Permission' ./internal/acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// clientProbeAgent exercises the Client seam from inside Prompt. The run func
// receives the connection's Client; returning an error records it for the test
// (the prompt still settles end_turn so a protocol response always arrives).
type clientProbeAgent struct {
	run func(ctx context.Context, client Client, req PromptRequest) error

	mu  sync.Mutex
	err error
}

var _ Agent = (*clientProbeAgent)(nil)

func (a *clientProbeAgent) NewSession(context.Context, NewSessionRequest) (NewSessionResponse, error) {
	return NewSessionResponse{SessionID: "session-client"}, nil
}

func (a *clientProbeAgent) Prompt(ctx context.Context, req PromptRequest, _ UpdateSink) (PromptResponse, error) {
	client, ok := ClientFromContext(ctx)
	if !ok {
		return PromptResponse{}, errors.New("acp: no client seam on the prompt context")
	}
	if a.run != nil {
		if err := a.run(ctx, client, req); err != nil {
			a.mu.Lock()
			a.err = err
			a.mu.Unlock()
		}
	}
	return PromptResponse{StopReason: StopReasonEndTurn}, nil
}

func (a *clientProbeAgent) Cancel(context.Context, string) error { return nil }
func (a *clientProbeAgent) Close(context.Context) error          { return nil }

// probedErr returns the error the probe recorded, if any.
func (a *clientProbeAgent) probedErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// startClientConn starts a server over interactive pipes with test-friendly
// defaults.
func startClientConn(t *testing.T, cfg Config) *interactive {
	t.Helper()
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return startInteractive(t, srv)
}

// initializeClient performs the initialize handshake advertising caps.
func initializeClient(t *testing.T, ic *interactive, caps ClientCapabilities) {
	t.Helper()
	ic.send(rpcRequest(t, 1, MethodInitialize, InitializeRequest{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: caps,
	}))
	if resp := ic.readResponse(1, 3*time.Second); resp.Error != nil {
		t.Fatalf("initialize returned error: %+v", resp.Error)
	}
}

// sendPrompt sends one session/prompt request for sessionID.
func sendPrompt(t *testing.T, ic *interactive, id int, sessionID, text string) {
	t.Helper()
	ic.send(rpcRequest(t, id, MethodSessionPrompt, PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{{Type: "text", Text: text}},
	}))
}

// readServerRequest reads frames until the server sends a request whose method
// matches, returning it (blank notifications and other frames are skipped).
func (i *interactive) readServerRequest(t *testing.T, method string, timeout time.Duration) wireMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("no server request %q within %s", method, timeout)
		}
		msg := i.readFrame(remaining)
		if msg.Method == method {
			if len(msg.ID) == 0 || string(msg.ID) == "null" {
				t.Fatalf("server request %q has no id: %+v", method, msg)
			}
			return msg
		}
	}
}

// replyResult answers a server request by id with result.
func (i *interactive) replyResult(t *testing.T, id json.RawMessage, result any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": JSONRPCVersion,
		"id":      id,
		"result":  result,
	})
	if err != nil {
		t.Fatalf("marshal client response: %v", err)
	}
	i.send(string(raw))
}

// replyError answers a server request by id with a JSON-RPC error.
func (i *interactive) replyError(t *testing.T, id json.RawMessage, code int, message string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": JSONRPCVersion,
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	if err != nil {
		t.Fatalf("marshal client error response: %v", err)
	}
	i.send(string(raw))
}

// decodeParams unmarshals a server request's params into v.
func decodeParams[T any](t *testing.T, msg wireMessage) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(msg.Params, &out); err != nil {
		t.Fatalf("decode params for %q: %v (raw %s)", msg.Method, err, msg.Params)
	}
	return out
}

// TestClientReadTextFileRoundTrip: a capability-advertising client receives
// fs/read_text_file with the agent's params and its result reaches the agent.
func TestClientReadTextFileRoundTrip(t *testing.T) {
	var (
		mu   sync.Mutex
		got  ReadTextFileResponse
		gErr error
	)
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		if !client.Capabilities().Fs.ReadTextFile {
			return errors.New("agent sees no fs.readTextFile capability")
		}
		resp, err := client.ReadTextFile(ctx, ReadTextFileRequest{
			SessionID: req.SessionID,
			Path:      "/tmp/notes.txt",
		})
		mu.Lock()
		got, gErr = resp, err
		mu.Unlock()
		return nil
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{Fs: &FsCapabilities{ReadTextFile: true}})
	sendPrompt(t, ic, 2, "session-client", "read the notes")

	req := ic.readServerRequest(t, MethodFSReadTextFile, 3*time.Second)
	params := decodeParams[ReadTextFileRequest](t, req)
	if params.SessionID != "session-client" || params.Path != "/tmp/notes.txt" {
		t.Fatalf("fs/read_text_file params = %+v, want sessionId/path", params)
	}
	ic.replyResult(t, req.ID, ReadTextFileResponse{Content: "file body"})

	if resp := ic.readResponse(2, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if gErr != nil {
		t.Fatalf("client.ReadTextFile = %v", gErr)
	}
	if got.Content != "file body" {
		t.Fatalf("ReadTextFile content = %q, want %q", got.Content, "file body")
	}
}

// TestClientReadTextFileWithoutCapability: a client that did not advertise the
// capability yields a method-not-found/ability error for the agent, and the
// server does not send an fs request (nothing to answer).
func TestClientReadTextFileWithoutCapability(t *testing.T) {
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		_, err := client.ReadTextFile(ctx, ReadTextFileRequest{SessionID: req.SessionID, Path: "/tmp/x"})
		return err
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{}) // no fs capability
	sendPrompt(t, ic, 3, "session-client", "read")

	if resp := ic.readResponse(3, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	err := agent.probedErr()
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("client.ReadTextFile without capability = %v, want *RPCError", err)
	}
	if rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("capability error code = %d, want %d (method not found)", rpcErr.Code, CodeMethodNotFound)
	}
}

// TestClientWriteTextFileAndTerminals: the full client fs/terminal surface over
// one connection: write -> terminal create -> output -> wait_for_exit -> kill ->
// release, each request carrying the documented params.
func TestClientWriteTextFileAndTerminals(t *testing.T) {
	calls := []string{}
	var callsMu sync.Mutex
	record := func(method string) {
		callsMu.Lock()
		calls = append(calls, method)
		callsMu.Unlock()
	}
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		if _, err := client.WriteTextFile(ctx, WriteTextFileRequest{SessionID: req.SessionID, Path: "/tmp/out.txt", Content: "written"}); err != nil {
			return err
		}
		created, err := client.CreateTerminal(ctx, CreateTerminalRequest{
			SessionID: req.SessionID,
			Command:   "echo",
			Args:      []string{"hi"},
			Cwd:       "/tmp",
		})
		if err != nil {
			return err
		}
		out, err := client.TerminalOutput(ctx, TerminalOutputRequest{SessionID: req.SessionID, TerminalID: created.TerminalID})
		if err != nil {
			return err
		}
		if out.Output != "hi\n" || out.Truncated {
			return errors.New("unexpected terminal output payload")
		}
		exit, err := client.WaitForTerminalExit(ctx, WaitForTerminalExitRequest{SessionID: req.SessionID, TerminalID: created.TerminalID})
		if err != nil {
			return err
		}
		if exit.ExitCode == nil || *exit.ExitCode != 0 {
			return errors.New("unexpected terminal exit status")
		}
		if _, err := client.KillTerminal(ctx, KillTerminalRequest{SessionID: req.SessionID, TerminalID: created.TerminalID}); err != nil {
			return err
		}
		_, err = client.ReleaseTerminal(ctx, ReleaseTerminalRequest{SessionID: req.SessionID, TerminalID: created.TerminalID})
		return err
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{
		Fs:       &FsCapabilities{ReadTextFile: true, WriteTextFile: true},
		Terminal: true,
	})
	sendPrompt(t, ic, 5, "session-client", "use the terminal")

	want := []string{
		MethodFSWriteTextFile,
		MethodTerminalCreate,
		MethodTerminalOutput,
		MethodTerminalWaitForExit,
		MethodTerminalKill,
		MethodTerminalRelease,
	}
	for _, method := range want {
		req := ic.readServerRequest(t, method, 3*time.Second)
		record(method)
		switch method {
		case MethodFSWriteTextFile:
			params := decodeParams[WriteTextFileRequest](t, req)
			if params.Path != "/tmp/out.txt" || params.Content != "written" || params.SessionID != "session-client" {
				t.Fatalf("fs/write_text_file params = %+v", params)
			}
			ic.replyResult(t, req.ID, WriteTextFileResponse{})
		case MethodTerminalCreate:
			params := decodeParams[CreateTerminalRequest](t, req)
			if params.Command != "echo" || len(params.Args) != 1 || params.Args[0] != "hi" || params.Cwd != "/tmp" {
				t.Fatalf("terminal/create params = %+v", params)
			}
			ic.replyResult(t, req.ID, CreateTerminalResponse{TerminalID: "term-1"})
		case MethodTerminalOutput:
			params := decodeParams[TerminalOutputRequest](t, req)
			if params.TerminalID != "term-1" || params.SessionID != "session-client" {
				t.Fatalf("terminal/output params = %+v", params)
			}
			exitCode := 0
			ic.replyResult(t, req.ID, TerminalOutputResponse{
				Output:    "hi\n",
				Truncated: false,
				ExitStatus: &TerminalExitStatus{
					ExitCode: &exitCode,
				},
			})
		case MethodTerminalWaitForExit:
			params := decodeParams[WaitForTerminalExitRequest](t, req)
			if params.TerminalID != "term-1" {
				t.Fatalf("terminal/wait_for_exit params = %+v", params)
			}
			exitCode := 0
			ic.replyResult(t, req.ID, WaitForTerminalExitResponse{ExitCode: &exitCode})
		case MethodTerminalKill, MethodTerminalRelease:
			params := decodeParams[ReleaseTerminalRequest](t, req)
			if params.TerminalID != "term-1" {
				t.Fatalf("%s params = %+v", method, params)
			}
			ic.replyResult(t, req.ID, struct{}{})
		}
	}
	if resp := ic.readResponse(5, 5*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	if err := agent.probedErr(); err != nil {
		t.Fatalf("terminal sequence failed: %v", err)
	}
	callsMu.Lock()
	defer callsMu.Unlock()
	if len(calls) != len(want) {
		t.Fatalf("server requests = %v, want %v", calls, want)
	}
}

// TestClientRequestTimesOut: an unresponsive client never blocks the server
// forever; the agent gets a timeout error and the prompt still settles.
func TestClientRequestTimesOut(t *testing.T) {
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		_, err := client.ReadTextFile(ctx, ReadTextFileRequest{SessionID: req.SessionID, Path: "/tmp/slow"})
		return err
	}}
	ic := startClientConn(t, Config{Agent: agent, ClientRequestTimeout: 120 * time.Millisecond})
	initializeClient(t, ic, ClientCapabilities{Fs: &FsCapabilities{ReadTextFile: true}})
	sendPrompt(t, ic, 7, "session-client", "slow read")

	// Consume the outgoing request but never answer it.
	ic.readServerRequest(t, MethodFSReadTextFile, 3*time.Second)

	if resp := ic.readResponse(7, 5*time.Second); resp.Error != nil {
		t.Fatalf("timed-out client request must not fail the prompt: %+v", resp.Error)
	}
	err := agent.probedErr()
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("client request error = %v, want a timeout error", err)
	}
}

// TestClientRequestErrorResponse: an error response from the client reaches the
// agent as an *RPCError with the client's code.
func TestClientRequestErrorResponse(t *testing.T) {
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		_, err := client.WriteTextFile(ctx, WriteTextFileRequest{SessionID: req.SessionID, Path: "/tmp/x", Content: "y"})
		return err
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{Fs: &FsCapabilities{WriteTextFile: true}})
	sendPrompt(t, ic, 8, "session-client", "write")

	req := ic.readServerRequest(t, MethodFSWriteTextFile, 3*time.Second)
	ic.replyError(t, req.ID, CodeAuthRequired, "the client refused the write")
	if resp := ic.readResponse(8, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	err := agent.probedErr()
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeAuthRequired {
		t.Fatalf("client error response = %v, want *RPCError code %d", err, CodeAuthRequired)
	}
}

// TestClientMalformedResponse: a malformed result payload surfaces as an agent
// error instead of a panic.
func TestClientMalformedResponse(t *testing.T) {
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		_, err := client.ReadTextFile(ctx, ReadTextFileRequest{SessionID: req.SessionID, Path: "/tmp/x"})
		return err
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{Fs: &FsCapabilities{ReadTextFile: true}})
	sendPrompt(t, ic, 9, "session-client", "malformed")

	req := ic.readServerRequest(t, MethodFSReadTextFile, 3*time.Second)
	ic.replyResult(t, req.ID, map[string]any{"content": 42})
	if resp := ic.readResponse(9, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	if err := agent.probedErr(); err == nil {
		t.Fatal("malformed client response produced no agent error")
	}
}

// TestClientRequestPermissionRoundTrip: session/request_permission is sent to
// the client with a toolCall and the four standard options, and the selected
// option reaches the agent.
func TestClientRequestPermissionRoundTrip(t *testing.T) {
	var mu sync.Mutex
	var got RequestPermissionResponse
	var gErr error
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		resp, err := client.RequestPermission(ctx, RequestPermissionRequest{
			SessionID: req.SessionID,
			ToolCall: ToolCallUpdate{
				ToolCallID: "call-9",
				Title:      "bash",
				Status:     "pending",
			},
			Options: PermissionOptions(),
		})
		mu.Lock()
		got, gErr = resp, err
		mu.Unlock()
		return nil
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{})
	sendPrompt(t, ic, 10, "session-client", "needs permission")

	req := ic.readServerRequest(t, MethodSessionRequestPermission, 3*time.Second)
	params := decodeParams[RequestPermissionRequest](t, req)
	if params.SessionID != "session-client" || params.ToolCall.ToolCallID != "call-9" || params.ToolCall.Title != "bash" {
		t.Fatalf("session/request_permission params = %+v", params)
	}
	kinds := map[string]bool{}
	for _, opt := range params.Options {
		kinds[opt.Kind] = true
		if opt.OptionID == "" || opt.Name == "" {
			t.Fatalf("permission option is incomplete: %+v", opt)
		}
	}
	for _, want := range []string{
		PermissionKindAllowOnce, PermissionKindAllowAlways,
		PermissionKindRejectOnce, PermissionKindRejectAlways,
	} {
		if !kinds[want] {
			t.Fatalf("permission options missing kind %q: %+v", want, params.Options)
		}
	}
	ic.replyResult(t, req.ID, RequestPermissionResponse{
		Outcome: RequestPermissionOutcome{Outcome: "selected", OptionID: PermissionKindAllowOnce},
	})
	if resp := ic.readResponse(10, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if gErr != nil {
		t.Fatalf("RequestPermission = %v", gErr)
	}
	if got.Outcome.Outcome != "selected" || got.Outcome.OptionID != PermissionKindAllowOnce {
		t.Fatalf("permission outcome = %+v, want selected allow_once", got.Outcome)
	}
}

// TestClientRequestPermissionCancelled: a cancelled outcome is a normal
// response (the agent decides to treat it as a rejection), not a transport
// error.
func TestClientRequestPermissionCancelled(t *testing.T) {
	var mu sync.Mutex
	var got RequestPermissionResponse
	var gErr error
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		resp, err := client.RequestPermission(ctx, RequestPermissionRequest{
			SessionID: req.SessionID,
			ToolCall:  ToolCallUpdate{ToolCallID: "call-10", Title: "bash"},
			Options:   PermissionOptions(),
		})
		mu.Lock()
		got, gErr = resp, err
		mu.Unlock()
		return nil
	}}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{})
	sendPrompt(t, ic, 11, "session-client", "cancel permission")

	req := ic.readServerRequest(t, MethodSessionRequestPermission, 3*time.Second)
	ic.replyResult(t, req.ID, RequestPermissionResponse{
		Outcome: RequestPermissionOutcome{Outcome: "cancelled"},
	})
	if resp := ic.readResponse(11, 3*time.Second); resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if gErr != nil {
		t.Fatalf("RequestPermission = %v", gErr)
	}
	if got.Outcome.Outcome != "cancelled" {
		t.Fatalf("permission outcome = %+v, want cancelled", got.Outcome)
	}
}

// TestClientRequestInjectedSeam: Config.ClientRequest overrides the transport
// seam, so an embedder/test can answer client requests without a real client
// connection; capability gating still applies.
func TestClientRequestInjectedSeam(t *testing.T) {
	type recorded struct {
		method string
		params json.RawMessage
	}
	var (
		mu    sync.Mutex
		calls []recorded
	)
	injected := func(_ context.Context, method string, params any) (json.RawMessage, error) {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		calls = append(calls, recorded{method: method, params: raw})
		mu.Unlock()
		return json.RawMessage(`{"content":"injected"}`), nil
	}
	var got string
	agent := &clientProbeAgent{run: func(ctx context.Context, client Client, req PromptRequest) error {
		resp, err := client.ReadTextFile(ctx, ReadTextFileRequest{SessionID: req.SessionID, Path: "/tmp/injected"})
		if err != nil {
			return err
		}
		got = resp.Content
		return nil
	}}
	srv, err := NewServer(Config{Agent: agent, Logf: func(string, ...any) {}, ClientRequest: injected})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	input := strings.Join([]string{
		rpcRequest(t, 1, MethodInitialize, InitializeRequest{
			ProtocolVersion:    ProtocolVersion,
			ClientCapabilities: ClientCapabilities{Fs: &FsCapabilities{ReadTextFile: true}},
		}),
		rpcRequest(t, 2, MethodSessionPrompt, PromptRequest{
			SessionID: "session-injected",
			Prompt:    []ContentBlock{{Type: "text", Text: "read"}},
		}),
	}, "\n") + "\n"
	msgs := serveLines(t, srv, input)
	resp := findResponse(t, msgs, 2)
	if resp.Error != nil {
		t.Fatalf("session/prompt returned error: %+v", resp.Error)
	}
	if got != "injected" {
		t.Fatalf("agent read content = %q, want injected", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || calls[0].method != MethodFSReadTextFile {
		t.Fatalf("injected client requests = %+v, want one fs/read_text_file", calls)
	}
}

// syncProbeAgent tries to use the Client seam from a synchronous control
// handler (session/new): the connection's read loop is busy dispatching that
// handler, so a server->client request there can never be answered. The seam
// must fail fast with an explicit contract error instead of blocking until the
// client-request timeout.
type syncProbeAgent struct {
	mu  sync.Mutex
	err error
}

var _ Agent = (*syncProbeAgent)(nil)

func (a *syncProbeAgent) NewSession(ctx context.Context, _ NewSessionRequest) (NewSessionResponse, error) {
	client, ok := ClientFromContext(ctx)
	if !ok {
		return NewSessionResponse{}, errors.New("acp: no client seam on the control context")
	}
	_, err := client.ReadTextFile(ctx, ReadTextFileRequest{Path: "notes.txt"})
	a.mu.Lock()
	a.err = err
	a.mu.Unlock()
	return NewSessionResponse{SessionID: "sync-session"}, nil
}

func (a *syncProbeAgent) Prompt(context.Context, PromptRequest, UpdateSink) (PromptResponse, error) {
	return PromptResponse{StopReason: StopReasonEndTurn}, nil
}

func (a *syncProbeAgent) Cancel(context.Context, string) error { return nil }
func (a *syncProbeAgent) Close(context.Context) error          { return nil }

// probedErr returns the error the synchronous handler saw.
func (a *syncProbeAgent) probedErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// TestClientSeamRejectsSynchronousControlHandler pins the contract: server->client
// requests are only allowed from an asynchronous handler (session/prompt).
// A synchronous control handler gets an explicit error immediately, and the
// connection keeps working afterwards.
func TestClientSeamRejectsSynchronousControlHandler(t *testing.T) {
	agent := &syncProbeAgent{}
	ic := startClientConn(t, Config{Agent: agent})
	initializeClient(t, ic, ClientCapabilities{Fs: &FsCapabilities{ReadTextFile: true}})
	start := time.Now()
	ic.send(rpcRequest(t, 2, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}))
	resp := ic.readResponse(2, 3*time.Second)
	if resp.Error != nil {
		t.Fatalf("session/new failed instead of returning: %+v", resp.Error)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("synchronous control handler blocked for %v; the client seam must fail fast", elapsed)
	}
	err := agent.probedErr()
	if err == nil {
		t.Fatal("client request from a synchronous control handler succeeded; want an explicit contract error")
	}
	if !strings.Contains(err.Error(), "synchronous") || !strings.Contains(err.Error(), "session/prompt") {
		t.Fatalf("contract error = %v, want it to name the synchronous-handler restriction and the async venue", err)
	}
	// The connection must stay healthy after the rejected call.
	ic.send(rpcRequest(t, 3, MethodSessionNew, NewSessionRequest{Cwd: t.TempDir()}))
	if resp := ic.readResponse(3, 3*time.Second); resp.Error != nil {
		t.Fatalf("connection unhealthy after the guard: %+v", resp.Error)
	}
}
