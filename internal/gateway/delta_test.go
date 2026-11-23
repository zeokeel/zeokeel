package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// applyOps is a client-side applier: it takes the snapshot a client holds and
// the ops a delta carries and produces the client's new copy. Using it in the
// tests proves the ops are sufficient to reconstruct the next snapshot — the
// real contract of the delta protocol (§6.6).
func applyOps(t *testing.T, session sessionDTO, ops []sessionOp) sessionDTO {
	t.Helper()
	out := session
	out.Messages = append([]messageDTO(nil), session.Messages...)
	for _, op := range ops {
		switch op.Op {
		case opSessionPatch:
			for field, value := range op.Fields {
				switch field {
				case "title":
					out.Title, _ = value.(string)
				case "model":
					out.Model, _ = value.(string)
				case "running":
					out.Running, _ = value.(bool)
				case "error":
					out.Error, _ = value.(string)
				case "interrupted":
					out.Interrupted, _ = value.(bool)
				default:
					t.Fatalf("unknown session.patch field %q", field)
				}
			}
		case opMsgAdd:
			if op.Message == nil {
				t.Fatal("msg.add without a message")
			}
			if op.Index != len(out.Messages) {
				t.Fatalf("msg.add index = %d, want %d", op.Index, len(out.Messages))
			}
			out.Messages = append(out.Messages, *op.Message)
		case opMsgReset:
			out.Messages = append([]messageDTO(nil), op.Messages...)
		case opMsgDelta:
			index := messageIndex(t, out.Messages, op.MessageID)
			if op.TextAppend != "" {
				out.Messages[index].Text += op.TextAppend
			}
			if op.ReasoningAppend != nil {
				segment := op.ReasoningAppend
				updated := applyReasoning(out.Messages[index], *segment)
				out.Messages[index] = updated
			}
		case opMsgToolUpsert:
			index := messageIndex(t, out.Messages, op.MessageID)
			if op.Tool == nil {
				t.Fatal("msg.tool.upsert without a tool")
			}
			replaced := false
			for i := range out.Messages[index].Tools {
				if out.Messages[index].Tools[i].ID == op.Tool.ID {
					out.Messages[index].Tools[i] = *op.Tool
					replaced = true
				}
			}
			if !replaced {
				out.Messages[index].Tools = append(out.Messages[index].Tools, *op.Tool)
			}
		case opMsgPatch:
			index := messageIndex(t, out.Messages, op.MessageID)
			for field, value := range op.Fields {
				switch field {
				case "text":
					out.Messages[index].Text, _ = value.(string)
				case "reasoning":
					out.Messages[index].Reasoning, _ = value.(string)
				case "reasoningSegments":
					out.Messages[index].ReasoningSegments, _ = value.([]reasoningSegmentDTO)
				case "streaming":
					out.Messages[index].Streaming, _ = value.(bool)
				case "truncated":
					out.Messages[index].Truncated, _ = value.(bool)
				case "toolTimelineKnown":
					out.Messages[index].ToolTimelineKnown, _ = value.(bool)
				case "tools":
					out.Messages[index].Tools, _ = value.([]toolCallDTO)
				case "usage":
					out.Messages[index].Usage, _ = value.(*messageUsageDTO)
				case "attachments":
					out.Messages[index].Attachments, _ = value.([]attachmentDTO)
				case "goalRound":
					out.Messages[index].GoalRound, _ = value.(*goalRoundDTO)
				default:
					t.Fatalf("unknown msg.patch field %q", field)
				}
			}
		default:
			t.Fatalf("unknown op %q", op.Op)
		}
	}
	return out
}

// messageIndex finds a message by id.
func messageIndex(t *testing.T, messages []messageDTO, id string) int {
	t.Helper()
	for i, message := range messages {
		if message.ID == id {
			return i
		}
	}
	t.Fatalf("message %q is not in the client copy", id)
	return -1
}

// applyReasoning folds one reasoning append into a message.
func applyReasoning(message messageDTO, growth reasoningAppend) messageDTO {
	message.Reasoning += growth.TextAppend
	if len(message.ReasoningSegments) > 0 {
		last := &message.ReasoningSegments[len(message.ReasoningSegments)-1]
		if last.Iteration == growth.Iteration {
			last.Text += growth.TextAppend
			return message
		}
	}
	message.ReasoningSegments = append(message.ReasoningSegments, reasoningSegmentDTO{
		Text:      growth.TextAppend,
		Iteration: growth.Iteration,
	})
	return message
}

