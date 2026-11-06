package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestLLMCompactorSummarizes pins real LLM compaction: the compactor asks the
// provider to summarize the dropped part of the conversation, keeps the
// system prompt and the newest messages, and stores the model's summary as a
// stable system message (rather than an empty placeholder).
func TestLLMCompactorSummarizes(t *testing.T) {
	const summaryText = "SUMMARY: user worked on alpha; the assistant answered with beta details."
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: repeatedContent("old-question ", 40)},
		{Role: RoleAssistant, Content: repeatedContent("old-answer ", 40)},
		{Role: RoleUser, Content: repeatedContent("middle-question ", 40)},
		{Role: RoleAssistant, Content: repeatedContent("middle-answer ", 40)},
		{Role: RoleUser, Content: "recent question"},
		{Role: RoleAssistant, Content: "recent answer"},
	}
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		if len(req.Messages) == 0 || req.Messages[0].Content != LLMCompactorSystemPrompt {
			return nil, errors.New("LLMCompactor did not send its summarizer prompt")
		}
		var sawOld bool
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, "old-question") {
				sawOld = true
			}
		}
		if !sawOld {
			return nil, errors.New("summarizer prompt omitted the old conversation")
		}
		return []Chunk{
			{Text: "SUMMARY: user worked on alpha; "},
			{Text: "the assistant answered with beta details.", Done: true},
		}, nil
	}}

	compactor := LLMCompactor{Provider: p, Model: "summary-model", KeepLast: 2}
	out, err := compactor.Compact(context.Background(), "session-llm", msgs, 100)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out) >= len(msgs) {
		t.Fatalf("compacted conversation has %d messages, want fewer than the input's %d", len(out), len(msgs))
	}
	if out[0].Role != RoleSystem || out[0].Content != "system rules" {
		t.Fatalf("compacted[0] = %s:%q, want the original system prompt", out[0].Role, out[0].Content)
	}

	var summary string
	for _, msg := range out {
		if msg.Role == RoleSystem && strings.Contains(msg.Content, "SUMMARY:") {
			summary = msg.Content
			break
		}
	}
	if summary == "" {
		t.Fatalf("compacted conversation has no system summary: %v", messageContents(out))
	}
	if !strings.HasPrefix(summary, compactionSummaryPrefix) {
		t.Fatalf("summary = %q, want the stable compaction prefix %q", summary, compactionSummaryPrefix)
	}
	if !strings.Contains(summary, summaryText) {
		t.Fatalf("summary = %q, want it to preserve the provider's text %q", summary, summaryText)
	}

	// The newest messages survive verbatim, and dropped content is gone.
	gotTail := messageContents(out[len(out)-2:])
	wantTail := []string{"user:recent question", "assistant:recent answer"}
	if strings.Join(gotTail, "|") != strings.Join(wantTail, "|") {
		t.Fatalf("compacted tail = %v, want %v", gotTail, wantTail)
	}
	for _, msg := range out {
		if strings.Contains(msg.Content, "old-question") || strings.Contains(msg.Content, "middle-answer") {
			t.Fatalf("compacted conversation still contains dropped content: %q", msg.Content)
		}
	}

	// The compactor must not mutate the conversation it was given.
	if msgs[1].Content != repeatedContent("old-question ", 40) || len(msgs) != 7 {
		t.Fatal("Compact mutated its input")
	}

	// The provider request must carry the old conversation and the model name
	// the app selected, with no tools.
	req, ok := p.requestAt(0)
	if !ok {
		t.Fatal("provider received no summarization request")
	}
	if req.Model != "summary-model" {
		t.Fatalf("summarizer model = %q, want %q", req.Model, "summary-model")
	}
	if req.SessionID != "session-llm" {
		t.Fatalf("summarizer session = %q, want %q", req.SessionID, "session-llm")
	}
	if len(req.Tools) != 0 {
		t.Fatalf("summarizer request carries %d tools, want none", len(req.Tools))
	}
}

