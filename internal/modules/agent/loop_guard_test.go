package agent

// Tests for the loop-hygiene repeat reminder: DSH's repeat-tool-reminder rules
// (exact canonical identity, consecutive counts, [3, 5, 8] thresholds, todo
// tools transparent, per-run chain) applied at ZeoKeel's tool seam.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestCanonicalizeToolArguments pins the identity rules: property order and
// number spelling do not matter, different values do, and malformed JSON falls
// back to its raw text.
func TestCanonicalizeToolArguments(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		same bool
	}{
		{name: "property order", a: `{"b":1,"a":2}`, b: `{"a":2,"b":1}`, same: true},
		{name: "nested order", a: `{"x":{"b":1,"a":2}}`, b: `{"x":{"a":2,"b":1}}`, same: true},
		{name: "number spelling", a: `{"n":1}`, b: `{"n":1.0}`, same: true},
		{name: "different value", a: `{"a":1}`, b: `{"a":2}`, same: false},
		{name: "different shape", a: `{"a":1}`, b: `[1]`, same: false},
		{name: "empty", a: "", b: " ", same: true},
		{name: "malformed raw", a: `{"text":`, b: `{"text":`, same: true},
		{name: "malformed differs", a: `{"text":`, b: `{"text":"x"`, same: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := canonicalizeToolArguments(tc.a) == canonicalizeToolArguments(tc.b)
			if got != tc.same {
				t.Fatalf("canonicalize(%q) == canonicalize(%q) = %t, want %t (%q vs %q)",
					tc.a, tc.b, got, tc.same, canonicalizeToolArguments(tc.a), canonicalizeToolArguments(tc.b))
			}
		})
	}
}

// TestRepeatToolGuardThresholds pins the DSH default thresholds: the gentle
// reminder at the first threshold, detailed reminders at the later ones.
func TestRepeatToolGuardThresholds(t *testing.T) {
	guard := newRepeatToolGuardFor([]int{3, 5, 8}, nil, 500)
	const args = `{"text":"same"}`
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("first call reminded: %q", reminder)
	}
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("second call reminded: %q", reminder)
	}
	third := guard.observe("echo", args)
	if !strings.Contains(third, "You are repeating the exact same tool call") {
		t.Fatalf("third call reminder = %q, want the gentle reminder", third)
	}
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("fourth call reminded: %q", reminder)
	}
	fifth := guard.observe("echo", args)
	for _, want := range []string{"Repeated tool call detected:", "- tool: echo", "- consecutive_calls: 5", `- arguments: {"text":"same"}`} {
		if !strings.Contains(fifth, want) {
			t.Fatalf("fifth call reminder lacks %q:\n%s", want, fifth)
		}
	}
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("sixth call reminded: %q", reminder)
	}
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("seventh call reminded: %q", reminder)
	}
	eighth := guard.observe("echo", args)
	if !strings.Contains(eighth, "- consecutive_calls: 8") {
		t.Fatalf("eighth call reminder = %q, want count 8", eighth)
	}
}

// TestRepeatToolGuardDifferentCallResets covers the chain rule: a different
// tracked call restarts the count, so the repeated echo calls before and after
// it do not add up to a threshold hit.
func TestRepeatToolGuardDifferentCallResets(t *testing.T) {
	guard := newRepeatToolGuardFor([]int{3}, nil, 500)
	const args = `{"text":"same"}`
	guard.observe("echo", args)
	guard.observe("echo", args)
	if reminder := guard.observe("read_file", `{"path":"a"}`); reminder != "" {
		t.Fatalf("different call reminded: %q", reminder)
	}
	// echo restarts at 1 because read_file was the last tracked call.
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("first echo after a reset reminded: %q", reminder)
	}
	if reminder := guard.observe("echo", args); reminder != "" {
		t.Fatalf("second echo after a reset reminded: %q", reminder)
	}
	reminder := guard.observe("echo", args)
	if !strings.Contains(reminder, "You are repeating the exact same tool call") {
		t.Fatalf("third consecutive echo reminder = %q, want the gentle reminder", reminder)
	}
}

