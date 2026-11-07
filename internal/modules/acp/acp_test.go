package acp

// Tests for the ACP JSON-RPC server: initialize/capabilities, method and frame
// errors, id echo, session/new, prompt streaming/order, cancel semantics, EOF
// and context shutdown, concurrency and module wiring.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

// fakeSession is one in-memory chat session.
type fakeSession struct {
	messages []contracts.ChatMessage
	running  bool
}

// fakeChat is a scriptable contracts.ChatService.
type fakeChat struct {
	mu sync.Mutex

	next        int
	sessions    map[string]*fakeSession
	subscribers map[uint64]func(string)
	nextSub     uint64

	newSessionErr error
	sendErr       error
	cancelErr     error

	sendHook   func(*fakeChat, string, string)
	cancelHook func(*fakeChat, string)

	newSessionCalls []string
	sendCalls       []string
	cancelCalls     []string
}

func newFakeChat() *fakeChat {
	return &fakeChat{sessions: map[string]*fakeSession{}, subscribers: map[uint64]func(string){}}
}

func (c *fakeChat) Config() contracts.ChatConfig { return contracts.ChatConfig{} }

func (c *fakeChat) Sessions() []contracts.ChatSessionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]contracts.ChatSessionInfo, 0, len(c.sessions))
	for id, session := range c.sessions {
		out = append(out, contracts.ChatSessionInfo{ID: id, Running: session.running, MessageCount: len(session.messages)})
	}
	return out
}

func (c *fakeChat) Session(id string) (contracts.ChatSession, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[id]
	if !ok {
		return contracts.ChatSession{}, false
	}
	out := contracts.ChatSession{ID: id, Running: session.running,
		Messages: append([]contracts.ChatMessage(nil), session.messages...)}
	return out, true
}

func (c *fakeChat) ActiveSession() string { return "" }

func (c *fakeChat) SetActiveSession(string) error { return nil }

func (c *fakeChat) NewSession(title string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newSessionCalls = append(c.newSessionCalls, title)
	if c.newSessionErr != nil {
		return "", c.newSessionErr
	}
	c.next++
	id := fmt.Sprintf("sess-%d", c.next)
	c.sessions[id] = &fakeSession{}
	return id, nil
}

func (c *fakeChat) DeleteSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, id)
	return nil
}

func (c *fakeChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	c.sendCalls = append(c.sendCalls, sessionID+":"+prompt)
	hook := c.sendHook
	err := c.sendErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if hook != nil {
		hook(c, sessionID, prompt)
	}
	return nil
}

func (c *fakeChat) Cancel(sessionID string) error {
	c.mu.Lock()
	c.cancelCalls = append(c.cancelCalls, sessionID)
	hook := c.cancelHook
	err := c.cancelErr
	if session, ok := c.sessions[sessionID]; ok {
		session.running = false
	}
	c.mu.Unlock()
	c.notifyAll(sessionID)
	if hook != nil {
		hook(c, sessionID)
	}
	return err
}

func (c *fakeChat) Subscribe(fn func(string)) (func(), error) {
	c.mu.Lock()
	c.nextSub++
	id := c.nextSub
	c.subscribers[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subscribers, id)
		c.mu.Unlock()
	}, nil
}

func (c *fakeChat) notifyAll(sessionID string) {
	c.mu.Lock()
	listeners := make([]func(string), 0, len(c.subscribers))
	for _, listener := range c.subscribers {
		listeners = append(listeners, listener)
	}
	c.mu.Unlock()
	for _, listener := range listeners {
		listener(sessionID)
	}
}

// setAssistantText mutates the last assistant message (creating one) and
// notifies subscribers.
func (c *fakeChat) setAssistantText(sessionID, text string, running bool) {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if !ok {
		session = &fakeSession{}
		c.sessions[sessionID] = session
	}
	if len(session.messages) == 0 || session.messages[len(session.messages)-1].Role != contracts.ChatRoleAssistant {
		session.messages = append(session.messages, contracts.ChatMessage{Role: contracts.ChatRoleAssistant})
	}
	session.messages[len(session.messages)-1].Text = text
	session.running = running
	c.mu.Unlock()
	c.notifyAll(sessionID)
}

