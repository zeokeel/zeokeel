package agent

// US-01/US-02 (2026-11 audit): a live run folds every model iteration into ONE
// assistant ChatMessage. The flat Reasoning string used to concatenate all
// iterations without boundaries, so the thinking↔tool interleave was lost
// live and "revived" only after a restart (rig history keeps one assistant
// message per iteration). ReasoningSegments keeps the per-iteration boundaries
// with the same TextOffset timeline mechanism ToolCall already uses, and the
// restore path fills one segment per persisted assistant message so live and
// restored turns carry the same structure.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// twoIterationProvider scripts a real two-iteration turn:
//
//	iteration 1: reasoning THINK-ONE-A/B → text "before " → echo tool call
//	iteration 2: reasoning THINK-TWO-A/B → text "FINAL-ANSWER" → stop
type twoIterationProvider struct{ calls int }

func (p *twoIterationProvider) Name() string { return "two-iteration" }

func (p *twoIterationProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.calls++
	if p.calls == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{Reasoning: "THINK-ONE-A"},
			{Reasoning: "THINK-ONE-B"},
			{Delta: "before "},
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "echo", Arguments: `{"text":"hi"}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Reasoning: "THINK-TWO-A"},
		{Reasoning: "THINK-TWO-B"},
		{Delta: "FINAL-ANSWER"},
		{FinishReason: "stop"},
	}}, nil
}

// newSegmentTestService builds a service over an explicit durable store so a
// second service can restore the same conversation (the live/restored pair).
func newSegmentTestService(t *testing.T, store *fakeStore, provider rig.Provider) *service {
	t.Helper()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "scripted"})
	svc := newService(Config{}, rigtools.NewRegistry(&echoTool{}),
		contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc
}

func TestLiveReasoningSegmentsTwoIterations(t *testing.T) {
	store := newFakeStore()
	svc := newSegmentTestService(t, store, &twoIterationProvider{})
	id, err := svc.NewSession("segments")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "use the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	if len(session.Messages) != 2 {
		t.Fatalf("live messages = %d, want one user + one folded assistant", len(session.Messages))
	}
	assistant := session.Messages[1]
	// The flat string stays the compatibility concatenation.
	if want := "THINK-ONE-ATHINK-ONE-BTHINK-TWO-ATHINK-TWO-B"; assistant.Reasoning != want {
		t.Fatalf("Reasoning = %q, want %q", assistant.Reasoning, want)
	}
	if len(assistant.ReasoningSegments) != 2 {
		t.Fatalf("ReasoningSegments = %+v, want 2 per-iteration segments (US-01: live folding lost the boundaries)", assistant.ReasoningSegments)
	}
	first, second := assistant.ReasoningSegments[0], assistant.ReasoningSegments[1]
	if first.Text != "THINK-ONE-ATHINK-ONE-B" || first.Iteration != 1 || first.TextOffset != 0 {
		t.Fatalf("segment[0] = %+v, want iteration 1 thinking at offset 0", first)
	}
	// Iteration 2 began after the "before " text delta: its offset is the
	// cumulative assistant text length at that moment (ToolCall.TextOffset
	// semantics).
	if second.Text != "THINK-TWO-ATHINK-TWO-B" || second.Iteration != 2 || second.TextOffset != len("before ") {
		t.Fatalf("segment[1] = %+v, want iteration 2 thinking at offset %d", second, len("before "))
	}
	if assistant.Text != "before FINAL-ANSWER" {
		t.Fatalf("Text = %q, want the cumulative turn text", assistant.Text)
	}
	if len(assistant.Tools) != 1 || assistant.Tools[0].TextOffset != len("before ") {
		t.Fatalf("Tools = %+v, want the echo call at offset %d", assistant.Tools, len("before "))
	}
}

func TestRestoredReasoningSegmentsIsomorphic(t *testing.T) {
	store := newFakeStore()
	svc := newSegmentTestService(t, store, &twoIterationProvider{})
	id, err := svc.NewSession("segments")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "use the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)

	// The conversion restore uses: every persisted assistant message is one
	// iteration and carries exactly one segment (US-02: live and restored
	// structure must agree).
	messages, err := store.History().Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	converted := chatMessagesFromRig(messages)
	var assistants []contracts.ChatMessage
	for _, message := range converted {
		if message.Role == contracts.ChatRoleAssistant {
			assistants = append(assistants, message)
		}
	}
	if len(assistants) != 2 {
		t.Fatalf("restored assistants = %d, want one per iteration", len(assistants))
	}
	for index, assistant := range assistants {
		iteration := index + 1
		if len(assistant.ReasoningSegments) != 1 {
			t.Fatalf("restored assistant %d segments = %+v, want exactly one", iteration, assistant.ReasoningSegments)
		}
		segment := assistant.ReasoningSegments[0]
		if segment.Iteration != iteration || segment.TextOffset != 0 || segment.Text != assistant.Reasoning {
			t.Fatalf("restored assistant %d segment = %+v (Reasoning %q), want iteration %d single segment at offset 0",
				iteration, segment, assistant.Reasoning, iteration)
		}
	}
	if assistants[0].Reasoning != "THINK-ONE-ATHINK-ONE-B" || assistants[1].Reasoning != "THINK-TWO-ATHINK-TWO-B" {
		t.Fatalf("restored reasoning = %q / %q, want the per-iteration split",
			assistants[0].Reasoning, assistants[1].Reasoning)
	}

	// The full restore path (a second service over the same durable store)
	// produces the same segmented structure.
	second := newSegmentTestService(t, store, &twoIterationProvider{})
	second.restore(context.Background())
	restored := mustSession(t, second, id)
	var restoredAssistants []contracts.ChatMessage
	for _, message := range restored.Messages {
		if message.Role == contracts.ChatRoleAssistant {
			restoredAssistants = append(restoredAssistants, message)
		}
	}
	if len(restoredAssistants) != 2 {
		t.Fatalf("restore() assistants = %d, want 2", len(restoredAssistants))
	}
	for index, assistant := range restoredAssistants {
		if len(assistant.ReasoningSegments) != 1 ||
			assistant.ReasoningSegments[0].Iteration != index+1 ||
			assistant.ReasoningSegments[0].Text != assistant.Reasoning {
			t.Fatalf("restore() assistant %d = %+v, want one segment for iteration %d", index+1, assistant, index+1)
		}
	}
}

// TestHandleEventReasoningSegmentBoundaries drives handleEvent directly with
// scripted rig events: deltas of one iteration append to that iteration's
// segment even when text arrives mid-iteration, and a new iteration starts a
// new segment at the cumulative text offset recorded at turn start.
func TestHandleEventReasoningSegmentBoundaries(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	id, err := svc.NewSession("boundaries")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID: "m-a", Role: contracts.ChatRoleAssistant, Streaming: true,
	})
	state.assistant = len(state.messages) - 1
	svc.mu.Unlock()

	ctx := context.Background()
	events := []rig.Event{
		{Type: rig.EventTurnStart, Iteration: 1},
		{Type: rig.EventReasoningDelta, Iteration: 1, Reasoning: "a"},
		{Type: rig.EventTextDelta, Iteration: 1, Text: "t"},
		{Type: rig.EventReasoningDelta, Iteration: 1, Reasoning: "b"},
		{Type: rig.EventTurnEnd, Iteration: 1},
		{Type: rig.EventTurnStart, Iteration: 2},
		{Type: rig.EventReasoningDelta, Iteration: 2, Reasoning: "c"},
		{Type: rig.EventTextDelta, Iteration: 2, Text: "u"},
		{Type: rig.EventTurnEnd, Iteration: 2},
	}
	for _, event := range events {
		svc.handleEvent(ctx, id, "", event, nil)
	}
	session := mustSession(t, svc, id)
	assistant := session.Messages[0]
	if assistant.Reasoning != "abc" {
		t.Fatalf("Reasoning = %q, want abc", assistant.Reasoning)
	}
	if len(assistant.ReasoningSegments) != 2 {
		t.Fatalf("segments = %+v, want 2", assistant.ReasoningSegments)
	}
	if got := assistant.ReasoningSegments[0]; got.Text != "ab" || got.Iteration != 1 || got.TextOffset != 0 {
		t.Fatalf("segment[0] = %+v, want ab/iteration 1/offset 0 (mid-iteration text must not split the segment)", got)
	}
	if got := assistant.ReasoningSegments[1]; got.Text != "c" || got.Iteration != 2 || got.TextOffset != len("t") {
		t.Fatalf("segment[1] = %+v, want c/iteration 2/offset %d", got, len("t"))
	}
}

// TestEventMessageFillsMissingReasoningSegment: the complete per-iteration
// assistant message (rig EventMessage) is authoritative for its iteration's
// reasoning. When no reasoning delta arrived (a provider that only reports the
// finished message), the segment is still created — at the turn-start text
// offset, not the offset after the iteration's text.
func TestEventMessageFillsMissingReasoningSegment(t *testing.T) {
	svc := newQueueService(t, nil, nil, nil)
	id, err := svc.NewSession("message-event")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID: "m-a", Role: contracts.ChatRoleAssistant, Streaming: true,
	})
	state.assistant = len(state.messages) - 1
	svc.mu.Unlock()

	ctx := context.Background()
	svc.handleEvent(ctx, id, "", rig.Event{Type: rig.EventTurnStart, Iteration: 1}, nil)
	svc.handleEvent(ctx, id, "", rig.Event{Type: rig.EventTextDelta, Iteration: 1, Text: "txt"}, nil)
	svc.handleEvent(ctx, id, "", rig.Event{
		Type: rig.EventMessage, Iteration: 1,
		Message: &rig.Message{Role: rig.RoleAssistant, Reasoning: "late"},
	}, nil)
	svc.handleEvent(ctx, id, "", rig.Event{Type: rig.EventTurnEnd, Iteration: 1}, nil)

	session := mustSession(t, svc, id)
	assistant := session.Messages[0]
	if len(assistant.ReasoningSegments) != 1 {
		t.Fatalf("segments = %+v, want one filled from EventMessage", assistant.ReasoningSegments)
	}
	if got := assistant.ReasoningSegments[0]; got.Text != "late" || got.Iteration != 1 || got.TextOffset != 0 {
		t.Fatalf("segment = %+v, want late/iteration 1/offset 0 (turn-start offset)", got)
	}
	if assistant.Reasoning != "late" {
		t.Fatalf("Reasoning = %q, want late (kept in sync with the segments)", assistant.Reasoning)
	}
}

// TestForkCopiesReasoningSegments: a fork owns its message copies, segments
// included (the fork must not share mutable segment slices with its source).
func TestForkCopiesReasoningSegments(t *testing.T) {
	store := newFakeStore()
	svc := newSegmentTestService(t, store, &twoIterationProvider{})
	id, err := svc.NewSession("segments")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "use the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	forkID, err := svc.ForkSession(id)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	fork := mustSession(t, svc, forkID)
	source := mustSession(t, svc, id)
	if len(fork.Messages) != len(source.Messages) {
		t.Fatalf("fork messages = %d, want %d", len(fork.Messages), len(source.Messages))
	}
	for index := range source.Messages {
		want, got := source.Messages[index].ReasoningSegments, fork.Messages[index].ReasoningSegments
		if len(got) != len(want) {
			t.Fatalf("fork message %d segments = %+v, want %+v", index, got, want)
		}
		for seg := range want {
			if got[seg] != want[seg] {
				t.Fatalf("fork message %d segment %d = %+v, want %+v", index, seg, got[seg], want[seg])
			}
		}
	}
}
