package session_test

// Interaction fixes from the independent UI audit: the session menu must fit a
// narrow window, the attach menu must stay available while a turn runs, and the
// composer draft/attachments are per-conversation.

import (
	"path/filepath"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newAttachmentHarnessWithSettings activates the session module with a settings
// store provided before activation (the module captures it then) and returns the
// attachment-capable harness.
func newAttachmentHarnessWithSettings(t *testing.T, chat *attachmentChat, settings *permissionSettings) *attachmentHarness {
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
	provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	activate(t, ctx, session.Module(), session.Config{})
	return &attachmentHarness{t: t, ui: uiRoot}
}

// menuItemInside reports whether rect (a menu row's labelled text) lies fully
// inside a width x height window.
func menuItemInside(rect ui.Rect, width, height float32) bool {
	return rect.X >= 0 && rect.Y >= 0 && rect.X+rect.W <= width && rect.Y+rect.H <= height
}

func TestSessionMenuTailReachableInNarrowWindow(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"704x720", 704, 720}, {"1280x800", 1280, 800}} {
		t.Run(size.name, func(t *testing.T) {
			chat := newFakeChat()
			chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "菜单适配会话", Messages: []contracts.ChatMessage{
				{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
				{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "你好呀"},
			}}, true)
			h := newPermissionHarness(t, chat, newPermissionSettings(nil))
			tester := h.tester()
			tester.SetSize(size.w, size.h)
			// The composer width comes from the last laid-out frame.
			for i := 0; i < 3; i++ {
				tester.Frame()
			}

			h.openSessionMenu(tester)
			// The menu's last row must be inside the window (no reachable-by-
			// scrolling-only tail) and clickable.
			last, ok := tester.Find("打开行为设置…")
			if !ok {
				t.Fatalf("%s: last menu item missing; texts = %v", size.name, tester.Texts())
			}
			if !menuItemInside(last, float32(size.w), float32(size.h)) {
				t.Fatalf("%s: last menu item rect %+v escapes the window", size.name, last)
			}
			h.clickExact(tester, "打开行为设置…")
			if !h.ui.Navigator().SettingsOpen() {
				t.Fatalf("%s: last menu item is not clickable", size.name)
			}
		})
	}
}

func TestSessionMenuSubmenusReachableAndShort(t *testing.T) {
	chat := presetSessionChat(newPresetChat())
	h := newPresetsHarness(t, chat, presetStore(t))
	tester := h.tester()
	tester.SetSize(704, 720)
	for i := 0; i < 3; i++ {
		tester.Frame()
	}

	h.openSessionMenu(tester)
	// Persona and presets are submenu rows: the outer menu stays short.
	h.assertTexts(tester, "权限模式", "对话模式", "人设", "插入预设", "打开行为设置…")
	// Reasoning is not repeated in the session menu: the model chip owns it.
	h.assertNoTexts(tester, "推理等级")
	h.clickExact(tester, "人设")
	h.assertTexts(tester, "Reviewer", "Writer", "无")
	h.clickExact(tester, "Reviewer")
	tester.Frame()
	if calls := chat.selectedCalls(); len(calls) != 1 || calls[0] != "sess-1:p1" {
		t.Fatalf("SetSessionPreset calls = %v", calls)
	}

	// The preset submenu is reachable too.
	h.openSessionMenu(tester)
	h.clickExact(tester, "插入预设")
	h.assertTexts(tester, "简洁回答", "代码审查", "逐步推理")
	h.clickExact(tester, "代码审查")
	tester.Frame()
	// Choosing a preset closes the whole menu and keeps the content as a draft.
	if tester.HasText("插入预设") {
		t.Fatalf("menu still open after choosing a preset; texts = %v", tester.Texts())
	}
}

