package shell

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/automation"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/store"
)

// automationLivePage is the Automation screen with real persisted tasks. The
// static baseline path (App.Ctrl == nil) never calls it.
func (a *App) automationLivePage(c *ui.Context, t *design.Tokens) {
	a.automationHeadingLive(c, t)
	a.automationFiltersLive(c, t)
	a.automationSearchLive(c, t)
	ui.Scroll(c).Grow(1).Children(func() {
		a.automationTaskList(c, t)
	})
	a.automationDialog(c, t)
}

// automationHeadingLive is the h1 and the dark "New" action, with the action
// opening the create dialog.
func (a *App) automationHeadingLive(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(automationHeadingHeight).
		Padding(automationHeadingPadTop, 0, 0, 0).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Margin(0, 0, automationHeadingGap, 0).Children(func() {
		ui.Text(c, a.T("automation.automation_tasks")).FontSize(20).FontWeight(500).LineHeight(1.4).
			TextColor(t.LabelPrimary)
		button := ui.Row(c).Height(32).PaddingX(12).Gap(4).Radius(16).
			AlignItems(ui.Center).Background(t.LabelPrimary).
			Label(a.T("automation.new_automation_task")).Role(ui.RoleButton).Focusable()
		button.Children(func() {
			ui.Row(c).Size(16, 16).Center().Children(func() {
				ui.Icon(c, automationPlusIcon).Size(13, 13).
					TextColor(t.OnPrimary)
			})
			ui.Text(c, a.T("automation.new")).FontSize(13).LineHeight(20.0 / 13.0).
				TextColor(t.OnPrimary)
		})
		button.OnClick(func() { a.beginAutomationAdd() })
	})
}

// automationFiltersLive is the All / Enabled / Inactive row; clicking a tab
// filters the list.
func (a *App) automationFiltersLive(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	filters := []string{"all", "enabled", "inactive"}
	// Keep the TabsBase pointer in step with direct automationFilter writes
	// (tests and older call sites set the string).
	for i, key := range filters {
		if key == st.automationFilter {
			st.automationTab = i
			break
		}
	}
	labels := []string{
		a.T("automation.all"), a.T("automation.enabled"), a.T("automation.inactive"),
	}
	tabs := ui.TabsBase(c, &st.automationTab, len(filters))
	tabs.List.Height(automationTabHeight).Gap(automationTabGap).
		AlignItems(ui.Center).Margin(0, 0, automationFiltersMB, 0).Children(func() {
		for i, label := range labels {
			active := i == st.automationTab
			el := tabs.Tab(i).Height(automationTabHeight).PaddingX(automationTabPadX).
				Radius(14).AlignItems(ui.Center).Label(label)
			if active {
				el = el.Background(t.BgHover)
			}
			el.Children(func() {
				text := ui.Text(c, label).FontSize(14).LineHeight(22.0 / 14.0).Top(1)
				if active {
					text = text.TextColor(t.LabelPrimary)
				} else {
					text = text.TextColor(t.LabelTertiary)
				}
			})
		}
	})
	if st.automationTab >= 0 && st.automationTab < len(filters) {
		st.automationFilter = filters[st.automationTab]
	}
}

// automationSearchLive is the rounded search field over the task list.
func (a *App) automationSearchLive(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	ui.Row(c).Height(automationSearchHeight).
		PaddingX(automationSearchPadX).Gap(automationSearchGap).
		Border(1, t.BorderL3).Radius(design.RadiusMD).
		AlignItems(ui.Center).Margin(0, 0, automationSearchMB, 0).Children(func() {
		ui.Row(c).Size(16, 16).Center().Children(func() {
			ui.Icon(c, automationSearchIcon).Size(14, 14).
				TextColor(t.LabelTertiary)
		})
		ui.TextInputBase(c, &st.automationQuery).
			Label(a.T("automation.search")).Placeholder(a.T("automation.search")).
			FontSize(14).Grow(1)
	})
}

