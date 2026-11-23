package session

import (
	"context"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// openGoalDialog opens the /goal modal, prefilled with the active session's
// current goal. Editing keeps the goal id/revision (the compare-and-set pair)
// and exposes its round cap; creating is always scoped to the active session.
func openGoalDialog(st *state, runtime uinative.UIRuntime) {
	if st == nil || st.goalService == nil || st.goalSessionID == "" {
		return
	}
	st.goalText = ""
	st.goalCapText = ""
	if st.goal != nil {
		st.goalText = st.goal.Objective
		if st.goal.MaxGoalRounds > 0 {
			st.goalCapText = strconv.Itoa(st.goal.MaxGoalRounds)
		}
	}
	st.goalOpen = true
	st.goalErr = ""
	runtime.Invalidate()
}

// openPlanDialog opens the /plan modal with an empty draft.
func openPlanDialog(st *state, runtime uinative.UIRuntime) {
	if st.planReviewer == nil {
		return
	}
	st.planOpen = true
	st.planDraft = ""
	st.planErr = ""
	runtime.Invalidate()
}

// buildGoalDialog renders the /goal modal: the objective input, the round-cap
// editor while editing an existing goal, and the footer actions. Saving is the
// primary action (rightmost, Enter); clearing is destructive and goes through
// ConfirmDanger instead of deleting at once.
func buildGoalDialog(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state) {
	if !st.goalOpen || st.goalService == nil {
		return
	}
	// A complete goal is terminal for editing-by-update: saving the dialog
	// creates the next goal instance instead (the service replaces a complete
	// goal with a new armed one).
	editing := st.goal != nil && st.goal.Phase != contracts.GoalPhaseComplete
	save := func() {
		objective := strings.TrimSpace(st.goalText)
		sessionID := st.goalSessionID
		if objective == "" || sessionID == "" {
			return
		}
		cap := 0
		if editing {
			if text := strings.TrimSpace(st.goalCapText); text != "" {
				parsed, err := strconv.Atoi(text)
				if err != nil || parsed <= 0 {
					st.goalErr = st.tr("自动续跑上限必须是正整数。",
						"The automatic continuation cap must be a positive integer.")
					runtime.Invalidate()
					return
				}
				cap = parsed
			}
		}
		var (
			next contracts.Goal
			err  error
		)
		if editing {
			next, err = st.goalService.UpdateGoal(context.Background(), sessionID,
				contracts.GoalRef{ID: st.goal.ID, Revision: st.goal.Revision},
				contracts.GoalUpdate{Action: contracts.GoalActionEdit, Objective: objective, MaxGoalRounds: cap})
		} else {
			next, err = st.goalService.CreateGoal(context.Background(), sessionID, objective, cap)
		}
		if err != nil {
			st.goalErr = st.tr("保存目标失败：", "Save goal failed: ") + st.goalErrorText(err)
			st.refreshGoalSnapshot()
			runtime.Invalidate()
			return
		}
		st.setGoalSnapshot(next)
		st.goalOpen = false
		st.goalErr = ""
		st.toast(contracts.NotifySuccess, st.tr("目标已保存", "Goal saved"))
		runtime.Invalidate()
	}
	ui.Modal(c, &st.goalOpen, func() {
		dialogEnter(c, save)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("设置目标", "Goal")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"为当前会话设置长期目标；引擎会按轮次自动推进，直到完成、受阻或你暂停。",
				"Set a long-term goal for this conversation; the engine advances it round by round until it is done, blocked, or you pause it.")),
				t.TextMuted(design.TypeXS)).MaxLines(3)
			ui.TextInput(c, &st.goalText).Label(st.tr("目标内容", "Goal text")).FillWidth().
				OnTextKey(enterTo(save))
			if editing {
				design.Apply(ui.Text(c, st.tr(
					"自动续跑上限（轮）：留空保持当前上限。",
					"Round cap: leave empty to keep the current cap.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
				ui.TextInput(c, &st.goalCapText).Label(st.tr("自动续跑上限（轮）", "Round cap")).
					Placeholder(st.tr("留空保持当前上限", "Leave empty to keep the current cap")).
					FillWidth().OnTextKey(enterTo(save))
				design.Apply(ui.Text(c, st.tr(
					"轮次用尽时目标会进入「受阻」；调高上限后再继续即可。",
					"An exhausted budget blocks the goal; raise the cap and continue it.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
			}
			if st.goalErr != "" {
				buildDialogError(c, t, st.goalErr)
			}
			actions := []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					st.goalOpen = false
					st.goalErr = ""
					runtime.Invalidate()
				}},
			}
			if st.goal != nil {
				actions = append(actions, uikit.DialogAction{
					Label: st.tr("删除目标记录", "Delete goal record"), Danger: true,
					OnClick: func() {
						st.goalOpen = false
						st.goalConfirm = "clear"
						runtime.Invalidate()
					},
				})
			}
			actions = append(actions, uikit.DialogAction{
				Label: st.tr("保存", "Save"), Primary: true,
				Disabled: strings.TrimSpace(st.goalText) == "",
				OnClick:  save,
			})
			ui.Divider(c)
			uikit.DialogFooter(c, actions)
		})
	})
}

// buildPlanDialog renders the /plan modal: the plan text area and the
// cancel/submit footer.
func buildPlanDialog(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state) {
	if !st.planOpen || st.planReviewer == nil {
		return
	}
	submit := func() {
		if strings.TrimSpace(st.planDraft) == "" {
			return
		}
		if _, err := st.planReviewer.Review(context.Background(), strings.TrimSpace(st.planDraft)); err != nil {
			st.planErr = st.tr("提交计划失败：", "Submit plan failed: ") + err.Error()
		} else {
			st.planOpen = false
			st.planErr = ""
			st.toast(contracts.NotifySuccess, st.tr("计划已提交审阅", "Plan submitted for review"))
		}
		runtime.Invalidate()
	}
	ui.Modal(c, &st.planOpen, func() {
		dialogEnter(c, submit)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("提交计划", "Submit plan")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"计划会交给用户审阅；批准后再执行。",
				"The plan is sent to the user for review; execution starts after approval.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextArea(c, &st.planDraft).Label(st.tr("计划内容", "Plan content")).
				FillWidth().MinHeight(design.Space6 * 3)
			if st.planErr != "" {
				buildDialogError(c, t, st.planErr)
			}
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					st.planOpen = false
					st.planErr = ""
					runtime.Invalidate()
				}},
				{Label: st.tr("提交审阅", "Submit review"), Primary: true,
					Disabled: strings.TrimSpace(st.planDraft) == "",
					OnClick:  submit},
			})
		})
	})
}

// buildDialogError is the danger row shared by the command dialogs.
func buildDialogError(c *ui.Context, t *design.Tokens, text string) {
	_ = t
	uikit.ErrorLine(c, text)
}

// dialogEnter runs a dialog's primary action on Enter. Inside a modal the
// shortcut lands on the overlay scope, so the key reaches it wherever the focus
// is; a focused button clicks natively first, and a text input takes Enter
// through OnTextKey (see enterTo), which calls the same action.
func dialogEnter(c *ui.Context, action func()) {
	if action == nil {
		return
	}
	c.OnShortcut(0, ui.KeyEnter, action)
}

// enterTo adapts a primary action to a single-line text input's OnTextKey: the
// focused input takes Enter first, so the dialog's default action runs from
// there.
func enterTo(action func()) func(ui.TextKey) bool {
	return func(k ui.TextKey) bool {
		if k.Key != ui.KeyEnter || k.Mods != 0 {
			return false
		}
		action()
		return true
	}
}
