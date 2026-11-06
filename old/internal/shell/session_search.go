package shell

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/icons"
	"github.com/zeokeel/zeokeel/old/internal/sessionreference"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// installTaskShortcuts registers the window's keyboard shortcuts. It mirrors
// the original set and adds the workspace and view-option overlays to the
// Escape order.
//
//	Cmd/Ctrl+N        new session
//	Cmd/Ctrl+,        settings
//	Cmd/Ctrl+K        focus the composer in a session, else search sessions
//	Cmd/Ctrl+/        open the keyboard shortcuts reference
//	Cmd/Ctrl+B        toggle the left sidebar (installChromeShortcuts)
//	Cmd/Ctrl+Alt+B    toggle the right sidebar (Session and Home)
//	Cmd/Ctrl+P        open the right sidebar's Workspace files tab
//	Ctrl+`            open the right sidebar's Terminal tab
//	Esc               close the topmost overlay, else stop a running turn
func (a *App) installTaskShortcuts(c *ui.Context) {
	a.bindShortcut(c, "new_session", ui.Cmd, ui.KeyN, func() { a.newSession() })
	a.bindShortcut(c, "add_workspace", ui.Cmd, ui.KeyO, func() { a.beginAddWorkspace() })
	a.bindShortcut(c, "settings", ui.Cmd, ui.KeyComma, func() { a.openSettings() })
	a.bindShortcut(c, "search", ui.Cmd, ui.KeyK, func() { a.taskShortcutK(c) })
	a.bindShortcut(c, "send_alternate", ui.Cmd, ui.KeyEnter, func() { a.submitAlternate(c) })
	a.bindShortcut(c, "shortcuts", ui.Cmd, ui.KeySlash, func() { a.openShortcutsDialog() })
	a.bindShortcut(c, "rename_session", ui.Cmd|ui.Alt, ui.KeyG, func() { a.renameCurrentSession() })
	a.bindShortcut(c, "archive_session", ui.Cmd|ui.Shift, ui.KeyA, func() { a.archiveCurrentSession(c) })
	a.bindShortcut(c, "toggle_right_sidebar", ui.Cmd|ui.Alt, ui.KeyB, func() { a.toggleRightSidebar() })
	a.bindShortcut(c, "workspace_files", ui.Cmd, ui.KeyP, func() { a.openRightSidebarTab(filesTab) })
	a.bindShortcut(c, "new_terminal", ui.Ctrl, ui.KeyBackquote, func() { a.openRightSidebarTab(terminalTab) })
	a.bindShortcut(c, "escape", 0, ui.KeyEscape, func() { a.closeTaskTopmost() })
}

// renameCurrentSession is DSH's Cmd/Ctrl+Alt+G: it opens the rename dialog for
// the current session.
func (a *App) renameCurrentSession() {
	if a.Ctrl == nil {
		return
	}
	sess, err := a.Ctrl.CurrentSession()
	if err != nil || sess == nil || sess.ID == "" {
		return
	}
	a.beginRename(sess.ID, sess.Title)
}

// archiveCurrentSession is DSH's Cmd/Ctrl+Shift+A. It archives the current
// session and opens the next unarchived one, or Home when none is left.
func (a *App) archiveCurrentSession(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	sess, err := a.Ctrl.CurrentSession()
	if err != nil || sess == nil || sess.ID == "" {
		return
	}
	if err := a.Ctrl.SetSessionArchived(context.Background(), sess.ID, true); err != nil {
		c.Toast(a.T("workspace.could_not_update_the_session"))
		return
	}
	a.liveMenus().moreOpen = false
	ctx := context.Background()
	sessions, err := a.Ctrl.SessionsForWorkspace(ctx, a.Ctrl.CurrentWorkspaceID(), false, a.manualSort)
	if err == nil {
		for _, next := range sessions {
			if next.ID != sess.ID {
				a.openSessionAsync(c, next.ID)
				c.Toast(a.T("workspace.session_archived"))
				return
			}
		}
	}
	a.SetSnapshot(nil)
	a.Screen = Home
	c.Toast(a.T("workspace.session_archived"))
}

// openShortcutsDialog shows the keyboard shortcut reference, from Cmd/Ctrl+/
// or Settings > Keyboard shortcuts.
func (a *App) openShortcutsDialog() {
	a.moreOpen = false
	a.closeSelectors()
	a.shortcutsOpen = true
}