// automationTaskList renders the filtered tasks, or the empty state.
func (a *App) automationTaskList(c *ui.Context, t *design.Tokens) {
	tasks, err := a.Ctrl.Automations().List()
	if err != nil {
		ui.Text(c, a.T("automation.could_not_load_automation_tasks")).
			FontSize(13).TextColor(t.Danger).Padding(automationEmptyPadY, 0, 0, 0)
		return
	}
	visible := a.visibleAutomations(tasks)
	if len(visible) == 0 {
		a.automationEmptyLive(c, t)
		return
	}
	ui.Column(c).Gap(design.Space2).Children(func() {
		for _, task := range visible {
			a.automationTaskRow(c, t, task)
		}
	})
}

// visibleAutomations filters tasks by the selected tab and the search query.
func (a *App) visibleAutomations(tasks []store.Automation) []store.Automation {
	st := a.shellUI()
	query := strings.ToLower(strings.TrimSpace(st.automationQuery))
	out := make([]store.Automation, 0, len(tasks))
	for _, task := range tasks {
		switch st.automationFilter {
		case "enabled":
			if !task.Enabled {
				continue
			}
		case "inactive":
			if task.Enabled {
				continue
			}
		}
		if query != "" {
			haystack := strings.ToLower(task.Title + "\n" + task.Prompt + "\n" + task.Cron)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, task)
	}
	return out
}

// automationTaskRow is one persisted task: title, prompt snippet, schedule and
// next run, the enable switch and the row actions.
func (a *App) automationTaskRow(c *ui.Context, t *design.Tokens, task store.Automation) {
	// The page column is min(960, contentWidth-96). A docked right sidebar
	// leaves it 304px wide, where the switch and the four actions cannot sit
	// on one line: the row's fixed children shrank to W=0 and Delete became
	// unclickable. Stack the actions onto a wrapping second row while the
	// column is too narrow for the single-line layout.
	stacked := a.automationPageWidth(c) < automationRowSingleLineW
	title := func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.Text(c, task.Title).FontSize(14).FixedLineHeight(20).FontWeight(500).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if !task.Enabled {
					ui.Row(c).Height(18).PaddingX(6).Radius(design.RadiusMax).
						Background(t.BgHover).AlignItems(ui.Center).Children(func() {
						ui.Text(c, a.T("automation.inactive")).FontSize(10).FixedLineHeight(14).
							TextColor(t.LabelTertiary)
					})
				}
			})
			if task.Prompt != "" {
				ui.Text(c, automationPromptSnippet(task.Prompt)).FontSize(12).FixedLineHeight(18).
					TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
			}
			ui.Text(c, a.automationTaskDetail(task)).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		})
	}
	// The switch is a real control, not decoration: clicking it persists the
	// same enabled flag as the Enable/Disable action.
	switchLabel := a.tr("Disable ", "停用 ") + task.Title
	if !task.Enabled {
		switchLabel = a.tr("Enable ", "启用 ") + task.Title
	}
	sw := func() {
		ui.Row(c).Shrink(0).Children(func() {
			a.pluginSwitchLive(c, t, task.Enabled, switchLabel, func() {
				a.toggleAutomation(c, task)
			})
		})
	}
	actions := func() {
		a.automationAction(c, t, a.T("automation.run_now"), func() {
			a.runAutomationNow(c, task.ID)
		})
		if task.Enabled {
			a.automationAction(c, t, a.T("automation.disable"), func() { a.toggleAutomation(c, task) })
		} else {
			a.automationAction(c, t, a.T("automation.enable"), func() { a.toggleAutomation(c, task) })
		}
		a.automationAction(c, t, a.T("automation.edit"), func() { a.beginAutomationEdit(task) })
		a.automationAction(c, t, a.T("automation.delete"), func() { a.deleteAutomation(c, task) })
	}
	if stacked {
		ui.Column(c).Padding(design.Space3, design.Space3).Gap(design.Space2).
			Radius(design.RadiusMD).Border(1, t.BorderL2).Children(func() {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				title()
				sw()
			})
			ui.Row(c).Gap(design.Space2).Wrap().AlignItems(ui.Center).Children(actions)
		})
		return
	}
	ui.Row(c).Padding(design.Space3, design.Space3).Gap(design.Space2).
		Radius(design.RadiusMD).Border(1, t.BorderL2).AlignItems(ui.Center).
		Children(func() {
			title()
			sw()
			actions()
		})
}

