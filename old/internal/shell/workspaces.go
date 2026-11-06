package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/icons"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// restoreWorkspace applies the persisted ui.workspace selection once per
// App. The static shell (Ctrl == nil) never reads it.
func (a *App) restoreWorkspace() {
	if a.Ctrl == nil || a.workspaceRestored {
		return
	}
	a.workspaceRestored = true
	ctx := context.Background()
	_, _ = a.Ctrl.RestoreWorkspace(ctx)
	if a.Ctrl.CurrentWorkspaceID() == "" {
		// A fresh store has no preference yet: show the first workspace so
		// the sidebar and Files tab have a root.
		_, _, _ = a.Ctrl.CurrentWorkspace(ctx)
	}
	a.loadWorkspaceCollapsed()
}

// settingWorkspaceCollapsed stores the ids of collapsed workspaces as a
// comma-separated list, so DSH's per-workspace expansion survives a restart
// without a schema change.
const settingWorkspaceCollapsed = "ui.workspace_collapsed"

// loadWorkspaceCollapsed restores the persisted per-workspace collapse flags.
func (a *App) loadWorkspaceCollapsed() {
	if a.Ctrl == nil {
		return
	}
	raw, err := a.Ctrl.Store().GetSetting(settingWorkspaceCollapsed)
	if err != nil || strings.TrimSpace(raw) == "" {
		return
	}
	if a.wsCollapsed == nil {
		a.wsCollapsed = make(map[string]bool)
	}
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			a.wsCollapsed[id] = true
		}
	}
}

// setWorkspaceCollapsed updates one workspace's expansion and persists the
// whole collapsed id set.
func (a *App) setWorkspaceCollapsed(id string, collapsed bool) {
	if a.wsCollapsed == nil {
		a.wsCollapsed = make(map[string]bool)
	}
	if collapsed {
		a.wsCollapsed[id] = true
	} else {
		delete(a.wsCollapsed, id)
	}
	if a.Ctrl == nil {
		return
	}
	var ids []string
	for wsID, isCollapsed := range a.wsCollapsed {
		if isCollapsed {
			ids = append(ids, wsID)
		}
	}
	sort.Strings(ids)
	_ = a.Ctrl.Store().SetSetting(settingWorkspaceCollapsed, strings.Join(ids, ","))
}

// The native Sidebar tracks one chosen string for the whole tree, so the app
// destinations and the workspace → session tree share one id space with
// prefixes. The choice is recomputed from the screen every frame
// (sidebarCanonicalSelection) and turned into navigation right after the
// Sidebar is built (sidebarChoice): that is what lets mygo own the selection,
// the arrow keys, the first-letter search and the tree's accessibility tree
// while the app still decides what a choice means.
const (
	sidebarSelHome       = "nav:home"
	sidebarSelPlugins    = "nav:plugins"
	sidebarSelAutomation = "nav:automation"
	sidebarSelWorkspace  = "ws:"
	sidebarSelSession    = "s:"
)

// sidebarCanonicalSelection is the choice the Sidebar shows for the current
// screen: the screen's destination, the open session, or nothing on Home so
// the last session's row stays clickable there.
func (a *App) sidebarCanonicalSelection() string {
	switch a.backgroundScreen() {
	case Home:
		return sidebarSelHome
	case Plugins:
		return sidebarSelPlugins
	case Automation:
		return sidebarSelAutomation
	case Session:
		if a.Ctrl != nil {
			if id := a.Ctrl.CurrentSessionID(); id != "" {
				return sidebarSelSession + id
			}
		}
	}
	return ""
}

// sidebarChoice turns the native Sidebar's new choice into navigation. The
// Sidebar writes the choice while the row builds, so it is read here right
// after the Sidebar returns and then reset to the canonical choice: the
// highlight follows what the screen shows instead of staying on the row that
// happened to be clicked. The native widget has already moved the keyboard
// selection, so arrow keys navigate too, as a desktop source list does.
func (a *App) sidebarChoice(c *ui.Context, t *design.Tokens) {
	choice := a.sidebarSel
	clicked := a.sidebarChosenByPointer
	a.sidebarChosenByPointer = false
	defer func() { a.sidebarSel = a.sidebarCanonicalSelection() }()
	switch {
	case choice == sidebarSelHome:
		a.goHome()
	case choice == sidebarSelPlugins:
		a.closeComposerPopovers()
		a.Screen = Plugins
	case choice == sidebarSelAutomation:
		a.closeComposerPopovers()
		a.Screen = Automation
	case strings.HasPrefix(choice, sidebarSelWorkspace):
		a.wsHoverID = ""
		id := strings.TrimPrefix(choice, sidebarSelWorkspace)
		if a.Ctrl == nil || id == "" {
			return
		}
		if clicked && id == a.Ctrl.CurrentWorkspaceID() {
			// A click on the open workspace toggles its sessions, as the
			// source list always did; the chevron does the same from the
			// keyboard.
			a.setWorkspaceCollapsed(id, !a.wsCollapsed[id])
			return
		}
		// Choosing another workspace opens it. Choosing the open one from
		// the keyboard only shows its sessions, so an arrow key can never
		// hide the list under the selection.
		a.setWorkspaceCollapsed(id, false)
		if id != a.Ctrl.CurrentWorkspaceID() {
			a.switchWorkspace(id)
		}
	case strings.HasPrefix(choice, sidebarSelSession):
		a.wsHoverID = ""
		if id := strings.TrimPrefix(choice, sidebarSelSession); id != "" {
			a.openSessionAsync(c, id)
		}
	}
}

// goHome returns to the Home screen from a session, a plugin page or the
// automation page. It only changes the destination: the next frame's
// syncDraftScope parks the session composer buffer on that session and
// restores the Home draft, so leaving never drops either side's text (PD-11).
func (a *App) goHome() {
	a.Screen = Home
	a.moreOpen = false
	a.closeSelectors()
	a.closeComposerPopovers()
}

// sidebarNavItems are the app destinations as native Sidebar items: the
// Sidebar's arrows, first-letter search, focus ring and accessibility tree
// cover them. Home is first so a session always has a visible way back to the
// Home composer (PD-11).
func (a *App) sidebarNavItems(c *ui.Context, t *design.Tokens) {
	ui.SidebarItem(c, sidebarSelHome, icons.NewChat(), a.T("shell.home"))
	ui.SidebarItem(c, sidebarSelPlugins, icons.Plugin(), a.T("shell.plugins"))
	ui.SidebarItem(c, sidebarSelAutomation, icons.Clock(), a.T("shell.automation_tasks"))
}

