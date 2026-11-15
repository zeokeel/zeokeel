package agent

// Durable tool-timeline restore tests: Rig history records which tool calls a
// turn made but not where in the reply each call arrived; the durable
// tool_call/tool_result events carry that metadata, and restore must replay it
// so the transcript order survives a restart. Events from old app versions
// without text_offset keep the message's timeline unknown (legacy fallback).

import (
	"context"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// seedToolTurn appends the durable history and the event log of one tool turn.
func seedToolTurn(t *testing.T, store *fakeStore, sessionID string, events ...rig.SessionEvent) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateSession(ctx, sessionID, "tool turn"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	assistant := rig.NewMessage(rig.RoleAssistant, "REPLY-AFTER-TOOLS")
	assistant.ToolCalls = []rig.ToolCall{
		{ID: "call-1", Name: "bash", Arguments: `{"command":"go build ./..."}`},
		{ID: "call-2", Name: "read_file", Arguments: `{"path":"go.mod"}`},
	}
	if err := store.History().Append(ctx, sessionID,
		rig.NewMessage(rig.RoleUser, "run it"), assistant); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	for _, event := range events {
		event.SessionID = sessionID
		// The caller owns the event time: zero means an old-version event whose
		// time is unknown (the time backfill must leave those rows unknown).
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent %s: %v", event.Type, err)
		}
	}
}

// restoredToolMessage returns the assistant message carrying tool calls.
func restoredToolMessage(t *testing.T, svc *service, sessionID string) contracts.ChatMessage {
	t.Helper()
	state, ok := svc.sessions[sessionID]
	if !ok {
		t.Fatalf("restore did not hydrate session %q", sessionID)
	}
	for _, message := range state.messages {
		if len(message.Tools) > 0 {
			return message
		}
	}
	t.Fatalf("restored messages carry no tool call: %+v", state.messages)
	return contracts.ChatMessage{}
}

// TestRestoreHydratesLiveToolTimeline covers the reported bug after a restart:
// a live turn whose model called tools BEFORE writing any prose recorded
// TextOffset 0 on every call. The durable events carry those offsets, so the
// restored message must report a known timeline (cards-above-text) instead of
// the legacy whole-text-then-cards fallback. Tool results restore their status
// and summary too.
func TestRestoreHydratesLiveToolTimeline(t *testing.T) {
	store := newFakeStore()
	seedToolTurn(t, store, "sess-tools",
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-1","name":"bash","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-2","name":"read_file","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_result", Payload: []byte(`{"id":"call-1","name":"bash","status":"ok","content":"build ok"}`)},
		rig.SessionEvent{Type: "tool_result", Payload: []byte(`{"id":"call-2","name":"read_file","status":"error","content":"open go.mod: no such file"}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-tools")
	if message.Text != "REPLY-AFTER-TOOLS" {
		t.Fatalf("restored text = %q, want the reply", message.Text)
	}
	if !message.ToolTimelineKnown {
		t.Fatal("restored live tool turn lost its known timeline (all-zero offsets are valid order)")
	}
	for _, tool := range message.Tools {
		if tool.TextOffset != 0 {
			t.Fatalf("tool %s TextOffset = %d, want the recorded 0", tool.Name, tool.TextOffset)
		}
	}
	first, second := message.Tools[0], message.Tools[1]
	if first.Status != "ok" || first.Summary != "build ok" {
		t.Fatalf("restored first tool = %+v, want ok/build ok", first)
	}
	if second.Status != "error" || second.Summary != "open go.mod: no such file" {
		t.Fatalf("restored second tool = %+v, want error/summary", second)
	}
}

// TestRestoreKeepsLegacyTimelineForOldEvents covers durable events written
// before the offset was recorded: the message must keep the legacy fallback
// (flag false) instead of misreading the missing offset as 0.
func TestRestoreKeepsLegacyTimelineForOldEvents(t *testing.T) {
	store := newFakeStore()
	seedToolTurn(t, store, "sess-old",
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-1","name":"bash"}`)},
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-2","name":"read_file"}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-old")
	if message.ToolTimelineKnown {
		t.Fatal("old events without text_offset must leave the legacy timeline intact")
	}
	for _, tool := range message.Tools {
		if tool.TextOffset != 0 {
			t.Fatalf("legacy tool %s TextOffset = %d, want 0", tool.Name, tool.TextOffset)
		}
	}
}