// automationAction is a small bordered row action.
func (a *App) automationAction(c *ui.Context, t *design.Tokens, label string, fn func()) {
	el := ui.Row(c).Height(28).MinWidth(44).PaddingX(10).Radius(design.RadiusSM).
		Border(1, t.BorderL3).AlignItems(ui.Center).Shrink(0).
		Label(label).Role(ui.RoleButton).Focusable()
	el.Children(func() {
		ui.Text(c, label).FontSize(12).FixedLineHeight(18).TextColor(t.LabelPrimary)
	})
	el.OnClick(fn)
}

// automationTaskDetail is the schedule and next-run line.
func (a *App) automationTaskDetail(task store.Automation) string {
	parts := []string{automation.Describe(task.Cron)}
	if task.NextRun != nil {
		parts = append(parts, a.T("automation.next", task.NextRun.Local().Format("2006-01-02 15:04")))
	}
	if task.LastRun != nil {
		parts = append(parts, a.T("automation.last", task.LastRun.Local().Format("2006-01-02 15:04")))
	}
	return strings.Join(parts, " · ")
}

// automationPromptSnippet collapses a multi-line prompt onto one line.
func automationPromptSnippet(prompt string) string {
	return strings.Join(strings.Fields(prompt), " ")
}

// automationEmptyLive is the baseline empty state with a clickable create
// action.
func (a *App) automationEmptyLive(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Padding(automationEmptyPadY, automationEmptyPadX).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, automationClockIcon).Size(24, 24).
			TextColor(ui.Hex("#ADB2B8")).Margin(0, 0, 12, 0)
		ui.Text(c, a.T("automation.empty")).
			FontSize(14).LineHeight(22.0/14.0).Top(0.5).
			LetterSpacing(0.05).
			TextColor(t.LabelTertiary).Margin(0, 0, 8, 0)
		label := a.T("automation.new_automation_task")
		button := ui.Row(c).Height(36).PaddingX(14).Radius(design.RadiusMD).
			Border(1, t.BorderL3).AlignItems(ui.Center).Justify(ui.Center).
			Background(t.BgBase).Margin(16, 0, 0, 0).
			Label(label).Role(ui.RoleButton).Focusable()
		button.Children(func() {
			ui.Text(c, label).FontSize(14).LineHeight(22.0 / 14.0).Top(1).
				TextColor(t.LabelPrimary)
		})
		button.OnClick(func() { a.beginAutomationAdd() })
	})
}

// beginAutomationAdd opens the create dialog.
func (a *App) beginAutomationAdd() {
	st := a.shellUI()
	st.automationDraft = automationDraft{enabled: true, cron: "@daily"}
	st.automationDialog = true
}

// beginAutomationEdit opens the edit dialog prefilled with task.
func (a *App) beginAutomationEdit(task store.Automation) {
	st := a.shellUI()
	st.automationDraft = automationDraft{
		id: task.ID, title: task.Title, prompt: task.Prompt,
		cron: task.Cron, enabled: task.Enabled,
	}
	st.automationDialog = true
}

// automationDialog is the create/edit form.
func (a *App) automationDialog(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	ui.Modal(c, &st.automationDialog, func() {
		ui.Column(c).Width(520).Gap(design.Space2).Children(func() {
			title := a.T("automation.new_automation_task")
			if st.automationDraft.id != "" {
				title = a.T("automation.edit_automation_task")
			}
			ui.Text(c, title).FontSize(16).FontWeight(600).TextColor(t.LabelPrimary)

			ui.Text(c, a.T("automation.title")).FontSize(12).TextColor(t.LabelTertiary)
			ui.TextInput(c, &st.automationDraft.title).Label(a.T("automation.title")).
				Placeholder(a.T("automation.daily_report")).AutoFocus()

			ui.Text(c, a.T("automation.prompt")).FontSize(12).TextColor(t.LabelTertiary)
			ui.TextArea(c, &st.automationDraft.prompt).Label(a.T("automation.prompt")).
				Placeholder(a.T("automation.summarize_yesterday_s_changes")).Height(92)

			ui.Text(c, a.T("automation.schedule")).FontSize(12).TextColor(t.LabelTertiary)
			ui.TextInput(c, &st.automationDraft.cron).Label(a.T("automation.schedule")).
				Placeholder("*/15 * * * *")
			ui.Text(c, a.T("automation.cron_15_0_9_hourly_or_daily")).
				FontSize(11).TextColor(t.LabelTertiary)

			ui.Checkbox(c, &st.automationDraft.enabled, a.T("automation.enabled"))

			ui.Row(c).Justify(ui.End).Gap(design.Space2).
				Padding(design.Space2, 0, 0, 0).Children(func() {
				if ui.Button(c, a.T("automation.cancel")).Clicked() {
					st.automationDialog = false
				}
				if ui.Button(c, a.T("automation.save")).Clicked() {
					a.confirmAutomation(c)
				}
			})
		})
	})
}

