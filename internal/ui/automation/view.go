package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildScreen renders one frame of the Automation screen: the page header, the
// inline creation form, the scheduled-task cards and the delete confirmation.
// Service calls happen in click handlers; Build only reads the list.
func buildScreen(c *ui.Context, runtime contracts.UIRuntime, navigator contracts.Navigator, cfg Config, st *state) {
	t := design.Of(c)
	service, hasService := st.ctx.Get(contracts.KeyAutomation)
	if !hasService || service == nil {
		uikit.EmptyStateBlock(c, st.tr("自动化后端未启用", "Automation backend not enabled"), "", nil)
		return
	}

	// Esc cancels the inline form, as every other form/dialog in the product:
	// without it the only way out of a mistaken "New task" was the Cancel button
	// far below the fields.
	c.OnShortcut(0, ui.KeyEscape, func() {
		if !st.formOpen {
			return
		}
		st.closeForm()
		runtime.Invalidate()
	})

	ui.Column(c).FillWidth().Grow(1).Gap(design.Space4).Children(func() {
		// Page header: title + description on the left, the primary New action
		// on the right (the one accent action of the page).
		ui.Row(c).FillWidth().AlignItems(ui.End).Gap(design.Space3).Children(func() {
			ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
				design.Apply(ui.Text(c, st.localized(cfg.Title, "自动化", "Automation")), t.Font(design.TypeL)).
					FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				design.Apply(ui.Text(c, st.tr(
					"按计划自动运行会话任务；可以启停、立即运行、编辑或删除。",
					"Runs session tasks on a schedule; start, stop, run now, edit or delete.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
			})
			if create := design.Apply(ui.PrimaryButton(c, st.localized(cfg.NewLabel, "新建任务", "New task")), t.PrimaryButton()); create.Clicked() {
				if st.formOpen {
					st.closeForm()
				} else {
					st.openCreateForm()
				}
				runtime.Invalidate()
			}
		})

		if st.pageErr != "" {
			uikit.ErrorLine(c, st.pageErr)
		}
		if st.formOpen {
			buildCreateForm(c, t, service, runtime, st)
		}

		tasks, err := service.List(context.Background())
		if err != nil {
			uikit.ErrorLine(c, automationErrorText(st,
				st.tr("读取任务失败：", "Failed to read tasks: "), err))
			return
		}
		buildTaskToolbar(c, t, runtime, st)
		filtered := filterAutomationTasks(tasks, st.query, st.taskFilter)
		if len(tasks) > 0 {
			design.Apply(ui.Text(c, automationTaskCountLabel(st, len(filtered))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
		switch {
		case len(tasks) == 0 && st.formOpen:
			// The form above is the way forward: a second "create" call to action
			// exactly when the user is already filling the form competed with its
			// Save button.
			design.Apply(ui.Text(c, st.tr("填写上方表单并保存即可创建任务。",
				"Fill the form above and save to create the task.")),
				t.TextMuted(design.TypeXS))
		case len(tasks) == 0:
			uikit.EmptyStateBlock(c,
				st.localized(cfg.EmptyText, "还没有自动化任务", "No automation tasks yet"),
				st.tr("创建后按计划自动运行；也可以随时立即运行。",
					"Tasks run on their schedule after creation; you can also run one now."),
				func() {
					first := design.Apply(ui.PrimaryButton(c, st.tr("创建第一个任务", "Create the first task")),
						t.PrimaryButton())
					if first.Clicked() {
						st.openCreateForm()
						runtime.Invalidate()
					}
				})
		case len(filtered) == 0 && st.formOpen:
			design.Apply(ui.Text(c, st.tr("没有匹配的任务。", "No matching tasks.")),
				t.TextMuted(design.TypeXS))
		case len(filtered) == 0:
			uikit.EmptyStateBlock(c, st.tr("没有匹配的任务", "No matching tasks"),
				st.tr("换个关键词或清空筛选试试。", "Try another search term or clear the filter."), nil)
		default:
			// The scheduled tasks are a collection: the official List virtualizes
			// and keys the cards by task ID instead of a Scroll plus a loop.
			st.taskList.Key = func(index int) any {
				if index < 0 || index >= len(filtered) {
					return nil
				}
				return filtered[index].ID
			}
			st.taskList.Label = func(index int) string {
				if index < 0 || index >= len(filtered) {
					return ""
				}
				return firstNonEmpty(filtered[index].Name, filtered[index].ID)
			}
			ui.List(c, &st.taskList, len(filtered)).
				Label(st.tr("自动化任务", "Automation tasks")).
				Gap(design.Space2).
				Grow(1).
				Rows(func(row ui.ListRow) {
					buildTaskCard(row.Context, t, service, runtime, st, filtered[row.Index])
				})
		}
	})

	buildDeleteDialog(c, t, service, runtime, st)
}

// Task filter values of the enable-state PopoverMenuButton (session-local).
const (
	taskFilterAll      = ""
	taskFilterEnabled  = "enabled"
	taskFilterDisabled = "disabled"
)

// automationTaskCountLabel renders the filtered result count.
func automationTaskCountLabel(st *state, count int) string {
	if st.english() {
		if count == 1 {
			return "1 task"
		}
		return fmt.Sprintf("%d tasks", count)
	}
	return fmt.Sprintf("%d 个任务", count)
}

// filterAutomationTasks keeps the tasks matching the case-insensitive search
// (name or prompt substring) and the enable-state filter.
func filterAutomationTasks(tasks []contracts.AutomationTask, query, filter string) []contracts.AutomationTask {
	needle := strings.ToLower(strings.TrimSpace(query))
	out := make([]contracts.AutomationTask, 0, len(tasks))
	for _, task := range tasks {
		switch filter {
		case taskFilterEnabled:
			if !task.Enabled {
				continue
			}
		case taskFilterDisabled:
			if task.Enabled {
				continue
			}
		}
		if needle != "" {
			name := strings.ToLower(task.Name)
			prompt := strings.ToLower(task.Prompt)
			if !strings.Contains(name, needle) && !strings.Contains(prompt, needle) {
				continue
			}
		}
		out = append(out, task)
	}
	return out
}

// buildTaskToolbar is the list toolbar: the search field and the enable-state
// filter menu (the filter is session-local state, not persisted).
func buildTaskToolbar(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.SearchField(c, &st.query).
			Label(st.tr("搜索任务", "Search tasks")).
			Placeholder(st.tr("搜索任务…", "Search tasks…")).Grow(1)
		ui.PopoverMenuButton(c, st.tr("筛选", "Filter"), func(m *ui.Menu) {
			choices := []struct{ value, zh, en string }{
				{taskFilterAll, "全部", "All"},
				{taskFilterEnabled, "仅启用", "Enabled only"},
				{taskFilterDisabled, "仅停用", "Disabled only"},
			}
			for _, choice := range choices {
				item := m.Item(st.tr(choice.zh, choice.en))
				item.Radio(st.taskFilter == choice.value)
				if item.Chosen() && st.taskFilter != choice.value {
					st.taskFilter = choice.value
					runtime.Invalidate()
				}
			}
		}).Tooltip(st.tr("按启用状态筛选任务", "Filter tasks by enabled state"))
	})
}

// buildTaskCard is one scheduled task: the name and its enable switch, the
// schedule, one muted metadata line, the run times, the last error and the run
// history disclosure, then Run now / Delete.
func buildTaskCard(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state, task contracts.AutomationTask) {
	c.Key("automation/" + task.ID)
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(task.Name, st.tr("(未命名任务)", "(Untitled task)"))), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			// The enable control is a bare native switch with its short caption:
			// a ui.Field belongs to a vertical form, not to this horizontal
			// header (the task name already names the row).
			enabled := task.Enabled
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space1).Children(func() {
				design.Apply(ui.Text(c, st.tr("启用", "Enabled")), t.TextMuted(design.TypeXXS))
				sw := ui.Switch(c, &enabled).Label(st.tr("启用任务", "Enable task"))
				if !sw.Changed() {
					return
				}
				updated := task
				updated.Enabled = enabled
				if _, err := service.Update(context.Background(), updated); err != nil {
					st.pageErr = automationErrorText(st,
						st.tr("更新任务失败：", "Failed to update task: "), err)
				} else {
					st.pageErr = ""
				}
				runtime.Invalidate()
			})
		})
		if human := humanSchedule(st, task.Schedule); human != "" {
			// A human schedule leads; the raw cron/interval stays available as a
			// tooltip for people who wrote it.
			design.Apply(ui.Text(c, human), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…").
				Tooltip(st.tr("原始计划：", "Raw schedule: ") + task.Schedule)
		} else {
			design.Apply(ui.Text(c, task.Schedule), t.MonoText(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		// Metadata is background information: one muted line, not three badges
		// stretched across the card.
		meta := make([]string, 0, 3)
		if model := strings.TrimSpace(task.Model); model != "" {
			meta = append(meta, st.tr("模型：", "Model: ")+model)
		}
		if workspaceID := strings.TrimSpace(task.WorkspaceID); workspaceID != "" {
			// The card names the workspace like the form's selector did; the raw
			// settings id is an implementation detail users cannot read.
			meta = append(meta, st.tr("工作区：", "Workspace: ")+st.workspaceDisplayName(workspaceID))
		}
		if permission := strings.TrimSpace(task.Permission); permission != "" {
			meta = append(meta, st.tr("权限：", "Permission: ")+automationPermissionLabel(st, permission))
		}
		if len(meta) > 0 {
			design.Apply(ui.Text(c, strings.Join(meta, " · ")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, st.tr("下次运行：", "Next run: ")+formatTaskTime(st, task.NextRun)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, st.tr("上次运行：", "Last run: ")+formatTaskTime(st, task.LastRun)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
		})
		if strings.TrimSpace(task.LastError) != "" {
			uikit.ErrorLine(c, st.tr("上次错误：", "Last error: ")+automationRawErrorText(st, task.LastError))
		}
		buildTaskHistory(c, t, st, task)
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			running := st.taskRunning(task)
			if running {
				// The backend runs "Run now" in its own goroutine: until it
				// records the run, the card says it is in flight instead of
				// leaving the user wondering whether the click worked.
				ui.Spinner(c).Label(st.tr("运行中…", "Running…")).
					Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, st.tr("运行中…", "Running…")),
					t.Font(design.TypeXXS)).TextColor(t.WarningText)
			}
			runNow := design.Apply(ui.Button(c, st.tr("立即运行", "Run now")),
				t.SecondaryButton()).Tooltip(st.tr("立即执行一次该任务", "Run this task once now")).
				Disabled(running)
			if runNow.Clicked() && !running {
				if err := service.RunNow(context.Background(), task.ID); err != nil {
					st.pageErr = automationErrorText(st,
						st.tr("运行任务失败：", "Failed to run task: "), err)
				} else {
					st.pageErr = ""
					st.markRunning(task.ID)
				}
				runtime.Invalidate()
			}
			edit := ui.Button(c, st.tr("编辑", "Edit")).
				Tooltip(st.tr("修改名称、提示词或运行计划", "Change the name, prompt or schedule"))
			if edit.Clicked() {
				openEditForm(st, task)
				runtime.Invalidate()
			}
			remove := design.Apply(ui.Button(c, st.tr("删除", "Delete")).Tooltip(st.tr("删除该任务", "Delete this task")),
				t.DangerButton())
			if remove.Clicked() {
				st.deleteOpen = true
				st.deleteTarget = task.ID
				st.deleteTask = task
				st.deleteName = firstNonEmpty(task.Name, st.tr("未命名任务", "Untitled task"))
				runtime.Invalidate()
			}
		})
	})
}

