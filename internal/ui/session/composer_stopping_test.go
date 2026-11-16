package session_test

// UX-06 (2026-11 audit, S8 stopping row): the main stop control shows a
// stopping state (停止中…, disabled, not re-triggerable) while the backend
// settles the cancel, and the 「已停止」 toast fires only after the settle —
// never while the reply still streams.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// stoppingChat is a running conversation whose fake backend settles the cancel
// later (the real agent's Cancel only cancels a context).
func stoppingChat() *fakeChat {
	chat := newFakeChat()
	chat.delayedCancel = true
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "停止中间态", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "写个函数"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在写…", Streaming: true},
		},
	}, true)
	return chat
}

// hasToast reports whether the notifier recorded a toast with the exact text.
func hasToast(notifier *fakeSessionNotifier, text string) bool {
	for _, toast := range notifier.toasts() {
		if toast.text == text {
			return true
		}
	}
	return false
}

// TestStopStoppingStateUntilSettle is the decisive UX-06 assertion: right after
// the stop click (run not settled) the control reads 停止中… and is disabled,
// and no 已停止 toast claims a finished stop; the toast arrives on the settle.
func TestStopStoppingStateUntilSettle(t *testing.T) {
	chat := stoppingChat()
	notifier := &fakeSessionNotifier{}
	h := newChatServiceHarness(t, chat, notifier)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "停止")

	h.click(tester, "停止")
	tester.Frame()

	s, _ := chat.Session("sess-1")
	if !s.Running {
		t.Fatal("precondition broken: the delayed-cancel fake settled immediately")
	}
	h.assertTexts(tester, "停止中…")
	if hasToast(notifier, "已停止") {
		t.Fatalf("UX-06: 已停止 toast fired while the reply still streams; toasts = %v", notifier.toasts())
	}
	// A second click on the disabled control must not re-trigger the cancel.
	if rect, ok := tester.Find("停止中…"); ok {
		tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
	}
	if got := len(chat.cancels()); got != 1 {
		t.Fatalf("UX-06: stop fired %d times, want 1 (S8: 不可重复触发)", got)
	}

	// The backend settles: the toast fires now, and the composer returns to idle.
	chat.settle("sess-1")
	tester.Frame()
	tester.Frame()
	if !hasToast(notifier, "已停止") {
		t.Fatalf("UX-06: no 已停止 toast after the settle; toasts = %v", notifier.toasts())
	}
	if _, ok := tester.Find("停止中…"); ok {
		t.Fatalf("stopping control still shown after the settle; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "发送")
}

// TestStopEscWhileStoppingDoesNotRetrigger covers the keyboard path: Esc stops
// once; a second Esc while stopping must not fire another cancel.
func TestStopEscWhileStoppingDoesNotRetrigger(t *testing.T) {
	chat := stoppingChat()
	notifier := &fakeSessionNotifier{}
	h := newChatServiceHarness(t, chat, notifier)
	tester := h.tester()
	tester.Frame()
	focusComposerForRunningTest(t, tester)

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertTexts(tester, "停止中…")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := len(chat.cancels()); got != 1 {
		t.Fatalf("Esc re-triggered the stop: cancels = %d, want 1", got)
	}
	if hasToast(notifier, "已停止") {
		t.Fatalf("已停止 toast before the settle; toasts = %v", notifier.toasts())
	}
}

// TestStopImmediateSettleToastsOnce is the control: a backend that settles
// synchronously still toasts exactly once (no duplicate from the frame-build
// fallback path).
func TestStopImmediateSettleToastsOnce(t *testing.T) {
	chat := newFakeChat() // Cancel settles immediately
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "立即停止", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "写个函数"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在写…", Streaming: true},
		},
	}, true)
	notifier := &fakeSessionNotifier{}
	h := newChatServiceHarness(t, chat, notifier)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "停止")
	tester.Frame()
	tester.Frame()

	count := 0
	for _, toast := range notifier.toasts() {
		if toast.text == "已停止" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("已停止 toast fired %d times, want exactly 1; toasts = %v", count, notifier.toasts())
	}
}
