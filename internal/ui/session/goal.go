package session

// Goal v2 UI: the composer dock, its lifecycle actions and the transcript's
// goal-round receipt.
//
// The durable truth is the per-session GoalService (contracts.KeyGoal): the UI
// caches only the ACTIVE conversation's snapshot (state.goalSessionID/goal) and
// re-reads it when the conversation switches, so one session's goal is never
// displayed in another. Every lifecycle action is a direct human GoalService
// call (edit/pause/resume/complete/clear with the snapshot's revision as the
// compare-and-set token); the engine-side continuation driver owns automatic
// rounds, so this module never sends a continuation prompt.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// goalDockStatus maps the DSH phase (plus the process-lifetime activation) onto
// the product's status vocabulary. `active` is never rendered as `running`: an
// armed goal is in progress (the engine may advance it), a disarmed active goal
// is waiting for the human to continue it.
func goalDockStatus(st *state, goal contracts.Goal) (label string, kind uikit.StatusKind) {
	switch goal.Phase {
	case contracts.GoalPhasePaused:
		return st.tr("目标已暂停", "Goal paused"), uikit.StatusInterrupted
	case contracts.GoalPhaseBlocked:
		return st.tr("目标受阻", "Goal blocked"), uikit.StatusError
	case contracts.GoalPhaseComplete:
		return st.tr("目标已完成", "Goal completed"), uikit.StatusSuccess
	default:
		if goal.Activation == contracts.GoalActivationArmed {
			// "执行中" (not 进行中): a goal phase is not the conversation's run
			// state, and the dock must not read as a second "运行中" badge.
			return st.tr("目标执行中", "Goal running"), uikit.StatusInProgress
		}
		return st.tr("等待继续", "Waiting to continue"), uikit.StatusPending
	}
}

// goalActivationTooltip explains the activation observation: whether the
// engine may advance this goal in this process without the human acting. The
// first sentence of each phase names the difference between the goal phase and
// the conversation's run state, so "目标执行中" is never read as "运行中".
func goalActivationTooltip(st *state, goal contracts.Goal) string {
	switch goal.Phase {
	case contracts.GoalPhaseActive:
		if goal.Activation == contracts.GoalActivationArmed {
			return st.tr("这是目标阶段，不等于会话的运行状态；引擎会自动续跑该目标，停止或重启后需要手动继续。",
				"This is the goal phase, not the session's run state. The engine continues this goal automatically; after a stop or restart you continue it manually.")
		}
		return st.tr("这是目标阶段，不等于会话的运行状态；自动续跑已关闭，点「继续目标」恢复。",
			"This is the goal phase, not the session's run state. Automatic continuation is off; choose Continue goal to resume it.")
	case contracts.GoalPhasePaused:
		return st.tr("这是目标阶段，不等于会话的运行状态；目标已暂停，不会自动续跑，点「继续目标」恢复。",
			"This is the goal phase, not the session's run state. The goal is paused and is not continued automatically; choose Continue goal to resume it.")
	case contracts.GoalPhaseBlocked:
		return st.tr("这是目标阶段，不等于会话的运行状态；目标受阻，不会自动续跑，处理阻塞后可继续。",
			"This is the goal phase, not the session's run state. The goal is blocked and is not continued automatically; continue it after resolving the block.")
	case contracts.GoalPhaseComplete:
		return st.tr("目标已完成，没有自动续跑。", "The goal is complete; nothing continues it.")
	default:
		return ""
	}
}

// goalResumeVisible reports whether a human "continue goal" action applies:
// an active-but-disarmed goal (a restart or a user Stop), a paused goal and a
// blocked goal can be resumed. A complete goal cannot.
func goalResumeVisible(goal contracts.Goal) bool {
	switch goal.Phase {
	case contracts.GoalPhaseActive:
		return goal.Activation != contracts.GoalActivationArmed
	case contracts.GoalPhasePaused, contracts.GoalPhaseBlocked:
		return true
	default:
		return false
	}
}

// goalResumeBlockedByCap reports an exhausted round budget: the service rejects
// a resume then, so the UI disables the action and points at the cap editor.
func goalResumeBlockedByCap(goal contracts.Goal) bool {
	return goal.Phase == contracts.GoalPhaseBlocked &&
		goal.MaxGoalRounds > 0 && goal.RoundsStarted >= goal.MaxGoalRounds
}

