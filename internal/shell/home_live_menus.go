package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// liveMenuState is the live-only popover state of one App. App lives in
// app.go, which this change does not own, so the state is kept here keyed by
// the App, the same way motion.go keeps the animation state. Only the view
// goroutine touches it, and the static App{Ctrl: nil} path never reads it.
type liveMenuState struct {
	permissionOpen bool
	modelOpen      bool
	moreOpen       bool

	// toolSummaryCollapsed hides the tool cards under the summary line. The
	// default false keeps the detailed Work details behavior visible.
	toolSummaryCollapsed bool

	// pendingTurn is the turn a jump control asked to reveal. The next frame
	// scrolls that turn's first message into view and clears it.
	pendingTurn int

	// Turn timing for the usage footer. The controller's Usage counters have
	// no duration, so the view measures the wall clock between the running
	// and idle phases. When no such measurement exists nothing is shown.
	turnRunning       bool
	turnStart         time.Time
	lastTurnDuration  time.Duration
	completedUsage    state.Usage
	hasCompletedUsage bool

	// lastPhase and doneSeen apply the "When to collapse work details"
	// setting: Immediately collapses a card as soon as its call ends,
	// On completion collapses every card when the turn goes idle.
	lastPhase state.Phase
	doneSeen  map[string]bool

	// Mention menu highlight. mentionKey is the query the index belongs to;
	// a new query resets the highlight to the first row. The items are
	// Focusable, so the arrows of the list's focus group and the composer's
	// Down/Up both move it.
	mentionIndex int
	mentionKey   string

	// breadcrumbOpen is the Files breadcrumb's collapsed-segments menu. It
	// only appears when the panel is too narrow for the whole path.
	breadcrumbOpen bool

	// Dismissed composer popups. Escape closes a visible mention/slash menu
	// before it considers stopping the running turn, and the menu stays
	// hidden while the draft that opened it is unchanged. Any edit of the
	// draft makes the derived popup visible again.
	mentionDismissedDraft string
	slashDismissedDraft   string
	// menuDraft/menuDraftSet let the view notice every draft edit, so any
	// change (including typing back to the dismissed text) clears the
	// dismissal instead of comparing against the stored draft forever.
	menuDraft    string
	menuDraftSet bool
}

// liveMenuStates maps each App to its live popover state. Entries live as long
// as their App; the real app has one.
var liveMenuStates sync.Map // map[*App]*liveMenuState

// liveMenus returns the popover state of a, making it on first use.
func (a *App) liveMenus() *liveMenuState {
	if s, ok := liveMenuStates.Load(a); ok {
		return s.(*liveMenuState)
	}
	s := &liveMenuState{}
	actual, _ := liveMenuStates.LoadOrStore(a, s)
	return actual.(*liveMenuState)
}

// menuText picks an English or Chinese label for the few controls that have no
// catalog entry yet. The shared catalog lives in i18n.go, which this change
// does not own; using T for existing keys and this helper for the new ones
// keeps both languages working without editing that file.
func (a *App) menuText(en, zh string) string {
	if a.lang() == LangZH {
		return zh
	}
	return en
}

// autoReviewLabel is the experimental access mode's visible label. The mode
// has no catalog entry in i18n.go (outside this change's ownership), so the
// live shell's menuText helper supplies both languages.
func (a *App) autoReviewLabel() string {
	return a.menuText("Auto review EXP", "自动审查（实验）")
}

// autoReviewDescription explains what the experimental mode does. It is shown
// under the option in the permission menus.
func (a *App) autoReviewDescription() string {
	return a.menuText(
		"The model reviews mutating tool calls and allows or denies them automatically.",
		"由模型审查会修改数据的工具调用，并自动允许或拒绝。")
}

// composerPermissionLabel is the permission chip's text. DSH calls the
// restrictive mode "Read Only"; the persisted value is session.PermissionAsk.
func (a *App) composerPermissionLabel(v string) string {
	switch v {
	case session.PermissionFull:
		return a.T("settings.full_access")
	case session.PermissionAsk:
		return a.T("settings.read_only")
	case session.PermissionAutoReview:
		return a.autoReviewLabel()
	default:
		return a.T("settings.workspace_write")
	}
}