// TestLLMCompactorSummarySurvivesInstruction is the LLM half of the
// production regression: eino strips a leading history system message when the
// agent has an Instruction, so the compaction summary must be marked as a
// system reminder to reach the model.
func TestLLMCompactorSummarySurvivesInstruction(t *testing.T) {
	const summaryText = "MODEL SUMMARY"
	hist := newFakeHistory()
	hist.msgs["session-llm-instruction"] = []Message{
		{Role: RoleUser, Content: repeatedContent("old-question ", 40)},
		{Role: RoleAssistant, Content: repeatedContent("old-answer ", 40)},
		{Role: RoleUser, Content: "recent-u"},
	}
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		if len(req.Messages) > 0 && req.Messages[0].Content == LLMCompactorSystemPrompt {
			return []Chunk{{Text: summaryText, Done: true}}, nil
		}
		return []Chunk{{Text: "turn reply", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{
		Provider:      p,
		Instruction:   "AGENT RULES",
		History:       hist,
		Compactor:     LLMCompactor{Provider: p, KeepLast: 1},
		ContextBudget: 1,
	})

	events := runTurn(t, agent, "session-llm-instruction", "new question")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	summarizer, ok := p.requestAt(0)
	if !ok || len(summarizer.Messages) == 0 || summarizer.Messages[0].Content != LLMCompactorSystemPrompt {
		t.Fatalf("first provider request is not the summarizer: %+v", summarizer.Messages)
	}
	req, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider received no turn request after summarization")
	}
	want := []string{
		"system:AGENT RULES",
		"system:" + fmt.Sprintf("%s%d messages)\n\n%s", compactionSummaryPrefix, 2, summaryText),
		"user:recent-u",
		"user:new question",
	}
	got := messageContents(req.Messages)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("turn provider messages = %v, want %v", got, want)
	}
}

// TestLLMCompactorKeepsToolGroupAcrossBoundary covers the LLM compactor's
// shared boundary logic: a retained tail may not start with a tool result whose
// assistant tool call was summarized away, and a retained assistant call must
// keep its result.
func TestLLMCompactorKeepsToolGroupAcrossBoundary(t *testing.T) {
	msgs := toolBoundaryConversation()
	direct := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		return []Chunk{{Text: "TOOL SUMMARY", Done: true}}, nil
	}}
	out, err := (LLMCompactor{Provider: direct, KeepLast: 4}).Compact(context.Background(), "session-tool-llm", msgs, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	assertToolExchangeIntact(t, out)
	got := messageContents(out)
	if len(got) < 2 || !strings.Contains(got[0], "Earlier conversation compacted (1 messages)") ||
		!strings.Contains(got[0], "TOOL SUMMARY") {
		t.Fatalf("compacted summary = %v, want one dropped message plus the model summary", got)
	}

	// The same input through a real turn: the summarizer request comes first,
	// then the turn request that must retain the tool exchange and the summary.
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		if len(req.Messages) > 0 && req.Messages[0].Content == LLMCompactorSystemPrompt {
			return []Chunk{{Text: "TOOL SUMMARY", Done: true}}, nil
		}
		return []Chunk{{Text: "turn reply", Done: true}}, nil
	}}
	hist := newFakeHistory()
	hist.msgs["session-tool-llm"] = msgs
	agent := newTestAgent(t, Config{
		Provider:      p,
		Instruction:   "AGENT RULES",
		History:       hist,
		Compactor:     LLMCompactor{Provider: p, KeepLast: 4},
		ContextBudget: 1,
	})
	events := runTurn(t, agent, "session-tool-llm", "new")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn produced error event: %q", errText)
	}
	if _, ok := p.requestAt(0); !ok {
		t.Fatal("provider received no summarizer request")
	}
	req, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider received no turn request after summarization")
	}
	assertToolExchangeIntact(t, req.Messages)
	if !strings.Contains(strings.Join(messageContents(req.Messages), "|"), "TOOL SUMMARY") {
		t.Fatalf("turn request lost the summary: %v", messageContents(req.Messages))
	}
}

// TestLLMCompactorMaxSummaryChars pins the documented character cap: the
// ellipsis is part of the limit, not an extra rune on top of it.
func TestLLMCompactorMaxSummaryChars(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "old question"},
		{Role: RoleAssistant, Content: "old answer"},
		{Role: RoleUser, Content: "recent"},
	}
	cases := []struct {
		name    string
		summary string
		max     int
	}{
		{"ascii", strings.Repeat("a", 30), 5},
		{"unicode", "你好世界你好世界", 3},
		{"single rune cap", "abcdef", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
				return []Chunk{{Text: tc.summary, Done: true}}, nil
			}}
			out, err := (LLMCompactor{Provider: p, KeepLast: 1, MaxSummaryChars: tc.max}).Compact(
				context.Background(), "session-cap", msgs, 1,
			)
			if err != nil {
				t.Fatalf("Compact: %v", err)
			}
			var body string
			for _, msg := range out {
				if isCompactionSummary(msg) {
					parts := strings.SplitN(msg.Content, "\n\n", 2)
					if len(parts) != 2 {
						t.Fatalf("summary %q has no body separator", msg.Content)
					}
					body = parts[1]
					break
				}
			}
			if body == "" {
				t.Fatalf("compacted output has no summary body: %v", messageContents(out))
			}
			if got := utf8.RuneCountInString(body); got > tc.max {
				t.Fatalf("summary body %q has %d runes, want <= MaxSummaryChars %d", body, got, tc.max)
			}
			if !strings.HasSuffix(body, "…") {
				t.Fatalf("truncated summary body %q does not end with the ellipsis", body)
			}
		})
	}
}

