package shell

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/store"
)

// Chrome geometry and the compact controls shared by the collapsed left
// sidebar and the right sidebar header.
const (
	// sidebarRailWidth is the collapsed sidebar: 64px of icons only.
	sidebarRailWidth float32 = 64
	// chromeIconSize is a header icon button's 28x28 hit area, as DSH's
	// iconButton controls are.
	chromeIconSize float32 = 28
)

// Chrome icon artwork: simple Lucide-style line icons for the controls
// mygo's icon package does not carry (split, fullscreen, right panel).
var (
	chromeIconSplit = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="7.5" height="16" rx="1.5"/><rect x="13.5" y="4" width="7.5" height="16" rx="1.5"/></svg>`))

	chromeIconFullscreen = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3H5a2 2 0 0 0-2 2v3M16 3h3a2 2 0 0 1 2 2v3M8 21H5a2 2 0 0 1-2-2v-3M16 21h3a2 2 0 0 0 2-2v-3"/></svg>`))

	chromeIconPanelRight = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" x2="15" y1="3" y2="21"/></svg>`))
)

// installChromeShortcuts registers the chrome shortcuts; it runs every
// frame alongside the task shortcuts.
//
//	Cmd/Ctrl+B  collapse or expand the left sidebar
func (a *App) installChromeShortcuts(c *ui.Context) {
	// Through bindShortcut, so the editable "Toggle left sidebar" row in the
	// shortcuts dialog actually changes the registered chord.
	a.bindShortcut(c, "toggle_sidebar", ui.Cmd, ui.KeyB, func() { a.toggleSidebar() })
}

// toggleSidebar is Cmd/Ctrl+B. The state is in memory only.
func (a *App) toggleSidebar() { a.sidebarCollapsed = !a.sidebarCollapsed }

// chromeSettingsShortcuts keeps Cmd/Ctrl+B working while the Settings
// modal is up: a modal makes the window's own shortcuts wait, so this
// pointer-transparent overlay above it registers the key itself. Without
// it a collapsed sidebar would have no way back from Settings until the
// modal closed.
func (a *App) chromeSettingsShortcuts(c *ui.Context) {
	ui.Overlay(c, func() {
		hotkey := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough()
		if a.overlayShortcut(hotkey, "toggle_sidebar", ui.Cmd, ui.KeyB) {
			a.toggleSidebar()
		}
	})
}

// overlayShortcut registers mods+key for an element above a modal, applying
// the persisted override for id exactly like bindShortcut does for window
// shortcuts. Without this the Settings modal kept the hard-coded default and
// an edited chord did nothing while Settings was open.
func (a *App) overlayShortcut(el ui.Element, id string, mods ui.Modifiers, key ui.Key) bool {
	if raw := a.shortcutOverride(id); raw != "" {
		if m, k, ok := parseShortcut(raw); ok {
			mods, key = m, k
		}
	}
	return el.OverlayShortcut(mods, key)
}

// collapseSidebarButton is the brand row's 28x28 collapse control (DSH's
// "Collapse sidebar"). It exists only in the live shell.
func (a *App) collapseSidebarButton(c *ui.Context, t *design.Tokens) {
	button := ui.Row(c).Size(chromeIconSize, chromeIconSize).Center().
		Radius(design.RadiusSM).Label(a.tr("Collapse sidebar", "收起侧边栏")).
		Role(ui.RoleButton).Focusable()
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icons.PanelLeft()).FontSize(16).TextColor(t.LabelSecondary)
	})
	button.OnClick(func() { a.sidebarCollapsed = true })
}

