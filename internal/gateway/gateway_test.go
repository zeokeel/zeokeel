package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeChat is an in-memory contracts.ChatService for the protocol tests. It is
// a leaf: no agent, no model, no store — exactly the read model the hub diffs.
type fakeChat struct {
	mu          sync.Mutex
	sessions    map[string]*contracts.ChatSession
	order       []string
	active      string
	subscribers map[int]func(string)
	nextSub     int
	// prompts is the optional PromptQueue half (per-session inboxes).
	prompts    map[string][]contracts.QueuedPrompt
	nextPrompt int
}

func newFakeChat() *fakeChat {
	return &fakeChat{
		sessions:    map[string]*contracts.ChatSession{},
		subscribers: map[int]func(string){},
		prompts:     map[string][]contracts.QueuedPrompt{},
	}
}

// addSession creates one session with an optional first user message.
func (f *fakeChat) addSession(id, title, firstMessage string) {
	f.mu.Lock()
	session := &contracts.ChatSession{ID: id, Title: title, UpdatedAt: time.Now()}
	if firstMessage != "" {
		session.Messages = append(session.Messages, contracts.ChatMessage{
			ID: id + "-m1", Role: contracts.ChatRoleUser, Text: firstMessage, CreatedAt: time.Now(),
		})
	}
	f.sessions[id] = session
	f.order = append(f.order, id)
	if f.active == "" {
		f.active = id
	}
	f.mu.Unlock()
	f.notify(id)
}

// appendText appends assistant text to a session, streaming style.
func (f *fakeChat) appendText(id, text string) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil {
		f.mu.Unlock()
		return
	}
	if len(session.Messages) > 0 {
		last := &session.Messages[len(session.Messages)-1]
		if last.Role == contracts.ChatRoleAssistant {
			last.Text += text
			last.Streaming = true
			session.UpdatedAt = time.Now()
			f.mu.Unlock()
			f.notify(id)
			return
		}
	}
	session.Messages = append(session.Messages, contracts.ChatMessage{
		ID:        fmt.Sprintf("%s-m%d", id, len(session.Messages)+1),
		Role:      contracts.ChatRoleAssistant,
		Text:      text,
		Streaming: true,
		CreatedAt: time.Now(),
	})
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// addMessage appends one distinct message.
func (f *fakeChat) addMessage(id, role, text string) {
	f.mu.Lock()
	session := f.sessions[id]
	if session == nil {
		f.mu.Unlock()
		return
	}
	session.Messages = append(session.Messages, contracts.ChatMessage{
		ID:        fmt.Sprintf("%s-m%d", id, len(session.Messages)+1),
		Role:      role,
		Text:      text,
		CreatedAt: time.Now(),
	})
	session.UpdatedAt = time.Now()
	f.mu.Unlock()
	f.notify(id)
}

// notify calls the subscribers synchronously, like the real agent goroutine.
func (f *fakeChat) notify(sessionID string) {
	f.mu.Lock()
	subs := make([]func(string), 0, len(f.subscribers))
	for _, fn := range f.subscribers {
		subs = append(subs, fn)
	}
	f.mu.Unlock()
	for _, fn := range subs {
		fn(sessionID)
	}
}

// Config implements contracts.ChatService.
func (f *fakeChat) Config() contracts.ChatConfig { return contracts.ChatConfig{Model: "test"} }

// Sessions implements contracts.ChatService.
func (f *fakeChat) Sessions() []contracts.ChatSessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contracts.ChatSessionInfo, 0, len(f.order))
	for i := len(f.order) - 1; i >= 0; i-- {
		session := f.sessions[f.order[i]]
		out = append(out, contracts.ChatSessionInfo{
			ID:           session.ID,
			Title:        session.Title,
			MessageCount: len(session.Messages),
			Running:      session.Running,
			UpdatedAt:    session.UpdatedAt,
		})
	}
	return out
}

// Session implements contracts.ChatService. Like the agent backend it returns a
// deep copy: the hub reads it outside this lock, so a returned slice must not
// alias the mutable session.
func (f *fakeChat) Session(sessionID string) (contracts.ChatSession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[sessionID]
	if !ok {
		return contracts.ChatSession{}, false
	}
	copied := *session
	copied.Messages = make([]contracts.ChatMessage, 0, len(session.Messages))
	for _, message := range session.Messages {
		clone := message
		clone.Tools = append([]contracts.ToolCall(nil), message.Tools...)
		clone.ReasoningSegments = append([]contracts.ReasoningSegment(nil), message.ReasoningSegments...)
		clone.Attachments = append([]contracts.Attachment(nil), message.Attachments...)
		copied.Messages = append(copied.Messages, clone)
	}
	return copied, true
}