// TestLLMCompactorDoesNotAliasRetainedTail pins the isolation contract: the
// retained tail (and its ToolCalls) must be a copy, not a window into the
// caller's conversation.
func TestLLMCompactorDoesNotAliasRetainedTail(t *testing.T) {
	msgs := toolBoundaryConversation()
	p := &fakeProvider{fn: func(_ context.Context, _ Request) ([]Chunk, error) {
		return []Chunk{{Text: "SUMMARY", Done: true}}, nil
	}}
	out, err := (LLMCompactor{
		Provider:           p,
		KeepLast:           4,
		ToolResultKeepLast: -1, // exercise the unpruned clone path
	}).Compact(context.Background(), "session-alias", msgs, 1)
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

// TestToolResultPruningDoesNotInflateTinyResults pins the size guard: a tool
// result smaller than the pruning placeholder must be left alone rather than
// replaced by a longer marker.
func TestToolResultPruningDoesNotInflateTinyResults(t *testing.T) {
	longPayload := strings.Repeat("old tool output ", 20)
	msgs := []Message{
		{Role: RoleTool, Content: "", ToolCallID: "c1", ToolName: "read_file"},
		{Role: RoleTool, Content: "x", ToolCallID: "c2", ToolName: "read_file"},
		{Role: RoleTool, Content: longPayload, ToolCallID: "c3", ToolName: "read_file"},
	}
	out := pruneToolResultsExact(msgs, 0)
	if out[0].Content != "" {
		t.Fatalf("empty tool result grew to %q", out[0].Content)
	}
	if out[1].Content != "x" {
		t.Fatalf("one-byte tool result grew to %q", out[1].Content)
	}
	if !strings.Contains(out[2].Content, ToolResultPrunedMarker) || len(out[2].Content) >= len(longPayload) {
		t.Fatalf("large tool result was not pruned: %q", out[2].Content)
	}
	if msgs[0].Content != "" || msgs[1].Content != "x" || msgs[2].Content != longPayload {
		t.Fatal("pruneToolResultsExact mutated its input")
	}
}

// TestCompactorFallbackOnError pins the safety net: a summarizer that fails,
// streams an error, or returns nothing usable must not abort compaction. The
// built-in WindowCompactor takes over and produces the deterministic summary.
func TestCompactorFallbackOnError(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: repeatedContent("u1:", 40)},
		{Role: RoleAssistant, Content: repeatedContent("a1:", 40)},
		{Role: RoleUser, Content: repeatedContent("u2:", 40)},
		{Role: RoleAssistant, Content: repeatedContent("a2:", 40)},
		{Role: RoleUser, Content: repeatedContent("u3:", 40)},
	}
	want, err := (WindowCompactor{KeepLast: 2}).Compact(context.Background(), "session-fb", msgs, 10)
	if err != nil {
		t.Fatalf("WindowCompactor: %v", err)
	}
	wantText := strings.Join(messageContents(want), "|")

	cases := []struct {
		name string
		fn   func(context.Context, Request) ([]Chunk, error)
	}{
		{"provider error", func(context.Context, Request) ([]Chunk, error) {
			return nil, errors.New("summarizer down")
		}},
		{"stream error chunk", func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Err: errors.New("summarizer stopped"), Done: true}}, nil
		}},
		{"empty summary", func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "   ", Done: true}}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{fn: tc.fn}
			got, err := (LLMCompactor{Provider: p, KeepLast: 2}).Compact(
				context.Background(), "session-fb", msgs, 10,
			)
			if err != nil {
				t.Fatalf("Compact returned %v, want the WindowCompactor fallback", err)
			}
			if gotText := strings.Join(messageContents(got), "|"); gotText != wantText {
				t.Fatalf("fallback = %v, want %v", messageContents(got), messageContents(want))
			}
		})
	}

	// An explicitly configured fallback is used instead of the default.
	fallback := &recordingCompactor{fn: func(context.Context, string, []Message, int) ([]Message, error) {
		return []Message{{Role: RoleSystem, Content: "custom fallback"}}, nil
	}}
	p := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return nil, errors.New("summarizer down")
	}}
	got, err := (LLMCompactor{Provider: p, KeepLast: 2, Fallback: fallback}).Compact(
		context.Background(), "session-fb", msgs, 10,
	)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if fallback.callCount() != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.callCount())
	}
	if len(got) != 1 || got[0].Content != "custom fallback" {
		t.Fatalf("compacted = %v, want the configured fallback output", messageContents(got))
	}
}

