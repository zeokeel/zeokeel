package sidebar_test

// Tests for the per-workspace "new chat" row action (the ⊕ on every workspace
// group row) and for the primary new-chat action unfolding the current
// workspace's group: the user can create a workspace and immediately start a
// conversation in it.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
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

// plusLabel is the localized accessible name of a workspace row's new-chat
// button.
func plusLabel(name string) string {
	return "在“" + name + "”中新建会话"
}

// TestWorkspaceRowNewChatStartsSession covers the user's main flow: after a
// workspace exists, its row's ⊕ opens it and starts a new chat there, then the
// session screen comes up.
func TestWorkspaceRowNewChatStartsSession(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.assertTexts(tester, "A 会话")
	if err := tester.Click(plusLabel("项目一")); err != nil {
		t.Fatalf("click per-workspace new chat: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()

	if len(workspace.openCalls) != 1 || workspace.openCalls[0] != "w1" {
		t.Fatalf("Open calls = %v, want [w1]", workspace.openCalls)
	}
	if created := chat.createdSessions(); len(created) != 1 || created[0] != "" {
		t.Fatalf("NewSession calls = %v, want one untitled session", created)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	h.assertNoTexts(tester, "打开工作区失败", "新建会话失败")
}

// TestWorkspaceRowNewChatDoesNotToggleTheGroup pins that the ⊕ click is the
// inner button's own: on a folded current workspace it starts the chat and
// leaves the group unfolded (one reveal, not a row click folding it again).
func TestWorkspaceRowNewChatDoesNotToggleTheGroup(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	// Fold the current workspace's group; its row action stays visible because
	// it belongs to the current workspace.
	h.click(tester, "项目一")
	h.assertNoTexts(tester, "A 会话")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一"); !ok || item.Expanded {
		t.Fatalf("the workspace group did not fold: %+v", item)
	}

	if err := tester.Click(plusLabel("项目一")); err != nil {
		t.Fatalf("click per-workspace new chat on a folded group: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()

	if created := chat.createdSessions(); len(created) != 1 {
		t.Fatalf("NewSession calls = %v, want one", created)
	}
	// The click is the button's own: the group is unfolded by the success (not
	// folded again by the row's click), so its session row is visible.
	h.assertTexts(tester, "A 会话")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一"); !ok || !item.Expanded {
		t.Fatalf("the workspace group is not unfolded after starting a chat: %+v", item)
	}
}

// TestWorkspaceNewChatButtonRevealAndKeyboard covers the reveal rule and the
// keyboard path: the current workspace's ⊕ is clickable without any hover,
// other rows reveal it on hover, and the button is reachable with Tab and
// presses with Enter.
func TestWorkspaceNewChatButtonRevealAndKeyboard(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	// The hover-only rule: a non-current workspace's action is inert (the
	// tester refuses the click as it reaches the row behind it).
	if err := tester.Click(plusLabel("项目二")); err == nil {
		t.Fatalf("non-current workspace's new-chat button is clickable without hover; texts = %v", tester.Texts())
	}
	if created := chat.createdSessions(); len(created) != 0 {
		t.Fatalf("an inert row action started sessions: %v", created)
	}
	// Hovering the row reveals it, and the click then opens that workspace,
	// starts a session there and navigates.
	rect, ok := tester.Find("项目二")
	if !ok {
		t.Fatalf("项目二 row missing; texts = %v", tester.Texts())
	}
	tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	if err := tester.Click(plusLabel("项目二")); err != nil {
		t.Fatalf("hovered workspace row did not reveal its new-chat button: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if len(workspace.openCalls) != 1 || workspace.openCalls[0] != "w2" {
		t.Fatalf("Open calls = %v, want [w2]", workspace.openCalls)
	}
	if created := chat.createdSessions(); len(created) != 1 {
		t.Fatalf("NewSession calls = %v, want one", created)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}

	// Keyboard: focusing a workspace row reveals its actions, and the two Tabs
	// after the row land on ⋯ then ⊕. Space presses the new-chat button (the
	// Outline claims Enter for its own row activation, the native tree rule).
	h2 := newHarnessWithWorkspace(t, currentWorkspaceSessions(), sidebar.Config{}, currentWorkspaceChat(t))
	tester2 := h2.tester()
	// The click focuses the group row's ButtonBase (and folds the group); its
	// focus makes the row actions reachable.
	h2.click(tester2, "项目一")
	h2.assertNoTexts(tester2, "A 会话")
	tester2.Key(0, ui.KeyTab) // → the row's ⋯ action
	tester2.Frame()
	tester2.Key(0, ui.KeyTab) // → the row's ⊕ action
	tester2.Frame()
	if !tester2.Focused(plusLabel("项目一")) {
		t.Fatalf("Tab did not reach the workspace new-chat button; texts = %v", tester2.Texts())
	}
	if tester2.Focused("工作区操作：项目一") {
		t.Fatalf("Tab landed on the ⋯ action, not the new-chat button; texts = %v", tester2.Texts())
	}
	tester2.Key(0, ui.KeySpace)
	tester2.Frame()
	tester2.Frame()
	if current := h2.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen after Space = %q, want session; texts = %v", current, tester2.Texts())
	}
	// The success unfolded the group again.
	h2.assertTexts(tester2, "A 会话")
}

// TestWorkspaceRowNewChatErrorsAreInline covers both failure points: opening
// the workspace and creating the session both report inline in the workspace
// section, and nothing navigates.
func TestWorkspaceRowNewChatErrorsAreInline(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	workspace.openErr = errors.New("open failed")
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	if err := tester.Click(plusLabel("项目一")); err != nil {
		t.Fatalf("click per-workspace new chat: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "打开工作区失败：open failed")
	if created := chat.createdSessions(); len(created) != 0 {
		t.Fatalf("a failed Open still created sessions: %v", created)
	}
	if current := h.uiRoot.Navigator().Current(); current == "session" {
		t.Fatal("a failed Open navigated to the session screen")
	}

	// NewSession failure: same inline treatment, no navigation.
	workspace.openErr = nil
	failing := &failingNewChat{fakeChat: currentWorkspaceSessions(), newErr: errors.New("new failed")}
	h2 := newHarnessWithWorkspace(t, failing, sidebar.Config{}, currentWorkspaceChat(t))
	tester2 := h2.tester()
	if err := tester2.Click(plusLabel("项目一")); err != nil {
		t.Fatalf("click per-workspace new chat: %v", err)
	}
	tester2.Frame()
	tester2.Frame()
	h2.assertTexts(tester2, "新建会话失败：new failed")
	if current := h2.uiRoot.Navigator().Current(); current == "session" {
		t.Fatal("a failed NewSession navigated to the session screen")
	}
}

// TestPrimaryNewChatUnfoldsCurrentWorkspaceGroup covers the global 新对话
// button: after creating a session it unfolds the current workspace's group so
// the fresh row is visible.
func TestPrimaryNewChatUnfoldsCurrentWorkspaceGroup(t *testing.T) {
	workspace := currentWorkspaceChat(t)
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	h.click(tester, "项目一")
	h.assertNoTexts(tester, "A 会话")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一"); !ok || item.Expanded {
		t.Fatalf("the workspace group did not fold: %+v", item)
	}

	h.click(tester, "新对话")
	tester.Frame()

	if created := chat.createdSessions(); len(created) != 1 {
		t.Fatalf("global new chat created %v sessions, want one", created)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	// The current workspace's group was unfolded by the action.
	h.assertTexts(tester, "A 会话")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一"); !ok || !item.Expanded {
		t.Fatalf("the current workspace's group was not unfolded: %+v", item)
	}
}

// TestWorkspaceRowLongUnbrokenNameKeepsActionsInsideTheSidebar pins the audit
// finding that a workspace name with one long unbroken word used to push the
// row's ⋯/⊕ outside the 280-DIP sidebar (label rect X=288, W=0, unclickable).
// The name Text now shrinks below its longest word (MinWidth(0)), so the
// action is still inside the row and clickable.
func TestWorkspaceRowLongUnbrokenNameKeepsActionsInsideTheSidebar(t *testing.T) {
	const longName = "drive-new-workspace1997302650"
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(contracts.Workspace{
		ID: "w1", Name: longName, Root: "/work/one",
	}).withCurrent(contracts.Workspace{ID: "w1", Name: longName, Root: "/work/one"})}
	chat := currentWorkspaceSessions()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()

	rect, ok := tester.Find(plusLabel(longName))
	if !ok {
		t.Fatalf("per-workspace new-chat action missing for a long name; texts = %v", tester.Texts())
	}
	if rect.W <= 0 {
		t.Fatalf("per-workspace new-chat action has zero width: %+v", rect)
	}
	if err := tester.Click(plusLabel(longName)); err != nil {
		t.Fatalf("per-workspace new-chat action outside the sidebar/cropped: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if created := chat.createdSessions(); len(created) != 1 {
		t.Fatalf("NewSession calls = %v, want one session", created)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
}
