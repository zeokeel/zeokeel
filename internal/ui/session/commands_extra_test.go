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
	h.assertTexts(tester, "已停止")
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
	tester.Frame()
	h.assertTexts(tester, "note.txt")

	// Cancellation is a no-op.
	setAttachmentPicker(t, func() ([]string, error) { return nil, nil })
	if err := attach.Run(); err != nil {
		t.Fatalf("cancelled attach Run: %v", err)
	}
	tester.Frame()
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
		"快捷键", "Cmd/Ctrl + K", "打开命令面板", "Cmd/Ctrl + B", "Esc 关闭")

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
		"Open the command palette", "Esc to close", "Close")
}
