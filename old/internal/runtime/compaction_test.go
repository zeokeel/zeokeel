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

// recordingCompactor is a scripted Compactor: it records every call and either
// delegates to fn or returns err.
type recordingCompactor struct {
	mu         sync.Mutex
	calls      int
	sessionIDs []string
	inputs     [][]Message
	budgets    []int
	fn         func(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error)
	err        error
}

var _ Compactor = (*recordingCompactor)(nil)

func (c *recordingCompactor) Compact(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error) {
	c.mu.Lock()
	c.calls++
	c.sessionIDs = append(c.sessionIDs, sessionID)
	c.inputs = append(c.inputs, cloneMessages(messages))
	c.budgets = append(c.budgets, budget)
	fn := c.fn
	err := c.err
	c.mu.Unlock()

	if fn != nil {
		return fn(ctx, sessionID, messages, budget)
	}
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (c *recordingCompactor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *recordingCompactor) inputAt(i int) []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.inputs) {
		return nil
	}
	return cloneMessages(c.inputs[i])
}

func (c *recordingCompactor) budgetAt(i int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.budgets) {
		return 0
	}
	return c.budgets[i]
}

func repeatedContent(prefix string, n int) string {
	return prefix + strings.Repeat("x", n)
}

func messageContents(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, string(msg.Role)+":"+msg.Content)
	}
	return out
}

func TestWindowCompactorKeepsSystemAndLastN(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
		{Role: RoleAssistant, Content: "m4"},
		{Role: RoleUser, Content: "m5"},
	}
	out, err := (WindowCompactor{KeepLast: 2}).Compact(context.Background(), "s", msgs, 100)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got := messageContents(out)
	want := []string{
		"system:system rules",
		"system:Earlier conversation compacted (3 messages)",
		"assistant:m4",
		"user:m5",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("compacted = %v, want %v", got, want)
	}
	// The input slice must not be mutated.
	if len(msgs) != 6 || msgs[1].Content != "m1" {
		t.Fatalf("Compact mutated its input: %+v", msgs)
	}
}

func TestWindowCompactorNoOpWhenWithinKeepLast(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, Content: "a1"},
	}
	out, err := (WindowCompactor{KeepLast: 5}).Compact(context.Background(), "s", msgs, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got, want := strings.Join(messageContents(out), "|"), strings.Join(messageContents(msgs), "|"); got != want {
		t.Fatalf("compacted = %q, want unchanged %q", got, want)
	}
}

func TestWindowCompactorReplacesPriorSummary(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleSystem, Content: "Earlier conversation compacted (99 messages)"},
		{Role: RoleUser, Content: "m1"},
		{Role: RoleAssistant, Content: "m2"},
		{Role: RoleUser, Content: "m3"},
	}
	out, err := (WindowCompactor{KeepLast: 1}).Compact(context.Background(), "s", msgs, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got := messageContents(out)
	want := []string{
		"system:system rules",
		"system:Earlier conversation compacted (2 messages)",
		"user:m3",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("compacted = %v, want %v", got, want)
	}
}

func TestCompactionTriggersOverBudgetAndPreservesOrder(t *testing.T) {
	hist := newFakeHistory()
	seeded := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: repeatedContent("u1:", 80)},
		{Role: RoleAssistant, Content: repeatedContent("a1:", 80)},
		{Role: RoleUser, Content: repeatedContent("u2:", 80)},
		{Role: RoleAssistant, Content: repeatedContent("a2:", 80)},
		{Role: RoleUser, Content: repeatedContent("u3:", 80)},
		{Role: RoleAssistant, Content: repeatedContent("a3:", 80)},
	}
	hist.msgs["session-compact"] = seeded
	compactor := &recordingCompactor{
		fn: func(_ context.Context, _ string, messages []Message, _ int) ([]Message, error) {
			return (WindowCompactor{KeepLast: 3}).Compact(context.Background(), "session-compact", messages, 20)
		},
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		History:       hist,
		Compactor:     compactor,
		ContextBudget: 20,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-compact", "new question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	if compactor.callCount() != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.callCount())
	}
	if budget := compactor.budgetAt(0); budget != 20 {
		t.Fatalf("compactor budget = %d, want 20", budget)
	}
	if got := len(compactor.inputAt(0)); got != len(seeded) {
		t.Fatalf("compactor saw %d messages, want %d", got, len(seeded))
	}

	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	got := messageContents(req.Messages)
	want := []string{
		"system:system rules",
		"system:Earlier conversation compacted (3 messages)",
		"assistant:" + repeatedContent("a2:", 80),
		"user:" + repeatedContent("u3:", 80),
		"assistant:" + repeatedContent("a3:", 80),
		"user:new question",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider messages = %v, want %v", got, want)
	}

	// History keeps the uncompacted turn messages: only the current turn is
	// appended, and the next turn compacts again from the full history.
	if stored := hist.messages("session-compact"); len(stored) != len(seeded)+2 {
		t.Fatalf("history length = %d, want %d", len(stored), len(seeded)+2)
	}
}

