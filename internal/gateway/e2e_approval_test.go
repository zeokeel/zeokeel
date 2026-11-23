package gateway

// Migrated from internal/regression/approval_geometry_e2e_test.go (UI-driven,
// 198 lines) + permission_test.go + approvals_test.go (backend-only), before S4
// deletes internal/regression.
//
// Behaviour kept, at the transport boundary instead of through the native UI:
// the approval chain the browser's card drives — the agent asks, the pending
// request reaches the client as approvals.delta, the client resolves it by
// protocol, and the tool result flows back to the model. Both verdicts are
// covered, and the deny case proves the tool never ran.

import (
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// approvalFromDelta extracts the first added approval of an approvals.delta.
func approvalFromDelta(t *testing.T, frame map[string]any) (id, tool, sessionID string) {
	t.Helper()
	params, _ := frame["params"].(map[string]any)
	added, _ := params["added"].([]any)
	if len(added) == 0 {
		t.Fatalf("approvals.delta carried no request: %v", params)
	}
	item, _ := added[0].(map[string]any)
	id, _ = item["id"].(string)
	tool, _ = item["tool"].(string)
	sessionID, _ = item["sessionId"].(string)
	return id, tool, sessionID
}

// TestE2EApprovalAllowRunsTheTool is the full chain: request → delta → allow →
// tool runs → result returns to the model → turn completes.
func TestE2EApprovalAllowRunsTheTool(t *testing.T) {
	probe := &approvalProbe{}
	rig := newE2ERig(t, func(_ []byte, call int) (int, string) {
		if call == 0 {
			return 0, sseToolCall("e2e_probe", `{"note":"hello"}`)
		}
		return 0, sseAnswer("the probe ran, all good")
	}, e2eOptions{tools: []rig.Tool{probe}})
	conn := rig.connect()
	sessionID := rig.newSession("approval-allow")

	// The run streams until the approval settles, so read it in the background.
	type outcome struct {
		frames []sseFrame
	}
	results := make(chan outcome, 1)
	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "run the probe", ClientRunID: "run-approve"})
		defer func() { _ = response.Body.Close() }()
		results <- outcome{frames: readFrames(t, response.Body, true)}
	}()

	// The pending approval arrives on the state plane.
	frame := conn.receive(func(method string) bool { return method == "approvals.delta" })
	id, tool, origin := approvalFromDelta(t, frame)
	if tool != "e2e_probe" {
		t.Fatalf("approval tool = %q, want e2e_probe", tool)
	}
	if origin != sessionID {
		t.Fatalf("approval sessionId = %q, want %q", origin, sessionID)
	}

	// The browser allows it by protocol.
	resolved := resultOf(t, conn.call("approval.resolve", map[string]any{"id": id, "decision": "allow"}))
	if resolved["resolved"] != true {
		t.Fatalf("approval.resolve = %v", resolved)
	}

	var frames []sseFrame
	select {
	case result := <-results:
		frames = result.frames
	case <-time.After(15 * time.Second):
		t.Fatal("the run never finished after the approval")
	}
	names := eventNames(frames)
	if len(frames) == 0 || frames[len(frames)-1].event != sseDone {
		t.Fatalf("stream did not end with done: %v", names)
	}
	var toolResult map[string]any
	for _, event := range framesWith(frames, sseToolResult) {
		if event.data["callId"] == "call-e2e" {
			toolResult = event.data
		}
	}
	if toolResult == nil || toolResult["ok"] != true {
		t.Fatalf("tool.result = %v (frames %v)", toolResult, names)
	}
	if probe.callCount() != 1 {
		t.Fatalf("the probe ran %d times, want 1", probe.callCount())
	}
	// The result reached the model: the second provider request carries it.
	if body := rig.model.lastBody(); !strings.Contains(string(body), "probe ran") {
		t.Fatalf("the tool result never reached the model: %s", body)
	}
	if rig.model.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (tool round + answer)", rig.model.callCount())
	}
}

