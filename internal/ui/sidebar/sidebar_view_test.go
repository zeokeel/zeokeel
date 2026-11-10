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

// automationSession builds the chat session the automation module creates for
// one scheduled run: the run's provenance is carried explicitly by
// ChatSessionInfo.Origin (the title keeps the product's "自动化: <task>"
// convention, but the sidebar must never hide a session by its title).
func automationSession(id, task string, updated time.Time) contracts.ChatSessionInfo {
	return contracts.ChatSessionInfo{
		ID:        id,
		Title:     "自动化: " + task,
		Origin:    contracts.SessionOriginAutomation,
		UpdatedAt: updated,
	}
}

// TestSidebarFlatListHidesAutomationSessions pins the approved simplification:
// the flat session list shows user conversations only, so an automation run
// never renders a row (or a pinned badge) even while it is the newest session.
func TestSidebarFlatListHidesAutomationSessions(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	run := automationSession("s-run", "每日摘要", base)
	run.Pinned = true
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		run,
		sessionAt("s-user", "普通会话", base.Add(-time.Hour)),
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "普通会话")
	h.assertNoTexts(tester, "自动化: 每日摘要", "每日摘要")
	if got := countText(tester, "置顶"); got != 0 {
		t.Fatalf("hidden automation run leaked %d 置顶 badges; texts = %v", got, tester.Texts())
	}
}

// TestSidebarHidesExplicitOriginSessions is the audit-F6 regression: hiding is
// driven by the session's provenance, not by its title. An automation run and a
// subagent child are hidden even with arbitrary titles.
func TestSidebarHidesExplicitOriginSessions(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		sessionAt("s-user", "普通会话", base),
		{
			ID: "s-run", Title: "每日摘要", Origin: contracts.SessionOriginAutomation,
			UpdatedAt: base,
		},
		{
			ID: "s-child", Title: "文档审查员", Origin: contracts.SessionOriginSubagent,
			UpdatedAt: base,
		},
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "普通会话")
	h.assertNoTexts(tester, "每日摘要", "文档审查员")
	// Only the one user session remains, so the list is not an empty state.
	h.assertNoTexts(tester, "还没有会话")
}

// TestSidebarKeepsUserSessionTitledLikeAnAutomationRun is the other half of
// audit F6: a normal user conversation whose title starts with the old
// "自动化: " convention has no automation provenance and must stay visible
// (and searchable, and pinnable) instead of disappearing from the sidebar.
func TestSidebarKeepsUserSessionTitledLikeAnAutomationRun(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	user := contracts.ChatSessionInfo{
		ID: "s-mine", Title: "自动化: 我的笔记", Pinned: true,
		UpdatedAt: base, Origin: contracts.SessionOriginUser,
	}
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		user,
		sessionAt("s-other", "另一个会话", base.Add(-time.Hour)),
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "自动化: 我的笔记", "另一个会话", "置顶")
	// The title-filtered search finds it like any other user conversation.
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("我的笔记")
	tester.Frame()
	h.assertTexts(tester, "自动化: 我的笔记")
	h.assertNoTexts(tester, "另一个会话")
}

// TestSidebarSearchHidesAutomationSessions: the title filter must not resurface
// an automation run when the query matches both its title and a user session;
// hiding follows the explicit provenance, not the title.
func TestSidebarSearchHidesAutomationSessions(t *testing.T) {
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s-user", Title: "项目周会"},
		{
			ID: "s-run", Title: "周报", Origin: contracts.SessionOriginAutomation,
			UpdatedAt: time.Unix(1_700_000_000, 0),
		},
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("周")
	tester.Frame()

	h.assertTexts(tester, "项目周会")
	h.assertNoTexts(tester, "周报")
}

// TestSidebarOnlyAutomationSessionsShowsEmptyState: a host with nothing but
// automation-run sessions shows the sidebar's user-session empty state (the
// new-chat landing surface), never the hidden run rows.
func TestSidebarOnlyAutomationSessionsShowsEmptyState(t *testing.T) {
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		automationSession("s-run", "每日摘要", time.Unix(1_700_000_000, 0)),
	}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "还没有会话", "新对话")
	h.assertNoTexts(tester, "自动化: 每日摘要")
}

// TestWorkspaceTreeHidesAutomationSessions: the workspace tree follows the same
// explicit-provenance rule for its rows, its per-group counts and the synthetic
// "未分组" group (a hidden run is not a user conversation, so it cannot make an
// empty ungrouped group appear; a user title with the old convention does not
// hide its session).
func TestWorkspaceTreeHidesAutomationSessions(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "用户会话", WorkspaceID: "w1", UpdatedAt: base},
		// A run hidden by provenance although its title carries no convention.
		{ID: "s2", Title: "每日摘要", WorkspaceID: "w1", Origin: contracts.SessionOriginAutomation, UpdatedAt: base},
		// An ungrouped run must not create the "未分组" group.
		{ID: "s3", Title: "后台整理", Origin: contracts.SessionOriginAutomation, UpdatedAt: base},
		// A user conversation titled like a run stays visible.
		{ID: "s4", Title: "自动化: 我的笔记", WorkspaceID: "w1", UpdatedAt: base},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "项目一", "用户会话", "自动化: 我的笔记")
	h.assertNoTexts(tester, "每日摘要", "后台整理")
	h.assertNoTexts(tester, "未分组")
	// The group count counts user sessions only (项目一 holds two visible rows).
	if got := countText(tester, "2"); got != 1 {
		t.Fatalf("group count rows showing 2 = %d, want the two user sessions; texts = %v", got, tester.Texts())
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
