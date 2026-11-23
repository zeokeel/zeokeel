package gateway

// Migrated from internal/regression/plan_mode_test.go (backend-only, 394 lines)
// and queue_send_test.go (UI-driven, 445 lines) before S4 deletes
// internal/regression — both now run on the gateway's real protocol, because
// that is the surface the browser actually uses: `plan.snapshot`/`plan.delta`/
// `plan.exit` for the plan overlay and `prompts:<sessionId>` for the queue.
//
// Kept:
//   - plan mode: the whitelisted planning tool runs, the plan content reaches the
//     client, the user's approve flows back, and the turn finishes;
//   - plan mode's fail-closed gate: a mutating tool is denied by the gate and
//     never executes (no approval card is involved);
//   - the prompt queue: a prompt sent while the run is in flight is queued, the
//     backend sends it once the run settles, and the inbox ends empty.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// planContent is the plan text the scripted model submits.
const planContent = "Plan: read the workspace, summarise it, propose two edits."

// TestE2EPlanReviewOverTheProtocol is the migrated plan-mode behaviour: submit a
// plan, see it on the state plane, approve it, and let the turn finish.
func TestE2EPlanReviewOverTheProtocol(t *testing.T) {
	rig := newE2ERig(t, func(_ []byte, call int) (int, string) {
		if call == 0 {
			return 0, sseToolCall("plan_review", `{"content":`+jsonString(planContent)+`}`)
		}
		return 0, sseAnswer("plan approved, proceeding")
	}, e2eOptions{activateHostTools: true})
	conn := rig.connect()
	sessionID := rig.newSession("plan-mode")

	// Plan mode is a settings row, exactly as the settings screen writes it.
	resultOf(t, conn.call("settings.set", map[string]any{"key": contracts.SettingAgentMode, "value": "plan"}))

	// Prime the plan domain like a client does on connect: the first build emits
	// no delta, so the delta below is the transition into reviewing.
	if snapshot := resultOf(t, conn.call("plan.snapshot", nil)); snapshot["state"] != "idle" {
		t.Fatalf("initial plan state = %v, want idle", snapshot["state"])
	}

	results := make(chan []sseFrame, 1)
	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "make a plan", ClientRunID: "run-plan"})
		defer func() { _ = response.Body.Close() }()
		results <- readFrames(t, response.Body, true)
	}()

	// The plan content is visible on its own domain (the overlay's input).
	delta := conn.receive(func(method string) bool { return method == "plan.delta" })
	params, _ := delta["params"].(map[string]any)
	if params["state"] != planReviewingState {
		t.Fatalf("plan.delta state = %v, want %s", params["state"], planReviewingState)
	}
	if !strings.Contains(params["content"].(string), "propose two edits") {
		t.Fatalf("plan.delta content = %v", params["content"])
	}
	snapshot := resultOf(t, conn.call("plan.snapshot", nil))
	if snapshot["state"] != planReviewingState || !strings.Contains(snapshot["content"].(string), "Plan:") {
		t.Fatalf("plan.snapshot = %v", snapshot)
	}

	// The user approves: the state machine moves and the client sees it.
	exited := resultOf(t, conn.call("plan.exit", map[string]any{"action": "approve"}))
	if exited["state"] != "approved" {
		t.Fatalf("plan.exit = %v, want state approved", exited)
	}

	var frames []sseFrame
	select {
	case frames = <-results:
	case <-time.After(20 * time.Second):
		t.Fatal("the run never finished after the plan was approved")
	}
	var toolResult map[string]any
	for _, event := range framesWith(frames, sseToolResult) {
		if event.data["callId"] == "call-e2e" {
			toolResult = event.data
		}
	}
	if toolResult == nil || toolResult["ok"] != true {
		t.Fatalf("plan_review tool result = %v (frames %v)", toolResult, eventNames(frames))
	}
	text := streamedText(frames)
	if !strings.Contains(text, "plan approved") {
		t.Fatalf("streamed text = %q", text)
	}
	after := resultOf(t, conn.call("plan.snapshot", nil))
	if after["state"] != "approved" {
		t.Fatalf("plan.snapshot after approval = %v", after)
	}
}

// TestE2EPlanModeDeniesMutatingTool is the fail-closed half: in plan mode a
// mutating tool is refused by the gate, never executed, and no approval card is
// raised (the gate denies before the approvals layer is consulted).
func TestE2EPlanModeDeniesMutatingTool(t *testing.T) {
	rig := newE2ERig(t, func(_ []byte, call int) (int, string) {
		if call == 0 {
			return 0, sseToolCall("write_file", `{"path":"plan-should-not-write.txt","content":"x"}`)
		}
		return 0, sseAnswer("understood, staying read-only")
	}, e2eOptions{activateHostTools: true})
	conn := rig.connect()
	sessionID := rig.newSession("plan-deny")

	resultOf(t, conn.call("settings.set", map[string]any{"key": contracts.SettingAgentMode, "value": " plan "}))

	results := make(chan []sseFrame, 1)
	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "write a file", ClientRunID: "run-plan-deny"})
		defer func() { _ = response.Body.Close() }()
		results <- readFrames(t, response.Body, true)
	}()

	var frames []sseFrame
	select {
	case frames = <-results:
	case <-time.After(20 * time.Second):
		t.Fatal("the denied run never finished")
	}
	var toolResult map[string]any
	for _, event := range framesWith(frames, sseToolResult) {
		if event.data["callId"] == "call-e2e" {
			toolResult = event.data
		}
	}
	if toolResult == nil || toolResult["ok"] != false {
		t.Fatalf("a mutating tool ran in plan mode: %v (frames %v)", toolResult, eventNames(frames))
	}
	if kind, _ := toolResult["error"].(string); !strings.Contains(strings.ToLower(kind), "den") && kind == "" {
		t.Fatalf("the denial carried no explanation: %v", toolResult)
	}
	// The model was told about the denial and answered.
	if text := streamedText(frames); !strings.Contains(text, "staying read-only") {
		t.Fatalf("streamed text = %q", text)
	}
	// No approval card was raised: the gate refused without asking.
	if approvals, ok := rig.root.Get(contracts.KeyApprovals); ok && approvals != nil {
		if pending := approvals.Pending(); len(pending) != 0 {
			t.Fatalf("plan mode raised an approval card: %+v", pending)
		}
	}
}