// runStatusLabel renders one run status label.
// runStatusLabel names a finished run. The backend's success status means the
// prompt was delivered to a fresh session, not that the agent's work succeeded:
// the label says 已投递 (sent) so the UI does not claim an outcome it cannot see.
func runStatusLabel(st *state, status string) string {
	if status == contracts.AutomationRunFailure {
		return st.tr("失败", "Failed")
	}
	return st.tr("已投递", "Sent")
}

// automationRunOutcomeHint explains that a successful delivery says nothing
// about the session's result.
func automationRunOutcomeHint(st *state) string {
	return st.tr("结果由会话决定", "The result depends on that session")
}

// automationRunSummary renders one run record's summary for the UI. The
// automation backend's own bookkeeping line ("ran in chat session <id>") is an
// internal id, so it becomes localized copy without the id; every other summary
// (an error, a tool result) is shown bounded, with the full text in the row's
// tooltip.
func automationRunSummary(st *state, summary string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "ran in chat session ") {
		return st.tr("已在会话中运行", "Ran in a chat session")
	}
	const maxSummaryRunes = 120
	runes := []rune(trimmed)
	if len(runes) > maxSummaryRunes {
		return string(runes[:maxSummaryRunes]) + "…"
	}
	return trimmed
}

// runStatusColor maps one run status to its semantic text color token.
func runStatusColor(t *design.Tokens, status string) ui.Color {
	if status == contracts.AutomationRunFailure {
		return t.DangerText
	}
	return t.SuccessText
}