// toggleRightSidebar is Cmd/Ctrl+Alt+B, DSH's right-sidebar shortcut.
func (a *App) toggleRightSidebar() {
	if a.Ctrl == nil {
		return
	}
	a.rightOpen = !a.rightOpen
	if !a.rightOpen {
		a.rightFullscreen = false
		a.rightSplit = false
	}
}

// openRightSidebarTab opens the right panel on tab. It is a no-op in the
// static demo shell; Home, Session, Plugins and Automation all host the panel.
func (a *App) openRightSidebarTab(tab rightTab) {
	if a.Ctrl == nil || !a.rightSidebarHosted() {
		return
	}
	a.rightOpen = true
	a.rightTab = tab
}

// taskShortcutK focuses the composer inside a live session and opens the
// session search elsewhere, as Cmd/Ctrl+K did before the view options.
func (a *App) taskShortcutK(c *ui.Context) {
	if a.Screen == Session && a.Ctrl != nil {
		a.composerRef.Focus(c)
		return
	}
	a.openSearch()
}

// closeTaskTopmost closes one overlay per Esc, including the workspace
// dialogs, the view-options popover and the derived "@"/"/" composer menus.
// Only with nothing to close does Esc stop the running turn, matching DSH's
// response-stop binding.
func (a *App) closeTaskTopmost() {
	if a.closeOneTaskOverlay() {
		return
	}
	if a.Screen == Session && a.Ctrl != nil {
		// A plan waiting for review is an overlay-like decision: Esc closes
		// the review card but keeps plan mode on (the pending composer badge
		// reopens it), so a reflexive Esc never leaves the planning flow.
		if review := a.planReviewArtifact(); review != nil {
			a.dismissPlanReview(review.CallID)
			return
		}
		// DSH denies a pending approval with Esc before it considers the
		// turn; stopping a turn that waits for approval would be a different
		// action.
		if pending := a.pendingApprovals(); len(pending) > 0 {
			a.Ctrl.Deny(pending[0].CallID)
			return
		}
		if snap := a.currentSnapshot(); snap != nil && snap.Phase == state.PhaseRunning {
			a.stopTurn()
		}
	}
}

// closeOneTaskOverlay closes exactly one open shell surface and reports
// whether it closed anything.
func (a *App) closeOneTaskOverlay() bool {
	switch {
	case a.wsDialogOpen:
		a.wsDialogOpen = false
	case a.wsDeleteOpen:
		a.wsDeleteOpen = false
	case a.viewOptionsOpen:
		a.viewOptionsOpen = false
	case a.inlineSearch:
		a.closeInlineSearch()
	case a.shellUI().rightNewTabOpen:
		a.shellUI().rightNewTabOpen = false
	case a.attachOpen:
		a.attachOpen = false
	case a.dismissComposerPopup():
		// Escape closes the derived "@" mention or "/" command menu before
		// the stop-turn fallback in closeTaskTopmost.
	default:
		return a.closeOneOverlay()
	}
	return true
}

// shellOverlays draws every live overlay above the current screen. It is the
// original overlay set plus the workspace dialogs and the content-searching
// session palette that replaced the title-only one.
func (a *App) shellOverlays(c *ui.Context, t *design.Tokens) {
	if a.aboutOpen {
		a.aboutDialog(c, t)
	}
	if a.shortcutsOpen {
		a.shortcutsDialog(c, t)
	}
	if a.renameOpen {
		a.renameDialog(c, t)
	}
	if a.deleteOpen {
		a.deleteDialog(c, t)
	}
	if a.wsDialogOpen {
		a.workspaceDialog(c, t)
	}
	if a.wsDeleteOpen {
		a.workspaceDeleteDialog(c, t)
	}
	if a.paletteOpen {
		a.sessionSearchDialog(c, t)
	}
}

// openInlineSearch is DSH's sidebar Search sessions: it expands an inline
// field in the sidebar and filters the workspace/session list there, leaving
// the palette modal for Cmd/Ctrl+K.
func (a *App) openInlineSearch() {
	if a.Ctrl == nil {
		return
	}
	a.moreOpen = false
	a.closeSelectors()
	a.viewOptionsOpen = false
	a.paletteOpen = false
	a.inlineSearch = true
	a.inlineQuery = ""
	a.searchIndex = 0
}

// closeInlineSearch collapses the inline field and clears its query.
func (a *App) closeInlineSearch() {
	a.inlineSearch = false
	a.inlineQuery = ""
}

