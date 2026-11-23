package gateway

// The prompts domain (S1d): the browser's queued-prompt inbox as a first-class
// read model, replacing the REST endpoints the web track had invented in its
// mock (`/api/sessions/:id/prompts` is not a gateway route).
//
// The queue's truth is the backend (contracts.PromptQueue, implemented by the
// agent module): this domain only reads it, so the gateway never keeps a second
// copy that could drift from the run that drains it.

import (
	"testing"
	"time"
)

// promptsOf decodes a prompt list from a wire snapshot payload.
func promptsOf(t *testing.T, result map[string]any) []any {
	t.Helper()
	prompts, _ := result["prompts"].([]any)
	return prompts
}

// TestPromptsSnapshotEnqueueRemoveReplay is the equivalence test: snapshot →
// enqueue/remove → delta → replaying the deltas equals the next snapshot.
func TestPromptsSnapshotEnqueueRemoveReplay(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	rig := &domainTestRig{runningGateway: startTestGateway(t, chat), chat: chat}
	defer rig.stop(t)
	conn := rig.connect(t)

	// Start empty.
	before := resultOf(t, conn.call("prompts.snapshot", map[string]any{"sessionId": "s1"}))
	if got := promptsOf(t, before); len(got) != 0 {
		t.Fatalf("initial inbox = %v, want empty", got)
	}
	rev := uint64(before["rev"].(float64))

	// Subscribe so the delta lands on this connection.
	subscribed := resultOf(t, conn.call("prompts.subscribe", map[string]any{"sessionId": "s1"}))
	if subscribed["resync"] != false {
		t.Fatalf("prompts.subscribe = %v, want no resync for an empty inbox", subscribed)
	}

	// Enqueue through the gateway command.
	enqueued := resultOf(t, conn.call("prompts.enqueue", map[string]any{"sessionId": "s1", "prompt": "  run the tests  "}))
	id, _ := enqueued["id"].(string)
	if id == "" {
		t.Fatalf("prompts.enqueue returned no id: %v", enqueued)
	}
	if enqueued["queued"] != float64(1) {
		t.Fatalf("queued = %v, want 1", enqueued["queued"])
	}

	delta := conn.receive(func(method string) bool { return method == "prompts.delta" })
	params, _ := delta["params"].(map[string]any)
	if uint64(params["baseRev"].(float64)) != rev {
		t.Fatalf("delta baseRev = %v, want %d", params["baseRev"], rev)
	}
	added, _ := params["added"].([]any)
	if len(added) != 1 {
		t.Fatalf("delta added = %v", params["added"])
	}
	first, _ := added[0].(map[string]any)
	if first["id"] != id || first["text"] != "  run the tests  " {
		t.Fatalf("delta item = %v", first)
	}

	// Snapshot after the enqueue, and replay the delta onto the old snapshot.
	after := resultOf(t, conn.call("prompts.snapshot", map[string]any{"sessionId": "s1"}))
	replayed := append(append([]any{}, promptsOf(t, before)...), added...)
	if len(replayed) != len(promptsOf(t, after)) {
		t.Fatalf("replayed %d prompts, snapshot has %d", len(replayed), len(promptsOf(t, after)))
	}
	if uint64(after["rev"].(float64)) != uint64(params["rev"].(float64)) {
		t.Fatalf("snapshot rev %v != delta rev %v", after["rev"], params["rev"])
	}

	// Remove it: the same round trip in reverse.
	removed := resultOf(t, conn.call("prompts.remove", map[string]any{"sessionId": "s1", "id": id}))
	if removed["queued"] != float64(0) {
		t.Fatalf("queued after remove = %v", removed["queued"])
	}
	removal := conn.receive(func(method string) bool { return method == "prompts.delta" })
	removalParams, _ := removal["params"].(map[string]any)
	removedIDs, _ := removalParams["removed"].([]any)
	if len(removedIDs) != 1 || removedIDs[0] != id {
		t.Fatalf("removal delta = %v", removalParams)
	}
	empty := resultOf(t, conn.call("prompts.snapshot", map[string]any{"sessionId": "s1"}))
	if got := promptsOf(t, empty); len(got) != 0 {
		t.Fatalf("inbox after removal = %v", got)
	}
}

