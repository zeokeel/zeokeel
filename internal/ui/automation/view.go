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
		buildAutomationEmpty(c, t, icons.Clock(), st.tr("自动化后端未启用", "Automation backend not enabled"))
		return
	}

	ui.Column(c).FillWidth().Grow(1).Gap(design.Space4).Children(func() {
		// Page header: title + description on the left, the primary New action
		// on the right.
		ui.Row(c).FillWidth().AlignItems(ui.End).Gap(design.Space3).Children(func() {
			ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
				design.Apply(ui.Text(c, st.localized(cfg.Title, "自动化", "Automation")), t.Font(design.TypeL)).
					FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				design.Apply(ui.Text(c, st.tr(
					"按计划自动运行会话任务；可以启停、立即运行或删除。",
					"Runs session tasks on a schedule; start, stop, run now or delete.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
			})
			if create := design.Apply(ui.PrimaryButton(c, st.localized(cfg.NewLabel, "新建任务", "New task")), t.PrimaryButton()); create.Clicked() {
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
			buildAutomationError(c, t, st.tr("读取任务失败：", "Failed to read tasks: ")+err.Error())
			return
		}
		if len(tasks) == 0 {
			buildAutomationEmpty(c, t, icons.Clock(), st.localized(cfg.EmptyText, "还没有自动化任务", "No automation tasks yet"))
			design.Apply(ui.Text(c, st.tr(
				"创建后按计划自动运行；也可以随时立即运行。",
				"Tasks run on their schedule after creation; you can also run one now.")),
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
			design.Apply(ui.Text(c, firstNonEmpty(task.Name, st.tr("(未命名任务)", "(Untitled task)"))), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			enabled := task.Enabled
			taskName := firstNonEmpty(task.Name, st.tr("任务", "task"))
			toggle := ui.Toggle(c, &enabled, st.tr(taskName+" 启用", taskName+" enabled"))
			if toggle.Changed() {
				updated := task
				updated.Enabled = enabled
				if _, err := service.Update(context.Background(), updated); err != nil {
					st.pageErr = st.tr("更新任务失败：", "Failed to update task: ") + err.Error()
				} else {
					st.pageErr = ""
				}
				runtime.Invalidate()
			}
		})
		design.Apply(ui.Text(c, task.Schedule), t.MonoFamily()).FontSize(11).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		ui.Row(c).FillWidth().Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, st.tr("下次运行：", "Next run: ")+formatTaskTime(task.NextRun)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, st.tr("上次运行：", "Last run: ")+formatTaskTime(task.LastRun)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
		})
		if strings.TrimSpace(task.LastError) != "" {
			buildAutomationError(c, t, st.tr("上次错误：", "Last error: ")+task.LastError)
		}
		buildTaskHistory(c, t, st, runtime, task)
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			runNow := ui.Button(c, st.tr("立即运行", "Run now")).Tooltip(st.tr("立即执行一次该任务", "Run this task once now"))
			if runNow.Clicked() {
				if err := service.RunNow(context.Background(), task.ID); err != nil {
					st.pageErr = st.tr("运行任务失败：", "Failed to run task: ") + err.Error()
				} else {
					st.pageErr = ""
				}
				runtime.Invalidate()
			}
			remove := ui.Button(c, st.tr("删除", "Delete")).Tooltip(st.tr("删除该任务", "Delete this task")).TextColor(t.DangerText)
			if remove.Clicked() {
				st.deleteOpen = true
				st.deleteTarget = task.ID
				st.deleteName = firstNonEmpty(task.Name, st.tr("未命名任务", "Untitled task"))
				runtime.Invalidate()
			}
		})
	})
}

// runStatusLabel renders one run status label.
func runStatusLabel(st *state, status string) string {
	if status == contracts.AutomationRunFailure {
		return st.tr("失败", "Failed")
	}
	return st.tr("成功", "Success")
}

// runStatusColor maps one run status to its semantic color.
func runStatusColor(t *design.Tokens, status string) ui.Color {
	if status == contracts.AutomationRunFailure {
		return t.DangerText
	}
	return t.Success
}

