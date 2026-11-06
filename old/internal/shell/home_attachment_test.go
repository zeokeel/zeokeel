package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// writeHomeAttachment writes one source file for the Home picker tests.
func writeHomeAttachment(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write attachment %s: %v", name, err)
	}
	return path
}

// pickHomeAttachment drives the Home "+" menu through the real Files entry
// with the injected picker.
func pickHomeAttachment(t *testing.T, tt *ui.Tester) {
	t.Helper()
	tt.Frame()
	if err := clickAddControl(t, tt); err != nil {
		t.Fatalf("open add menu: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	tt.Frame()
}

// TestAttachmentHomeFollowsSend proves files picked on Home belong to the Home
// draft: staging creates no session and never touches the last-opened session,
// and Home Send copies them onto the session it creates before the first turn,
// so the provider receives the text and the user message carries the chip.
func TestAttachmentHomeFollowsSend(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	oldID := h.ctrl.CurrentSessionID()
	const body = "home attachment body"
	src := writeHomeAttachment(t, "home-send.txt", body)
	h.app.PickFiles = func() ([]string, error) { return []string{src}, nil }
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)

	pickHomeAttachment(t, tt)
	if !tt.HasText("home-send.txt") {
		t.Fatalf("Home composer does not render the staged chip: %v", tt.Texts())
	}
	// Staging is owned by the Home draft, not the previous session.
	if rows, err := h.store.ListAttachments(oldID); err != nil || len(rows) != 0 {
		t.Fatalf("old session attachments after staging = %d, %v; want 0", len(rows), err)
	}
	sessions, err := h.ctrl.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("picking a Home file created a session: %d sessions", len(sessions))
	}

	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus Home composer: %v", err)
	}
	tt.Type("analyze this")
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("click Home Send: %v", err)
	}
	h.ctrl.Wait()
	h.app.SetSnapshot(h.ctrl.Snapshot())

	newID := h.ctrl.CurrentSessionID()
	if newID == "" || newID == oldID {
		t.Fatalf("Home Send current session = %q, want a new session (old %q)", newID, oldID)
	}
	if h.app.Screen != Session {
		t.Fatalf("Home Send left screen = %v, want Session", h.app.Screen)
	}
	if len(h.app.homeAttachments) != 0 {
		t.Fatalf("Home attachments after Send = %+v, want cleared", h.app.homeAttachments)
	}

	if rows, err := h.store.ListAttachments(newID); err != nil || len(rows) != 1 {
		t.Fatalf("new session attachments = %d, %v; want 1", len(rows), err)
	} else if rows[0].Name != "home-send.txt" || rows[0].MessageID == "" {
		t.Fatalf("new session attachment = %+v, want home-send.txt linked to the message", rows[0])
	}
	if rows, err := h.store.ListAttachments(oldID); err != nil || len(rows) != 0 {
		t.Fatalf("old session attachments after Send = %d, %v; want 0", len(rows), err)
	}

	// The provider sees the text file as part of the turn's prompt.
	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	var user *runtime.Message
	for i := range req.Messages {
		if req.Messages[i].Role == runtime.RoleUser {
			user = &req.Messages[i]
		}
	}
	if user == nil {
		t.Fatalf("provider request has no user message: %+v", req.Messages)
	}
	if !strings.Contains(user.Content, "analyze this") || !strings.Contains(user.Content, body) {
		t.Fatalf("provider user content missing prompt or attachment body: %q", user.Content)
	}

	// The sent chip moved onto the new session's user message.
	var msg *state.Message
	for i := range h.ctrl.Snapshot().Messages {
		if m := &h.ctrl.Snapshot().Messages[i]; m.Role == state.RoleUser && m.Text == "analyze this" {
			msg = m
		}
	}
	if msg == nil {
		t.Fatalf("new session has no user message: %+v", h.ctrl.Snapshot().Messages)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "home-send.txt" {
		t.Fatalf("user message attachments = %+v, want home-send.txt", msg.Attachments)
	}
	tt.Frame()
	if !tt.HasText("home-send.txt") {
		t.Fatalf("session screen does not render the sent chip: %v", tt.Texts())
	}
}

// TestAttachmentHomeChipRemove removes a staged Home file through the chip's
// remove control; nothing was copied into any session.
func TestAttachmentHomeChipRemove(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	oldID := h.ctrl.CurrentSessionID()
	src := writeHomeAttachment(t, "home-visible.txt", "remove me")
	h.app.PickFiles = func() ([]string, error) { return []string{src}, nil }
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)

	pickHomeAttachment(t, tt)
	if !tt.HasText("home-visible.txt") {
		t.Fatalf("Home composer does not render the staged chip: %v", tt.Texts())
	}
	if err := tt.Click("Remove attachment: home-visible.txt"); err != nil {
		t.Fatalf("click Home remove chip: %v", err)
	}
	tt.Frame()
	if tt.HasText("home-visible.txt") {
		t.Fatalf("Home chip still rendered after remove: %v", tt.Texts())
	}
	if len(h.app.homeAttachments) != 0 {
		t.Fatalf("Home attachments after remove = %+v, want none", h.app.homeAttachments)
	}
	if rows, err := h.store.ListAttachments(oldID); err != nil || len(rows) != 0 {
		t.Fatalf("old session attachments after remove = %d, %v; want 0", len(rows), err)
	}
}

// TestAttachmentHomeWithoutCurrentSession: staging on Home no longer requires
// an open session, because the session is created by Send.
func TestAttachmentHomeWithoutCurrentSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	if err := h.ctrl.DeleteSession(ctx, h.ctrl.CurrentSessionID()); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if id := h.ctrl.CurrentSessionID(); id != "" {
		t.Fatalf("current session after delete = %q, want none", id)
	}
	h.app.SetSnapshot(nil)
	src := writeHomeAttachment(t, "no-session.txt", "staged")
	h.app.PickFiles = func() ([]string, error) { return []string{src}, nil }
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)

	pickHomeAttachment(t, tt)
	if !tt.HasText("no-session.txt") {
		t.Fatalf("Home composer does not render the chip without a session: %v", tt.Texts())
	}
	if len(h.app.homeAttachments) != 1 {
		t.Fatalf("Home attachments = %+v, want one", h.app.homeAttachments)
	}
}
