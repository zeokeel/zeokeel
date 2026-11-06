package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// fakeHistory is an in-memory History for tests.
type fakeHistory struct {
	mu       sync.Mutex
	msgs     map[string][]Message
	appends  int
	readErr  error
	writeErr error
}

var _ History = (*fakeHistory)(nil)

func newFakeHistory() *fakeHistory {
	return &fakeHistory{msgs: make(map[string][]Message)}
}

func (h *fakeHistory) Messages(_ context.Context, sessionID string) ([]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.readErr != nil {
		return nil, h.readErr
	}
	return append([]Message(nil), h.msgs[sessionID]...), nil
}

func (h *fakeHistory) Append(_ context.Context, sessionID string, msgs ...Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.writeErr != nil {
		return h.writeErr
	}
	h.appends++
	h.msgs[sessionID] = append(h.msgs[sessionID], msgs...)
	return nil
}

func (h *fakeHistory) messages(sessionID string) []Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Message(nil), h.msgs[sessionID]...)
}

func (h *fakeHistory) appendCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.appends
}

func echoProvider(t *testing.T) *fakeProvider {
	t.Helper()
	return &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		var lastUser string
		for _, msg := range req.Messages {
			if msg.Role == RoleUser {
				lastUser = msg.Content
			}
		}
		return []Chunk{{Text: "reply:" + lastUser, Done: true}}, nil
	}}
}

// TestHistorySeedsTurnAndAppends verifies that pre-existing history is sent to
// the provider ahead of the new prompt and that the turn is appended after.
func TestHistorySeedsTurnAndAppends(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-history"] = []Message{
		{Role: RoleUser, Content: "old question"},
		{Role: RoleAssistant, Content: "old answer", Reasoning: "old reasoning"},
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{Provider: p, History: hist})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-history", "new question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	got := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		got = append(got, string(msg.Role)+":"+msg.Content)
	}
	want := []string{"user:old question", "assistant:old answer", "user:new question"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider messages = %v, want %v", got, want)
	}
	if req.Messages[1].Reasoning != "old reasoning" {
		t.Fatalf("seeded reasoning = %q", req.Messages[1].Reasoning)
	}

	stored := hist.messages("session-history")
	if len(stored) != 4 {
		t.Fatalf("history length = %d, want 4: %+v", len(stored), stored)
	}
	if stored[2].Role != RoleUser || stored[2].Content != "new question" {
		t.Fatalf("appended user message = %+v", stored[2])
	}
	if stored[3].Role != RoleAssistant || stored[3].Content != "reply:new question" {
		t.Fatalf("appended assistant message = %+v", stored[3])
	}
	if hist.appendCount() != 1 {
		t.Fatalf("Append calls = %d, want 1", hist.appendCount())
	}
}

// TestHistoryTwoTurnsRoundTrip runs two turns on one stateless-from-scratch
// Agent and checks that the second turn sees the first turn's exchange.
func TestHistoryTwoTurnsRoundTrip(t *testing.T) {
	hist := newFakeHistory()
	p := echoProvider(t)
	agent := newTestAgent(t, Config{Provider: p, History: hist})

	for _, text := range []string{"first", "second"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ch, err := agent.Run(ctx, "session-two-turns", text)
		if err != nil {
			cancel()
			t.Fatalf("Run(%q): %v", text, err)
		}
		events := collectEvents(t, ch)
		cancel()
		if _, ok := lastUsage(events); !ok {
			t.Fatalf("turn %q emitted no usage", text)
		}
	}

	second, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider got no second request")
	}
	got := make([]string, 0, len(second.Messages))
	for _, msg := range second.Messages {
		got = append(got, string(msg.Role)+":"+msg.Content)
	}
	want := []string{"user:first", "assistant:reply:first", "user:second"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("second request messages = %v, want %v", got, want)
	}

	stored := hist.messages("session-two-turns")
	wantStored := []string{"user:first", "assistant:reply:first", "user:second", "assistant:reply:second"}
	if len(stored) != len(wantStored) {
		t.Fatalf("history = %+v, want %v", stored, wantStored)
	}
	for i, wantMsg := range wantStored {
		if gotMsg := string(stored[i].Role) + ":" + stored[i].Content; gotMsg != wantMsg {
			t.Fatalf("history[%d] = %q, want %q", i, gotMsg, wantMsg)
		}
	}
}

// TestToolTurnIsAppendedToHistory verifies that the assistant tool call and the
// tool result are both persisted, which is what a provider needs to round-trip
// a tool conversation on the next turn.
func TestToolTurnIsAppendedToHistory(t *testing.T) {
	clock := &fakeClock{result: "now"}
	reg := NewRegistry()
	if err := reg.Register(clock); err != nil {
		t.Fatal(err)
	}
	hist := newFakeHistory()
	agent := newTestAgent(t, Config{Provider: scriptedToolProvider(t), Tools: reg, History: hist})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-history-tool", "what time is it?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	stored := hist.messages("session-history-tool")
	if len(stored) != 4 {
		t.Fatalf("history = %+v, want user/assistant(tool)/tool/assistant", stored)
	}
	if stored[0].Role != RoleUser || stored[1].Role != RoleAssistant || len(stored[1].ToolCalls) != 1 ||
		stored[2].Role != RoleTool || stored[3].Role != RoleAssistant {
		t.Fatalf("history order = %+v", stored)
	}
	var sawCall, sawResult bool
	for _, msg := range stored {
		switch {
		case msg.Role == RoleAssistant && len(msg.ToolCalls) == 1:
			sawCall = true
			if msg.ToolCalls[0].ID != "call-1" || msg.ToolCalls[0].Name != "clock" {
				t.Fatalf("stored tool call = %+v", msg.ToolCalls[0])
			}
		case msg.Role == RoleTool:
			sawResult = true
			if msg.ToolCallID != "call-1" || msg.Content != clock.result || msg.ToolName != "clock" {
				t.Fatalf("stored tool result = %+v", msg)
			}
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("history missing tool messages: %+v", stored)
	}
}

func TestNilHistoryIsStateless(t *testing.T) {
	p := echoProvider(t)
	agent := newTestAgent(t, Config{Provider: p})

	for _, text := range []string{"one", "two"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ch, err := agent.Run(ctx, "session-stateless", text)
		if err != nil {
			cancel()
			t.Fatalf("Run(%q): %v", text, err)
		}
		collectEvents(t, ch)
		cancel()
	}

	second, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider got no second request")
	}
	if len(second.Messages) != 1 || second.Messages[0].Role != RoleUser || second.Messages[0].Content != "two" {
		t.Fatalf("stateless second request messages = %+v", second.Messages)
	}
}

func TestHistoryReadErrorIsSetupError(t *testing.T) {
	hist := newFakeHistory()
	hist.readErr = errors.New("disk on fire")
	agent := newTestAgent(t, Config{Provider: echoProvider(t), History: hist})

	if _, err := agent.Run(context.Background(), "session", "hi"); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("Run error = %v, want history read failure", err)
	}
}

func TestHistoryAppendErrorBecomesErrorEvent(t *testing.T) {
	hist := newFakeHistory()
	hist.writeErr = errors.New("disk full")
	agent := newTestAgent(t, Config{Provider: echoProvider(t), History: hist})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-append-err", "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)

	if errText := findErrorText(events); !strings.Contains(errText, "disk full") {
		t.Fatalf("error event = %q, events: %#v", errText, events)
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseFailed {
		t.Fatalf("phases = %v, want failed", gotPhases)
	}
}
