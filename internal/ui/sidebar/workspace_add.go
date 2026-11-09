package sidebar

// Sidebar "Add workspace": a native button in the Workspace section header and
// a native Modal (name + directory picker). Creation goes through the optional
// contracts.WorkspaceService; on success the list refreshes (it is read per
// frame), the new workspace is opened and a quiet status badge is shown.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
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

// workspaceNameFromDir derives a workspace name from a directory path.
func workspaceNameFromDir(dir string) string {
	dir = strings.TrimSpace(dir)
	cleaned := filepath.Clean(dir)
	base := strings.TrimSpace(filepath.Base(cleaned))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return dir
	}
	return base
}

// openAddWorkspaceDialog resets and opens the add-workspace modal.
func openAddWorkspaceDialog(state *paneState, runtime contracts.UIRuntime) {
	state.addWorkspaceOpen = true
	state.addWorkspaceName = ""
	state.addWorkspaceDir = ""
	state.addWorkspaceErr = ""
	state.addWorkspaceStatus = ""
	runtime.Invalidate()
}

// submitAddWorkspace validates and creates the workspace, then closes the modal
// and selects the new workspace when the backend allows it.
func submitAddWorkspace(state *paneState, workspace contracts.WorkspaceService, runtime contracts.UIRuntime) {
	dir := strings.TrimSpace(state.addWorkspaceDir)
	if dir == "" {
		state.addWorkspaceErr = state.T("目录不能为空", "The directory is required")
		runtime.Invalidate()
		return
	}
	name := strings.TrimSpace(state.addWorkspaceName)
	if name == "" {
		name = workspaceNameFromDir(dir)
	}
	created, err := workspace.Create(context.Background(), name, dir)
	if err != nil {
		state.addWorkspaceErr = state.T("添加工作区失败：", "Could not add the workspace: ") + err.Error()
		runtime.Invalidate()
		return
	}
	if strings.TrimSpace(created.ID) != "" {
		// Best effort: selecting the new workspace makes the tree highlight it
		// and the Files tab follows it. A backend that cannot open it still
		// keeps the created workspace in the list.
		_, _ = workspace.Open(context.Background(), created.ID)
	}
	state.addWorkspaceOpen = false
	state.addWorkspaceName = ""
	state.addWorkspaceDir = ""
	state.addWorkspaceErr = ""
	state.addWorkspaceStatus = state.T("已添加工作区", "Workspace added")
	runtime.Invalidate()
}

// buildAddWorkspaceDialog renders the add-workspace native modal.
func buildAddWorkspaceDialog(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, state *paneState) {
	// The Modal must be rendered every frame (showing only while the open flag
	// is true): removing it from the tree on close leaves its overlay behind,
	// which then swallows pointer events when the dialog is opened again.
	if workspace == nil {
		return
	}
	ui.Modal(c, &state.addWorkspaceOpen, func() {
		submit := func() { submitAddWorkspace(state, workspace, runtime) }
		design.Apply(ui.Column(c).Width(design.DialogWidthS).Gap(design.Space2), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, state.T("添加工作区", "Add workspace")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, state.T("选择一个目录作为工作区；名称留空时使用目录名。",
				"Choose a folder as a workspace; the folder name is used when the name is empty.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			design.Apply(ui.Text(c, state.T("名称", "Name")), t.TextMuted(design.TypeXS))
			// Enter submits from either field, as in a desktop form: the
			// input keeps the keyboard, so the footer's default action alone
			// would not fire while typing.
			ui.TextInput(c, &state.addWorkspaceName).
				Label(state.T("名称 输入", "Name input")).FillWidth().
				OnTextKey(uikit.EnterTo(submit))
			design.Apply(ui.Text(c, state.T("目录", "Directory")), t.TextMuted(design.TypeXS))
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &state.addWorkspaceDir).
					Label(state.T("目录 输入", "Directory input")).FillWidth().
					OnTextKey(uikit.EnterTo(submit))
				if ui.Button(c, state.T("选择目录…", "Choose folder…")).Clicked() {
					dir, err := DirectoryPicker(state.T("选择工作区目录", "Choose workspace folder"))
					if err != nil {
						state.addWorkspaceErr = state.T("无法打开目录选择器：", "Could not open the folder picker: ") + err.Error()
					} else if strings.TrimSpace(dir) != "" {
						state.addWorkspaceDir = dir
						state.addWorkspaceErr = ""
					}
					runtime.Invalidate()
				}
			})
			if state.addWorkspaceErr != "" {
				th := t.Theme()
				ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.AlertTriangle()).Size(th.Space(3), th.Space(3)).TextColor(t.Danger)
					design.Apply(ui.Text(c, state.addWorkspaceErr), t.Font(design.TypeXS)).TextColor(t.DangerText)
				})
			}
			// The product's one dialog action bar: the cancel on the left, the
			// primary Add on the right, focused so Enter presses it.
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: state.T("取消", "Cancel"), OnClick: func() {
					state.addWorkspaceOpen = false
					state.addWorkspaceErr = ""
					runtime.Invalidate()
				}},
				{Label: state.T("添加", "Add"), Primary: true, Default: true, OnClick: submit},
			})
		})
	})
	buildWorkspaceDeleteDialog(c, t, workspace, runtime, state)
}

// buildWorkspaceDeleteDialog confirms the destructive row-menu action that
// forgets a workspace: the folder stays on disk, only the registration goes.
func buildWorkspaceDeleteDialog(c *ui.Context, t *design.Tokens,
	workspace contracts.WorkspaceService, runtime contracts.UIRuntime, state *paneState) {
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