// buildTaskHistory is the task card's run-history disclosure: the latest run
// summary when collapsed and the recent runs (newest first) when expanded.
func buildTaskHistory(c *ui.Context, t *design.Tokens, st *state, runtime contracts.UIRuntime, task contracts.AutomationTask) {
	open := st.historyOpen[task.ID]
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if len(task.RunHistory) > 0 {
			latest := task.RunHistory[0]
			design.Apply(ui.Badge(c, runStatusLabel(st, latest.Status)), t.Badge()).
				TextColor(runStatusColor(t, latest.Status))
			design.Apply(ui.Text(c, firstNonEmpty(latest.Summary, "—")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
		} else {
			ui.Spacer(c).Grow(1)
		}
		if ui.Button(c, st.tr("历史", "History")).Tooltip(st.tr("查看运行历史", "Show run history")).Clicked() {
			st.historyOpen[task.ID] = !open
			runtime.Invalidate()
		}
	})
	if !open {
		return
	}
	ui.Divider(c)
	if len(task.RunHistory) == 0 {
		design.Apply(ui.Text(c, st.tr("还没有运行记录", "No runs yet")), t.TextMuted(design.TypeXXS))
		return
	}
	for _, record := range task.RunHistory {
		record := record
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, record.StartedAt.Local().Format("15:04:05")), t.MonoFamily()).
				FontSize(10).TextColor(t.LabelTertiary).NoWrap()
			design.Apply(ui.Badge(c, runStatusLabel(st, record.Status)), t.Badge()).
				TextColor(runStatusColor(t, record.Status))
			design.Apply(ui.Text(c, firstNonEmpty(record.Summary, "—")), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
		})
	}
}

// buildCreateForm is the inline task creation card.
func buildCreateForm(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state) {
	name := strings.TrimSpace(st.formName)
	prompt := strings.TrimSpace(st.formPrompt)
	schedule := strings.TrimSpace(st.formSchedule)
	valid := name != "" && prompt != "" && schedule != ""

	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, st.tr("新建自动化任务", "New automation task")), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		ui.TextInput(c, &st.formName).Label(st.tr("任务名称", "Task name")).
			Placeholder(st.tr("例如：每日依赖巡检", "For example: daily dependency check")).FillWidth()
		ui.TextArea(c, &st.formPrompt).Label(st.tr("任务提示词", "Task prompt")).
			Placeholder(st.tr("任务触发时发给 agent 的提示词", "Prompt sent to the agent when the task runs")).FillWidth().MinHeight(design.Space6 * 4)
		ui.TextInput(c, &st.formSchedule).Label(st.tr("运行计划", "Schedule")).
			Placeholder("*/5 * * * * 或 every 60s").FillWidth()
		design.Apply(ui.Text(c, st.tr(
			"支持 cron 子集（分 时 日 月 周）与 every <duration>（如 every 60s）。",
			"Supports a cron subset (minute hour day month weekday) and every <duration> (e.g. every 60s).")),
			t.TextMuted(design.TypeXXXS)).MaxLines(2)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Toggle(c, &st.formEnabled, st.tr("启用", "Enabled"))
			ui.Spacer(c).Grow(1)
			cancel := ui.Button(c, st.tr("取消", "Cancel"))
			if cancel.Clicked() {
				st.formOpen = false
				st.formErr = ""
				runtime.Invalidate()
			}
			save := design.Apply(ui.PrimaryButton(c, st.tr("保存", "Save")), t.PrimaryButton()).Disabled(!valid)
			if save.Clicked() && valid {
				_, err := service.Create(context.Background(), contracts.AutomationTask{
					Name:     name,
					Prompt:   prompt,
					Schedule: schedule,
					Enabled:  st.formEnabled,
				})
				if err != nil {
					st.formErr = st.tr("创建任务失败：", "Failed to create task: ") + err.Error()
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
	message := st.tr(
		fmt.Sprintf("“%s”将被删除，此操作无法撤销。", firstNonEmpty(st.deleteName, st.tr("未命名任务", "Untitled task"))),
		fmt.Sprintf("%q will be deleted. This cannot be undone.", firstNonEmpty(st.deleteName, st.tr("未命名任务", "Untitled task"))))
	switch ui.AlertDialog(c, &st.deleteOpen, st.tr("删除自动化任务", "Delete automation task"),
		message, st.tr("取消", "Cancel"), st.tr("确认删除", "Delete")) {
	case 1:
		if err := service.Delete(context.Background(), st.deleteTarget); err != nil {
			st.pageErr = st.tr("删除任务失败：", "Failed to delete task: ") + err.Error()
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