// TestRepeatToolGuardExcludedToolsTransparent verifies the transparent chain
// precisely, with the excluded call interleaved before the threshold.
func TestRepeatToolGuardExcludedToolsTransparent(t *testing.T) {
	guard := newRepeatToolGuardFor([]int{3}, map[string]bool{"todo_write": true}, 500)
	const args = `{"text":"same"}`
	guard.observe("echo", args)
	guard.observe("echo", args)
	if reminder := guard.observe("todo_write", `{"todos":[]}`); reminder != "" {
		t.Fatalf("excluded tool reminded: %q", reminder)
	}
	reminder := guard.observe("echo", args)
	if !strings.Contains(reminder, "You are repeating the exact same tool call") {
		t.Fatalf("third tracked call reminder = %q, want the gentle reminder", reminder)
	}
}

// TestRepeatToolGuardPreviewBoundsArguments proves the detailed reminder caps
// the argument text without splitting a UTF-8 rune.
func TestRepeatToolGuardPreviewBoundsArguments(t *testing.T) {
	guard := newRepeatToolGuardFor([]int{2, 3}, nil, 16)
	long := `{"text":"` + strings.Repeat("中", 40) + `"}`
	guard.observe("echo", long)
	if gentle := guard.observe("echo", long); !strings.Contains(gentle, "You are repeating") {
		t.Fatalf("second call reminder = %q, want the gentle reminder", gentle)
	}
	reminder := guard.observe("echo", long)
	if !strings.Contains(reminder, "… (+") || !strings.Contains(reminder, "more chars)") {
		t.Fatalf("detailed reminder does not mark the truncation: %q", reminder)
	}
	if strings.ToValidUTF8(reminder, "") != reminder {
		t.Fatalf("detailed reminder is not valid UTF-8: %q", reminder)
	}
}

// stubNoticeTool is a tool that also implements rig.NoticeSource.
type stubNoticeTool struct {
	notices []string
}

func (t *stubNoticeTool) Name() string           { return "stub_notice" }
func (t *stubNoticeTool) Description() string    { return "stub" }
func (t *stubNoticeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *stubNoticeTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}
func (t *stubNoticeTool) PendingNotices() []string { return t.notices }

type stubPlainTool struct{ result rig.ToolResult }

func (t *stubPlainTool) Name() string           { return "stub_plain" }
func (t *stubPlainTool) Description() string    { return "stub" }
func (t *stubPlainTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *stubPlainTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return t.result, nil
}

// TestWithRepeatGuardAppendsReminderAndPreservesNotices covers the wrapper
// contract: metadata is unchanged, the reminder is appended to the repeated
// result (no reminder before the threshold), IsError survives, and the optional
// NoticeSource capability is forwarded.
func TestWithRepeatGuardAppendsReminderAndPreservesNotices(t *testing.T) {
	notice := &stubNoticeTool{notices: []string{"job finished"}}
	plain := &stubPlainTool{result: rig.ToolResult{Content: "plain result", IsError: true}}
	guard := newRepeatToolGuardFor([]int{3}, nil, 500)
	tools := withRepeatGuard([]rig.Tool{notice, plain}, guard)
	if len(tools) != 2 {
		t.Fatalf("wrapped tools = %d, want 2", len(tools))
	}
	if tools[0].Name() != "stub_notice" || tools[0].Description() != "stub" {
		t.Fatalf("wrapped metadata changed: %s / %s", tools[0].Name(), tools[0].Description())
	}
	source, ok := tools[0].(rig.NoticeSource)
	if !ok {
		t.Fatal("wrapped notice tool lost rig.NoticeSource")
	}
	if got := source.PendingNotices(); len(got) != 1 || got[0] != "job finished" {
		t.Fatalf("forwarded notices = %v", got)
	}
	if _, ok := tools[1].(rig.NoticeSource); ok {
		t.Fatal("wrapped plain tool unexpectedly implements rig.NoticeSource")
	}

	call := rig.ToolCall{Name: "stub_plain", Arguments: `{"a":1}`}
	for index := 1; index <= 3; index++ {
		result, err := tools[1].Invoke(context.Background(), call)
		if err != nil {
			t.Fatalf("invoke %d: %v", index, err)
		}
		if !result.IsError {
			t.Fatalf("invoke %d lost IsError", index)
		}
		containsReminder := strings.Contains(result.Content, "You are repeating the exact same tool call")
		if index < 3 && containsReminder {
			t.Fatalf("invoke %d got a reminder before the threshold: %q", index, result.Content)
		}
		if index == 3 && !containsReminder {
			t.Fatalf("third invoke lacks the reminder: %q", result.Content)
		}
	}
}