// sidebarWorkspacesHeader is the "Workspaces" strip above the tree. A native
// Sidebar has no section header that carries actions, so the strip is a themed
// row of native buttons inside the Sidebar's scrolling column; the tree below
// is still made of Sidebar items.
func (a *App) sidebarWorkspacesHeader(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Height(th.Space(9)).Margin(th.Space(4), 0, 0, 0).
		PaddingX(th.Space(2)).Gap(th.Space(1)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.T("shell.workspaces")).FontSize(th.FontSize - 1).FontWeight(600).
			TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
		search := a.sidebarHeaderButton(c, t, icons.Search(), a.T("shell.search_sessions"))
		search.OnClick(func() { a.openInlineSearch() })
		sliders := a.sidebarHeaderButton(c, t, icons.Sliders(), a.T("shell.view_options"))
		sliders.OnClick(func() { a.viewOptionsOpen = !a.viewOptionsOpen })
		// The View options menu is a plain Popover: its body paints the shared
		// branded panel, clamped to the sidebar column.
		ui.Popover(c, sliders, &a.viewOptionsOpen, func() { a.viewOptionsPopover(c, t) })
		add := a.sidebarHeaderButton(c, t, icons.FolderPlus(), a.T("shell.add_workspace"))
		add.OnClick(func() { a.beginAddWorkspace() })
	})
}

// sidebarHeaderButton is one icon control of the Workspaces strip: a native
// ButtonBase at the theme's control size with an accessible name, a tooltip and
// the brand hover tint.
func (a *App) sidebarHeaderButton(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string) ui.Element {
	th := t.Theme()
	button := ui.ButtonBase(c).Size(th.Space(7), th.Space(7)).Radius(design.RadiusSM).
		Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icon).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.LabelSecondary)
	})
	return button
}

// workspaceTree splits workspaces into roots and a parent -> children map by
// filesystem ancestry: a workspace whose root lies inside another workspace's
// root nests under the closest such ancestor. Roots with an empty path stay
// top-level.
func workspaceTree(workspaces []store.Workspace) (map[string][]store.Workspace, []store.Workspace) {
	children := make(map[string][]store.Workspace)
	roots := make([]store.Workspace, 0, len(workspaces))
	rootOf := func(ws store.Workspace) string {
		r := strings.TrimSpace(ws.Root)
		if r == "" {
			return ""
		}
		return filepath.Clean(r)
	}
	for _, ws := range workspaces {
		path := rootOf(ws)
		parent := ""
		parentLen := -1
		if path != "" {
			for _, other := range workspaces {
				if other.ID == ws.ID {
					continue
				}
				otherPath := rootOf(other)
				if otherPath == "" || otherPath == path {
					continue
				}
				rel, err := filepath.Rel(otherPath, path)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				if len(otherPath) > parentLen {
					parent, parentLen = other.ID, len(otherPath)
				}
			}
		}
		if parent == "" {
			roots = append(roots, ws)
		} else {
			children[parent] = append(children[parent], ws)
		}
	}
	return children, roots
}

// sidebarTree fills the native Sidebar with the workspace → session tree: one
// item per workspace, whose children carry the disclosure and the inline
// actions, then that workspace's sessions as indented items of the same
// Sidebar. The grouping modes (By workspace / Workspace Tree / In one list),
// the archive filter and the inline search keep their behavior.
func (a *App) sidebarTree(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	// Search sessions expands an inline field and replaces the tree with
	// matching rows while it has a query; Cmd/Ctrl+K stays the palette modal.
	if a.inlineSearch {
		a.sidebarSearchField(c, t)
		if strings.TrimSpace(a.inlineQuery) != "" {
			a.inlineSearchResults(c, t)
			return
		}
	}
	ctx := context.Background()
	workspaces, err := a.workspaceList()
	if err != nil {
		ui.Text(c, a.T("workspace.could_not_load_workspaces")).
			FontSize(t.Theme().FontSize - 2).TextColor(t.LabelTertiary).
			PaddingX(t.Theme().Space(2))
		return
	}
	if len(workspaces) == 0 {
		a.sidebarWorkspacesEmpty(c, t)
		return
	}
	current := a.Ctrl.CurrentWorkspaceID()
	group := normalizeGroupBy(a.groupBy)
	includeArchived := a.includeArchived()

	if group == groupByList {
		sessions, sessionErr := a.Ctrl.SessionsForWorkspace(ctx, "", includeArchived, a.manualSort)
		if sessionErr != nil {
			a.sidebarSessionNotice(c, t, sessionErr, 0)
			return
		}
		sessions = a.orderManualSessions(a.pinnedFirst(a.filterSessions(sessions)))
		if len(sessions) == 0 {
			a.sidebarSessionNotice(c, t, nil, 0)
			return
		}
		for _, sess := range sessions {
			// The flat grouping has no workspace headers: every row is a
			// top-level item of the tree.
			a.sidebarSessionRow(c, t, sess, 0, 1)
		}
		return
	}

	sessions, sessionErr := a.Ctrl.SessionsForWorkspace(ctx, current, includeArchived, a.manualSort)
	if sessionErr == nil {
		sessions = a.orderManualSessions(a.pinnedFirst(a.filterSessions(sessions)))
	}
	if group == groupByTree {
		// Workspace Tree nests child workspaces under their ancestor and lists
		// every workspace's own sessions; every workspace can still collapse
		// and that choice persists.
		children, roots := workspaceTree(workspaces)
		var render func(ws store.Workspace, depth int)
		render = func(ws store.Workspace, depth int) {
			wsSessions, wsErr := a.Ctrl.SessionsForWorkspace(ctx, ws.ID, includeArchived, a.manualSort)
			if wsErr == nil {
				wsSessions = a.orderManualSessions(a.pinnedFirst(a.filterSessions(wsSessions)))
			}
			a.sidebarWorkspaceRow(c, t, ws, ws.ID == current, wsSessions, wsErr, depth, true, func() {
				for _, child := range children[ws.ID] {
					render(child, depth+1)
				}
			})
		}
		for _, ws := range roots {
			render(ws, 0)
		}
		return
	}
	for _, ws := range workspaces {
		a.sidebarWorkspaceRow(c, t, ws, ws.ID == current, sessions, sessionErr, 0, false, nil)
	}
}

