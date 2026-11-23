package uiroot

// The desktop startup workspace gate: before the shell renders, every launch
// asks which folder this session belongs to. It is the DSH-style single-folder
// model of WORKSPACE-UX: one open folder drives the sidebar, the files
// inspector, memory and automation, and the multi-workspace tree is gone from
// the chrome (the backend registry still keeps every workspace).
//
// The gate is a full-window page, not a modal: nothing behind it is meaningful
// yet. It renders only when the workspace backend resolves from the kernel
// (contracts.KeyWorkspace), so a host without that module (headless, ACP, the
// snapshot harness) keeps the shell it always had; Config.EnableWorkspaceGate is the host opt-in and Config.SkipWorkspaceGate
// lets an embedder dismiss it explicitly.
//
// Enter continues the preselected (most recent / currently open) folder, a
// click on a recent row opens that folder, and 选择其他文件夹… runs the native
// directory picker and registers the chosen folder through
// WorkspaceService.Create (which derives the name and makes it current). The
// folder is opened through WorkspaceService.Open, so the next frame's chrome
// reads it like any other selection.

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/mcpreload"
	"github.com/zeokeel/zeokeel/internal/lib/workspacerecent"
)

// workspaceGateRecentLimit is how many recent folders the gate lists: three
// fits one page without a scrollbar and covers "the folder I used yesterday"
// in practice (the switch dialog carries the full list).
const workspaceGateRecentLimit = 3