// buildTaskHistory is the task card's run-history disclosure: the latest run
// summary stays visible, and the native Collapsible lists the recent runs
// (newest first).
func buildTaskHistory(c *ui.Context, t *design.Tokens, st *state, task contracts.AutomationTask) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if len(task.RunHistory) > 0 {
			latest := task.RunHistory[0]
			design.Apply(ui.Badge(c, runStatusLabel(st, latest.Status)), t.Badge()).
				TextColor(runStatusColor(t, latest.Status))
			design.Apply(ui.Text(c, firstNonEmpty(automationRunSummary(st, latest.Summary), "—")),
				t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…").
				Tooltip(strings.TrimSpace(latest.Summary))
			if latest.Status != contracts.AutomationRunFailure {
				design.Apply(ui.Text(c, automationRunOutcomeHint(st)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
			if sessionID := automationRunSessionID(latest.Summary); sessionID != "" {
				st.buildOpenSessionButton(c, t, sessionID)
			}
		} else {
			ui.Spacer(c).Grow(1)
		}
	})
	open := st.historyOpen[task.ID]
	ui.Collapsible(c, st.tr("运行历史", "Run history"), &open, func() {
		if len(task.RunHistory) == 0 {
			design.Apply(ui.Text(c, st.tr("还没有运行记录", "No runs yet")), t.TextMuted(design.TypeXXS))
			return
		}
		for _, record := range task.RunHistory {
			record := record
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				started := firstNonEmpty(uikit.FormatDateTime(st.locale, record.StartedAt), "—")
				design.Apply(ui.Text(c, started), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
				design.Apply(ui.Badge(c, runStatusLabel(st, record.Status)), t.Badge()).
					TextColor(runStatusColor(t, record.Status))
				design.Apply(ui.Text(c, firstNonEmpty(automationRunSummary(st, record.Summary), "—")),
					t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…").
					Tooltip(strings.TrimSpace(record.Summary))
				if record.Status != contracts.AutomationRunFailure {
					design.Apply(ui.Text(c, automationRunOutcomeHint(st)), t.Font(design.TypeXXXS)).
						TextColor(t.LabelTertiary).NoWrap()
				}
				if sessionID := automationRunSessionID(record.Summary); sessionID != "" {
					st.buildOpenSessionButton(c, t, sessionID)
				}
			})
		}
	})
	st.historyOpen[task.ID] = open
}

// buildCreateForm is the inline task creation card: a native Form with visible
// field labels (the old form used placeholders as labels).
func buildCreateForm(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state) {
	// Recompute the schedule field's error and its next-run preview from the
	// draft every frame: typing updates the preview immediately, and the error
	// clears as soon as the draft becomes valid again.
	validateScheduleDraft(st)
	name := strings.TrimSpace(st.formName)
	prompt := strings.TrimSpace(st.formPrompt)
	schedule := strings.TrimSpace(st.formSchedule)
	valid := name != "" && prompt != "" && schedule != ""

	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		formTitle := st.tr("新建自动化任务", "New automation task")
		if strings.TrimSpace(st.formEditID) != "" {
			formTitle = st.tr("编辑自动化任务", "Edit automation task")
		}
		design.Apply(ui.Text(c, formTitle), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		ui.Form(c, func() {
			ui.Field(c, st.tr("任务名称", "Task name"), func() {
				ui.TextInput(c, &st.formName).
					Placeholder(st.tr("例如：每日依赖巡检", "For example: daily dependency check")).FillWidth()
			})
			ui.Field(c, st.tr("任务提示词", "Task prompt"), func() {
				ui.TextArea(c, &st.formPrompt).
					Placeholder(st.tr("任务触发时发给 agent 的提示词", "Prompt sent to the agent when the task runs")).
					FillWidth().MinHeight(design.Space6 * 4)
			})
			ui.Field(c, st.tr("运行计划", "Schedule"), func() {
				ui.TextInput(c, &st.formSchedule).
					Placeholder("*/5 * * * * " + st.tr("或", "or") + " every 60s").FillWidth()
			}).Description(st.tr(
				"支持 */N * * * *（每 N 分钟）、M H * * *（每天 HH:MM）、@hourly、@daily、every <N>s、after <N>s。",
				"Supports */N * * * * (every N minutes), M H * * * (daily at HH:MM), @hourly, @daily, every <N>s, after <N>s.")).
				Error(st.scheduleErr)
			if st.schedulePreview != "" {
				design.Apply(ui.Text(c, st.schedulePreview), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
			}
			ui.Field(c, st.tr("模型", "Model"), func() {
				buildModelSelector(c, st)
			}).Description(st.tr("留空跟随默认模型；可从内置目录选择，也可自定义模型 ID。",
				"Empty follows the default model; pick a built-in preset or type a custom model id."))
			if hint := automationModelHint(st, st.formModel); hint != "" {
				design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
					TextColor(t.WarningText).MaxLines(2)
			}
			ui.Field(c, st.tr("工作区", "Workspace"), func() {
				buildWorkspaceSelector(c, st)
			})
			ui.Field(c, st.tr("权限", "Permission"), func() {
				buildPermissionSelector(c, st)
			})
			ui.Field(c, st.tr("启用", "Enabled"), func() {
				ui.Switch(c, &st.formEnabled).Label(st.tr("启用任务", "Enable task"))
			})
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spacer(c).Grow(1)
			cancel := design.Apply(ui.Button(c, st.tr("取消", "Cancel")), t.SecondaryButton())
			if cancel.Clicked() {
				st.closeForm()
				runtime.Invalidate()
			}
			save := design.Apply(ui.PrimaryButton(c, st.tr("保存", "Save")), t.PrimaryButton()).Disabled(!valid)
			if save.Clicked() && valid {
				// Validate the schedule with the same parser the backend runs so
				// an invalid draft is reported at the field, not as an English
				// backend error after a round trip.
				if raw := strings.TrimSpace(st.formSchedule); raw != "" {
					if _, err := contracts.ParseAutomationSchedule(raw); err != nil {
						st.scheduleErr = st.tr("运行计划无效：请检查格式与取值。",
							"Invalid schedule: check the format and values.")
						st.schedulePreview = ""
						runtime.Invalidate()
						return
					}
				}
				task := contracts.AutomationTask{
					Name:        name,
					Prompt:      prompt,
					Model:       strings.TrimSpace(st.formModel),
					Schedule:    schedule,
					WorkspaceID: strings.TrimSpace(st.formWorkspaceID),
					Permission:  strings.TrimSpace(st.formPermission),
					Enabled:     st.formEnabled,
				}
				var err error
				if strings.TrimSpace(st.formEditID) != "" {
					updated := st.formEditTask
					updated.Name = task.Name
					updated.Prompt = task.Prompt
					updated.Model = task.Model
					updated.Schedule = task.Schedule
					updated.WorkspaceID = task.WorkspaceID
					updated.Permission = task.Permission
					updated.Enabled = task.Enabled
					_, err = service.Update(context.Background(), updated)
				} else {
					_, err = service.Create(context.Background(), task)
				}
				if err != nil {
					action := st.tr("创建任务失败：", "Failed to create task: ")
					if st.formEditID != "" {
						action = st.tr("保存任务失败：", "Failed to save task: ")
					}
					st.formErr = automationErrorText(st, action, err)
				} else {
					st.resetForm()
				}
				runtime.Invalidate()
			}
		})
		if st.formErr != "" {
			uikit.ErrorLine(c, st.formErr)
		}
	})
}

// resetForm clears the inline form and its edit target after a successful save.
func (st *state) resetForm() {
	st.formOpen = false
	st.formName = ""
	st.formPrompt = ""
	st.formModel = ""
	st.formSchedule = ""
	st.formWorkspaceID = ""
	st.formWorkspaceName = ""
	st.formPermission = ""
	st.formEnabled = true
	st.formErr = ""
	st.formEditID = ""
	st.formEditTask = contracts.AutomationTask{}
}

// humanSchedule renders the human-readable form of a stored schedule through the
// shared parser: the card and the form preview show the same wording. An
// unrecognized expression returns "" and the card shows the raw text instead.
func humanSchedule(st *state, raw string) string {
	schedule, err := contracts.ParseAutomationSchedule(raw)
	if err != nil {
		return ""
	}
	return automationScheduleLabel(st, schedule)
}

// automationScheduleLabel renders one parsed schedule in the frame's language.
func automationScheduleLabel(st *state, schedule contracts.AutomationSchedule) string {
	switch schedule.Kind() {
	case contracts.AutomationScheduleEveryMinutes:
		minutes := schedule.EveryMinutes()
		if minutes == 1 {
			return st.tr("每分钟", "Every minute")
		}
		return st.tr(fmt.Sprintf("每 %d 分钟", minutes), fmt.Sprintf("Every %d minutes", minutes))
	case contracts.AutomationScheduleHourly:
		return st.tr("每小时", "Every hour")
	case contracts.AutomationScheduleDaily:
		hour, minute, _ := schedule.HourMinute()
		return st.tr(fmt.Sprintf("每天 %02d:%02d", hour, minute),
			fmt.Sprintf("Daily at %02d:%02d", hour, minute))
	case contracts.AutomationScheduleEverySeconds:
		seconds := schedule.IntervalSeconds()
		return st.tr(fmt.Sprintf("每 %d 秒", seconds), fmt.Sprintf("Every %d seconds", seconds))
	case contracts.AutomationScheduleOnceAfter:
		seconds := schedule.IntervalSeconds()
		return st.tr(fmt.Sprintf("%d 秒后运行一次", seconds),
			fmt.Sprintf("Once after %d seconds", seconds))
	default:
		return schedule.String()
	}
}

// automationSchedulePreview composes the valid-draft preview line: the next run
// instant plus the human-readable shape.
func automationSchedulePreview(st *state, schedule contracts.AutomationSchedule) string {
	next := uikit.FormatDateTime(st.locale, schedule.Next(time.Now()))
	return st.tr("下次运行：", "Next run: ") + next + " · " + automationScheduleLabel(st, schedule)
}

// validateScheduleDraft recomputes the field error and preview from the draft.
// It returns false when the draft is non-empty and invalid.
func validateScheduleDraft(st *state) bool {
	st.scheduleErr = ""
	st.schedulePreview = ""
	raw := strings.TrimSpace(st.formSchedule)
	if raw == "" {
		return true
	}
	schedule, err := contracts.ParseAutomationSchedule(raw)
	if err != nil {
		st.scheduleErr = st.tr("运行计划无效：请检查格式与取值。",
			"Invalid schedule: check the format and values.")
		return false
	}
	st.schedulePreview = automationSchedulePreview(st, schedule)
	return true
}

// automationModelCatalog is the composer's model preset list (the settings row
// wins, otherwise the built-in catalog) — the same source the Model page edits.
func automationModelCatalog(st *state) []contracts.ModelInfo {
	store, ok := st.ctx.Get(contracts.KeySettings)
	if !ok || store == nil {
		return contracts.DefaultModelCatalog()
	}
	models, err := contracts.ParseModelInfoList(store.Get(contracts.SettingModelCatalogModels, ""))
	if err != nil || len(models) == 0 {
		return contracts.DefaultModelCatalog()
	}
	return models
}

// automationModelCatalogMatch resolves one model id to its catalog entry.
func automationModelCatalogMatch(models []contracts.ModelInfo, id string) (contracts.ModelInfo, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return contracts.ModelInfo{}, false
	}
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.ID), id) {
			return model, true
		}
	}
	return contracts.ModelInfo{}, false
}

