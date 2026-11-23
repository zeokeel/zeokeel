package sidebar_test

// Compact single-workspace sidebar suite (WORKSPACE-UX, AGENT-SURFACES S12):
// the sidebar shows ONE read-only folder line ("工作文件夹 / <name>", root as
// tooltip) and a flat list of the open folder's conversations. The workspace
// rail, the switch/manage dialog and the folder color picker are gone, so no
// sidebar control changes or manages folders; the session list, its search,
// the row actions, 新建会话, the module navigation and the settings footer stay.
//
// These tests replace the former rail/switch/color/add/rename suites.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// fakeWorkspace is a minimal contracts.WorkspaceService for the sidebar tests:
// a fixed registry, one current folder and a log of the folders opened by the
// module (a session whose folder differs switches it on open).
type fakeWorkspace struct {
	mu         sync.Mutex
	workspaces []contracts.Workspace
	current    contracts.Workspace
	hasCurrent bool
	listErr    error
	opened     []string
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

func (f *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, id)
	for _, workspace := range f.workspaces {
		if workspace.ID == id {
			f.current, f.hasCurrent = workspace, true
			return workspace, nil
		}
	}
	return contracts.Workspace{ID: id}, nil
}

func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// openedIDs returns the folders the module opened, in order.
func (f *fakeWorkspace) openedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

// treeChat builds sessions spread over two folders, one forgotten folder and
// one with no binding at all.
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

// currentWorkspace returns the project-one workspace (the usual open folder).
func currentWorkspace() contracts.Workspace {
	return contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"}
}

// countText returns how many frame texts exactly equal want.
func countText(tester *ui.Tester, want string) int {
	count := 0
	for _, text := range tester.Texts() {
		if text == want {
			count++
		}
	}
	return count
}

// TestWorkspaceLineIsReadOnly pins the replacement of the header card and the
// rail: one read-only line names the open folder and nothing offers a switch,
// a manage dialog, an add action or a folder color.
func TestWorkspaceLineIsReadOnly(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作文件夹 /", "项目一", "新建会话")
	h.assertNoTexts(tester, "切换工作文件夹", "管理工作文件夹", "添加工作文件夹",
		"工作区操作：项目一", "最近使用", "选择其他文件夹…", "打开", "取消",
		"切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
	// The former rail's accessible item labels are gone with the rail itself.
	h.assertNoTexts(tester, "切换工作文件夹：项目一", "切换工作文件夹：项目二")
	// The line is one row inside the sidebar, not a card above it.
	line, ok := tester.Find("工作文件夹 /")
	if !ok {
		t.Fatalf("folder line missing; texts = %v", tester.Texts())
	}
	if line.W <= 0 || line.H <= 0 {
		t.Fatalf("folder line has no layout: %+v", line)
	}
}

// TestWorkspaceScopesFlatList: the list holds the open folder's conversations
// (plus unbound/forgotten ones, which would otherwise be unreachable) and never
// another folder's.
func TestWorkspaceScopesFlatList(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).
		withCurrent(contracts.Workspace{ID: "w2", Name: "项目二", Root: "/work/two"})
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "工作文件夹 /", "项目二", "C 会话", "D 会话", "E 会话")
	h.assertNoTexts(tester, "A 会话", "B 会话")
	// The flat "会话" section label still belongs to the no-backend fallback.
	if got := countText(tester, "会话"); got != 0 {
		t.Fatalf("flat section label rendered %d times with a workspace; texts = %v", got, tester.Texts())
	}
}

// TestWorkspaceSearchScope: the title search cannot resurface another folder's
// conversation, and clearing it restores the folder's rows.
func TestWorkspaceSearchScope(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "A 会话", "B 会话")

	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("C 会话")
	tester.Frame()
	h.assertTexts(tester, "没有匹配")
	h.assertNoTexts(tester, "C 会话", "A 会话", "B 会话")

	clickLabel(t, tester, "搜索会话与消息")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.assertTexts(tester, "工作文件夹 /", "项目一", "A 会话", "B 会话", "D 会话", "E 会话")
}

// TestWorkspaceNewChat: the one 新建会话 action creates an untitled session and
// opens the conversation screen; the sidebar never changes the open folder for
// it (the binding is the agent's, on create/send).
func TestWorkspaceNewChat(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.click(tester, "新建会话")
	if created := chat.createdSessions(); len(created) != 1 || created[0] != "" {
		t.Fatalf("NewSession calls = %v, want one untitled session", created)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("starting a chat reopened the workspace: %v", opened)
	}
	h.assertTexts(tester, "工作文件夹 /", "项目一", "A 会话")
}

// TestWorkspaceSessionRowActions keeps the conversation row menu after the
// workspace UI removal: the shared popover menu opens on right-click, offers
// 设为当前 for an inactive row and always the destructive delete.
func TestWorkspaceSessionRowActions(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
			{ID: "s2", Title: "B 会话", WorkspaceID: "w1"},
		},
		active: "s1",
	}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	// The active row: the destructive action is there, 设为当前 is not.
	openRowMenu(t, tester, "A 会话")
	h.assertTexts(tester, "删除")
	h.assertNoTexts(tester, "设为当前")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	// An inactive row offers 设为当前, which opens it.
	openRowMenu(t, tester, "B 会话")
	h.assertTexts(tester, "删除", "设为当前")
	h.click(tester, "设为当前")
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session after 设为当前 = %q, want s2", active)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
}

// TestNoWorkspaceKeepsFlatList: without the workspace backend the sidebar keeps
// the legacy flat list and its "会话" section label; no folder line appears.
func TestNoWorkspaceKeepsFlatList(t *testing.T) {
	h := newHarness(t, treeChat(), sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "会话", "A 会话", "B 会话", "C 会话", "D 会话", "E 会话")
	h.assertNoTexts(tester, "工作文件夹 /", "未选择工作文件夹", "项目一", "项目二")
}

// TestWorkspaceNoOpenFolderCopy: a registry with no open folder (right after
// deleting the open one, or a fresh registry) says so on the line and every
// conversation stays reachable instead of vanishing.
func TestWorkspaceNoOpenFolderCopy(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "旧会话", WorkspaceID: ""},
		{ID: "s2", Title: "别的文件夹会话", WorkspaceID: "w2"},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "未选择工作文件夹", "旧会话", "别的文件夹会话")
	h.assertNoTexts(tester, "工作文件夹 /", "还没有会话", "选择其他文件夹…")
}

// TestWorkspaceListErrorFallsBackFlat: a failing registry must not hide the
// session list; the plain "会话" list is the fallback.
func TestWorkspaceListErrorFallsBackFlat(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	workspace.listErr = errors.New("workspace registry unavailable")
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "会话", "A 会话", "B 会话")
	h.assertNoTexts(tester, "工作文件夹 /", "项目一")
}