// setAssistantTextQuiet mutates state without notifying subscribers.
func (c *fakeChat) setAssistantTextQuiet(sessionID, text string, running bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[sessionID]
	if !ok {
		session = &fakeSession{}
		c.sessions[sessionID] = session
	}
	if len(session.messages) == 0 || session.messages[len(session.messages)-1].Role != contracts.ChatRoleAssistant {
		session.messages = append(session.messages, contracts.ChatMessage{Role: contracts.ChatRoleAssistant})
	}
	session.messages[len(session.messages)-1].Text = text
	session.running = running
}

// wireFrame is one decoded output frame.
type wireFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serveLines runs Serve over scripted input lines and decodes the output.
func serveLines(t *testing.T, service *service, lines ...string) []wireFrame {
	t.Helper()
	input := strings.Join(lines, "\n") + "\n"
	var output bytes.Buffer
	if err := service.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return decodeFrames(t, output.String())
}

// decodeFrames decodes newline-framed output.
func decodeFrames(t *testing.T, output string) []wireFrame {
	t.Helper()
	frames := make([]wireFrame, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var frame wireFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("decode output frame %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

// initializeLine / methods helpers.
func initializeLine(id any) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"initialize"}`, jsonValue(id))
}

func jsonValue(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func sessionNewLine(id any) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"session/new","params":{"cwd":"/tmp"}}`, jsonValue(id))
}

func promptLine(id any, sessionID, text string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":%q}]}}`,
		jsonValue(id), sessionID, text)
}

func cancelLine(id any, sessionID string) string {
	if id == nil {
		return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":%q}}`, sessionID)
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"session/cancel","params":{"sessionId":%q}}`, jsonValue(id), sessionID)
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/acp" {
		t.Fatalf("module ID = %q", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyACP.Name() {
		t.Fatalf("provides = %+v, want KeyACP", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{contracts.KeyChat.Name(), contracts.KeyLogger.Name()} {
		if !required[name] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, name)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	if err := descriptor.Validate(Config{}); err != nil {
		t.Fatalf("Validate(empty) = %v", err)
	}
	if err := descriptor.Validate(Config{TimeoutMs: -1}); err == nil {
		t.Fatal("Validate accepted a negative timeoutMs")
	}
}

func TestModuleActivationProvidesACPService(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	chat := newFakeChat()
	if _, err := root.Provide(contracts.KeyChat, contracts.ChatService(chat)); err != nil {
		t.Fatalf("provide chat: %v", err)
	}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(&fakeLogger{})); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyChat).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "acp", Module: ModuleID}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()
	acpService, err := root.Require(contracts.KeyACP)
	if err != nil {
		t.Fatalf("require KeyACP: %v", err)
	}
	var output bytes.Buffer
	if err := acpService.Serve(context.Background(),
		strings.NewReader(initializeLine("init")), &output); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(output.String(), `"protocolVersion":1`) {
		t.Fatalf("initialize output = %q", output.String())
	}
}

func TestInitializeUnknownMethodAndBadJSON(t *testing.T) {
	service := newService(Config{}, newFakeChat(), &fakeLogger{})
	frames := serveLines(t, service,
		initializeLine("init"),
		`{"jsonrpc":"2.0","id":7,"method":"no/such/method"}`,
		`{this is not json`,
		initializeLine(8),
	)
	if len(frames) != 4 {
		t.Fatalf("frames = %+v, want 4", frames)
	}
	if string(frames[0].ID) != `"init"` || frames[0].Error != nil {
		t.Fatalf("initialize response = %+v", frames[0])
	}
	var initResult struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			PromptCapabilities map[string]bool `json:"promptCapabilities"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(frames[0].Result, &initResult); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initResult.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocolVersion = %d, want %d", initResult.ProtocolVersion, ProtocolVersion)
	}
	for _, key := range []string{"image", "audio", "embeddedContext"} {
		value, ok := initResult.AgentCapabilities.PromptCapabilities[key]
		if !ok || value {
			t.Fatalf("promptCapabilities[%q] = %v/%v, want present false", key, value, ok)
		}
	}
	if frames[1].Error == nil || frames[1].Error.Code != CodeMethodNotFound || string(frames[1].ID) != "7" {
		t.Fatalf("unknown method response = %+v", frames[1])
	}
	if frames[2].Error == nil || frames[2].Error.Code != CodeParseError || string(frames[2].ID) != "null" {
		t.Fatalf("parse error response = %+v", frames[2])
	}
	if frames[3].Error != nil || frames[3].ID == nil {
		t.Fatalf("second initialize response = %+v", frames[3])
	}
}

