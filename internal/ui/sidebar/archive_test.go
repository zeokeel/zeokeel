package sidebar_test

// Tests for archived sessions: hidden by default, shown (and sorted last) when
// the footer view option is on, the ⋯ menu archive/unarchive action, the
// "已归档" row badge and the per-group counts.

import (
	"sync"
	"testing"

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

func TestArchivedHiddenByDefault(t *testing.T) {
	chat := newArchiveChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "普通会话"},
		archivedSession("s2", "归档会话"),
	)
	settings := newSidebarSettings(nil)
	h := newHarnessWithSettings(t, chat, settings, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "普通会话", "显示已归档")
	h.assertNoTexts(tester, "归档会话")
	if got := countText(tester, "已归档"); got != 0 {
		t.Fatalf("已归档 badges = %d, want 0; texts = %v", got, tester.Texts())
	}
}

func TestArchivedShownAndSortedLast(t *testing.T) {
	settings := newSidebarSettings(map[string]string{
		contracts.SettingSessionsShowArchived: "true",
	})
	chat := newArchiveChat(
		archivedSession("s1", "归档一"),
		contracts.ChatSessionInfo{ID: "s2", Title: "置顶普通", Pinned: true},
		contracts.ChatSessionInfo{ID: "s3", Title: "普通二"},
		archivedSession("s4", "归档二"),
	)
	h := newHarnessWithSettings(t, chat, settings, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "归档一", "置顶普通", "普通二", "归档二", "已归档", "隐藏已归档")
	if got := countText(tester, "已归档"); got != 2 {
		t.Fatalf("已归档 badges = %d, want 2; texts = %v", got, tester.Texts())
	}
	texts := tester.Texts()
	pinned, plain, archivedOne, archivedTwo := textIndex(texts, "置顶普通"),
		textIndex(texts, "普通二"), textIndex(texts, "归档一"), textIndex(texts, "归档二")
	if !(pinned >= 0 && pinned < plain && plain < archivedOne && archivedOne < archivedTwo) {
		t.Fatalf("archive order wrong: pinned=%d plain=%d arch1=%d arch2=%d; texts = %v",
			pinned, plain, archivedOne, archivedTwo, texts)
	}
}

func TestArchiveMenuTogglesState(t *testing.T) {
	t.Run("archive", func(t *testing.T) {
		chat := newArchiveChat(contracts.ChatSessionInfo{ID: "s1", Title: "会话一"})
		h := newHarness(t, chat, sidebar.Config{})
		tester := h.tester()
		openRowMenu(t, tester)
		h.assertTexts(tester, "归档会话")
		h.click(tester, "归档会话")

		calls := chat.archiveCalls()
		if len(calls) != 1 || calls[0].id != "s1" || !calls[0].archived {
			t.Fatalf("SetSessionArchived calls = %+v, want s1/true", calls)
		}
		// Hidden again by default: the row and its badge disappear.
		h.assertNoTexts(tester, "会话一", "已归档")
	})

	t.Run("unarchive", func(t *testing.T) {
		settings := newSidebarSettings(map[string]string{
			contracts.SettingSessionsShowArchived: "true",
		})
		chat := newArchiveChat(archivedSession("s2", "归档会话"))
		h := newHarnessWithSettings(t, chat, settings, sidebar.Config{})
		tester := h.tester()
		h.assertTexts(tester, "归档会话", "已归档")
		openRowMenu(t, tester)
		h.assertTexts(tester, "取消归档")
		h.click(tester, "取消归档")

		calls := chat.archiveCalls()
		if len(calls) != 1 || calls[0].id != "s2" || calls[0].archived {
			t.Fatalf("SetSessionArchived calls = %+v, want s2/false", calls)
		}
		if got := countText(tester, "已归档"); got != 0 {
			t.Fatalf("已归档 badges = %d after unarchive, want 0; texts = %v", got, tester.Texts())
		}
	})
}

func TestArchivedFooterToggle(t *testing.T) {
	settings := newSidebarSettings(nil)
	chat := newArchiveChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "普通会话"},
		archivedSession("s2", "归档会话"),
	)
	h := newHarnessWithSettings(t, chat, settings, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "显示已归档")
	h.assertNoTexts(tester, "归档会话")

	h.click(tester, "显示已归档")
	if got := settings.get(contracts.SettingSessionsShowArchived); got != "true" {
		t.Fatalf("persisted showArchived = %q, want true", got)
	}
	h.assertTexts(tester, "隐藏已归档", "归档会话", "已归档")

	h.click(tester, "隐藏已归档")
	if got := settings.get(contracts.SettingSessionsShowArchived); got != "false" {
		t.Fatalf("persisted showArchived = %q, want false", got)
	}
	h.assertTexts(tester, "显示已归档")
	h.assertNoTexts(tester, "归档会话")
}

func TestArchivedGroupCountsFollowViewOption(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := newArchiveChat(
		contracts.ChatSessionInfo{ID: "s1", Title: "普通一", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s2", Title: "普通二", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s3", Title: "归档三", WorkspaceID: "w1", Archived: true},
	)

	hidden := newSidebarSettings(nil)
	h := newHarnessWithSettingsAndWorkspace(t, chat, hidden, workspace, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "2")
	h.assertNoTexts(tester, "归档三")

	shown := newSidebarSettings(map[string]string{
		contracts.SettingSessionsShowArchived: "true",
	})
	h2 := newHarnessWithSettingsAndWorkspace(t, chat, shown, workspace, sidebar.Config{})
	tester2 := h2.tester()
	h2.assertTexts(tester2, "3", "归档三", "已归档")
}

func TestArchiveDegradesWithoutBackends(t *testing.T) {
	// No settings store and no SessionArchiver: archives stay hidden, the
	// footer toggle is absent and the row menu has no archive item.
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "普通会话"},
		archivedSession("s2", "归档会话"),
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "普通会话")
	h.assertNoTexts(tester, "归档会话", "显示已归档", "隐藏已归档")
	if got := countText(tester, "已归档"); got != 0 {
		t.Fatalf("已归档 badges = %d, want 0; texts = %v", got, tester.Texts())
	}
	openRowMenu(t, tester)
	h.assertNoTexts(tester, "归档会话", "取消归档")
}