// sidebarSearchField is the expanded inline search row: a native text input
// with the search magnifier and a brand clear button, all on the theme's
// spacing grid. It keeps a persistent, labelled Clear control (mygo's
// SearchField only shows its own clear button while the query is non-empty),
// which is what the keyboard and assistive-technology flow and the
// regression tests rely on.
func (a *App) sidebarSearchField(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	row := ui.Row(c).Height(th.Space(9)).
		Margin(0, th.Space(2), 0, th.Space(3)).
		PaddingX(th.Space(2)).Gap(th.Space(1.5)).AlignItems(ui.Center).
		Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusSM)
	row.Children(func() {
		ui.Icon(c, icons.Search()).Size(th.Space(3.75), th.Space(3.75)).
			TextColor(t.LabelTertiary).Shrink(0)
		input := ui.TextInputBase(c, &a.inlineQuery).
			Label(a.tr("Search session names", "搜索会话名称")).
			Placeholder(a.T("search.search_sessions")).
			AutoFocus().Grow(1)
		input.OnChange(func() { a.searchIndex = 0 })
		clearLabel := a.tr("Clear search", "清除搜索")
		clear := ui.ButtonBase(c).Size(th.Space(4), th.Space(4)).Center().
			Radius(design.RadiusSM).Label(clearLabel).Tooltip(clearLabel).Shrink(0)
		if clear.Hovered() {
			clear = clear.Background(t.BgHover)
		}
		clear.Children(func() {
			ui.Text(c, "×").FontSize(14).TextColor(t.LabelTertiary).NoWrap()
		})
		clear.OnClick(func() { a.inlineQuery = "" })
	})
}

// searchResultsFor queries the current workspace for sessions matching query.
func (a *App) searchResultsFor(query string) []store.Session {
	if a.Ctrl == nil {
		return nil
	}
	sessions, err := a.Ctrl.SearchSessions(context.Background(), query, a.includeArchived(), a.manualSort)
	if err != nil {
		return nil
	}
	return a.filterSessions(sessions)
}

// inlineSearchResults replaces the sidebar list with the matching sessions:
// ghost result rows on the brand grid, the current session highlighted and
// archived sessions marked with a muted pill.
func (a *App) inlineSearchResults(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	results := a.searchResultsFor(a.inlineQuery)
	ui.Column(c).PaddingX(th.Space(3)).PaddingY(th.Space(1)).
		Gap(th.Space(0.5)).Children(func() {
		if len(results) == 0 {
			ui.Text(c, a.T("search.no_matching_sessions")).
				FontSize(12).TextColor(t.LabelTertiary).PaddingX(th.Space(2))
			return
		}
		for _, sess := range results {
			item := sess
			row := searchResultRow(c, t, "Open "+item.Title).FillWidth()
			if item.ID == a.Ctrl.CurrentSessionID() {
				row = row.Background(t.BgHover)
			}
			row.Children(func() {
				ui.Icon(c, icons.Folder()).Size(th.Space(3.5), th.Space(3.5)).
					TextColor(t.LabelSecondary)
				ui.Text(c, item.Title).FontSize(13).FixedLineHeight(20).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				if item.Archived {
					ui.Text(c, a.T("search.archived")).FontSize(10).
						FixedLineHeight(16).TextColor(t.LabelTertiary).
						PaddingX(th.Space(1.5)).Radius(design.RadiusMax).
						Background(t.BgHover)
				}
			})
			row.OnClick(func() {
				a.closeInlineSearch()
				a.openSessionAsync(c, item.ID)
			})
		}
	})
}

// searchResultRow is the shared ghost row of a session search result: the
// small brand corner, the hover tint and the row's accessible name. The caller
// adds the width it needs: FillWidth in the sidebar list, Grow in a dialog row.
func searchResultRow(c *ui.Context, t *design.Tokens, label string) ui.Element {
	th := t.Theme()
	row := ui.ButtonBase(c).Height(th.Space(8)).PaddingX(th.Space(2)).
		Gap(th.Space(2)).AlignItems(ui.Center).Justify(ui.Start).
		Radius(design.RadiusSM).Label(label)
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	return row
}

// openSearch shows the session search modal with a clean query.
func (a *App) openSearch() {
	a.moreOpen = false
	a.closeSelectors()
	a.viewOptionsOpen = false
	a.inlineSearch = false
	a.paletteQuery = ""
	a.searchIndex = 0
	a.paletteOpen = true
}

// searchResults asks the store for the current workspace's sessions matching
// the palette query in their title or message content.
func (a *App) searchResults() []store.Session {
	return a.searchResultsFor(a.paletteQuery)
}