// composerPermissionMode is the mode the composer controls must show. On a
// session screen it is the session's effective (persisted) mode, not the
// global default: after switching a session to Full access the chip must say
// Full access even when Settings still defaults to Read Only.
func (a *App) composerPermissionMode() string {
	if a.Ctrl != nil {
		switch a.backgroundScreen() {
		case Session:
			if id := a.Ctrl.CurrentSessionID(); id != "" {
				return a.Ctrl.SessionPermissionOrDefault(id)
			}
		case Home:
			// Home stages the access mode for the next session instead of
			// showing (or mutating) the last session's mode.
			return a.homePermissionMode()
		}
	}
	return a.currentSettings().Permission
}

// livePermissionChip is the composer's access-mode control. Clicking it opens
// a real menu that persists the choice through updateSettings instead of
// jumping to the Settings screen.
func (a *App) livePermissionChip(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		ui.Row(c).Height(28).Padding(0, design.Space1, 0, design.Space2).
			Gap(design.Space1).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, homeIconShield).Size(14, 14).TextColor(t.LabelSecondary)
			ui.Text(c, a.permissionLabel(a.currentSettings().Permission)).FontSize(13).
				FixedLineHeight(20).FontWeight(500).TextColor(t.LabelSecondary)
			ui.Icon(c, homeIconChevron).Size(14, 14).TextColor(t.LabelTertiary)
		})
		return
	}
	st := a.liveMenus()
	mode := a.composerPermissionMode()
	current := a.composerPermissionLabel(mode)
	chip := ui.Row(c).Height(28).Padding(0, design.Space1, 0, design.Space2).
		Gap(design.Space1).AlignItems(ui.Center).
		Label(a.menuText("Access mode, current: ", "访问模式，当前：") + current).
		Value(current).Role(ui.RolePopUpButton).Focusable()
	chip.Children(func() {
		ui.Icon(c, homeIconShield).Size(14, 14).TextColor(t.LabelSecondary)
		// DSH's narrow composer trigger is the 44px icon+chevron only; the
		// full "Workspace Write" label wraps the 28px toolbar once the
		// docked panel narrows the composer to ~360px.
		if a.composerPopupWidthFor(c) >= narrowComposerLabelW {
			ui.Text(c, current).FontSize(13).
				FixedLineHeight(20).FontWeight(500).TextColor(t.LabelSecondary)
		}
		ui.Icon(c, homeIconChevron).Size(14, 14).TextColor(t.LabelTertiary)
	})
	chip.Expanded(st.permissionOpen)
	chip.OnClick(func() { st.permissionOpen = !st.permissionOpen })
	ui.Popover(c, chip, &st.permissionOpen, func() { a.permissionMenu(c, t) })
}

// permissionMenu lists the access modes, marking the session's effective one.
// Auto review EXP carries a one-line description because its behavior is not
// obvious from the label alone.
func (a *App) permissionMenu(c *ui.Context, t *design.Tokens) {
	mode := a.composerPermissionMode()
	options := []struct {
		label  string
		desc   string
		value  string
		active bool
	}{
		{a.T("settings.read_only"), "", session.PermissionAsk, mode == session.PermissionAsk},
		{a.T("settings.workspace_write"), "", session.PermissionWorkspace, mode == session.PermissionWorkspace},
		{a.T("settings.full_access"), "", session.PermissionFull, mode == session.PermissionFull},
		{a.autoReviewLabel(), a.autoReviewDescription(), session.PermissionAutoReview, mode == session.PermissionAutoReview},
	}
	for _, option := range options {
		value := option.value
		label := option.label
		desc := option.desc
		ui.Column(c).Gap(1).Children(func() {
			item := liveMenuItem(c, t, label, option.active)
			item.OnClick(func() {
				a.liveMenus().permissionOpen = false
				if a.Ctrl == nil {
					return
				}
				if a.backgroundScreen() == Home {
					// Stage for the next created session; do not touch the last
					// session or the General default.
					a.homePermission = value
					return
				}
				// Session: write only this session's override. The General
				// setting is the future-session default and is not saved here.
				a.Ctrl.SetSessionPermission(a.Ctrl.CurrentSessionID(), value)
			})
			if desc != "" {
				ui.Text(c, desc).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).MaxLines(2).
					Padding(0, design.Space2, 0, design.Space2)
			}
		})
	}
}

