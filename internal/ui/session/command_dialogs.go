package session

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// openGoalDialog opens the /goal modal, prefilled with the active goal.
func openGoalDialog(st *state, runtime contracts.UIRuntime) {
	if st.goalTracker == nil {
		return
	}
	st.goalText = ""
	if _, text, _, ok := st.goalTracker.Get(); ok {
		st.goalText = text
	}
	st.goalOpen = true
	st.goalErr = ""
	runtime.Invalidate()
}

// openPlanDialog opens the /plan modal with an empty draft.
func openPlanDialog(st *state, runtime contracts.UIRuntime) {
	if st.planReviewer == nil {
		return
	}
	st.planOpen = true
	st.planDraft = ""
	st.planErr = ""
	runtime.Invalidate()
}

// openMemoryDialog opens the /memory modal with an empty draft.
func openMemoryDialog(st *state, runtime contracts.UIRuntime) {
	if st.memoryStore == nil {
		return
	}
	st.memOpen = true
	st.memDraft = ""
	st.memErr = ""
	runtime.Invalidate()
}

// buildGoalDialog renders the /goal modal: the goal input and the footer
// actions. Saving is the primary action (rightmost, Enter); clearing is the
// dangerous one and goes through the header's AlertDialog confirmation instead
// of clearing at once.
func buildGoalDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.goalOpen || st.goalTracker == nil {
		return
	}
	save := func() {
		if strings.TrimSpace(st.goalText) == "" {
			return
		}
		// Editing an existing goal keeps its ID (and therefore the
		// auto-continue budget); only a fresh goal gets a new identity through
		// Set.
		_, _, _, hasGoal := st.goalTracker.Get()
		var err error
		if lifecycler, ok := st.goalTracker.(contracts.GoalLifecycler); ok && hasGoal {
			_, err = lifecycler.Edit(strings.TrimSpace(st.goalText))
		} else {
			_, err = st.goalTracker.Set(context.Background(), strings.TrimSpace(st.goalText))
		}
		if err != nil {
			st.goalErr = st.tr("保存目标失败：", "Save goal failed: ") + err.Error()
		} else {
			st.goalOpen = false
			st.goalErr = ""
			st.toast(contracts.NotifySuccess, st.tr("目标已保存", "Goal saved"))
		}
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
				"为当前会话设置长期目标；工具运行时会持续参考它。",
				"Set a long-term goal for this conversation; tool runs keep referring to it.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextInput(c, &st.goalText).Label(st.tr("目标内容", "Goal text")).FillWidth().
				OnTextKey(enterTo(save))
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
			if _, _, _, hasGoal := st.goalTracker.Get(); hasGoal {
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
func buildPlanDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
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

// buildMemoryDialog renders the /memory modal: the memory input and the
// cancel/add footer. A successful add is a transient toast.
func buildMemoryDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.memOpen || st.memoryStore == nil {
		return
	}
	add := func() {
		text := strings.TrimSpace(st.memDraft)
		if text == "" {
			return
		}
		if _, err := st.memoryStore.Add(context.Background(), contracts.Memory{
			Text:  text,
			Scope: "global",
		}); err != nil {
			st.memErr = st.tr("添加记忆失败：", "Add memory failed: ") + err.Error()
		} else {
			st.memOpen = false
			st.memErr = ""
			st.memDraft = ""
			st.toast(contracts.NotifySuccess, st.tr("已添加记忆", "Memory added"))
		}
		runtime.Invalidate()
	}
	ui.Modal(c, &st.memOpen, func() {
		dialogEnter(c, add)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("添加记忆", "Add memory")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"保存一条全局记忆，后续会话都可以使用。",
				"Save a global memory that later conversations can use.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextInput(c, &st.memDraft).Label(st.tr("记忆内容", "Memory text")).FillWidth().
				OnTextKey(enterTo(add))
			if st.memErr != "" {
				buildDialogError(c, t, st.memErr)
			}
			ui.Divider(c)
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					st.memOpen = false
					st.memErr = ""
					runtime.Invalidate()
				}},
				{Label: st.tr("添加", "Add"), Primary: true,
					Disabled: strings.TrimSpace(st.memDraft) == "",
					OnClick:  add},
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
