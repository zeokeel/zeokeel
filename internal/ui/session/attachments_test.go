package session_test

// Tests for the composer attachments and the user-message attachment chips:
// the picker seam, staging/removal, size and read errors, SendWithAttachments,
// the unsupported-backend error and the read-only chips of a sent message.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// tinyPNG is a valid 1x1 PNG for thumbnail decoding.
var tinyPNG = func() []byte {
	data, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	return data
}()

// attachmentChat is a fakeChat that also implements contracts.AttachmentSender
// and records the attachment sends.
type attachmentChat struct {
	*fakeChat
	callMu   sync.Mutex
	calls    []attachmentSendCall
	sendErr  error
	parallel bool
}

type attachmentSendCall struct {
	sessionID   string
	prompt      string
	attachments []contracts.Attachment
}

func newAttachmentChat() *attachmentChat {
	return &attachmentChat{fakeChat: newFakeChat()}
}

func (c *attachmentChat) SendWithAttachments(sessionID, prompt string, attachments []contracts.Attachment) error {
	copied := make([]contracts.Attachment, len(attachments))
	for index, attachment := range attachments {
		copied[index] = attachment
		copied[index].Data = append([]byte(nil), attachment.Data...)
	}
	c.callMu.Lock()
	c.calls = append(c.calls, attachmentSendCall{sessionID: sessionID, prompt: prompt, attachments: copied})
	err := c.sendErr
	c.callMu.Unlock()
	if err != nil {
		return err
	}

	// Mirror fakeChat.Send's read-model update, with attachments on the message.
	c.fakeChat.mu.Lock()
	stored, ok := c.fakeChat.sessions[sessionID]
	if !ok {
		c.fakeChat.mu.Unlock()
		return fmt.Errorf("fake: unknown session %q", sessionID)
	}
	stored.Running = true
	stored.Messages = append(stored.Messages, contracts.ChatMessage{
		ID:          fmt.Sprintf("m-%d", len(stored.Messages)+1),
		Role:        contracts.ChatRoleUser,
		Text:        prompt,
		Attachments: copied,
		CreatedAt:   time.Unix(1700000100, 0),
	})
	c.fakeChat.sessions[sessionID] = stored
	c.fakeChat.mu.Unlock()
	c.fakeChat.emit(sessionID)
	return nil
}

func (c *attachmentChat) sentWithAttachments() []attachmentSendCall {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	return append([]attachmentSendCall(nil), c.calls...)
}

// --- harness ----------------------------------------------------------------

type attachmentHarness struct {
	t  *testing.T
	ui *uiroot.Root
}

func newAttachmentHarness(t *testing.T, chat contracts.ChatService) *attachmentHarness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	activate(t, ctx, session.Module(), session.Config{})
	return &attachmentHarness{t: t, ui: uiRoot}
}

func (h *attachmentHarness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.ui.View, 1200, 800)
}