// View-option keys are ordinary settings rows, so the grouping, sort order
// and archive filter survive a restart instead of resetting to their defaults.
const (
	settingSessionsManualSort    = "ui.sessions_manual_sort"
	settingSessionsShowArchived  = "ui.sessions_show_archived"
	settingSessionsGroupBy       = "ui.sessions_group_by"
	settingSessionsArchiveFilter = "ui.sessions_archive_filter"
)

// View-option values, matching DSH's View options popover.
const (
	groupByWorkspace = "workspace" // WorkSpace
	groupByTree      = "tree"      // Workspace Tree
	groupByList      = "list"      // In one list

	archiveHide     = "hide"     // Hide archived
	archiveAll      = "all"      // All conversations (show archived)
	archiveArchived = "archived" // Archived only
)

// normalizeGroupBy/archiveFilter accept only the values the popover offers.
func normalizeGroupBy(v string) string {
	switch v {
	case groupByWorkspace, groupByTree, groupByList:
		return v
	}
	return groupByWorkspace
}

func normalizeArchiveFilter(v string) string {
	switch v {
	case archiveHide, archiveAll, archiveArchived:
		return v
	}
	return archiveHide
}

// includeArchived reports whether the session query should return archived
// sessions; the archived-only filter fetches them and narrows afterwards.
func (a *App) includeArchived() bool {
	return normalizeArchiveFilter(a.archiveFilter) != archiveHide
}

// filterSessions applies the archive filter to a fetched session list.
func (a *App) filterSessions(sessions []store.Session) []store.Session {
	switch normalizeArchiveFilter(a.archiveFilter) {
	case archiveArchived:
		out := make([]store.Session, 0, len(sessions))
		for _, sess := range sessions {
			if sess.Archived {
				out = append(out, sess)
			}
		}
		return out
	default:
		return sessions
	}
}

// restoreViewOptions loads the persisted grouping, sort order and archive
// filter once per App. The static shell never reads it.
func (a *App) restoreViewOptions() {
	if a.Ctrl == nil || a.viewOptionsLoaded {
		return
	}
	a.viewOptionsLoaded = true
	a.groupBy = groupByWorkspace
	a.archiveFilter = archiveHide
	if raw, err := a.Ctrl.Store().GetSetting(settingSessionsManualSort); err == nil {
		a.manualSort = raw == "true"
	}
	if raw, err := a.Ctrl.Store().GetSetting(settingSessionsGroupBy); err == nil {
		a.groupBy = normalizeGroupBy(raw)
	}
	if raw, err := a.Ctrl.Store().GetSetting(settingSessionsArchiveFilter); err == nil {
		a.archiveFilter = normalizeArchiveFilter(raw)
	}
	// Legacy installs stored only the boolean; keep their choice.
	if raw, err := a.Ctrl.Store().GetSetting(settingSessionsShowArchived); err == nil && raw == "true" {
		if a.archiveFilter == archiveHide {
			a.archiveFilter = archiveAll
		}
	}
	a.showArchived = a.includeArchived()
}

// persistViewOptions writes every view option after a change. A store failure
// is not fatal for the frame; the in-memory choice still applies until restart.
func (a *App) persistViewOptions() {
	if a.Ctrl == nil {
		return
	}
	a.showArchived = a.includeArchived()
	_ = a.Ctrl.Store().SetSetting(settingSessionsManualSort, boolString(a.manualSort))
	_ = a.Ctrl.Store().SetSetting(settingSessionsShowArchived, boolString(a.showArchived))
	_ = a.Ctrl.Store().SetSetting(settingSessionsGroupBy, normalizeGroupBy(a.groupBy))
	_ = a.Ctrl.Store().SetSetting(settingSessionsArchiveFilter, normalizeArchiveFilter(a.archiveFilter))
}

