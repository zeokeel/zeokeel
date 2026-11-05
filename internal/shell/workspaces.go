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

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
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

// workspaceSidebar is the live sidebar's workspace list: every real
// workspace, with the selected one's sessions grouped underneath. The
// workspaces follow ui.workspace_order (drag/drop), and pinned sessions
// lead the group.
func (a *App) workspaceSidebar(c *ui.Context, t *design.Tokens) {
	ctx := context.Background()
	workspaces, err := a.workspaceList()
	if err != nil {
		ui.Text(c, a.T("workspace.could_not_load_workspaces")).
			FontSize(11).TextColor(t.LabelTertiary).PaddingX(design.Space2)
		return
	}
	// DSH's inline Search sessions filters this list instead of opening a
	// dialog; the header field owns the query.
	if a.inlineSearch && strings.TrimSpace(a.inlineQuery) != "" {
		a.inlineSearchResults(c, t)
		return
	}
	current := a.Ctrl.CurrentWorkspaceID()
	group := normalizeGroupBy(a.groupBy)
	includeArchived := a.includeArchived()

	if group == groupByList {
		// In one list: every workspace's sessions in a single flat list.
		sessions, sessionErr := a.Ctrl.SessionsForWorkspace(ctx, "", includeArchived, a.manualSort)
		sessions = a.orderManualSessions(a.pinnedFirst(a.filterSessions(sessions)))
		a.sidebarSessionList(c, t, sessions, sessionErr)
		return
	}

	sessions, sessionErr := a.Ctrl.SessionsForWorkspace(ctx, current, includeArchived, a.manualSort)
	if sessionErr == nil {
		sessions = a.orderManualSessions(a.pinnedFirst(a.filterSessions(sessions)))
	}

	ui.Column(c).PaddingX(design.Space3).PaddingY(design.Space1).
		Gap(design.Space1).Children(func() {
		if group == groupByTree {
			// Workspace Tree: nest child workspaces under their ancestor
			// (indented), then list each workspace's own sessions. The old
			// flat walk painted parent and child at the same x.
			children, roots := workspaceTree(workspaces)
			var render func(ws store.Workspace, depth int)
			render = func(ws store.Workspace, depth int) {
				treeSessions, treeErr := a.Ctrl.SessionsForWorkspace(ctx, ws.ID, includeArchived, a.manualSort)
				treeSessions = a.orderManualSessions(a.pinnedFirst(a.filterSessions(treeSessions)))
				a.workspaceNodeDepth(c, t, ws, ws.ID == current, treeSessions, treeErr, true, depth,
					func() {
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
			a.workspaceNodeDepth(c, t, ws, ws.ID == current, sessions, sessionErr, false, 0, nil)
		}
	})
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

// sidebarSessionList is the flat "In one list" grouping: named, focusable
// session rows with no workspace headers.
func (a *App) sidebarSessionList(c *ui.Context, t *design.Tokens, sessions []store.Session, sessionErr error) {
	ui.Column(c).PaddingX(design.Space3).PaddingY(design.Space1).Gap(2).Children(func() {
		if sessionErr != nil {
			ui.Text(c, a.T("workspace.could_not_load_sessions")).
				FontSize(11).TextColor(t.LabelTertiary).PaddingX(design.Space2)
			return
		}
		for _, sess := range sessions {
			item := sess
			row := ui.Row(c).Height(32).Width(256).PaddingX(design.Space2).Gap(design.Space1).
				AlignItems(ui.Center).Radius(design.RadiusSM).
				Label(item.Title).Role(ui.RoleButton).Focusable()
			if item.ID == a.Ctrl.CurrentSessionID() {
				row = row.Background(t.BgHover)
			}
			row.Children(func() {
				ui.Text(c, item.Title).FontSize(14).FixedLineHeight(20).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("...")
				ui.Text(c, a.sessionWhen(item.UpdatedAt)).FontSize(10).
					FixedLineHeight(16).TextColor(t.LabelTertiary)
			})
			row.OnClick(func() { a.openSession(item.ID) })
		}
	})
}

// workspaceNode draws one workspace row and, under the selected (or, in the
// Workspace Tree grouping, every) workspace, its sessions.
func (a *App) workspaceNode(c *ui.Context, t *design.Tokens, ws store.Workspace, selected bool, sessions []store.Session, sessionErr error, forceExpanded bool) {
	a.workspaceNodeDepth(c, t, ws, selected, sessions, sessionErr, forceExpanded, 0, nil)
}

// workspaceNodeDepth is workspaceNode with an indent depth and an optional
// callback that renders nested child workspaces between this row and its
// sessions (Workspace Tree).
func (a *App) workspaceNodeDepth(c *ui.Context, t *design.Tokens, ws store.Workspace, selected bool, sessions []store.Session, sessionErr error, forceExpanded bool, depth int, childNodes func()) {
	indent := design.Space2 + float32(depth)*14
	ui.Column(c).Gap(2).Children(func() {
		row := ui.Row(c).Height(34).PaddingX(design.Space2).PaddingStart(indent).Gap(6).
			Radius(design.RadiusMD).AlignItems(ui.Center)
		// DSH keeps the selected workspace row on the sidebar color; only
		// hover tints it. The selected session row carries the fill.
		if row.Hovered() {
			row = row.Background(t.BgHover)
		}
		expanded := false
		if forceExpanded {
			// Workspace Tree keeps every workspace visible, but each one can
			// still be collapsed, and that choice persists.
			expanded = !a.wsCollapsed[ws.ID]
		} else {
			expanded = selected && !a.wsCollapsed[ws.ID]
		}
		row.Children(func() {
			open := ui.Row(c).Grow(1).Gap(design.Space2).AlignItems(ui.Center).
				Label(ws.Name).Role(ui.RoleButton).Focusable()
			open.Children(func() {
				folder := t.LabelSecondary
				if selected {
					folder = t.Accent
				}
				ui.Icon(c, icons.Folder()).FontSize(15).TextColor(folder)
				ui.Text(c, ws.Name).FontSize(14).FixedLineHeight(20).TextColor(t.LabelPrimary).
					Grow(1).MaxLines(1).Ellipsis("...")
				chevron := "▸"
				if expanded {
					chevron = "⌄"
				}
				ui.Text(c, chevron).FontSize(11).TextColor(t.LabelTertiary)
			})
			id := ws.ID
			open.OnClick(func() {
				a.wsHoverID = ""
				if id == a.Ctrl.CurrentWorkspaceID() {
					a.setWorkspaceCollapsed(id, !a.wsCollapsed[id])
					return
				}
				a.setWorkspaceCollapsed(id, false)
				a.switchWorkspace(id)
			})

			// Actions mirror DSH's workspace row: a "new session" button and
			// the workspace ellipsis menu, revealed on hover or selection.
			if row.Hovered() || selected {
				actions := ui.Row(c).Gap(design.Space1).AlignItems(ui.Center)
				actions.Children(func() {
					plus := ui.Row(c).Size(20, 20).Center().
						Label(a.tr("New session in ", "在以下位置新建会话：") + ws.Name).
						Role(ui.RoleButton).Focusable()
					plus.Children(func() {
						ui.Icon(c, icons.Plus()).FontSize(14).TextColor(t.LabelSecondary)
					})
					plus.OnClick(func() { a.newSessionInWorkspace(ws) })
					more := ui.Row(c).Size(18, 18).Center().
						Label(a.tr("Workspace actions for ", "工作区操作：") + ws.Name).
						Role(ui.RoleButton).Focusable()
					more.Children(func() {
						ui.Text(c, "⋮").FontSize(12).FontWeight(700).TextColor(t.LabelSecondary)
					})
					more.PopoverMenuButton(func(m *ui.Menu) { a.workspaceMenu(c, m, ws) })
				})
			}
		})
		row.PopoverMenu(func(m *ui.Menu) { a.workspaceMenu(c, m, ws) })

		// Drag/drop reordering: the row is the source of its workspace id
		// and a drop target; the top half inserts the dragged workspace
		// before this row, the bottom half after it. ui.DragOver draws the
		// insert edge while a row is dragged over.
		row = row.Drag(ws.ID)
		if src, over := ui.DragOver[string](row); over && src != ws.ID {
			if workspaceDropBefore(row) {
				row = row.BorderWidth(2, 0, 0, 0).BorderColor(t.Accent)
			} else {
				row = row.BorderWidth(0, 0, 2, 0).BorderColor(t.Accent)
			}
		}
		if src, dropped := ui.Drop[string](row); dropped && src != ws.ID {
			before := workspaceDropBefore(row)
			a.reorderWorkspace(src, ws.ID, before)
			a.wsHoverID = ""
		}
		// The hover card is a pointer affordance, not part of a drag: it
		// would otherwise cover the rows the workspace is dragged over.
		if row.Dragging() {
			a.wsHoverID = ""
		} else {
			a.maybeWorkspaceHoverCard(c, t, row, ws)
		}

		if !expanded {
			return
		}
		// Nested child workspaces sit directly under their ancestor and before
		// the ancestor's own sessions.
		if childNodes != nil {
			childNodes()
		}
		if sessionErr != nil {
			ui.Text(c, a.T("workspace.could_not_load_sessions")).
				FontSize(11).TextColor(t.LabelTertiary).PaddingX(design.Space5)
			return
		}
		if len(sessions) == 0 {
			ui.Text(c, a.T("workspace.no_sessions_yet")).
				FontSize(11).TextColor(t.LabelTertiary).Padding(2, design.Space5, 2, design.Space2)
			return
		}
		for _, sess := range sessions {
			a.workspaceSessionRow(c, t, sess)
		}
	})
}

// workspaceSessionRow is one session under the selected workspace. The row
// opens it and the ellipsis menu offers rename, archive and delete.
func (a *App) workspaceSessionRow(c *ui.Context, t *design.Tokens, sess store.Session) {
	row := ui.Row(c).Height(32).Padding(0, design.Space2, 0, design.Space2).
		Radius(design.RadiusMD).Gap(design.Space1).AlignItems(ui.Center)
	if sess.ID == a.Ctrl.CurrentSessionID() || row.Hovered() {
		row = row.Background(t.BgHover)
	}
	// Manual ordering: every session row is a drag source and a drop target.
	// The top half inserts before this row, the bottom half after it.
	row = row.Drag(sess.ID)
	if src, over := ui.DragOver[string](row); over && src != sess.ID {
		if workspaceDropBefore(row) {
			row = row.BorderWidth(2, 0, 0, 0).BorderColor(t.Accent)
		} else {
			row = row.BorderWidth(0, 0, 2, 0).BorderColor(t.Accent)
		}
	}
	if src, dropped := ui.Drop[string](row); dropped && src != sess.ID {
		a.reorderSession(src, sess.ID, workspaceDropBefore(row))
	}
	row.Children(func() {
		open := ui.Row(c).Grow(1).Gap(design.Space1).AlignItems(ui.Center).
			Label(sess.Title).Role(ui.RoleButton).Focusable()
		open.Children(func() {
			dotColor := t.BorderL3
			if sess.ID == a.Ctrl.CurrentSessionID() {
				if snap := a.Snapshot(); snap != nil && snap.Phase == state.PhaseRunning {
					dotColor = t.Accent
				}
			}
			ui.Row(c).Size(16, 16).Center().Children(func() {
				ui.Box(c).Size(6, 6).Radius(design.RadiusMax).Background(dotColor)
			})
			title := sess.Title
			if sess.Archived {
				title += " · " + a.T("workspace.archived")
			}
			ui.Text(c, title).FontSize(14).FixedLineHeight(20).TextColor(t.LabelPrimary).
				Grow(1).MaxLines(1).Ellipsis("...")
			ui.Text(c, a.sessionWhen(sess.UpdatedAt)).FontSize(10).FixedLineHeight(16).
				TextColor(t.LabelTertiary)
		})
		id := sess.ID
		open.OnClick(func() { a.openSession(id) })

		if row.Hovered() || sess.ID == a.Ctrl.CurrentSessionID() {
			more := ui.Row(c).Size(18, 18).Center().
				Label(a.menuText("Session actions for "+sess.Title, "会话操作："+sess.Title)).
				Role(ui.RoleButton).Focusable()
			more.Children(func() {
				ui.Text(c, "⋯").FontSize(12).FontWeight(700).TextColor(t.LabelSecondary)
			})
			more.PopoverMenuButton(func(m *ui.Menu) { a.workspaceSessionMenu(c, m, sess) })
		}
	})
	row.PopoverMenu(func(m *ui.Menu) { a.workspaceSessionMenu(c, m, sess) })
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
			ui.TextInput(c, &a.wsName).Label(a.T("workspace.workspace_name")).AutoFocus().
				OnSubmit(func() { a.confirmWorkspace(c) })
			if a.wsEditID == "" {
				ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &a.wsRoot).Label(a.T("workspace.root_path")).
						Grow(1).OnSubmit(func() { a.confirmWorkspace(c) })
					if ui.Button(c, a.T("workspace.browse")).Clicked() {
						a.browseWorkspaceRoot()
					}
				})
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