// workspaceGateDirectoryPicker is the gate's directory-picker seam: the default
// opens the native mygo directory dialog with the localized title; tests
// replace it with a scripted selection so no desktop backend is needed.
var workspaceGateDirectoryPicker = func(title string) (string, error) {
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

// workspaceGateActive reports whether this frame renders the gate instead of
// the shell: every launch asks, an embedder can skip it, and a host whose
// kernel has no workspace backend never sees it.
func (r *Root) workspaceGateActive() bool {
	if !r.enableWorkspaceGate || r.skipWorkspaceGate || r.workspaceGateDone {
		return false
	}
	_, ok := r.workspaceService()
	return ok
}

// workspaceService resolves the workspace backend lazily: the UI root exists
// before the assembly loads, so the lookup must happen per frame, not in New.
func (r *Root) workspaceService() (contracts.WorkspaceService, bool) {
	r.mu.Lock()
	kernel := r.kernelContext
	r.mu.Unlock()
	if service, ok := kernel.Get(contracts.KeyWorkspace); ok && service != nil {
		return service, true
	}
	return nil, false
}

// gateChat resolves the chat read model for the recency ordering (sessions are
// bound to the folder they were created in). It mirrors the host's own lookup:
// the explicit Config value first, then the kernel.
func (r *Root) gateChat() contracts.ChatService {
	r.mu.Lock()
	chat := r.chatService
	kernel := r.kernelContext
	r.mu.Unlock()
	if chat != nil {
		return chat
	}
	if fromKernel, ok := kernel.Get(contracts.KeyChat); ok {
		return fromKernel
	}
	return nil
}

// gateRecents reads the registry and the session list and orders them by
// recency. Both reads are in-memory snapshots, safe inside a build pass.
func (r *Root) gateRecents(workspace contracts.WorkspaceService) []workspacerecent.Recent {
	workspaces, err := workspace.List(context.Background())
	if err != nil {
		return nil
	}
	currentID := ""
	if current, ok, err := workspace.Current(context.Background()); err == nil && ok {
		currentID = strings.TrimSpace(current.ID)
	}
	var sessions []contracts.ChatSessionInfo
	if chat := r.gateChat(); chat != nil {
		sessions = chat.Sessions()
	}
	return workspacerecent.Build(workspaces, sessions, currentID, workspaceGateRecentLimit)
}

// confirmWorkspaceGate dismisses the gate once a workspace is open. Idempotent:
// the Enter shortcut and a click can arrive in the same input burst.
func (r *Root) confirmWorkspaceGate() {
	if r.workspaceGateDone {
		return
	}
	r.workspaceGateDone = true
	r.workspaceGateErr = ""
	r.requestFrame()
}

// openGateWorkspace makes one registered workspace current and continues into
// the shell. A failed open stays on the gate with the error inline.
func (r *Root) openGateWorkspace(workspace contracts.WorkspaceService, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if _, err := workspace.Open(context.Background(), id); err != nil {
		r.workspaceGateErr = r.tr("无法打开工作文件夹：", "Could not open the folder: ") + err.Error()
		r.requestFrame()
		return
	}
	// MCP connectors are workspace-scoped: rebuild them for the chosen folder.
	mcpreload.Trigger(r.kernelContext)
	r.confirmWorkspaceGate()
}

// pickGateWorkspace runs the native folder picker, registers the chosen folder
// (Create derives the name and makes it current) and continues. A cancelled
// picker changes nothing; a failure stays on the gate with the error inline.
func (r *Root) pickGateWorkspace(workspace contracts.WorkspaceService) {
	dir, err := workspaceGateDirectoryPicker(r.tr("选择工作文件夹", "Choose a workspace folder"))
	if err != nil {
		r.workspaceGateErr = r.tr("无法打开目录选择器：", "Could not open the folder picker: ") + err.Error()
		r.requestFrame()
		return
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	created, err := workspace.Create(context.Background(), "", dir)
	if err != nil {
		r.workspaceGateErr = r.tr("无法打开工作文件夹：", "Could not open the folder: ") + err.Error()
		r.requestFrame()
		return
	}
	if strings.TrimSpace(created.ID) == "" {
		r.workspaceGateErr = r.tr("无法打开工作文件夹：", "Could not open the folder: ") + r.tr("没有登记工作文件夹", "no folder was registered")
		r.requestFrame()
		return
	}
	// MCP connectors are workspace-scoped: rebuild them for the new folder.
	mcpreload.Trigger(r.kernelContext)
	r.confirmWorkspaceGate()
}

// buildWorkspaceGate renders the startup page: the product name, the one
// decision (which folder), the recent folders, the primary continue action and
// the native folder picker. Tokens and uikit only; the layout is the approved
// frame .cache/workspace-preview/01-startup.png.
func (r *Root) buildWorkspaceGate(c *ui.Context) {
	workspace, ok := r.workspaceService()
	if !ok {
		return
	}
	t := design.Of(c)
	recent := r.gateRecents(workspace)
	// Preselect the open folder, else the most recent one. Indices are clamped
	// every frame because the registry can change while the gate is up (a folder
	// registered from another window, a failed read).
	if r.gateSelected >= len(recent) {
		r.gateSelected = 0
	}
	if r.gateSelected < 0 {
		r.gateSelected = -1
	}
	for index, entry := range recent {
		if entry.Current {
			r.gateSelected = index
			break
		}
	}
	if r.gateSelected < 0 && len(recent) > 0 {
		r.gateSelected = 0
	}

	// Enter continues the preselected folder. The gate's own shortcut: the
	// window's chrome shortcuts are not bound while the gate is up, so Escape,
	// the palette and new-session chords cannot reach behind it.
	if len(recent) > 0 {
		c.OnShortcut(0, ui.KeyEnter, func() {
			if r.gateSelected < 0 || r.gateSelected >= len(recent) {
				return
			}
			r.openGateWorkspace(workspace, recent[r.gateSelected].Workspace.ID)
		})
	} else {
		c.OnShortcut(0, ui.KeyEnter, func() { r.pickGateWorkspace(workspace) })
	}

	name := r.appName
	ui.Column(c).Background(t.BgBase).FillWidth().FillHeight().
		Justify(ui.Center).AlignItems(ui.Center).Children(func() {
		design.Apply(ui.Column(c), t.Gap(design.Space5)).
			Width(design.DialogWidthM).MaxWidthPercent(92).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeS)).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, r.tr("打开工作文件夹", "Open a workspace folder")),
				t.Font(design.TypeXL)).TextColor(t.LabelPrimary).
				Role(ui.RoleHeading).Level(1)
			design.Apply(ui.Text(c, r.tr(
				"选择一个文件夹作为这次会话的工作区。会话、文件和记忆都只属于它。",
				"Choose a folder as this session's workspace. Sessions, files and memory belong to it.")),
				t.Font(design.TypeBody)).TextColor(t.LabelSecondary)

			if len(recent) > 0 {
				design.Apply(ui.Column(c), t.Panel()).FillWidth().
					Padding(design.Space3).Gap(design.Space1).Children(func() {
					design.Apply(ui.Text(c, r.tr("最近使用", "Recently used")), t.TextMuted(design.TypeXXS))
					r.buildGateRecentList(c, t, recent)
				})
			}

			ui.Row(c).FillWidth().Gap(design.Space2).Children(func() {
				if len(recent) > 0 {
					selected := recent[r.gateSelected].Workspace
					label := r.tr("继续使用 ", "Continue with ") + workspaceDisplayName(selected, r.tr("未命名工作文件夹", "Untitled folder"))
					button := ui.PrimaryButton(c, label)
					design.Apply(button, t.PrimaryButton()).Grow(1)
					if button.Clicked() {
						r.openGateWorkspace(workspace, selected.ID)
					}
				}
				chooseLabel := r.tr("选择其他文件夹…", "Choose another folder…")
				choose := ui.Button(c, chooseLabel)
				if len(recent) > 0 {
					design.Apply(choose, t.SecondaryButton())
				} else {
					// Without recents the picker is the only action: it carries
					// the primary emphasis (the one accent block of the page).
					design.Apply(choose, t.PrimaryButton()).Grow(1)
				}
				if choose.Clicked() {
					r.pickGateWorkspace(workspace)
				}
			})

			hint := r.tr("按 Enter 继续；每次启动都会询问。", "Press Enter to continue; you'll be asked on every launch.")
			if len(recent) == 0 {
				hint = r.tr("按 Enter 选择文件夹；每次启动都会询问。", "Press Enter to choose a folder; you'll be asked on every launch.")
			}
			design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXXS)).TextColor(t.LabelTertiary)
			if r.workspaceGateErr != "" {
				design.Apply(ui.Text(c, r.workspaceGateErr), t.TextMuted(design.TypeXS)).
					TextColor(t.DangerText)
			}
		})
	})
}

