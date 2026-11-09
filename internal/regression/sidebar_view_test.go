package regression

// Adversarial audit: the sidebar's fixed view. The 视图 popover and its
// persisted sort/group options are gone: with a workspace backend the sidebar
// always groups by workspace, ordered by recent activity with pinned sessions
// ahead inside a group, and a stale stored option cannot bring the name sort or
// the flat mode back. Only a host without a workspace backend gets the flat
// fallback, which still sorts recent-first and pinned-first.

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

// startSidebarView activates the sidebar with the optional workspace backend
// and opens the product view.
func startSidebarView(t *testing.T, chat contracts.ChatService,
	workspace contracts.WorkspaceService) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
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

// TestSidebarViewMenuIsGone pins the removal of the 视图 popover and its sort
// and grouping choices in both languages.
func TestSidebarViewMenuIsGone(t *testing.T) {
	workspace := &auditWorkspace{
		created:   []contracts.Workspace{{ID: "w1", Name: "项目一", Root: "/work/one"}},
		currentID: "w1",
	}
	chat := &sidebarViewChat{fakeChat: newFakeChat(), infos: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "Alpha", WorkspaceID: "w1", UpdatedAt: time.Unix(1_700_000_000, 0)},
	}}
	h, tester := startSidebarView(t, chat, workspace)
	requireHasTexts(t, tester, "工作区", "项目一", "Alpha")
	for _, gone := range []string{"视图", "排序", "最近活动", "名称", "分组", "按工作区", "平铺"} {
		requireNoText(t, tester, gone)
	}

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	requireHasTexts(t, tester, "Workspace", "Add workspace", "项目一", "Alpha")
	for _, gone := range []string{"View", "Sort", "Recent activity", "Group", "By workspace", "Flat"} {
		requireNoText(t, tester, gone)
	}
}

// TestSidebarStaleViewSettingsIgnored seeds the retired persisted options: the
// sidebar keeps grouping by workspace (no flat mode) and keeps recent activity
// ordering with pinned-first inside a group (no name sort), and it never
// rewrites the stale rows.
func TestSidebarStaleViewSettingsIgnored(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspace := &auditWorkspace{
		created: []contracts.Workspace{
			{ID: "w1", Name: "项目一", Root: "/work/one"},
			{ID: "w2", Name: "项目二", Root: "/work/two"},
		},
		currentID: "w1",
	}
	chat := &sidebarViewChat{fakeChat: newFakeChat(), infos: []contracts.ChatSessionInfo{
		{ID: "s-old", Title: "Alpha 旧", WorkspaceID: "w1", UpdatedAt: base.Add(-3 * time.Hour)},
		{ID: "s-pin", Title: "Zulu 置顶", WorkspaceID: "w1", Pinned: true, UpdatedAt: base.Add(-4 * time.Hour)},
		{ID: "s-new", Title: "Beta 新", WorkspaceID: "w2", UpdatedAt: base},
	}}
	h, tester := startSidebarView(t, chat, workspace)
	if err := h.settings.Set("sidebar.sort", "name"); err != nil {
		t.Fatalf("seed sidebar.sort: %v", err)
	}
	if err := h.settings.Set("sidebar.group", "flat"); err != nil {
		t.Fatalf("seed sidebar.group: %v", err)
	}
	tester.Frame()

	// Grouping wins over the stale "flat" value.
	requireHasTexts(t, tester, "项目一", "项目二")
	// Recent-activity group order: 项目二 (newest session) above 项目一.
	if order := sidebarOrder(tester, "项目二", "项目一"); !(order[0] >= 0 && order[0] < order[1]) {
		t.Fatalf("group order = %v; want recent activity first; texts = %v", order, tester.Texts())
	}
	// Name sorting does not apply: inside 项目一 the pinned Zulu row comes
	// before the unpinned Alpha row even though Alpha sorts first by name.
	if order := sidebarOrder(tester, "Zulu 置顶", "Alpha 旧"); !(order[0] >= 0 && order[0] < order[1]) {
		t.Fatalf("pinned-first order lost: %v; texts = %v", order, tester.Texts())
	}
	// Reading frames never rewrite the retired settings rows.
	if got := h.settings.Get("sidebar.sort", ""); got != "name" {
		t.Fatalf("a frame overwrote the stale sidebar.sort to %q", got)
	}
	if got := h.settings.Get("sidebar.group", ""); got != "flat" {
		t.Fatalf("a frame overwrote the stale sidebar.group to %q", got)
	}
}

// TestSidebarGroupOrderEmptyWorkspaceCreatedAt covers the boundary of a
// workspace with no visible sessions: it sorts by its CreatedAt, newest first,
// so a freshly added workspace stays at the top; an archived session never
// counts as activity.
func TestSidebarGroupOrderEmptyWorkspaceCreatedAt(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspace := &auditWorkspace{
		created: []contracts.Workspace{
			{ID: "w-old", Name: "旧项目", Root: "/work/old", CreatedAt: base.Add(-10 * time.Hour)},
			{ID: "w-empty", Name: "新建项目", Root: "/work/empty", CreatedAt: base},
		},
		currentID: "w-empty",
	}
	chat := &sidebarViewChat{fakeChat: newFakeChat(), infos: []contracts.ChatSessionInfo{
		{ID: "s-old", Title: "旧会话", WorkspaceID: "w-old", UpdatedAt: base.Add(-5 * time.Hour)},
		// An archived session in 旧项目 is newer, but it is not visible.
		{ID: "s-archived", Title: "归档会话", WorkspaceID: "w-old", Archived: true, UpdatedAt: base.Add(time.Hour)},
	}}
	_, tester := startSidebarView(t, chat, workspace)
	requireHasTexts(t, tester, "新建项目", "旧项目")
	if order := sidebarOrder(tester, "新建项目", "旧项目"); !(order[0] >= 0 && order[0] < order[1]) {
		t.Fatalf("group order = %v; want the empty workspace by CreatedAt first; texts = %v",
			order, tester.Texts())
	}
	requireNoText(t, tester, "归档会话")
}

// TestSidebarNoWorkspaceFlatRecentPinned covers the only fallback: without a
// workspace backend the rows are one flat list, still recent-first and
// pinned-first, with no view menu to change that.
func TestSidebarNoWorkspaceFlatRecentPinned(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	chat := &sidebarViewChat{fakeChat: newFakeChat(), infos: []contracts.ChatSessionInfo{
		{ID: "s-old", Title: "Old", UpdatedAt: base.Add(-2 * time.Hour)},
		{ID: "s-pin", Title: "Pinned old", Pinned: true, UpdatedAt: base.Add(-3 * time.Hour)},
		{ID: "s-new", Title: "Newest", UpdatedAt: base},
	}}
	_, tester := startSidebarView(t, chat, nil)
	requireHasTexts(t, tester, "会话", "Old", "Pinned old", "Newest")
	for _, gone := range []string{"视图", "排序", "分组", "工作区", "未分组"} {
		requireNoText(t, tester, gone)
	}
	order := sidebarOrder(tester, "Pinned old", "Newest", "Old")
	if !(order[0] >= 0 && order[0] < order[1] && order[1] < order[2]) {
		t.Fatalf("flat fallback order = %v; want pinned first then recent; texts = %v", order, tester.Texts())
	}
}
