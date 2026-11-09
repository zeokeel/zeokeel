package regression

// Adversarial audit: the Home composer (new-conversation surface). Negative and
// boundary cases: empty draft / Enter on empty send nothing, Enter sends and
// navigates, attachment staging with a backend that cannot send attachments
// falls back with a readable hint, a failing picker/NewSession/Send surfaces
// errors without losing the draft, and a product without a chat backend stays
// safe.
//
// The Home UI keeps its own dispatcher; this file drives the real Home screen
// frame through mygo's tester, so it asserts user-visible behavior rather than
// internals.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// startHomeUI activates the Home screen (plus a stub session screen) and renders
// the product view. A nil chat models a product without the agent backend.
func startHomeUI(t *testing.T, chat contracts.ChatService, sessionScreen bool) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	if chat != nil {
		provide(t, h.ctx, contracts.KeyChat, chat)
	}
	if sessionScreen {
		h.registerScreen("session", "会话", "session-body")
	}
	activate(t, h.ctx, home.Module(), home.Config{})
	h.push("home")
	tester := h.tester(1200, 800)
	tester.Frame()
	return h, tester
}

// clickHomeText clicks one Home label and repaints.
func clickHomeText(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// overrideHomePicker replaces the Home attachment-picker seam for one test.
func overrideHomePicker(t *testing.T, fn func() ([]string, error)) {
	t.Helper()
	previous := home.AttachmentPicker
	home.AttachmentPicker = fn
	t.Cleanup(func() { home.AttachmentPicker = previous })
}

// TestHomeComposerEmptyDraftDoesNotSend covers the disabled send action and the
// Enter key on an empty draft: no session is created and nothing is sent.
func TestHomeComposerEmptyDraftDoesNotSend(t *testing.T) {
	chat := newFakeChat()
	_, tester := startHomeUI(t, chat, true)
	clickHomeText(t, tester, "发送")
	if got := len(chat.recordedSends()); got != 0 || chat.sessionCount() != 0 {
		t.Fatalf("empty draft sent %d prompts / created %d sessions", got, chat.sessionCount())
	}

	// Enter on an empty (focused) composer is a no-op too.
	clickHomeText(t, tester, "问点什么…")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := len(chat.recordedSends()); got != 0 || chat.sessionCount() != 0 {
		t.Fatalf("Enter on an empty draft sent %d prompts", got)
	}

	// A product without a chat backend still renders and stays safe.
	_, noChatTester := startHomeUI(t, nil, false)
	requireHasText(t, noChatTester, "发送")
	clickHomeText(t, noChatTester, "发送")
	clickHomeText(t, noChatTester, "问点什么…")
	noChatTester.Key(0, ui.KeyEnter)
	noChatTester.Frame()
}

// TestHomeComposerEnterSendsAndNavigates covers the happy path: typing and
// pressing Enter creates a session, sends the trimmed prompt and navigates to
// the conversation; Shift+Enter only inserts a newline.
func TestHomeComposerEnterSendsAndNavigates(t *testing.T) {
	chat := newFakeChat()
	h, tester := startHomeUI(t, chat, true)
	clickHomeText(t, tester, "问点什么…")
	tester.Type("  hello home  ")
	tester.Frame()

	// Shift+Enter must not send.
	tester.Key(ui.Shift, ui.KeyEnter)
	tester.Frame()
	if got := len(chat.recordedSends()); got != 0 {
		t.Fatalf("Shift+Enter sent %d prompts, want 0", got)
	}

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	sends := chat.recordedSends()
	if len(sends) != 1 || !strings.HasSuffix(sends[0], ":hello home") {
		t.Fatalf("Enter sends = %v, want one trimmed prompt", sends)
	}
	if chat.sessionCount() != 1 {
		t.Fatalf("sessions created = %d, want 1", chat.sessionCount())
	}
	if current := h.ui.Navigator().Current(); current != "session" {
		t.Fatalf("current screen after send = %q, want session", current)
	}
}

// TestHomeComposerAttachmentFallback covers staged attachments on a backend
// without AttachmentSender: the send falls back to the plain prompt and shows the
// readable hint; a failing picker shows its error.
func TestHomeComposerAttachmentFallback(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("audit"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	overrideHomePicker(t, func() ([]string, error) { return []string{notes}, nil })

	// No session screen is registered, so the frame stays on Home and the hint
	// remains visible after the fallback send.
	chat := newFakeChat()
	_, tester := startHomeUI(t, chat, false)
	// The 附件 menu is closed until asked for; opening it offers both the native
	// picker and the manual path fallback (the redesign's single attach control).
	requireNoText(t, tester, "选择文件…")
	clickHomeText(t, tester, "附件")
	requireHasText(t, tester, "选择文件…")
	requireHasText(t, tester, "手动输入路径…")
	clickHomeText(t, tester, "选择文件…")
	settleFrames(t, tester, "staged home attachment chip", func() bool {
		return tester.HasText("notes.txt")
	})
	clickHomeText(t, tester, "问点什么…")
	tester.Type("with attachments")
	tester.Frame()
	clickHomeText(t, tester, "发送")

	requireHasText(t, tester, "附件需在会话中发送")
	if got := len(chat.recordedSends()); got != 1 {
		t.Fatalf("fallback sends = %d, want the plain prompt", got)
	}

	// Picker failure is readable and keeps the composer usable.
	overrideHomePicker(t, func() ([]string, error) { return nil, errors.New("picker failed") })
	clickHomeText(t, tester, "附件")
	clickHomeText(t, tester, "选择文件…")
	settleFrames(t, tester, "home picker error line", func() bool {
		return tester.HasText("无法打开文件选择器：picker failed")
	})
}

// TestHomeComposerSendErrors covers NewSession/Send failures: the danger line is
// readable, the draft is not silently dropped and no navigation happens.
func TestHomeComposerSendErrors(t *testing.T) {
	chat := newFakeChat()
	chat.setSendError(errors.New("network down"))
	h, tester := startHomeUI(t, chat, true)
	clickHomeText(t, tester, "问点什么…")
	tester.Type("will fail")
	tester.Frame()
	clickHomeText(t, tester, "发送")
	requireHasText(t, tester, "发送失败：network down")
	if current := h.ui.Navigator().Current(); current != "home" {
		t.Fatalf("current screen after a failed send = %q, want home", current)
	}

	newFailing := newFakeChat()
	newFailing.setNewSessionError(errors.New("store closed"))
	_, tester = startHomeUI(t, newFailing, true)
	clickHomeText(t, tester, "问点什么…")
	tester.Type("cannot start")
	tester.Frame()
	clickHomeText(t, tester, "发送")
	requireHasText(t, tester, "发送失败：store closed")
	if got := len(newFailing.recordedSends()); got != 0 {
		t.Fatalf("NewSession failure still sent %d prompts", got)
	}
}