// buildGateRecentList renders the recent folders as one native List: the
// choice follows the preselection, Enter and a click open the chosen folder,
// and every row carries the name, its root and how long ago it was used.
func (r *Root) buildGateRecentList(c *ui.Context, t *design.Tokens, recent []workspacerecent.Recent) {
	now := time.Now()
	r.gateList.Key = func(row int) any { return recent[row].Workspace.ID }
	r.gateList.Label = func(row int) string {
		return workspaceRecentLabel(r, recent[row], now, r.tr("上次使用", "Last used"))
	}
	r.gateList.Selected = &r.gateSelected
	var body ui.Element
	// The chosen row is the quiet persistent selection tint, not mygo's native
	// accent block (DESIGN §5.2): the list builds with a theme whose accent is
	// the selection tint.
	ui.Themed(c, gateSelectionTheme(t)).Background(ui.Transparent).FillWidth().Children(func() {
		body = ui.List(c, &r.gateList, len(recent)).Rows(func(row ui.ListRow) {
			entry := recent[row.Index]
			r.buildGateRecentRow(row.Context, t, entry, row.Index == r.gateSelected,
				workspaceRecentLabel(r, entry, now, r.tr("上次使用", "Last used")),
				func() { r.openGateWorkspace(r.mustWorkspaceService(), entry.Workspace.ID) })
		})
	})
	if body.Submitted() {
		if r.gateSelected >= 0 && r.gateSelected < len(recent) {
			r.openGateWorkspace(r.mustWorkspaceService(), recent[r.gateSelected].Workspace.ID)
		}
	}
}

// mustWorkspaceService re-resolves the backend for a row click; the gate only
// builds when it resolved, so a miss is a teardown race and opens nothing.
func (r *Root) mustWorkspaceService() contracts.WorkspaceService {
	service, _ := r.workspaceService()
	return service
}

