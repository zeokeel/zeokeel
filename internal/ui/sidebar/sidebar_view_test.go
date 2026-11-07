package sidebar_test

// Tests for the sidebar's sort/group view options.

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

func viewChat(sessions ...contracts.ChatSessionInfo) *fakeChat {
	return &fakeChat{sessions: sessions}
}

func sessionAt(id, title string, updated time.Time) contracts.ChatSessionInfo {
	return contracts.ChatSessionInfo{ID: id, Title: title, UpdatedAt: updated}
}

// orderOf returns the positions of the wanted labels in the frame texts.
func orderOf(testerTexts []string, names ...string) []int {
	indexes := make([]int, 0, len(names))
	for _, name := range names {
		found := -1
		for index, text := range testerTexts {
			if strings.TrimSpace(text) == name {
				found = index
				break
			}
		}
		indexes = append(indexes, found)
	}
	return indexes
}

func TestSidebarDefaultSortRecent(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := viewChat(
		sessionAt("old", "Old", base.Add(-2*time.Hour)),
		sessionAt("new", "Newest", base),
		sessionAt("mid", "Middle", base.Add(-time.Hour)),
	)
	h := newHarnessWithSettings(t, chat, newSidebarSettings(nil), sidebar.Config{})
	tester := h.tester()
	order := orderOf(tester.Texts(), "Newest", "Middle", "Old")
	if order[0] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("default order = %v; texts = %v", order, tester.Texts())
	}
}

func TestSidebarNameSortKeepsPinnedFirst(t *testing.T) {
	settings := newSidebarSettings(map[string]string{contracts.SettingSidebarSort: "name"})
	chat := viewChat(
		contracts.ChatSessionInfo{ID: "z", Title: "Zulu", Pinned: true},
		contracts.ChatSessionInfo{ID: "b", Title: "beta"},
		contracts.ChatSessionInfo{ID: "a", Title: "Alpha"},
	)
	h := newHarnessWithSettings(t, chat, settings, sidebar.Config{})
	tester := h.tester()
	order := orderOf(tester.Texts(), "Zulu", "Alpha", "beta")
	if order[0] < 0 || order[1] < 0 || order[2] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("name order = %v; texts = %v", order, tester.Texts())
	}
}

func TestSidebarFlatGroupHidesWorkspaceTitles(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).
		withCurrent(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})
	settings := newSidebarSettings(map[string]string{
		contracts.SettingSidebarGroup: "flat",
		contracts.SettingSidebarSort:  "name",
	})
	chat := viewChat(
		contracts.ChatSessionInfo{ID: "s2", Title: "Beta", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s1", Title: "Alpha", WorkspaceID: "w1"},
		contracts.ChatSessionInfo{ID: "s3", Title: "Gamma", WorkspaceID: ""},
	)
	h := newHarnessWithSettingsAndWorkspace(t, chat, settings, workspace, sidebar.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "项目一", "项目二", "未分组")
	order := orderOf(tester.Texts(), "Alpha", "Beta", "Gamma")
	if order[0] < 0 || order[1] < 0 || order[2] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("flat order = %v; texts = %v", order, tester.Texts())
	}
}

func TestSidebarViewMenuWritesSettings(t *testing.T) {
	settings := newSidebarSettings(nil)
	h := newHarnessWithSettings(t, viewChat(sessionAt("s1", "A", time.Now())), settings, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "视图")
	h.click(tester, "视图")
	h.assertTexts(tester, "排序", "最近活动", "名称", "分组", "按工作区", "平铺")
	h.click(tester, "名称")
	if got := settings.get(contracts.SettingSidebarSort); got != "name" {
		t.Fatalf("sidebar.sort = %q, want name", got)
	}
	h.click(tester, "视图")
	h.click(tester, "平铺")
	if got := settings.get(contracts.SettingSidebarGroup); got != "flat" {
		t.Fatalf("sidebar.group = %q, want flat", got)
	}
	// Switching back writes the default values too.
	h.click(tester, "视图")
	h.click(tester, "最近活动")
	h.click(tester, "视图")
	h.click(tester, "按工作区")
	if got := settings.get(contracts.SettingSidebarSort); got != "recent" {
		t.Fatalf("sidebar.sort after back = %q", got)
	}
	if got := settings.get(contracts.SettingSidebarGroup); got != "workspace" {
		t.Fatalf("sidebar.group after back = %q", got)
	}
}

func TestSidebarViewBadValuesFallBack(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	settings := newSidebarSettings(map[string]string{
		contracts.SettingSidebarSort:  "bogus",
		contracts.SettingSidebarGroup: "bogus",
	})
	chat := viewChat(sessionAt("s1", "Alpha", time.Unix(1700000000, 0)))
	h := newHarnessWithSettingsAndWorkspace(t, chat, settings, workspace, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "项目一", "项目二", "视图")
}

func TestSidebarViewEnglish(t *testing.T) {
	settings := newSidebarSettings(nil)
	h := newHarnessWithSettings(t, viewChat(sessionAt("s1", "A", time.Now())), settings, sidebar.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "View")
	h.click(tester, "View")
	h.assertTexts(tester, "Sort", "Recent activity", "Name", "Group", "By workspace", "Flat")
	h.click(tester, "Name")
	if got := settings.get(contracts.SettingSidebarSort); got != "name" {
		t.Fatalf("English sidebar.sort = %q", got)
	}
}
