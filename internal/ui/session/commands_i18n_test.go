package session_test

// i18n tests for the slash-command registry titles/hints: they follow the host
// locale on activation and after a language switch (the command palette and the
// /help modal read the registry directly).

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// focusEnglishComposer focuses the composer in the English locale (the shared
// helper's zh labels no longer exist after the language switch).
func focusEnglishComposer(t *testing.T, tester *ui.Tester) {
	t.Helper()
	send, ok := tester.Find("Send")
	if !ok {
		t.Fatalf("English send control missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(send.X-120, send.Y-50)
	tester.Frame()
	if !tester.Focused("Ask ZeoKeel…") {
		t.Fatalf("English composer TextArea did not take focus; texts = %v", tester.Texts())
	}
}

func TestSessionCommandsFollowLocale(t *testing.T) {
	h := newCommandDialogHarness(t, permissionChat(), "zh", nil, nil, nil)
	tester := h.tester()
	tester.Frame()

	if got := findSessionCommand(t, h, "zeokeel.session.stop").Title; got != "停止" {
		t.Fatalf("zh stop title = %q, want 停止", got)
	}
	if got := findSessionCommand(t, h, "zeokeel.session.help").Hint; got != "查看可用命令与快捷键" {
		t.Fatalf("zh help hint = %q", got)
	}

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	for id, want := range map[string][2]string{
		"zeokeel.session.new":    {"New conversation", "Start and open a new conversation"},
		"zeokeel.session.stop":   {"Stop", "Stop the current reply"},
		"zeokeel.session.attach": {"Attach files", "Add files or images to the input"},
		"zeokeel.session.help":   {"Help", "Show the available commands and shortcuts"},
	} {
		command := findSessionCommand(t, h, id)
		if command.Title != want[0] || command.Hint != want[1] {
			t.Fatalf("%s = %q/%q, want %q/%q", id, command.Title, command.Hint, want[0], want[1])
		}
	}

	// The slash menu renders the refreshed English titles/hints.
	focusEnglishComposer(t, tester)
	tester.Type("/")
	tester.Frame()
	// The disabled /stop command (no running session) is filtered from the menu.
	h.assertTexts(tester, "New conversation", "Start and open a new conversation",
		"Attach files", "Add files or images to the input", "Help")

	// Switching back restores the Chinese registry on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	if got := findSessionCommand(t, h, "zeokeel.session.stop").Title; got != "停止" {
		t.Fatalf("zh stop title after switching back = %q", got)
	}
}

func TestHelpModalEnglishCommandTitles(t *testing.T) {
	h := newCommandDialogHarness(t, permissionChat(), "en", nil, nil, nil)
	tester := h.tester()
	tester.Frame()
	help := findSessionCommand(t, h, "zeokeel.session.help")
	if err := help.Run(); err != nil {
		t.Fatalf("help Run: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Help", "Slash commands", "New conversation", "Stop",
		"Attach files", "Permission mode", "Esc to close")
}