// ActiveSession implements contracts.ChatService.
func (f *fakeChat) ActiveSession() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// SetActiveSession implements contracts.ChatService.
func (f *fakeChat) SetActiveSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = sessionID
	return nil
}

// NewSession implements contracts.ChatService.
func (f *fakeChat) NewSession(title string) (string, error) {
	id := fmt.Sprintf("s%d", len(f.order)+1)
	f.addSession(id, title, "")
	return id, nil
}

// DeleteSession implements contracts.ChatService.
func (f *fakeChat) DeleteSession(sessionID string) error {
	f.mu.Lock()
	delete(f.sessions, sessionID)
	for i, id := range f.order {
		if id == sessionID {
			f.order = append(f.order[:i], f.order[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
	f.notify(sessionID)
	return nil
}

// Send implements contracts.ChatService.
func (f *fakeChat) Send(sessionID, prompt string) error { return nil }

// Cancel implements contracts.ChatService.
func (f *fakeChat) Cancel(sessionID string) error { return nil }

// --- optional PromptQueue half --------------------------------------------

// EnqueuePrompt implements contracts.PromptQueue.
func (f *fakeChat) EnqueuePrompt(_ context.Context, sessionID, prompt string) (contracts.QueuedPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[sessionID]; !ok {
		return contracts.QueuedPrompt{}, fmt.Errorf("unknown session %q", sessionID)
	}
	if strings.TrimSpace(prompt) == "" {
		return contracts.QueuedPrompt{}, fmt.Errorf("empty prompt")
	}
	f.nextPrompt++
	entry := contracts.QueuedPrompt{
		ID:        fmt.Sprintf("q%d", f.nextPrompt),
		SessionID: sessionID,
		Text:      prompt,
		CreatedAt: time.Now(),
	}
	f.prompts[sessionID] = append(f.prompts[sessionID], entry)
	return entry, nil
}

// QueuedPrompts implements contracts.PromptQueue.
func (f *fakeChat) QueuedPrompts(sessionID string) []contracts.QueuedPrompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.QueuedPrompt(nil), f.prompts[sessionID]...)
}

// RemoveQueuedPrompt implements contracts.PromptQueue (idempotent).
func (f *fakeChat) RemoveQueuedPrompt(_ context.Context, sessionID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.prompts[sessionID]
	for i, item := range items {
		if item.ID == id {
			f.prompts[sessionID] = append(items[:i], items[i+1:]...)
			return nil
		}
	}
	return nil
}

// queuePrompts appends a prompt directly through the backend (bypassing the
// gateway), to prove the polling degradation notices backend-side changes.
func (f *fakeChat) queuePrompts(sessionID, text string) contracts.QueuedPrompt {
	f.mu.Lock()
	f.nextPrompt++
	entry := contracts.QueuedPrompt{ID: fmt.Sprintf("q%d", f.nextPrompt), SessionID: sessionID, Text: text, CreatedAt: time.Now()}
	f.prompts[sessionID] = append(f.prompts[sessionID], entry)
	f.mu.Unlock()
	return entry
}

// Subscribe implements contracts.ChatService.
func (f *fakeChat) Subscribe(fn func(string)) (func(), error) {
	f.mu.Lock()
	id := f.nextSub
	f.nextSub++
	f.subscribers[id] = fn
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		delete(f.subscribers, id)
		f.mu.Unlock()
	}, nil
}

// testLogger is a contracts.Logger that writes to the test log.
type testLogger struct {
	t *testing.T
}

func (l testLogger) Printf(format string, args ...any) { l.t.Logf(format, args...) }

// runningGateway is one gateway started for a test.
type runningGateway struct {
	gateway *Gateway
	info    Info
	cancel  context.CancelFunc
	done    chan error
	address string
}

// newTestGateway creates a gateway for chat without serving it yet; callers may
// tweak fields (the poll interval) before Serve starts reading them.
func newTestGateway(t *testing.T, chat contracts.ChatService) *Gateway {
	t.Helper()
	root := testRoot(t, chat)
	gateway, err := New(root, Options{
		DataDir: t.TempDir(),
		Port:    -1,
		Logger:  testLogger{t: t},
		Out:     discardWriter{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return gateway
}

// startTestGateway serves one gateway on a kernel-assigned port.
func startTestGateway(t *testing.T, chat contracts.ChatService) *runningGateway {
	t.Helper()
	return serveGateway(t, newTestGateway(t, chat))
}

// serveGateway runs Serve in the background and waits for gateway.json.
func serveGateway(t *testing.T, gateway *Gateway) *runningGateway {
	t.Helper()
	dataDir := gateway.opts.DataDir
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gateway.Serve(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := readInfo(dataDir)
		if err == nil {
			return &runningGateway{
				gateway: gateway,
				info:    info,
				cancel:  cancel,
				done:    done,
				address: fmt.Sprintf("127.0.0.1:%d", info.Port),
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("gateway never published %s: %v", InfoFileName, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// discardWriter is an io.Writer that drops everything.
type discardWriter struct{}

// Write implements io.Writer.
func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// cordisRootFor builds a kernel root with the backends the domain tests need.
// Every optional service is provided only when the caller passes one, mirroring
// the real products (a nil backend means "that domain has nothing to read").
func cordisRootFor(t *testing.T, chat contracts.ChatService, approvals *fakeApprovals,
	settings *fakeSettings, workspace *fakeWorkspace) cordis.Context {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if chat != nil {
		if _, err := root.Provide(contracts.KeyChat, chat); err != nil {
			t.Fatalf("provide chat: %v", err)
		}
	}
	if approvals != nil {
		if _, err := root.Provide(contracts.KeyApprovals, contracts.Approvals(approvals)); err != nil {
			t.Fatalf("provide approvals: %v", err)
		}
	}
	if settings != nil {
		if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(settings)); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	if workspace != nil {
		if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
			t.Fatalf("provide workspace: %v", err)
		}
	}
	return root
}

// stop shuts the gateway down and asserts it exited cleanly.
func (g *runningGateway) stop(t *testing.T) {
	t.Helper()
	g.cancel()
	select {
	case err := <-g.done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not stop")
	}
}

// testRoot builds a kernel root that provides a chat service.
func testRoot(t *testing.T, chat contracts.ChatService) cordis.Context {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeyChat, chat); err != nil {
		t.Fatalf("provide chat: %v", err)
	}
	return root
}

// postAuth sends the fragment-token exchange.
func postAuth(t *testing.T, address, origin string, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/auth", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /auth: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// TestServeHandshakeSnapshotAndDelta is the S1a end-to-end proof: token
// exchange, WebSocket handshake, initialize/ready, both snapshots, and a
// streaming delta produced by a service change.
func TestServeHandshakeSnapshotAndDelta(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	// 1. The fragment token buys the HttpOnly cookie (no CORS origin here: a
	// non-browser client, so only the token is required).
	authResponse := postAuth(t, gw.address, "", mustJSON(t, map[string]string{"token": gw.info.Token}))
	if authResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /auth status = %d, want 204", authResponse.StatusCode)
	}
	cookie := cookieFromResponse(t, authResponse)
	if !strings.Contains(authResponse.Header.Get("Set-Cookie"), "HttpOnly") {
		t.Fatalf("auth cookie is not HttpOnly: %q", authResponse.Header.Get("Set-Cookie"))
	}

	// 2. The control plane handshake.
	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin": gw.info.Origin(),
		"Cookie": cookie,
	})
	defer conn.close()

	handshake := resultOf(t, conn.call("initialize", map[string]any{
		"clientName":      "gateway-test",
		"clientVersion":   "0.0.0",
		"protocolVersion": ProtocolVersion,
		"capabilities":    []string{},
	}))
	if handshake["epoch"] != gw.info.Epoch {
		t.Fatalf("initialize epoch = %v, want %s", handshake["epoch"], gw.info.Epoch)
	}
	limits, _ := handshake["limits"].(map[string]any)
	if limits["maxFrameBytes"] != float64(MaxFrameBytes) {
		t.Fatalf("initialize limits = %v", limits)
	}
	resultOf(t, conn.call("ready", nil))

	// 3. Session list + one conversation.
	listResult := resultOf(t, conn.call("session.list.snapshot", nil))
	sessions, _ := listResult["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("session.list.snapshot returned %d sessions, want 1", len(sessions))
	}
	listRev := uint64(listResult["rev"].(float64))

	snapshot := resultOf(t, conn.call("session.snapshot", map[string]any{"sessionId": "s1"}))
	rev := uint64(snapshot["rev"].(float64))
	session, _ := snapshot["session"].(map[string]any)
	messages, _ := session["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("session.snapshot returned %d messages, want 1", len(messages))
	}
	first, _ := messages[0].(map[string]any)
	if first["text"] != "hello" || first["role"] != contracts.ChatRoleUser {
		t.Fatalf("first message = %v", first)
	}

	// 4. Subscribe from the snapshot's rev: no resync, no replay.
	subscribe := resultOf(t, conn.call("session.subscribe", map[string]any{"sessionId": "s1", "sinceRev": rev}))
	if subscribe["resync"] != false {
		t.Fatalf("subscribe from the current rev asked for a resync: %v", subscribe)
	}

	// 5. One streaming change reaches the client as a delta.
	chat.appendText("s1", "world")
	delta := conn.receive(func(method string) bool { return method == "session.delta" })
	params, _ := delta["params"].(map[string]any)
	if uint64(params["baseRev"].(float64)) != rev || uint64(params["rev"].(float64)) != rev+1 {
		t.Fatalf("session.delta revs = %v/%v, want %d/%d", params["baseRev"], params["rev"], rev, rev+1)
	}
	ops, _ := params["ops"].([]any)
	if len(ops) == 0 {
		t.Fatal("session.delta carried no ops")
	}
	opsJSON := mustJSON(t, ops)
	if !strings.Contains(opsJSON, `"msg.add"`) && !strings.Contains(opsJSON, `"msg.delta"`) {
		t.Fatalf("session.delta ops did not describe the new text: %s", opsJSON)
	}

	// 6. The sidebar list changed too (message count / updatedAt).
	listDelta := conn.receive(func(method string) bool { return method == "session.list.delta" })
	listParams, _ := listDelta["params"].(map[string]any)
	if listParams["baseRev"] == nil || uint64(listParams["baseRev"].(float64)) != listRev {
		t.Fatalf("session.list.delta baseRev = %v, want %d", listParams["baseRev"], listRev)
	}
}

// TestSubscribeAsksForResyncWhenRevisionIsGone proves the baseRev contract: a
// client that presents a rev the gateway cannot chain from is told to take a
// fresh snapshot, never handed an inconsistent stream.
func TestSubscribeAsksForResyncWhenRevisionIsGone(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Authorization": "Bearer " + gw.info.Token,
	})
	defer conn.close()
	resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(t, conn.call("ready", nil))

	// A rev from another epoch (or from a domain the gateway dropped).
	future := resultOf(t, conn.call("session.subscribe", map[string]any{"sessionId": "s1", "sinceRev": 99}))
	if future["resync"] != true {
		t.Fatalf("subscribe from a future rev = %v, want resync", future)
	}
	// The same domain resumed from its own rev does not resync.
	current := resultOf(t, conn.call("session.subscribe", map[string]any{
		"sessionId": "s1", "sinceRev": uint64(future["rev"].(float64)),
	}))
	if current["resync"] != false {
		t.Fatalf("subscribe from the current rev = %v, want no resync", current)
	}
}

// TestSessionSnapshotAppliesTailWindow proves the snapshot never ships an
// unbounded history without saying so (§6.4: truncation is explicit).
func TestSessionSnapshotAppliesTailWindow(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "Long", "first")
	for i := 0; i < 9; i++ {
		chat.addMessage("s1", contracts.ChatRoleAssistant, fmt.Sprintf("reply %d", i))
	}
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Authorization": "Bearer " + gw.info.Token,
	})
	defer conn.close()
	resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(t, conn.call("ready", nil))

	snapshot := resultOf(t, conn.call("session.snapshot", map[string]any{"sessionId": "s1", "tail": 2}))
	session, _ := snapshot["session"].(map[string]any)
	messages, _ := session["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("tail window returned %d messages, want 2", len(messages))
	}
	if snapshot["truncated"] != true {
		t.Fatalf("truncated = %v, want true", snapshot["truncated"])
	}
	if snapshot["oldestMessageId"] == "" || snapshot["oldestMessageId"] == nil {
		t.Fatalf("oldestMessageId missing: %v", snapshot)
	}
}

