package sidebar_test

// Frame tests for the switch/manage dialog (the approved frame 03-switch.png):
// the recent folders with the open one checked, choosing another folder, the
// native picker, open/cancel, and the row menu that reuses the existing
// workspace rename and delete dialogs.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// openSwitchDialog opens the header's switch dialog and returns the tester.
func openSwitchDialog(t *testing.T, h *harness) *ui.Tester {
	t.Helper()
	tester := h.tester()
	h.assertTexts(tester, "工作文件夹")
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("click the switch chevron: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。", "最近使用", "打开", "取消")
	return tester
}

// TestWorkspaceSwitchDialogListsRecentsWithCurrentCheck covers the dialog's
// content: every recent folder with its root and age, the open one marked 当前
// and checked, and the picker/open/cancel actions.
func TestWorkspaceSwitchDialogListsRecentsWithCurrentCheck(t *testing.T) {
	base := time.Now()
	workspaces := append(treeWorkspaces(),
		contracts.Workspace{ID: "w3", Name: "笔记", Root: "/work/notes", CreatedAt: base.Add(-72 * time.Hour)})
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(workspaces...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	h.assertTexts(tester, "项目一", "/work/one", "项目二", "/work/two", "笔记",
		"选择其他文件夹…", "当前")
	// The open folder is marked in words and with one check, not only by color.
	if got := countText(tester, "当前"); got != 1 {
		t.Fatalf("当前 labels = %d, want one; texts = %v", got, tester.Texts())
	}
	if got := countText(tester, "打开"); got != 1 {
		t.Fatalf("打开 actions = %d, want one; texts = %v", got, tester.Texts())
	}
}

// TestWorkspaceSwitchDialogOpensTheChosenFolder: choosing another row and
// pressing 打开 opens that folder and dismisses the dialog.
func TestWorkspaceSwitchDialogOpensTheChosenFolder(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	if err := tester.Click("项目二"); err != nil {
		t.Fatalf("click the 项目二 row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if calls := workspace.openCalls; len(calls) != 0 {
		t.Fatalf("a single click opened %v; a click only chooses the row", calls)
	}
	h.click(tester, "打开")
	if calls := workspace.openCalls; len(calls) != 1 || calls[0] != "w2" {
		t.Fatalf("Open calls = %v, want [w2]", calls)
	}
	h.assertNoTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
	// The header follows the new folder at once.
	h.assertTexts(tester, "项目二")
}

// TestWorkspaceSwitchDialogEnterOpensTheChosenRow covers the keyboard path: the
// list takes the focus as the dialog opens, Down chooses the next folder and
// Enter opens it.
func TestWorkspaceSwitchDialogEnterOpensTheChosenRow(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	tester.Key(0, ui.KeyDown)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if calls := workspace.openCalls; len(calls) != 1 || calls[0] != "w2" {
		t.Fatalf("Enter opened %v, want the chosen [w2]", calls)
	}
}

// TestWorkspaceSwitchDialogCancelCloses: 取消 and Escape dismiss without opening
// anything.
func TestWorkspaceSwitchDialogCancelCloses(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	h.click(tester, "取消")
	h.assertNoTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
	if calls := workspace.openCalls; len(calls) != 0 {
		t.Fatalf("cancel opened %v", calls)
	}

	// Escape is the native modal's close.
	tester = openSwitchDialog(t, h)
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
}

// TestWorkspaceSwitchDialogChooseFolder covers 选择其他文件夹…: the native
// picker registers the folder, the folder becomes current and the dialog
// dismisses itself.
func TestWorkspaceSwitchDialogChooseFolder(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/three", nil })
	tester := openSwitchDialog(t, h)

	h.click(tester, "选择其他文件夹…")
	if workspace.createCalls != 1 || workspace.createName != "" || workspace.createRoot != "/work/three" {
		t.Fatalf("Create = calls %d name %q root %q, want the empty name and the picked folder",
			workspace.createCalls, workspace.createName, workspace.createRoot)
	}
	h.assertNoTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
	// The new folder is the open one: the header and the list follow it.
	h.assertTexts(tester, "three")
}

// TestWorkspaceSwitchDialogPickerErrorStaysInDialog: a failed picker keeps the
// dialog up and shows the failure inline.
func TestWorkspaceSwitchDialogPickerErrorStaysInDialog(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	workspace.createErr = errors.New("folder is a file")
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/file.txt", nil })
	tester := openSwitchDialog(t, h)

	h.click(tester, "选择其他文件夹…")
	h.assertTexts(tester, "添加工作区失败：", "folder is a file")
	h.assertTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
}

// TestWorkspaceSwitchDialogRenameFromRowMenu migrates the workspace rename
// flow: the row menu's rename closes the switch dialog and opens the existing
// rename dialog, whose save calls WorkspaceRenamer.
func TestWorkspaceSwitchDialogRenameFromRowMenu(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	renamer := &recordingRenamer{recordingWorkspace: workspace}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, renamer)
	tester := openSwitchDialog(t, h)

	openRowMenu(t, tester, "项目二")
	h.assertTexts(tester, "重命名工作区", "删除工作区")
	h.click(tester, "重命名工作区")
	h.assertTexts(tester, "重命名工作区", "取消", "保存")
	// The switch dialog closed first: one modal at a time.
	h.assertNoTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")

	// The dialog focuses the name input as it opens: select the draft, type and
	// press Enter (the keyboard path the dialog promises).
	tester.Key(ui.Cmd, ui.KeyA)
	chatClearAndType(tester, "新名字")
	h.click(tester, "保存")
	if calls := renamer.calls; len(calls) != 1 || calls[0].id != "w2" || calls[0].name != "新名字" {
		t.Fatalf("RenameWorkspace calls = %+v, want w2/新名字", calls)
	}
	h.assertTexts(tester, "已重命名工作区")
}

// TestWorkspaceSwitchDialogRenameErrorsKeepTheDialogOpen covers the localized
// rename errors: an empty name is rejected before the backend, a duplicate name
// is reported inline.
func TestWorkspaceSwitchDialogRenameErrorsKeepTheDialogOpen(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	renamer := &recordingRenamer{recordingWorkspace: workspace, err: contracts.ErrWorkspaceNameTaken}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, renamer)
	tester := openSwitchDialog(t, h)

	openRowMenu(t, tester, "项目二")
	h.click(tester, "重命名工作区")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	h.click(tester, "保存")
	h.assertTexts(tester, "名称不能为空")
	if calls := renamer.calls; len(calls) != 0 {
		t.Fatalf("an empty name reached the backend: %+v", calls)
	}

	clickLabel(t, tester, "工作区名称")
	tester.Key(ui.Cmd, ui.KeyA)
	chatClearAndType(tester, "项目一")
	h.click(tester, "保存")
	h.assertTexts(tester, "已有同名工作区")
	if calls := renamer.calls; len(calls) != 1 {
		t.Fatalf("duplicate rename calls = %+v, want one", calls)
	}
}

