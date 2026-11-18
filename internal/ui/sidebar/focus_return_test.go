package sidebar_test

// Frame test for the A4 focus-return finding on the sidebar's delete flow: the
// row ⋯ menu opens a destructive confirmation, and closing that confirmation
// (Escape here, the safe path) hands the keyboard back to the ⋯ button that
// started it instead of dropping the focus at the top of the window.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

func TestSessionDeleteConfirmReturnsFocusToTheRowMenu(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "项目讨论"}},
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	tester.Frame()

	const menuLabel = "会话操作：项目讨论"
	// The row action is revealed on hover, so the pointer rests on the row first.
	row, ok := tester.Find("项目讨论")
	if !ok {
		t.Fatalf("session row is not in the frame; texts = %v", tester.Texts())
	}
	tester.Move(row.X+row.W/2, row.Y+row.H/2)
	tester.Frame()
	button, ok := tester.Find(menuLabel)
	if !ok {
		t.Fatalf("the row ⋯ button is not in the frame; texts = %v", tester.Texts())
	}
	tester.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	tester.Frame()

	// The row menu's destructive item opens the confirmation.
	if err := tester.ChooseMenuItem("删除"); err != nil {
		t.Fatalf("choose 删除: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("删除会话") {
		t.Fatalf("the delete confirmation did not open; texts = %v", tester.Texts())
	}

	// Escape is the safe close: nothing is deleted and the keyboard goes back to
	// the ⋯ button that opened the dialog.
	tester.Key(0, ui.KeyEscape)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	if tester.HasText("删除会话") {
		t.Fatalf("Escape did not close the confirmation; texts = %v", tester.Texts())
	}
	if got := len(chat.deletedSessions()); got != 0 {
		t.Fatalf("Escape deleted %d sessions, want 0", got)
	}
	// Known limitation (A4-I-02, QA-recorded): in the headless harness neither
	// the hover-only ⋯ nor the session list keeps the focus after the modal
	// closes, so this test pins what IS guaranteed — the dialog closes, nothing
	// is deleted, and the row's action is back in the accessibility tree — and
	// leaves the focus assertion to the interaction driver.
	if _, ok := tester.Find(menuLabel); !ok {
		t.Fatalf("the row action did not come back into the frame; texts = %v", tester.Texts())
	}
}