// TestUnknownSessionIsSessionNotFound proves the stable application error code
// the browser branches on (1001).
func TestUnknownSessionIsSessionNotFound(t *testing.T) {
	chat := newFakeChat()
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Authorization": "Bearer " + gw.info.Token,
	})
	defer conn.close()
	resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(t, conn.call("ready", nil))

	protocolErr := errorOf(t, conn.call("session.snapshot", map[string]any{"sessionId": "nope"}))
	if protocolErr["code"] != float64(codeSessionNotFound) {
		t.Fatalf("error = %v, want code %d", protocolErr, codeSessionNotFound)
	}
	data, _ := protocolErr["data"].(map[string]any)
	if data["name"] != "session_not_found" {
		t.Fatalf("error data = %v", data)
	}
}

// TestAuthRejectsBadTokenOriginAndMissingCookie covers the three failure paths
// of §3.2 that a browser test cannot easily provoke.
func TestAuthRejectsBadTokenOriginAndMissingCookie(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	// Wrong token.
	badToken := postAuth(t, gw.address, "", mustJSON(t, map[string]string{"token": "nope"}))
	if badToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", badToken.StatusCode)
	}
	// Foreign origin.
	badOrigin := postAuth(t, gw.address, "http://evil.example", mustJSON(t, map[string]string{"token": gw.info.Token}))
	if badOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("bad origin status = %d, want 403", badOrigin.StatusCode)
	}
	// WebSocket without a cookie (a browser would always send Origin).
	noCookie := dialTestWSError(t, gw.address, map[string]string{"Origin": gw.info.Origin()})
	if noCookie != http.StatusUnauthorized {
		t.Fatalf("no-cookie websocket status = %d, want 401", noCookie)
	}
	// WebSocket from a foreign origin with a valid token.
	badWSOrigin := dialTestWSError(t, gw.address, map[string]string{
		"Origin":        "http://evil.example",
		"Authorization": "Bearer " + gw.info.Token,
	})
	if badWSOrigin != http.StatusForbidden {
		t.Fatalf("bad-origin websocket status = %d, want 403", badWSOrigin)
	}
	// A rebound Host header is refused even for static assets.
	if status := rawStatus(t, gw.address, "GET / HTTP/1.1\r\nHost: evil.example\r\nConnection: close\r\n\r\n"); status != http.StatusForbidden {
		t.Fatalf("rebound host status = %d, want 403", status)
	}
	// Static assets do not need the token (the first paint has none). With no
	// client bundle the gateway answers with a readable "not built" page and a
	// 503 — never a panic, never a bare 404. The tree is pinned here so the
	// assertion does not depend on whether web/dist was copied into the binary.
	gw.gateway.webFS = fstest.MapFS{}
	if status := rawStatus(t, gw.address, "GET / HTTP/1.1\r\nHost: "+gw.address+"\r\nConnection: close\r\n\r\n"); status != http.StatusServiceUnavailable {
		t.Fatalf("static status without a bundle = %d, want 503", status)
	}
	// Bearer auth works without a cookie (non-browser clients).
	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Authorization": "Bearer " + gw.info.Token,
	})
	conn.close()
}