func TestSessionNewAndPromptStreaming(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantText(sessionID, "Hello", true)
		c.setAssistantText(sessionID, "Hello world", false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service,
		sessionNewLine("new"),
		promptLine("prompt", "sess-1", "say hello"),
	)
	if len(chat.newSessionCalls) != 1 {
		t.Fatalf("NewSession calls = %v", chat.newSessionCalls)
	}
	if len(chat.sendCalls) != 1 || chat.sendCalls[0] != "sess-1:say hello" {
		t.Fatalf("Send calls = %v", chat.sendCalls)
	}
	var newResult struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(frames[0].Result, &newResult); err != nil || newResult.SessionID != "sess-1" {
		t.Fatalf("session/new result = %+v/%v", newResult, err)
	}
	var chunks []string
	responseIndex := -1
	for index, frame := range frames[1:] {
		if frame.Method == MethodSessionUpdate {
			var params struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					SessionUpdate string `json:"sessionUpdate"`
					Content       struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"update"`
			}
			if err := json.Unmarshal(frame.Params, &params); err != nil {
				t.Fatalf("decode update: %v", err)
			}
			if params.SessionID != "sess-1" || params.Update.SessionUpdate != UpdateAgentMessageChunk ||
				params.Update.Content.Type != "text" {
				t.Fatalf("update params = %+v", params)
			}
			chunks = append(chunks, params.Update.Content.Text)
			continue
		}
		if frame.Error == nil && frame.ID != nil {
			responseIndex = index + 1
		}
	}
	if strings.Join(chunks, "") != "Hello world" {
		t.Fatalf("chunks = %v, want Hello+ world", chunks)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %v, want two deltas", chunks)
	}
	if responseIndex < 0 || responseIndex != len(frames)-1 {
		t.Fatalf("prompt response index = %d of %d, want last", responseIndex, len(frames))
	}
	var promptResult struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(frames[responseIndex].Result, &promptResult); err != nil ||
		promptResult.StopReason != StopReasonEndTurn {
		t.Fatalf("prompt result = %+v/%v", promptResult, err)
	}
}

// TestPromptCompletesWithoutNotification covers a chat service that updates the
// read model silently (the post-Send snapshot path).
func TestPromptCompletesWithoutNotification(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantTextQuiet(sessionID, "silent answer", false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service, promptLine(1, "sess-1", "ask"))
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want chunk+response", frames)
	}
	if frames[0].Method != MethodSessionUpdate || !strings.Contains(string(frames[0].Params), "silent answer") {
		t.Fatalf("chunk = %+v", frames[0])
	}
	if !strings.Contains(string(frames[1].Result), "end_turn") {
		t.Fatalf("result = %s", frames[1].Result)
	}
}

func TestPromptInvalidParamsAndCancelUnknown(t *testing.T) {
	chat := newFakeChat()
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service,
		`{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"prompt":[{"type":"text","text":"x"}]}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"sess-1","prompt":[]}}`,
		`{"jsonrpc":"2.0","id":3,"method":"session/cancel","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"session/cancel","params":{"sessionId":"missing"}}`,
	)
	if len(frames) != 4 {
		t.Fatalf("frames = %+v", frames)
	}
	byID := make(map[string]wireFrame, len(frames))
	for _, frame := range frames {
		byID[string(frame.ID)] = frame
	}
	for _, id := range []string{"1", "2", "3"} {
		frame, ok := byID[id]
		if !ok || frame.Error == nil || frame.Error.Code != CodeInvalidParams {
			t.Fatalf("frame %s = %+v, want invalid params", id, frame)
		}
	}
	if frame, ok := byID["4"]; !ok || frame.Error != nil {
		t.Fatalf("cancel unknown session = %+v, want success no-op", frame)
	}
	if len(chat.cancelCalls) != 1 || chat.cancelCalls[0] != "missing" {
		t.Fatalf("cancel calls = %v", chat.cancelCalls)
	}
}