// sidebarWorkspaceRow is one workspace of the tree: a native SidebarItem whose
// Children compose the disclosure chevron, the inline new-session action and
// the workspace menu, all always visible (a desktop control is not hover-only),
// with the workspace hover card, a right-click menu and drag/drop reordering.
// While expanded, the workspace's sessions and any child workspaces follow it.
func (a *App) sidebarWorkspaceRow(c *ui.Context, t *design.Tokens, ws store.Workspace, selected bool, sessions []store.Session, sessionErr error, depth int, forceExpanded bool, childNodes func()) {
	th := t.Theme()
	indent := th.Space(float32(depth) * 4)
	expanded := !a.wsCollapsed[ws.ID] && (forceExpanded || selected)
	icon := icons.Folder()
	if expanded {
		icon = icons.FolderOpen()
	}
	// The native Sidebar lays every item flat, so the workspace → session
	// nesting is told to assistive technology with Level: the workspace row
	// is a tree item at its depth, its sessions one level below it. Screen
	// readers read the outline hierarchy from it, as they do from mygo's own
	// SidebarSections.
	//
	// The row's inline actions live beside the item, overlaid on its trailing
	// edge, instead of inside it: mygo's SidebarItem is a leaf for assistive
	// technology (named by its content), so buttons inside it would not be
	// exposed to screen readers. As siblings they read as the item's own
	// controls, and the item keeps a clean name and level.
	row := ui.Row(c).Shrink(0)
	if depth == 0 {
		// A little air between workspace groups; nested child workspaces
		// stay tight to their parent.
		row = row.Margin(th.Space(2), 0, 0, 0)
	}
	var item ui.Element
	row.Children(func() {
		item = ui.SidebarItem(c, sidebarSelWorkspace+ws.ID, icon, ws.Name).
			PaddingStart(indent).
			// The actions overlaid at the trailing edge keep their room
			// reserved for the label.
			PaddingEnd(th.Space(19)).
			Grow(1).Level(depth + 1).Expanded(expanded)
		if item.Clicked() {
			a.sidebarChosenByPointer = true
		}
		actions := ui.Row(c).Absolute().Right(th.Space(1)).Top(0).Bottom(0).
			Gap(th.Space(0.5)).AlignItems(ui.Center)
		actions.Children(func() {
			// The native item has no disclosure arrow of its own, so the row
			// carries one as a native button; it only expands or collapses
			// the workspace, while choosing the row opens the workspace.
			label, chevron := a.T("workspace.show_sessions"), icons.ChevronRight()
			if expanded {
				label, chevron = a.T("workspace.hide_sessions"), icons.ChevronDown()
			}
			a.sidebarItemAction(c, t, label, chevron, t.LabelTertiary, func() {
				a.setWorkspaceCollapsed(ws.ID, expanded)
			})
			a.sidebarItemAction(c, t,
				a.tr("New session in ", "在以下位置新建会话：")+ws.Name, icons.Plus(),
				t.LabelSecondary, func() { a.newSessionInWorkspace(ws) })
			a.sidebarItemMenuAction(c, t,
				a.tr("Workspace actions for ", "工作区操作：")+ws.Name, icons.More(),
				t.LabelSecondary, func(m *ui.Menu) { a.workspaceMenu(c, m, ws) })
		})
	})
	// Drag/drop reordering: the row drags its workspace id and takes a drop in
	// its top half before itself, in its bottom half after itself.
	item = item.Drag(ws.ID)
	if src, over := ui.DragOver[string](item); over && src != ws.ID {
		item = sidebarDropEdge(item, workspaceDropBefore(item), t)
	}
	if src, dropped := ui.Drop[string](item); dropped && src != ws.ID {
		a.reorderWorkspace(src, ws.ID, workspaceDropBefore(item))
		a.wsHoverID = ""
	}
	item.PopoverMenu(func(m *ui.Menu) { a.workspaceMenu(c, m, ws) })
	// The hover card is a pointer affordance, not part of a drag: it would
	// otherwise cover the rows the workspace is dragged over.
	if item.Dragging() {
		a.wsHoverID = ""
	} else {
		a.maybeWorkspaceHoverCard(c, t, item, ws)
	}
	if !expanded {
		return
	}
	if childNodes != nil {
		childNodes()
	}
	if sessionErr != nil {
		a.sidebarSessionNotice(c, t, sessionErr, indent)
		return
	}
	if len(sessions) == 0 {
		a.sidebarSessionNotice(c, t, nil, indent)
		return
	}
	sessionIndent := indent + th.Space(6)
	for _, sess := range sessions {
		a.sidebarSessionRow(c, t, sess, sessionIndent, depth+2)
	}
}

// sidebarSessionNotice is the inline "no sessions yet" or load-error line
// under a workspace, indented with its workspace on the theme's grid.
func (a *App) sidebarSessionNotice(c *ui.Context, t *design.Tokens, err error, indent float32) {
	th := t.Theme()
	text := a.T("workspace.no_sessions_yet")
	if err != nil {
		text = a.T("workspace.could_not_load_sessions")
	}
	ui.Text(c, text).FontSize(th.FontSize-2).TextColor(t.LabelTertiary).
		Padding(0, th.Space(2), th.Space(1), indent+th.Space(6))
}

// sidebarWorkspacesEmpty is the tree's first-run empty state: the tree area
// would otherwise be blank under the Workspaces strip, with only unlabeled
// header icons, so the first workspace gets a sentence and a button (UX-09).
func (a *App) sidebarWorkspacesEmpty(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Padding(design.Space3, design.Space2).Gap(th.Space(2)).Children(func() {
		ui.Text(c, a.T("workspace.no_workspaces_yet")).FontSize(th.FontSize - 2).
			TextColor(t.LabelTertiary).MaxLines(2)
		label := a.T("shell.add_workspace")
		button := ui.Button(c, label).Height(th.Space(8)).Label(label)
		button.OnClick(func() { a.beginAddWorkspace() })
	})
}

