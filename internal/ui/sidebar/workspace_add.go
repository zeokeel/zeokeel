package sidebar

// Sidebar "Add workspace": a native button in the Workspace section header and
// a native Modal (name + directory picker). Creation goes through the optional
// contracts.WorkspaceService; on success the list refreshes (it is read per
// frame), the new workspace is opened and a quiet status badge is shown.

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// DirectoryPicker is the sidebar's workspace directory-picker seam: the default
// opens the native mygo directory dialog; tests replace it with a scripted
// selection so no desktop backend is needed.
var DirectoryPicker = func() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     "选择工作区目录",
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
		design.Apply(ui.Column(c).Width(460).Gap(design.Space2), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, state.T("添加工作区", "Add workspace")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, state.T("选择一个目录作为工作区；名称留空时使用目录名。",
				"Choose a folder as a workspace; the folder name is used when the name is empty.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			design.Apply(ui.Text(c, state.T("名称", "Name")), t.TextMuted(design.TypeXS))
			ui.TextInput(c, &state.addWorkspaceName).
				Label(state.T("名称 输入", "Name input")).FillWidth()
			design.Apply(ui.Text(c, state.T("目录", "Directory")), t.TextMuted(design.TypeXS))
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &state.addWorkspaceDir).
					Label(state.T("目录 输入", "Directory input")).FillWidth()
				if ui.Button(c, state.T("选择目录…", "Choose folder…")).Clicked() {
					dir, err := DirectoryPicker()
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
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				add := ui.PrimaryButton(c, state.T("添加", "Add"))
				design.Apply(add, t.PrimaryButton())
				if add.Clicked() {
					submitAddWorkspace(state, workspace, runtime)
				}
				if ui.Button(c, state.T("取消", "Cancel")).Clicked() {
					state.addWorkspaceOpen = false
					state.addWorkspaceErr = ""
					runtime.Invalidate()
				}
			})
		})
	})
}