// liveModelMenuBadge is the composer's model selector. It shows the selected
// default, its configured reasoning effort and a chevron, and opens the model
// list. The static App keeps modelBadge's baseline text.
func (a *App) liveModelMenuBadge(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		a.modelBadge(c, t)
		return
	}
	st := a.liveMenus()
	modelLabel := a.composerModelLabel()
	badgeAccessible := a.menuText("Select model, current "+modelLabel, "选择模型，当前 "+modelLabel)
	if model := a.composerModel(); model != nil && model.ReasoningEffort != "" {
		badgeAccessible += a.menuText(", reasoning effort "+model.ReasoningEffort, "，推理强度 "+model.ReasoningEffort)
	}
	badge := ui.Row(c).Height(28).Padding(0, design.Space1, 0, design.Space2).
		Gap(design.Space1).AlignItems(ui.Center).
		Label(badgeAccessible).Value(modelLabel).Role(ui.RolePopUpButton).Focusable()
	badge.Children(func() {
		ui.Text(c, modelLabel).FontSize(13).FixedLineHeight(20).
			TextColor(t.LabelSecondary).MaxLines(1)
		if model := a.composerModel(); model != nil && model.ReasoningEffort != "" {
			tag := homeTagAccent(t)
			ui.Row(c).Padding(1, design.Space2).Radius(design.RadiusMax).
				Background(tag.Alpha(0.10)).Children(func() {
				ui.Text(c, model.ReasoningEffort).FontSize(11).FixedLineHeight(17).
					FontWeight(500).TextColor(tag)
			})
		}
		ui.Icon(c, homeIconChevron).Size(14, 14).TextColor(t.LabelTertiary)
	})
	badge.Expanded(st.modelOpen)
	badge.OnClick(func() { st.modelOpen = !st.modelOpen })
	ui.Popover(c, badge, &st.modelOpen, func() { a.modelPickerMenu(c, t) })
}

// modelPickerMenu lists every configured model and makes a click its default.
func (a *App) modelPickerMenu(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	models, err := a.Ctrl.Models().List()
	if err != nil {
		ui.Text(c, a.T("models.could_not_load_models")).FontSize(12).
			FixedLineHeight(18).TextColor(t.Danger)
		return
	}
	if len(models) == 0 {
		ui.Text(c, a.T("models.no_models_configured")).FontSize(12).
			FixedLineHeight(18).TextColor(t.LabelTertiary)
		return
	}
	currentID := a.composerModelID()
	for _, model := range models {
		model := model
		selected := model.ID == currentID
		row := ui.Row(c).Height(36).PaddingX(design.Space2).Gap(design.Space2).
			Radius(design.RadiusSM).AlignItems(ui.Center).Width(240).
			Label(modelDisplayName(model)).Role(ui.RoleMenuItemRadio).
			Checked(selected).Focusable()
		if row.Hovered() {
			row = row.Background(t.BgHover)
		}
		row.Children(func() {
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, modelDisplayName(model)).FontSize(13).FixedLineHeight(18).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				ui.Text(c, modelDetail(model)).FontSize(11).FixedLineHeight(15).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			})
			if selected {
				ui.Text(c, "✓").FontSize(12).TextColor(t.Accent)
			}
		})
		row.OnClick(func() {
			a.liveMenus().modelOpen = false
			if a.Ctrl == nil {
				return
			}
			if a.backgroundScreen() == Home {
				// Stage for the next created session; the global default in
				// Settings > Models is not changed from the composer.
				a.homeModel = model.ID
				return
			}
			if id := a.Ctrl.CurrentSessionID(); id != "" {
				if err := a.Ctrl.SetSessionModel(id, model.ID); err != nil {
					c.Toast(a.T("models.could_not_select_model"))
				}
			}
		})
	}
}

