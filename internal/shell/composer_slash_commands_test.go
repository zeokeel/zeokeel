package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// slashComposer focuses the live composer, types one slash line and submits it
// with Enter, then renders the frames the deferred command feedback needs.
func slashComposer(t *testing.T, app *App, tt *ui.Tester, line string) {
	t.Helper()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type(line)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	// The command's toast is queued by the key handler and shown by the next
	// View; advance past the toast fade so HasText sees it.
	tt.Advance(300 * 1e6) // 300ms
	tt.Frame()
}

// TestSlashFileCommand proves /file both opens the real picker seam and
// attaches a workspace-relative path, while rejecting paths outside the
// workspace.
func TestSlashFileCommand(t *testing.T) {
	t.Run("bare opens the picker and attaches", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		notes := filepath.Join(h.dir, "notes.txt")
		if err := os.WriteFile(notes, []byte("hello"), 0o644); err != nil {
			t.Fatalf("write notes: %v", err)
		}
		picked := false
		h.app.PickFiles = func() ([]string, error) {
			picked = true
			return []string{notes}, nil
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/file")
		if !picked {
			t.Fatal("/file did not call the file picker seam")
		}
		pending, err := h.ctrl.PendingAttachments(context.Background(), h.ctrl.CurrentSessionID())
		if err != nil {
			t.Fatalf("PendingAttachments: %v", err)
		}
		if len(pending) != 1 || pending[0].Name != "notes.txt" {
			t.Fatalf("pending attachments = %+v, want notes.txt", pending)
		}
	})

	t.Run("workspace path attaches directly", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		notes := filepath.Join(h.dir, "notes.txt")
		if err := os.WriteFile(notes, []byte("hello"), 0o644); err != nil {
			t.Fatalf("write notes: %v", err)
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/file notes.txt")
		pending, err := h.ctrl.PendingAttachments(context.Background(), h.ctrl.CurrentSessionID())
		if err != nil {
			t.Fatalf("PendingAttachments: %v", err)
		}
		if len(pending) != 1 || pending[0].Name != "notes.txt" {
			t.Fatalf("pending attachments = %+v, want notes.txt", pending)
		}
	})

	t.Run("outside the workspace is rejected", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("hello"), 0o644); err != nil {
			t.Fatalf("write outside: %v", err)
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/file "+outside)
		pending, err := h.ctrl.PendingAttachments(context.Background(), h.ctrl.CurrentSessionID())
		if err != nil {
			t.Fatalf("PendingAttachments: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("outside-workspace path was attached: %+v", pending)
		}
		if !tt.HasText("Attachments must be inside the session workspace.") {
			t.Fatalf("missing outside-workspace toast: %v", tt.Texts())
		}
	})

	t.Run("home stages for the next session", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		notes := filepath.Join(h.dir, "notes.txt")
		if err := os.WriteFile(notes, []byte("hello"), 0o644); err != nil {
			t.Fatalf("write notes: %v", err)
		}
		h.app.Screen = Home
		h.app.draft = ""
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/file notes.txt")
		if snap := h.app.homeAttachmentSnapshot(); snap == nil || len(snap.Attachments) != 1 {
			t.Fatalf("home staging = %+v, want one attachment", h.app.homeAttachments)
		}
	})
}

// TestSlashPermissionCommand proves /permission reports the current mode and
// writes the session/staged access mode; invalid values change nothing.
func TestSlashPermissionCommand(t *testing.T) {
	t.Run("bare reports the current mode", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/permission")
		if h.app.draft != "" {
			t.Fatalf("/permission left draft %q", h.app.draft)
		}
		if !tt.HasText("Use /permission read-only|workspace|full|auto") {
			t.Fatalf("missing access-mode report: %v", tt.Texts())
		}
	})

	t.Run("argument writes the session override", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		id := h.ctrl.CurrentSessionID()
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/permission full")
		if got := h.ctrl.SessionPermissionOrDefault(id); got != session.PermissionFull {
			t.Fatalf("session permission = %q, want full", got)
		}
		slashComposer(t, h.app, tt, "/permission read-only")
		if got := h.ctrl.SessionPermissionOrDefault(id); got != session.PermissionAsk {
			t.Fatalf("session permission = %q, want read-only", got)
		}
	})

	t.Run("invalid value changes nothing", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		id := h.ctrl.CurrentSessionID()
		before := h.ctrl.SessionPermissionOrDefault(id)
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/permission bogus")
		if got := h.ctrl.SessionPermissionOrDefault(id); got != before {
			t.Fatalf("invalid /permission changed mode to %q", got)
		}
		if !tt.HasText("Unknown access mode") {
			t.Fatalf("missing invalid-value toast: %v", tt.Texts())
		}
	})

	t.Run("home stages the choice", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Home
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		slashComposer(t, h.app, tt, "/permission read-only")
		if h.app.homePermission != session.PermissionAsk {
			t.Fatalf("home permission = %q, want read-only", h.app.homePermission)
		}
	})
}