// TestRestoreHydratesPartialOffsetsConservatively covers a mixed event log (a
// call whose offset survived, one whose event predates the field): the
// timeline stays unknown because one row would otherwise render at offset 0.
func TestRestoreHydratesPartialOffsetsConservatively(t *testing.T) {
	store := newFakeStore()
	seedToolTurn(t, store, "sess-mixed",
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-1","name":"bash","text_offset":12}`)},
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-2","name":"read_file"}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-mixed")
	if message.ToolTimelineKnown {
		t.Fatal("a partially hydratable timeline must keep the legacy fallback")
	}
	if message.Tools[0].TextOffset != 12 {
		t.Fatalf("recorded offset lost: %+v", message.Tools[0])
	}
}

// TestRestoreHydratesRepeatedCallIDsInOrder covers providers that number calls
// per turn: the same call id appears in two turns, and each turn's event must
// hydrate its own restored message (the durable log and the history are both
// chronological). A first-match map would let the second turn overwrite the
// first one's offset.
func TestRestoreHydratesRepeatedCallIDsInOrder(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-repeat", "repeat"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first := rig.NewMessage(rig.RoleAssistant, "first round")
	first.ToolCalls = []rig.ToolCall{{ID: "call-0", Name: "bash"}}
	second := rig.NewMessage(rig.RoleAssistant, "second round")
	second.ToolCalls = []rig.ToolCall{{ID: "call-0", Name: "bash"}}
	if err := store.History().Append(ctx, "sess-repeat",
		rig.NewMessage(rig.RoleUser, "one"), first,
		rig.NewMessage(rig.RoleUser, "two"), second); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	for _, event := range []rig.SessionEvent{
		{Type: "tool_call", Payload: []byte(`{"id":"call-0","name":"bash","text_offset":11}`)},
		{Type: "tool_result", Payload: []byte(`{"id":"call-0","name":"bash","status":"ok","content":"first ok"}`)},
		{Type: "tool_call", Payload: []byte(`{"id":"call-0","name":"bash","text_offset":12}`)},
		{Type: "tool_result", Payload: []byte(`{"id":"call-0","name":"bash","status":"error","content":"second failed"}`)},
	} {
		event.SessionID = "sess-repeat"
		event.Time = time.Now()
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	state := svc.sessions["sess-repeat"]
	var tools []contracts.ToolCall
	for _, message := range state.messages {
		tools = append(tools, message.Tools...)
	}
	if len(tools) != 2 {
		t.Fatalf("restored tools = %+v, want two", tools)
	}
	if tools[0].TextOffset != 11 || tools[0].Status != "ok" || tools[0].Summary != "first ok" {
		t.Fatalf("first turn tool = %+v, want turn-one event", tools[0])
	}
	if tools[1].TextOffset != 12 || tools[1].Status != "error" || tools[1].Summary != "second failed" {
		t.Fatalf("second turn tool = %+v, want turn-two event", tools[1])
	}
}

// TestRestoreSkipsTimelineWhenEventCountMismatches covers a rewound turn (for
// example after Regenerate, whose dropped run left its tool_call events in the
// durable log while the history keeps only the retried run): the call id counts
// no longer line up, so hydrating by id could attach the old run's offset. The
// message must keep its legacy (unknown) timeline instead.
func TestRestoreSkipsTimelineWhenEventCountMismatches(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-rewound", "rewound"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	retried := rig.NewMessage(rig.RoleAssistant, "retried answer")
	retried.ToolCalls = []rig.ToolCall{{ID: "call-0", Name: "bash"}}
	if err := store.History().Append(ctx, "sess-rewound",
		rig.NewMessage(rig.RoleUser, "one"), retried); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	for _, event := range []rig.SessionEvent{
		// The dropped run's call, then the retried run's call.
		{Type: "tool_call", Payload: []byte(`{"id":"call-0","name":"bash","text_offset":4}`)},
		{Type: "tool_call", Payload: []byte(`{"id":"call-0","name":"bash","text_offset":7}`)},
	} {
		event.SessionID = "sess-rewound"
		event.Time = time.Now()
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-rewound")
	if message.ToolTimelineKnown {
		t.Fatal("ambiguous call-id counts must not certify the timeline")
	}
	if message.Tools[0].TextOffset != 0 {
		t.Fatalf("tool offset = %d, want the unknown default 0", message.Tools[0].TextOffset)
	}
}

// --- tool call timing -------------------------------------------------------

// toolCallStartedAt is the read model's start clock of one call (zero unknown).
func toolCallStartedAt(call contracts.ToolCall) time.Time { return call.StartedAt }

// toolCallEndedAt is the read model's end clock of one call (zero unknown).
func toolCallEndedAt(call contracts.ToolCall) time.Time { return call.EndedAt }

// TestRestoreHydratesToolTimes covers the durable time backfill: the tool_call
// event's time starts the call and the tool_result event's time ends it, for
// each queued id in order.
func TestRestoreHydratesToolTimes(t *testing.T) {
	start1 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	end1 := start1.Add(2 * time.Second)
	start2 := end1.Add(500 * time.Millisecond)
	end2 := start2.Add(1500 * time.Millisecond)
	store := newFakeStore()
	seedToolTurn(t, store, "sess-times",
		rig.SessionEvent{Type: "tool_call", Time: start1, Payload: []byte(`{"id":"call-1","name":"bash","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_result", Time: end1, Payload: []byte(`{"id":"call-1","name":"bash","status":"ok","content":"done"}`)},
		rig.SessionEvent{Type: "tool_call", Time: start2, Payload: []byte(`{"id":"call-2","name":"read_file","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_result", Time: end2, Payload: []byte(`{"id":"call-2","name":"read_file","status":"ok","content":"content"}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-times")
	if got := toolCallStartedAt(message.Tools[0]); !got.Equal(start1) {
		t.Fatalf("first tool StartedAt = %v, want %v", got, start1)
	}
	if got := toolCallEndedAt(message.Tools[0]); !got.Equal(end1) {
		t.Fatalf("first tool EndedAt = %v, want %v", got, end1)
	}
	if got := toolCallStartedAt(message.Tools[1]); !got.Equal(start2) {
		t.Fatalf("second tool StartedAt = %v, want %v", got, start2)
	}
	if got := toolCallEndedAt(message.Tools[1]); !got.Equal(end2) {
		t.Fatalf("second tool EndedAt = %v, want %v", got, end2)
	}
}

// TestRestoreLeavesToolTimesUnknownWithoutEventTimes covers old-version events
// whose time is unknown: the restored calls keep zero clocks instead of a
// guessed value.
func TestRestoreLeavesToolTimesUnknownWithoutEventTimes(t *testing.T) {
	store := newFakeStore()
	seedToolTurn(t, store, "sess-old-times",
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-1","name":"bash","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-2","name":"read_file","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_result", Payload: []byte(`{"id":"call-1","name":"bash","status":"ok","content":"done"}`)},
		rig.SessionEvent{Type: "tool_result", Payload: []byte(`{"id":"call-2","name":"read_file","status":"ok","content":"content"}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-old-times")
	for index, tool := range message.Tools {
		if !toolCallStartedAt(tool).IsZero() || !toolCallEndedAt(tool).IsZero() {
			t.Fatalf("tool %d times = %v..%v, want unknown (zero)", index,
				toolCallStartedAt(tool), toolCallEndedAt(tool))
		}
	}
}

// TestRestoreDoesNotBackfillMismatchedToolTimes covers the id-queue safety rule:
// when the durable log has a different number of events for a call id than the
// restored history, the ids are not eligible and their times stay unknown
// (attaching another run's clock would be a false record).
func TestRestoreDoesNotBackfillMismatchedToolTimes(t *testing.T) {
	first := time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)
	second := first.Add(3 * time.Second)
	store := newFakeStore()
	// The history has one call-1; the log has two, so neither id is eligible.
	seedToolTurn(t, store, "sess-mismatch",
		rig.SessionEvent{Type: "tool_call", Time: first, Payload: []byte(`{"id":"call-1","name":"bash","text_offset":0}`)},
		rig.SessionEvent{Type: "tool_call", Time: second, Payload: []byte(`{"id":"call-1","name":"bash","text_offset":0}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-mismatch")
	for index, tool := range message.Tools {
		if !toolCallStartedAt(tool).IsZero() || !toolCallEndedAt(tool).IsZero() {
			t.Fatalf("tool %d got a mismatched clock: %v..%v", index,
				toolCallStartedAt(tool), toolCallEndedAt(tool))
		}
	}
}

// TestRestoreKeepsRepeatedToolIDTimesInOrder covers providers that reuse ids
// per turn ("call_0"): two restored calls with the same id take their own
// events in sequence order instead of overwriting each other.
func TestRestoreKeepsRepeatedToolIDTimesInOrder(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-repeat", "repeat ids"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	first := rig.NewMessage(rig.RoleAssistant, "FIRST")
	first.ToolCalls = []rig.ToolCall{{ID: "call_0", Name: "bash"}}
	second := rig.NewMessage(rig.RoleAssistant, "SECOND")
	second.ToolCalls = []rig.ToolCall{{ID: "call_0", Name: "read_file"}}
	if err := store.History().Append(ctx, "sess-repeat",
		rig.NewMessage(rig.RoleUser, "one"), first,
		rig.NewMessage(rig.RoleUser, "two"), second); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	start1 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	end1 := start1.Add(time.Second)
	start2 := end1.Add(time.Minute)
	end2 := start2.Add(2 * time.Second)
	for _, event := range []rig.SessionEvent{
		{Type: "tool_call", Time: start1, Payload: []byte(`{"id":"call_0","name":"bash","text_offset":0}`)},
		{Type: "tool_result", Time: end1, Payload: []byte(`{"id":"call_0","name":"bash","status":"ok","content":"one"}`)},
		{Type: "tool_call", Time: start2, Payload: []byte(`{"id":"call_0","name":"read_file","text_offset":0}`)},
		{Type: "tool_result", Time: end2, Payload: []byte(`{"id":"call_0","name":"read_file","status":"ok","content":"two"}`)},
	} {
		event.SessionID = "sess-repeat"
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent %s: %v", event.Type, err)
		}
	}
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	state := svc.sessions["sess-repeat"]
	if state == nil {
		t.Fatal("restore lost the repeated-id session")
	}
	var timed []contracts.ToolCall
	for _, message := range state.messages {
		timed = append(timed, message.Tools...)
	}
	if len(timed) != 2 {
		t.Fatalf("restored tool calls = %d, want 2", len(timed))
	}
	if !timed[0].StartedAt.Equal(start1) || !timed[0].EndedAt.Equal(end1) {
		t.Fatalf("first call times = %v..%v, want %v..%v",
			timed[0].StartedAt, timed[0].EndedAt, start1, end1)
	}
	if !timed[1].StartedAt.Equal(start2) || !timed[1].EndedAt.Equal(end2) {
		t.Fatalf("second call times = %v..%v, want %v..%v",
			timed[1].StartedAt, timed[1].EndedAt, start2, end2)
	}
}

// TestLiveToolCallRecordsStartAndEndTimes covers the live read model: the call
// is stamped when it is requested and when its result arrives, so the
// trajectory can show a real duration without a restart.
func TestLiveToolCallRecordsStartAndEndTimes(t *testing.T) {
	store := newFakeStore()
	provider := &fixToolCallProvider{toolName: "fail_tool"}
	svc := fixService(t, Config{}, rigtools.NewRegistry(&fixFailingTool{}), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	var tool contracts.ToolCall
	for _, message := range session.Messages {
		if len(message.Tools) > 0 {
			tool = message.Tools[0]
		}
	}
	if tool.StartedAt.IsZero() || tool.EndedAt.IsZero() {
		t.Fatalf("live tool times = %v..%v, want both stamped", tool.StartedAt, tool.EndedAt)
	}
	if tool.EndedAt.Before(tool.StartedAt) {
		t.Fatalf("live tool ended %v before it started %v", tool.EndedAt, tool.StartedAt)
	}
}