// boolString is the settings-store encoding for a bool.
func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// viewOptionsPopover is the sidebar's View options menu with its three groups:
// Group by, Order by and Filter sessions. It uses the shared branded popover
// panel, clamped to the sidebar column, and the shared checkable menu rows; the
// group headings stay the small tertiary labels the popover always had.
func (a *App) viewOptionsPopover(c *ui.Context, t *design.Tokens) {
	group := normalizeGroupBy(a.groupBy)
	archive := normalizeArchiveFilter(a.archiveFilter)
	a.popoverMenuPanelIn(c, t, sidebarW, func() {
		a.viewOptionGroup(c, t, a.T("search.group_by"))
		a.viewOptionItem(c, t, a.T("search.workspace"), group == groupByWorkspace, func() {
			a.groupBy = groupByWorkspace
			a.persistViewOptions()
		})
		a.viewOptionItem(c, t, a.T("search.workspace_tree"), group == groupByTree, func() {
			a.groupBy = groupByTree
			a.persistViewOptions()
		})
		a.viewOptionItem(c, t, a.T("search.in_one_list"), group == groupByList, func() {
			a.groupBy = groupByList
			a.persistViewOptions()
		})
		a.viewOptionGroup(c, t, a.T("search.order_by"))
		a.viewOptionItem(c, t, a.T("search.manual"), a.manualSort, func() {
			a.manualSort = true
			a.persistViewOptions()
		})
		a.viewOptionItem(c, t, a.T("search.last_updated"), !a.manualSort, func() {
			a.manualSort = false
			a.persistViewOptions()
		})
		a.viewOptionGroup(c, t, a.T("search.filter_sessions"))
		a.viewOptionItem(c, t, a.T("search.hide_archived"), archive == archiveHide, func() {
			a.archiveFilter = archiveHide
			a.persistViewOptions()
		})
		a.viewOptionItem(c, t, a.T("search.all_conversations"), archive == archiveAll, func() {
			a.archiveFilter = archiveAll
			a.persistViewOptions()
		})
		a.viewOptionItem(c, t, a.T("search.archived_only"), archive == archiveArchived, func() {
			a.archiveFilter = archiveArchived
			a.persistViewOptions()
		})
	})
}

// viewOptionGroup is one dimmed section heading inside the popover.
func (a *App) viewOptionGroup(c *ui.Context, t *design.Tokens, label string) {
	th := t.Theme()
	ui.Text(c, label).FontSize(11).FixedLineHeight(16).
		TextColor(t.LabelTertiary).Padding(th.Space(1), th.Space(2), 0, th.Space(2))
}

// viewOptionItem is one checkable row of the view options popover: the shared
// branded check row (RoleMenuItemCheckBox, Focusable, Checked). It closes the
// popover on every choice, as DSH does, so sort and archive filtering stay one
// click away.
func (a *App) viewOptionItem(c *ui.Context, t *design.Tokens, label string, selected bool, choose func()) {
	a.popoverMenuCheckRow(c, t, label, selected, func() {
		a.viewOptionsOpen = false
		if choose != nil {
			choose()
		}
	})
}

// sessionSearchDialog is the modal session search with keyboard navigation,
// content matching, view options and per-row archive.
func (a *App) sessionSearchDialog(c *ui.Context, t *design.Tokens) {
	results := a.searchResults()
	a.clampSearchIndex(len(results))

	ui.Overlay(c, func() {
		back := ui.Row(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			Justify(ui.Center).AlignItems(ui.Center).
			Background(t.Scrim()).Modal()
		back.OnClick(func() { a.paletteOpen = false })
		back.Children(func() {
			th := t.Theme()
			panel := ui.Column(c).Width(th.Space(145)).Padding(th.Space(5)).
				Background(t.BgRaised).Radius(design.RadiusLG).
				Border(1, t.BorderL2).
				Shadow(0, 2, 8, 0, homeCardShadow(t))
			panel.OnClick(func() {})
			panel.Children(func() {
				a.sessionSearchHeader(c, t)
				query := ui.SearchField(c, &a.paletteQuery).
					Label(a.T("search.search_sessions")).
					Placeholder(a.T("search.search_sessions")).AutoFocus()
				if query.Changed() {
					a.searchIndex = 0
				}
				if query.Submitted() {
					a.openSearchSelection()
				}
				a.sessionSearchOptions(c, t)
				a.sessionSearchResults(c, t, results)
				ui.Text(c, a.T("search.enter_opens_moves_esc_closes")).
					FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary)
			})
		})

		if back.OverlayShortcut(0, ui.KeyEscape) {
			a.paletteOpen = false
			return
		}
		if back.OverlayShortcut(0, ui.KeyDown) {
			a.moveSearchSelection(1, len(results))
			return
		}
		if back.OverlayShortcut(0, ui.KeyUp) {
			a.moveSearchSelection(-1, len(results))
			return
		}
		if back.OverlayShortcut(0, ui.KeyEnter) {
			a.openSearchSelection()
		}
	})
}

