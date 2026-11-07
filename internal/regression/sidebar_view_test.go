package regression

// Adversarial audit: the sidebar view options (sidebar.sort / sidebar.group).
// Negative/boundary cases: the persisted choices are written from the 视图
// popover, name sorting is case-insensitive, flat grouping hides the workspace
// headers, and missing/malformed values fall back to the old defaults without
// overwriting the stored row.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// sidebarViewChat overrides the session-list read model with scripted infos.
type sidebarViewChat struct {
	*fakeChat
	infos []contracts.ChatSessionInfo
}

func (c *sidebarViewChat) Sessions() []contracts.ChatSessionInfo {
	return append([]contracts.ChatSessionInfo(nil), c.infos...)
}

// startSidebarView activates the sidebar with the optional workspace backend and
// opens the product view.
func startSidebarView(t *testing.T, chat contracts.ChatService, workspace contracts.WorkspaceService,
	values map[string]string) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	for key, value := range values {
		if err := h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	provide(t, h.ctx, contracts.KeyChat, chat)
	if workspace != nil {
		provide(t, h.ctx, contracts.KeyWorkspace, workspace)
	}
	activate(t, h.ctx, sidebar.Module(), sidebar.Config{})
	h.registerScreen("home", "首页", "home-body")
	h.push("home")
	tester := h.tester(1280, 800)
	tester.Frame()
	return h, tester
}

// clickSidebarView clicks one sidebar label.
func clickSidebarView(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// sidebarOrder returns the positions of the wanted labels in the frame texts.
func sidebarOrder(tester *ui.Tester, names ...string) []int {
	texts := tester.Texts()
	indexes := make([]int, 0, len(names))
	for _, name := range names {
		found := -1
		for index, text := range texts {
			if strings.TrimSpace(text) == name {
				found = index
				break
			}
		}
		indexes = append(indexes, found)
	}
	return indexes
}

// TestSidebarViewMenuSortsByNameAndWrites covers the view popover: the default is
// recent-activity order, choosing 名称 writes sidebar.sort=name and sorts
// case-insensitively, and every choice persists (including switching back).
func TestSidebarViewMenuSortsByNameAndWrites(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	chat := &sidebarViewChat{fakeChat: newFakeChat(), infos: []contracts.ChatSessionInfo{
		{ID: "z", Title: "Zulu", UpdatedAt: base},                 // newest
		{ID: "b", Title: "beta", UpdatedAt: base.Add(-time.Hour)}, // older
		{ID: "a", Title: "Alpha", UpdatedAt: base.Add(-2 * time.Hour)},
	}}
	h, tester := startSidebarView(t, chat, nil, nil)
	requireHasText(t, tester, "视图")
	order := sidebarOrder(tester, "Zulu", "beta", "Alpha")
	if order[0] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("default recent order = %v; texts = %v", order, tester.Texts())
	}

	// Name sort: case-insensitive, persisted.
	clickSidebarView(t, tester, "视图")
	requireHasTexts(t, tester, "排序", "最近活动", "名称", "分组", "按工作区", "平铺")
	clickSidebarView(t, tester, "名称")
	if got := h.settings.Get(contracts.SettingSidebarSort, ""); got != "name" {
		t.Fatalf("sidebar.sort = %q, want name", got)
	}
	order = sidebarOrder(tester, "Alpha", "beta", "Zulu")
	if order[0] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("name order = %v; texts = %v", order, tester.Texts())
	}

	// Flat grouping is persisted too.
	clickSidebarView(t, tester, "视图")
	clickSidebarView(t, tester, "平铺")
	if got := h.settings.Get(contracts.SettingSidebarGroup, ""); got != "flat" {
		t.Fatalf("sidebar.group = %q, want flat", got)
	}

	// Switching back writes the defaults again.
	clickSidebarView(t, tester, "视图")
	clickSidebarView(t, tester, "最近活动")
	clickSidebarView(t, tester, "视图")
	clickSidebarView(t, tester, "按工作区")
	if got := h.settings.Get(contracts.SettingSidebarSort, ""); got != "recent" {
		t.Fatalf("sidebar.sort after switching back = %q", got)
	}
	if got := h.settings.Get(contracts.SettingSidebarGroup, ""); got != "workspace" {
		t.Fatalf("sidebar.group after switching back = %q", got)
	}
}

// TestSidebarViewFlatHidesGroupsAndBadValuesFallBack covers workspace grouping: the
// default tree shows the workspace headers, flat hides them, and malformed stored
// values normalize to the defaults without being overwritten.
func TestSidebarViewFlatHidesGroupsAndBadValuesFallBack(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspace := &auditWorkspace{created: []contracts.Workspace{
		{ID: "w1", Name: "项目一", Root: "/work/one", CreatedAt: base},
		{ID: "w2", Name: "项目二", Root: "/work/two", CreatedAt: base.Add(time.Minute)},
	}}
	chat := &sidebarViewChat{fakeChat: newFakeChat(), infos: []contracts.ChatSessionInfo{
		{ID: "s2", Title: "Beta", WorkspaceID: "w1", UpdatedAt: base},
		{ID: "s1", Title: "Alpha", WorkspaceID: "w1", UpdatedAt: base.Add(-time.Minute)},
		{ID: "s3", Title: "Gamma", WorkspaceID: "", UpdatedAt: base.Add(-2 * time.Minute)},
	}}
	h, tester := startSidebarView(t, chat, workspace, nil)
	requireHasTexts(t, tester, "项目一", "项目二")

	// Flat hides the workspace headers and keeps every session.
	clickSidebarView(t, tester, "视图")
	clickSidebarView(t, tester, "平铺")
	if got := h.settings.Get(contracts.SettingSidebarGroup, ""); got != "flat" {
		t.Fatalf("sidebar.group = %q, want flat", got)
	}
	requireNoText(t, tester, "项目一")
	requireNoText(t, tester, "项目二")
	requireNoText(t, tester, "未分组")
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		requireHasText(t, tester, title)
	}

	// Malformed stored values fall back to recent/workspace without panicking and
	// without overwriting the bad rows (the menu still writes explicitly).
	if err := h.settings.Set(contracts.SettingSidebarSort, "bogus"); err != nil {
		t.Fatalf("set bogus sort: %v", err)
	}
	if err := h.settings.Set(contracts.SettingSidebarGroup, "bogus"); err != nil {
		t.Fatalf("set bogus group: %v", err)
	}
	tester.Frame()
	requireHasTexts(t, tester, "项目一", "项目二")
	order := sidebarOrder(tester, "Beta", "Alpha", "Gamma")
	if order[0] < 0 || order[0] > order[1] || order[1] > order[2] {
		t.Fatalf("fallback recent order = %v; texts = %v", order, tester.Texts())
	}
	if got := h.settings.Get(contracts.SettingSidebarSort, ""); got != "bogus" {
		t.Fatalf("a frame overwrote sidebar.sort to %q", got)
	}
	if got := h.settings.Get(contracts.SettingSidebarGroup, ""); got != "bogus" {
		t.Fatalf("a frame overwrote sidebar.group to %q", got)
	}
}