// TestWorkspaceSwitchDialogRenameHiddenWithoutRenamer: a host whose workspace
// service has no rename half keeps the rest of the menu and hides only rename.
func TestWorkspaceSwitchDialogRenameHiddenWithoutRenamer(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	openRowMenu(t, tester, "项目二")
	h.assertTexts(tester, "删除工作区")
	h.assertNoTexts(tester, "重命名工作区")
}

// TestWorkspaceSwitchDialogDeleteFromRowMenu migrates the delete flow: the row
// menu closes the switch dialog, the existing danger confirmation takes the
// keyboard, Enter cancels it and only the explicit danger action deletes.
func TestWorkspaceSwitchDialogDeleteFromRowMenu(t *testing.T) {
	workspace := &deletingWorkspace{recordingWorkspace: &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace()),
	}}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	openRowMenu(t, tester, "项目二")
	h.click(tester, "删除工作区")
	h.assertTexts(tester, "删除工作区", "取消", "删除")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(workspace.deleted) != 0 {
		t.Fatalf("Enter on the workspace confirmation deleted: %v", workspace.deleted)
	}
	h.assertNoTexts(tester, "删除工作区")

	tester = openSwitchDialog(t, h)
	openRowMenu(t, tester, "项目二")
	h.click(tester, "删除工作区")
	h.click(tester, "删除")
	if len(workspace.deleted) != 1 || workspace.deleted[0] != "w2" {
		t.Fatalf("Delete calls = %v, want [w2]", workspace.deleted)
	}
	h.assertTexts(tester, "已删除工作区")
}

