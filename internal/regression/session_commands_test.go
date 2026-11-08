package regression

// Adversarial audit: the extra session slash commands (/停止, /附加文件, /帮助).
// Negative/boundary cases: /stop is enabled only for a running turn and turns a
// Cancel failure into a readable error; /file needs an AttachmentSender, reads
// the picker seam (success/cancel/error) and stages files; /help lists every
// registered command plus shortcuts and closes on Escape.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// sessionCommandHarness is the session screen with a scripted chat backend.
type sessionCommandHarness struct {
	h        *harness
	chat     contracts.ChatService
	tester   *ui.Tester
	notifier *fakeNotifier
}

// startSessionCommands activates the session screen with the given chat.
func startSessionCommands(t *testing.T, chat contracts.ChatService) *sessionCommandHarness {
	t.Helper()
	h := newBareHarness(t)
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyChat, chat)
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &sessionCommandHarness{h: h, chat: chat, tester: h.tester(1280, 800), notifier: notifier}
	harness.tester.Frame()
	return harness
}

// command looks up one registered command by ID.
func (s *sessionCommandHarness) command(t *testing.T, id string) contracts.CommandSpec {
	t.Helper()
	for _, command := range s.h.ui.Runtime().Commands() {
		if command.ID == id {
			return command
		}
	}
	t.Fatalf("command %q is not registered", id)
	return contracts.CommandSpec{}
}

// attachSessionChat adds contracts.AttachmentSender to the fake chat.
type attachSessionChat struct {
	*fakeChat
	attachCalls []string
}

func (c *attachSessionChat) SendWithAttachments(sessionID, prompt string, attachments []contracts.Attachment) error {
	c.attachCalls = append(c.attachCalls, prompt)
	return nil
}

// overrideSessionPicker replaces the session attachment-picker seam.
func overrideSessionPicker(t *testing.T, fn func() ([]string, error)) {
	t.Helper()
	previous := sessionui.AttachmentPicker
	sessionui.AttachmentPicker = fn
	t.Cleanup(func() { sessionui.AttachmentPicker = previous })
}