// confirmAutomation validates the cron expression and persists the task.
func (a *App) confirmAutomation(c *ui.Context) {
	st := a.shellUI()
	if a.Ctrl == nil {
		st.automationDialog = false
		return
	}
	draft := st.automationDraft
	title := strings.TrimSpace(draft.title)
	if title == "" {
		c.Toast(a.T("automation.title_is_required"))
		return
	}
	cron := strings.TrimSpace(draft.cron)
	next, err := automation.NextRun(cron, time.Now())
	if err != nil {
		c.Toast(a.T("automation.invalid_schedule"))
		return
	}
	task := store.Automation{
		ID:      draft.id,
		Title:   title,
		Prompt:  strings.TrimSpace(draft.prompt),
		Cron:    cron,
		Enabled: draft.enabled,
	}
	if task.Enabled {
		task.NextRun = &next
	}
	if task.ID != "" {
		// Editing must not clear the last-run history.
		if existing, err := a.Ctrl.Automations().Get(task.ID); err == nil {
			task.LastRun = existing.LastRun
			task.CreatedAt = existing.CreatedAt
		}
		_, err = a.Ctrl.Automations().Update(task)
	} else {
		_, err = a.Ctrl.Automations().Create(task)
	}
	if err != nil {
		c.Toast(a.T("automation.could_not_save_automation_task"))
		return
	}
	st.automationDialog = false
	c.Toast(a.T("automation.automation_task_saved"))
}

// toggleAutomation flips a task's enabled flag, scheduling the next run when
// it is turned on and clearing the stale schedule when it is turned off.
func (a *App) toggleAutomation(c *ui.Context, task store.Automation) {
	if a.Ctrl == nil {
		return
	}
	task.Enabled = !task.Enabled
	if task.Enabled {
		if next, err := automation.NextRun(task.Cron, time.Now()); err == nil {
			task.NextRun = &next
		}
	} else {
		task.NextRun = nil
	}
	if _, err := a.Ctrl.Automations().Update(task); err != nil {
		c.Toast(a.T("automation.could_not_update_automation_task"))
		return
	}
	if task.Enabled {
		c.Toast(a.T("automation.automation_task_enabled"))
	} else {
		c.Toast(a.T("automation.automation_task_disabled"))
	}
}

// deleteAutomation removes a task.
func (a *App) deleteAutomation(c *ui.Context, task store.Automation) {
	if a.Ctrl == nil {
		return
	}
	if err := a.Ctrl.Automations().Delete(task.ID); err != nil {
		c.Toast(a.T("automation.could_not_delete_automation_task"))
		return
	}
	c.Toast(a.T("automation.automation_task_deleted"))
}

// runAutomationNow invokes the app's RunNow hook. When no hook is wired the
// user is told execution is unavailable instead of a silent no-op.
func (a *App) runAutomationNow(c *ui.Context, id string) {
	if a.Ctrl == nil || !a.Ctrl.RunAutomationNow(id) {
		c.Toast(a.T("automation.no_automation_runner_is_configured"))
		return
	}
	c.Toast(a.T("automation.automation_started"))
}

// SetAutomationRunNow wires the app's "Run now" callback.
func (a *App) SetAutomationRunNow(fn func(id string)) {
	if a.Ctrl != nil {
		a.Ctrl.SetRunNow(fn)
	}
}