// TestWorkspaceSwitchDialogOrdersRecentsByUse replaces the old group-ordering
// test: the dialog lists folders by their newest conversation, a folder without
// conversations uses its creation time (so a freshly added folder is on top),
// and an archived conversation never lifts a folder.
func TestWorkspaceSwitchDialogOrdersRecentsByUse(t *testing.T) {
	base := time.Now()
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
	workspace := newFakeWorkspace(workspaces...).withCurrent(contracts.Workspace{ID: "w-active", Name: "活跃项目", Root: "/work/active"})
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	texts := tester.Texts()
	// The row's accessible label starts the row's texts (the header shows the
	// open folder's name too, so the plain names are ambiguous).
	rowLabel := func(name, root string) string { return name + "，" + root + "，" }
	empty := indexOfPrefix(texts, rowLabel("新建项目", "/work/empty"))
	active := indexOfPrefix(texts, rowLabel("活跃项目", "/work/active"))
	old := indexOfPrefix(texts, rowLabel("旧项目", "/work/old"))
	if !(empty >= 0 && empty < active && active < old) {
		t.Fatalf("recent order = 新建项目:%d 活跃项目:%d 旧项目:%d; want the empty folder's creation time first; texts = %v",
			empty, active, old, texts)
	}
	h.assertNoTexts(tester, "归档会话")
}

// TestWorkspaceSwitchDialogLongNameKeepsActionsInside replaces the old
// long-group-name test: an unbroken workspace name wraps its text instead of
// pushing the row's check and menu out of the dialog.
func TestWorkspaceSwitchDialogLongNameKeepsActionsInside(t *testing.T) {
	long := "drive-new-workspace-with-a-very-long-unbroken-name-0123456789"
	workspaces := append(treeWorkspaces(),
		contracts.Workspace{ID: "w-long", Name: long, Root: "/work/" + long})
	workspace := newFakeWorkspace(workspaces...).withCurrent(contracts.Workspace{ID: "w-long", Name: long, Root: "/work/" + long})
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := openSwitchDialog(t, h)

	// The dialog shows the long name and keeps its three actions in one row,
	// left to right, so the unbroken name cannot push a control out.
	choose, ok := tester.Find("选择其他文件夹…")
	if !ok {
		t.Fatalf("folder picker missing; texts = %v", tester.Texts())
	}
	openButton, ok := tester.Find("打开")
	if !ok {
		t.Fatalf("打开 missing; texts = %v", tester.Texts())
	}
	cancel, ok := tester.Find("取消")
	if !ok {
		t.Fatalf("取消 missing; texts = %v", tester.Texts())
	}
	if !(choose.X+choose.W <= openButton.X+1 && openButton.X+openButton.W <= cancel.X+1) {
		t.Fatalf("dialog actions overlap: choose=%+v open=%+v cancel=%+v", choose, openButton, cancel)
	}
	// The header itself keeps the long name inside the 280 DIP sidebar (the name
	// text ellipsizes instead of pushing the switch chevron out).
	if rect, ok := tester.Find(long); ok {
		if rect.X+rect.W > 280 {
			t.Fatalf("header name leaves the sidebar: x=%v w=%v", rect.X, rect.W)
		}
	}
}

// recordingRenamer adds the optional contracts.WorkspaceRenamer half and records
// the calls.
type recordingRenamer struct {
	*recordingWorkspace
	calls []struct{ id, name string }
	err   error
}

func (r *recordingRenamer) RenameWorkspace(_ context.Context, id, name string) (contracts.Workspace, error) {
	r.calls = append(r.calls, struct{ id, name string }{id: id, name: name})
	if r.err != nil {
		return contracts.Workspace{}, r.err
	}
	return contracts.Workspace{ID: id, Name: name}, nil
}

// indexOfPrefix returns the first frame text starting with prefix, or -1.
func indexOfPrefix(texts []string, prefix string) int {
	for index, text := range texts {
		if strings.HasPrefix(text, prefix) {
			return index
		}
	}
	return -1
}

// chatClearAndType types text into the focused input after its content was
// cleared with Cmd+A / Backspace.
func chatClearAndType(tester *ui.Tester, text string) {
	tester.Type(text)
	tester.Frame()
}

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
