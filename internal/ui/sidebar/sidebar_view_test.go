package sidebar_test

// Tests for the sidebar's one fixed view: there is no view menu (the 视图
// popover and its sort/group settings are gone), workspace grouping is always
// on when the workspace backend exists, sessions always sort by recent
// activity with pinned-first inside a group, and the only fallback is the flat
// list for a host without a workspace backend.

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

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

func sessionAt(id, title string, updated time.Time) contracts.ChatSessionInfo {
	return contracts.ChatSessionInfo{ID: id, Title: title, UpdatedAt: updated}
}

// TestSidebarHasNoViewMenu pins the removal of the 视图 popover and its sort
// and grouping choices in both languages.
func TestSidebarHasNoViewMenu(t *testing.T) {
	settings := newSidebarSettings(nil)
	workspace := newFakeWorkspace(treeWorkspaces()...)
	h := newHarnessWithSettingsAndWorkspace(t, treeChat(), settings, workspace, sidebar.Config{})
	tester := h.tester()

	// "分组" alone would match the "未分组" group name, so the retired menu's
	// grouping choices are pinned by their own labels.
	h.assertNoTexts(tester, "视图", "排序", "最近活动", "名称", "按工作区", "平铺")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertNoTexts(tester, "View", "Sort", "Recent activity", "Name", "Group", "By workspace", "Flat")
	// The fixed product labels survive the language switch.
	h.assertTexts(tester, "Workspace", "Add workspace")
}

// TestSidebarAlwaysGroupsByWorkspace ignores stale view settings: even a store
// that still holds retired sort/group keys gets the workspace tree sorted by
// recent activity, pinned-first inside each group.
func TestSidebarAlwaysGroupsByWorkspace(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	settings := newSidebarSettings(map[string]string{
		"sidebar.sort":  "name",
		"sidebar.group": "flat",
	})
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s-old", Title: "Alpha 旧", WorkspaceID: "w1", UpdatedAt: base.Add(-3 * time.Hour)},
		{ID: "s-pin", Title: "Zulu 置顶", WorkspaceID: "w1", Pinned: true, UpdatedAt: base.Add(-4 * time.Hour)},
		{ID: "s-new", Title: "Beta 新", WorkspaceID: "w2", UpdatedAt: base},
	}}
	h := newHarnessWithSettingsAndWorkspace(t, chat, settings, workspace, sidebar.Config{})
	tester := h.tester()

	// Grouping wins over the stale "flat" value.
	h.assertTexts(tester, "项目一", "项目二")
	texts := tester.Texts()
	// Recent-activity group order: 项目二 (newest session) above 项目一.
	two, one := textIndex(texts, "项目二"), textIndex(texts, "项目一")
	if !(two >= 0 && one >= 0 && two < one) {
		t.Fatalf("group order = 项目二:%d 项目一:%d; want recent activity first; texts = %v", two, one, texts)
	}
	// Name sorting does not apply: inside 项目一 the pinned Zulu row comes
	// before the unpinned Alpha row even though Alpha sorts first by name.
	pinned, alpha := textIndex(texts, "Zulu 置顶"), textIndex(texts, "Alpha 旧")
	if !(pinned >= 0 && alpha >= 0 && pinned < alpha) {
		t.Fatalf("pinned-first order lost: pinned=%d alpha=%d; texts = %v", pinned, alpha, texts)
	}
	// Reading frames never rewrites the retired settings rows.
	if got := settings.get("sidebar.sort"); got != "name" {
		t.Fatalf("a frame overwrote the stale sidebar.sort to %q", got)
	}
	if got := settings.get("sidebar.group"); got != "flat" {
		t.Fatalf("a frame overwrote the stale sidebar.group to %q", got)
	}
}

// TestSidebarFlatFallbackRecentAndPinned covers the only fallback: without a
// workspace backend the rows are one flat list, still recent-first and
// pinned-first, with no view menu to change that.
func TestSidebarFlatFallbackRecentAndPinned(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		sessionAt("s-old", "Old", base.Add(-2*time.Hour)),
		{ID: "s-pin", Title: "Pinned old", Pinned: true, UpdatedAt: base.Add(-3 * time.Hour)},
		sessionAt("s-new", "Newest", base),
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "会话", "Old", "Pinned old", "Newest")
	h.assertNoTexts(tester, "视图", "排序", "分组", "工作区", "未分组")
	order := orderOf(tester.Texts(), "Pinned old", "Newest", "Old")
	if order[0] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("flat fallback order = %v; want pinned first then recent; texts = %v",
			order, tester.Texts())
	}
}
