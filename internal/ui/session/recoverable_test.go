package session_test

// Tests for the recoverable destructive actions (H14 + task-20): memory delete
// and clear offer Undo through contracts.ActionNotifier, and archiving a
// conversation reports success with Undo.

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// NotifyAction makes fakeSessionNotifier an ActionNotifier. The toast is still
// recorded as a plain notification, plus the undo action for the test to run.
func (f *fakeSessionNotifier) NotifyAction(level, text, actionLabel string, action func()) contracts.Notification {
	f.mu.Lock()
	f.notifications = append(f.notifications, sessionToast{level: level, text: text})
	f.actions = append(f.actions, notifierAction{level: level, text: text, label: actionLabel, action: action})
	f.mu.Unlock()
	return contracts.Notification{ID: "notice-action", Level: level, Text: text, ActionLabel: actionLabel}
}

// lastAction returns the most recent undo action a toast offered.
func (f *fakeSessionNotifier) lastAction() (notifierAction, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.actions) == 0 {
		return notifierAction{}, false
	}
	return f.actions[len(f.actions)-1], true
}

// archiveChat adds the optional SessionArchiver half to the fake chat.
type archiveChat struct {
	*fakeChat
	mu       sync.Mutex
	archived map[string]bool
	err      error
}

func (c *archiveChat) SetSessionArchived(sessionID string, archived bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if c.archived == nil {
		c.archived = make(map[string]bool)
	}
	c.archived[sessionID] = archived
	return nil
}

func (c *archiveChat) archivedFlag(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.archived[sessionID]
}

// newChatServiceHarness activates the session screen with any ChatService.
func newChatServiceHarness(t *testing.T, chat contracts.ChatService, notifier contracts.Notifier) *harness {
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
	return &harness{t: t, ctx: ctx, ui: uiRoot}
}

func TestArchiveConversationReportsSuccessWithUndo(t *testing.T) {
	base := newFakeChat()
	base.addSession(contracts.ChatSession{
		ID: "sess-archive", Title: "归档测试会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	chat := &archiveChat{fakeChat: base}
	notifier := &fakeSessionNotifier{}
	h := newChatServiceHarness(t, chat, notifier)
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	tester.Frame()
	h.assertTexts(tester, "归档会话")
	h.click(tester, "归档会话")
	if !chat.archivedFlag("sess-archive") {
		t.Fatal("archive action did not set the archive flag")
	}
	action, ok := notifier.lastAction()
	if !ok || action.text != "会话已归档" || action.label != "撤销" {
		t.Fatalf("archive toast = %+v ok=%v, want 会话已归档 with 撤销", action, ok)
	}

	// Undo restores the conversation to the sidebar list on the next frame.
	action.action()
	tester.Frame()
	if chat.archivedFlag("sess-archive") {
		t.Fatal("undo did not clear the archive flag")
	}
	_ = ui.KeyEscape
}
