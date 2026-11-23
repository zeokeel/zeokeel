package sidebar_test

// Tests for the sidebar's single-workspace view (WORKSPACE-UX): the header names
// the one open folder and the list shows only that folder's conversations. The
// multi-folder registry still exists in the backend (the switch dialog manages
// it), but the chrome has no workspace tree, no groups and no per-group
// new-chat action. These tests replace the old workspace-tree suite.

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

// fakeWorkspace is a minimal contracts.WorkspaceService for the sidebar tests.
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

// currentWorkspace returns the project-one workspace (the sidebar fixtures'
// usual open folder).
func currentWorkspace() contracts.Workspace {
	return contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"}
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

// TestSingleWorkspaceHeaderAndScopedList is the approved sidebar: the header
// reads 工作文件夹 + the open folder + its switch chevron, and the list holds
// only that folder's conversations. The other registered folder's session is
// gone; a session without a binding or with a forgotten folder stays reachable
// (hiding it would make it unreachable from any surface).
func TestSingleWorkspaceHeaderAndScopedList(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作文件夹", "项目一", "切换工作文件夹",
		"A 会话", "B 会话", "D 会话", "E 会话")
	// The other folder's conversation is not in this folder's list, and the old
	// tree vocabulary (groups, add button, per-group new chat) is gone.
	h.assertNoTexts(tester, "C 会话", "工作区", "未分组", "项目二", "添加工作区",
		"在“项目一”中新建会话", "在“项目二”中新建会话")
	// The flat "会话" section label has no group heading any more.
	if got := countText(tester, "会话"); got != 0 {
		t.Fatalf("flat section label still rendered %d times; texts = %v", got, tester.Texts())
	}
}

// TestSingleWorkspaceSwitchingScopesTheList covers the filter from the other
// side: with project two open the sidebar lists its conversation and not project
// one's.
func TestSingleWorkspaceSwitchingScopesTheList(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).
		withCurrent(contracts.Workspace{ID: "w2", Name: "项目二", Root: "/work/two"})
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "项目二", "C 会话", "D 会话", "E 会话")
	h.assertNoTexts(tester, "A 会话", "B 会话", "项目一")
}

// TestSingleWorkspaceRowClickOpensSession keeps the flat row's click behavior:
// a click on the row's leading block opens the conversation and the accent
// stripe marks the selection (no tree indent any more).
func TestSingleWorkspaceRowClickOpensSession(t *testing.T) {
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
		{ID: "s2", Title: "B 会话", WorkspaceID: "w1"},
	}, active: "s2"}
	ws := newFakeWorkspace(currentWorkspace()).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, ws)
	tester := h.tester()

	row, ok := tester.Find("A 会话")
	if !ok {
		t.Fatalf("A 会话 row missing; texts = %v", tester.Texts())
	}
	// The row's own first children are the stripe and the conversation glyph
	// (the flat list has no prefix): a click on the row front opens the session.
	tester.ClickAt(row.X+20, row.Y+row.H/2)
	tester.Frame()
	tester.Frame()
	if got := chat.ActiveSession(); got != "s1" {
		t.Fatalf("active session after clicking the row front = %q, want s1", got)
	}
}

// TestSingleWorkspaceHeaderTooltipIsRoot keeps the duplicate-basename case
// readable: the header's name shows the folder's full root on hover.
func TestSingleWorkspaceHeaderTooltipIsRoot(t *testing.T) {
	workspace := newFakeWorkspace(contracts.Workspace{ID: "w1", Name: "src", Root: "/projects/one/src"}).
		withCurrent(contracts.Workspace{ID: "w1", Name: "src", Root: "/projects/one/src"})
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()
	names := 0
	var rect ui.Rect
	// The header name is the only "src" text; hover it for the root tooltip.
	if found, ok := tester.Find("src"); ok {
		rect = found
		names++
	}
	if names != 1 {
		t.Fatalf("header name count = %d, want one; texts = %v", names, tester.Texts())
	}
	tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Advance(700 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("/projects/one/src") {
		t.Fatalf("header tooltip does not show the folder root; texts = %v", tester.Texts())
	}
}

// TestSingleWorkspaceZeroSessions is the fresh-install state: the header still
// names the folder, the empty state invites a conversation and the new-chat
// action works without any tree row.
func TestSingleWorkspaceZeroSessions(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作文件夹", "项目一", "还没有会话", "新建会话")
	h.assertNoTexts(tester, "还没有工作区", "选择 + 添加工作区")
	if err := tester.Click("新建会话"); err != nil {
		t.Fatalf("new-chat row missing: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("new chat did not open the session screen: %q", current)
	}
}

// TestSingleWorkspaceNoOpenFolderCopy covers a host whose registry has no open
// folder (right after deleting the open one, or a fresh registry): the header
// says so and every conversation stays reachable instead of vanishing.
func TestSingleWorkspaceNoOpenFolderCopy(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "旧会话", WorkspaceID: ""},
		{ID: "s2", Title: "别的文件夹会话", WorkspaceID: "w2"},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作文件夹", "未选择工作文件夹", "旧会话", "别的文件夹会话")
	h.assertNoTexts(tester, "还没有会话", "项目一", "项目二")
}

// TestSingleWorkspaceSearchStaysInTheFolder keeps the title search scoped: a
// query cannot resurface a conversation of another folder.
func TestSingleWorkspaceSearchStaysInTheFolder(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "A 会话", "B 会话")

	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("C 会话")
	tester.Frame()
	h.assertTexts(tester, "没有匹配")
	h.assertNoTexts(tester, "C 会话", "A 会话", "B 会话", "项目二")

	clickLabel(t, tester, "搜索会话与消息")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.assertTexts(tester, "A 会话", "B 会话", "D 会话", "E 会话")
}

// TestSingleWorkspaceEmptySearchCopy keeps the search empty state in the list
// area (the header never disappears).
func TestSingleWorkspaceEmptySearchCopy(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "工作文件夹", "项目一", "没有匹配")
	h.assertNoTexts(tester, "A 会话")
}

// TestSingleWorkspaceNoBackendKeepsFlatList: sessions may carry workspace ids,
// but without the workspace backend the sidebar keeps the legacy flat list and
// its "会话" section label.
func TestSingleWorkspaceNoBackendKeepsFlatList(t *testing.T) {
	h := newHarness(t, treeChat(), sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "会话", "A 会话", "B 会话", "C 会话", "D 会话", "E 会话")
	h.assertNoTexts(tester, "工作文件夹", "切换工作文件夹", "未分组", "项目一")
}

// TestSingleWorkspaceListErrorFallsBackFlat: a failing registry must not hide
// the session list; the plain list is the fallback.
func TestSingleWorkspaceListErrorFallsBackFlat(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	workspace.listErr = errors.New("workspace registry unavailable")
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "会话", "A 会话", "B 会话")
	h.assertNoTexts(tester, "工作文件夹", "项目一")
}