// buildGateRecentRow renders one recent folder: the folder glyph, the name with
// its root below, the age at the row end and the check on the chosen row.
func (r *Root) buildGateRecentRow(c *ui.Context, t *design.Tokens,
	entry workspacerecent.Recent, selected bool, label string, open func()) {
	name := workspaceDisplayName(entry.Workspace, r.tr("未命名工作文件夹", "Untitled folder"))
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space2, design.Space2).Radius(design.RadiusSM).Label(label)
	if selected {
		row = row.Background(t.BgSelected)
	}
	row.Children(func() {
		ui.Icon(c, icons.Folder()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
		design.Apply(ui.Column(c), t.Gap(design.Space0)).Grow(1).MinWidth(design.Space0).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			root := strings.TrimSpace(entry.Workspace.Root)
			if root == "" {
				root = name
			}
			design.Apply(ui.Text(c, root), t.TextMuted(design.TypeXXS)).
				MaxLines(1).Ellipsis("…")
		})
		design.Apply(ui.Text(c, workspaceRecentAge(r, entry)), t.TextMuted(design.TypeXXS)).Shrink(0)
		if selected {
			ui.Icon(c, icons.Check()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.Accent)
		}
	})
	// A click opens the folder: the recent row is a destination, not a choice
	// the user has to confirm again (the primary button continues the
	// preselection for the keyboard path).
	row.ObserveInput(func(ev ui.InputEvent) {
		if ev.Kind != ui.InputPointerUp || ev.Button != 0 {
			return
		}
		open()
	})
}

// workspaceRecentLabel is a recent row's accessible name: its folder, its root
// and its age, so the check-colored choice is never color-only.
func workspaceRecentLabel(r *Root, entry workspacerecent.Recent, now time.Time, currentLabel string) string {
	name := workspaceDisplayName(entry.Workspace, r.tr("未命名工作文件夹", "Untitled folder"))
	parts := []string{name}
	if root := strings.TrimSpace(entry.Workspace.Root); root != "" && root != name {
		parts = append(parts, root)
	}
	if age := workspaceRecentAgeFor(r, entry, now, currentLabel); age != "" {
		parts = append(parts, age)
	}
	return strings.Join(parts, "，")
}

// workspaceRecentAge is the age text of a recent row: the caller's word for the
// open folder, else the relative day ("今天/昨天/3 天前").
func workspaceRecentAge(r *Root, entry workspacerecent.Recent) string {
	return workspaceRecentAgeFor(r, entry, time.Now(), r.tr("上次使用", "Last used"))
}

// workspaceRecentAgeFor is workspaceRecentAge with an explicit clock (the
// accessible label and the visible row agree within one frame).
func workspaceRecentAgeFor(r *Root, entry workspacerecent.Recent, now time.Time, currentLabel string) string {
	if entry.Current {
		return currentLabel
	}
	return workspacerecent.AgeLabel(r.tr, entry.Activity, now)
}

// workspaceDisplayName is a workspace's visible name: the registered name, else
// the root's base name, else the caller's fallback.
func workspaceDisplayName(workspace contracts.Workspace, fallback string) string {
	if name := strings.TrimSpace(workspace.Name); name != "" {
		return name
	}
	if root := strings.TrimSpace(workspace.Root); root != "" {
		if base := strings.TrimSpace(lastPathElement(root)); base != "" {
			return base
		}
		return root
	}
	return fallback
}

// lastPathElement returns the final element of a folder path without importing
// path/filepath into the layout file twice (the value is display-only).
func lastPathElement(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

// uikit.T is the product's one localization helper; the gate keeps its own tr
// through the root's locale, so this reference keeps the import honest when a
// future copy needs it.

// gateSelectionTheme is the local theme the recent list builds with: mygo
// paints a chosen row with Theme.Accent/AccentText, so an accent whose color is
// the selection tint turns the native chosen fill into the quiet persistent
// selection (BgSelected fill, LabelPrimary text), as the sidebar's lists do.
func gateSelectionTheme(t *design.Tokens) *ui.Theme {
	theme := *t.Theme()
	theme.Accent = t.BgSelected
	theme.AccentHover = t.BgSelected
	theme.AccentPressed = t.BgSelected
	theme.InactiveAccent = t.BgSelected
	theme.AccentText = t.LabelPrimary
	return &theme
}
