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
	// sidebarRailWidth is the brand's collapsed sidebar: the 64 DIP icon rail,
	// the theme's Space(16) at the brand's 4 DIP grid.
	sidebarRailWidth float32 = 64
	// chromeIconSize is a header icon button's 28x28 hit area: the theme's
	// Space(7), the smallest desktop control that still clears WCAG's 24 DIP
	// target.
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

// sidebarBrandRow is the sidebar's top row: the brand mark, the ZeoKeel
// wordmark and the collapse control, all on the theme's spacing grid. The
// wordmark is sized in rem off the brand's 14 DIP base instead of a sampled
// pixel value, so it follows the theme's type scale.
func (a *App) sidebarBrandRow(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Padding(th.Space(4), th.Space(3), th.Space(2)).AlignItems(ui.Center).
		Gap(th.Space(2)).Children(func() {
		ui.Image(c, mark).Size(th.Space(6), th.Space(6))
		ui.Text(c, "ZeoKeel").FontSize(th.Rem(1.25)).FixedLineHeight(th.Rem(1.7)).
			FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		if a.Ctrl != nil {
			a.collapseSidebarButton(c, t)
		}
	})
}

// sidebarNewSession is the sidebar's primary action: a native ButtonBase in
// the brand accent, so pointer, Enter/Space, focus and the pressed state are
// mygo's instead of a hand-drawn button. The row keeps the theme's corner and
// spacing; its label is the control's accessible name too.
func (a *App) sidebarNewSession(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Padding(0, th.Space(3), th.Space(2)).Children(func() {
		button := ui.ButtonBase(c).Height(th.Space(9)).Grow(1).Radius(th.Radius).
			Background(t.Accent).Label(a.T("shell.new_session"))
		switch {
		case button.Pressed():
			button = button.Background(t.AccentPressed)
		case button.Hovered():
			button = button.Background(t.AccentHover)
		}
		button.Children(func() {
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.CirclePlus()).Size(th.Space(4), th.Space(4)).
					TextColor(t.AccentText)
				ui.Text(c, a.T("shell.new_session")).FontSize(th.FontSize).
					FixedLineHeight(th.Space(5)).FontWeight(500).TextColor(t.AccentText)
			})
		})
		if button.Clicked() {
			a.newSession()
		}
	})
}

// collapseSidebarButton is the brand row's collapse control: a native
// ButtonBase at the theme's control size, so it takes the focus and Enter and
// Space like every other button.
func (a *App) collapseSidebarButton(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	label := a.tr("Collapse sidebar", "收起侧边栏")
	button := ui.ButtonBase(c).Size(th.Space(7), th.Space(7)).Radius(design.RadiusSM).
		Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icons.PanelLeft()).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
	})
	button.OnClick(func() { a.sidebarCollapsed = true })
}

// sidebarRail is the collapsed 64 DIP sidebar: the brand mark, an always
// visible expand control, the primary actions and the workspace roots as icon
// rows. Every screen shares this rail, so the reopen control is always
// reachable; Cmd/Ctrl+B also toggles the sidebar from anywhere. The rows are
// native ButtonBases so each keeps the framework's focus, Enter/Space and
// Tooltip behavior.
func (a *App) sidebarRail(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	design.Apply(ui.Column(c), t.Sidebar()).Width(sidebarRailWidth).Children(func() {
		ui.Row(c).Height(th.Space(12)).Center().Children(func() {
			ui.Image(c, mark).Size(th.Space(6), th.Space(6))
		})
		ui.Row(c).PaddingX(th.Space(2)).Children(func() {
			expand := chromeIconRow(c, t, a.tr("Expand sidebar", "展开侧边栏"), icons.PanelLeft(), false)
			expand.OnClick(func() { a.sidebarCollapsed = false })
		})
		ui.Column(c).Padding(th.Space(1), th.Space(2), th.Space(2), th.Space(2)).
			Gap(th.Space(1)).Children(func() {
			newButton := chromeIconRow(c, t, a.T("shell.new_session"), icons.CirclePlus(), false)
			newButton.OnClick(func() { a.newSession() })

			plugins := chromeIconRow(c, t, a.T("shell.plugins"), icons.Plugin(), a.Screen == Plugins)
			plugins.OnClick(func() { a.Screen = Plugins })
			automation := chromeIconRow(c, t, a.T("shell.automation_tasks"), icons.Clock(), a.Screen == Automation)
			automation.OnClick(func() { a.Screen = Automation })

			ui.Box(c).Height(1).Margin(th.Space(1)).Background(t.BorderL2)

			if workspaces, err := a.workspaceList(); err == nil && a.Ctrl != nil {
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
		ui.Spacer(c).Grow(1)
		ui.Row(c).Padding(0, th.Space(2), th.Space(2), th.Space(2)).Children(func() {
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

// chromeIconRow is one icon-only rail row: a native ButtonBase at the theme's
// rail metrics. The label is its accessible name, the tooltip and the
// tester's click target; there is no visible text, which is what "icons only"
// means.
func chromeIconRow(c *ui.Context, t *design.Tokens, label string, icon *ui.SVG, active bool) ui.Element {
	th := t.Theme()
	row := ui.ButtonBase(c).Size(th.Space(12), th.Space(9)).Radius(design.RadiusSM).
		Label(label).Tooltip(label)
	switch {
	case active:
		row = row.Background(t.BgHover)
	case row.Hovered():
		row = row.Background(t.BgHover)
	}
	if icon != nil {
		row.Children(func() {
			color := t.LabelSecondary
			if active {
				color = t.Accent
			}
			ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(color)
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
