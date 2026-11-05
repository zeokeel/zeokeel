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

// TestApprovalCardFitsMinimumWindow covers the 900x600 approval card: the
// fixed 868px frame used to push the tool name off-window.
func TestApprovalCardFitsMinimumWindow(t *testing.T) {
	h := newApprovalApp(t, &alwaysAllowProvider{})
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "needs approval"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApprovalPending(t, h.ctrl)

	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	for _, label := range []string{"Approval required", "echo", "Allow", "Always allow", "Deny"} {
		r := assertRectInside(t, tt, label, 900, 600)
		if r.X+r.W > 900 {
			t.Fatalf("%q right edge = %v, outside the 900px window", label, r.X+r.W)
		}
	}
	// The card itself fits inside the main column.
	card := assertRectInside(t, tt, "Approval required", 900, 600)
	if card.X+card.W > 900 {
		t.Fatalf("approval header right edge = %v, outside the window", card.X+card.W)
	}
}

// TestApprovalCardFitsWithDockedPanel covers the same clamp when the right
// panel takes part of the content column.
func TestApprovalCardFitsWithDockedPanel(t *testing.T) {
	h := newApprovalApp(t, &alwaysAllowProvider{})
	h.app.rightOpen = true
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "needs approval"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApprovalPending(t, h.ctrl)
	tt := ui.NewTester(h.app.View, 1200, 800)
	tt.Frame()
	for _, label := range []string{"echo", "Allow", "Always allow", "Deny"} {
		assertRectInside(t, tt, label, 1200, 800)
	}
}

// TestRightSidebarCollapseTargetAtMinimumWindow measures the panel's own
// collapse control (now uniquely named) rather than the Session header's
// duplicate, and requires a real 28x28 target.
func TestRightSidebarCollapseTargetAtMinimumWindow(t *testing.T) {
	for _, screen := range []struct {
		s Screen
		n string
	}{{Plugins, "plugins"}, {Automation, "automation"}, {Session, "session"}} {
		t.Run(screen.n, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			h.app.Screen = screen.s
			h.app.rightOpen = true
			tt := ui.NewTester(h.app.View, 900, 600)
			tt.Frame()
			panel, ok := tt.Find("Right sidebar")
			if !ok {
				t.Fatal("right panel missing")
			}
			control, ok := tt.Find("Collapse right sidebar")
			if !ok {
				t.Fatalf("panel collapse control missing: %v", tt.Texts())
			}
			if control.W < 28 || control.H < 28 {
				t.Fatalf("panel collapse control = %+v, want at least 28x28", control)
			}
			if control.X < panel.X || control.X+control.W > panel.X+panel.W+1 ||
				control.X+control.W > 900 || control.Y+control.H > 600 {
				t.Fatalf("collapse control %+v is outside the panel %+v", control, panel)
			}
			for _, label := range []string{"New tab", "Split", "Fullscreen"} {
				r := assertRectInside(t, tt, label, 900, 600)
				if r.X+r.W > panel.X+panel.W+1 {
					t.Fatalf("%q right edge = %v overflows panel right %v", label, r.X+r.W, panel.X+panel.W)
				}
			}
			tt.ClickAt(control.X+control.W/2, control.Y+control.H/2)
			tt.Frame()
			if h.app.rightOpen {
				t.Fatal("panel collapse control did not close the panel")
			}
		})
	}
}

