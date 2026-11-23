package sidebar_test

// Frame tests for the workspace rename flow after the single-workspace move:
// the switch dialog's row menu offers "重命名工作区" only when the backend
// implements the optional renamer, the native dialog's Enter saves and Escape
// cancels, an empty name is rejected inline, a duplicate name surfaces the
// backend's localized error, and a successful rename refreshes the header (and
// the switch dialog's list).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// renamingWorkspace extends fakeWorkspace with the optional renamer half,
// mirroring the backend's contract: trim + case-insensitive duplicate check,
// empty names rejected with the exported sentinel, identity/current preserved.
type renamingWorkspace struct {
	*fakeWorkspace
	renameCalls []string
}

func (f *renamingWorkspace) RenameWorkspace(_ context.Context, id, name string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renameCalls = append(f.renameCalls, id+"="+name)
	name = strings.TrimSpace(name)
	if name == "" {
		return contracts.Workspace{}, fmt.Errorf("%w: %q", contracts.ErrWorkspaceNameEmpty, id)
	}
	for _, other := range f.workspaces {
		if other.ID != id && strings.EqualFold(strings.TrimSpace(other.Name), name) {
			return contracts.Workspace{}, fmt.Errorf("%w: %q", contracts.ErrWorkspaceNameTaken, name)
		}
	}
	for index, ws := range f.workspaces {
		if ws.ID != id {
			continue
		}
		renamed := ws
		renamed.Name = name
		f.workspaces[index] = renamed
		if f.current.ID == id {
			f.current = renamed
		}
		return renamed, nil
	}
	return contracts.Workspace{}, fmt.Errorf("workspace: not found: %q", id)
}

// openWorkspaceRename opens the switch dialog, one folder's row menu and its
// rename entry (the single-workspace path to the existing rename dialog).
func openWorkspaceRename(t *testing.T, tester *ui.Tester, title string) {
	t.Helper()
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	openRowMenu(t, tester, title)
	if err := tester.Click("重命名工作区"); err != nil {
		t.Fatalf("click 重命名工作区: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
}

// TestWorkspaceRenameFromRowMenu covers the happy path: the row menu offers the
// rename entry only with the renamer present, Enter in the native dialog saves,
// and the header plus the switch dialog show the new name.
func TestWorkspaceRenameFromRowMenu(t *testing.T) {
	current := contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"}
	workspace := &renamingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(current)}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	openWorkspaceRename(t, tester, "项目一")
	h.assertTexts(tester, "重命名工作区", "工作区名称", "取消", "保存")

	// The dialog focuses the name input: select the current draft, type the new
	// name and press Enter (the keyboard path the dialog promises).
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("新项目名")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Frame()

	if len(workspace.renameCalls) != 1 || workspace.renameCalls[0] != "w1=新项目名" {
		t.Fatalf("RenameWorkspace calls = %v, want [w1=新项目名]", workspace.renameCalls)
	}
	// The dialog closed (its controls are gone) and the header shows the new
	// name; the success badge "已重命名工作区" contains the menu copy, so the
	// dialog is asserted by its own controls instead.
	h.assertNoTexts(tester, "工作区名称", "保存")
	h.assertTexts(tester, "新项目名", "已重命名工作区")
	h.assertNoTexts(tester, "项目一")
	// The current workspace keeps its identity, so its new title is the open one.
	currentAfter, ok, err := workspace.Current(context.Background())
	if err != nil || !ok || currentAfter.ID != "w1" || currentAfter.Name != "新项目名" {
		t.Fatalf("current workspace after rename = %+v (%v, %v), want w1 新项目名", currentAfter, ok, err)
	}
}

// TestWorkspaceRenameEmptyNameIsRejected covers the inline validation: an empty
// name keeps the dialog open with the localized error, and the backend is not
// asked.
func TestWorkspaceRenameEmptyNameIsRejected(t *testing.T) {
	workspace := &renamingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(
		contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	openWorkspaceRename(t, tester, "项目一")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	h.assertTexts(tester, "重命名工作区", "名称不能为空")
	if len(workspace.renameCalls) != 0 {
		t.Fatalf("an empty name reached the backend: %v", workspace.renameCalls)
	}
	// Cancelling leaves the registry and the header untouched.
	h.click(tester, "取消")
	h.assertNoTexts(tester, "重命名工作区")
	h.assertTexts(tester, "项目一")
}

// TestWorkspaceRenameEscapeCancels covers the keyboard cancel: Escape closes
// the dialog without renaming anything.
func TestWorkspaceRenameEscapeCancels(t *testing.T) {
	workspace := &renamingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(
		contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	openWorkspaceRename(t, tester, "项目二")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("不保存的名字")
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()

	h.assertNoTexts(tester, "重命名工作区")
	h.assertTexts(tester, "项目一")
	// The registry is unchanged: reopening the switch dialog still lists 项目二.
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("reopen the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "项目二")
	if len(workspace.renameCalls) != 0 {
		t.Fatalf("Escape still renamed: %v", workspace.renameCalls)
	}
}

// TestWorkspaceRenameDuplicateIsLocalized covers the backend's duplicate error:
// the contract sentinel becomes the product's localized copy in the dialog.
func TestWorkspaceRenameDuplicateIsLocalized(t *testing.T) {
	workspace := &renamingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(
		contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	openWorkspaceRename(t, tester, "项目一")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("项目二")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	h.assertTexts(tester, "重命名工作区", "已有同名工作区")
	if len(workspace.renameCalls) != 1 {
		t.Fatalf("RenameWorkspace calls = %v, want one rejected attempt", workspace.renameCalls)
	}
	// The rejected rename changed nothing: the header still names 项目一 and the
	// switch dialog still lists both folders.
	h.click(tester, "取消")
	h.assertTexts(tester, "项目一")
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("reopen the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "项目一", "项目二")
}

// TestWorkspaceRenameHiddenWithoutRenamer covers the graceful fallback: a host
// without contracts.WorkspaceRenamer keeps the delete entry and hides only
// rename.
func TestWorkspaceRenameHiddenWithoutRenamer(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(
		contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"})
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	openRowMenu(t, tester, "项目一")
	h.assertTexts(tester, "删除工作区")
	h.assertNoTexts(tester, "重命名工作区")
}
