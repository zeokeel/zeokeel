package gateway

// Tests for the three contract gaps a user-facing audit found (task-36):
//
//	① notify.action must really run the retained callback, exactly once, and
//	  reject an unknown or already-consumed id with a parameter error;
//	② session.snapshot's beforeMessageId must page backwards without gaps or
//	  duplicates (tail alone already worked);
//	③ context.snapshot / tasks.snapshot must not advertise a sinceRev they do
//	  not honour.
//
// Each one is written before the implementation, so the red output is the record
// of the gap rather than a description of it.

import (
	"fmt"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// connect dials the gateway's control plane and completes the handshake.
func connectGateway(t *testing.T, gw *runningGateway) *testWSConn {
	t.Helper()
	conn := dialTestWS(t, gw.address, map[string]string{
		"Origin":        gw.info.Origin(),
		"Authorization": "Bearer " + gw.info.Token,
	})
	t.Cleanup(conn.close)
	resultOf(t, conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(t, conn.call("ready", nil))
	return conn
}

// TestNotifyActionRunsTheRetainedCallback is ①'s end-to-end proof: a toast is
// posted with a real action, the client posts back the actionId, and the state
// the action was supposed to roll back is actually rolled back. The reply alone
// proves nothing — "returned {}" is what an empty stub does too.
func TestNotifyActionRunsTheRetainedCallback(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	// The "undone" state the action restores.
	conn := connectGateway(t, gw)
	// The toast is broadcast to ready clients, so the connection must exist
	// before the notification is posted.
	undone := make(chan struct{}, 1)
	notifier, ok := gw.gateway.root.Get(contracts.KeyNotifier)
	if !ok || notifier == nil {
		t.Fatal("the gateway provides no notifier")
	}
	actionNotifier, ok := notifier.(contracts.ActionNotifier)
	if !ok {
		t.Fatalf("the gateway notifier is %T, want contracts.ActionNotifier", notifier)
	}
	notification := actionNotifier.NotifyAction(contracts.NotifyInfo, "已归档", "撤销", func() {
		select {
		case undone <- struct{}{}:
		default:
		}
	})
	if notification.ActionLabel != "撤销" {
		t.Fatalf("notification = %+v, want an action label", notification)
	}

	// The wire announcement must carry the id and the actionId the client echoes.
	posted := conn.receive(func(method string) bool { return method == "toast.posted" })
	params, _ := posted["params"].(map[string]any)
	if params["id"] != notification.ID {
		t.Fatalf("toast.posted id = %v, want %q", params["id"], notification.ID)
	}
	actionID, _ := params["actionId"].(string)
	if actionID == "" || params["actionLabel"] != "撤销" {
		t.Fatalf("toast.posted = %v, want actionLabel + actionId", params)
	}

	resultOf(t, conn.call("notify.action", map[string]any{"id": notification.ID, "actionId": actionID}))
	select {
	case <-undone:
	case <-time.After(2 * time.Second):
		t.Fatal("notify.action returned without running the callback")
	}

	// The action is consumed: a second click must not run it again, and it must
	// say why instead of answering {}.
	second := errorOf(t, conn.call("notify.action", map[string]any{"id": notification.ID, "actionId": actionID}))
	if data, _ := second["data"].(map[string]any); data["field"] != "id" {
		t.Fatalf("a consumed action answered %v, want a parameter error naming the id", second)
	}
	select {
	case <-undone:
		t.Fatal("the callback ran twice for one action")
	case <-time.After(50 * time.Millisecond):
	}

	// An id nobody ever posted is the same class of mistake.
	unknown := errorOf(t, conn.call("notify.action", map[string]any{"id": "toast-404", "actionId": actionID}))
	if data, _ := unknown["data"].(map[string]any); data["field"] != "id" {
		t.Fatalf("an unknown id answered %v, want a parameter error naming the id", unknown)
	}
}

// TestSessionSnapshotPagesBackwards is ②'s proof: tail alone returns the newest
// window plus the cursor of the oldest message it returned; beforeMessageId
// continues from that cursor, and concatenating the pages reproduces the full
// transcript with no gap and no duplicate.
func TestSessionSnapshotPagesBackwards(t *testing.T) {
	chat := newScriptedChat()
	chat.addSession("s1", "First", "hello")
	const total = 300
	chat.mu.Lock()
	for index := 0; index < total; index++ {
		role := contracts.ChatRoleUser
		if index%2 == 1 {
			role = contracts.ChatRoleAssistant
		}
		chat.sessions["s1"].Messages = append(chat.sessions["s1"].Messages, contracts.ChatMessage{
			ID: fmt.Sprintf("m-%03d", index), Role: role, Text: fmt.Sprintf("message %d", index),
		})
	}
	var want []string
	for _, message := range chat.sessions["s1"].Messages {
		want = append(want, message.ID)
	}
	chat.mu.Unlock()

	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	conn := connectGateway(t, gw)

	// Page 1: the newest 50 + the cursor.
	first := resultOf(t, conn.call("session.snapshot", map[string]any{"sessionId": "s1", "tail": 50}))
	if first["truncated"] != true {
		t.Fatalf("tail:50 reported truncated=%v, want true", first["truncated"])
	}
	page, _ := first["session"].(map[string]any)
	messages, _ := page["messages"].([]any)
	if len(messages) != 50 {
		t.Fatalf("page 1 = %d messages, want 50", len(messages))
	}
	cursor, _ := first["oldestMessageId"].(string)
	if cursor == "" {
		t.Fatal("no oldestMessageId cursor to page from")
	}
	var got []string
	for _, message := range messages {
		row, _ := message.(map[string]any)
		got = append(got, fmt.Sprint(row["id"]))
	}
	if got[len(got)-1] != want[len(want)-1] {
		t.Fatalf("page 1 ends with %q, want the newest %q", got[len(got)-1], want[len(want)-1])
	}

	// Keep paging backwards until the cursor runs out.
	for cursor != "" {
		reply := resultOf(t, conn.call("session.snapshot", map[string]any{
			"sessionId": "s1", "tail": 50, "beforeMessageId": cursor,
		}))
		page, _ := reply["session"].(map[string]any)
		messages, _ := page["messages"].([]any)
		if len(messages) == 0 {
			break
		}
		var older []string
		for _, message := range messages {
			row, _ := message.(map[string]any)
			older = append(older, fmt.Sprint(row["id"]))
		}
		got = append(older, got...)
		cursor, _ = reply["oldestMessageId"].(string)
		if len(older) < 50 {
			break
		}
	}
	if len(got) != len(want) {
		t.Fatalf("paged %d messages, want %d (no gap, no duplicate)", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("paged sequence differs at %d: got %q want %q", index, got[index], want[index])
		}
	}

	// Without parameters the behaviour must not change. It is NOT the whole
	// transcript: §6.4 specifies a default tail window (SnapshotTailDefault) so a
	// long conversation does not arrive as one multi-megabyte frame. This test
	// pins that default rather than "everything", because "everything" is what a
	// naive pagination refactor would quietly turn it into.
	whole := resultOf(t, conn.call("session.snapshot", map[string]any{"sessionId": "s1"}))
	wholePage, _ := whole["session"].(map[string]any)
	wholeMessages, _ := wholePage["messages"].([]any)
	if len(wholeMessages) != SnapshotTailDefault {
		t.Fatalf("unparameterised snapshot = %d messages, want the default window %d", len(wholeMessages), SnapshotTailDefault)
	}
	if whole["truncated"] != true || whole["oldestMessageId"] == "" {
		t.Fatalf("unparameterised snapshot = %v, want truncated + a cursor", whole)
	}

	// An explicit tail large enough to cover the transcript returns all of it.
	all := resultOf(t, conn.call("session.snapshot", map[string]any{"sessionId": "s1", "tail": 500}))
	allPage, _ := all["session"].(map[string]any)
	allMessages, _ := allPage["messages"].([]any)
	if len(allMessages) != len(want) {
		t.Fatalf("tail:500 = %d messages, want the whole %d", len(allMessages), len(want))
	}
}

// TestSnapshotsDoNotAdvertiseSinceRev is ③: context.snapshot and tasks.snapshot
// are full snapshots, so their parameter contract must not list sinceRev —
// declaring a parameter that has no effect is worse than not having it, because
// the stand-ins and the client both start believing it works.
func TestSnapshotsDoNotAdvertiseSinceRev(t *testing.T) {
	document, err := buildContract()
	if err != nil {
		t.Fatalf("build contract: %v", err)
	}
	for _, method := range []string{"context.snapshot", "tasks.snapshot"} {
		spec, ok := document.Params[method]
		if !ok {
			t.Fatalf("%s is absent from the contract", method)
		}
		for _, field := range append(append([]string{}, spec.Required...), spec.Optional...) {
			if field == "sinceRev" {
				t.Errorf("%s declares sinceRev but ignores it; subscribe is the incremental method", method)
			}
		}
	}
	// The subscribe halves keep it: that is where increments are negotiated.
	for _, method := range []string{"context.subscribe", "tasks.subscribe"} {
		spec := document.Params[method]
		found := false
		for _, field := range spec.Optional {
			if field == "sinceRev" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s lost its sinceRev field", method)
		}
	}
}