// sessionSearchHeader is the dialog title and the modifier hint.
func (a *App) sessionSearchHeader(c *ui.Context, t *design.Tokens) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.T("search.search_sessions")).FontSize(16).
			FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
		ui.Text(c, a.T("search.title_or_message_content")).
			FontSize(11).TextColor(t.LabelTertiary)
	})
}

// sessionSearchOptions shows and toggles the sort and archive filters inside
// the dialog, mirroring the sidebar popover.
func (a *App) sessionSearchOptions(c *ui.Context, t *design.Tokens) {
	sortLabel := a.T("search.last_updated")
	if a.manualSort {
		sortLabel = a.T("search.manual")
	}
	archiveLabel := a.T("search.hide_archived")
	if a.includeArchived() {
		archiveLabel = a.T("search.all_conversations")
	}
	ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		a.searchFilterChip(c, t, a.T("search.sort_2")+sortLabel, false, func() {
			a.manualSort = !a.manualSort
			a.persistViewOptions()
		})
		a.searchFilterChip(c, t, archiveLabel, a.includeArchived(), func() {
			if a.includeArchived() {
				a.archiveFilter = archiveHide
			} else {
				a.archiveFilter = archiveAll
			}
			a.persistViewOptions()
		})
	})
}

// searchFilterChip is one view-option pill in the search dialog. It carries a
// name, a role and focusability so keyboard and assistive-technology users can
// change sort and archive filtering from the dialog, not only the sidebar.
func (a *App) searchFilterChip(c *ui.Context, t *design.Tokens, label string, checked bool, toggle func()) {
	th := t.Theme()
	chip := ui.ButtonBase(c).Height(th.Space(7)).PaddingX(th.Space(2)).
		Radius(design.RadiusSM).AlignItems(ui.Center).
		Label(label).Role(ui.RoleButton).Checked(checked).Focusable()
	color := t.LabelSecondary
	if checked {
		chip = chip.Background(t.Accent.Alpha(0.10).Over(t.BgRaised)).
			Border(1, t.Accent)
		color = t.Accent
	} else {
		chip = chip.Border(1, t.BorderL2)
		if chip.Hovered() {
			chip = chip.Background(t.BgHover)
		}
	}
	chip.Children(func() {
		ui.Text(c, label).FontSize(12).FixedLineHeight(18).TextColor(color)
	})
	chip.OnClick(toggle)
}

// sessionSearchResults draws the matching sessions with the keyboard
// selection highlighted and an archive action per row.
func (a *App) sessionSearchResults(c *ui.Context, t *design.Tokens, results []store.Session) {
	if len(results) == 0 {
		ui.Text(c, a.T("search.no_matching_sessions")).
			FontSize(12).TextColor(t.LabelTertiary).Padding(design.Space2, 0, design.Space2, 0)
		return
	}
	th := t.Theme()
	ui.Scroll(c).Height(th.Space(80)).Children(func() {
		ui.Column(c).Gap(th.Space(0.25)).Children(func() {
			for i, sess := range results {
				a.sessionSearchRow(c, t, sess, i)
			}
		})
	})
}

// sessionSearchRow is one result: the open target, the timestamp and the
// archive control.
func (a *App) sessionSearchRow(c *ui.Context, t *design.Tokens, sess store.Session, index int) {
	th := t.Theme()
	selected := index == a.searchIndex || sess.ID == a.Ctrl.CurrentSessionID()
	row := ui.Row(c).Height(th.Space(9)).PaddingX(th.Space(1)).
		Radius(design.RadiusSM).AlignItems(ui.Center).Gap(th.Space(1))
	if selected {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		// A small accent rail marks the keyboard selection or the current
		// session; unselected rows keep the rail's space so titles align.
		rail := ui.Box(c).Width(th.Space(0.5)).Height(th.Space(6)).
			Radius(design.RadiusMax).Shrink(0)
		if selected {
			rail.Background(t.Accent)
		}
		open := searchResultRow(c, t, "Open "+sess.Title).Grow(1)
		open = open.PaddingX(0)
		open.Children(func() {
			title := sess.Title
			if sess.Archived {
				title += " · " + a.T("search.archived")
			}
			ui.Text(c, title).FontSize(13).FixedLineHeight(20).TextColor(t.LabelPrimary).
				Grow(1).MaxLines(1).Ellipsis("…")
			ui.Text(c, a.sessionWhen(sess.UpdatedAt)).FontSize(11).
				FixedLineHeight(20).TextColor(t.LabelTertiary).NoWrap()
		})
		id := sess.ID
		open.OnClick(func() {
			a.paletteOpen = false
			a.openSessionAsync(c, id)
		})

		label := a.T("search.archive")
		archived := true
		if sess.Archived {
			label = a.T("search.unarchive")
			archived = false
		}
		archive := ui.ButtonBase(c).Height(th.Space(7)).PaddingX(th.Space(2)).
			Radius(design.RadiusSM).Label(label).TextColor(t.LabelSecondary)
		if archive.Hovered() {
			archive = archive.Background(t.BgHover)
		}
		archive.Children(func() {
			ui.Text(c, label).FontSize(12).FixedLineHeight(20).
				TextColor(t.LabelSecondary).NoWrap()
		})
		if archive.Clicked() {
			a.setSessionArchived(c, sess, archived)
		}
	})
}