// TestE2EQueueDrainsAfterTheRun is the migrated queue behaviour: a prompt sent
// while the turn is in flight waits in the backend inbox, then the backend sends
// it when the run settles.
func TestE2EQueueDrainsAfterTheRun(t *testing.T) {
	release := make(chan struct{})
	rig := newE2ERig(t, func(body []byte, call int) (int, string) {
		if call == 0 {
			// Hold the first turn so the second prompt is genuinely queued.
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			return 0, sseAnswer("first answer")
		}
		return 0, sseAnswer("queued answer: " + requestContent(body, "user"))
	}, e2eOptions{})
	conn := rig.connect()
	sessionID := rig.newSession("queue-drain")

	results := make(chan []sseFrame, 1)
	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "first prompt", ClientRunID: "run-queue-1"})
		defer func() { _ = response.Body.Close() }()
		results <- readFrames(t, response.Body, true)
	}()
	waitFor(t, "the first run to start", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && session.Running
	})

	// A real client watches the inbox; subscribing also primes the domain so the
	// drain below is a delta rather than a first-build snapshot.
	resultOf(t, conn.call("prompts.subscribe", map[string]any{"sessionId": sessionID}))

	// Queue a second prompt through the protocol while the run is in flight.
	enqueued := resultOf(t, conn.call("prompts.enqueue", map[string]any{"sessionId": sessionID, "prompt": "second prompt"}))
	if enqueued["queued"] != float64(1) {
		t.Fatalf("prompts.enqueue = %v, want queued 1", enqueued)
	}
	if current := resultOf(t, conn.call("prompts.snapshot", map[string]any{"sessionId": sessionID})); len(promptsOf(t, current)) != 1 {
		t.Fatalf("inbox after enqueue = %v", current["prompts"])
	}

	// Let the first run finish: the backend sends the queued prompt on its own.
	close(release)
	select {
	case frames := <-results:
		if last := eventNames(frames); last[len(last)-1] != sseDone {
			t.Fatalf("the first run did not finish with done: %v", last)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the first run never finished")
	}

	waitFor(t, "the queued prompt to be sent to the model", func() bool {
		return rig.model.callCount() >= 2 && strings.Contains(string(rig.model.lastBody()), "second prompt")
	})
	// The inbox empties once the prompt is started, and both turns are stored.
	waitFor(t, "the inbox to drain", func() bool {
		snapshot := resultOf(t, conn.call("prompts.snapshot", map[string]any{"sessionId": sessionID}))
		return len(promptsOf(t, snapshot)) == 0
	})
	waitFor(t, "both turns to settle in the session", func() bool {
		session, ok := rig.chat.Session(sessionID)
		if !ok || session.Running {
			return false
		}
		users, assistants := 0, 0
		for _, message := range session.Messages {
			switch message.Role {
			case contracts.ChatRoleUser:
				users++
			case contracts.ChatRoleAssistant:
				assistants++
			}
		}
		return users == 2 && assistants >= 2
	})
}

// TestE2EQueueRemoveWhileRunning keeps the negative: removing a queued prompt
// while the run is in flight means it is never sent.
func TestE2EQueueRemoveWhileRunning(t *testing.T) {
	release := make(chan struct{})
	rig := newE2ERig(t, func(body []byte, call int) (int, string) {
		if call == 0 {
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			return 0, sseAnswer("first answer")
		}
		return 0, sseAnswer("second answer: " + requestContent(body, "user"))
	}, e2eOptions{})
	conn := rig.connect()
	sessionID := rig.newSession("queue-remove")

	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "keep me", ClientRunID: "run-queue-2"})
		_ = response.Body.Close()
	}()
	waitFor(t, "the run to start", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && session.Running
	})
	enqueued := resultOf(t, conn.call("prompts.enqueue", map[string]any{"sessionId": sessionID, "prompt": "delete me"}))
	id, _ := enqueued["id"].(string)
	if id == "" {
		t.Fatalf("prompts.enqueue returned no id: %v", enqueued)
	}
	if errObject, failed := conn.call("prompts.remove", map[string]any{"sessionId": sessionID, "id": id})["error"]; failed {
		t.Fatalf("prompts.remove: %v", errObject)
	}
	close(release)

	waitFor(t, "the run to settle", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && !session.Running
	})
	// Give the drain a beat: the removed prompt must never be sent.
	time.Sleep(300 * time.Millisecond)
	if body := string(rig.model.lastBody()); strings.Contains(body, "delete me") {
		t.Fatalf("a removed prompt was sent to the model: %s", body)
	}
	if calls := rig.model.callCount(); calls != 1 {
		t.Fatalf("the model was called %d times, want 1 (the removed prompt must not start a run)", calls)
	}
}

// planReviewingState mirrors the plan domain's reviewing state.
const planReviewingState = "reviewing"

// jsonString renders a Go string as a JSON string literal.
func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// streamedText concatenates the message deltas of a run.
func streamedText(frames []sseFrame) string {
	var text strings.Builder
	for _, frame := range framesWith(frames, sseMessageDelta) {
		value, _ := frame.data["text"].(string)
		text.WriteString(value)
	}
	return text.String()
}