func (h *attachmentHarness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// clickExact clicks the first element whose text or label equals label exactly
// (Tester.Click matches substrings, which is ambiguous in menu frames).
func (h *attachmentHarness) clickExact(tester *ui.Tester, label string) {
	h.t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		h.t.Fatalf("no element shows exactly %q; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// attachFiles opens the composer's single 附件 menu and chooses the native
// picker item. The picker itself runs off the UI thread: callers keep waiting on
// frames for the staged result.
func (h *attachmentHarness) attachFiles(tester *ui.Tester) {
	h.t.Helper()
	h.click(tester, "附件")
	h.click(tester, "选择文件…")
}

// attachManualPaths opens the 附件 menu and chooses the manual path-entry item.
func (h *attachmentHarness) attachManualPaths(tester *ui.Tester) {
	h.t.Helper()
	h.click(tester, "附件")
	h.click(tester, "手动输入路径…")
}

func (h *attachmentHarness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func (h *attachmentHarness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame unexpectedly shows %q; texts = %v", text, tester.Texts())
		}
	}
}

// waitFor drives frames until every wanted text is visible. The native picker
// runs off the UI thread and publishes through UIRuntime.Post, so its result
// lands on a later frame than the click.
func (h *attachmentHarness) waitFor(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		missing := ""
		for _, want := range wanted {
			if !tester.HasText(want) {
				missing = want
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("frame does not show %q (wanted %v); texts = %v",
				missing, wanted, tester.Texts())
		}
		time.Sleep(2 * time.Millisecond)
		tester.Frame()
	}
}

// settle drives frames for a short while so background work that will never
// publish (a cancelled picker) has had its chance to post before an absence is
// asserted.
func (h *attachmentHarness) settle(tester *ui.Tester) {
	h.t.Helper()
	for i := 0; i < 25; i++ {
		time.Sleep(2 * time.Millisecond)
		tester.Frame()
	}
}

// setAttachmentPicker replaces the package seam for one test and restores it.
func setAttachmentPicker(t *testing.T, fn func() ([]string, error)) {
	t.Helper()
	previous := session.AttachmentPicker
	session.AttachmentPicker = fn
	t.Cleanup(func() { session.AttachmentPicker = previous })
}

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func activeSessionChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "附件会话"}, true)
	return chat
}

// --- tests ------------------------------------------------------------------