// homeWorkspacePicker lists the known workspaces and switches to the clicked
// one. It is only built while the chip's popover is open.
func (a *App) homeWorkspacePicker(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	ctx := context.Background()
	workspaces, err := a.Ctrl.ListWorkspaces(ctx)
	if err != nil {
		ui.Text(c, a.T("workspace.could_not_load_workspaces")).FontSize(12).
			FixedLineHeight(18).TextColor(t.Danger)
		return
	}
	if len(workspaces) == 0 {
		ui.Text(c, a.T("workspace.add_workspace")).FontSize(12).
			FixedLineHeight(18).TextColor(t.LabelTertiary)
		return
	}
	current := a.Ctrl.CurrentWorkspaceID()
	for _, ws := range workspaces {
		ws := ws
		row := ui.Row(c).Height(34).PaddingX(design.Space2).Gap(design.Space2).
			Radius(design.RadiusSM).AlignItems(ui.Center).Width(260).
			Label(a.menuText("Switch to "+ws.Name, "切换到 "+ws.Name)).
			Role(ui.RoleMenuItemRadio).Checked(ws.ID == current).Focusable()
		if row.Hovered() {
			row = row.Background(t.BgHover)
		}
		row.Children(func() {
			ui.Image(c, homeIconFolder).Size(15, 15).TextColor(t.LabelSecondary)
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, ws.Name).FontSize(13).FixedLineHeight(18).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				ui.Text(c, ws.Root).FontSize(11).FixedLineHeight(15).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			})
			if ws.ID == current {
				ui.Text(c, "✓").FontSize(12).TextColor(t.Accent)
			}
		})
		row.OnClick(func() {
			a.wsPickerOpen = false
			a.switchWorkspaceFromHome(c, ws.ID)
		})
	}
	// DSH's picker ends with an add action; without it this chip offered no
	// way to add a workspace once one already existed.
	add := ui.Row(c).Height(34).PaddingX(design.Space2).Gap(design.Space2).
		Radius(design.RadiusSM).AlignItems(ui.Center).Width(260).
		Label(a.T("workspace.add_workspace_ellipsis")).
		Role(ui.RoleMenuItem).Focusable()
	if add.Hovered() {
		add = add.Background(t.BgHover)
	}
	add.Children(func() {
		ui.Icon(c, homeIconPlus).Size(15, 15).TextColor(t.LabelSecondary)
		ui.Text(c, a.T("workspace.add_workspace_ellipsis")).FontSize(13).FixedLineHeight(18).
			TextColor(t.LabelPrimary)
	})
	add.OnClick(func() {
		a.wsPickerOpen = false
		a.beginAddWorkspace()
	})
}

// liveMenuItem is one row of a composer or header popover.
func liveMenuItem(c *ui.Context, t *design.Tokens, label string, active bool) ui.Element {
	item := ui.Button(c, label).Height(30).PaddingX(design.Space2).
		Radius(design.RadiusSM).TextColor(t.LabelPrimary)
	if active {
		item = item.Background(t.BgHover)
	}
	return item
}

// sessionHeaderMenu is DSH's session-header More actions popover: Fork
// session, Download session log and Feedback. Session CRUD
// (rename/archive/delete/copy title) lives on the sidebar row's context menu,
// where DSH keeps it, so this menu no longer claims parity while showing a
// different set.
func (a *App) sessionHeaderMenu(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil {
		return
	}
	sess, err := a.Ctrl.CurrentSession()
	if err != nil || sess == nil {
		return
	}
	closeMenu := func() { a.liveMenus().moreOpen = false }

	fork := liveMenuItem(c, t, a.tr("Fork session", "创建分支会话"), false)
	fork.OnClick(func() {
		closeMenu()
		a.forkSession(c, *sess)
	})
	download := liveMenuItem(c, t, a.tr("Download session log", "下载会话日志"), false)
	download.OnClick(func() {
		closeMenu()
		a.downloadSessionLog(c, *sess)
	})
	feedback := liveMenuItem(c, t, a.tr("Feedback", "反馈"), false)
	feedback.OnClick(func() {
		closeMenu()
		a.recordSessionFeedback(c, *sess)
	})
}

// forkSession copies the current session and makes the copy current. The
// controller loads the copied history into the live snapshot; the app mirrors
// that snapshot so the shell switches to the branch immediately.
func (a *App) forkSession(c *ui.Context, sess store.Session) {
	if a.Ctrl == nil {
		c.Toast(a.tr("Fork is not available", "分支不可用"))
		return
	}
	if _, err := a.Ctrl.ForkSession(context.Background(), sess.ID); err != nil {
		c.Toast(fmt.Sprintf(a.tr("Could not fork the session: %v", "会话分支失败：%v"), err))
		return
	}
	if snap := a.Ctrl.Snapshot(); snap != nil {
		a.SetSnapshot(snap)
	}
	c.Toast(a.tr("Forked session", "已创建分支会话"))
}

// downloadSessionLog exports the session's JSONL history through the
// save-dialog seam (or a deterministic workspace path when no picker is
// injected) and reports where it was written.
func (a *App) downloadSessionLog(c *ui.Context, sess store.Session) {
	if a.Ctrl == nil {
		c.Toast(a.tr("Session log export is not available", "会话日志导出不可用"))
		return
	}
	dir := a.workspaceRoot()
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	suggested := filepath.Join(dir, "zeokeel-session-"+sess.ID+".jsonl")
	path := suggested
	if a.SaveSessionLog != nil {
		chosen, err := a.SaveSessionLog(suggested)
		if err != nil {
			c.Toast(fmt.Sprintf(a.tr("Could not save the session log: %v", "会话日志保存失败：%v"), err))
			return
		}
		if strings.TrimSpace(chosen) == "" {
			c.Toast(a.tr("Session log export cancelled", "已取消导出会话日志"))
			return
		}
		path = chosen
	}
	if err := a.Ctrl.ExportSession(context.Background(), sess.ID, path); err != nil {
		c.Toast(fmt.Sprintf(a.tr("Could not save the session log: %v", "会话日志保存失败：%v"), err))
		return
	}
	c.Toast(fmt.Sprintf(a.tr("Session log saved to %s", "会话日志已保存到 %s"), path))
}