// TestLiveMentionListboxGeometryAndKeyboard covers keyboard selection in the
// mention listbox: Down/Enter and Tab/Enter choose rows instead of being
// mouse-only. (The former DSH listbox width / 34px row-height pins were retired
// with pixel parity.)
func TestLiveMentionListboxGeometryAndKeyboard(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	for _, name := range []string{"probe.txt", "probe2.txt"} {
		if err := os.WriteFile(filepath.Join(h.dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	h.app.Screen = Home
	h.app.draft = "@probe"
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText("Files") {
		t.Fatalf("mention menu has no section title: %v", tt.Texts())
	}
	if _, ok := tt.Find("probe.txt"); !ok {
		t.Fatalf("mention row missing: %v", tt.Texts())
	}

	// Down then Enter in the composer selects the second row.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.draft != "@probe2.txt " {
		t.Fatalf("Down+Enter draft = %q, want @probe2.txt ", h.app.draft)
	}

	// Tab moves focus into the list; Enter chooses the focused row.
	h.app.draft = "@probe"
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("probe.txt") {
		t.Fatalf("Tab did not focus the first mention row: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.draft != "@probe.txt " {
		t.Fatalf("Tab+Enter draft = %q, want @probe.txt ", h.app.draft)
	}
}

// TestHomeSendHitTargetCoversCircle clicks the top and bottom edges of the
// visible 34x34 Send circle; the focusable row used to be only 16px tall.
func TestHomeSendHitTargetCoversCircle(t *testing.T) {
	for _, edge := range []struct {
		name string
		dy   float32
	}{{"top", 1}, {"bottom", 33}} {
		t.Run(edge.name, func(t *testing.T) {
			provider := &shellProvider{}
			h := newLiveApp(t, provider)
			h.app.Language = LangEN
			h.app.Screen = Home
			h.app.draft = "send me"
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			send, ok := tt.Find("Send message")
			if !ok {
				t.Fatalf("Send message missing: %v", tt.Texts())
			}
			if send.W < 33 || send.H < 33 {
				t.Fatalf("Send target = %+v, want the full 34x34 circle", send)
			}
			tt.ClickAt(send.X+send.W/2, send.Y+edge.dy)
			tt.Frame()
			waitIdle(t, h.ctrl)
			h.ctrl.Wait()
			if got := provider.callCount(); got != 1 {
				t.Fatalf("click at %s edge produced %d model calls, want 1", edge.name, got)
			}
		})
	}
}

// TestDarkLiveOffSwitchContrast covers the live dark Plugins switch: the OFF
// knob and track must meet WCAG 2.1 SC 1.4.11 non-text contrast (3:1). (The
// former DSH token/colour pins were retired with pixel parity.)
func TestDarkLiveOffSwitchContrast(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.store.SetSetting(session.SettingTheme, session.ThemeDark); err != nil {
		t.Fatalf("theme setting: %v", err)
	}
	if err := h.ctrl.SetPluginEnabled("voice_input", false); err != nil {
		t.Fatalf("disable Voice input: %v", err)
	}
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	off, ok := tt.Find("Enable Voice input")
	if !ok {
		t.Fatalf("disabled switch missing: %v", tt.Texts())
	}
	img := tt.Image()
	knob := img.RGBAAt(int(off.X)+6, int(off.Y+off.H/2))
	track := img.RGBAAt(int(off.X+off.W)-6, int(off.Y+off.H/2))
	ratio := contrastRatio(ui.RGBA(track.R, track.G, track.B, 1), ui.RGBA(knob.R, knob.G, knob.B, 1))
	if ratio < 3 {
		t.Fatalf("dark OFF switch contrast = %.2f, want >= 3 (WCAG non-text contrast)", ratio)
	}
}

// TestCrossWorkspaceSessionClicks covers both real UI groupings: clicking a
// session from another workspace must select that workspace too.
func TestCrossWorkspaceSessionClicks(t *testing.T) {
	for _, grouping := range []struct {
		name string
		by   string
	}{{"In one list", groupByList}, {"Workspace Tree", groupByTree}} {
		t.Run(grouping.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			ctx := context.Background()
			first := h.ctrl.CurrentWorkspaceID()
			second, err := h.ctrl.CreateWorkspace(ctx, "second-ws", t.TempDir())
			if err != nil {
				t.Fatalf("CreateWorkspace: %v", err)
			}
			title := "second-ws-session"
			otherSession, err := h.ctrl.CreateSessionInWorkspace(ctx, second.ID, title)
			if err != nil {
				t.Fatalf("CreateSessionInWorkspace: %v", err)
			}
			if err := h.ctrl.SelectWorkspace(ctx, first); err != nil {
				t.Fatalf("SelectWorkspace(first): %v", err)
			}
			h.app.Screen = Home
			h.app.groupBy = grouping.by
			// Keep restoreViewOptions from replacing the grouping with the
			// persisted default on the first frame.
			h.app.viewOptionsLoaded = true
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			if err := tt.Click(title); err != nil {
				t.Fatalf("click cross-workspace session %q: %v", title, err)
			}
			tt.Frame()
			if got := h.ctrl.CurrentSessionID(); got != otherSession {
				t.Fatalf("current session = %q, want %q", got, otherSession)
			}
			if got := h.ctrl.CurrentWorkspaceID(); got != second.ID {
				t.Fatalf("workspace after %s click = %q, want %q", grouping.name, got, second.ID)
			}
		})
	}
}

// TestSettingsModalKeepsDraftScope covers Cmd+N and a palette session-open
// while Settings is open: the background surface keeps its own draft.
func TestSettingsModalKeepsDraftScope(t *testing.T) {
	t.Run("Cmd+N", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		oldID := h.ctrl.CurrentSessionID()
		h.app.draft = "draft before settings"
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if h.app.Screen != Session {
			t.Fatalf("screen = %v, want Session", h.app.Screen)
		}
		tt.Key(ui.Cmd, ui.KeyComma)
		tt.Frame()
		if h.app.Screen != Settings {
			t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
		}
		tt.Key(ui.Cmd, ui.KeyN)
		tt.Frame()
		if h.app.Screen != Session {
			t.Fatalf("Cmd+N screen = %v, want Session", h.app.Screen)
		}
		if id := h.ctrl.CurrentSessionID(); id == oldID {
			t.Fatal("Cmd+N did not create a new session")
		}
		if h.app.draft != "" {
			t.Fatalf("new session draft = %q, want blank", h.app.draft)
		}
		if got := h.app.draftForSession(oldID); got != "draft before settings" {
			t.Fatalf("old session draft = %q, want the settings-time text", got)
		}
		h.app.openSession(oldID)
		tt.Frame()
		if h.app.draft != "draft before settings" {
			t.Fatalf("reopened draft = %q, want restored", h.app.draft)
		}
	})

	t.Run("Home draft", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Home
		h.app.draft = "home draft"
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyComma)
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyN)
		tt.Frame()
		if h.app.homeDraft != "home draft" {
			t.Fatalf("homeDraft = %q, want the Home draft saved across Settings", h.app.homeDraft)
		}
		h.app.Screen = Home
		tt.Frame()
		if h.app.draft != "home draft" {
			t.Fatalf("Home draft after returning = %q, want restored", h.app.draft)
		}
	})

	t.Run("palette open", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		h.app.Screen = Session
		oldID := h.ctrl.CurrentSessionID()
		other, err := h.ctrl.CreateSession(context.Background(), "other")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if _, err := h.ctrl.OpenSession(context.Background(), oldID); err != nil {
			t.Fatalf("OpenSession(old): %v", err)
		}
		h.app.draft = "palette draft"
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyComma)
		tt.Frame()
		h.app.openSession(other)
		tt.Frame()
		if h.app.Screen != Session {
			t.Fatalf("screen = %v, want Session", h.app.Screen)
		}
		if got := h.app.draftForSession(oldID); got != "palette draft" {
			t.Fatalf("old draft = %q, want saved before the palette jump", got)
		}
		if h.app.draft != "" {
			t.Fatalf("opened session draft = %q, want blank", h.app.draft)
		}
	})
}