// clampSearchIndex keeps the keyboard selection inside results.
func (a *App) clampSearchIndex(n int) {
	if n <= 0 {
		a.searchIndex = 0
		return
	}
	if a.searchIndex < 0 {
		a.searchIndex = 0
	}
	if a.searchIndex >= n {
		a.searchIndex = n - 1
	}
}

// moveSearchSelection moves the keyboard selection, wrapping at the ends.
func (a *App) moveSearchSelection(delta, n int) {
	if n <= 0 {
		a.searchIndex = 0
		return
	}
	a.searchIndex = (a.searchIndex + delta + n) % n
}

// openSearchSelection opens the highlighted result and closes the dialog.
func (a *App) openSearchSelection() {
	results := a.searchResults()
	if len(results) == 0 {
		return
	}
	a.clampSearchIndex(len(results))
	sess := results[a.searchIndex]
	a.paletteOpen = false
	a.openSession(sess.ID)
}

// The composer's "@" menu lists sessions of every workspace, while the
// sidebar's AR-08 session cache (sessions_cache_async.go) only holds the
// current workspace. The menu therefore needs its own session data source: one
// cached copy of every workspace's sessions plus their workspace names, so a
// frame never queries the store. It lives next to the other session data
// sources in this file rather than in App because app.go is owned by other
// changes; like liveMenus and shellUI, its state is keyed by the App.

// mentionSessionCandidate is one session row of the "@" menu: the session, the
// workspace name to show in its detail when it belongs to a workspace other
// than the current one ("" for the current workspace, whose rows need no extra
// location), and the session's working directory (its workspace root), which
// ranks discovery.
type mentionSessionCandidate struct {
	session   store.Session
	workspace string
	cwd       string
}

// mentionSessionListCache caches one App's cross-workspace "@" menu sessions.
type mentionSessionListCache struct {
	mu sync.Mutex
	// workspaceID is the current workspace the workspace labels were built
	// for: switching workspaces rebuilds the cache synchronously so a stale
	// label never names the wrong current workspace.
	workspaceID string
	items       []mentionSessionCandidate
	loadedAt    time.Time
	loading     bool
}

// mentionSessionCaches maps each App to its "@" menu session data source.
var mentionSessionCaches sync.Map // map[*App]*mentionSessionListCache

// mentionSessionCandidates returns the "@" menu's session rows: the current
// workspace's sessions from the cached sidebar list merged with the other
// workspaces' sessions from this file's cache, most recently updated first.
// Every workspace is represented so the rows can name the workspace of a
// session that is not the current one. Both lists are caches, so a row can lag
// a rename by one TTL; the menu keeps rendering while a background refresh
// replaces the list.
func (a *App) mentionSessionCandidates() []mentionSessionCandidate {
	if a.Ctrl == nil {
		return nil
	}
	current := a.Ctrl.CurrentWorkspaceID()
	cross := a.crossWorkspaceSessions()
	// The sidebar cache can still hold the previous workspace for one TTL
	// after a switch; label those rows from the freshly rebuilt cross cache so
	// they never read as the current workspace's sessions.
	labels := make(map[string]string, len(cross))
	for _, candidate := range cross {
		if candidate.workspace != "" {
			labels[candidate.session.ID] = candidate.workspace
		}
	}
	currentRoot := strings.TrimSpace(a.Ctrl.WorkspaceRoot())
	out := make([]mentionSessionCandidate, 0, maxMentionSessions)
	seen := make(map[string]struct{}, maxMentionSessions)
	for _, sess := range a.sessions() {
		seen[sess.ID] = struct{}{}
		candidate := mentionSessionCandidate{session: sess, cwd: currentRoot}
		if sess.WorkspaceID != current {
			candidate.workspace = labels[sess.ID]
		}
		out = append(out, candidate)
	}
	for _, candidate := range cross {
		if _, ok := seen[candidate.session.ID]; ok {
			continue
		}
		out = append(out, candidate)
	}
	// Most recently updated first is the base order the menu always had; the
	// discovery ranking then lifts the current working directory's sessions
	// above the other workspaces' (DSH's candidate affinity) while keeping the
	// base order inside each group.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].session.UpdatedAt.After(out[j].session.UpdatedAt)
	})
	return rankMentionCandidates(currentRoot, out)
}

