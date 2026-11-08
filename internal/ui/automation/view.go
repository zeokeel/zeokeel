package automation

import (
	"context"
	"fmt"
	"strconv"
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
				st.formOpen = !st.formOpen
				st.formErr = ""
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
			uikit.ErrorLine(c, st.tr("读取任务失败：", "Failed to read tasks: ")+err.Error())
			return
		}
		buildTaskToolbar(c, t, runtime, st)
		filtered := filterAutomationTasks(tasks, st.query, st.taskFilter)
		if len(tasks) > 0 {
			design.Apply(ui.Text(c, automationTaskCountLabel(st, len(filtered))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
		switch {
		case len(tasks) == 0:
			uikit.EmptyStateBlock(c,
				st.localized(cfg.EmptyText, "还没有自动化任务", "No automation tasks yet"),
				st.tr("创建后按计划自动运行；也可以随时立即运行。",
					"Tasks run on their schedule after creation; you can also run one now."),
				func() {
					first := design.Apply(ui.PrimaryButton(c, st.tr("创建第一个任务", "Create the first task")),
						t.PrimaryButton())
					if first.Clicked() {
						st.formOpen = true
						st.formErr = ""
						runtime.Invalidate()
					}
				})
		case len(filtered) == 0:
			uikit.EmptyStateBlock(c, st.tr("没有匹配的任务", "No matching tasks"),
				st.tr("换个关键词或清空筛选试试。", "Try another search term or clear the filter."), nil)
		default:
			ui.Scroll(c).FillWidth().Grow(1).Children(func() {
				ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
					for _, task := range filtered {
						buildTaskCard(c, t, service, runtime, st, task)
					}
				})
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
				item.Checked(st.taskFilter == choice.value)
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
			// A native switch in a labeled field: the visible label says what the
			// switch does ("启用"), the task name is already the card title.
			enabled := task.Enabled
			ui.Field(c, st.tr("启用", "Enabled"), func() {
				sw := ui.Switch(c, &enabled)
				sw.Label(st.tr("启用任务", "Enable task"))
				if !sw.Changed() {
					return
				}
				updated := task
				updated.Enabled = enabled
				if _, err := service.Update(context.Background(), updated); err != nil {
					st.pageErr = st.tr("更新任务失败：", "Failed to update task: ") + err.Error()
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
			design.Apply(ui.Text(c, task.Schedule), t.MonoFamily()).FontSize(11).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		// Metadata is background information: one muted line, not three badges
		// stretched across the card.
		meta := make([]string, 0, 3)
		if model := strings.TrimSpace(task.Model); model != "" {
			meta = append(meta, st.tr("模型：", "Model: ")+model)
		}
		if workspaceID := strings.TrimSpace(task.WorkspaceID); workspaceID != "" {
			meta = append(meta, st.tr("工作区：", "Workspace: ")+workspaceID)
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
			uikit.ErrorLine(c, st.tr("上次错误：", "Last error: ")+task.LastError)
		}
		buildTaskHistory(c, t, st, task)
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
func runStatusLabel(st *state, status string) string {
	if status == contracts.AutomationRunFailure {
		return st.tr("失败", "Failed")
	}
	return st.tr("成功", "Success")
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
			design.Apply(ui.Text(c, firstNonEmpty(latest.Summary, "—")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
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
				design.Apply(ui.Text(c, formatTaskClock(st, record.StartedAt)), t.MonoFamily()).
					FontSize(11).TextColor(t.LabelTertiary).NoWrap()
				design.Apply(ui.Badge(c, runStatusLabel(st, record.Status)), t.Badge()).
					TextColor(runStatusColor(t, record.Status))
				design.Apply(ui.Text(c, firstNonEmpty(record.Summary, "—")), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
			})
		}
	})
	st.historyOpen[task.ID] = open
}

// buildCreateForm is the inline task creation card: a native Form with visible
// field labels (the old form used placeholders as labels).
func buildCreateForm(c *ui.Context, t *design.Tokens, service contracts.AutomationService, runtime contracts.UIRuntime, st *state) {
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
				ui.TextInput(c, &st.formSchedule).Placeholder("*/5 * * * * " + st.tr("或", "or") + " every 60s").FillWidth()
			})
			ui.Field(c, st.tr("模型", "Model"), func() {
				ui.TextInput(c, &st.formModel).Placeholder(st.tr("默认模型", "Default model")).FillWidth()
			})
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
		design.Apply(ui.Text(c, st.tr(
			"支持 cron 子集（分 时 日 月 周）与 every <duration>（如 every 60s）。",
			"Supports a cron subset (minute hour day month weekday) and every <duration> (e.g. every 60s).")),
			t.TextMuted(design.TypeXXXS)).MaxLines(2)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spacer(c).Grow(1)
			cancel := design.Apply(ui.Button(c, st.tr("取消", "Cancel")), t.SecondaryButton())
			if cancel.Clicked() {
				st.formOpen = false
				st.formErr = ""
				st.formEditID = ""
				st.formEditTask = contracts.AutomationTask{}
				runtime.Invalidate()
			}
			save := design.Apply(ui.PrimaryButton(c, st.tr("保存", "Save")), t.PrimaryButton()).Disabled(!valid)
			if save.Clicked() && valid {
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
					st.formErr = action + err.Error()
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

// openEditForm opens the inline form prefilled with one task: the form's save
// action calls Update instead of Create, so a schedule or prompt can be fixed
// without deleting and recreating the task.
func openEditForm(st *state, task contracts.AutomationTask) {
	st.formOpen = true
	st.formErr = ""
	st.formEditID = task.ID
	st.formEditTask = task
	st.formName = task.Name
	st.formPrompt = task.Prompt
	st.formModel = task.Model
	st.formSchedule = task.Schedule
	st.formWorkspaceID = task.WorkspaceID
	st.formWorkspaceName = ""
	st.formPermission = task.Permission
	st.formEnabled = task.Enabled
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

// humanSchedule renders the human-readable form of the schedule shapes the
// product documents (cron subset and "every <duration>"); an unrecognized
// expression returns "" and the card shows the raw text instead.
func humanSchedule(st *state, raw string) string {
	schedule := strings.TrimSpace(raw)
	lower := strings.ToLower(schedule)
	if strings.HasPrefix(lower, "every ") {
		if d, err := time.ParseDuration(strings.TrimSpace(lower[len("every "):])); err == nil && d > 0 {
			return st.tr("每 ", "Every ") + humanScheduleDuration(st, d)
		}
		return ""
	}
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return ""
	}
	step := func(field string) (int, bool) {
		if !strings.HasPrefix(field, "*/") {
			return 0, false
		}
		n, err := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		return n, err == nil && n > 0
	}
	if n, ok := step(fields[0]); ok && fields[1] == "*" && fields[2] == "*" && fields[3] == "*" && fields[4] == "*" {
		return st.tr("每 ", "Every ") + st.tr(fmt.Sprintf("%d 分钟", n), fmt.Sprintf("%d minutes", n))
	}
	if fields[0] == "0" {
		if n, ok := step(fields[1]); ok && fields[2] == "*" && fields[3] == "*" && fields[4] == "*" {
			return st.tr("每 ", "Every ") + st.tr(fmt.Sprintf("%d 小时", n), fmt.Sprintf("%d hours", n))
		}
	}
	if fields[2] == "*" && fields[3] == "*" && fields[4] == "*" {
		minute, merr := strconv.Atoi(fields[0])
		hour, herr := strconv.Atoi(fields[1])
		if merr == nil && herr == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24 {
			return st.tr(fmt.Sprintf("每天 %02d:%02d", hour, minute),
				fmt.Sprintf("Daily at %02d:%02d", hour, minute))
		}
	}
	return ""
}

// humanScheduleDuration renders one positive duration as the largest whole
// unit, e.g. 24h -> "24 小时".
func humanScheduleDuration(st *state, d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		hours := int(d / time.Hour)
		return st.tr(fmt.Sprintf("%d 小时", hours), fmt.Sprintf("%d hours", hours))
	case d%time.Minute == 0:
		minutes := int(d / time.Minute)
		return st.tr(fmt.Sprintf("%d 分钟", minutes), fmt.Sprintf("%d minutes", minutes))
	default:
		seconds := int(d / time.Second)
		return st.tr(fmt.Sprintf("%d 秒", seconds), fmt.Sprintf("%d seconds", seconds))
	}
}

// automationPermissionChoice is one permission option in the task form and
// cards.
type automationPermissionChoice struct {
	value string
	zh    string
	en    string
}

var automationPermissions = []automationPermissionChoice{
	{value: contracts.PermissionAsk, zh: "询问", en: "Ask"},
	{value: contracts.PermissionWorkspace, zh: "工作区", en: "Workspace"},
	{value: contracts.PermissionFull, zh: "全部", en: "Full access"},
	{value: contracts.PermissionNever, zh: "从不", en: "Never"},
}

// automationPermissionLabel renders one permission value (unknown values show
// their raw value).
func automationPermissionLabel(st *state, value string) string {
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
			st.pageErr = st.tr("删除任务失败：", "Failed to delete task: ") + err.Error()
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

// formatTaskClock renders a run timestamp's time of day in the current
// language's clock format, "—" when unset.
func formatTaskClock(st *state, value time.Time) string {
	return firstNonEmpty(uikit.FormatClock(st.locale, value), "—")
}