// goalNonTerminal reports whether the ACTIVE conversation's goal can be
// completed.
func goalNonTerminal(st *state) bool {
	return st != nil && st.goal != nil && st.goal.Phase != contracts.GoalPhaseComplete
}

// goalBlockedReasonText is the human copy of a blocked goal's reason. The
// stable code stays in the tooltip; the round-limit code gets actionable copy
// instead of the backend's English diagnostic.
func goalBlockedReasonText(st *state, goal contracts.Goal) string {
	if goal.BlockedReason == nil {
		return ""
	}
	message := strings.TrimSpace(goal.BlockedReason.Message)
	switch goal.BlockedReason.Code {
	case contracts.GoalBlockedCodeRoundLimit:
		return fmt.Sprintf(st.tr("自动续跑已达轮次上限（%d/%d）：在「编辑目标」中调高上限后可继续。",
			"Automatic continuation reached its round limit (%d/%d); raise the cap in Edit goal to continue."),
			goal.RoundsStarted, goal.MaxGoalRounds)
	case contracts.GoalBlockedCodeModelReported:
		if message == "" {
			return ""
		}
		return st.tr("受阻原因：", "Blocked: ") + message
	default:
		return message
	}
}

// goalBlockedReasonTooltip is the stable blocked-reason code plus its concrete
// message, kept in the tooltip so the dock copy stays quiet.
func goalBlockedReasonTooltip(goal contracts.Goal) string {
	if goal.BlockedReason == nil {
		return ""
	}
	code := strings.TrimSpace(goal.BlockedReason.Code)
	message := strings.TrimSpace(goal.BlockedReason.Message)
	switch {
	case code == "":
		return message
	case message == "":
		return code
	default:
		return code + ": " + message
	}
}

// syncActiveGoal points the cached goal at the conversation on screen. It runs
// at frame start; only a switch re-reads the service, so a frame never performs
// a backend read.
func (st *state) syncActiveGoal(sessionID string) {
	if st == nil {
		return
	}
	if st.goalSessionID == sessionID {
		return
	}
	st.goalSessionID = sessionID
	st.goal = nil
	if st.goalService == nil || sessionID == "" {
		return
	}
	if goal, ok := st.goalService.Goal(sessionID); ok {
		st.setGoalSnapshot(goal)
	}
}

// refreshGoalSnapshot re-reads the active conversation's goal after a change
// notification or a failed compare-and-set.
func (st *state) refreshGoalSnapshot() {
	if st == nil || st.goalService == nil || st.goalSessionID == "" {
		return
	}
	if goal, ok := st.goalService.Goal(st.goalSessionID); ok {
		st.setGoalSnapshot(goal)
		return
	}
	st.goal = nil
}

// setGoalSnapshot caches one goal returned by the service.
func (st *state) setGoalSnapshot(goal contracts.Goal) {
	copied := goal
	st.goal = &copied
}

// goalRef is the compare-and-set token of the cached snapshot.
func (st *state) goalRef() (contracts.GoalRef, bool) {
	if st == nil || st.goal == nil {
		return contracts.GoalRef{}, false
	}
	return contracts.GoalRef{ID: st.goal.ID, Revision: st.goal.Revision}, true
}

// goalErrorText localizes the GoalService sentinels; unknown errors keep their
// message (the caller adds the localized action prefix).
func (st *state) goalErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, contracts.ErrGoalNotFound):
		return st.tr("当前会话没有目标。", "This conversation has no goal.")
	case errors.Is(err, contracts.ErrGoalStaleRef):
		return st.tr("目标已被其他操作更新，请重试。", "The goal changed in the meantime; try again.")
	case errors.Is(err, contracts.ErrGoalConflict):
		return st.tr("当前会话已有未完成目标，请先编辑或完成它。",
			"This conversation already has an unfinished goal; edit or complete it first.")
	case errors.Is(err, contracts.ErrGoalInvalidTransition):
		return st.tr("当前目标状态不允许这个操作。", "The current goal phase does not allow that action.")
	case errors.Is(err, contracts.ErrGoalRoundLimit):
		return st.tr("自动续跑轮次已用尽。", "The automatic continuation round budget is exhausted.")
	case errors.Is(err, contracts.ErrGoalDirectHumanRequired):
		return st.tr("这个操作需要直接的人类回合。", "That action needs a direct human turn.")
	default:
		return err.Error()
	}
}

