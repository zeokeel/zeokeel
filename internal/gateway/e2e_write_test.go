package gateway

// The two write commands the browser cannot work without (S1e): creating a
// conversation and writing a settings row. Both publish their effect before the
// command answers — the gateway is the writer, so nothing here waits for a poll.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// startWriteGateway serves a gateway whose root has a chat backend and an
// in-memory settings store (the two services the write commands touch). The poll
// interval is set before Serve starts the poller, so no test races it.
func startWriteGateway(t *testing.T, chat contracts.ChatService, pollInterval time.Duration) (*runningGateway, *fakeSettings) {
	t.Helper()
	settings := newFakeSettings(nil)
	root := cordisRootFor(t, chat, nil, settings, nil, nil, nil)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	if pollInterval > 0 {
		gateway.pollInterval = pollInterval
	}
	return serveGateway(t, gateway), settings
}

// TestRefreshNowIsNotReentrant pins the one way refreshNow could deadlock: being
// called from a hub command function (it dispatches a command and waits). The
// bounded dispatch turns that mistake into a named error instead of a hang.
func TestRefreshNowIsNotReentrant(t *testing.T) {
	chat := newFakeChat()
	settings := newFakeSettings(nil)
	root := cordisRootFor(t, chat, nil, settings, nil, nil, nil)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	// A bare hub over the same services: the guard is about dispatch, not HTTP.
	h := newHub("test-epoch", chat)
	h.sources = &domainSources{gateway: gateway}
	if err := h.start(context.Background()); err != nil {
		t.Fatalf("hub.start: %v", err)
	}
	defer h.stop()
	// Set before the command below: the channel handoff orders it before the
	// hub goroutine reads it.
	h.refreshTimeout = 50 * time.Millisecond

	var nestedErr error
	// This command function runs ON the hub goroutine; a nested refresh must
	// fail fast instead of waiting for the loop that is executing it.
	if _, err := hubCall(context.Background(), h, func(*hub) (bool, error) {
		nestedErr = h.refreshNow(context.Background(), domainSettings)
		return true, nil
	}); err != nil {
		t.Fatalf("outer command: %v", err)
	}
	if nestedErr == nil {
		t.Fatal("a refresh from inside a hub command must not succeed silently")
	}
	if !strings.Contains(nestedErr.Error(), "must not be called from a hub command") {
		t.Fatalf("nested refresh error = %v, want the reentrancy diagnostic", nestedErr)
	}
	// The hub is still healthy afterwards: a real refresh from this goroutine
	// works and emits.
	if err := h.refreshNow(context.Background(), domainSettings); err != nil {
		t.Fatalf("the hub did not recover: %v", err)
	}
	if _, err := h.snapshotDomain(context.Background(), domainSettings); err != nil {
		t.Fatalf("snapshot after recovery: %v", err)
	}
}

// TestSessionCreatePublishesTheListDelta is the end-to-end unblock: create a
// session, see it in the sidebar delta, then fetch its snapshot.
func TestSessionCreatePublishesTheListDelta(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	rig := &domainTestRig{runningGateway: startTestGateway(t, chat), chat: chat}
	defer rig.stop(t)
	conn := rig.connect(t)

	before := resultOf(t, conn.call("session.list.snapshot", nil))
	rev := uint64(before["rev"].(float64))

	created := resultOf(t, conn.call("session.create", map[string]any{"title": "  Second  "}))
	sessionID, _ := created["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session.create returned no id: %v", created)
	}
	if created["epoch"] != rig.info.Epoch {
		t.Fatalf("session.create epoch = %v", created["epoch"])
	}
	if uint64(created["rev"].(float64)) <= rev {
		t.Fatalf("session.create rev = %v, want it past %d", created["rev"], rev)
	}

	// The sidebar delta reached this connection (the list domain is global).
	delta := conn.receive(func(method string) bool { return method == "session.list.delta" })
	params, _ := delta["params"].(map[string]any)
	added, _ := params["added"].([]any)
	if len(added) != 1 {
		t.Fatalf("session.list.delta added = %v", params["added"])
	}
	item, _ := added[0].(map[string]any)
	if item["id"] != sessionID {
		t.Fatalf("added session = %v, want %q", item, sessionID)
	}
	// The backend trims the title; the transport does not rewrite it further.
	if item["title"] != "Second" {
		t.Fatalf("title = %v, want Second (trimmed by the backend)", item["title"])
	}

	// And the new conversation is immediately readable.
	snapshot := resultOf(t, conn.call("session.snapshot", map[string]any{"sessionId": sessionID}))
	session, _ := snapshot["session"].(map[string]any)
	if session["id"] != sessionID || session["title"] != "Second" {
		t.Fatalf("session.snapshot = %v", session)
	}
	if messages, _ := session["messages"].([]any); len(messages) != 0 {
		t.Fatalf("a new session has %d messages", len(messages))
	}
}