// sidebarSessionRow is one session of the tree: a native SidebarItem with the
// conversation icon, its relative time and the session menu as an always
// visible inline action. level is its depth in the accessibility tree: 1 in
// the flat grouping, the workspace's level + 1 under a workspace. It is a
// drag/drop source and target in manual order, and a right click opens the
// same menu.
func (a *App) sidebarSessionRow(c *ui.Context, t *design.Tokens, sess store.Session, indent float32, level int) {
	th := t.Theme()
	title := sess.Title
	archived := false
	if sess.Archived {
		title += " · " + a.T("workspace.archived")
		archived = true
	}
	// As with a workspace row, the item is a leaf for assistive technology and
	// its session menu is a sibling overlaid on the trailing edge, so screen
	// readers see a named tree item at its level next to its own button.
	row := ui.Row(c).Shrink(0)
	var item ui.Element
	row.Children(func() {
		item = ui.SidebarItem(c, sidebarSelSession+sess.ID, icons.Message(), title).
			PaddingStart(indent).PaddingEnd(th.Space(16)).
			Grow(1).Level(level)
		if archived {
			item = item.Tooltip(title)
		}
		actions := ui.Row(c).Absolute().Right(th.Space(1)).Top(0).Bottom(0).
			Gap(th.Space(1)).AlignItems(ui.Center)
		actions.Children(func() {
			if when := a.sessionWhen(sess.UpdatedAt); when != "" {
				ui.Text(c, when).FontSize(th.FontSize - 3).FixedLineHeight(th.Space(4)).
					TextColor(t.LabelTertiary)
			}
			if a.sessionRunning(sess.ID) {
				ui.Box(c).Size(th.Space(1.5), th.Space(1.5)).Radius(design.RadiusMax).
					Background(t.Accent)
			}
			// The row keeps the "⋯" affordance as native text: it is the mark
			// the product already used, and a text node stays findable and
			// clickable for keyboard and assistive-technology tests.
			a.sidebarItemGlyphMenuAction(c, t,
				a.menuText("Session actions for "+sess.Title, "会话操作："+sess.Title),
				"⋯", t.LabelSecondary,
				func(m *ui.Menu) { a.workspaceSessionMenu(c, m, sess) })
		})
	})
	// Manual ordering: every session row is a drag source and a drop target.
	item = item.Drag(sess.ID)
	if src, over := ui.DragOver[string](item); over && src != sess.ID {
		item = sidebarDropEdge(item, workspaceDropBefore(item), t)
	}
	if src, dropped := ui.Drop[string](item); dropped && src != sess.ID {
		a.reorderSession(src, sess.ID, workspaceDropBefore(item))
	}
	item.PopoverMenu(func(m *ui.Menu) { a.workspaceSessionMenu(c, m, sess) })
}

// sessionRunning reports whether the open session's turn is still running, for
// the accent dot beside its row.
func (a *App) sessionRunning(id string) bool {
	if a.Ctrl == nil || id != a.Ctrl.CurrentSessionID() {
		return false
	}
	snap := a.Snapshot()
	return snap != nil && snap.Phase == state.PhaseRunning
}

// sidebarItemButton is the base of a native Sidebar item's inline action: an
// 18 DIP ButtonBase at the brand's corner, with an accessible name, a tooltip
// and a hover tint. The actions are always rendered: a desktop control is not
// hover-only, and keyboard users must be able to reach it.
func (a *App) sidebarItemButton(c *ui.Context, t *design.Tokens, label string) ui.Element {
	th := t.Theme()
	button := ui.ButtonBase(c).Size(th.Space(4.5), th.Space(4.5)).Radius(design.RadiusSM).
		Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	return button
}

// sidebarItemAction is an inline action of a Sidebar item: the base button with
// a themed icon in the given tone.
func (a *App) sidebarItemAction(c *ui.Context, t *design.Tokens, label string, icon *ui.SVG, color ui.Color, onClick func()) ui.Element {
	button := a.sidebarItemButton(c, t, label)
	button.Children(func() {
		ui.Icon(c, icon).Size(t.Theme().Space(3.5), t.Theme().Space(3.5)).TextColor(color)
	})
	button.OnClick(onClick)
	return button
}

// sidebarItemMenuAction is sidebarItemAction for a menu: the inline twin of the
// row's right-click menu, so the actions are discoverable without a right
// click.
func (a *App) sidebarItemMenuAction(c *ui.Context, t *design.Tokens, label string, icon *ui.SVG, color ui.Color, build func(*ui.Menu)) ui.Element {
	button := a.sidebarItemButton(c, t, label)
	button.Children(func() {
		ui.Icon(c, icon).Size(t.Theme().Space(3.5), t.Theme().Space(3.5)).TextColor(color)
	})
	button.PopoverMenuButton(build)
	return button
}

// sidebarItemGlyphMenuAction is sidebarItemMenuAction with a text glyph
// instead of an icon, for rows that keep a familiar mark such as "⋯" while
// still being a native button.
func (a *App) sidebarItemGlyphMenuAction(c *ui.Context, t *design.Tokens, label, glyph string, color ui.Color, build func(*ui.Menu)) ui.Element {
	button := a.sidebarItemButton(c, t, label)
	button.Children(func() {
		ui.Text(c, glyph).FontSize(t.Theme().FontSize - 2).FontWeight(600).
			FixedLineHeight(t.Theme().Space(4)).TextColor(color)
	})
	button.PopoverMenuButton(build)
	return button
}

// sidebarDropEdge paints the insert edge of a row a drag is over: the top
// border inserts before the row, the bottom one after it.
func sidebarDropEdge(item ui.Element, before bool, t *design.Tokens) ui.Element {
	if before {
		return item.BorderWidth(2, 0, 0, 0).BorderColor(t.Accent)
	}
	return item.BorderWidth(0, 0, 2, 0).BorderColor(t.Accent)
}

// workspaceMenu fills a workspace row's menu: rename, the accessible
// move up/down fallback for drag reordering, and delete.
func (a *App) workspaceMenu(c *ui.Context, m *ui.Menu, ws store.Workspace) {
	a.wsHoverID = ""
	if m.Item(a.T("workspace.rename_workspace")).Chosen() {
		a.beginRenameWorkspace(ws)
	}
	canUp, canDown := a.workspaceMoveTargets(ws.ID)
	if m.Item(a.tr("Move up", "上移")).Disabled(!canUp).Chosen() {
		a.moveWorkspaceBy(ws.ID, -1)
	}
	if m.Item(a.tr("Move down", "下移")).Disabled(!canDown).Chosen() {
		a.moveWorkspaceBy(ws.ID, 1)
	}
	m.Separator()
	if m.Item(a.T("workspace.delete_workspace")).Chosen() {
		a.beginDeleteWorkspace(ws)
	}
}

// workspaceSessionMenu fills a session row's menu, including pin/unpin,
// archive and copying the session id.
func (a *App) workspaceSessionMenu(c *ui.Context, m *ui.Menu, sess store.Session) {
	a.wsHoverID = ""
	if m.Item(a.T("workspace.rename")).Chosen() {
		a.beginRename(sess.ID, sess.Title)
	}
	if a.sessionPinned(sess.ID) {
		if m.Item(a.tr("Unpin", "取消置顶")).Chosen() {
			a.setSessionPinned(c, sess.ID, false)
		}
	} else {
		if m.Item(a.tr("Pin", "置顶")).Chosen() {
			a.setSessionPinned(c, sess.ID, true)
		}
	}
	if sess.Archived {
		if m.Item(a.T("workspace.unarchive")).Chosen() {
			a.setSessionArchived(c, sess, false)
		}
	} else {
		if m.Item(a.T("workspace.archive")).Chosen() {
			a.setSessionArchived(c, sess, true)
		}
	}
	if a.manualSort {
		canUp, canDown := a.sessionMoveTargets(sess.ID)
		if m.Item(a.tr("Move up", "上移")).Disabled(!canUp).Chosen() {
			a.moveSessionBy(sess.ID, -1)
		}
		if m.Item(a.tr("Move down", "下移")).Disabled(!canDown).Chosen() {
			a.moveSessionBy(sess.ID, 1)
		}
	}
	if m.Item(a.T("workspace.copy_title")).Chosen() {
		c.WriteClipboard(sess.Title)
		c.Toast(a.T("workspace.copied_session_title"))
	}
	if m.Item(a.tr("Copy session ID", "复制 Session ID")).Chosen() {
		c.WriteClipboard(sess.ID)
		a.chromeToast(c, "Copied session ID", "已复制 Session ID")
	}
	m.Separator()
	if m.Item(a.T("workspace.delete")).Chosen() {
		a.beginDelete(sess.ID, sess.Title)
	}
}