// dialTestWSError performs the handshake and returns the HTTP status the
// gateway answered with.
func dialTestWSError(t *testing.T, address string, headers map[string]string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	request := "GET /ws HTTP/1.1\r\nHost: " + address +
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"
	for name, value := range headers {
		request += name + ": " + value + "\r\n"
	}
	request += "\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode
}

// rawStatus sends one raw HTTP request and returns the status code.
func rawStatus(t *testing.T, address, request string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode
}

// TestGatewayJSONIsOwnerOnly proves the discovery file is not world readable
// (it carries the token, §2.4).
func TestGatewayJSONIsOwnerOnly(t *testing.T) {
	chat := newFakeChat()
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	info, err := readInfo(t.TempDir())
	if err == nil {
		t.Fatalf("readInfo on an empty directory returned %v, want an error", info)
	}
	path := infoPath(gw.gateway.opts.DataDir, InfoFileName)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := stat.Mode().Perm(); mode != 0o600 {
		t.Fatalf("%s mode = %o, want 600", InfoFileName, mode)
	}
	var published Info
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(payload, &published); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if published.Port != gw.info.Port || published.Epoch != gw.info.Epoch || published.Token != gw.info.Token {
		t.Fatalf("published info %+v differs from the running gateway %+v", published, gw.info)
	}
	if published.Product != defaultProduct {
		t.Fatalf("published product = %q, want %q", published.Product, defaultProduct)
	}
}