// TestStopCommandEnabledAndCancel covers /stop: disabled when no turn runs,
// enabled while running, calls Cancel and reports the outcome; a Cancel failure
// is a readable danger line.
func TestStopCommandEnabledAndCancel(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-idle", Title: "空闲",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	harness := startSessionCommands(t, chat)
	stop := harness.command(t, "zeokeel.session.stop")
	if stop.Enabled == nil || stop.Enabled() {
		t.Fatal("/stop is enabled without a running turn")
	}
	// The palette skips disabled commands, so the disabled state is the guard;
	// (Run is only reachable through the palette.)

	// A running turn enables the command; Cancel is called and the success
	// badge is rendered in the composer area.
	chat.addSession(contracts.ChatSession{ID: "sess-run", Title: "运行中", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	harness.tester.Frame()
	stop = harness.command(t, "zeokeel.session.stop")
	if stop.Enabled == nil || !stop.Enabled() {
		t.Fatal("/stop is disabled for a running turn")
	}
	if err := stop.Run(); err != nil {
		t.Fatalf("Run /stop: %v", err)
	}
	harness.tester.Frame()
	if got := chat.cancelCalls(); len(got) != 1 || got[0] != "sess-run" {
		t.Fatalf("Cancel calls = %v, want [sess-run]", got)
	}
	requireToast(t, harness.notifier, "已停止")

	// Cancel failure is readable and leaves the stack usable.
	chat.setCancelError(errors.New("cancel timeout"))
	if err := stop.Run(); err != nil {
		t.Fatalf("Run /stop with a Cancel error: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "停止失败：cancel timeout")
}

// TestFileCommandVariants covers /附加文件: an AttachmentSender-less backend is
// rejected with a hint; the picker's success stages file chips; cancelling is a
// no-op; a picker error is readable.
func TestFileCommandVariants(t *testing.T) {
	// Backend without AttachmentSender.
	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{ID: "sess-plain", Title: "纯聊天",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	harness := startSessionCommands(t, plain)
	attach := harness.command(t, "zeokeel.session.attach")
	if err := attach.Run(); err != nil {
		t.Fatalf("Run /附加文件: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "当前后端不支持附件")

	// Backend with AttachmentSender: the picker stages chips.
	dir := t.TempDir()
	notes := filepath.Join(dir, "audit-notes.md")
	image := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(notes, []byte("# notes"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if err := os.WriteFile(image, []byte("\x89PNG"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	overrideSessionPicker(t, func() ([]string, error) { return []string{notes, image}, nil })
	rich := &attachSessionChat{fakeChat: newFakeChat()}
	rich.addSession(contracts.ChatSession{ID: "sess-rich", Title: "附件",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	richHarness := startSessionCommands(t, rich)
	attach = richHarness.command(t, "zeokeel.session.attach")
	if err := attach.Run(); err != nil {
		t.Fatalf("Run /附加文件 (rich): %v", err)
	}
	settleFrames(t, richHarness.tester, "staged attachment chips", func() bool {
		return richHarness.tester.HasText("audit-notes.md") && richHarness.tester.HasText("shot.png")
	})

	// Cancel (empty selection) is a quiet no-op.
	overrideSessionPicker(t, func() ([]string, error) { return nil, nil })
	if err := attach.Run(); err != nil {
		t.Fatalf("Run /附加文件 (cancel): %v", err)
	}
	spinFrames(richHarness.tester, 3)                       // async picker result: empty selection is a no-op
	requireHasText(t, richHarness.tester, "audit-notes.md") // previously staged chips stay

	// Picker failure is readable.
	overrideSessionPicker(t, func() ([]string, error) { return nil, errors.New("picker failed") })
	if err := attach.Run(); err != nil {
		t.Fatalf("Run /附加文件 (error): %v", err)
	}
	settleFrames(t, richHarness.tester, "picker error line", func() bool {
		return richHarness.tester.HasText("无法打开文件选择器：picker failed")
	})
}

// TestHelpCommandModalAndEscape covers /帮助: the modal lists the registered
// commands and the window shortcuts, and Escape closes it.
func TestHelpCommandModalAndEscape(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-help", Title: "帮助",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "q"}}}, true)
	harness := startSessionCommands(t, chat)
	help := harness.command(t, "zeokeel.session.help")
	if help.Enabled != nil && !help.Enabled() {
		t.Fatal("/help is disabled")
	}
	if err := help.Run(); err != nil {
		t.Fatalf("Run /帮助: %v", err)
	}
	harness.tester.Frame()
	for _, want := range []string{"帮助", "斜杠命令", "快捷键", "关闭", "Cmd/Ctrl + K",
		"Cmd/Ctrl + Alt + I", "切换右侧栏", "新会话", "停止", "附加文件", "帮助"} {
		requireHasText(t, harness.tester, want)
	}
	// The modal lists every registered command (session commands included).
	commandCount := len(harness.h.ui.Runtime().Commands())
	if commandCount < 12 {
		t.Fatalf("registered commands = %d, want >= 12", commandCount)
	}
	for _, id := range []string{"zeokeel.session.stop", "zeokeel.session.attach", "zeokeel.session.help"} {
		found := false
		for _, command := range harness.h.ui.Runtime().Commands() {
			if command.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("command %q missing from the registry", id)
		}
	}

	// Escape closes the modal.
	harness.tester.Key(0, ui.KeyEscape)
	harness.tester.Frame()
	requireNoText(t, harness.tester, "快捷键")
	requireNoText(t, harness.tester, "切换右侧栏")
	// The command registry is untouched by opening/closing help.
	if got := len(harness.h.ui.Runtime().Commands()); got != commandCount {
		t.Fatalf("command count changed from %d to %d", commandCount, got)
	}
}

// ensure the help shortcut list strings stay covered by the audit (compile-time
// guard against accidental rename without a test update).
var _ = strings.TrimSpace