// setSessionArchived flips a session's archive flag and toasts the outcome.
func (a *App) setSessionArchived(c *ui.Context, sess store.Session, archived bool) {
	if a.Ctrl == nil {
		return
	}
	if err := a.Ctrl.SetSessionArchived(context.Background(), sess.ID, archived); err != nil {
		c.Toast(a.T("workspace.could_not_update_the_session"))
		return
	}
	if archived {
		c.Toast(a.T("workspace.session_archived"))
	} else {
		c.Toast(a.T("workspace.session_unarchived"))
	}
}

// switchWorkspace selects id, opens its most recent session and resets the
// Files tab to the new root.
// newSessionInWorkspace creates and opens a session inside ws, mirroring the
// workspace row's "new session" action in DSH.
func (a *App) newSessionInWorkspace(ws store.Workspace) {
	if a.Ctrl == nil {
		a.Screen = Session
		return
	}
	oldScreen := a.Screen
	oldID := a.draftOwnerSessionID()
	ctx := context.Background()
	if err := a.Ctrl.SelectWorkspace(ctx, ws.ID); err != nil {
		return
	}
	id, err := a.Ctrl.CreateSessionInWorkspace(ctx, ws.ID, a.T("session.new_session"))
	if err != nil {
		return
	}
	snap, err := a.Ctrl.OpenSession(ctx, id)
	if err != nil {
		return
	}
	a.SetSnapshot(snap)
	a.applyDefaultAgentPreset()
	a.Screen = Session
	a.moveDraftToSession(oldScreen, oldID, id)
}

// switchWorkspaceFromHome is DSH's hero workspace picker: it selects the
// workspace for a new task, creates a blank session there and carries the Home
// draft into that blank session. It never opens the workspace's latest
// conversation or drops the draft into an existing one.
func (a *App) switchWorkspaceFromHome(c *ui.Context, id string) {
	if a.Ctrl == nil {
		return
	}
	ctx := context.Background()
	if err := a.Ctrl.SelectWorkspace(ctx, id); err != nil {
		return
	}
	a.resetFiles()
	a.viewOptionsOpen = false
	a.wsHoverID = ""
	a.wsPickerOpen = false

	sessionID, err := a.Ctrl.CreateSessionInWorkspace(ctx, id, a.T("session.new_session"))
	if err != nil {
		return
	}
	snap, err := a.Ctrl.OpenSession(ctx, sessionID)
	if err != nil {
		return
	}
	a.SetSnapshot(snap)
	a.applyHomeComposerChoices(sessionID)
	// The hero picker carries the Home draft into the new session, so carry
	// the files staged with that draft too.
	if err := a.attachHomeAttachments(sessionID); err != nil {
		// Mirror sendHomeDraft's failure handling: never leave the empty
		// session it just created behind, and never silently drop the staged
		// files. Keep the Home draft and its chips so the user can remove the
		// bad pick and try the switch again.
		_ = a.Ctrl.DeleteSession(ctx, sessionID)
		a.SetSnapshot(nil)
		a.Screen = Home
		if c != nil {
			c.Toast(fmt.Sprintf(a.menuText("Could not attach file: %v",
				"无法添加附件：%v"), err))
		}
		return
	}
	a.transferDraftToSession(sessionID)
	a.Screen = Session
}

func (a *App) switchWorkspace(id string) {
	if a.Ctrl == nil {
		return
	}
	ctx := context.Background()
	if err := a.Ctrl.SelectWorkspace(ctx, id); err != nil {
		return
	}
	a.resetFiles()
	a.viewOptionsOpen = false
	a.wsHoverID = ""

	sessions, err := a.Ctrl.SessionsForWorkspace(ctx, id, a.includeArchived(), a.manualSort)
	if err == nil && len(sessions) > 0 {
		a.openSession(sessions[0].ID)
		return
	}
	a.SetSnapshot(nil)
	a.Screen = Home
}

// settingWorkspaceOrder is the persisted drag/drop order of workspaces: a
// comma-separated list of workspace ids. It is an ordinary settings row, so
// no schema change is needed; ids missing from it keep their store order
// after the named ones.
const settingWorkspaceOrder = "ui.workspace_order"

// orderWorkspaces applies ui.workspace_order to list, keeping the store
// order for workspaces the key does not name.
func (a *App) orderWorkspaces(list []store.Workspace) []store.Workspace {
	order := a.loadWorkspaceOrder()
	if len(order) == 0 || len(list) == 0 {
		return list
	}
	pos := make(map[string]int, len(order))
	for i, id := range order {
		pos[id] = i
	}
	out := append([]store.Workspace(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, iok := pos[out[i].ID]
		pj, jok := pos[out[j].ID]
		switch {
		case iok && jok:
			return pi < pj
		case iok:
			return true
		case jok:
			return false
		default:
			return false
		}
	})
	return out
}

// loadWorkspaceOrder reads and caches ui.workspace_order.
func (a *App) loadWorkspaceOrder() []string {
	if a.workspaceOrderLoaded {
		return a.workspaceOrder
	}
	a.workspaceOrderLoaded = true
	if a.Ctrl == nil {
		return nil
	}
	raw, err := a.Ctrl.Store().GetSetting(settingWorkspaceOrder)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	order := parts[:0]
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			order = append(order, id)
		}
	}
	a.workspaceOrder = order
	return order
}

// persistWorkspaceOrder writes the ids as ui.workspace_order and updates
// the in-memory cache; the store failure is not fatal for the frame.
func (a *App) persistWorkspaceOrder(ids []string) {
	a.workspaceOrderLoaded = true
	a.workspaceOrder = append([]string(nil), ids...)
	if a.Ctrl == nil {
		return
	}
	_ = a.Ctrl.Store().SetSetting(settingWorkspaceOrder, strings.Join(ids, ","))
}