// automationModelLabel renders the model field's menu label: the follow-default
// row, a catalog display name, or the custom id.
func automationModelLabel(st *state, models []contracts.ModelInfo, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return st.tr("跟随默认模型", "Use the default model")
	}
	if match, ok := automationModelCatalogMatch(models, model); ok {
		return firstNonEmpty(strings.TrimSpace(match.Name), match.ID)
	}
	return model
}

// automationModelHint warns about a model that is outside the catalog, with the
// same rule the Model page uses: the id is kept, but the endpoint may fall back
// to its own default.
func automationModelHint(st *state, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if _, ok := automationModelCatalogMatch(automationModelCatalog(st), model); ok {
		return ""
	}
	return st.tr("该模型不在内置目录中；保存后仍会使用该名称，端点可能回退到默认模型。",
		"This model is not in the built-in catalog; it is still used as entered and the endpoint may fall back to its default.")
}

// buildModelSelector renders the task model picker: the follow-default row, the
// catalog presets and a custom-id mode for models the catalog does not know.
func buildModelSelector(c *ui.Context, st *state) {
	models := automationModelCatalog(st)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		label := automationModelLabel(st, models, st.formModel)
		ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
			current := m.Item(st.tr("跟随默认模型", "Use the default model"))
			current.Radio(strings.TrimSpace(st.formModel) == "" && !st.formModelCustom)
			if current.Chosen() {
				st.formModel = ""
				st.formModelCustom = false
			}
			for _, model := range models {
				model := model
				display := firstNonEmpty(strings.TrimSpace(model.Name), strings.TrimSpace(model.ID))
				item := m.Item(display)
				item.Radio(!st.formModelCustom && strings.EqualFold(strings.TrimSpace(st.formModel), strings.TrimSpace(model.ID)))
				if item.Chosen() {
					st.formModel = model.ID
					st.formModelCustom = false
				}
			}
			custom := m.Item(st.tr("自定义模型…", "Custom model…"))
			custom.Radio(st.formModelCustom)
			if custom.Chosen() {
				st.formModelCustom = true
				if _, ok := automationModelCatalogMatch(models, st.formModel); ok {
					st.formModel = ""
				}
			}
		}).Tooltip(st.tr("选择任务运行使用的模型", "Choose the model this task runs with"))
		if st.formModelCustom {
			ui.TextInput(c, &st.formModel).
				Label(st.tr("自定义模型 ID", "Custom model id")).
				Placeholder(st.tr("例如：deepseek-chat", "e.g. deepseek-chat")).FillWidth()
		}
	})
}