// goalActionFailed records one localized lifecycle failure.
func goalActionFailed(st *state, runtime contracts.UIRuntime, prefix string, err error) {
	st.goalActionErr = prefix + st.goalErrorText(err)
	st.refreshGoalSnapshot()
	runtime.Invalidate()
}

// goalActionSucceeded publishes the service's new snapshot and clears the last
// failure.
func goalActionSucceeded(st *state, runtime contracts.UIRuntime, goal contracts.Goal, toast string) {
	st.setGoalSnapshot(goal)
	st.goalActionErr = ""
	if toast != "" {
		st.toast(contracts.NotifySuccess, toast)
	}
	runtime.Invalidate()
}

// pauseGoalAction pauses the cached active goal.
func pauseGoalAction(st *state, runtime contracts.UIRuntime) {
	if st == nil || st.goalService == nil || st.goal == nil {
		return
	}
	ref, ok := st.goalRef()
	if !ok {
		return
	}
	sessionID := st.goal.SessionID
	goal, err := st.goalService.UpdateGoal(context.Background(), sessionID, ref, contracts.GoalUpdate{
		Action: contracts.GoalActionPause,
	})
	if err != nil {
		goalActionFailed(st, runtime, st.tr("暂停目标失败：", "Pause goal failed: "), err)
		return
	}
	goalActionSucceeded(st, runtime, goal, st.tr("目标已暂停", "Goal paused"))
}

// resumeGoalAction continues an active-disarmed, paused or blocked goal through
// the human path (the model cannot resume a paused goal).
func resumeGoalAction(st *state, runtime contracts.UIRuntime) {
	if st == nil || st.goalService == nil || st.goal == nil {
		return
	}
	if goalResumeBlockedByCap(*st.goal) {
		st.goalActionErr = st.tr("自动续跑轮次已用尽：在「编辑目标」中调高上限后再继续。",
			"The automatic continuation round budget is exhausted; raise the cap in Edit goal first.")
		runtime.Invalidate()
		return
	}
	ref, ok := st.goalRef()
	if !ok {
		return
	}
	sessionID := st.goal.SessionID
	goal, err := st.goalService.UpdateGoal(context.Background(), sessionID, ref, contracts.GoalUpdate{
		Action: contracts.GoalActionResume,
	})
	if err != nil {
		goalActionFailed(st, runtime, st.tr("继续目标失败：", "Resume goal failed: "), err)
		return
	}
	goalActionSucceeded(st, runtime, goal, st.tr("目标已继续", "Goal resumed"))
}

// completeGoalAction marks the cached goal complete.
func completeGoalAction(st *state, runtime contracts.UIRuntime) {
	if st == nil || st.goalService == nil || st.goal == nil {
		return
	}
	ref, ok := st.goalRef()
	if !ok {
		return
	}
	sessionID := st.goal.SessionID
	goal, err := st.goalService.UpdateGoal(context.Background(), sessionID, ref, contracts.GoalUpdate{
		Action: contracts.GoalActionComplete,
	})
	if err != nil {
		goalActionFailed(st, runtime, st.tr("完成目标失败：", "Complete goal failed: "), err)
		return
	}
	goalActionSucceeded(st, runtime, goal, st.tr("目标已完成", "Goal completed"))
}

// clearGoalAction removes the cached goal with its revision check; the record
// is gone (not merely ended), so the confirmation is the caller's job.
func clearGoalAction(st *state, runtime contracts.UIRuntime) {
	if st == nil || st.goalService == nil || st.goal == nil {
		return
	}
	ref, ok := st.goalRef()
	if !ok {
		return
	}
	sessionID := st.goal.SessionID
	cleared, err := st.goalService.ClearGoal(context.Background(), sessionID, ref)
	if err != nil {
		goalActionFailed(st, runtime, st.tr("清除目标失败：", "Clear goal failed: "), err)
		return
	}
	if !cleared {
		// Another change already removed it: just drop the stale view.
		st.goal = nil
	}
	st.goal = nil
	st.goalActionErr = ""
	st.toast(contracts.NotifySuccess, st.tr("目标已清除", "Goal cleared"))
	runtime.Invalidate()
}

