package sidebar_test

// Frame tests for the keyboard path to a session row's context menu: with the
// native list focused and a row chosen, the menu key or Shift+F10 opens the
// chosen row's menu (mygo docs/ui/context-menu.md: the menu key, or Shift+F10,
// opens the menu of the chosen row of a focused list).

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// TestSessionListMenuKeyOpensChosenRowMenu: the session List keeps the focus
// while the rows paint the choice, so the keyboard must reach the same row menu
// the ⋯ button and a right-click open. The chosen row is deliberately the
// non-active one: only that menu carries 设为当前, which proves the menu belongs
// to the choice rather than to the current conversation.
func TestSessionListMenuKeyOpensChosenRowMenu(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "会话一"},
			{ID: "s2", Title: "会话二"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	// Click the second row: the List takes the keyboard and the choice, and the
	// session becomes the active one.
	h.click(tester, "会话二")
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session after the click = %q, want s2", active)
	}
	// Move the choice back to the first row: chosen, but not active.
	tester.Key(0, ui.KeyUp)
	tester.Frame()

	assertNonActiveMenu := func(key ui.Key, mods ui.Modifiers) {
		t.Helper()
		tester.Key(mods, key)
		tester.Frame()
		menu := tester.Menu()
		if !slices.Contains(menu, "设为当前") {
			t.Fatalf("%v opened %q, want the chosen non-active session's menu (with 设为当前); texts = %v",
				key, menu, tester.Texts())
		}
		if !slices.Contains(menu, "删除") {
			t.Fatalf("%v opened %q, want the session menu (with 删除)", key, menu)
		}
		tester.CloseMenu()
		tester.Frame()
	}

	assertNonActiveMenu(ui.KeyContextMenu, 0)

	// Closing the menu hands the focus back to the list, which shows the current
	// session as its choice while it has no cursor; move the choice back to the
	// non-active row and open the same menu with Shift+F10.
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	assertNonActiveMenu(ui.KeyF10, ui.Shift)
}