// TestSlashExportCommand proves /export runs the session log export through the
// save-dialog seam and writes the JSONL file.
func TestSlashExportCommand(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	target := filepath.Join(t.TempDir(), "export.jsonl")
	suggested := ""
	h.app.SaveSessionLog = func(path string) (string, error) {
		suggested = path
		return target, nil
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	slashComposer(t, h.app, tt, "/export")
	if !strings.Contains(suggested, "zeokeel-session-") {
		t.Fatalf("export suggested path = %q", suggested)
	}
	info, err := os.Stat(target)
	if err != nil || info.Size() == 0 {
		t.Fatalf("exported log missing or empty: %v", err)
	}
	if !tt.HasText("Session log saved to") {
		t.Fatalf("missing export toast: %v", tt.Texts())
	}
}

// TestSlashFeedbackCommand proves /feedback writes the real feedback rows: a
// rating on the newest assistant answer, and a session-level row without one.
func TestSlashFeedbackCommand(t *testing.T) {
	app := m6LiveApp(t, &m6Provider{steps: []m6Step{{text: "the answer"}}})
	app.Language = LangEN
	m6Submit(t, app, "hello")
	id := app.Ctrl.CurrentSessionID()
	snap := app.Snapshot()
	msgID := ""
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		if snap.Messages[i].Role == state.RoleAssistant {
			msgID = snap.Messages[i].MsgID
			break
		}
	}
	if msgID == "" {
		t.Fatal("fixture has no assistant message")
	}

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	slashComposer(t, app, tt, "/feedback bad")
	rows, err := app.Ctrl.Feedback(id)
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}
	if len(rows) != 1 || rows[0].Rating != "bad" || rows[0].MessageID != msgID {
		t.Fatalf("feedback rows = %+v, want one bad row for %q", rows, msgID)
	}
	if raw, _ := app.Ctrl.Store().GetSetting("feedback." + id + "." + msgID); raw != "bad" {
		t.Fatalf("feedback setting = %q, want bad", raw)
	}

	slashComposer(t, app, tt, "/feedback")
	rows, err = app.Ctrl.Feedback(id)
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}
	if len(rows) != 2 || rows[1].Rating != "recorded" || rows[1].MessageID != "session" {
		t.Fatalf("session feedback rows = %+v", rows)
	}

	// No assistant answer: the rating is refused instead of writing a row.
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt2 := ui.NewTester(h.app.View, 1600, 1000)
	tt2.Frame()
	slashComposer(t, h.app, tt2, "/feedback good")
	rows, err = h.ctrl.Feedback(h.ctrl.CurrentSessionID())
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("feedback without an answer wrote rows: %+v", rows)
	}
}

// TestSlashHelpListsAllCommands pins /help's catalog text and the documented
// unknown-command behavior.
func TestSlashHelpListsAllCommands(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	slashComposer(t, h.app, tt, "/help")
	for _, want := range []string{
		"/file", "/plan", "/feedback", "/permission", "/model", "/export",
		"/clear", "/stop", "/help", "/new", "/settings", "/automate", "/plugins",
		"Compaction is automatic", "Unknown commands are sent to the model",
	} {
		if !strings.Contains(h.app.draft, want) {
			t.Fatalf("/help text missing %q: %q", want, h.app.draft)
		}
	}
}

// TestSlashNewCommandsNeverReachModel checks the new commands run locally: the
// model provider is never called and no slash line becomes a user message.
func TestSlashNewCommandsNeverReachModel(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	h.app.Language = LangEN
	notes := filepath.Join(h.dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	h.app.SaveSessionLog = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "export.jsonl"), nil
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, line := range []string{"/file notes.txt", "/permission workspace", "/export", "/feedback"} {
		slashComposer(t, h.app, tt, line)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("new slash commands reached the model (%d calls)", got)
	}
	messages, err := h.store.ListMessages(h.ctrl.CurrentSessionID())
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, message := range messages {
		if message.Role == string(state.RoleUser) && strings.HasPrefix(strings.TrimSpace(message.Content), "/") {
			t.Fatalf("slash line became a user message: %q", message.Content)
		}
	}
}