// openCreateForm/Esc-close helpers -----------------------------------------

// openCreateForm opens the inline form in create mode with a clean error state.
func (st *state) openCreateForm() {
	st.formOpen = true
	st.formErr = ""
	st.scheduleErr = ""
	st.schedulePreview = ""
}

// closeForm closes the inline form and clears its transient validation state,
// keeping the drafts (reopening restores what the user typed).
func (st *state) closeForm() {
	st.formOpen = false
	st.formErr = ""
	st.scheduleErr = ""
	st.schedulePreview = ""
	st.formEditID = ""
	st.formEditTask = contracts.AutomationTask{}
}

// openEditForm opens the inline form prefilled with one task: the form's save
// action calls Update instead of Create, so a schedule or prompt can be fixed
// without deleting and recreating the task.
func openEditForm(st *state, task contracts.AutomationTask) {
	st.openCreateForm()
	st.formEditID = task.ID
	st.formEditTask = task
	st.formName = task.Name
	st.formPrompt = task.Prompt
	st.formModel = task.Model
	st.formSchedule = task.Schedule
	st.formWorkspaceID = task.WorkspaceID
	st.formWorkspaceName = ""
	st.formPermission = normalizeAutomationPermission(task.Permission)
	st.formEnabled = task.Enabled
}

