package sidebar_test

// Tests for the sidebar's one new-chat action after the single-workspace move:
// the primary "新建会话" row starts a conversation in the open folder. The old
// per-workspace ⊕ on each workspace group row is gone with the tree; the switch
// dialog (workspace_switch_test.go) is how a human changes the folder first.

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// failingNewChat is a fakeChat whose NewSession can fail.
type failingNewChat struct {
	*fakeChat
	newErr error
}

func (f *failingNewChat) NewSession(title string) (string, error) {
	if f.newErr != nil {
		return "", f.newErr
	}
	return f.fakeChat.NewSession(title)
}

// currentWorkspaceChat builds one current workspace with one session in it.
func currentWorkspaceChat(t *testing.T) *recordingWorkspace {
	t.Helper()
	return &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).
		withCurrent(contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})}
}

func currentWorkspaceSessions() *fakeChat {
	return &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
	}}
}

// plusLabel is the retired per-workspace new-chat accessible name. It stays in
// the test suite as the label the sidebar must NOT render any more.
func plusLabel(name string) string {
	return "在“" + name + "”中新建会话"
}

// TestPrimaryNewChatStartsSessionInTheOpenFolder covers the one new-chat action:
// the primary row creates an untitled session (the agent binds it to the open
// folder) and opens the conversation screen.
func TestPrimaryNewChatStartsSessionInTheOpenFolder(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "A 会话")
	if err := tester.Click("新建会话"); err != nil {
		t.Fatalf("click the primary new-chat row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()

	if created := chat.createdSessions(); len(created) != 1 || created[0] != "" {
		t.Fatalf("NewSession calls = %v, want one untitled session", created)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	// The new session binds to the open folder; the sidebar itself does not
	// switch folders for it (that is the agent's binding on create/send).
	if len(workspace.openCalls) != 0 {
		t.Fatalf("starting a chat reopened the current workspace: %v", workspace.openCalls)
	}
}

// TestPrimaryNewChatErrorIsSilent keeps the row's old error behavior: a failed
// new session just does not navigate (the host reports the failure through the
// session layer), and the frame stays usable.
func TestPrimaryNewChatErrorIsSilent(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	chat := &failingNewChat{fakeChat: currentWorkspaceSessions(), newErr: errors.New("no model")}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	if err := tester.Click("新建会话"); err != nil {
		t.Fatalf("click the primary new-chat row: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "工作文件夹", "项目一", "A 会话")
}

// TestPerWorkspaceNewChatIsGone pins the removal: no workspace row exists any
// more, so there is no per-workspace ⊕ and no row tooltip to carry it.
func TestPerWorkspaceNewChatIsGone(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	h := newHarnessWithWorkspace(t, currentWorkspaceSessions(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertNoTexts(tester, plusLabel("项目一"), plusLabel("项目二"))
	// The rail carries the folder actions now.
	h.assertTexts(tester, "管理工作文件夹", "添加工作文件夹")
	// The row actions that remain belong to conversations.
	if _, ok := tester.Find("会话操作：A 会话"); !ok {
		t.Fatalf("the conversation row lost its ⋯ action; texts = %v", tester.Texts())
	}
}

// TestHeaderLongNameKeepsTheSwitchControlInside covers the long-name case in
// the rail: the accessible item stays inside the 64 DIP rail.
func TestHeaderLongNameKeepsTheSwitchControlInside(t *testing.T) {
	long := "drive-new-workspace-with-a-very-long-unbroken-name-0123456789"
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(
		contracts.Workspace{ID: "w1", Name: long, Root: "/work/" + long}).
		withCurrent(contracts.Workspace{ID: "w1", Name: long, Root: "/work/" + long})}
	h := newHarnessWithWorkspace(t, currentWorkspaceSessions(), sidebar.Config{}, workspace)
	tester := h.tester()

	item, ok := tester.Find("切换工作文件夹：" + long)
	if !ok {
		t.Fatalf("rail item missing; texts = %v", tester.Texts())
	}
	if item.W > design.WorkspaceRailWidth {
		t.Fatalf("rail item leaves the rail: x=%v w=%v", item.X, item.W)
	}
}