// reorderWorkspace moves srcID before or after dstID and persists the new
// order. It is the drop handler for a workspace row.
func (a *App) reorderWorkspace(srcID, dstID string, before bool) {
	if a.Ctrl == nil || srcID == "" || srcID == dstID {
		return
	}
	list, err := a.workspaceList()
	if err != nil {
		return
	}
	ids := make([]string, 0, len(list))
	for _, ws := range list {
		if ws.ID != srcID {
			ids = append(ids, ws.ID)
		}
	}
	idx := -1
	for i, id := range ids {
		if id == dstID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	if !before {
		idx++
	}
	out := make([]string, 0, len(ids)+1)
	out = append(out, ids[:idx]...)
	out = append(out, srcID)
	out = append(out, ids[idx:]...)
	a.persistWorkspaceOrder(out)
}

// moveWorkspaceBy moves a workspace one slot up (-1) or down (+1). It is
// the workspace menu's accessible fallback for drag reordering.
func (a *App) moveWorkspaceBy(id string, delta int) {
	if a.Ctrl == nil || delta == 0 {
		return
	}
	list, err := a.workspaceList()
	if err != nil {
		return
	}
	idx := -1
	for i, ws := range list {
		if ws.ID == id {
			idx = i
			break
		}
	}
	target := idx + delta
	if idx < 0 || target < 0 || target >= len(list) {
		return
	}
	list[idx], list[target] = list[target], list[idx]
	ids := make([]string, 0, len(list))
	for _, ws := range list {
		ids = append(ids, ws.ID)
	}
	a.persistWorkspaceOrder(ids)
}

// workspaceMoveTargets reports whether the workspace can move up and/or
// down in the current order, for the menu items' disabled state.
func (a *App) workspaceMoveTargets(id string) (up, down bool) {
	if a.Ctrl == nil {
		return false, false
	}
	list, err := a.workspaceList()
	if err != nil {
		return false, false
	}
	for i, ws := range list {
		if ws.ID == id {
			return i > 0, i < len(list)-1
		}
	}
	return false, false
}

// settingSessionOrder is the persisted manual order of session ids, used when
// View options > Order by is Manual. Session rows are drag sources/targets and
// the row menu offers Move up/down as the keyboard fallback.
const settingSessionOrder = "ui.session_order"

// loadSessionOrder reads and caches ui.session_order.
func (a *App) loadSessionOrder() []string {
	if a.sessionOrderLoaded {
		return a.sessionOrder
	}
	a.sessionOrderLoaded = true
	if a.Ctrl == nil {
		return nil
	}
	raw, err := a.Ctrl.Store().GetSetting(settingSessionOrder)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			a.sessionOrder = append(a.sessionOrder, id)
		}
	}
	return a.sessionOrder
}

// persistSessionOrder writes the ids as ui.session_order.
func (a *App) persistSessionOrder(ids []string) {
	a.sessionOrderLoaded = true
	a.sessionOrder = append([]string(nil), ids...)
	if a.Ctrl == nil {
		return
	}
	_ = a.Ctrl.Store().SetSetting(settingSessionOrder, strings.Join(ids, ","))
}

// orderManualSessions applies the persisted manual order when View options >
// Order by is Manual. Sessions absent from the order keep their store order
// after the named ones.
func (a *App) orderManualSessions(sessions []store.Session) []store.Session {
	if !a.manualSort || len(sessions) < 2 {
		return sessions
	}
	order := a.loadSessionOrder()
	if len(order) == 0 {
		return sessions
	}
	pos := make(map[string]int, len(order))
	for i, id := range order {
		pos[id] = i
	}
	out := append([]store.Session(nil), sessions...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, iok := pos[out[i].ID]
		pj, jok := pos[out[j].ID]
		switch {
		case iok && jok:
			return pi < pj
		case iok:
			return true
		case jok:
			return false
		default:
			return false
		}
	})
	return out
}