// run tracking and session links ------------------------------------------

// runRunningGuard bounds an in-flight "Run now" marker in case the backend never
// records the run (for example when its goroutine panics before the write).
const runRunningGuard = 15 * time.Minute

// markRunning records that "Run now" was pressed for one task.
func (st *state) markRunning(taskID string) {
	if st.running == nil {
		st.running = make(map[string]time.Time)
	}
	st.running[taskID] = time.Now()
}

// taskRunning reports whether a task has an in-flight "Run now": the marker is
// cleared once the backend records the run (LastRun moved) or the task is gone.
func (st *state) taskRunning(task contracts.AutomationTask) bool {
	started, ok := st.running[task.ID]
	if !ok {
		return false
	}
	if !task.LastRun.IsZero() && task.LastRun.After(started) {
		delete(st.running, task.ID)
		return false
	}
	if time.Since(started) > runRunningGuard {
		delete(st.running, task.ID)
		return false
	}
	return true
}

// automationRunSessionID extracts the session id from the backend's run
// summary ("ran in chat session <id>"); other summaries carry no session.
func automationRunSessionID(summary string) string {
	trimmed := strings.TrimSpace(summary)
	const prefix = "ran in chat session "
	if len(trimmed) <= len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(trimmed[len(prefix):])
}

// buildOpenSessionButton renders the history row's link into the session the run
// created, so a failed agent turn can be inspected where it happened.
func (st *state) buildOpenSessionButton(c *ui.Context, t *design.Tokens, sessionID string) {
	open := design.Apply(ui.Button(c, st.tr("打开会话", "Open session")), t.SecondaryButton()).
		Tooltip(st.tr("打开这次运行创建的会话", "Open the session this run created"))
	if !open.Clicked() {
		return
	}
	if chat, ok := st.ctx.Get(contracts.KeyChat); ok && chat != nil {
		_ = chat.SetActiveSession(sessionID)
	}
	if st.navigator != nil {
		_ = st.navigator.Push("session")
	}
}

