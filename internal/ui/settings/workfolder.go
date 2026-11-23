package settings

// The "Work folder" row of the Behavior page. ZeoKeel opens one workspace
// folder at a time (single-workspace product): the host creates and opens
// <dataDir>/workspace on a launch without one, and this row is the user's only
// place to point the product at another folder. It shows the folder the shell,
// the files inspector and the agent tools currently use and runs the native
// directory picker behind 更改… / Change…; the chosen folder is registered and
// made current through the workspace backend, which is the same call the
// sidebar's switch flow uses. There is deliberately no workspace list here: the
// product has no multi-workspace navigation any more.

import (
	"context"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/mcpreload"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// WorkFolderDirectoryPicker is the settings row's directory-picker seam: the
// default opens the native mygo directory dialog with the localized title;
// tests replace it with a scripted selection so no desktop backend is needed.
// The title is a parameter (not package state) so windows in different
// languages cannot race.
var WorkFolderDirectoryPicker = func(title string) (string, error) {
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

// workFolderState is the row's UI-thread state: the optional workspace backend,
// the kernel context (for the workspace-scoped MCP reload) and the last change
// result shown inline under the row.
type workFolderState struct {
	workspace contracts.WorkspaceService
	kernel    cordis.Context
	status    string
	statusErr bool
}

// newWorkFolderState resolves the optional workspace backend. A composition
// without it renders no row: there is no folder to show or change.
func newWorkFolderState(kernel cordis.Context) *workFolderState {
	workspace, _ := kernel.Get(contracts.KeyWorkspace)
	return &workFolderState{workspace: workspace, kernel: kernel}
}

// buildWorkFolderSection renders the row: the section title, the current root
// and the native-picker action. The current folder is read per build (the
// sidebar and other surfaces can change it) and a failed read reads as "no
// folder" instead of hiding the action.
func buildWorkFolderSection(c *ui.Context, t *design.Tokens, locale contracts.LocaleService,
	runtime uinative.UIRuntime, st *workFolderState) {
	if st == nil || st.workspace == nil {
		return
	}
	root := ""
	if current, ok, err := st.workspace.Current(context.Background()); err == nil && ok {
		root = strings.TrimSpace(current.Root)
	}
	uikit.Section(c, localized(locale, "工作文件夹", "Work folder"), func(body *ui.Context) {
		ui.Row(body).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			design.Apply(ui.Column(body), t.Gap(design.Space0)).Grow(1).MinWidth(design.Space0).
				Children(func() {
					label := root
					color := t.LabelPrimary
					if label == "" {
						label = localized(locale, "未选择工作文件夹", "No workspace folder")
						color = t.LabelSecondary
					}
					design.Apply(ui.Text(body, label), t.Font(design.TypeBody)).
						TextColor(color).MaxLines(2).Ellipsis("…")
					design.Apply(ui.Text(body, localized(locale,
						"会话、文件和记忆都属于这个文件夹。",
						"Sessions, files and memory belong to this folder.")),
						t.TextMuted(design.TypeXXS)).MaxLines(2)
				})
			change := ui.Button(body, localized(locale, "更改…", "Change…"))
			design.Apply(change, t.SecondaryButton())
			if change.Clicked() {
				changeWorkFolder(st, runtime, locale)
			}
		})
		if st.status != "" {
			statusBadgesRow(body, t, st.status, st.statusErr)
		}
	})
}

// changeWorkFolder runs the native folder picker and makes the chosen folder
// current. A cancelled picker changes nothing. A registered folder is reused by
// the backend (Create returns it and makes it current), so choosing the current
// folder is a harmless no-op rather than a duplicate. MCP connectors are
// workspace-scoped, so the switch triggers their rebuild; the shell redraws
// from the new current workspace on the next frame.
func changeWorkFolder(st *workFolderState, runtime uinative.UIRuntime, locale contracts.LocaleService) {
	dir, err := WorkFolderDirectoryPicker(localized(locale, "选择工作文件夹", "Choose a workspace folder"))
	if err != nil {
		st.status = localized(locale, "无法打开目录选择器：", "Could not open the folder picker: ") + err.Error()
		st.statusErr = true
		runtime.Invalidate()
		return
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	created, err := st.workspace.Create(context.Background(), "", dir)
	if err != nil {
		st.status = localized(locale, "无法更改工作文件夹：", "Could not change the work folder: ") + err.Error()
		st.statusErr = true
		runtime.Invalidate()
		return
	}
	if strings.TrimSpace(created.ID) == "" {
		st.status = localized(locale, "无法更改工作文件夹：", "Could not change the work folder: ") +
			localized(locale, "没有登记工作文件夹", "no folder was registered")
		st.statusErr = true
		runtime.Invalidate()
		return
	}
	// Create already makes the folder current; Open states the switch
	// explicitly, so a backend that changes that contract still ends here.
	if _, err := st.workspace.Open(context.Background(), created.ID); err != nil {
		st.status = localized(locale, "无法更改工作文件夹：", "Could not change the work folder: ") + err.Error()
		st.statusErr = true
		runtime.Invalidate()
		return
	}
	st.status = localized(locale, "工作文件夹已更改", "Work folder changed")
	st.statusErr = false
	mcpreload.Trigger(st.kernel)
	runtime.Invalidate()
}
