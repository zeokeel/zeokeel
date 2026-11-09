package sidebar

// Sidebar "Add workspace": the 工作区 header's + button opens the native
// directory picker directly and registers the chosen folder. The workspace
// backend derives the name from the folder, so there is no naming dialog: one
// click, one native folder chooser, and the workspace appears current and
// unfolded in the tree (its row then carries the per-workspace new-chat
// action).
//
// The result is reported through the host toast bridge when it exists and
// through the header's quiet status/error rows otherwise. A folder that is
// already registered is not an error: the backend reuses it and makes it
// current, which the sidebar reports as a switch rather than a duplicate.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// DirectoryPicker is the sidebar's workspace directory-picker seam: the default
// opens the native mygo directory dialog with the localized title; tests replace
// it with a scripted selection so no desktop backend is needed. The title is a
// parameter (not package state) so windows in different languages cannot race.
var DirectoryPicker = func(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     title,
		Directory: true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// addWorkspace runs the direct add flow: the native folder picker, then
// workspace.Create with an empty name (the backend derives it from the
// folder). A cancelled picker changes nothing; a failure lands in the header's
// inline error row or the host's error toast.
func addWorkspace(state *paneState, workspace contracts.WorkspaceService, runtime contracts.UIRuntime) {
	dir, err := DirectoryPicker(state.T("选择工作区目录", "Choose workspace folder"))
	if err != nil {
		reportWorkspaceActionError(state, runtime,
			state.T("无法打开目录选择器：", "Could not open the folder picker: ")+err.Error())
		return
	}
	// A cancelled picker is a no-op: no error, no status change.
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	state.addWorkspaceErr = ""
	// Reporting only: the backend itself reuses an already registered folder.
	_, reused := registeredWorkspaceRoot(workspace, dir)
	created, err := workspace.Create(context.Background(), "", dir)
	if err != nil {
		reportWorkspaceActionError(state, runtime,
			state.T("添加工作区失败：", "Could not add the workspace: ")+err.Error())
		return
	}
	state.addWorkspaceErr = ""
	// The new workspace is current (Create makes it so) and its group may be
	// folded: open it so its row and new-chat action are visible at once.
	if id := strings.TrimSpace(created.ID); id != "" {
		state.tree.Open.Add(workspaceTreeKey(id))
	}
	status := state.T("已添加工作区", "Workspace added")
	if reused {
		status = state.T("已切换工作区", "Workspace switched")
	}
	reportWorkspaceAdded(state, runtime, status)
}

// registeredWorkspaceRoot reports whether dir is already one of the
// workspaces, matching the canonical root the backend stores.
func registeredWorkspaceRoot(workspace contracts.WorkspaceService, dir string) (contracts.Workspace, bool) {
	listed, err := workspace.List(context.Background())
	if err != nil {
		return contracts.Workspace{}, false
	}
	for _, ws := range listed {
		if sameWorkspaceRoot(ws.Root, dir) {
			return ws, true
		}
	}
	return contracts.Workspace{}, false
}

// sameWorkspaceRoot compares two workspace roots, tolerating a trailing
// separator and a symlinked path (workspace.Create stores the canonical root).
func sameWorkspaceRoot(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	left, right = filepath.Clean(left), filepath.Clean(right)
	if left == right {
		return true
	}
	absoluteLeft, leftErr := filepath.Abs(left)
	absoluteRight, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if absoluteLeft == absoluteRight {
		return true
	}
	resolvedLeft, leftErr := filepath.EvalSymlinks(absoluteLeft)
	resolvedRight, rightErr := filepath.EvalSymlinks(absoluteRight)
	return leftErr == nil && rightErr == nil && resolvedLeft == resolvedRight
}

// reportWorkspaceAdded shows the quiet success feedback: a host toast when the
// notifier exists, else the header's status badge.
func reportWorkspaceAdded(state *paneState, runtime contracts.UIRuntime, text string) {
	if state.notifier != nil {
		state.notifier.Notify(contracts.NotifySuccess, text)
		state.addWorkspaceStatus = ""
	} else {
		state.addWorkspaceStatus = text
	}
	runtime.Invalidate()
}

// reportWorkspaceActionError makes a failed add visible: an error toast when
// the host has a notifier, else the header's persistent inline error row.
func reportWorkspaceActionError(state *paneState, runtime contracts.UIRuntime, text string) {
	state.addWorkspaceStatus = ""
	if state.notifier != nil {
		state.notifier.Notify(contracts.NotifyError, text)
		state.addWorkspaceErr = ""
	} else {
		state.addWorkspaceErr = text
	}
	runtime.Invalidate()
}

// buildWorkspaceDeleteDialog confirms the destructive row-menu action that
// forgets a workspace: the folder stays on disk, only the registration goes.
// It is built on the window-level overlay slot every frame (the add dialog no
// longer exists), so the alert cannot get stuck or disappear.
func buildWorkspaceDeleteDialog(c *ui.Context, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, state *paneState) {
	if workspace == nil {
		return
	}
	name := firstNonEmpty(state.workspaceDeleteName, state.T("未命名工作区", "Untitled workspace"))
	message := fmt.Sprintf(state.T("从列表中移除工作区“%s”？文件夹不会被删除。",
		"Remove the workspace “%s” from the list? Its folder is not deleted."), name)
	switch ui.AlertDialog(c, &state.workspaceDeleteOpen,
		state.T("删除工作区", "Delete workspace"), message,
		state.T("取消", "Cancel"), state.T("删除", "Delete")) {
	case 1:
		if err := workspace.Delete(context.Background(), state.workspaceDeleteID); err != nil {
			state.workspaceDeleteErr = state.T("删除工作区失败：", "Could not delete the workspace: ") + err.Error()
		} else {
			state.workspaceDeleteErr = ""
			state.addWorkspaceStatus = state.T("已删除工作区", "Workspace deleted")
		}
		state.workspaceDeleteID = ""
		state.workspaceDeleteName = ""
		runtime.Invalidate()
	case 0, ui.AlertDismissed:
		state.workspaceDeleteID = ""
		state.workspaceDeleteName = ""
		runtime.Invalidate()
	}
}