// automationErrorText localizes a backend failure: the automation module's
// errors carry an English "automation:" prefix and parser grammar, which never
// reach the UI verbatim.
func automationErrorText(st *state, prefix string, err error) string {
	if err == nil {
		return prefix
	}
	message := strings.TrimSpace(strings.TrimPrefix(err.Error(), "automation: "))
	lower := strings.ToLower(message)
	if strings.Contains(lower, "schedule") || strings.Contains(lower, "cron") ||
		strings.Contains(lower, "every") || strings.Contains(lower, "after") {
		return prefix + st.tr("运行计划无效，请检查格式后重试。",
			"The schedule is invalid; check it and try again.")
	}
	return prefix + message
}

// automationRawErrorText sanitizes a persisted run error for the card: the
// backend's module prefix is noise, the cause stays visible.
func automationRawErrorText(st *state, raw string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "automation: "))
	if trimmed == "" {
		return raw
	}
	return trimmed
}

// automationPermissionChoice is one permission option in the task form and
// cards.
type automationPermissionChoice struct {
	value string
	zh    string
	en    string
}

var automationPermissions = []automationPermissionChoice{
	{value: contracts.PermissionReadOnly, zh: "只读", en: "Read-only"},
	{value: contracts.PermissionWorkspace, zh: "工作区可写", en: "Workspace-write"},
	{value: contracts.PermissionFull, zh: "所有权限", en: "All permissions"},
}

// normalizeAutomationPermission maps one stored task override onto the three
// current presets: the retired "ask"/"never" values (old picker rows) normalize
// to read-only and the empty/unknown values keep their raw value so the form can
// still show an inherit/legacy selection.
func normalizeAutomationPermission(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case contracts.PermissionAsk, contracts.PermissionNever:
		return contracts.PermissionReadOnly
	default:
		return value
	}
}

// automationPermissionLabel renders one permission value. The retired
// "ask"/"never" values stored by old tasks display as their normalized default
// (read-only); any other unknown value shows its raw value.
func automationPermissionLabel(st *state, value string) string {
	value = normalizeAutomationPermission(value)
	for _, choice := range automationPermissions {
		if choice.value == value {
			return st.tr(choice.zh, choice.en)
		}
	}
	return value
}

// buildWorkspaceSelector renders the optional workspace popover; an empty
// selection means "use the current workspace" (WorkspaceID stays empty).
func buildWorkspaceSelector(c *ui.Context, st *state) {
	label := st.tr("跟随当前工作区", "Follow the current workspace")
	if id := strings.TrimSpace(st.formWorkspaceID); id != "" {
		label = firstNonEmpty(st.formWorkspaceName, id)
	}
	ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		current := m.Item(st.tr("跟随当前工作区", "Follow the current workspace"))
		current.Checked(strings.TrimSpace(st.formWorkspaceID) == "")
		if current.Chosen() {
			st.formWorkspaceID = ""
			st.formWorkspaceName = ""
		}
		service, ok := st.ctx.Get(contracts.KeyWorkspace)
		if !ok || service == nil {
			return
		}
		workspaces, err := service.List(context.Background())
		if err != nil {
			return
		}
		for _, workspace := range workspaces {
			workspace := workspace
			name := firstNonEmpty(workspace.Name, workspace.ID)
			item := m.Item(name)
			item.Checked(workspace.ID == strings.TrimSpace(st.formWorkspaceID))
			if item.Chosen() {
				st.formWorkspaceID = workspace.ID
				st.formWorkspaceName = name
			}
		}
	}).Tooltip(st.tr("选择任务运行的工作区", "Choose the workspace the task runs in"))
}