// TestPromptsErrorsKeepTheContract covers the three documented error paths.
func TestPromptsErrorsKeepTheContract(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	rig := &domainTestRig{runningGateway: startTestGateway(t, chat), chat: chat}
	defer rig.stop(t)
	conn := rig.connect(t)

	// Empty prompt: a parameter error, not a queued blank entry.
	empty := errorOf(t, conn.call("prompts.enqueue", map[string]any{"sessionId": "s1", "prompt": "   "}))
	if empty["code"] != float64(codeInvalidParams) {
		t.Fatalf("empty prompt = %v, want invalid params", empty)
	}
	// Unknown session: not found.
	missing := errorOf(t, conn.call("prompts.enqueue", map[string]any{"sessionId": "nope", "prompt": "hi"}))
	if missing["code"] != float64(codeSessionNotFound) {
		t.Fatalf("unknown session = %v, want session_not_found", missing)
	}
	// Removing an unknown id is idempotent (a retry after a dropped socket must
	// not fail).
	response := conn.call("prompts.remove", map[string]any{"sessionId": "s1", "id": "q-does-not-exist"})
	if errObject, failed := response["error"]; failed {
		t.Fatalf("duplicate remove = %v, want success", errObject)
	}
	if queued := chat.QueuedPrompts("s1"); len(queued) != 0 {
		t.Fatalf("failed requests queued %d prompts", len(queued))
	}
}

// TestPromptsFollowBackendChanges proves the honest degradation: the backend has
// no change notification, so the gateway polls subscribed inboxes — a prompt
// queued behind the gateway's back still produces a delta.
func TestPromptsFollowBackendChanges(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gateway := newTestGateway(t, chat)
	// Set the interval before Serve starts the poller (a write after that would
	// be a data race against the goroutine reading it).
	gateway.pollInterval = 20 * time.Millisecond
	served := serveGateway(t, gateway)
	rig := &domainTestRig{runningGateway: served, chat: chat}
	defer rig.stop(t)
	conn := rig.connect(t)

	resultOf(t, conn.call("prompts.subscribe", map[string]any{"sessionId": "s1"}))
	// A change the gateway did not make (the agent draining or another client).
	entry := chat.queuePrompts("s1", "queued by the backend")

	delta := conn.receive(func(method string) bool { return method == "prompts.delta" })
	params, _ := delta["params"].(map[string]any)
	added, _ := params["added"].([]any)
	if len(added) != 1 {
		t.Fatalf("polled delta = %v", params)
	}
	item, _ := added[0].(map[string]any)
	if item["id"] != entry.ID {
		t.Fatalf("polled delta item = %v, want %q", item, entry.ID)
	}
}

// TestDiffPromptsReorder pins the reordering branch: same set, new order.
func TestDiffPromptsReorder(t *testing.T) {
	makePrompts := func(ids ...string) promptsSnapshot {
		snapshot := promptsSnapshot{SessionID: "s1"}
		for _, id := range ids {
			snapshot.Prompts = append(snapshot.Prompts, queuedPromptDTO{ID: id, Text: id})
		}
		return snapshot
	}
	if delta := diffPrompts(makePrompts("a", "b"), makePrompts("a", "b")); delta != nil {
		t.Fatalf("identical inbox produced a delta: %+v", delta)
	}
	delta := diffPrompts(makePrompts("a", "b"), makePrompts("b", "a"))
	if delta == nil || !delta.Reordered || len(delta.Order) != 2 || delta.Order[0] != "b" {
		t.Fatalf("reorder delta = %+v", delta)
	}
	delta = diffPrompts(makePrompts("a", "b"), makePrompts("a", "c"))
	if delta == nil || len(delta.Added) != 1 || len(delta.Removed) != 1 {
		t.Fatalf("add/remove delta = %+v", delta)
	}
}