// streamingSession is one conversation mid-run.
func streamingSession() sessionDTO {
	return sessionDTO{
		ID:      "s1",
		Title:   "Conversation",
		Running: true,
		Messages: []messageDTO{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "hello"},
			{
				ID:        "m2",
				Role:      contracts.ChatRoleAssistant,
				Text:      "Hi",
				Streaming: true,
				Tools: []toolCallDTO{
					{ID: "call1", Name: "shell", Status: "running", Arguments: `{"cmd":"ls"}`},
				},
			},
		},
	}
}

// TestSnapshotDeltaStitches is the core delta proof: replaying the ops onto the
// previous snapshot yields exactly the next snapshot.
func TestSnapshotDeltaStitches(t *testing.T) {
	previous := streamingSession()
	next := previous
	next.Messages = append([]messageDTO(nil), previous.Messages...)
	next.Messages[1].Text = "Hi there"
	next.Messages[1].Tools = []toolCallDTO{
		{ID: "call1", Name: "shell", Status: "done", Summary: "2 files", Arguments: `{"cmd":"ls"}`},
	}
	next.Messages = append(next.Messages, messageDTO{ID: "m3", Role: contracts.ChatRoleUser, Text: "thanks"})
	next.Running = false

	ops := diffSession(previous, next)
	stitched := applyOps(t, previous, ops)
	if !reflect.DeepEqual(stitched, next) {
		t.Fatalf("stitched session differs from the snapshot\n got: %+v\nwant: %+v", stitched, next)
	}
	// Streaming text must travel as an append, not a full re-send.
	found := false
	for _, op := range ops {
		if op.Op == opMsgDelta && op.MessageID == "m2" && op.TextAppend == " there" {
			found = true
		}
	}
	if !found {
		t.Fatalf("text growth was not an append op: %+v", ops)
	}
}

// TestDiffSessionRewritesHistoryAsReset covers regenerate/compact: the ids stop
// matching, so the delta replaces the history instead of inventing ops.
func TestDiffSessionRewritesHistoryAsReset(t *testing.T) {
	previous := streamingSession()
	next := sessionDTO{
		ID: previous.ID,
		Messages: []messageDTO{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "hello"},
			{ID: "m2b", Role: contracts.ChatRoleAssistant, Text: "A completely new answer"},
		},
	}
	ops := diffSession(previous, next)
	if !hasOp(ops, opMsgReset) {
		t.Fatalf("expected a msg.reset, got %+v", ops)
	}
	stitched := applyOps(t, previous, ops)
	if !reflect.DeepEqual(stitched.Messages, next.Messages) {
		t.Fatalf("reset did not rewrite history:\n got %+v\nwant %+v", stitched.Messages, next.Messages)
	}
}

// TestDiffSessionShrinksHistoryAsReset covers a compaction that keeps the ids
// of the surviving messages.
func TestDiffSessionShrinksHistoryAsReset(t *testing.T) {
	previous := streamingSession()
	next := sessionDTO{ID: previous.ID, Messages: []messageDTO{{ID: "m1", Role: contracts.ChatRoleUser, Text: "hello"}}}
	ops := diffSession(previous, next)
	if !hasOp(ops, opMsgReset) {
		t.Fatalf("expected a msg.reset, got %+v", ops)
	}
	stitched := applyOps(t, previous, ops)
	if len(stitched.Messages) != 1 || stitched.Messages[0].ID != "m1" {
		t.Fatalf("stitched messages = %+v", stitched.Messages)
	}
}

// hasOp reports whether the op list contains one kind.
func hasOp(ops []sessionOp, kind string) bool {
	for _, op := range ops {
		if op.Op == kind {
			return true
		}
	}
	return false
}

// TestDiffSessionReasoningAppends streams thinking segment by segment.
func TestDiffSessionReasoningAppends(t *testing.T) {
	previous := sessionDTO{ID: "s1", Messages: []messageDTO{{
		ID: "m1", Role: contracts.ChatRoleAssistant,
		Reasoning:         "thinking",
		ReasoningSegments: []reasoningSegmentDTO{{Text: "thinking", Iteration: 1}},
	}}}
	next := sessionDTO{ID: "s1", Messages: []messageDTO{{
		ID: "m1", Role: contracts.ChatRoleAssistant,
		Reasoning:         "thinking harder",
		ReasoningSegments: []reasoningSegmentDTO{{Text: "thinking harder", Iteration: 1}},
	}}}
	ops := diffSession(previous, next)
	stitched := applyOps(t, previous, ops)
	if stitched.Messages[0].Reasoning != "thinking harder" {
		t.Fatalf("reasoning = %q, want %q", stitched.Messages[0].Reasoning, "thinking harder")
	}
	if !reflect.DeepEqual(stitched.Messages[0].ReasoningSegments, next.Messages[0].ReasoningSegments) {
		t.Fatalf("segments = %+v, want %+v", stitched.Messages[0].ReasoningSegments, next.Messages[0].ReasoningSegments)
	}
}