func TestCompactionNilCompactorIsNoOp(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-no-compactor"] = []Message{
		{Role: RoleUser, Content: repeatedContent("old:", 400)},
		{Role: RoleAssistant, Content: repeatedContent("answer:", 400)},
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		History:       hist,
		ContextBudget: 1, // over budget, but there is no compactor
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-no-compactor", "next")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	if len(req.Messages) != 3 {
		t.Fatalf("provider messages = %d, want 3 (full history + prompt)", len(req.Messages))
	}
}

func TestCompactionUnderBudgetDoesNotCallCompactor(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-under-budget"] = []Message{
		{Role: RoleUser, Content: "short"},
		{Role: RoleAssistant, Content: "brief"},
	}
	compactor := &recordingCompactor{}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		History:       hist,
		Compactor:     compactor,
		ContextBudget: 1000,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-under-budget", "next")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	if compactor.callCount() != 0 {
		t.Fatalf("compactor calls = %d, want 0", compactor.callCount())
	}
}

// TestWindowCompactorSummarySurvivesInstruction is the regression for eino's
// defaultGenModelInput: with a non-empty Instruction it strips the leading
// history system message, which used to be the only carrier of the compaction
// summary. Production always sets Instruction, so without the system-reminder
// marker every compacted turn silently dropped the summary.
func TestWindowCompactorSummarySurvivesInstruction(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-window-instruction"] = []Message{
		{Role: RoleUser, Content: repeatedContent("old-question ", 40)},
		{Role: RoleAssistant, Content: repeatedContent("old-answer ", 40)},
		{Role: RoleUser, Content: "recent-u"},
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		Instruction:   "AGENT RULES",
		History:       hist,
		Compactor:     WindowCompactor{KeepLast: 1},
		ContextBudget: 1,
	})

	events := runTurn(t, agent, "session-window-instruction", "new question")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	got := providerMessages(t, p, 0)
	want := []string{
		"system:AGENT RULES",
		"system:Earlier conversation compacted (2 messages)",
		"user:recent-u",
		"user:new question",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider messages = %v, want %v", got, want)
	}
}

// assertToolExchangeIntact verifies both directions of the OpenAI tool-call
// contract: every role=tool message answers a preceding assistant tool_calls
// entry, and every assistant tool call in msgs has a result.
func assertToolExchangeIntact(t *testing.T, msgs []Message) {
	t.Helper()
	pending := make(map[string]bool)
	for _, msg := range msgs {
		switch msg.Role {
		case RoleAssistant:
			for _, call := range msg.ToolCalls {
				pending[call.ID] = true
			}
		case RoleTool:
			if !pending[msg.ToolCallID] {
				t.Fatalf("orphan tool result %q has no preceding assistant call: %v", msg.ToolCallID, messageContents(msgs))
			}
			delete(pending, msg.ToolCallID)
		}
	}
	for id := range pending {
		t.Fatalf("assistant tool call %q has no tool result: %v", id, messageContents(msgs))
	}
}

func toolBoundaryConversation() []Message {
	return []Message{
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: RoleTool, Content: "result(c1)", ToolCallID: "c1", ToolName: "read_file"},
		{Role: RoleAssistant, Content: "a1"},
		{Role: RoleUser, Content: "u2"},
		{Role: RoleAssistant, Content: "a2"},
	}
}

// TestWindowCompactorKeepsToolGroupAcrossBoundary pins the boundary snap: a
// keep-last window that would start at a tool result must extend backward to
// the assistant message declaring that call, otherwise the compacted history
// (and the provider request) contains an orphan role=tool message.
func TestWindowCompactorKeepsToolGroupAcrossBoundary(t *testing.T) {
	msgs := toolBoundaryConversation()
	out, err := (WindowCompactor{KeepLast: 4}).Compact(context.Background(), "session-tool", msgs, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	assertToolExchangeIntact(t, out)
	if out[1].Role != RoleAssistant || len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("compacted tail starts at %+v, want the assistant call for c1", out)
	}
	if want := "system:Earlier conversation compacted (1 messages)"; messageContents(out)[0] != want {
		t.Fatalf("summary = %q, want %q", messageContents(out)[0], want)
	}

	// The same input through a real turn must stay paired in the provider
	// request, including when the summary is preserved past Instruction.
	hist := newFakeHistory()
	hist.msgs["session-tool-window"] = msgs
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		Instruction:   "AGENT RULES",
		History:       hist,
		Compactor:     WindowCompactor{KeepLast: 4},
		ContextBudget: 1,
	})
	events := runTurn(t, agent, "session-tool-window", "new")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	assertToolExchangeIntact(t, req.Messages)
	if got := messageContents(req.Messages); len(got) == 0 || !strings.Contains(strings.Join(got, "|"), "Earlier conversation compacted (1 messages)") {
		t.Fatalf("provider request lost the compaction summary: %v", got)
	}
}

