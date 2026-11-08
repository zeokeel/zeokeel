package sidebar_test

// Tests for archived sessions: hidden by default, shown (and sorted last) when
// the footer view option is on, the ⋯ menu archive/unarchive action, the
// "已归档" row badge and the per-group counts.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// archiveChat is a fakeChat that also implements contracts.SessionArchiver and
// updates the read model so the next frame reflects the new archive state.
type archiveChat struct {
	*fakeChat
	mu         sync.Mutex
	archives   []archiveCall
	archiveErr error
}

type archiveCall struct {
	id       string
	archived bool
}

func newArchiveChat(sessions ...contracts.ChatSessionInfo) *archiveChat {
	return &archiveChat{fakeChat: &fakeChat{sessions: sessions}}
}

func (c *archiveChat) SetSessionArchived(sessionID string, archived bool) error {
	c.mu.Lock()
	c.archives = append(c.archives, archiveCall{id: sessionID, archived: archived})
	err := c.archiveErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.fakeChat.mu.Lock()
	defer c.fakeChat.mu.Unlock()
	for index := range c.fakeChat.sessions {
		if c.fakeChat.sessions[index].ID == sessionID {
			c.fakeChat.sessions[index].Archived = archived
		}
	}
	return nil
}

func (c *archiveChat) archiveCalls() []archiveCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]archiveCall(nil), c.archives...)
}

// sidebarSettings is a contracts.SettingsService backed by a map.
type sidebarSettings struct {
	mu     sync.Mutex
	values map[string]string
	sets   []string
}

func newSidebarSettings(values map[string]string) *sidebarSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &sidebarSettings{values: values}
}

func (s *sidebarSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *sidebarSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	s.sets = append(s.sets, key+"="+value)
	return nil
}

func (s *sidebarSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

func (s *sidebarSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func archivedSession(id, title string) contracts.ChatSessionInfo {
	return contracts.ChatSessionInfo{ID: id, Title: title, Archived: true}
}

// --- tests ------------------------------------------------------------------

func TestArchivedSessionsNeverAppearInSidebar(t *testing.T) {
	// Even with the legacy settings row set, archived sessions stay out of the
	// sidebar: the settings surface's archive page owns them now.
	for _, settings := range []*sidebarSettings{
		newSidebarSettings(nil),
		newSidebarSettings(map[string]string{contracts.SettingSessionsShowArchived: "true"}),
	} {
		chat := newArchiveChat(
			contracts.ChatSessionInfo{ID: "s1", Title: "普通会话"},
			archivedSession("s2", "归档会话"),
		)
		h := newHarnessWithSettings(t, chat, settings, sidebar.Config{})
		tester := h.tester()
		h.assertTexts(tester, "普通会话")
		h.assertNoTexts(tester, "归档会话", "已归档", "显示已归档", "隐藏已归档")
	}
}

func TestNoArchivedViewToggleInSidebar(t *testing.T) {
	chat := newArchiveChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	h := newHarnessWithSettings(t, chat, newSidebarSettings(nil), sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "设置")
	h.assertNoTexts(tester, "显示已归档", "隐藏已归档", "Show archived", "Hide archived")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Settings")
	h.assertNoTexts(tester, "显示已归档", "隐藏已归档", "Show archived", "Hide archived")
}

func TestArchivedSessionsExcludedFromMessageSearch(t *testing.T) {
	chat := newArchiveChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "普通会话"},
		archivedSession("s2", "归档会话"),
	)
	store := &fakeStore{hits: []contracts.MessageSearchHit{
		hit("s1", "普通会话", "user", "可见命中片段", time.Unix(1700000000, 0)),
		hit("s2", "归档会话", "user", "归档命中片段", time.Unix(1700000001, 0)),
	}}
	h := newHarnessWithStore(t, chat, store, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("命中")
	tester.Frame()

	h.assertTexts(tester, "消息搜索", "可见命中片段")
	h.assertNoTexts(tester, "归档会话", "归档命中片段")
}

func TestArchiveActionHidesSessionAndRaisesUndoToast(t *testing.T) {
	notifier := &archiveNotifier{}
	chat := newArchiveChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	h := newHarnessWithNotifier(t, chat, notifier)
	tester := h.tester()
	openRowMenu(t, tester, "会话一")
	h.assertTexts(tester, "归档会话")
	h.click(tester, "归档会话")

	calls := chat.archiveCalls()
	if len(calls) != 1 || calls[0].id != "s1" || !calls[0].archived {
		t.Fatalf("SetSessionArchived calls = %+v, want s1/true", calls)
	}
	h.assertNoTexts(tester, "会话一")

	// The success feedback is a toast with an Undo action; invoking the action
	// restores the session.
	actions := notifier.actionCalls()
	if len(actions) != 1 {
		t.Fatalf("action toasts = %+v, want one archive notice", actions)
	}
	action := actions[0]
	if action.level != contracts.NotifySuccess || !strings.Contains(action.text, "会话一") {
		t.Fatalf("archive notice = %+v", action)
	}
	if action.actionLabel == "" || action.action == nil {
		t.Fatalf("archive notice has no undo action: %+v", action)
	}
	action.action()
	tester.Frame()
	h.assertTexts(tester, "会话一")
	calls = chat.archiveCalls()
	if len(calls) != 2 || calls[1].id != "s1" || calls[1].archived {
		t.Fatalf("undo calls = %+v, want s1/false", calls)
	}
}

func TestArchiveActionFallsBackToPlainToast(t *testing.T) {
	// A host notifier without the optional action half still gets the archive
	// feedback, as a plain success toast.
	notifier := &plainNotifier{}
	chat := newArchiveChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
	h := newHarnessWithNotifier(t, chat, notifier)
	tester := h.tester()
	openRowMenu(t, tester, "会话一")
	h.click(tester, "归档会话")

	if len(notifier.notifications) != 1 {
		t.Fatalf("plain notifications = %+v, want one", notifier.notifications)
	}
	if got := notifier.notifications[0].Level; got != contracts.NotifySuccess {
		t.Fatalf("notification level = %q, want success", got)
	}
	h.assertNoTexts(tester, "会话一")
}

func TestArchivedGroupCountsExcludeArchived(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := newArchiveChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "普通一", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s2", Title: "普通二", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s3", Title: "归档三", WorkspaceID: "w1", Archived: true},
	)
	h := newHarnessWithSettingsAndWorkspace(t, chat,
		newSidebarSettings(map[string]string{contracts.SettingSessionsShowArchived: "true"}), workspace, sidebar.Config{})
	tester := h.tester()
	// The workspace's count is the visible sessions only (2), and the archived
	// one never appears in the tree.
	h.assertTexts(tester, "2")
	h.assertNoTexts(tester, "归档三", "已归档")
}