// TestShortcutEditorReportsInvalidInput covers the silent Save: an invalid
// chord and a failing store write must both surface an inline error.
func TestShortcutEditorReportsInvalidInput(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if err := tt.Click("Edit shortcuts"); err != nil {
		t.Fatalf("open shortcuts editor: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Edit shortcut for New session"); err != nil {
		t.Fatalf("start editing: %v", err)
	}
	tt.Frame()
	h.app.shortcutDraft = "definitely not a key"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save invalid binding: %v", err)
	}
	tt.Frame()
	if h.app.shortcutEditID == "" {
		t.Fatal("invalid Save closed the editor instead of showing an error")
	}
	if !tt.HasText("not a valid shortcut") || !tt.HasText("definitely not a key") {
		t.Fatalf("invalid Save gave no inline error: %v", tt.Texts())
	}
	if !tt.Focused("Shortcut for New session") {
		t.Fatalf("focus left the shortcut field after an invalid Save: %v", tt.Texts())
	}
	if raw, _ := h.ctrl.Store().GetSetting(keybindingKey("new_session")); raw != "" {
		t.Fatalf("invalid Save persisted %q", raw)
	}

	// A store write failure is distinguished from a parse failure.
	if err := h.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	err := h.app.saveShortcutOverride("new_session", "Cmd+Shift+J")
	if err == nil {
		t.Fatal("save against a closed store returned nil")
	}
	if !strings.Contains(err.Error(), "could not save") {
		t.Fatalf("closed-store error = %v, want a distinct save failure", err)
	}
}

// TestLiveSessionUsageNotPinnedToRightEdge keeps the usage pills over the
// centered composer instead of the window's right edge.
func TestLiveSessionUsageNotPinnedToRightEdge(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	// Give the session usage so the footer renders (it hides before any turn).
	usageSnap := state.NewSnapshot()
	usageSnap.Usage = state.Usage{Turns: 1, Steps: 2, In: 100, Out: 20, CacheHit: 50}
	h.app.SetSnapshot(usageSnap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	usage, ok := tt.Find("Session usage")
	if !ok {
		t.Fatal("usage footer missing")
	}
	composer, _ := tt.Find("Session composer")
	if usage.X+usage.W > 1580 {
		t.Fatalf("usage row right edge = %v, pinned to the window edge", usage.X+usage.W)
	}
	if usage.X+usage.W/2 > composer.X+composer.W/2+40 {
		t.Fatalf("usage row is not centered over the composer: usage %+v composer %+v", usage, composer)
	}
}