// TestE2EApprovalDenyKeepsTheToolUnrun is the negative half: deny is a verdict,
// not a neutral dismiss — the tool must not execute, and the turn still ends.
func TestE2EApprovalDenyKeepsTheToolUnrun(t *testing.T) {
	probe := &approvalProbe{}
	rig := newE2ERig(t, func(_ []byte, call int) (int, string) {
		if call == 0 {
			return 0, sseToolCall("e2e_probe", `{"note":"nope"}`)
		}
		return 0, sseAnswer("understood, not running it")
	}, e2eOptions{tools: []rig.Tool{probe}})
	conn := rig.connect()
	sessionID := rig.newSession("approval-deny")

	results := make(chan []sseFrame, 1)
	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "run the probe", ClientRunID: "run-deny"})
		defer func() { _ = response.Body.Close() }()
		results <- readFrames(t, response.Body, true)
	}()

	frame := conn.receive(func(method string) bool { return method == "approvals.delta" })
	id, _, _ := approvalFromDelta(t, frame)
	resultOf(t, conn.call("approval.resolve", map[string]any{"id": id, "decision": "deny"}))

	var frames []sseFrame
	select {
	case frames = <-results:
	case <-time.After(15 * time.Second):
		t.Fatal("the run never finished after the denial")
	}
	var toolResult map[string]any
	for _, event := range framesWith(frames, sseToolResult) {
		if event.data["callId"] == "call-e2e" {
			toolResult = event.data
		}
	}
	if toolResult == nil || toolResult["ok"] != false {
		t.Fatalf("tool.result = %v, want ok=false (frames %v)", toolResult, eventNames(frames))
	}
	if probe.callCount() != 0 {
		t.Fatalf("a denied call still ran the tool %d times", probe.callCount())
	}
	if last := eventNames(frames); last[len(last)-1] != sseDone {
		t.Fatalf("denial ended the stream without done: %v", last)
	}
}

// TestE2EApprovalRejectsUnknownVerdict keeps the contract's decision vocabulary:
// a browser may not send a neutral or invented verb.
func TestE2EApprovalRejectsUnknownVerdict(t *testing.T) {
	probe := &approvalProbe{}
	rig := newE2ERig(t, func(_ []byte, call int) (int, string) {
		if call == 0 {
			return 0, sseToolCall("e2e_probe", `{}`)
		}
		return 0, sseAnswer("done")
	}, e2eOptions{tools: []rig.Tool{probe}})
	conn := rig.connect()
	sessionID := rig.newSession("approval-verdict")

	go func() {
		response := openRun(t, rig.served, runRequest{SessionID: sessionID, Text: "probe", ClientRunID: "run-verdict"})
		_ = response.Body.Close()
	}()
	frame := conn.receive(func(method string) bool { return method == "approvals.delta" })
	id, _, _ := approvalFromDelta(t, frame)

	// "yes" is not in the contract's vocabulary ("allow"/"allow_always"/"deny").
	badVerdict := errorOf(t, conn.call("approval.resolve", map[string]any{"id": id, "decision": "yes"}))
	if badVerdict["code"] != float64(codeInvalidParams) {
		t.Fatalf("error = %v, want invalid params", badVerdict)
	}
	// The first answer wins and a repeat of it is idempotent (contract: resolve
	// is idempotent after the first answer).
	resultOf(t, conn.call("approval.resolve", map[string]any{"id": id, "decision": "deny"}))
	repeated := resultOf(t, conn.call("approval.resolve", map[string]any{"id": id, "decision": "allow"}))
	if repeated["resolved"] != true {
		t.Fatalf("repeat resolve = %v, want idempotent success", repeated)
	}
	// An id that was never pending is a stale revision: the client re-snapshots.
	unknown := errorOf(t, conn.call("approval.resolve", map[string]any{"id": "appr-never", "decision": "allow"}))
	if unknown["code"] != float64(codeRevisionGone) {
		t.Fatalf("unknown id resolve = %v, want revision_gone", unknown)
	}
	// The denied call must not have run.
	waitFor(t, "the run to settle", func() bool {
		session, ok := rig.chat.Session(sessionID)
		return ok && !session.Running
	})
	if probe.callCount() != 0 {
		t.Fatalf("the probe ran %d times", probe.callCount())
	}
	_ = contracts.ApprovalDeny
}