// TestMergeSessionDeltaDegradesToResync proves the backpressure rule: a merged
// frame that outgrows the op budget becomes a resync marker instead of unbounded
// memory (§4.5).
func TestMergeSessionDeltaDegradesToResync(t *testing.T) {
	pending := &sessionDelta{SessionID: "s1", BaseRev: 1, Rev: 2}
	for i := 0; i < DeltaRingEntries+2; i++ {
		next := &sessionDelta{SessionID: "s1", BaseRev: uint64(2 + i), Rev: uint64(3 + i),
			Ops: []sessionOp{{Op: opMsgDelta, MessageID: "m1", TextAppend: "x"}}}
		mergeSessionDelta(pending, next)
	}
	if !pending.Resync {
		t.Fatal("merged delta did not degrade to a resync")
	}
	if len(pending.Ops) != 0 {
		t.Fatalf("resync delta still carries %d ops", len(pending.Ops))
	}
	if pending.Rev != uint64(3+DeltaRingEntries+1) {
		t.Fatalf("merged rev = %d, want the newest", pending.Rev)
	}
	// A newer pending frame ignores a stale one.
	if mergeSessionDelta(pending, &sessionDelta{Rev: 1}); pending.Rev == 1 {
		t.Fatal("merge accepted a stale delta")
	}
}

// TestMergeListDeltaUnions covers the list coalescing rule: adds and changes
// union by id, removals win, and an id that is both removed and re-added stays
// added.
func TestMergeListDeltaUnions(t *testing.T) {
	pending := &listDelta{BaseRev: 1, Rev: 2,
		Added:   []sessionInfoDTO{{ID: "a", Title: "A"}},
		Changed: []sessionInfoDTO{{ID: "b", Title: "B"}},
		Removed: []string{"c"},
	}
	next := &listDelta{BaseRev: 2, Rev: 3,
		Changed: []sessionInfoDTO{{ID: "a", Title: "A2"}},
		Removed: []string{"b"},
		Added:   []sessionInfoDTO{{ID: "c", Title: "C"}},
	}
	merged := mergeListDelta(pending, next)
	if merged.BaseRev != 1 || merged.Rev != 3 {
		t.Fatalf("merged revs = %d..%d, want 1..3", merged.BaseRev, merged.Rev)
	}
	if len(merged.Added) != 2 {
		t.Fatalf("merged added = %+v, want a and c", merged.Added)
	}
	for _, item := range merged.Added {
		if item.ID == "a" && item.Title != "A2" {
			t.Fatalf("resurrected add kept the old value: %+v", item)
		}
	}
	if len(merged.Changed) != 0 {
		t.Fatalf("merged changed = %+v, want none (b was removed)", merged.Changed)
	}
	if !containsString(merged.Removed, "b") {
		t.Fatalf("merged removed = %+v, want b", merged.Removed)
	}
}

// TestCanReplayBoundaries pins the resume decision (§6.5).
func TestCanReplayBoundaries(t *testing.T) {
	domain := &domainState{rev: 5, ring: []ringEntry{{baseRev: 3, rev: 4}, {baseRev: 4, rev: 5}}}
	cases := []struct {
		since uint64
		want  bool
	}{
		{5, true},  // already current
		{4, true},  // one entry in the ring
		{3, true},  // the oldest baseRev is still covered
		{2, false}, // older than the ring
		{9, false}, // from another epoch
	}
	for _, c := range cases {
		if got := canReplay(domain, c.since); got != c.want {
			t.Errorf("canReplay(since=%d) = %v, want %v", c.since, got, c.want)
		}
	}
	empty := &domainState{rev: 5}
	if canReplay(empty, 4) {
		t.Error("canReplay with an empty ring = true, want false")
	}
}

// TestSessionSnapshotTailHelper pins the explicit truncation rule.
func TestDTOMappingKeepsUnknownClocksAbsent(t *testing.T) {
	message := newMessageDTO(contracts.ChatMessage{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"})
	if message.CreatedAt != nil {
		t.Fatalf("zero CreatedAt became a published clock: %v", message.CreatedAt)
	}
	if strings.TrimSpace(mustJSON(t, message)) == "" {
		t.Fatal("message did not serialize")
	}
}
