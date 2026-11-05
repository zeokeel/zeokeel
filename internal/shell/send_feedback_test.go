package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestKeyboardSendFailureIsSurfacedAndRollsBack pins UX-01/UX-03: pressing
// Enter in the Home composer with no agent must show the deferred toast (mygo
// drops Context toasts raised outside a build pass) and must not leave the
// session it created behind.
func TestKeyboardSendFailureIsSurfacedAndRollsBack(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	h.ctrl.SetAgent(nil, nil)

	before, err := h.ctrl.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list sessions before: %v", err)
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("please send")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if !tt.HasText("No agent configured") {
		t.Fatalf("keyboard ErrNoAgent was not surfaced: %q", tt.Texts())
	}
	if h.app.Screen != Home {
		t.Fatalf("failed Home send left the app on %v, want Home", h.app.Screen)
	}
	after, err := h.ctrl.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list sessions after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("failed Home send left %d session(s) behind, want %d", len(after)-len(before), 0)
	}
	if got := h.app.draft; got != "please send" {
		t.Fatalf("failed Home send dropped the draft: %q", got)
	}
}

// TestKeyboardSendFailureRestoresHomeAttachments covers the same rollback for
// the staged files: the session receives them before the first Submit, so a
// failed submit must re-stage them on the Home draft instead of deleting them
// with the rolled-back session.
func TestKeyboardSendFailureRestoresHomeAttachments(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	h.ctrl.SetAgent(nil, nil)
	path := filepath.Join(h.dir, "note.txt")
	if err := os.WriteFile(path, []byte("attachment body"), 0o600); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	h.app.homeAttachments = []homeAttachment{{ID: "home-1", Path: path, Name: "note.txt"}}
	h.app.draft = "please send"

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if !tt.HasText("No agent configured") {
		t.Fatalf("keyboard ErrNoAgent was not surfaced: %q", tt.Texts())
	}
	if len(h.app.homeAttachments) != 1 || h.app.homeAttachments[0].Path != path {
		t.Fatalf("failed send did not restore the staged files: %+v", h.app.homeAttachments)
	}
	if h.app.Screen != Home {
		t.Fatalf("failed Home send left the app on %v, want Home", h.app.Screen)
	}
}

// TestSessionKeyboardSendFailureIsSurfaced covers the Session composer: an
// Enter submit that fails must reach the deferred toast queue instead of the
// dropped one-frame c.Toast call.
func TestSessionKeyboardSendFailureIsSurfaced(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	h.ctrl.SetAgent(nil, nil)
	h.app.draft = "please send"

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if !tt.HasText("No agent configured") {
		t.Fatalf("session keyboard ErrNoAgent was not surfaced: %q", tt.Texts())
	}
	if got := h.app.draft; got != "please send" {
		t.Fatalf("failed session send dropped the draft: %q", got)
	}
	if id := h.ctrl.CurrentSessionID(); id == "" {
		t.Fatal("failed session send closed the open session")
	}
}

// TestDeferredToastQueuesOutsideBuildPass documents the mechanism the send
// failures rely on: with no build-pass context the message is queued, and the
// next View flushes it into a toast.
func TestDeferredToastQueuesOutsideBuildPass(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session

	h.app.toastDeferred(nil, "queued failure")
	if got := h.app.slashMenu().deferredToast; got != "queued failure" {
		t.Fatalf("deferred toast = %q, want queued failure", got)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("queued failure") {
		t.Fatalf("deferred toast was not flushed into the frame: %q", tt.Texts())
	}
	if got := h.app.slashMenu().deferredToast; got != "" {
		t.Fatalf("deferred toast was not cleared after flushing: %q", got)
	}
}

// TestEmptySessionChatShowsEmptyState pins UX-02: an empty session's Chat tab
// invites the first message instead of showing a blank pane, and offers the
// Settings > Models CTA only when no agent is configured.
func TestEmptySessionChatShowsEmptyState(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Start the conversation") {
		t.Fatalf("empty session transcript has no title: %q", tt.Texts())
	}
	if !tt.HasText("Send a message below") {
		t.Fatalf("empty session transcript has no hint: %q", tt.Texts())
	}
	if tt.HasText("No model is configured yet") {
		t.Fatalf("model CTA shown although an agent exists: %q", tt.Texts())
	}

	h.ctrl.SetAgent(nil, nil)
	tt.Frame()
	if !tt.HasText("No model is configured yet") {
		t.Fatalf("empty session without an agent has no model CTA: %q", tt.Texts())
	}
	if err := tt.Click("Add a model in Settings"); err != nil {
		t.Fatalf("click model CTA: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("model CTA opened %v, want Settings", h.app.Screen)
	}
	if got := h.app.shellUI().settingsTab; got != settingsTabModels {
		t.Fatalf("model CTA selected settings tab %v, want Models", got)
	}
}

// TestNonEmptySessionHasNoEmptyState guards the empty state's trigger: a
// session with a recorded message must not render it.
func TestNonEmptySessionHasNoEmptyState(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	if err := h.ctrl.Submit(context.Background(), h.ctrl.CurrentSessionID(), "hello"); err != nil {
		t.Fatalf("submit first message: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snap := h.ctrl.Snapshot(); snap != nil && len(snap.Messages) > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.app.SetSnapshot(h.ctrl.Snapshot())
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if tt.HasText("Start the conversation") {
		t.Fatalf("empty state rendered for a session with messages: %q", tt.Texts())
	}
}
