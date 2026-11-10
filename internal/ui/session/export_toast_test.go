package session_test

// Tests (written before the implementation, TDD) for the session export toast
// wiring: the export actions also raise a host toast when a notifier exists.

import (
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeSessionNotifier records the toasts the session module raises.
type fakeSessionNotifier struct {
	mu            sync.Mutex
	notifications []sessionToast
	dismissed     []string
	// actions keeps the undo actions offered through contracts.ActionNotifier.
	actions []notifierAction
}

// notifierAction is one undo action a toast offered.
type notifierAction struct {
	level  string
	text   string
	label  string
	action func()
}

type sessionToast struct {
	level string
	text  string
}

func (f *fakeSessionNotifier) SetHandler(func(contracts.Notification)) {}

func (f *fakeSessionNotifier) Notify(level, text string) contracts.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifications = append(f.notifications, sessionToast{level: level, text: text})
	return contracts.Notification{ID: "notice-1", Level: level, Text: text}
}

func (f *fakeSessionNotifier) Dismiss(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissed = append(f.dismissed, id)
}

func (f *fakeSessionNotifier) toasts() []sessionToast {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sessionToast(nil), f.notifications...)
}

// newToastHarness builds the session screen with the optional notifier.
func newToastHarness(t *testing.T, chat *fakeChat, notifier contracts.Notifier) *harness {
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
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

func exportToastChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-export", Title: "导出会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	return chat
}

// --- tests ------------------------------------------------------------------

func TestExportSuccessRaisesSuccessToast(t *testing.T) {
	setExportSaver(t, func(title, name, content string) (string, error) { return "/tmp/" + name, nil })
	notifier := &fakeSessionNotifier{}
	h := newToastHarness(t, exportToastChat(), notifier)
	tester := h.tester()
	openConversationActions(t, h, tester)
	h.click(tester, "导出 Markdown")
	tester.Frame()

	toasts := notifier.toasts()
	if len(toasts) != 1 {
		t.Fatalf("toasts = %+v, want one success toast", toasts)
	}
	if toasts[0].level != contracts.NotifySuccess || toasts[0].text == "" {
		t.Fatalf("success toast = %+v, want a non-empty success notification", toasts[0])
	}
}

func TestExportFailureRaisesErrorToast(t *testing.T) {
	setExportSaver(t, func(title, name, content string) (string, error) { return "", errors.New("disk full") })
	notifier := &fakeSessionNotifier{}
	h := newToastHarness(t, exportToastChat(), notifier)
	tester := h.tester()
	openConversationActions(t, h, tester)
	h.click(tester, "导出 Markdown")
	tester.Frame()

	toasts := notifier.toasts()
	if len(toasts) != 1 {
		t.Fatalf("toasts = %+v, want one error toast", toasts)
	}
	if toasts[0].level != contracts.NotifyError || !strings.Contains(toasts[0].text, "disk full") {
		t.Fatalf("error toast = %+v, want an error notification with the failure", toasts[0])
	}
}

func TestExportJSONLSuccessRaisesSuccessToast(t *testing.T) {
	withJSONLSeam(t, func(title, name, content string) (string, error) { return "/tmp/" + name, nil })
	notifier := &fakeSessionNotifier{}
	h := newToastHarness(t, exportToastChat(), notifier)
	tester := h.tester()
	openConversationActions(t, h, tester)
	h.click(tester, "导出 JSONL")
	tester.Frame()

	toasts := notifier.toasts()
	if len(toasts) != 1 || toasts[0].level != contracts.NotifySuccess {
		t.Fatalf("JSONL success toast = %+v, want a success toast", toasts)
	}
}

func TestExportWithoutNotifierStillWorks(t *testing.T) {
	setExportSaver(t, func(title, name, content string) (string, error) { return "/tmp/" + name, nil })
	h := newToastHarness(t, exportToastChat(), nil)
	tester := h.tester()
	openConversationActions(t, h, tester)
	h.click(tester, "导出 Markdown")
	tester.Frame()
	// Without a notifier the export still succeeds; no resident badge appears.
	h.assertNoTexts(tester, "已导出", "导出失败")
}