// TestSessionCreateRejectsBadTitles covers the title guard rails.
func TestSessionCreateRejectsBadTitles(t *testing.T) {
	chat := newFakeChat()
	rig := &domainTestRig{runningGateway: startTestGateway(t, chat), chat: chat}
	defer rig.stop(t)
	conn := rig.connect(t)

	tooLong := strings.Repeat("x", maxSessionTitleRunes+1)
	for _, title := range []string{tooLong, "bad\x00title", "line\nbreak"} {
		protocolErr := errorOf(t, conn.call("session.create", map[string]any{"title": title}))
		if protocolErr["code"] != float64(codeInvalidParams) {
			t.Fatalf("title %q = %v, want invalid params", title, protocolErr)
		}
	}
	// An empty title is legitimate: the backend trims it and the sidebar shows
	// its own placeholder.
	created := resultOf(t, conn.call("session.create", map[string]any{"title": "   "}))
	if id, _ := created["sessionId"].(string); id == "" {
		t.Fatalf("empty title was rejected: %v", created)
	}
}

// TestSettingsSetPublishesImmediately proves the write path does not wait for
// the settings poll (which runs every 2s in production).
func TestSettingsSetPublishesImmediately(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	// A slow poll interval so a passing test cannot be the poller's doing.
	served, settings := startWriteGateway(t, chat, time.Hour)
	rig := &domainTestRig{runningGateway: served, chat: chat, settings: settings}
	defer rig.stop(t)
	conn := rig.connect(t)

	before := resultOf(t, conn.call("settings.snapshot", nil))
	rev := uint64(before["rev"].(float64))

	written := resultOf(t, conn.call("settings.set", map[string]any{"key": "ui.theme.mode", "value": "dark"}))
	if uint64(written["rev"].(float64)) <= rev {
		t.Fatalf("settings.set rev = %v, want it past %d", written["rev"], rev)
	}

	start := time.Now()
	delta := conn.receive(func(method string) bool { return method == "settings.delta" })
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("settings.delta took %s (the write path must not wait for the poll)", elapsed)
	}
	params, _ := delta["params"].(map[string]any)
	rows, _ := params["rows"].(map[string]any)
	if rows["ui.theme.mode"] != "dark" {
		t.Fatalf("settings.delta rows = %v", rows)
	}
	if uint64(params["baseRev"].(float64)) != rev {
		t.Fatalf("settings.delta baseRev = %v, want %d", params["baseRev"], rev)
	}

	// Reading it back agrees, and the stored row really changed.
	after := resultOf(t, conn.call("settings.snapshot", nil))
	afterRows, _ := after["rows"].(map[string]any)
	if afterRows["ui.theme.mode"] != "dark" {
		t.Fatalf("settings.snapshot rows = %v", afterRows)
	}
	if rig.settings.Get("ui.theme.mode", "") != "dark" {
		t.Fatalf("the settings store was not written")
	}
}

// TestSettingsSetPermissionRefreshesApprovals pins the derived-domain rule: a
// permission row changes the approval mode, so the approvals snapshot must move
// too — immediately, not on the next poll.
func TestSettingsSetPermissionRefreshesApprovals(t *testing.T) {
	rig := startDomainGateway(t)
	defer rig.stop(t)
	conn := rig.connect(t)

	before := resultOf(t, conn.call("approvals.snapshot", nil))
	if before["permission"] != contracts.PermissionWorkspace {
		t.Fatalf("initial permission = %v", before["permission"])
	}
	rev := uint64(before["rev"].(float64))

	resultOf(t, conn.call("settings.set", map[string]any{
		"key": contracts.SettingAgentPermission, "value": contracts.PermissionFull,
	}))
	delta := conn.receive(func(method string) bool { return method == "approvals.delta" })
	params, _ := delta["params"].(map[string]any)
	if params["resync"] != true {
		t.Fatalf("a permission change must resync the approvals domain: %v", params)
	}
	after := resultOf(t, conn.call("approvals.snapshot", nil))
	if after["permission"] != contracts.PermissionFull || after["mode"] != contracts.PermissionFull {
		t.Fatalf("approvals after the permission write = %v/%v", after["mode"], after["permission"])
	}
	if uint64(after["rev"].(float64)) <= rev {
		t.Fatal("the approvals rev did not move")
	}
}

// TestSettingsSetRejectsBadKeys covers the key guard rails.
func TestSettingsSetRejectsBadKeys(t *testing.T) {
	chat := newFakeChat()
	served, settings := startWriteGateway(t, chat, 0)
	rig := &domainTestRig{runningGateway: served, chat: chat, settings: settings}
	defer rig.stop(t)
	conn := rig.connect(t)

	for _, key := range []string{"", "  ", ".leading.dot", "has space", "bad\x00key", strings.Repeat("k", 129)} {
		protocolErr := errorOf(t, conn.call("settings.set", map[string]any{"key": key, "value": "x"}))
		if protocolErr["code"] != float64(codeInvalidParams) {
			t.Fatalf("key %q = %v, want invalid params", key, protocolErr)
		}
	}
	// A well-formed key the process does not itself read is accepted: the store
	// is generic and cannot know which rows a loaded module uses.
	if errObject, failed := conn.call("settings.set", map[string]any{"key": "module.custom.row", "value": "1"})["error"]; failed {
		t.Fatalf("a well-formed unknown key was rejected: %v", errObject)
	}
	// Oversized values are refused rather than truncating silently.
	big := strings.Repeat("x", maxSettingValueBytes+1)
	protocolErr := errorOf(t, conn.call("settings.set", map[string]any{"key": "chat.systemPrompt", "value": big}))
	if protocolErr["code"] != float64(codeInvalidParams) {
		t.Fatalf("oversized value = %v, want invalid params", protocolErr)
	}
}