// buildGoalDock is the active goal's one compact line above the composer: the
// phase/activation badge, the objective (ellipsized, full text in the tooltip),
// the automatic-continuation round budget, the human 「继续目标」 action while
// the goal waits, a hover-only clear entry and the ⋯ goal menu. The group is an
// inline, left-aligned cluster capped at 70% of the reading measure; a blocked
// goal adds its concrete reason on a quiet second line.
func buildGoalDock(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st == nil || st.goal == nil {
		return
	}
	goal := *st.goal
	label, kind := goalDockStatus(st, goal)
	th := t.Theme()
	ui.Row(c).FillWidth().Justify(ui.Start).Children(func() {
		group := ui.Column(c).MaxWidthPercent(70).Gap(design.Space1)
		group.Children(func() {
			ui.Row(c).Height(t.Scale(th.Space(7))).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				badge := uikit.StatusBadge(c, t, kind, label)
				if tooltip := goalActivationTooltip(st, goal); tooltip != "" {
					// The phase badge is a status region: naming it (and giving it
					// the status role) carries the tooltip to assistive technology
					// as its description, so the goal-phase-vs-run-state
					// distinction is not pointer-only.
					badge.Role(ui.RoleStatus).Label(label).Tooltip(tooltip)
				}
				if objective := strings.TrimSpace(goal.Objective); objective != "" {
					design.Apply(ui.Text(c, objective), t.Font(design.TypeXXS)).
						TextColor(t.LabelSecondary).Shrink(1).MaxLines(1).
						Ellipsis("…").Tooltip(objective)
				}
				if goal.MaxGoalRounds > 0 {
					metaSeparator(c, t)
					rounds := fmt.Sprintf(st.tr("%d/%d 轮", "%d/%d rounds"), goal.RoundsStarted, goal.MaxGoalRounds)
					design.Apply(ui.Text(c, rounds), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).NoWrap().Shrink(0).
						Tooltip(st.tr("自动续跑轮次（已开始 / 上限）", "Automatic continuation rounds (started / cap)"))
				}
				if goalResumeVisible(goal) {
					buildGoalResumeButton(c, t, runtime, st, goal)
				}
				// The clear entry appears on hover so the resting strip stays one
				// quiet line; it opens the same destructive confirmation as the
				// menu's 清除目标记录 instead of clearing on a stray click. It is
				// an icon-only action, so it goes through uikit.IconButton and
				// gets the shared 24 DIP hit target (AP-17); the glyph keeps the
				// dock's quiet tertiary ink.
				if group.Hovered() {
					uikit.IconButton(c, uikit.IconButtonOptions{
						Key:      "session.goal.clear",
						Label:    st.tr("清除目标", "Clear goal"),
						Tooltip:  st.tr("清除目标记录（需确认）", "Clear the goal record (asks first)"),
						Icon:     icons.Close(),
						Size:     design.IconSizeS,
						InkColor: t.LabelTertiary,
						OnClick: func() {
							st.goalConfirm = "clear"
							runtime.Invalidate()
						},
					})
				}
				buildGoalDockMenuButton(c, t, runtime, st)
			})
			if goal.Phase == contracts.GoalPhaseBlocked {
				if reason := goalBlockedReasonText(st, goal); reason != "" {
					reasonElement := design.Apply(ui.Text(c, reason), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
					if tooltip := goalBlockedReasonTooltip(goal); tooltip != "" {
						reasonElement.Tooltip(tooltip)
					}
				}
			}
		})
	})
}

// buildGoalResumeButton is the quiet dock action that re-arms an active goal
// after a restart/Stop or leaves the paused/blocked phase.
func buildGoalResumeButton(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, goal contracts.Goal) {
	label := st.tr("继续目标", "Continue goal")
	button := design.Apply(ui.Button(c.Key("session.goal.resume"), label), t.SecondaryButton()).
		Height(design.RowCompactHeight).Shrink(0)
	if goalResumeBlockedByCap(goal) {
		button.Disabled(true).Tooltip(st.tr(
			"自动续跑轮次已用尽：先在「编辑目标」中调高上限",
			"The automatic continuation budget is exhausted; raise the cap in Edit goal first"))
	}
	if button.Clicked() {
		resumeGoalAction(st, runtime)
	}
}

