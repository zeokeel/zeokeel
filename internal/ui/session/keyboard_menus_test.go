package session_test

// Frame tests for the composer's keyboard-driven suggestion menus: the slash
// command menu and the @ reference menu take arrows, Enter/Tab and Esc without
// sending the draft or stealing the focus.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// registerKeyboardCommands registers two commands whose titles share a token,
// so a filtered slash menu lists exactly these two rows.
func registerKeyboardCommands(t *testing.T, h *harness, runs *[]string) {
	t.Helper()
	for _, name := range []string{"键盘甲", "键盘乙"} {
		name := name
		unregister, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
			ID:    "test.keyboard." + name,
			Title: name,
			Hint:  "键盘测试",
			Order: 10,
			Run:   func() error { *runs = append(*runs, name); return nil },
		})
		if err != nil {
			t.Fatalf("register command %s: %v", name, err)
		}
		t.Cleanup(unregister)
	}
}

// rowOrder returns the on-screen order of the wanted texts.
func rowOrder(t *testing.T, tester *ui.Tester, wanted ...string) []string {
	t.Helper()
	type placed struct {
		name string
		y    float32
	}
	placedRows := make([]placed, 0, len(wanted))
	for _, name := range wanted {
		rect, ok := tester.Find(name)
		if !ok {
			t.Fatalf("%q missing; texts = %v", name, tester.Texts())
		}
		placedRows = append(placedRows, placed{name: name, y: rect.Y})
	}
	for i := 1; i < len(placedRows); i++ {
		for j := i; j > 0 && placedRows[j].y < placedRows[j-1].y; j-- {
			placedRows[j], placedRows[j-1] = placedRows[j-1], placedRows[j]
		}
	}
	out := make([]string, 0, len(placedRows))
	for _, row := range placedRows {
		out = append(out, row.name)
	}
	return out
}

// TestSlashMenuKeyboard drives the slash menu from the keyboard: Down moves the
// highlight and Enter runs the highlighted command (never sends the draft),
// Tab accepts, Esc closes the menu without clearing the draft.
func TestSlashMenuKeyboard(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "键盘斜杠"}, true)
	h := newHarness(t, chat)
	var runs []string
	registerKeyboardCommands(t, h, &runs)
	tester := h.tester()
	focusComposer(t, tester)

	tester.Type("/键盘")
	tester.Frame()
	h.assertTexts(tester, "键盘甲", "键盘乙")
	order := rowOrder(t, tester, "键盘甲", "键盘乙")

	// Down moves the highlight from the first row to the second; Enter runs it.
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(runs) != 1 || runs[0] != order[1] {
		t.Fatalf("runs after Down+Enter = %v, want [%s]", runs, order[1])
	}
	h.assertNoTexts(tester, "键盘甲", "键盘乙")
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("Enter sent the slash draft as chat: %v", sends)
	}

	// Tab accepts the highlighted row (the menu's first row on reopen).
	focusComposer(t, tester)
	tester.Type("/键盘")
	tester.Frame()
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if len(runs) != 2 || runs[1] != order[0] {
		t.Fatalf("runs after Tab = %v, want [%s]", runs, order[0])
	}
	h.assertNoTexts(tester, "键盘甲", "键盘乙")
}

// TestSlashMenuEscapeKeepsDraft covers the dismissal semantics: Esc closes the
// menu without clearing the draft, and Enter then sends the draft normally (the
// menu is closed, so there is no command to run).
func TestSlashMenuEscapeKeepsDraft(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "键盘斜杠"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposer(t, tester)

	tester.Type("/xyz")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的命令")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "没有匹配的命令")
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer lost focus after Esc; texts = %v", tester.Texts())
	}

	// Enter with the menu closed sends the draft normally: Esc did not clear
	// it, and a literal "/xyz" message stays sendable.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	sends := chat.sends()
	if len(sends) != 1 || sends[0].prompt != "/xyz" {
		t.Fatalf("Send calls after Esc+Enter = %v, want [/xyz]", sends)
	}
	// The send started a run: the transcript shows the sent message.
	h.assertTexts(tester, "/xyz")
}

// TestSlashDraftEnterSemantics covers N1: Enter runs a command only while the
// menu is open with a highlighted match; with no match (or a dismissed menu) it
// sends the draft like the Send button does.
func TestSlashDraftEnterSemantics(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "斜杠发送"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposer(t, tester)

	// No command matches "/note": the menu is open but empty, so Enter sends
	// the literal message.
	tester.Type("/note")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的命令")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	sends := chat.sends()
	if len(sends) != 1 || sends[0].prompt != "/note" {
		t.Fatalf("Send calls for a no-match slash draft = %v, want [/note]", sends)
	}

	// A matching command takes Enter instead: the command runs and nothing is
	// sent as chat.
	var runs []string
	registerKeyboardCommands(t, h, &runs)
	chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "斜杠发送"})
	tester.Frame()
	focusComposer(t, tester)
	tester.Type("/键盘")
	tester.Frame()
	h.assertTexts(tester, "键盘甲", "键盘乙")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(runs) != 1 {
		t.Fatalf("command runs after Enter = %v, want one", runs)
	}
	if sends := chat.sends(); len(sends) != 1 {
		t.Fatalf("a matched slash command was also sent as chat: %v", sends)
	}
}

// TestMentionMenuKeyboard covers the @ menu's keyboard: Down moves the
// highlight, Enter inserts the reference (the composer text becomes the
// mention), Esc closes the menu and leaves the draft to send as chat.
func TestMentionMenuKeyboard(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "键盘引用"}, true)
	workspace := &fakeWorkspace{
		search: []contracts.FileEntry{{Name: "README.md", Path: "README.md", Size: 112}},
	}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()
	focusMentionComposer(t, h, tester)

	tester.Type("@REA")
	tester.Frame()
	h.assertTexts(tester, "工作区文件", "README.md")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertNoTexts(tester, "工作区文件")

	h.click(tester, "发送")
	sends := chat.sends()
	if len(sends) != 1 || sends[0].prompt != "@README.md" {
		t.Fatalf("Send calls = %v, want [@README.md]", sends)
	}

	// Finish the run the first send started, so the composer is editable again.
	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "键盘引用",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "@README.md"}},
	})
	tester.Frame()

	// Esc dismisses the menu without touching the draft; Enter then sends the
	// typed mention text as a normal message.
	focusMentionComposer(t, h, tester)
	tester.Type("@REA")
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "工作区文件")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	sends = chat.sends()
	if len(sends) != 2 || sends[1].prompt != "@REA" {
		t.Fatalf("Send calls after Esc = %v, want the mention draft sent", sends)
	}
}