// recordSessionFeedback persists a session-level feedback marker. The store
// keeps the row (and the legacy setting), so it is visible after a reopen and
// included in the session export.
func (a *App) recordSessionFeedback(c *ui.Context, sess store.Session) {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		c.Toast(a.tr("Feedback is not available", "反馈不可用"))
		return
	}
	if err := a.Ctrl.RecordFeedback(context.Background(), sess.ID, "session", "recorded", ""); err != nil {
		c.Toast(a.tr("Could not record feedback", "反馈记录失败"))
		return
	}
	c.Toast(a.tr("Feedback recorded", "反馈已记录"))
}

// sessionHeaderMore is the "More actions" button the header renders before
// the right-sidebar toggle.
func (a *App) sessionHeaderMore(c *ui.Context, t *design.Tokens) {
	st := a.liveMenus()
	more := ui.Row(c).Size(28, 28).Center().
		Label(a.menuText("More actions", "更多操作")).Role(ui.RoleButton).Focusable()
	more.Children(func() {
		ui.Text(c, "⋯").FontSize(13).FontWeight(700).TextColor(t.LabelSecondary)
	})
	more.Expanded(st.moreOpen)
	more.OnClick(func() { st.moreOpen = !st.moreOpen })
	ui.Popover(c, more, &st.moreOpen, func() { a.sessionHeaderMenu(c, t) })
}

// observeTurnTiming records how long a turn ran so the usage footer can show
// "Completed in" and a real tok/s figure. Nothing is recorded when the view
// never saw the running phase: the footer then omits those fields rather than
// inventing a duration.
func (a *App) observeTurnTiming(now time.Time, snap *state.Snapshot) {
	if snap == nil {
		return
	}
	st := a.liveMenus()
	if snap.Phase == state.PhaseRunning {
		if !st.turnRunning {
			st.turnRunning = true
			st.turnStart = now
			st.lastTurnDuration = 0
		}
		return
	}
	if st.turnRunning {
		if d := now.Sub(st.turnStart); d > 0 {
			st.lastTurnDuration = d
		}
		st.turnRunning = false
		st.completedUsage = snap.Usage
		st.hasCompletedUsage = true
		return
	}
	// A turn that started and finished between two frames leaves no duration
	// behind. Drop the previous turn's measurement as soon as the counters
	// show a different turn, so the footer never pairs new tokens with an old
	// clock.
	if st.hasCompletedUsage && snap.Usage != st.completedUsage {
		st.lastTurnDuration = 0
		st.hasCompletedUsage = false
	}
}

// liveUsageText is the composer's usage footer. The base counters always show;
// Completed in, tok/s and cache% appear only when the snapshot and the view's
// own clock make them computable.
func (a *App) liveUsageText(snap *state.Snapshot) string {
	if snap == nil {
		return ""
	}
	u := snap.Usage
	parts := []string{a.T("session.live_usage", u.Turns, u.Steps, u.Out)}
	st := a.liveMenus()
	if !st.turnRunning && st.lastTurnDuration > 0 {
		d := st.lastTurnDuration
		parts = append(parts, a.menuText("Completed in ", "完成于 ")+formatTurnDuration(d))
		if u.Out > 0 {
			tps := int(float64(u.Out)/d.Seconds() + 0.5)
			if tps > 0 {
				parts = append(parts, fmt.Sprintf("%d tok/s", tps))
			}
		}
	}
	if u.In > 0 && u.CacheHit > 0 {
		pct := u.CacheHit * 100 / u.In
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		parts = append(parts, fmt.Sprintf("%s %d%%", a.menuText("cache", "缓存"), pct))
	}
	return joinUsageParts(parts)
}

// joinUsageParts joins the footer's non-empty pieces.
func joinUsageParts(parts []string) string {
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += part
	}
	return out
}

// formatTurnDuration is DSH's short completion time: seconds only under a
// minute, then the "Xm Ys" form the working indicator uses.
func formatTurnDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= time.Minute {
		return formatDiveTime(d)
	}
	seconds := d.Seconds()
	if seconds < 10 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	return fmt.Sprintf("%.0fs", seconds)
}