func TestAttachmentStagingAndRemoval(t *testing.T) {
	dir := t.TempDir()
	textPath := writeTestFile(t, dir, "note.txt", []byte("hello attachment"))
	imagePath := writeTestFile(t, dir, "pic.png", tinyPNG)
	setAttachmentPicker(t, func() ([]string, error) {
		return []string{textPath, imagePath}, nil
	})

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	h.attachFiles(tester)
	h.waitFor(tester, "note.txt", "pic.png", "16 B")

	// Removing one chip drops only that attachment.
	if err := tester.Click("移除附件 note.txt"); err != nil {
		t.Fatalf("click remove: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "note.txt")
	h.assertTexts(tester, "pic.png")
}

func TestAttachmentPickerErrors(t *testing.T) {
	t.Run("picker error", func(t *testing.T) {
		setAttachmentPicker(t, func() ([]string, error) { return nil, fmt.Errorf("dialog failed") })
		h := newAttachmentHarness(t, activeSessionChat())
		tester := h.tester()
		h.attachFiles(tester)
		h.waitFor(tester, "无法打开文件选择器：dialog failed")
	})

	t.Run("missing file", func(t *testing.T) {
		setAttachmentPicker(t, func() ([]string, error) {
			return []string{filepath.Join(t.TempDir(), "missing.txt")}, nil
		})
		h := newAttachmentHarness(t, activeSessionChat())
		tester := h.tester()
		h.attachFiles(tester)
		h.waitFor(tester, "无法添加 missing.txt：不是可读的普通文件")
	})

	t.Run("too large", func(t *testing.T) {
		dir := t.TempDir()
		bigPath := filepath.Join(dir, "huge.bin")
		file, err := os.Create(bigPath)
		if err != nil {
			t.Fatalf("create big file: %v", err)
		}
		if err := file.Truncate((10 << 20) + 1); err != nil {
			_ = file.Close()
			t.Fatalf("truncate: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		setAttachmentPicker(t, func() ([]string, error) { return []string{bigPath}, nil })
		h := newAttachmentHarness(t, activeSessionChat())
		tester := h.tester()
		h.attachFiles(tester)
		h.waitFor(tester, "huge.bin 超过 10 MB 附件上限")
	})
}

func TestSendWithAttachments(t *testing.T) {
	dir := t.TempDir()
	textPath := writeTestFile(t, dir, "note.txt", []byte("hello attachment"))
	imagePath := writeTestFile(t, dir, "pic.png", tinyPNG)
	setAttachmentPicker(t, func() ([]string, error) {
		return []string{textPath, imagePath}, nil
	})

	chat := newAttachmentChat()
	chat.fakeChat.addSession(contracts.ChatSession{ID: "sess-1", Title: "附件会话"}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	h.attachFiles(tester)
	h.waitFor(tester, "note.txt", "pic.png")

	h.click(tester, "输入消息…")
	tester.Type("看看这些文件")
	tester.Frame()
	h.click(tester, "发送")
	tester.Frame()

	calls := chat.sentWithAttachments()
	if len(calls) != 1 {
		t.Fatalf("SendWithAttachments calls = %v, want one", calls)
	}
	call := calls[0]
	if call.sessionID != "sess-1" || call.prompt != "看看这些文件" {
		t.Fatalf("call = %+v, want sess-1/看看这些文件", call)
	}
	if len(call.attachments) != 2 {
		t.Fatalf("attachments = %+v, want two", call.attachments)
	}
	if call.attachments[0].Name != "note.txt" || call.attachments[0].MIME != "text/plain" ||
		string(call.attachments[0].Data) != "hello attachment" {
		t.Fatalf("text attachment = %+v", call.attachments[0])
	}
	if call.attachments[1].Name != "pic.png" || call.attachments[1].MIME != "image/png" ||
		!call.attachments[1].IsImage() || len(call.attachments[1].Data) == 0 {
		t.Fatalf("image attachment = %+v", call.attachments[1])
	}
	if strings.TrimSpace(call.attachments[0].ID) == "" {
		t.Fatal("attachment has no generated ID")
	}

	// The staged chips were cleared and the sent user message shows its chips.
	h.assertTexts(tester, "看看这些文件", "note.txt", "pic.png", "运行中")
	if got := chat.sends(); len(got) != 0 {
		t.Fatalf("plain Send calls = %v, want none (attachments use SendWithAttachments)", got)
	}
}

func TestSendAttachmentOnlyByEnter(t *testing.T) {
	dir := t.TempDir()
	textPath := writeTestFile(t, dir, "note.md", []byte("# note"))
	setAttachmentPicker(t, func() ([]string, error) { return []string{textPath}, nil })

	chat := newAttachmentChat()
	chat.fakeChat.addSession(contracts.ChatSession{ID: "sess-1", Title: "附件会话"}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	h.attachFiles(tester)
	h.waitFor(tester, "note.md", "6 B")

	// An empty draft with a staged attachment is still sendable (Enter too).
	h.click(tester, "输入消息…")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	calls := chat.sentWithAttachments()
	if len(calls) != 1 || calls[0].prompt != "" {
		t.Fatalf("attachment-only send calls = %+v, want one empty prompt", calls)
	}
	h.assertTexts(tester, "note.md")
}

func TestSendAttachmentsUnsupportedBackend(t *testing.T) {
	dir := t.TempDir()
	textPath := writeTestFile(t, dir, "note.txt", []byte("hi"))
	setAttachmentPicker(t, func() ([]string, error) { return []string{textPath}, nil })

	chat := activeSessionChat() // *fakeChat does not implement AttachmentSender
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	h.attachFiles(tester)
	h.waitFor(tester, "note.txt")
	h.click(tester, "发送")
	tester.Frame()

	h.assertTexts(tester, "当前后端不支持附件", "note.txt")
	if got := chat.sends(); len(got) != 0 {
		t.Fatalf("Send calls = %v, want none", got)
	}
}

func TestUserMessageAttachmentChips(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "附件消息",
		Messages: []contracts.ChatMessage{{
			ID:   "m1",
			Role: contracts.ChatRoleUser,
			Text: "", // attachments-only message
			Attachments: []contracts.Attachment{
				{ID: "att-1", Name: "report.md", MIME: "text/plain", Data: []byte("hello")},
				{ID: "att-2", Name: "chart.png", MIME: "image/png", Data: tinyPNG},
			},
			CreatedAt: time.Unix(1700000000, 0),
		}},
	}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "report.md", "5 B", "chart.png")
	h.assertNoTexts(tester, "当前后端不支持附件")
}
