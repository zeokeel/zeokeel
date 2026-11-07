package regression

// Adversarial audit: slash-command i18n. In the English locale every session
// command's Title/Hint is English, and a language switch re-registers the
// commands so the palette/help modal follow on the next frame; switching back
// restores Chinese.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// englishSessionCommands is the expected English copy of the session commands.
var englishSessionCommands = map[string][2]string{
	"zeokeel.session.new":        {"New conversation", "Start and open a new conversation"},
	"zeokeel.session.fork":       {"Duplicate conversation", "Copy the current conversation"},
	"zeokeel.session.export":     {"Export conversation", "Export the current conversation as Markdown"},
	"zeokeel.session.goal":       {"Set goal", "Set or clear a long-term goal for this conversation"},
	"zeokeel.session.plan":       {"Review plan", "Submit a plan for the user to approve"},
	"zeokeel.session.memory":     {"Add memory", "Save a global memory"},
	"zeokeel.session.feedback":   {"Feedback", "Rate the latest answer"},
	"zeokeel.session.permission": {"Permission mode", "Cycle permission: ask → workspace → full"},
	"zeokeel.session.settings":   {"Open settings", "Open the settings panel"},
	"zeokeel.session.stop":       {"Stop", "Stop the current reply"},
	"zeokeel.session.attach":     {"Attach files", "Add files or images to the input"},
	"zeokeel.session.help":       {"Help", "Show the available commands and shortcuts"},
}

// sessionCommandByID finds one registered command.
func sessionCommandByID(t *testing.T, h *harness, id string) contracts.CommandSpec {
	t.Helper()
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == id {
			return command
		}
	}
	t.Fatalf("command %q is not registered", id)
	return contracts.CommandSpec{}
}

// startCommandLocaleUI boots the session screen with one active session.
func startCommandLocaleUI(t *testing.T, language string) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	if language != "" {
		h.ui.Locale().SetLanguage(language)
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-i18n", Title: "i18n session",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	return h, tester
}

// TestSessionCommandsEnglishCopy covers the English registry: all commands have
// English titles/hints on activation.
func TestSessionCommandsEnglishCopy(t *testing.T) {
	h, _ := startCommandLocaleUI(t, "en")
	for id, want := range englishSessionCommands {
		command := sessionCommandByID(t, h, id)
		if command.Title != want[0] || command.Hint != want[1] {
			t.Fatalf("%s = %q/%q, want %q/%q", id, command.Title, command.Hint, want[0], want[1])
		}
		for _, text := range []string{command.Title, command.Hint} {
			if containsHan(text) {
				t.Fatalf("%s still has Chinese copy: %q/%q", id, command.Title, command.Hint)
			}
		}
	}
}

// containsHan reports whether text contains a Han character.
func containsHan(text string) bool {
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

// TestSessionCommandLocaleSwitchNextFrame covers the mid-session switch: the
// registry follows on the next frame (help modal included), and switching back
// restores Chinese.
func TestSessionCommandLocaleSwitchNextFrame(t *testing.T) {
	h, tester := startCommandLocaleUI(t, "zh")
	if got := sessionCommandByID(t, h, "zeokeel.session.stop").Title; got != "停止" {
		t.Fatalf("zh stop title = %q, want 停止", got)
	}

	// Switch to English: the next frame re-registers the commands.
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	stop := sessionCommandByID(t, h, "zeokeel.session.stop")
	if stop.Title != "Stop" || stop.Hint != "Stop the current reply" {
		t.Fatalf("en stop command = %q/%q after switch", stop.Title, stop.Hint)
	}
	if got := sessionCommandByID(t, h, "zeokeel.session.help").Title; got != "Help" {
		t.Fatalf("en help title after switch = %q", got)
	}

	// The help modal renders the refreshed English copy.
	help := sessionCommandByID(t, h, "zeokeel.session.help")
	if err := help.Run(); err != nil {
		t.Fatalf("Run /help: %v", err)
	}
	tester.Frame()
	requireHasTexts(t, tester, "Help", "Slash commands", "New conversation", "Stop",
		"Attach files", "Permission mode", "Esc to close")
	requireNoText(t, tester, "斜杠命令")

	// Escape closes the modal; switching back restores the Chinese registry.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	if got := sessionCommandByID(t, h, "zeokeel.session.stop").Title; got != "停止" {
		t.Fatalf("zh stop title after switching back = %q", got)
	}
	if got := sessionCommandByID(t, h, "zeokeel.session.attach").Title; got != "附加文件" {
		t.Fatalf("zh attach title after switching back = %q", got)
	}
}
