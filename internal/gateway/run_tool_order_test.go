package gateway

import "testing"

// docs/TRANSPORT.md §4.6 promises that `tool.start` precedes the same callId's
// `tool.result`. emitTool used to emit *either* frame — start while running, result
// once settled — so a tool that was already finished when the stream first saw it
// (a call that completed inside one coalescing window, or a message created with its
// tools already settled) produced an orphan `tool.result`. The browser then had no
// name or input for it and rendered the step as the raw call id
// (`call_93bca2c6ab4b478f9fa84247`), which is what a live coding run showed.
func TestRunStreamEmitsStartBeforeResultForAnAlreadySettledTool(t *testing.T) {
	stream := &runStream{baseline: map[string]bool{}, wake: make(chan struct{}, 1)}
	stream.consume(&sessionDelta{Ops: []sessionOp{{
		Op: opMsgAdd,
		Message: &messageDTO{
			ID:   "m-a",
			Role: "assistant",
			Tools: []toolCallDTO{{
				ID: "call-1", Name: "bash", Status: "ok", Summary: "ok", Arguments: `{"command":"ls"}`,
			}},
		},
	}}})

	if len(stream.log) != 2 {
		t.Fatalf("want start+result for a settled tool, got %+v", stream.log)
	}
	if stream.log[0].name != sseToolStart || stream.log[1].name != sseToolResult {
		t.Fatalf("wrong order: %q then %q", stream.log[0].name, stream.log[1].name)
	}
	start, result := stream.log[0], stream.log[1]
	if start.data["callId"] != "call-1" || start.data["name"] != "bash" {
		t.Fatalf("the start frame must name the call: %+v", start.data)
	}
	if result.data["callId"] != "call-1" {
		t.Fatalf("the result frame must carry the call id: %+v", result.data)
	}
	if result.data["ok"] != true {
		t.Fatalf("result framing lost: %+v", result.data)
	}
}

// A tool seen running and then settled must not emit its start twice: the client
// keys steps by callId and a duplicate would look like a second call.
func TestRunStreamEmitsOneStartPerCall(t *testing.T) {
	stream := &runStream{baseline: map[string]bool{}, wake: make(chan struct{}, 1)}
	stream.consume(&sessionDelta{Ops: []sessionOp{{
		Op: opMsgAdd,
		Message: &messageDTO{ID: "m-a", Role: "assistant", Tools: []toolCallDTO{{
			ID: "call-2", Name: "bash", Status: "running", Arguments: `{"command":"sleep 1"}`,
		}}},
	}}})
	stream.consume(&sessionDelta{Ops: []sessionOp{{
		Op: opMsgToolUpsert,
		Tool: &toolCallDTO{
			ID: "call-2", Name: "bash", Status: "ok", Summary: "done", Arguments: `{"command":"sleep 1"}`,
		},
	}}})

	var starts, results int
	for _, event := range stream.log {
		switch event.name {
		case sseToolStart:
			starts++
		case sseToolResult:
			results++
		}
	}
	if starts != 1 || results != 1 {
		t.Fatalf("want exactly one start and one result, got %d/%d: %+v", starts, results, stream.log)
	}
}