// reorderSession moves srcID before or after dstID in the manual order. It
// derives the current order from the visible session list when the setting
// was never written, so the first drag starts from what the user sees.
func (a *App) reorderSession(srcID, dstID string, before bool) {
	if a.Ctrl == nil || srcID == "" || srcID == dstID {
		return
	}
	ctx := context.Background()
	sessions, err := a.Ctrl.SessionsForWorkspace(ctx, a.Ctrl.CurrentWorkspaceID(), a.includeArchived(), true)
	if err != nil {
		return
	}
	ids := make([]string, 0, len(sessions)+len(a.loadSessionOrder()))
	seen := make(map[string]bool)
	for _, id := range a.loadSessionOrder() {
		if id != srcID && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	for _, sess := range sessions {
		if sess.ID != srcID && !seen[sess.ID] {
			ids = append(ids, sess.ID)
			seen[sess.ID] = true
		}
	}
	idx := -1
	for i, id := range ids {
		if id == dstID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	if !before {
		idx++
	}
	out := make([]string, 0, len(ids)+1)
	out = append(out, ids[:idx]...)
	out = append(out, srcID)
	out = append(out, ids[idx:]...)
	a.persistSessionOrder(out)
}

// moveSessionBy moves a session one slot in the manual order.
func (a *App) moveSessionBy(id string, delta int) {
	if a.Ctrl == nil || delta == 0 {
		return
	}
	ctx := context.Background()
	sessions, err := a.Ctrl.SessionsForWorkspace(ctx, a.Ctrl.CurrentWorkspaceID(), a.includeArchived(), true)
	if err != nil {
		return
	}
	order := a.loadSessionOrder()
	ids := make([]string, 0, len(sessions)+len(order))
	seen := make(map[string]bool)
	for _, sid := range order {
		if !seen[sid] {
			ids = append(ids, sid)
			seen[sid] = true
		}
	}
	for _, sess := range sessions {
		if !seen[sess.ID] {
			ids = append(ids, sess.ID)
			seen[sess.ID] = true
		}
	}
	idx := -1
	for i, sid := range ids {
		if sid == id {
			idx = i
			break
		}
	}
	target := idx + delta
	if idx < 0 || target < 0 || target >= len(ids) {
		return
	}
	ids[idx], ids[target] = ids[target], ids[idx]
	a.persistSessionOrder(ids)
}

// sessionMoveTargets reports whether a session can move up/down in the manual
// order, for the menu items' disabled state.
func (a *App) sessionMoveTargets(id string) (up, down bool) {
	if a.Ctrl == nil {
		return false, false
	}
	ctx := context.Background()
	sessions, err := a.Ctrl.SessionsForWorkspace(ctx, a.Ctrl.CurrentWorkspaceID(), a.includeArchived(), true)
	if err != nil {
		return false, false
	}
	sessions = a.orderManualSessions(sessions)
	for i, sess := range sessions {
		if sess.ID == id {
			return i > 0, i < len(sessions)-1
		}
	}
	return false, false
}

// sessionPinnedKey is the persisted pin flag of one session. Pinning uses
// a settings row rather than a schema column; the sidebar sorts pinned
// sessions first.
func sessionPinnedKey(id string) string { return "session." + id + ".pinned" }

// sessionPinned reads a session's pin flag, caching it for the app run.
func (a *App) sessionPinned(id string) bool {
	if a.pinnedSessions == nil {
		a.pinnedSessions = make(map[string]bool)
	}
	if pinned, ok := a.pinnedSessions[id]; ok {
		return pinned
	}
	pinned := false
	if a.Ctrl != nil {
		if raw, err := a.Ctrl.Store().GetSetting(sessionPinnedKey(id)); err == nil {
			pinned = raw == "true"
		}
	}
	a.pinnedSessions[id] = pinned
	return pinned
}

// setSessionPinned persists session.<id>.pinned and toasts the outcome.
func (a *App) setSessionPinned(c *ui.Context, id string, pinned bool) {
	if a.Ctrl == nil {
		return
	}
	value := "false"
	if pinned {
		value = "true"
	}
	if err := a.Ctrl.Store().SetSetting(sessionPinnedKey(id), value); err != nil {
		a.chromeToast(c, "Could not update the session", "无法更新会话")
		return
	}
	if a.pinnedSessions == nil {
		a.pinnedSessions = make(map[string]bool)
	}
	a.pinnedSessions[id] = pinned
	if pinned {
		a.chromeToast(c, "Session pinned", "会话已置顶")
	} else {
		a.chromeToast(c, "Session unpinned", "会话已取消置顶")
	}
}

// pinnedFirst moves pinned sessions ahead of the rest, keeping the order
// within both groups (the controller's manual or updated order).
func (a *App) pinnedFirst(sessions []store.Session) []store.Session {
	anyPinned := false
	for _, sess := range sessions {
		if a.sessionPinned(sess.ID) {
			anyPinned = true
			break
		}
	}
	if !anyPinned {
		return sessions
	}
	out := append([]store.Session(nil), sessions...)
	sort.SliceStable(out, func(i, j int) bool {
		return a.sessionPinned(out[i].ID) && !a.sessionPinned(out[j].ID)
	})
	return out
}

// addWorkspaceRow was a second, pointer-only "Add workspace" row under the
// workspace list. DSH has no such row (the header's + button is the only
// control), and it duplicated the action, so the sidebar no longer renders it.

// beginAddWorkspace opens the workspace dialog in create mode.
func (a *App) beginAddWorkspace() {
	a.wsEditID = ""
	a.wsName = ""
	a.wsRoot = ""
	a.wsErr = ""
	a.wsCreateRoot = false
	a.wsDialogOpen = true
	a.wsDeleteOpen = false
	a.viewOptionsOpen = false
}

// beginRenameWorkspace opens the workspace dialog in rename mode.
func (a *App) beginRenameWorkspace(ws store.Workspace) {
	a.wsEditID = ws.ID
	a.wsName = ws.Name
	a.wsRoot = ws.Root
	a.wsErr = ""
	a.wsDialogOpen = true
	a.wsDeleteOpen = false
	a.viewOptionsOpen = false
}

// beginDeleteWorkspace opens the workspace delete confirmation.
func (a *App) beginDeleteWorkspace(ws store.Workspace) {
	a.wsEditID = ""
	a.wsDeleteID = ws.ID
	a.wsDeleteName = ws.Name
	a.wsDeleteOpen = true
	a.wsDialogOpen = false
	a.viewOptionsOpen = false
}

// workspaceDialog edits a workspace name and, when creating, its root path.
func (a *App) workspaceDialog(c *ui.Context, t *design.Tokens) {
	ui.Modal(c, &a.wsDialogOpen, func() {
		ui.Column(c).Width(460).Gap(design.Space3).Children(func() {
			title := a.T("workspace.add_workspace")
			if a.wsEditID != "" {
				title = a.T("workspace.rename_workspace")
			}
			ui.Text(c, title).FontSize(16).FontWeight(600).TextColor(t.LabelPrimary)
			// The dialog is a native form so both inputs carry a visible
			// label; a bare TextInput only names the field for assistive
			// technology, which left the two rows looking blank (UX-05).
			ui.Form(c, func() {
				ui.Field(c, a.T("workspace.workspace_name"), func() {
					ui.TextInput(c, &a.wsName).Label(a.T("workspace.workspace_name")).AutoFocus().
						OnSubmit(func() { a.confirmWorkspace(c) })
				})
				if a.wsEditID == "" {
					ui.Field(c, a.T("workspace.root_path"), func() {
						ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
							ui.TextInput(c, &a.wsRoot).Label(a.T("workspace.root_path")).
								Grow(1).OnSubmit(func() { a.confirmWorkspace(c) })
							if ui.Button(c, a.T("workspace.browse")).Clicked() {
								a.browseWorkspaceRoot()
							}
						})
					})
				}
			})
			if a.wsEditID == "" {
				if a.workspaceRootMissing() {
					ui.Text(c, a.T("workspace.root_missing_hint")).FontSize(11).
						TextColor(t.WarningText)
					ui.Checkbox(c, &a.wsCreateRoot, a.T("workspace.create_root"))
				}
				ui.Text(c, a.T("workspace.sessions_created_in_this_workspace_run_under_its_root")).
					FontSize(11).TextColor(t.LabelTertiary)
			} else if a.wsRoot != "" {
				ui.Text(c, a.wsRoot).FontSize(12).TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			}
			if a.wsErr != "" {
				ui.Text(c, a.wsErr).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(design.Space2).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("workspace.cancel")).Clicked() {
					a.wsDialogOpen = false
				}
				label := a.T("workspace.create")
				if a.wsEditID != "" {
					label = a.T("workspace.save")
				}
				if ui.Button(c, label).Clicked() {
					a.confirmWorkspace(c)
				}
			})
		})
	})
}

// browseWorkspaceRoot asks the injected picker (or mygo.Dialog) for a
// directory and fills the root field.
func (a *App) browseWorkspaceRoot() {
	pick := a.PickDirectory
	if pick == nil {
		if !mygo.App.IsReady() {
			// No desktop backend (headless/automated run or a pre-Run
			// window): a native dialog would block forever. Keep the typed
			// path field usable and say why Browse did nothing.
			a.wsErr = a.tr(
				"The folder picker is unavailable right now. Type the root path instead.",
				"文件夹选择器当前不可用，请直接输入根目录路径。")
			return
		}
		pick = pickWorkspaceDirectory
	}
	path, err := pick()
	if err != nil {
		a.wsErr = a.T("workspace.could_not_open_the_folder_picker")
		return
	}
	if strings.TrimSpace(path) != "" {
		a.wsRoot = path
		a.wsErr = ""
	}
}

