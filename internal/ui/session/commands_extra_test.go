package session_test

// TDD tests (RED first) for the composer commands /stop, /file and /help.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// commandsOf returns the registered commands of one attachmentHarness.
func commandsOf(h *attachmentHarness) []contracts.CommandSpec { return h.ui.Runtime().Commands() }

// commandOf finds one command of the attachmentHarness.
func commandOf(t *testing.T, h *attachmentHarness, id string) contracts.CommandSpec {
	t.Helper()
	for _, command := range commandsOf(h) {
		if command.ID == id {
			return command
		}
	}
	t.Fatalf("command %q is not registered; commands = %v", id, commandsOf(h))
	return contracts.CommandSpec{}
}

func TestExtraCommandsRegistered(t *testing.T) {
	h := newAttachmentHarness(t, newAttachmentChat())
	wanted := []struct {
		id, title, hint string
		order           int
	}{
		{"zeokeel.session.stop", "停止", "停止当前生成的回复", 44},
		{"zeokeel.session.attach", "附加文件", "添加文件或图片到输入框", 46},
		{"zeokeel.session.help", "帮助", "查看可用命令与快捷键", 55},
	}
	for _, want := range wanted {
		command := commandOf(t, h, want.id)
		if command.Title != want.title || command.Hint != want.hint || command.Order != want.order {
			t.Fatalf("%s = %q/%q/%d, want %q/%q/%d", want.id,
				command.Title, command.Hint, command.Order, want.title, want.hint, want.order)
		}
	}
	count := 0
	for _, command := range commandsOf(h) {
		if strings.HasPrefix(command.ID, "zeokeel.session.") {
			count++
		}
	}
	if count != 15 {
		t.Fatalf("session commands = %d, want 15", count)
	}
}

func TestStopCommandCancelsRunningSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "运行", Running: true}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	stop := commandOf(t, h, "zeokeel.session.stop")
	if stop.Enabled == nil || !stop.Enabled() {
		t.Fatal("stop command is disabled while the session is running")
	}
	if err := stop.Run(); err != nil {
		t.Fatalf("stop Run: %v", err)
	}
	if ids := chat.cancels(); len(ids) != 1 || ids[0] != "sess-1" {
		t.Fatalf("Cancel calls = %v, want sess-1", ids)
	}
	tester.Frame()
	// The stop result is a transient toast (no resident status badge).
	h.assertNoTexts(tester, "已停止", "停止失败")
}

func TestStopCommandDisabledAndError(t *testing.T) {
	idle := newAttachmentHarness(t, newAttachmentChat())
	idleCmd := commandOf(t, idle, "zeokeel.session.stop")
	if idleCmd.Enabled != nil && idleCmd.Enabled() {
		t.Fatal("stop command is enabled without a running session")
	}

	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "运行", Running: true}, true)
	chat.setCancelError(errors.New("cancel boom"))
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	stop := commandOf(t, h, "zeokeel.session.stop")
	if err := stop.Run(); err != nil {
		t.Fatalf("stop Run: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "停止失败：cancel boom")
}

func TestFileCommandStagesAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })
	chat := newAttachmentChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "附件"}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	attach := commandOf(t, h, "zeokeel.session.attach")
	if attach.Enabled != nil && !attach.Enabled() {
		t.Fatal("attach command is disabled for an attachment-capable backend")
	}
	if err := attach.Run(); err != nil {
		t.Fatalf("attach Run: %v", err)
	}
	// The picker runs off the UI thread: its staged result arrives on a later
	// frame through UIRuntime.Post.
	h.waitFor(tester, "note.txt")

	// Cancellation is a no-op.
	setAttachmentPicker(t, func() ([]string, error) { return nil, nil })
	if err := attach.Run(); err != nil {
		t.Fatalf("cancelled attach Run: %v", err)
	}
	h.settle(tester)
	h.assertTexts(tester, "note.txt")
}

func TestFileCommandUnsupportedBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })
	chat := newFakeChat() // no AttachmentSender
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "附件"}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	attach := commandOf(t, h, "zeokeel.session.attach")
	if err := attach.Run(); err != nil {
		t.Fatalf("attach Run: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "当前后端不支持附件")
	h.assertNoTexts(tester, "note.txt")
}

func TestHelpModalListsCommandsAndShortcuts(t *testing.T) {
	h := newAttachmentHarness(t, newAttachmentChat())
	tester := h.tester()
	tester.Frame()
	help := commandOf(t, h, "zeokeel.session.help")
	if err := help.Run(); err != nil {
		t.Fatalf("help Run: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "帮助", "斜杠命令", "停止", "附加文件", "帮助",
		"快捷键", "Cmd/Ctrl + K", "打开命令面板", "Cmd/Ctrl + B",
		"Cmd/Ctrl + Alt + I", "切换右侧栏", "Enter", "发送消息")

	// The command list is the official List: named for assistive technology and
	// with its rows exposed by the command titles.
	if _, ok := tester.AccessByRole(ui.RoleList, "斜杠命令"); !ok {
		t.Fatalf("the help command list is not an accessible named list; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "帮助"); !ok {
		t.Fatalf("the help command list rows are not exposed by name; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "斜杠命令")
}

func TestHelpModalEnglishCopy(t *testing.T) {
	h := newAttachmentHarness(t, newAttachmentChat())
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	help := commandOf(t, h, "zeokeel.session.help")
	if err := help.Run(); err != nil {
		t.Fatalf("help Run: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Help", "Slash commands", "Keyboard shortcuts",
		"Open the command palette", "Toggle the inspector", "Close")
}

// TestHelpModalShortcutsFollowSettings covers R2-06: the help reference lists
// the product's desktop chords, and the two configurable ones render the live
// settings values (with the documented defaults when unset).
func TestHelpModalShortcutsFollowSettings(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	tester.Frame()
	help := findSessionCommand(t, h, "zeokeel.session.help")
	if err := help.Run(); err != nil {
		t.Fatalf("help Run: %v", err)
	}
	tester.Frame()

	// Defaults plus the fixed chords the host and composer register.
	for _, want := range []string{"Cmd/Ctrl + N", "Cmd/Ctrl + ,", "Cmd/Ctrl + K",
		"Cmd/Ctrl + B", "Cmd/Ctrl + Alt + I", "新会话", "设置", "发送消息"} {
		requireSessionText(t, tester, want)
	}

	// A customized chord is rendered as the product's display label.
	settings.set(contracts.SettingsUIShortcutCommandPalette, "alt+k")
	settings.set(contracts.SettingsUIShortcutToggleSidebar, "ctrl+b")
	tester.Frame()
	requireSessionText(t, tester, "Alt + K")
	requireSessionText(t, tester, "Ctrl + B")
	requireSessionNoText(t, tester, "Cmd/Ctrl + K")
	requireSessionNoText(t, tester, "Cmd/Ctrl + B")
}

// requireSessionText fails when the frame does not show text.
func requireSessionText(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	if !tester.HasText(text) {
		t.Fatalf("frame does not show %q; texts = %v", text, tester.Texts())
	}
}

// requireSessionNoText fails when the frame shows text.
func requireSessionNoText(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	if tester.HasText(text) {
		t.Fatalf("frame still shows %q; texts = %v", text, tester.Texts())
	}
}