// TestGuardedToolPreservesToolErrors keeps Rig's error semantics: a tool's Go
// error is returned unchanged.
func TestGuardedToolPreservesToolErrors(t *testing.T) {
	boom := errors.New("boom")
	inner := &errorStubTool{err: boom}
	wrapped := withRepeatGuard([]rig.Tool{inner}, newRepeatToolGuard())[0]
	result, err := wrapped.Invoke(context.Background(), rig.ToolCall{Name: "error_stub"})
	if !errors.Is(err, boom) {
		t.Fatalf("invoke error = %v, want %v", err, boom)
	}
	if result.Content != "" {
		t.Fatalf("invoke result = %+v, want the inner error result", result)
	}
}

// errorStubTool fails every invoke with a fixed error.
type errorStubTool struct{ err error }

func (t *errorStubTool) Name() string           { return "error_stub" }
func (t *errorStubTool) Description() string    { return "stub" }
func (t *errorStubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *errorStubTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{}, t.err
}

// repeatingToolProvider requests one identical echo call per model round, then
// returns a final answer; it records every request so tests can inspect the
// tool results the model saw.
type repeatingToolProvider struct {
	mu       sync.Mutex
	calls    int
	requests []rig.Request
}

func (p *repeatingToolProvider) Name() string { return "repeating" }

func (p *repeatingToolProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if call%4 != 0 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: fmt.Sprintf("call-%d", call), Name: "echo", Arguments: `{"text":"same"}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

// lastToolText returns the text of the last tool message in one request.
func lastToolText(t *testing.T, request rig.Request) (string, bool) {
	t.Helper()
	for index := len(request.Messages) - 1; index >= 0; index-- {
		message := request.Messages[index]
		if message.Role != rig.RoleTool {
			continue
		}
		var text strings.Builder
		for _, part := range message.Content {
			text.WriteString(part.Text)
		}
		return text.String(), true
	}
	return "", false
}

// TestRunRepeatToolCallsGetReminder is the loop-level integration test: the
// third consecutive identical call's tool result carries the gentle reminder
// into the next model request, and a following run starts a fresh chain.
func TestRunRepeatToolCallsGetReminder(t *testing.T) {
	provider := &repeatingToolProvider{}
	svc := newTestService(t, Config{}, nil, nil, &echoTool{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("loop")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "go"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)

	provider.mu.Lock()
	requests := append([]rig.Request(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(requests))
	}
	// Request 3 is the one sent after the second tool round: no reminder yet.
	if text, ok := lastToolText(t, requests[2]); ok && strings.Contains(text, "You are repeating the exact same tool call") {
		t.Fatalf("second repeat already received a reminder: %q", text)
	}
	// Request 4 is sent after the third tool round: the reminder must be there.
	text, ok := lastToolText(t, requests[3])
	if !ok {
		t.Fatal("fourth request has no tool message")
	}
	if !strings.Contains(text, "You are repeating the exact same tool call") {
		t.Fatalf("third repeat result lacks the reminder: %q", text)
	}

	// A second run resets the chain: its third repeat must remind again, so the
	// total number of reminders across both runs is two (a leaked chain would
	// produce a different escalation at counts 5/6/7 instead).
	if err := svc.Send(id, "again"); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	waitIdle(t, svc, id)
	provider.mu.Lock()
	requests = append([]rig.Request(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 8 {
		t.Fatalf("model requests = %d, want 8", len(requests))
	}
	// The last request carries both runs' tool results; one reminder per run is
	// the fresh-chain evidence (a leaked chain would escalate at counts 5+).
	reminders := 0
	for _, message := range requests[len(requests)-1].Messages {
		if message.Role != rig.RoleTool {
			continue
		}
		for _, part := range message.Content {
			if strings.Contains(part.Text, "You are repeating the exact same tool call") {
				reminders++
			}
		}
	}
	if reminders != 2 {
		t.Fatalf("gentle reminders across two runs = %d, want 2 (per-run chain reset)", reminders)
	}
}
