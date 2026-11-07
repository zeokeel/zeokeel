package session

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
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
	st.memNotice = ""
	runtime.Invalidate()
}

// buildGoalDialog renders the /goal modal: the goal input, the save/clear
// actions and the error line.
func buildGoalDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.goalOpen || st.goalTracker == nil {
		return
	}
	ui.Modal(c, &st.goalOpen, func() {
		design.Apply(ui.Column(c).Width(480).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("设置目标", "Goal")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"为当前会话设置长期目标；工具运行时会持续参考它。",
				"Set a long-term goal for this conversation; tool runs keep referring to it.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextInput(c, &st.goalText).Label(st.tr("目标内容", "Goal text")).FillWidth()
			if st.goalErr != "" {
				buildDialogError(c, t, st.goalErr)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				save := design.Apply(ui.PrimaryButton(c, st.tr("保存", "Save")), t.PrimaryButton()).
					Disabled(strings.TrimSpace(st.goalText) == "")
				if save.Clicked() {
					if _, err := st.goalTracker.Set(context.Background(), strings.TrimSpace(st.goalText)); err != nil {
						st.goalErr = st.tr("保存目标失败：", "Save goal failed: ") + err.Error()
					} else {
						st.goalOpen = false
						st.goalErr = ""
						st.memNotice = ""
					}
					runtime.Invalidate()
				}
				if ui.Button(c, st.tr("清除目标", "Clear goal")).Clicked() {
					st.goalTracker.Clear()
					st.goalOpen = false
					st.goalErr = ""
					runtime.Invalidate()
				}
				if ui.Button(c, st.tr("取消", "Cancel")).Clicked() {
					st.goalOpen = false
					st.goalErr = ""
					runtime.Invalidate()
				}
			})
		})
	})
}

// buildPlanDialog renders the /plan modal: the plan text area and the
// submit/cancel actions.
func buildPlanDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.planOpen || st.planReviewer == nil {
		return
	}
	ui.Modal(c, &st.planOpen, func() {
		design.Apply(ui.Column(c).Width(560).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
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
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				submit := design.Apply(ui.PrimaryButton(c, st.tr("提交审阅", "Submit review")),
					t.PrimaryButton()).Disabled(strings.TrimSpace(st.planDraft) == "")
				if submit.Clicked() {
					if _, err := st.planReviewer.Review(context.Background(), strings.TrimSpace(st.planDraft)); err != nil {
						st.planErr = st.tr("提交计划失败：", "Submit plan failed: ") + err.Error()
					} else {
						st.planOpen = false
						st.planErr = ""
					}
					runtime.Invalidate()
				}
				if ui.Button(c, st.tr("取消", "Cancel")).Clicked() {
					st.planOpen = false
					st.planErr = ""
					runtime.Invalidate()
				}
			})
		})
	})
}

// buildMemoryDialog renders the /memory modal: the memory input and the
// add/cancel actions; a successful add closes it and shows the 已添加 badge.
func buildMemoryDialog(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if !st.memOpen || st.memoryStore == nil {
		return
	}
	ui.Modal(c, &st.memOpen, func() {
		design.Apply(ui.Column(c).Width(480).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("添加记忆", "Add memory")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"保存一条全局记忆，后续会话都可以使用。",
				"Save a global memory that later conversations can use.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			ui.TextInput(c, &st.memDraft).Label(st.tr("记忆内容", "Memory text")).FillWidth()
			if st.memErr != "" {
				buildDialogError(c, t, st.memErr)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				add := design.Apply(ui.PrimaryButton(c, st.tr("添加", "Add")), t.PrimaryButton()).
					Disabled(strings.TrimSpace(st.memDraft) == "")
				if add.Clicked() {
					_, err := st.memoryStore.Add(context.Background(), contracts.Memory{
						Text:  strings.TrimSpace(st.memDraft),
						Scope: "global",
					})
					if err != nil {
						st.memErr = st.tr("添加记忆失败：", "Add memory failed: ") + err.Error()
					} else {
						st.memOpen = false
						st.memErr = ""
						st.memDraft = ""
						st.memNotice = st.tr("已添加", "Added")
					}
					runtime.Invalidate()
				}
				if ui.Button(c, st.tr("取消", "Cancel")).Clicked() {
					st.memOpen = false
					st.memErr = ""
					runtime.Invalidate()
				}
			})
		})
	})
}

// buildDialogError is the danger row shared by the command dialogs.
func buildDialogError(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText)
}
