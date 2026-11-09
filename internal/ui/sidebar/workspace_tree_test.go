package sidebar_test

// Tests for the workspace → session sidebar tree: grouping, current-workspace
// highlight/badge, per-workspace counts, collapse/expand, the "未分组" group,
// cross-group search and the flat fallback without a workspace backend.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// fakeWorkspace is a minimal contracts.WorkspaceService for the sidebar tree.
type fakeWorkspace struct {
	mu         sync.Mutex
	workspaces []contracts.Workspace
	current    contracts.Workspace
	hasCurrent bool
	listErr    error
}

func newFakeWorkspace(workspaces ...contracts.Workspace) *fakeWorkspace {
	return &fakeWorkspace{workspaces: workspaces}
}

func (f *fakeWorkspace) withCurrent(workspace contracts.Workspace) *fakeWorkspace {
	f.current = workspace
	f.hasCurrent = true
	return f
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]contracts.Workspace(nil), f.workspaces...), nil
}

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasCurrent {
		return contracts.Workspace{}, false, nil
	}
	return f.current, true, nil
}

func (f *fakeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (f *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// treeChat builds sessions spread over two workspaces, one unknown workspace
// and one with no workspace at all.
func treeChat() *fakeChat {
	return &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
		{ID: "s2", Title: "B 会话", WorkspaceID: "w1"},
		{ID: "s3", Title: "C 会话", WorkspaceID: "w2"},
		{ID: "s4", Title: "D 会话", WorkspaceID: ""},
		{ID: "s5", Title: "E 会话", WorkspaceID: "unknown-ws"},
	}}
}

func treeWorkspaces() []contracts.Workspace {
	return []contracts.Workspace{
		{ID: "w1", Name: "项目一", Root: "/work/one"},
		{ID: "w2", Name: "项目二", Root: "/work/two"},
	}
}

func countText(tester *ui.Tester, want string) int {
	count := 0
	for _, text := range tester.Texts() {
		if text == want {
			count++
		}
	}
	return count
}

func TestWorkspaceTreeGroupsAndIndent(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作区", "项目一", "项目二", "未分组",
		"A 会话", "B 会话", "C 会话", "D 会话", "E 会话")
	// The "工作区" header replaces the flat "会话" section label (the search
	// field's "搜索会话与消息" is a different, longer text node).
	if got := countText(tester, "会话"); got != 0 {
		t.Fatalf("flat section label still rendered %d times; texts = %v", got, tester.Texts())
	}

	// Group order: 项目一, 项目二, then the trailing 未分组 group.
	texts := tester.Texts()
	if one, two, ungrouped := textIndex(texts, "项目一"), textIndex(texts, "项目二"), textIndex(texts, "未分组"); !(one >= 0 && one < two && two < ungrouped) {
		t.Fatalf("group order = one:%d two:%d ungrouped:%d; texts = %v", one, two, ungrouped, texts)
	}

	// Nested session rows are indented below their workspace header.
	headerRect, ok := tester.Find("未分组")
	if !ok {
		t.Fatalf("未分组 header missing; texts = %v", texts)
	}
	sessionRect, ok := tester.Find("D 会话")
	if !ok {
		t.Fatalf("D 会话 missing; texts = %v", texts)
	}
	if sessionRect.X <= headerRect.X {
		t.Fatalf("session row x=%.1f is not indented past the workspace header x=%.1f",
			sessionRect.X, headerRect.X)
	}
}

func TestWorkspaceTreeCountsAndCurrentBadge(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	// The current workspace name appears as the row's accessible label and the
	// row's text: two labels, one visual row. The header no longer repeats it.
	if got := countText(tester, "项目一"); got != 2 {
		t.Fatalf("当前工作区 name labels = %d, want 2 (row label + row text); texts = %v", got, tester.Texts())
	}
	// Group counts: 项目一=2, 项目二=1, 未分组=2 (empty + unknown workspace id).
	h.assertTexts(tester, "2", "1")
	if got := countText(tester, "2"); got != 2 {
		t.Fatalf("badge 2 count = %d, want 2 (项目一 and 未分组); texts = %v", got, tester.Texts())
	}
}

// TestWorkspaceGroupsOrderByRecentActivity pins the one product ordering: groups
// sort by their newest visible session (UpdatedAt desc), a workspace without
// visible sessions uses its CreatedAt (so a fresh workspace is on top), and an
// archived session never lifts a group (it is not visible in the sidebar).
func TestWorkspaceGroupsOrderByRecentActivity(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspaces := []contracts.Workspace{
		{ID: "w-old", Name: "旧项目", Root: "/work/old", CreatedAt: base.Add(-10 * time.Hour)},
		{ID: "w-active", Name: "活跃项目", Root: "/work/active", CreatedAt: base.Add(-9 * time.Hour)},
		{ID: "w-empty", Name: "新建项目", Root: "/work/empty", CreatedAt: base},
	}
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s-old", Title: "旧会话", WorkspaceID: "w-old", UpdatedAt: base.Add(-5 * time.Hour)},
		{ID: "s-active", Title: "活跃会话", WorkspaceID: "w-active", UpdatedAt: base.Add(-time.Hour)},
		{ID: "s-archived", Title: "归档会话", WorkspaceID: "w-old", Archived: true, UpdatedAt: base.Add(time.Hour)},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, newFakeWorkspace(workspaces...))
	tester := h.tester()

	texts := tester.Texts()
	empty, active, old := textIndex(texts, "新建项目"), textIndex(texts, "活跃项目"), textIndex(texts, "旧项目")
	if !(empty >= 0 && empty < active && active < old) {
		t.Fatalf("group order = 新建项目:%d 活跃项目:%d 旧项目:%d; want the empty workspace's CreatedAt first; texts = %v",
			empty, active, old, texts)
	}
	h.assertNoTexts(tester, "归档会话")
}