func TestRunningComposerKeepsAttachMenu(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "next-message.txt", []byte("next"))
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })

	chat := newAttachmentChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "运行中附件", Running: true}, true)
	settings := newPermissionSettings(map[string]string{contracts.SettingSendWhileRunning: "true"})
	h := newAttachmentHarnessWithSettings(t, chat, settings)
	tester := h.tester()
	tester.Frame()

	// The attach menu stays available while the turn runs (queue-while-running
	// is on, so the next message can be composed already).
	h.assertTexts(tester, "附件", "排队", "停止")
	h.attachFiles(tester)
	h.waitFor(tester, "next-message.txt")
	h.assertTexts(tester, "本轮只发送文字；附件会保留在输入框，随下一条消息发送")

	// Queue a text prompt: the queue carries text, the staged file must stay
	// staged in the composer for after the turn (not silently dropped).
	h.click(tester, "输入消息…")
	tester.Type("queued prompt")
	tester.Frame()
	h.click(tester, "排队")
	tester.Frame()
	h.assertTexts(tester, "已排队 1 条", "next-message.txt")
	h.assertNoTexts(tester, "queued prompt") // the draft moved to the queue

	// A second readback: the attachment chip is still removable/staged.
	h.assertTexts(tester, "移除附件 next-message.txt")
}

func TestComposerDraftsArePerConversation(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "only-a.txt", []byte("a"))
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })

	chat := newAttachmentChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "会话 A"}, true)
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "会话 B"}, false)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	totalSends := func() int { return len(chat.sends()) + len(chat.sentWithAttachments()) }
	sendFromComposer := func(sessionID string) {
		t.Helper()
		if err := chat.SetActiveSession(sessionID); err != nil {
			t.Fatalf("activate %s: %v", sessionID, err)
		}
		tester.Frame()
		h.click(tester, "输入消息…")
		tester.Key(0, ui.KeyEnter)
		tester.Frame()
	}

	// Conversation A: a draft plus a staged attachment.
	h.click(tester, "输入消息…")
	tester.Type("draft A")
	tester.Frame()
	h.attachFiles(tester)
	h.waitFor(tester, "only-a.txt")

	// Switch to B: A's staged attachment must not leak in, and pressing Enter
	// on B's empty composer must not send A's draft anywhere.
	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("activate B: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "only-a.txt")
	h.click(tester, "输入消息…")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := totalSends(); got != 0 {
		t.Fatalf("switching conversations sent a leaked draft (%d sends)", got)
	}

	// B gets its own draft.
	h.click(tester, "输入消息…")
	tester.Type("draft B")
	tester.Frame()

	// Back to A: its attachment is restored and Enter sends A's draft with it.
	sendFromComposer("sess-1")
	calls := chat.sentWithAttachments()
	if len(calls) != 1 || len(chat.sends()) != 0 {
		t.Fatalf("A sends = %+v plain = %+v, want one attachment send", calls, chat.sends())
	}
	if calls[0].prompt != "draft A" || len(calls[0].attachments) != 1 ||
		calls[0].attachments[0].Name != "only-a.txt" {
		t.Fatalf("A restored draft = %+v, want draft A + only-a.txt", calls[0])
	}

	// Back to B: only its own text comes back (no attachments).
	sendFromComposer("sess-2")
	if got := chat.sends(); len(got) != 1 || got[0].prompt != "draft B" {
		t.Fatalf("B restored draft sends = %+v, want plain draft B", got)
	}

	// A fresh conversation starts clean: Enter sends nothing.
	chat.addSession(contracts.ChatSession{ID: "sess-3", Title: "会话 C"}, false)
	sendFromComposer("sess-3")
	if got := totalSends(); got != 2 {
		t.Fatalf("fresh conversation sent a leaked draft (%d sends)", got)
	}
}

func TestAttachmentPickerResultStaysWithOriginConversation(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "late.txt", []byte("late"))
	release := make(chan struct{})
	setAttachmentPicker(t, func() ([]string, error) {
		<-release
		return []string{path}, nil
	})

	chat := newAttachmentChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "会话 A"}, true)
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "会话 B"}, false)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	// Open the picker in A, then switch to B while it is still open.
	h.attachFiles(tester)
	h.assertTexts(tester, "选择中…")
	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("activate B: %v", err)
	}
	tester.Frame()
	close(release)
	// Drive frames until the posted picker result lands.
	h.settle(tester)
	if tester.HasText("late.txt") {
		t.Fatalf("picker result staged into the newly opened conversation; texts = %v", tester.Texts())
	}

	// It belongs to A: switching back restores the staged file.
	if err := chat.SetActiveSession("sess-1"); err != nil {
		t.Fatalf("activate A: %v", err)
	}
	tester.Frame()
	h.waitFor(tester, "late.txt")
	if got := filepath.Base(path); got != "late.txt" {
		t.Fatalf("unexpected fixture path %q", got)
	}
}