// sidebarRail is the collapsed 64px sidebar: the brand mark, an always
// visible expand control, the primary actions and the workspace roots as
// icon rows. Every screen shares this rail, so the reopen control is
// always reachable; Cmd/Ctrl+B also toggles the sidebar from anywhere.
func (a *App) sidebarRail(c *ui.Context, t *design.Tokens) {
	design.Apply(ui.Column(c), t.Sidebar()).Width(sidebarRailWidth).Children(func() {
		ui.Row(c).Height(48).Center().Children(func() {
			ui.Image(c, mark).Size(24, 24)
		})
		ui.Row(c).PaddingX(design.Space2).Children(func() {
			expand := chromeIconRow(c, t, a.tr("Expand sidebar", "展开侧边栏"), icons.PanelLeft(), false)
			expand.OnClick(func() { a.sidebarCollapsed = false })
		})
		ui.Column(c).Padding(design.Space1, design.Space2, design.Space2, design.Space2).
			Gap(design.Space1).Children(func() {
			newButton := chromeIconRow(c, t, a.T("shell.new_session"), icons.CirclePlus(), false)
			newButton.OnClick(func() { a.newSession() })

			plugins := chromeIconRow(c, t, a.T("shell.plugins"), icons.Plugin(), a.Screen == Plugins)
			plugins.OnClick(func() { a.Screen = Plugins })
			automation := chromeIconRow(c, t, a.T("shell.automation_tasks"), icons.Clock(), a.Screen == Automation)
			automation.OnClick(func() { a.Screen = Automation })

			ui.Box(c).Height(1).Margin(design.Space1, design.Space1, design.Space1, design.Space1).
				Background(t.BorderL2)

			if workspaces, err := a.workspaceList(); err == nil {
				current := a.Ctrl.CurrentWorkspaceID()
				for _, ws := range workspaces {
					workspace := ws
					item := chromeIconRow(c, t, workspace.Name, icons.Folder(), workspace.ID == current)
					item.OnClick(func() {
						if workspace.ID == a.Ctrl.CurrentWorkspaceID() {
							a.setWorkspaceCollapsed(workspace.ID, !a.wsCollapsed[workspace.ID])
							return
						}
						a.switchWorkspace(workspace.ID)
					})
				}
			}
			add := chromeIconRow(c, t, a.T("workspace.add_workspace"), icons.FolderPlus(), false)
			add.OnClick(func() { a.beginAddWorkspace() })
		})
		ui.Text(c, "").Grow(1)
		ui.Row(c).Padding(0, design.Space2, design.Space2, design.Space2).Children(func() {
			more := chromeIconRow(c, t, a.T("menu.more"), icons.More(), a.moreOpen)
			more.Expanded(a.moreOpen)
			more.OnClick(func() {
				a.closeSelectors()
				a.moreOpen = !a.moreOpen
			})
			ui.Popover(c, more, &a.moreOpen, func() { a.moreMenu(c, t) })
		})
	})
}

// chromeIconRow is one 48x36 icon-only rail row. The label is its
// accessible name and the tester's click target; there is no visible text,
// which is what "icons only" means.
func chromeIconRow(c *ui.Context, t *design.Tokens, label string, icon *ui.SVG, active bool) ui.Element {
	row := ui.Row(c).Size(48, 36).Center().Radius(design.RadiusSM)
	if active {
		row = row.Background(t.BgHover)
	} else if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row = row.Label(label).Role(ui.RoleButton).Focusable()
	if icon != nil {
		row.Children(func() {
			color := t.LabelSecondary
			if active {
				color = t.LabelPrimary
			}
			ui.Icon(c, icon).FontSize(16).TextColor(color)
		})
	}
	return row
}

// chromeHeaderButton is a 28x28 icon button for the right sidebar header.
// active tints the control, as Split and Fullscreen do while on.
func chromeHeaderButton(c *ui.Context, t *design.Tokens, label string, icon *ui.SVG, active bool, onClick func()) ui.Element {
	return chromeHeaderButtonWidth(c, t, label, icon, active, chromeIconSize, onClick)
}

// chromeHeaderButtonWidth is chromeHeaderButton with an explicit width. The
// right-panel header uses it to keep all of its controls inside a 220px panel
// without letting the collapse control shrink; Shrink(0) makes the width a
// hard target rather than a share of the remaining row.
func chromeHeaderButtonWidth(c *ui.Context, t *design.Tokens, label string, icon *ui.SVG, active bool, width float32, onClick func()) ui.Element {
	button := ui.Row(c).Size(width, chromeIconSize).Shrink(0).Center().
		Radius(design.RadiusSM).Label(label).Role(ui.RoleButton).Focusable()
	if active {
		button = button.Background(t.BgHover)
	} else if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		color := t.LabelSecondary
		if active {
			color = t.LabelPrimary
		}
		ui.Icon(c, icon).FontSize(15).TextColor(color)
	})
	button.OnClick(onClick)
	return button
}

// workspaceDropBefore reports whether the pointer is over the top half of
// a 34px workspace row, which drops the dragged workspace before this row;
// the bottom half drops after it.
func workspaceDropBefore(row ui.Element) bool {
	_, y, over := row.PointerPosition()
	if !over {
		return true
	}
	return y < workspaceRowHeight/2
}

// workspaceRowHeight is the workspace browser row height.
const workspaceRowHeight float32 = 34

// formatCreatedAt is the hover card's absolute creation time, in local
// time and language-neutral digits as DSH's date template is.
func formatCreatedAt(created time.Time) string {
	if created.IsZero() {
		return ""
	}
	local := created.Local()
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d",
		local.Year(), int(local.Month()), local.Day(), local.Hour(), local.Minute())
}

// workspaceList returns the workspace browser's order: the persisted
// ui.workspace_order first, then any workspace it does not name in store
// order.
func (a *App) workspaceList() ([]store.Workspace, error) {
	if a.Ctrl == nil {
		return nil, nil
	}
	list, err := a.Ctrl.ListWorkspaces(context.Background())
	if err != nil {
		return nil, err
	}
	return a.orderWorkspaces(list), nil
}

// chromeToast is a short live-only confirmation. It is a helper rather
// than a catalog entry so the chrome work did not need i18n.go.
func (a *App) chromeToast(c *ui.Context, en, zh string) {
	c.Toast(a.tr(en, zh))
}