func TestPromptCancelSettlesCancelled(t *testing.T) {
	chat := newFakeChat()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantText(sessionID, "partial", true)
		once.Do(func() { close(started) })
		<-release
	}
	chat.cancelHook = func(c *fakeChat, sessionID string) {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	service := newService(Config{}, chat, &fakeLogger{})
	reader, writer := io.Pipe()
	var output bytes.Buffer
	serveDone := make(chan error, 1)
	go func() { serveDone <- service.Serve(context.Background(), reader, &output) }()
	write := func(line string) {
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			t.Fatalf("write input: %v", err)
		}
	}
	write(sessionNewLine(1))
	write(promptLine(2, "sess-1", "long turn"))
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not start")
	}
	write(cancelLine(nil, "sess-1"))
	_ = writer.Close()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	frames := decodeFrames(t, output.String())
	foundCancelled := false
	for _, frame := range frames {
		if frame.ID != nil && frame.Error == nil && strings.Contains(string(frame.Result), "cancelled") {
			foundCancelled = true
		}
	}
	if !foundCancelled {
		t.Fatalf("frames = %+v, want a cancelled stopReason", frames)
	}
	if len(chat.cancelCalls) != 1 || chat.cancelCalls[0] != "sess-1" {
		t.Fatalf("cancel calls = %v", chat.cancelCalls)
	}
}

func TestEOFAndContextShutdown(t *testing.T) {
	service := newService(Config{}, newFakeChat(), &fakeLogger{})
	if err := service.Serve(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("Serve(empty): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Serve(ctx, strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("Serve(cancelled): %v", err)
	}

	// A blocked read must not keep Serve alive after context cancellation.
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx, reader, io.Discard) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve(blocked reader): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
	_ = writer.Close()
}

func TestConcurrentPromptsAndLargeFrame(t *testing.T) {
	chat := newFakeChat()
	chat.sendHook = func(c *fakeChat, sessionID, prompt string) {
		c.setAssistantText(sessionID, "answer:"+prompt, false)
	}
	service := newService(Config{}, chat, &fakeLogger{})
	large := strings.Repeat("x", 200*1024)
	frames := serveLines(t, service,
		initializeLine("init"),
		sessionNewLine(1),
		sessionNewLine(2),
		promptLine(3, "sess-1", "first"),
		promptLine(4, "sess-2", "second"),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"session/new","params":{"blob":%q}}`, large),
	)
	// initialize + three session/new + two prompt responses + two chunks.
	if len(frames) != 8 {
		t.Fatalf("frames = %d, want 8", len(frames))
	}
	responses := 0
	for _, frame := range frames {
		if frame.ID != nil && frame.Error == nil && frame.Method == "" {
			responses++
		}
	}
	if responses != 6 {
		t.Fatalf("responses = %d, want 6 successful responses", responses)
	}
	chunks := 0
	for _, frame := range frames {
		if frame.Method == MethodSessionUpdate {
			chunks++
		}
	}
	if chunks != 2 {
		t.Fatalf("chunks = %d, want one per prompt", chunks)
	}
}