// rankMentionCandidates applies sessionreference.Candidates ordering to the
// merged "@" menu rows: sessions whose working directory is the caller's come
// first, untitled sessions still fall back to their id for the reference label,
// and the incoming order is stable inside each affinity group.
func rankMentionCandidates(targetCwd string, candidates []mentionSessionCandidate) []mentionSessionCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	sessions := make([]sessionreference.Session, 0, len(candidates))
	byID := make(map[string]mentionSessionCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.session.ID] = candidate
		sessions = append(sessions, sessionreference.Session{
			ID:    candidate.session.ID,
			Cwd:   candidate.cwd,
			Title: candidate.session.Title,
		})
	}
	ranked, err := sessionreference.Candidates(targetCwd, "", len(sessions), sessions)
	if err != nil || len(ranked) != len(sessions) {
		return candidates
	}
	out := make([]mentionSessionCandidate, 0, len(ranked))
	for _, candidate := range ranked {
		out = append(out, byID[candidate.SessionID])
	}
	return out
}

// crossWorkspaceSessions returns the cached sessions outside the current
// workspace, refreshing them in the background once they are stale. The first
// load (and the first load after a workspace switch) is synchronous so the
// menu starts with real data, exactly like the sidebar's session cache.
func (a *App) crossWorkspaceSessions() []mentionSessionCandidate {
	current := a.Ctrl.CurrentWorkspaceID()
	cache := &mentionSessionListCache{}
	if existing, ok := mentionSessionCaches.Load(a); ok {
		cache = existing.(*mentionSessionListCache)
	} else {
		actual, _ := mentionSessionCaches.LoadOrStore(a, cache)
		cache = actual.(*mentionSessionListCache)
	}
	cache.mu.Lock()
	sameWorkspace := cache.workspaceID == current
	fresh := sameWorkspace && !cache.loadedAt.IsZero() &&
		time.Since(cache.loadedAt) < shellListFreshFor
	items := cache.items
	loading := cache.loading
	cache.mu.Unlock()
	if fresh || loading {
		return items
	}
	if !sameWorkspace || cache.loadedAt.IsZero() {
		list, err := a.loadCrossWorkspaceSessions(current)
		if err != nil {
			return nil
		}
		cache.mu.Lock()
		cache.workspaceID = current
		cache.items = list
		cache.loadedAt = time.Now()
		cache.mu.Unlock()
		return list
	}
	cache.mu.Lock()
	cache.loading = true
	cache.mu.Unlock()
	go func() {
		list, err := a.loadCrossWorkspaceSessions(current)
		cache.mu.Lock()
		cache.loading = false
		if err == nil {
			cache.workspaceID = current
			cache.items = list
			cache.loadedAt = time.Now()
		}
		cache.mu.Unlock()
	}()
	return items
}

// loadCrossWorkspaceSessions queries every workspace's sessions and labels the
// sessions outside currentWorkspaceID with their workspace name.
func (a *App) loadCrossWorkspaceSessions(currentWorkspaceID string) ([]mentionSessionCandidate, error) {
	ctx := context.Background()
	sessions, err := a.Ctrl.SessionsForWorkspace(ctx, "", true, false)
	if err != nil {
		return nil, err
	}
	workspaces, err := a.Ctrl.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(workspaces))
	roots := make(map[string]string, len(workspaces))
	for _, ws := range workspaces {
		names[ws.ID] = strings.TrimSpace(ws.Name)
		roots[ws.ID] = strings.TrimSpace(ws.Root)
	}
	out := make([]mentionSessionCandidate, 0, len(sessions))
	for _, sess := range sessions {
		name := ""
		if sess.WorkspaceID != currentWorkspaceID {
			name = names[sess.WorkspaceID]
		}
		out = append(out, mentionSessionCandidate{
			session:   sess,
			workspace: name,
			cwd:       roots[sess.WorkspaceID],
		})
	}
	return out, nil
}
