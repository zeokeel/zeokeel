package sidebar

// Sidebar view options: session sort (recent activity / name) and grouping
// (workspace tree / flat). The choices live in settings; a missing or malformed
// value falls back to the old defaults (recent activity, workspace groups).

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

const (
	sidebarSortRecent = "recent"
	sidebarSortName   = "name"
	sidebarGroupTree  = "workspace"
	sidebarGroupFlat  = "flat"
)

// sidebarViewSettings reads and normalizes the two persisted view options.
func sidebarViewSettings(settings contracts.SettingsService) (sortMode, groupMode string) {
	sortMode, groupMode = sidebarSortRecent, sidebarGroupTree
	if settings == nil {
		return sortMode, groupMode
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingSidebarSort, ""))) {
	case sidebarSortName:
		sortMode = sidebarSortName
	case sidebarSortRecent:
		sortMode = sidebarSortRecent
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingSidebarGroup, ""))) {
	case sidebarGroupFlat:
		groupMode = sidebarGroupFlat
	case sidebarGroupTree:
		groupMode = sidebarGroupTree
	}
	return sortMode, groupMode
}

// sortSidebarSessions returns a sorted copy: name uses the display title
// case-insensitively, recent uses UpdatedAt descending with stable input order
// for equal timestamps.
func sortSidebarSessions(sessions []contracts.ChatSessionInfo, sortMode string) []contracts.ChatSessionInfo {
	if len(sessions) < 2 {
		return sessions
	}
	ordered := append([]contracts.ChatSessionInfo(nil), sessions...)
	if sortMode == sidebarSortName {
		sort.SliceStable(ordered, func(i, j int) bool {
			left := strings.ToLower(strings.TrimSpace(sessionTitle(ordered[i])))
			right := strings.ToLower(strings.TrimSpace(sessionTitle(ordered[j])))
			if left == right {
				return ordered[i].ID < ordered[j].ID
			}
			return left < right
		})
		return ordered
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
	})
	return ordered
}

// buildSidebarViewMenu renders the native view-options popover. It is only
// available with a settings store so every choice can be persisted.
func buildSidebarViewMenu(c *ui.Context, runtime contracts.UIRuntime, state *paneState) {
	if state == nil || state.settings == nil {
		return
	}
	sortMode, groupMode := sidebarViewSettings(state.settings)
	ui.PopoverMenuButton(c, state.T("视图", "View"), func(m *ui.Menu) {
		m.Item(state.T("排序", "Sort")).Disabled(true)
		recent := m.Item(state.T("最近活动", "Recent activity"))
		recent.Checked(sortMode == sidebarSortRecent)
		if recent.Chosen() && sortMode != sidebarSortRecent {
			_ = state.settings.Set(contracts.SettingSidebarSort, sidebarSortRecent)
			runtime.Invalidate()
		}
		byName := m.Item(state.T("名称", "Name"))
		byName.Checked(sortMode == sidebarSortName)
		if byName.Chosen() && sortMode != sidebarSortName {
			_ = state.settings.Set(contracts.SettingSidebarSort, sidebarSortName)
			runtime.Invalidate()
		}
		m.Separator()
		m.Item(state.T("分组", "Group")).Disabled(true)
		byWorkspace := m.Item(state.T("按工作区", "By workspace"))
		byWorkspace.Checked(groupMode == sidebarGroupTree)
		if byWorkspace.Chosen() && groupMode != sidebarGroupTree {
			_ = state.settings.Set(contracts.SettingSidebarGroup, sidebarGroupTree)
			runtime.Invalidate()
		}
		flat := m.Item(state.T("平铺", "Flat"))
		flat.Checked(groupMode == sidebarGroupFlat)
		if flat.Chosen() && groupMode != sidebarGroupFlat {
			_ = state.settings.Set(contracts.SettingSidebarGroup, sidebarGroupFlat)
			runtime.Invalidate()
		}
	}).Tooltip(state.T("侧栏排序与分组", "Sidebar sort and grouping"))
}

// buildSidebarListTitle renders the flat session-list title row with the view
// options menu (used when no workspace backend exists).
func buildSidebarListTitle(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, state *paneState, title string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, title), t.SectionLabel())
		ui.Spacer(c).Grow(1)
		buildSidebarViewMenu(c, runtime, state)
	})
}