// errWorkspacePickerUnavailable reports that the native directory picker
// cannot run in this process, so the dialog must fall back to its typed path.
var errWorkspacePickerUnavailable = errors.New("shell: workspace picker unavailable")

// pickWorkspaceDirectory is the production Browse… implementation. It is a
// variable so tests never need a desktop dialog.
var pickWorkspaceDirectory = func() (string, error) {
	if !mygo.App.IsReady() {
		return "", errWorkspacePickerUnavailable
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     "Choose workspace root",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// workspaceRootMissing reports whether the typed root does not exist yet, so
// the dialog can offer to create it instead of failing after the fact.
func (a *App) workspaceRootMissing() bool {
	root := strings.TrimSpace(a.wsRoot)
	if root == "" {
		return false
	}
	_, err := os.Stat(root)
	return os.IsNotExist(err)
}

// checkWorkspaceRoot validates a new workspace's root before it is persisted
// (PD-17): an existing path must be a directory that accepts a file, a missing
// one is created only when the user ticked "Create this folder", and every
// failure becomes a localized line in the dialog instead of a silent bad
// workspace.
func (a *App) checkWorkspaceRoot(root string) error {
	// A new workspace root must be absolute: a relative path would resolve
	// against whatever directory the app was started in and create the folder
	// somewhere the user does not expect (PD-17).
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%s", a.T("workspace.root_must_be_absolute"))
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			if !a.wsCreateRoot {
				return fmt.Errorf("%s", a.T("workspace.root_missing_error"))
			}
			if mkErr := os.MkdirAll(root, 0o755); mkErr != nil {
				return fmt.Errorf("%s: %v", a.T("workspace.root_could_not_create"), mkErr)
			}
			return nil
		}
		return fmt.Errorf("%s: %v", a.T("workspace.root_unreadable"), err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s", a.T("workspace.root_not_a_folder"))
	}
	probe, err := os.CreateTemp(root, ".zeokeel-write-probe-*")
	if err != nil {
		return fmt.Errorf("%s: %v", a.T("workspace.root_not_writable"), err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// confirmWorkspace validates and saves the workspace dialog.
func (a *App) confirmWorkspace(c *ui.Context) {
	name := strings.TrimSpace(a.wsName)
	if name == "" {
		a.wsErr = a.T("workspace.workspace_name_cannot_be_empty")
		return
	}
	root := strings.TrimSpace(a.wsRoot)
	if a.wsEditID == "" && root == "" {
		a.wsErr = a.T("workspace.root_path_cannot_be_empty")
		return
	}
	if a.wsEditID == "" {
		if err := a.checkWorkspaceRoot(root); err != nil {
			a.wsErr = err.Error()
			return
		}
	}
	if a.Ctrl == nil {
		a.wsDialogOpen = false
		return
	}
	ctx := context.Background()
	if a.wsEditID != "" {
		if _, err := a.Ctrl.RenameWorkspace(ctx, a.wsEditID, name); err != nil {
			a.wsErr = a.T("workspace.could_not_rename_the_workspace")
			return
		}
		a.wsDialogOpen = false
		a.wsErr = ""
		c.Toast(a.T("workspace.workspace_renamed"))
		return
	}
	if _, err := a.Ctrl.CreateWorkspace(ctx, name, root); err != nil {
		a.wsErr = a.T("workspace.could_not_create_the_workspace")
		return
	}
	a.wsDialogOpen = false
	a.wsErr = ""
	a.resetFiles()
	a.SetSnapshot(nil)
	a.Screen = Home
	c.Toast(a.T("workspace.workspace_created"))
}

// workspaceDeleteDialog confirms deleting a workspace and cascading its
// sessions.
func (a *App) workspaceDeleteDialog(c *ui.Context, t *design.Tokens) {
	ui.Modal(c, &a.wsDeleteOpen, func() {
		ui.Column(c).Width(440).Gap(design.Space3).Children(func() {
			ui.Text(c, a.T("workspace.delete_workspace_2")).FontSize(16).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Text(c, a.T("workspace.delete_named", a.wsDeleteName)).
				FontSize(13).TextColor(t.LabelSecondary)
			ui.Row(c).Gap(design.Space2).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("workspace.cancel")).Clicked() {
					a.wsDeleteOpen = false
				}
				del := ui.Button(c, a.T("workspace.delete")).TextColor(t.Danger)
				if del.Clicked() {
					a.confirmDeleteWorkspace(c)
				}
			})
		})
	})
}

// confirmDeleteWorkspace removes the workspace and switches to the first
// one that remains.
func (a *App) confirmDeleteWorkspace(c *ui.Context) {
	if a.Ctrl == nil {
		a.wsDeleteOpen = false
		return
	}
	ctx := context.Background()
	id := a.wsDeleteID
	if err := a.Ctrl.DeleteWorkspace(ctx, id); err != nil {
		c.Toast(a.T("workspace.could_not_delete_the_workspace"))
		return
	}
	a.wsDeleteOpen = false
	a.SetSnapshot(nil)
	a.Screen = Home
	a.resetFiles()

	// The controller clears the selection only when the deleted workspace was
	// current. Keep the user's current workspace when it survived; otherwise
	// select the first remaining one.
	target := a.Ctrl.CurrentWorkspaceID()
	if target == "" {
		workspaces, err := a.Ctrl.ListWorkspaces(ctx)
		if err != nil || len(workspaces) == 0 {
			c.Toast(a.T("workspace.workspace_deleted"))
			return
		}
		if err := a.Ctrl.SelectWorkspace(ctx, workspaces[0].ID); err != nil {
			c.Toast(a.T("workspace.workspace_deleted"))
			return
		}
		target = workspaces[0].ID
	}
	if sessions, err := a.Ctrl.SessionsForWorkspace(ctx, target, a.includeArchived(), a.manualSort); err == nil && len(sessions) > 0 {
		a.openSession(sessions[0].ID)
	}
	c.Toast(a.T("workspace.workspace_deleted"))
}

// workspaceRoot returns the selected workspace's filesystem root, or "".
func (a *App) workspaceRoot() string {
	if a.Ctrl == nil {
		return ""
	}
	return a.Ctrl.WorkspaceRoot()
}

// statWorkspaceRoot reports whether root exists and is a directory. It is
// used by the Files tab to explain an unusable workspace root.
func statWorkspaceRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrInvalid
	}
	return nil
}
