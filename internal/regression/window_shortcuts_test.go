package regression

// Adversarial audit: the built-in window shortcuts Cmd/Ctrl+N (new session) and
// Cmd/Ctrl+, (settings). The uiroot package unit-tests the handlers; this file
// adds the regression-layer branches: session creation + navigation through the
// kernel-bound chat service, the independent Cmd/Ctrl+K/B chrome shortcuts, a
// failing NewSession, and a product without any chat backend.

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// startShortcutUI builds the host UI root bound to the kernel context with an
// optional chat service and a stub session screen.
func startShortcutUI(t *testing.T, chat contracts.ChatService) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	if chat != nil {
		provide(t, h.ctx, contracts.KeyChat, chat)
	}
	h.ui.SetKernelContext(h.ctx)
	h.registerScreen("session", "会话", "session-body")
	tester := h.tester(1200, 800)
	tester.Frame()
	return h, tester
}

// TestWindowShortcutNewSessionAndSettings covers Cmd+N/Cmd+,: a new session is
// created and navigated to, settings toggle independently, and Cmd+K/B keep
// their own behavior without creating sessions.
// settleShortcut advances frames/timers until cond holds or a short budget
// expires; shortcut handlers can post their work across frames.
func settleShortcut(t *testing.T, tester *ui.Tester, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; texts = %v", what, tester.Texts())
		}
		tester.Advance(5 * time.Millisecond)
	}
}

func TestWindowShortcutNewSessionAndSettings(t *testing.T) {
	chat := newFakeChat()
	h, tester := startShortcutUI(t, chat)

	// Cmd/Ctrl+N: create and navigate.
	tester.Key(ui.Cmd, ui.KeyN)
	settleShortcut(t, tester, "first new session", func() bool { return chat.sessionCount() == 1 })
	if current := h.ui.Navigator().Current(); current != "session" {
		t.Fatalf("Cmd+N navigator current = %q, want session", current)
	}
	tester.Key(ui.Cmd, ui.KeyN)
	settleShortcut(t, tester, "second new session", func() bool { return chat.sessionCount() == 2 })

	// Cmd/Ctrl+,: toggles the settings surface.
	if h.ui.Navigator().SettingsOpen() {
		t.Fatal("settings open before Cmd+,")
	}
	tester.Key(ui.Cmd, ui.KeyComma)
	settleShortcut(t, tester, "settings open", func() bool { return h.ui.Navigator().SettingsOpen() })
	// Closing the surface (Escape/button path) and reopening with the shortcut
	// works; the open action itself is idempotent.
	h.ui.Navigator().CloseSettings()
	tester.Frame()
	if h.ui.Navigator().SettingsOpen() {
		t.Fatal("settings did not close")
	}
	tester.Key(ui.Cmd, ui.KeyComma)
	settleShortcut(t, tester, "settings reopen", func() bool { return h.ui.Navigator().SettingsOpen() })
	h.ui.Navigator().CloseSettings()
	tester.Frame()

	// Cmd+K opens the palette; Cmd+B collapses the sidebar. Neither creates a
	// session nor opens settings (no shortcut conflict).
	before := chat.sessionCount()
	tester.Key(ui.Cmd, ui.KeyK)
	tester.Frame()
	requireHasText(t, tester, "命令面板")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	requireNoText(t, tester, "命令面板")
	tester.Key(ui.Cmd, ui.KeyB)
	tester.Frame()
	if chat.sessionCount() != before {
		t.Fatalf("Cmd+K/B created sessions: before %d after %d", before, chat.sessionCount())
	}
	if h.ui.Navigator().SettingsOpen() {
		t.Fatal("Cmd+K/B opened settings")
	}
}

// TestWindowShortcutsWithoutChatAreSafe covers a product without a chat backend
// and a failing NewSession: the shortcuts stay silent (no panic, no navigation).
func TestWindowShortcutsWithoutChatAreSafe(t *testing.T) {
	// No chat provided: Cmd+N has no handler and must not panic.
	h, tester := startShortcutUI(t, nil)
	initial := h.ui.Navigator().Current()
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	if current := h.ui.Navigator().Current(); current != initial {
		t.Fatalf("Cmd+N without chat navigated from %q to %q", initial, current)
	}
	// The other shortcuts still work.
	tester.Key(ui.Cmd, ui.KeyComma)
	tester.Frame()
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("Cmd+, without chat did not open settings")
	}

	// A failing NewSession is silent: no navigation, no panic.
	failing := newFakeChat()
	failing.setNewSessionError(errors.New("store closed"))
	h, tester = startShortcutUI(t, failing)
	initial = h.ui.Navigator().Current()
	tester.Key(ui.Cmd, ui.KeyN)
	tester.Frame()
	if current := h.ui.Navigator().Current(); current != initial {
		t.Fatalf("Cmd+N with a failing NewSession navigated from %q to %q", initial, current)
	}
	if got := failing.sessionCount(); got != 0 {
		t.Fatalf("failing NewSession created %d sessions", got)
	}
}