func TestArchiveDegradesWithoutBackends(t *testing.T) {
	// No settings store and no SessionArchiver: archived sessions stay hidden
	// and the row menu has no archive item.
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "普通会话"},
		archivedSession("s2", "归档会话"),
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "普通会话")
	h.assertNoTexts(tester, "归档会话", "显示已归档", "隐藏已归档")
	openRowMenu(t, tester, "普通会话")
	h.assertNoTexts(tester, "归档会话", "取消归档")
}

// --- toast doubles ----------------------------------------------------------

// archiveNotifier is a host notifier with the optional ActionNotifier half: it
// records action toasts so the archive undo path can be asserted.
type archiveNotifier struct {
	mu      sync.Mutex
	actions []archiveActionCall
}

type archiveActionCall struct {
	level       string
	text        string
	actionLabel string
	action      func()
}

func (n *archiveNotifier) SetHandler(func(contracts.Notification)) {}
func (n *archiveNotifier) Notify(level, text string) contracts.Notification {
	return contracts.Notification{ID: "plain", Level: level, Text: text}
}
func (n *archiveNotifier) Dismiss(string) {}
func (n *archiveNotifier) NotifyAction(level, text, actionLabel string, action func()) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.actions = append(n.actions, archiveActionCall{level: level, text: text, actionLabel: actionLabel, action: action})
	return contracts.Notification{ID: "action", Level: level, Text: text}
}
func (n *archiveNotifier) actionCalls() []archiveActionCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]archiveActionCall(nil), n.actions...)
}

// plainNotifier is a host notifier without the optional action half.
type plainNotifier struct {
	notifications []contracts.Notification
}

func (n *plainNotifier) SetHandler(func(contracts.Notification)) {}
func (n *plainNotifier) Notify(level, text string) contracts.Notification {
	n.notifications = append(n.notifications, contracts.Notification{ID: "plain", Level: level, Text: text})
	return n.notifications[len(n.notifications)-1]
}
func (n *plainNotifier) Dismiss(string) {}