// TestWindowCompactorDoesNotAliasRetainedTail is the WindowCompactor half of
// the isolation contract: mutating the compacted tail must not reach the
// conversation the compactor was given.
func TestWindowCompactorDoesNotAliasRetainedTail(t *testing.T) {
	msgs := toolBoundaryConversation()
	out, err := (WindowCompactor{KeepLast: 4}).Compact(context.Background(), "session-window-alias", msgs, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	mutated := false
	for i := range out {
		if out[i].Role == RoleAssistant && len(out[i].ToolCalls) > 0 {
			out[i].ToolCalls[0].Name = "mutated"
			mutated = true
		}
	}
	if !mutated {
		t.Fatalf("compacted tail has no assistant tool call to mutate: %v", messageContents(out))
	}
	if got := msgs[1].ToolCalls[0].Name; got != "read_file" {
		t.Fatalf("compacted output aliased the input ToolCalls: input name = %q", got)
	}
}

// TestCompactionErrorSurfacesAsEvent pins the async-compaction error contract:
// Run returns a live event channel before the summarizer runs, so a compaction
// failure must arrive as state.Error (with a failed terminal status) rather
// than a setup error. The provider is never called for the turn itself.
func TestCompactionErrorSurfacesAsEvent(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-compact-err"] = []Message{
		{Role: RoleUser, Content: repeatedContent("old:", 400)},
	}
	compactor := &recordingCompactor{err: errors.New("compactor exploded")}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		History:       hist,
		Compactor:     compactor,
		ContextBudget: 1,
	})

	ch, err := agent.Run(context.Background(), "session-compact-err", "next")
	if err != nil {
		t.Fatalf("Run: %v, want an async event channel", err)
	}
	events := collectEvents(t, ch)

	if errText := findErrorText(events); !strings.Contains(errText, "compactor exploded") {
		t.Fatalf("error event = %q, want the compactor failure", errText)
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseFailed {
		t.Fatalf("phases = %v, want a failed terminal status", gotPhases)
	}
	if _, ok := p.requestAt(0); ok {
		t.Fatal("provider was called despite compaction failure")
	}
}

// TestRunDoesNotBlockOnCompaction pins the Submit-latency regression: a
// summarizer that has not returned must not delay Run (and therefore must not
// block the controller's Submit path or the UI thread). The turn publishes
// PhaseRunning before compacting and completes normally once released.
func TestRunDoesNotBlockOnCompaction(t *testing.T) {
	hist := newFakeHistory()
	hist.msgs["session-hold-compact"] = []Message{
		{Role: RoleUser, Content: repeatedContent("old:", 400)},
		{Role: RoleAssistant, Content: repeatedContent("answer:", 400)},
	}
	started := make(chan struct{})
	release := make(chan struct{})
	compactor := &recordingCompactor{
		fn: func(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return (WindowCompactor{KeepLast: 1}).Compact(ctx, sessionID, messages, budget)
		},
	}
	p := echoProvider(t)
	agent := newTestAgent(t, Config{
		Provider:      p,
		History:       hist,
		Compactor:     compactor,
		ContextBudget: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		ch  <-chan state.Event
		err error
	}
	done := make(chan result, 1)
	begin := time.Now()
	go func() {
		ch, err := agent.Run(ctx, "session-hold-compact", "next")
		done <- result{ch: ch, err: err}
	}()

	var ch <-chan state.Event
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Run: %v", got.err)
		}
		ch = got.ch
		if elapsed := time.Since(begin); elapsed > 2*time.Second {
			t.Fatalf("Run took %v while the summarizer was blocked, want a prompt return", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked on the summarizer; Submit would block the UI thread")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("compaction never started after Run returned")
	}
	close(release)

	events := collectEvents(t, ch)
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	gotPhases := phases(events)
	if len(gotPhases) == 0 || gotPhases[len(gotPhases)-1] != state.PhaseIdle {
		t.Fatalf("phases = %v, want a completed (idle) turn", gotPhases)
	}
	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider got no request after compaction")
	}
	if !strings.Contains(strings.Join(messageContents(req.Messages), "|"), "Earlier conversation compacted") {
		t.Fatalf("provider request lacks the compacted summary: %v", messageContents(req.Messages))
	}
}