// TestWorkspaceGroupTooltipIsRoot keeps the duplicate-basename case readable:
// resting the pointer on the group row shows the workspace's full root, so two
// folders with the same base name stay apart.
func TestWorkspaceGroupTooltipIsRoot(t *testing.T) {
	workspace := newFakeWorkspace(contracts.Workspace{ID: "w1", Name: "src", Root: "/projects/one/src"})
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()
	rect, ok := tester.Find("src")
	if !ok {
		t.Fatalf("workspace group row missing; texts = %v", tester.Texts())
	}
	tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Advance(700 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("/projects/one/src") {
		t.Fatalf("workspace row tooltip does not show its root; texts = %v", tester.Texts())
	}
}

// TestWorkspaceTreeRendersWithZeroSessions is the fresh-install / first-added
// workspace state: with the workspace backend present the tree (and therefore
// the per-workspace new-chat action) renders even when no conversation exists.
func TestWorkspaceTreeRendersWithZeroSessions(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作区", "项目一", "项目二", "0")
	h.assertNoTexts(tester, "还没有会话")
	// The group row still carries both row actions.
	if _, ok := tester.Find("工作区操作：项目一"); !ok {
		t.Fatalf("zero-session workspace row lost its ⋯ action; texts = %v", tester.Texts())
	}
	// The current workspace row exposes its new-chat action without a hover:
	// clicking it is the proof (the tester refuses to click an inert control).
	if err := tester.Click(plusLabel("项目一")); err != nil {
		t.Fatalf("per-workspace new-chat button missing or hidden on the current workspace: %v; texts = %v",
			err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("clicking the zero-session workspace's new-chat button did not open the session screen: %q", current)
	}
}

// TestWorkspaceTreeEmptyBackendCopy covers a workspace backend with no
// registered workspace: the section keeps the add action and says what to do.
func TestWorkspaceTreeEmptyBackendCopy(t *testing.T) {
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, newFakeWorkspace())
	tester := h.tester()
	h.assertTexts(tester, "工作区", "添加工作区", "还没有工作区", "点击 + 添加工作区")
	h.assertNoTexts(tester, "还没有会话")
}

// TestWorkspaceTreeSearchNoMatchCopy keeps the search empty state inside the
// tree area (the section itself never disappears).
func TestWorkspaceTreeSearchNoMatchCopy(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "工作区", "没有匹配的会话")
	h.assertNoTexts(tester, "A 会话", "项目一")
}

func TestWorkspaceTreeCollapse(t *testing.T) {
	// No current workspace keeps the group row names unique (no section badge).
	workspace := newFakeWorkspace(treeWorkspaces()...)
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "A 会话", "B 会话", "C 会话")

	h.click(tester, "项目一")
	h.assertNoTexts(tester, "A 会话", "B 会话")
	h.assertTexts(tester, "C 会话", "项目二")

	h.click(tester, "项目一")
	h.assertTexts(tester, "A 会话", "B 会话")

	// The ungrouped group folds too.
	h.click(tester, "未分组")
	h.assertNoTexts(tester, "D 会话", "E 会话")
}

func TestWorkspaceTreeUngroupedAndEmptyWorkspaces(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "旧会话", WorkspaceID: ""},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "项目一", "项目二", "未分组", "旧会话", "0", "1")
}

func TestWorkspaceTreeSearchAcrossGroups(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "A 会话", "C 会话", "D 会话")

	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("C 会话")
	tester.Frame()
	h.assertTexts(tester, "项目二", "C 会话")
	h.assertNoTexts(tester, "项目一", "A 会话", "B 会话", "未分组", "D 会话", "E 会话")

	clickLabel(t, tester, "搜索会话与消息")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.assertTexts(tester, "A 会话", "C 会话", "D 会话", "项目一", "项目二", "未分组")
}

func TestWorkspaceTreeNoBackendKeepsFlatList(t *testing.T) {
	// Sessions may carry workspace ids, but without the workspace backend the
	// sidebar keeps the legacy flat list and its "会话" section label.
	h := newHarness(t, treeChat(), sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "会话", "A 会话", "B 会话", "C 会话", "D 会话", "E 会话")
	h.assertNoTexts(tester, "工作区", "未分组", "项目一")
}

func TestWorkspaceTreeListErrorFallsBackFlat(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	workspace.listErr = errors.New("workspace registry unavailable")
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "会话", "A 会话", "B 会话")
	h.assertNoTexts(tester, "工作区", "项目一")
}
