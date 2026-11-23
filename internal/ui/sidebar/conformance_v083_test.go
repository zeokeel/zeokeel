package sidebar_test

// Conformance frame tests for the sidebar's native widgets: the session rows are
// a mygo List (keyboard, Enter, single click), and the workspace switch dialog
// is a mygo Modal around a List (Up/Down choose, Enter opens) with the native
// directory picker behind 选择其他文件夹…. The old workspace Outline and its
// per-group actions were removed with the single-workspace view; the switch
// dialog tests live in workspace_switch_test.go.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// TestSessionListSingleClickAndKeyboard covers the List's behavior: a single
// click opens a session (including the one already active, which the List does
// not report as a new choice), Up/Down move only the choice, and Enter opens
// the chosen session.
func TestSessionListSingleClickAndKeyboard(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "会话一"},
			{ID: "s2", Title: "会话二"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	// The active session is the List's choice; clicking it still opens the
	// conversation screen from Home without re-setting the active session.
	if _, ok := tester.Find("会话一"); !ok {
		t.Fatalf("session row missing; texts = %v", tester.Texts())
	}
	h.click(tester, "会话一")
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("clicking the current session row did not open the session screen: %q", current)
	}
	if calls := chat.activeCalls(); len(calls) != 0 {
		t.Fatalf("clicking the already active session called SetActiveSession: %v", calls)
	}

	// A click on another row switches the session and opens it.
	h.click(tester, "会话二")
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session after click = %q, want s2", active)
	}

	// Up moves the choice without switching the conversation; Enter opens the
	// chosen row.
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("Up changed the active session to %q; the choice alone must move", active)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if active := chat.ActiveSession(); active != "s1" {
		t.Fatalf("Enter did not open the chosen session: active = %q", active)
	}
}

// TestSessionListChoiceSurvivesHostScreenNavigation pins the two selection
// models side by side: the session List chooses the current conversation while
// the host navigator owns the screen destination. The expanded sidebar no longer
// draws a Home row, so the host navigates directly (the collapsed rail and the
// command palette are the remaining destination entry points).
func TestSessionListChoiceSurvivesHostScreenNavigation(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "会话一"},
			{ID: "s2", Title: "会话二"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.click(tester, "会话二")
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	// The removed Home destination leaves no row behind; the host's navigator
	// still drives the screen, and that navigation never touches the
	// conversation choice.
	h.assertNoTexts(tester, "首页")
	if err := h.uiRoot.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tester.Frame()
	if current := h.uiRoot.Navigator().Current(); current != "home" {
		t.Fatalf("current screen after host navigation = %q, want home", current)
	}
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session changed by the host navigation: %q", active)
	}
}
