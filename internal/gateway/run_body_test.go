package gateway

import "testing"

// The run stream turns session deltas into SSE frames. Only the **assistant**
// writes the turn's body: the user's message is appended by Send after the
// baseline is taken, so it arrives on the same stream as an `msg.add`, and it was
// being emitted as `message.delta` — the browser then rendered the prompt a
// second time as assistant text (found by running the v0.3.1 Linux build: the
// assistant bubble read "你好", and the composer's "connecting" state could never
// appear because a frame looked present the instant the message was sent).
//
// docs/TRANSPORT.md §4.6: `message.delta` is 正文增量 — the answer, not the
// question.
func TestRunStreamDoesNotEmitTheUsersOwnMessageAsBody(t *testing.T) {
	stream := &runStream{baseline: map[string]bool{}, wake: make(chan struct{}, 1)}

	stream.consume(&sessionDelta{Ops: []sessionOp{{
		Op:      opMsgAdd,
		Message: &messageDTO{ID: "m-user", Role: "user", Text: "你好"},
	}}})
	if len(stream.log) != 0 {
		t.Fatalf("the user's own message became a body frame: %+v", stream.log)
	}

	// A user message that grows (attachments arriving late, a patch) is still not
	// the body.
	stream.consume(&sessionDelta{Ops: []sessionOp{{
		Op:         opMsgDelta,
		MessageID:  "m-user",
		TextAppend: "更多",
	}}})
	if len(stream.log) != 0 {
		t.Fatalf("a delta on the user's message became a body frame: %+v", stream.log)
	}

	// The assistant's first frame must still arrive — the fix must not mute the
	// stream it exists to carry.
	stream.consume(&sessionDelta{Ops: []sessionOp{{
		Op:      opMsgAdd,
		Message: &messageDTO{ID: "m-assistant", Role: "assistant", Text: "在"},
	}}})
	if len(stream.log) != 1 || stream.log[0].name != sseMessageDelta {
		t.Fatalf("the assistant's first frame was dropped: %+v", stream.log)
	}
}

// A baseline message (already on screen when the run started) never re-emits: the
// rule that keeps a run from replaying history applies to the user's message too.
func TestRunStreamIgnoresBaselineUserMessages(t *testing.T) {
	stream := &runStream{
		baseline: map[string]bool{"m-old-user": true, "m-old-assistant": true},
		wake:     make(chan struct{}, 1),
	}
	stream.consume(&sessionDelta{Ops: []sessionOp{
		{Op: opMsgAdd, Message: &messageDTO{ID: "m-old-user", Role: "user", Text: "上一条"}},
		{Op: opMsgAdd, Message: &messageDTO{ID: "m-old-assistant", Role: "assistant", Text: "答"}},
	}})
	if len(stream.log) != 0 {
		t.Fatalf("baseline messages re-emitted: %+v", stream.log)
	}
}