// TestCompactorSummarizerTimeoutFallsBack pins the short summarizer timeout:
// a provider that ignores the request and never answers must not hold the turn
// for the provider's own 60s timeout. The summarizer deadline trips, the
// deterministic WindowCompactor takes over, and Compact returns promptly.
func TestCompactorSummarizerTimeoutFallsBack(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: repeatedContent("u1:", 40)},
		{Role: RoleAssistant, Content: repeatedContent("a1:", 40)},
		{Role: RoleUser, Content: repeatedContent("u2:", 40)},
		{Role: RoleAssistant, Content: repeatedContent("a2:", 40)},
		{Role: RoleUser, Content: repeatedContent("u3:", 40)},
	}
	want, err := (WindowCompactor{KeepLast: 2}).Compact(context.Background(), "session-timeout", msgs, 10)
	if err != nil {
		t.Fatalf("WindowCompactor: %v", err)
	}

	p := &fakeProvider{fn: func(ctx context.Context, _ Request) ([]Chunk, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	compactor := LLMCompactor{Provider: p, KeepLast: 2, Timeout: 20 * time.Millisecond}

	begin := time.Now()
	got, err := compactor.Compact(context.Background(), "session-timeout", msgs, 10)
	elapsed := time.Since(begin)
	if err != nil {
		t.Fatalf("Compact: %v, want the fallback after the summarizer timeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Compact took %v with a 20ms summarizer timeout", elapsed)
	}
	if gotText, wantText := strings.Join(messageContents(got), "|"), strings.Join(messageContents(want), "|"); gotText != wantText {
		t.Fatalf("fallback = %v, want %v", messageContents(got), messageContents(want))
	}
	if _, ok := p.requestAt(0); !ok {
		t.Fatal("summarizer was never called")
	}
}

// TestToolResultPruning pins the tool-result pruner: old tool messages keep
// their identity but their payload is replaced by a marker, while recent
// results and non-tool messages are untouched.
func TestToolResultPruning(t *testing.T) {
	oldPayload := strings.Repeat("old tool output ", 20)
	recentPayload := strings.Repeat("recent tool output ", 3)
	msgs := []Message{
		{Role: RoleSystem, Content: "system rules"},
		{Role: RoleUser, Content: "inspect the files"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: RoleTool, Content: oldPayload, ToolCallID: "c1", ToolName: "read_file"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c2", Name: "grep"}}},
		{Role: RoleTool, Content: oldPayload, ToolCallID: "c2", ToolName: "grep"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c3", Name: "bash"}}},
		{Role: RoleTool, Content: recentPayload, ToolCallID: "c3", ToolName: "bash"},
		{Role: RoleUser, Content: "thanks"},
	}

	out := PruneToolResults(msgs, 1)
	if len(out) != len(msgs) {
		t.Fatalf("pruned conversation has %d messages, want %d", len(out), len(msgs))
	}

	for _, i := range []int{3, 5} {
		if !strings.Contains(out[i].Content, ToolResultPrunedMarker) {
			t.Fatalf("tool result %d = %q, want the pruning marker %q", i, out[i].Content, ToolResultPrunedMarker)
		}
		if len(out[i].Content) >= len(oldPayload) {
			t.Fatalf("tool result %d is not shorter than the original payload: %d >= %d", i, len(out[i].Content), len(oldPayload))
		}
		if !strings.Contains(out[i].Content, fmt.Sprintf("%d bytes omitted", len(oldPayload))) {
			t.Fatalf("tool result %d = %q, want the omitted byte count", i, out[i].Content)
		}
		if out[i].Role != RoleTool || out[i].ToolCallID != fmt.Sprintf("c%d", (i-1)/2) {
			t.Fatalf("pruned tool message lost its identity: %+v", out[i])
		}
	}
	if out[7].Content != recentPayload {
		t.Fatalf("recent tool result = %q, want it untouched", out[7].Content)
	}
	for _, i := range []int{0, 1, 2, 4, 6, 8} {
		if out[i].Content != msgs[i].Content || out[i].Role != msgs[i].Role {
			t.Fatalf("non-tool message %d changed: %+v, want %+v", i, out[i], msgs[i])
		}
	}
	if msgs[3].Content != oldPayload || msgs[5].Content != oldPayload {
		t.Fatal("PruneToolResults mutated its input")
	}

	// Pruning must be idempotent: a second pass leaves the marker intact.
	again := PruneToolResults(out, 1)
	if again[3].Content != out[3].Content {
		t.Fatalf("second prune changed the marker: %q -> %q", out[3].Content, again[3].Content)
	}
}
