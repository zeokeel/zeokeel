package sidebar_test

// Conformance frame tests for the v0.8.3 native-widget migration of the
// sidebar: the session rows are a mygo List (keyboard, Enter, single click), the
// workspace tree is a mygo Outline (open/close from the keyboard, tree rows),
// and the workspace add dialog uses the product's one dialog footer with Enter
// submitting from the fields. These tests sit beside the existing suite: they
// pin the new behavior without changing the older assertions.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// deletingWorkspace records the row menu's destructive action on top of the
// existing workspace test double.
type deletingWorkspace struct {
	*recordingWorkspace
	deleted []string
}

func (w *deletingWorkspace) Delete(_ context.Context, id string) error {
	w.deleted = append(w.deleted, id)
	return nil
}

// TestSessionListSingleClickAndKeyboard covers the List's behavior: a single
// click opens a session (including the one already active, which the List does
// not report as a new choice), Up/Down move only the choice, and Enter opens
// the chosen session.
func TestSessionListSingleClickAndKeyboard(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "会话一"},
			{ID: "s2", Title: "会话二"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	// The active session is the List's choice; clicking it still opens the
	// conversation screen from Home without re-setting the active session.
	if _, ok := tester.Find("会话一"); !ok {
		t.Fatalf("session row missing; texts = %v", tester.Texts())
	}
	h.click(tester, "会话一")
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("clicking the current session row did not open the session screen: %q", current)
	}
	if calls := chat.activeCalls(); len(calls) != 0 {
		t.Fatalf("clicking the already active session called SetActiveSession: %v", calls)
	}

	// A click on another row switches the session and opens it.
	h.click(tester, "会话二")
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session after click = %q, want s2", active)
	}

	// Up moves the choice without switching the conversation; Enter opens the
	// chosen row.
	tester.Key(0, ui.KeyUp)
	tester.Frame()
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("Up changed the active session to %q; the choice alone must move", active)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if active := chat.ActiveSession(); active != "s1" {
		t.Fatalf("Enter did not open the chosen session: active = %q", active)
	}
}

// TestSessionListCoexistsWithHostSidebarSelection pins the two selection models
// side by side: the session List chooses the current conversation while the
// host's native Sidebar keeps choosing the screen destination.
func TestSessionListCoexistsWithHostSidebarSelection(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "会话一"},
			{ID: "s2", Title: "会话二"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.click(tester, "会话二")
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	// The host's Sidebar still owns the screen destination: its Home item
	// navigates back, and the session list keeps the conversation choice.
	if err := tester.Click("首页"); err != nil {
		t.Fatalf("click Home: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if current := h.uiRoot.Navigator().Current(); current != "home" {
		t.Fatalf("current screen after Home = %q, want home", current)
	}
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session changed by the host navigation: %q", active)
	}
}

// TestWorkspaceOutlineKeyboardFolding covers the Outline's tree keyboard: after
// a click chooses a workspace row, Right and Left open and close it, and
// Down/Enter move into the group and open the chosen session.
func TestWorkspaceOutlineKeyboardFolding(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...)
	chat := treeChat()
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "A 会话", "B 会话")

	// Click the group row: the old rows folded on click, and the row becomes
	// the outline's choice.
	h.click(tester, "项目一")
	h.assertNoTexts(tester, "A 会话", "B 会话")

	// Right opens the chosen group from the keyboard, Left closes it.
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	h.assertTexts(tester, "A 会话", "B 会话")
	tester.Key(0, ui.KeyLeft)
	tester.Frame()
	h.assertNoTexts(tester, "A 会话", "B 会话")

	// Down moves to the first session of the open group and Enter opens it.
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if active := chat.ActiveSession(); active != "s1" {
		t.Fatalf("Enter on the first session row activated %q, want s1", active)
	}
}

// TestWorkspaceAddDialogFooterAndEnter covers the one dialog footer (cancel on
// the left, primary Add on the right) and Enter submitting from the fields.
func TestWorkspaceAddDialogFooterAndEnter(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()
	h.click(tester, "添加工作区")
	h.assertTexts(tester, "添加工作区", "名称", "目录", "取消", "添加")

	cancel, ok := tester.Find("取消")
	if !ok {
		t.Fatalf("cancel action missing; texts = %v", tester.Texts())
	}
	add, ok := tester.Find("添加")
	if !ok {
		t.Fatalf("primary Add action missing; texts = %v", tester.Texts())
	}
	if !(cancel.X < add.X) {
		t.Fatalf("dialog footer order cancel=%.1f add=%.1f; want cancel on the left, Add on the right",
			cancel.X, add.X)
	}

	// Enter in the fields submits the dialog: the record shows the values.
	clickLabel(t, tester, "名称 输入")
	tester.Type("项目三")
	tester.Frame()
	clickLabel(t, tester, "目录 输入")
	tester.Type("/work/three")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Frame()
	if workspace.createCalls != 1 || workspace.createName != "项目三" || workspace.createRoot != "/work/three" {
		t.Fatalf("Enter did not create the workspace: calls=%d name=%q root=%q",
			workspace.createCalls, workspace.createName, workspace.createRoot)
	}
}

// TestWorkspaceRowMenuOpensAndDeletes covers the workspace row's shared menu:
// the ... row action and the right-click both offer open/delete, and delete
// confirms through the native alert before it forgets the workspace.
func TestWorkspaceRowMenuOpensAndDeletes(t *testing.T) {
	workspace := &deletingWorkspace{recordingWorkspace: &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(treeWorkspaces()...),
	}}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	openRowMenu(t, tester, "项目二")
	h.assertTexts(tester, "打开工作区", "删除工作区")
	h.click(tester, "打开工作区")
	if len(workspace.openCalls) != 1 || workspace.openCalls[0] != "w2" {
		t.Fatalf("Open calls = %v, want [w2]", workspace.openCalls)
	}

	openRowMenu(t, tester, "项目二")
	h.click(tester, "删除工作区")
	h.assertTexts(tester, "删除工作区", "取消", "删除")
	h.click(tester, "删除")
	if len(workspace.deleted) != 1 || workspace.deleted[0] != "w2" {
		t.Fatalf("Delete calls = %v, want [w2]", workspace.deleted)
	}
	h.assertTexts(tester, "已删除工作区")
}