// buildPermissionSelector renders the optional per-task permission override;
// an empty selection keeps the global approval setting.
func buildPermissionSelector(c *ui.Context, st *state) {
	label := st.tr("默认全局", "Global default")
	if permission := strings.TrimSpace(st.formPermission); permission != "" {
		label = automationPermissionLabel(st, permission)
	}
	ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		global := m.Item(st.tr("默认全局", "Global default"))
		global.Checked(strings.TrimSpace(st.formPermission) == "")
		if global.Chosen() {
			st.formPermission = ""
		}
		for _, choice := range automationPermissions {
			choice := choice
			item := m.Item(st.tr(choice.zh, choice.en))
			item.Checked(choice.value == strings.TrimSpace(st.formPermission))
			if item.Chosen() {
				st.formPermission = choice.value
			}
		}
	}).Tooltip(st.tr("选择任务会话的权限模式", "Choose the task session's permission mode"))
}

// buildDeleteDialog is the native delete confirmation for one task.
func buildDeleteDialog(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state) {
	if !st.deleteOpen {
		return
	}
	message := st.tr(
		fmt.Sprintf("“%s”将被删除；删除后可在通知里撤销。", firstNonEmpty(st.deleteName, st.tr("未命名任务", "Untitled task"))),
		fmt.Sprintf("%q will be deleted. Undo it from the notification.", firstNonEmpty(st.deleteName, st.tr("未命名任务", "Untitled task"))))
	switch ui.AlertDialog(c, &st.deleteOpen, st.tr("删除自动化任务", "Delete automation task"),
		message, st.tr("取消", "Cancel"), st.tr("确认删除", "Delete")) {
	case 1:
		deleted := st.deleteTask
		if err := service.Delete(context.Background(), st.deleteTarget); err != nil {
			st.pageErr = automationErrorText(st,
				st.tr("删除任务失败：", "Failed to delete task: "), err)
		} else {
			st.pageErr = ""
			raiseTaskDeletedToast(st, service, deleted)
		}
		st.deleteTarget = ""
		st.deleteName = ""
		st.deleteTask = contracts.AutomationTask{}
		runtime.Invalidate()
	}
}

// actionNotifier is the optional notifier half that renders one action button
// on a toast (contracts.ActionNotifier); a host without it gets a plain toast.
type actionNotifier interface {
	NotifyAction(level, text, actionLabel string, action func()) contracts.Notification
}

// raiseTaskDeletedToast reports a successful task deletion with an Undo action
// that re-creates the task (same name/prompt/schedule; the backend assigns a
// fresh id and recomputes the next run).
func raiseTaskDeletedToast(st *state, service contracts.AutomationService, task contracts.AutomationTask) {
	if st == nil || st.notifier == nil || service == nil {
		return
	}
	name := firstNonEmpty(task.Name, st.tr("未命名任务", "Untitled task"))
	text := st.tr("已删除任务：", "Task deleted: ") + name
	undo := func() {
		restored := task
		restored.ID = ""
		restored.CreatedAt = time.Time{}
		restored.UpdatedAt = time.Time{}
		restored.LastRun = time.Time{}
		restored.NextRun = time.Time{}
		restored.LastError = ""
		restored.RunHistory = nil
		_, _ = service.Create(context.Background(), restored)
		if st.runtime != nil {
			st.runtime.Invalidate()
		}
	}
	if notifier, ok := st.notifier.(actionNotifier); ok {
		notifier.NotifyAction(contracts.NotifySuccess, text, st.tr("撤销", "Undo"), undo)
		return
	}
	st.notifier.Notify(contracts.NotifySuccess, text)
}

// formatTaskTime renders a task timestamp in the user's date format, "—" when
// unset. It goes through the shared locale helper so every page formats dates
// the same way.
func formatTaskTime(st *state, value time.Time) string {
	return firstNonEmpty(uikit.FormatDateTime(st.locale, value), "—")
}

// workspaceDisplayName resolves a task's workspace id to its display name. The
// workspace list is cached for the screen visit (the host service is in-memory)
// and a vanished workspace reads as such instead of leaking the raw id.
func (st *state) workspaceDisplayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if !st.workspaceNamesLoaded {
		st.workspaceNamesLoaded = true
		st.workspaceNames = make(map[string]string)
		if service, ok := st.ctx.Get(contracts.KeyWorkspace); ok && service != nil {
			if workspaces, err := service.List(context.Background()); err == nil {
				for _, workspace := range workspaces {
					name := firstNonEmpty(strings.TrimSpace(workspace.Name), strings.TrimSpace(workspace.ID))
					if trimmed := strings.TrimSpace(workspace.ID); trimmed != "" {
						st.workspaceNames[trimmed] = name
					}
				}
			}
		}
	}
	if name := st.workspaceNames[id]; name != "" {
		return name
	}
	return st.tr("未知工作区", "Unknown workspace")
}