// buildGoalDockMenuButton is the goal strip's ⋯ entry (24 DIP More icon).
func buildGoalDockMenuButton(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	label := st.tr("目标操作", "Goal actions")
	uikit.IconButton(c, uikit.IconButtonOptions{
		Key:      "session.goal.menu",
		Label:    label,
		Tooltip:  label,
		Icon:     icons.More(),
		Size:     t.Scale(design.IconSizeS),
		Variant:  uikit.IconButtonNative,
		InkColor: t.LabelTertiary,
	}).PopoverMenuButton(func(m *ui.Menu) { buildGoalMenu(m, t, runtime, st) })
}

// buildGoalMenu fills the goal strip's menu: pause/resume and complete first,
// then edit, then the destructive clear in the danger color, separated. The
// entries reflect the current phase: a complete goal only offers edit/clear.
func buildGoalMenu(m *ui.Menu, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	_ = t
	if st == nil || st.goal == nil {
		return
	}
	goal := *st.goal
	if goal.Phase == contracts.GoalPhaseActive {
		if m.Item(st.tr("暂停目标", "Pause goal")).ID("goal.pause").Chosen() {
			pauseGoalAction(st, runtime)
		}
	}
	if goalResumeVisible(goal) {
		item := m.Item(st.tr("继续目标", "Continue goal")).ID("goal.resume")
		if goalResumeBlockedByCap(goal) {
			item.Disabled(true).Trailing(st.tr("轮次已用尽", "round limit reached"))
		} else if item.Chosen() {
			resumeGoalAction(st, runtime)
		}
	}
	if goal.Phase != contracts.GoalPhaseComplete {
		if m.Item(st.tr("完成目标", "Complete goal")).ID("goal.complete").Chosen() {
			completeGoalAction(st, runtime)
		}
	}
	m.Separator()
	if m.Item(st.tr("编辑目标", "Edit goal")).ID("goal.edit").Chosen() {
		openGoalDialog(st, runtime)
	}
	if m.Item(st.tr("清除目标记录", "Delete goal record")).ID("goal.clear").Danger().Chosen() {
		st.goalConfirm = "clear"
		runtime.Invalidate()
	}
}

// buildGoalMenuActions renders the destructive clear through the shared
// ConfirmDanger dialog: the safe action (取消) is the Default so a stray Enter
// cancels, and 确认清除 never is (DESIGN.md AP-12).
func buildGoalMenuActions(c *ui.Context, runtime contracts.UIRuntime, st *state) {
	if st == nil || st.goalConfirm != "clear" {
		return
	}
	open := true
	uikit.ConfirmDanger(c, &open, uikit.ConfirmDangerOptions{
		Key:   "session.goal.confirm.clear",
		Title: st.tr("清除目标", "Clear goal"),
		Message: st.tr("清除当前目标及其记录？自动续跑会停止，记录会被移除且无法撤销。",
			"Clear the current goal and its record? Automatic continuation stops, the record is removed, and this cannot be undone."),
		Confirm: st.tr("确认清除", "Clear"),
		Cancel:  st.tr("取消", "Cancel"),
		OnConfirm: func() {
			clearGoalAction(st, runtime)
		},
	})
	if !open {
		st.goalConfirm = ""
	}
}

// buildGoalRoundNotice renders a user message the engine's goal driver injected
// as an automatic continuation round: it is not a human prompt, so it never
// wears the user bubble or the user message actions. The badge names it 目标续跑
// and the round budget is visible; the generated prompt stays a quiet one-line
// preview with the full text in the tooltip.
func buildGoalRoundNotice(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	round := message.GoalRound
	if round == nil {
		return
	}
	ui.Row(c.Key("session.message." + message.ID)).FillWidth().
		AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Icon(c, icons.Activity()).Size(design.Space4, design.Space4).
			TextColor(t.LabelTertiary)
		design.Apply(ui.Badge(c, st.tr("目标续跑", "Goal round")), t.Badge()).
			TextColor(t.LabelSecondary)
		rounds := ""
		if round.MaxGoalRounds > 0 {
			rounds = fmt.Sprintf(st.tr("第 %d/%d 轮", "Round %d/%d"), round.Round, round.MaxGoalRounds)
		} else if round.Round > 0 {
			rounds = fmt.Sprintf(st.tr("第 %d 轮", "Round %d"), round.Round)
		}
		if rounds != "" {
			design.Apply(ui.Text(c, rounds), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap().Shrink(0)
		}
		if summary := noticeSummary(message.Text); summary != "" {
			design.Apply(ui.Text(c, summary), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Selectable().MaxLines(1).
				Ellipsis("…").Tooltip(message.Text)
		}
	})
}
