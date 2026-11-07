package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// buildScreen renders one frame of the Automation screen: the page header, the
// inline creation form, the scheduled-task cards and the delete confirmation.
// Service calls happen in click handlers; Build only reads the list.
func buildScreen(c *ui.Context, runtime contracts.UIRuntime, navigator contracts.Navigator, cfg Config, st *state) {
	t := design.Of(c)
	service, hasService := st.ctx.Get(contracts.KeyAutomation)
	if !hasService || service == nil {
		buildAutomationEmpty(c, t, icons.Clock(), "自动化后端未启用")
		return
	}

	ui.Column(c).FillWidth().Grow(1).Gap(design.Space4).Children(func() {
		// Page header: title + description on the left, the primary New action
		// on the right.
		ui.Row(c).FillWidth().AlignItems(ui.End).Gap(design.Space3).Children(func() {
			ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
				design.Apply(ui.Text(c, cfg.Title), t.Font(design.TypeL)).
					FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				design.Apply(ui.Text(c,
					"按计划自动运行会话任务；可以启停、立即运行或删除。"),
					t.TextMuted(design.TypeXS)).MaxLines(2)
			})
			if create := design.Apply(ui.PrimaryButton(c, cfg.NewLabel), t.PrimaryButton()); create.Clicked() {
				st.formOpen = !st.formOpen
				st.formErr = ""
				runtime.Invalidate()
			}
		})

		if st.pageErr != "" {
			buildAutomationError(c, t, st.pageErr)
		}
		if st.formOpen {
			buildCreateForm(c, t, service, runtime, st)
		}

		tasks, err := service.List(context.Background())
		if err != nil {
			buildAutomationError(c, t, "读取任务失败："+err.Error())
			return
		}
		if len(tasks) == 0 {
			buildAutomationEmpty(c, t, icons.Clock(), cfg.EmptyText)
			design.Apply(ui.Text(c, "创建后按计划自动运行；也可以随时立即运行。"),
				t.TextMuted(design.TypeXXS))
		} else {
			ui.Scroll(c).FillWidth().Grow(1).Children(func() {
				ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
					for _, task := range tasks {
						buildTaskCard(c, t, service, runtime, st, task)
					}
				})
			})
		}
	})

	buildDeleteDialog(c, t, service, runtime, st)
}

// buildTaskCard is one scheduled task: name, native enable toggle, schedule,
// next/last run metadata, the last error and Run now / Delete actions.
func buildTaskCard(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state, task contracts.AutomationTask) {
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(task.Name, "(未命名任务)")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			enabled := task.Enabled
			toggle := ui.Toggle(c, &enabled, firstNonEmpty(task.Name, "任务")+" 启用")
			if toggle.Changed() {
				updated := task
				updated.Enabled = enabled
				if _, err := service.Update(context.Background(), updated); err != nil {
					st.pageErr = "更新任务失败：" + err.Error()
				} else {
					st.pageErr = ""
				}
				runtime.Invalidate()
			}
		})
		design.Apply(ui.Text(c, task.Schedule), t.MonoFamily()).FontSize(11).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		ui.Row(c).FillWidth().Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, "下次运行："+formatTaskTime(task.NextRun)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, "上次运行："+formatTaskTime(task.LastRun)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
		})
		if strings.TrimSpace(task.LastError) != "" {
			buildAutomationError(c, t, "上次错误："+task.LastError)
		}
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			runNow := ui.Button(c, "立即运行").Tooltip("立即执行一次该任务")
			if runNow.Clicked() {
				if err := service.RunNow(context.Background(), task.ID); err != nil {
					st.pageErr = "运行任务失败：" + err.Error()
				} else {
					st.pageErr = ""
				}
				runtime.Invalidate()
			}
			remove := ui.Button(c, "删除").Tooltip("删除该任务").TextColor(t.DangerText)
			if remove.Clicked() {
				st.deleteOpen = true
				st.deleteTarget = task.ID
				st.deleteName = firstNonEmpty(task.Name, "未命名任务")
				runtime.Invalidate()
			}
		})
	})
}

// buildCreateForm is the inline task creation card.
func buildCreateForm(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state) {
	name := strings.TrimSpace(st.formName)
	prompt := strings.TrimSpace(st.formPrompt)
	schedule := strings.TrimSpace(st.formSchedule)
	valid := name != "" && prompt != "" && schedule != ""

	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, "新建自动化任务"), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		ui.TextInput(c, &st.formName).Label("任务名称").
			Placeholder("例如：每日依赖巡检").FillWidth()
		ui.TextArea(c, &st.formPrompt).Label("任务提示词").
			Placeholder("任务触发时发给 agent 的提示词").FillWidth().MinHeight(design.Space6 * 4)
		ui.TextInput(c, &st.formSchedule).Label("运行计划").
			Placeholder("*/5 * * * * 或 every 60s").FillWidth()
		design.Apply(ui.Text(c,
			"支持 cron 子集（分 时 日 月 周）与 every <duration>（如 every 60s）。"),
			t.TextMuted(design.TypeXXXS)).MaxLines(2)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Toggle(c, &st.formEnabled, "启用")
			ui.Spacer(c).Grow(1)
			cancel := ui.Button(c, "取消")
			if cancel.Clicked() {
				st.formOpen = false
				st.formErr = ""
				runtime.Invalidate()
			}
			save := design.Apply(ui.PrimaryButton(c, "保存"), t.PrimaryButton()).Disabled(!valid)
			if save.Clicked() && valid {
				_, err := service.Create(context.Background(), contracts.AutomationTask{
					Name:     name,
					Prompt:   prompt,
					Schedule: schedule,
					Enabled:  st.formEnabled,
				})
				if err != nil {
					st.formErr = "创建任务失败：" + err.Error()
				} else {
					st.formOpen = false
					st.formName = ""
					st.formPrompt = ""
					st.formSchedule = ""
					st.formEnabled = true
					st.formErr = ""
				}
				runtime.Invalidate()
			}
		})
		if st.formErr != "" {
			buildAutomationError(c, t, st.formErr)
		}
	})
}

// buildDeleteDialog is the native delete confirmation for one task.
func buildDeleteDialog(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state) {
	if !st.deleteOpen {
		return
	}
	message := fmt.Sprintf("“%s”将被删除，此操作无法撤销。", firstNonEmpty(st.deleteName, "未命名任务"))
	switch ui.AlertDialog(c, &st.deleteOpen, "删除自动化任务", message, "取消", "确认删除") {
	case 1:
		if err := service.Delete(context.Background(), st.deleteTarget); err != nil {
			st.pageErr = "删除任务失败：" + err.Error()
		} else {
			st.pageErr = ""
		}
		st.deleteTarget = ""
		st.deleteName = ""
		runtime.Invalidate()
	}
}

// buildAutomationEmpty is the centered empty state.
func buildAutomationEmpty(c *ui.Context, t *design.Tokens, icon *ui.SVG, text string) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space8).Children(func() {
		ui.Row(c).Size(th.Space(10), th.Space(10)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icon).Size(th.Space(5), th.Space(5)).TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, text), t.Font(design.TypeM)).
			FontWeight(600).TextColor(t.LabelPrimary)
	})
}

// buildAutomationError is the screen's danger row.
func buildAutomationError(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText)
	})
}

// formatTaskTime renders a task timestamp, "—" when unset.
func formatTaskTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04")
}
