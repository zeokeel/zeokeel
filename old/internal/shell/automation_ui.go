package shell

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/automation"
	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// automationLivePage is the Automation screen with real persisted tasks,
// laid out on the brand page column: native header action, segmented filter,
// native search field, task cards and the create/edit dialog.
func (a *App) automationLivePage(c *ui.Context, t *design.Tokens) {
	a.automationHeadingLive(c, t)
	// Automations only fire while the app runs (the scheduler lives in the
	// desktop process), so say it where the user creates them (PD-06).
	ui.Row(c).AlignItems(ui.Center).Margin(0, 0, t.Theme().Space(2), 0).Children(func() {
		nativeText(c, design.TypeXXS, t.LabelTertiary, a.menuText(
			"Automations run only while ZeoKeel is running in the foreground.",
			"自动化任务仅在 ZeoKeel 前台运行时执行。"))
	})
	a.automationFiltersLive(c, t)
	a.automationSearchLive(c, t)
	ui.Scroll(c).Grow(1).Children(func() {
		a.automationTaskList(c, t)
	})
	a.automationDialog(c, t)
	a.automationDeleteDialog(c, t)
}

// automationHeadingLive is the page header: the title and the native primary
// "New" action, which opens the create dialog.
func (a *App) automationHeadingLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Padding(th.Space(4), 0, 0, 0).Margin(0, 0, th.Space(4), 0).
		AlignItems(ui.End).Justify(ui.SpaceBetween).Children(func() {
		ui.Column(c).Gap(th.Space(1)).Children(func() {
			nativeText(c, design.TypeXL, t.LabelPrimary, a.T("automation.automation_tasks")).
				FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary, a.T("automation.subtitle")).
				MaxLines(1)
		})
		a.automationNewButton(c, t)
	})
}

// automationNewButton is the header's primary action: mygo's PrimaryButton
// (native focus, Enter/Space, disabled semantics) with the brand accent fill,
// a plus glyph and the "New" label. The accessible name is the full action so
// keyboard and screen-reader users get "New automation task".
func (a *App) automationNewButton(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	button := ui.PrimaryButton(c, "").Label(a.T("automation.new_automation_task"))
	button.Children(func() {
		ui.Icon(c, automationPlusIcon).Size(th.Space(3.5), th.Space(3.5)).
			TextColor(t.AccentText)
		nativeText(c, design.TypeBody, t.AccentText, a.T("automation.new")).FontWeight(500)
	})
	button.OnClick(func() { a.beginAutomationAdd() })
}

// automationFiltersLive is the All / Enabled / Inactive filter. It is built on
// mygo's segmented control base, so the filter is one focus group: Tab enters
// at the selected segment and the arrows move between the choices, as native
// desktop segmented controls do.
func (a *App) automationFiltersLive(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	th := t.Theme()
	filters := []string{"all", "enabled", "inactive"}
	// Keep the selected index in step with direct automationFilter writes
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
	// The track hugs its segments as native segmented controls do; the row
	// wrapper keeps the page column from stretching it to full width.
	var track ui.Element
	ui.Row(c).Margin(0, 0, th.Space(3), 0).AlignItems(ui.Center).Children(func() {
		parts := ui.SegmentedBase(c, &st.automationTab, len(filters))
		track = parts.Track.Height(th.Space(8)).Padding(th.Space(0.5)).Gap(th.Space(0.5)).
			Radius(th.Radius).Background(t.BgStrong).Border(1, t.BorderL2).
			Label(a.tr("Filter automation tasks", "筛选自动化任务"))
		track.Children(func() {
			for i, label := range labels {
				active := i == st.automationTab
				segment := parts.Segment(i).Height(th.Space(7)).PaddingX(th.Space(3)).
					Radius(th.Radius - th.Space(0.5)).AlignItems(ui.Center).Label(label)
				switch {
				case active && t.Dark:
					// In the dark theme the selected segment lifts with the
					// translucent brand tint; BgRaised is darker than the
					// track there and would read as "unselected".
					segment = segment.Background(t.BgHover).Border(1, t.BorderL3)
				case active:
					segment = segment.Background(t.BgRaised).Border(1, t.BorderL2)
				case segment.Hovered():
					segment = segment.Background(t.BgHover)
				}
				segment.Children(func() {
					color := t.LabelTertiary
					weight := 400
					if active {
						color, weight = t.LabelPrimary, 500
					}
					nativeText(c, design.TypeXS, color, label).FontWeight(weight)
				})
			}
		})
	})
	// Apply the click (if any) before the list reads the selected filter, so
	// the frame that handles the click already shows the new filtered list.
	_ = track.Changed()
	if st.automationTab >= 0 && st.automationTab < len(filters) {
		st.automationFilter = filters[st.automationTab]
	}
}

// automationSearchLive is the native search field over the task list: mygo's
// SearchField carries the magnifier, clear action, IME and focus behavior, so
// the page does not draw its own field chrome.
func (a *App) automationSearchLive(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	th := t.Theme()
	ui.Row(c).AlignItems(ui.Center).Margin(0, 0, th.Space(4), 0).Children(func() {
		ui.SearchField(c, &st.automationQuery).
			Label(a.T("automation.search")).
			Placeholder(a.T("automation.search")).
			Grow(1)
	})
}

// automationTaskList renders the filtered tasks, or the empty state.
func (a *App) automationTaskList(c *ui.Context, t *design.Tokens) {
	if a.Ctrl == nil || a.Ctrl.Automations() == nil {
		return
	}
	tasks, err := a.Ctrl.Automations().List()
	if err != nil {
		nativeText(c, design.TypeXXS, t.Danger, a.T("automation.could_not_load_automation_tasks")).
			PaddingY(t.Theme().Space(8))
		return
	}
	visible := a.visibleAutomations(tasks)
	if len(visible) == 0 {
		a.automationEmptyLive(c, t)
		return
	}
	th := t.Theme()
	ui.Column(c).Gap(th.Space(2)).Padding(0, 0, th.Space(6), 0).Children(func() {
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

// automationTaskRow is one persisted task: a brand card with the title,
// prompt snippet and schedule, the native enable switch and the native row
// actions. When the column is too narrow for one line the actions wrap onto a
// second row so every target is still usable beside the docked right sidebar.
func (a *App) automationTaskRow(c *ui.Context, t *design.Tokens, task store.Automation) {
	th := t.Theme()
	stacked := a.automationPageWidth(c) < automationRowSingleLineUnits*design.Space1
	title := func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(0.5)).Children(func() {
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				nativeText(c, design.TypeBody, t.LabelPrimary, task.Title).
					FontWeight(500).MaxLines(1).Ellipsis("…")
				if !task.Enabled {
					pill := ui.Row(c).PaddingX(th.Space(1.5)).PaddingY(th.Space(0.5)).
						Radius(design.RadiusMax).Background(t.BgHover).AlignItems(ui.Center)
					pill.Children(func() {
						nativeText(c, design.TypeXXXS, t.LabelTertiary, a.T("automation.inactive"))
					})
				}
			})
			if task.Prompt != "" {
				nativeText(c, design.TypeXS, t.LabelSecondary,
					automationPromptSnippet(task.Prompt)).MaxLines(1).Ellipsis("…")
			}
			nativeText(c, design.TypeXXS, t.LabelTertiary, a.automationTaskDetail(task)).
				MaxLines(1).Ellipsis("…")
			// Latest run: status + how long ago, with the failure reason on
			// the card so a broken automation is visible without opening the
			// history (PD-06).
			latest := a.automationLatestRun(task.ID)
			runColor := t.LabelTertiary
			if latest != nil {
				switch latest.Status {
				case store.AutomationRunFailed:
					runColor = t.DangerText
				case store.AutomationRunRunning, store.AutomationRunCompleted:
					runColor = t.LabelSecondary
				}
			}
			nativeText(c, design.TypeXXS, runColor, a.automationRunSummary(latest)).
				MaxLines(1).Ellipsis("…")
			if latest != nil && strings.TrimSpace(latest.Error) != "" {
				nativeText(c, design.TypeXXXS, t.DangerText,
					a.automationFailureText(latest.Error)).MaxLines(2).Ellipsis("…")
			}
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
		a.automationAction(c, t, a.mapLiteralHistoryLabel(), func() { a.toggleAutomationRuns(task.ID) })
		if task.Enabled {
			a.automationAction(c, t, a.T("automation.disable"), func() { a.toggleAutomation(c, task) })
		} else {
			a.automationAction(c, t, a.T("automation.enable"), func() { a.toggleAutomation(c, task) })
		}
		a.automationAction(c, t, a.T("automation.edit"), func() { a.beginAutomationEdit(task) })
		a.automationDangerAction(c, t, a.T("automation.delete"), func() {
			a.beginAutomationDelete(task)
		})
	}
	card := func(el ui.Element) ui.Element {
		return el.Padding(th.Space(3)).Gap(th.Space(3)).Radius(design.RadiusLG).
			Background(t.BgRaised).Border(1, t.BorderL2)
	}
	ui.Column(c).Gap(th.Space(1)).Children(func() {
		if stacked {
			card(ui.Column(c)).Children(func() {
				ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
					title()
					sw()
				})
				ui.Row(c).Gap(th.Space(1)).Wrap().AlignItems(ui.Center).Children(actions)
			})
		} else {
			card(ui.Row(c)).AlignItems(ui.Center).Children(func() {
				title()
				sw()
				// The action strip sits to the right of the switch on the
				// one-line layout; the gap comes from the card's own theme gap.
				ui.Row(c).Gap(th.Space(1)).Shrink(0).Children(actions)
			})
		}
		a.automationRunHistory(c, t, task)
	})
}

// mapLiteralHistoryLabel is the row action's label: "History" in English,
// "历史" in Chinese.
func (a *App) mapLiteralHistoryLabel() string {
	return a.menuText("History", "历史")
}

// toggleAutomationRuns expands or collapses one task's run history.
func (a *App) toggleAutomationRuns(taskID string) {
	st := a.shellUI()
	if st.automationRunsOpen == nil {
		st.automationRunsOpen = make(map[string]bool)
	}
	st.automationRunsOpen[taskID] = !st.automationRunsOpen[taskID]
}

// automationRunHistory draws the last runs of one task when its history is
// expanded: status, start time, duration and the failure reason.
func (a *App) automationRunHistory(c *ui.Context, t *design.Tokens, task store.Automation) {
	st := a.shellUI()
	if !st.automationRunsOpen[task.ID] || a.Ctrl == nil || a.Ctrl.Store() == nil {
		return
	}
	runs, err := a.Ctrl.Store().ListAutomationRuns(task.ID, 10)
	if err != nil {
		nativeText(c, design.TypeXXS, t.Danger, a.menuText("Could not load the run history", "无法加载运行历史")).
			PaddingX(t.Theme().Space(3))
		return
	}
	ui.Column(c).Gap(t.Theme().Space(1)).Padding(t.Theme().Space(2), t.Theme().Space(3)).
		Radius(design.RadiusMD).Background(t.BgStrong).Children(func() {
		if len(runs) == 0 {
			nativeText(c, design.TypeXXS, t.LabelTertiary, a.menuText("No runs recorded yet", "尚无运行记录"))
			return
		}
		for _, run := range runs {
			r := run
			line := a.automationRunLabel(r.Status)
			if when := a.sessionWhen(r.StartedAt); when != "" {
				line += " · " + when
			}
			if r.FinishedAt != nil {
				line += " · " + a.menuText("finished", "完成于") + " " + a.sessionWhen(*r.FinishedAt)
			}
			ui.Row(c).Gap(t.Theme().Space(2)).AlignItems(ui.Center).Children(func() {
				statusColor := t.LabelSecondary
				switch r.Status {
				case store.AutomationRunFailed:
					statusColor = t.DangerText
				case store.AutomationRunCompleted:
					statusColor = t.Success
				}
				nativeText(c, design.TypeXXS, statusColor, line).Grow(1).MaxLines(2)
				if r.SessionID != "" {
					label := a.menuText("Open session", "打开会话")
					if ui.Button(c, label).Height(t.Theme().Space(7)).Label(label).Clicked() {
						a.openSessionAsync(c, r.SessionID)
					}
				}
			})
			if strings.TrimSpace(r.Error) != "" {
				nativeText(c, design.TypeXXXS, t.DangerText,
					a.automationFailureText(r.Error)).MaxLines(2).Ellipsis("…")
			}
		}
	})
}

// automationAction is one native row action. mygo's Button gives the action
// the theme's control look, focus ring and keyboard activation; the minimum
// width keeps every label a comfortable click target in the dense row.
func (a *App) automationAction(c *ui.Context, t *design.Tokens, label string, fn func()) {
	button := ui.Button(c, label).Label(label).MinWidth(design.Space1 * 11)
	button.OnClick(fn)
}

// automationDangerAction is a destructive row action: the same native button
// as the other row actions, painted in the theme's danger color so Delete is
// not visually identical to Run now, Enable or Edit.
func (a *App) automationDangerAction(c *ui.Context, t *design.Tokens, label string, fn func()) {
	button := ui.Button(c, label).Label(label).MinWidth(design.Space1 * 11).
		TextColor(t.Danger)
	button.OnClick(fn)
}

// automationScheduleLabel is the human-readable schedule line, localized to
// the UI language: the runtime's automation.Describe is English-only and the
// Chinese UI used to show "Every hour" and "Daily at 00:00" verbatim. The
// scheduling itself is untouched: this only renders the persisted cron.
func (a *App) automationScheduleLabel(spec string) string {
	schedule, err := automation.Parse(spec)
	if err != nil {
		// Invalid schedules are shown unchanged, as automation.Describe does.
		return spec
	}
	switch schedule.Kind() {
	case automation.KindEveryMinute:
		if n := schedule.Every(); n == 1 {
			return a.T("automation.every_minute")
		}
		return a.T("automation.every_n_minutes", schedule.Every())
	case automation.KindHourly:
		return a.T("automation.every_hour")
	case automation.KindDaily:
		hour, minute, ok := schedule.HourMinute()
		if !ok {
			return spec
		}
		return a.T("automation.daily_at", fmt.Sprintf("%02d:%02d", hour, minute))
	default:
		return spec
	}
}

// automationTaskDetail is the schedule and next-run line.
func (a *App) automationTaskDetail(task store.Automation) string {
	parts := []string{a.automationScheduleLabel(task.Cron)}
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

// automationEmptyLive is the empty state: the brand clock mark, the hint and
// a native create action, all on the page column instead of the retired fixed
// 48 DIP empty-state padding.
func (a *App) automationEmptyLive(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	label := a.T("automation.new_automation_task")
	ui.Column(c).Padding(th.Space(12), 0).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, automationClockIcon).Size(th.Space(7), th.Space(7)).
			TextColor(t.LabelTertiary).Margin(0, 0, th.Space(3), 0)
		nativeText(c, design.TypeBody, t.LabelSecondary, a.T("automation.empty")).
			Margin(0, 0, th.Space(3), 0)
		ui.Button(c, label).Label(label).OnClick(func() { a.beginAutomationAdd() })
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
		workspaceID: task.WorkspaceID, permission: task.Permission, model: task.Model,
	}
	st.automationDialog = true
}

// automationDialog is the create/edit form on mygo's native Modal. The field
// order is title, prompt, schedule, enabled, actions, which keeps the dialog's
// tab order stable for keyboard users.
func (a *App) automationDialog(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	ui.Modal(c, &st.automationDialog, func() {
		th := t.Theme()
		ui.Column(c).Width(th.Space(130)).Gap(th.Space(2)).Children(func() {
			title := a.T("automation.new_automation_task")
			if st.automationDraft.id != "" {
				title = a.T("automation.edit_automation_task")
			}
			nativeText(c, design.TypeM, t.LabelPrimary, title).FontWeight(600)

			nativeText(c, design.TypeXXS, t.LabelTertiary, a.T("automation.title"))
			ui.TextInput(c, &st.automationDraft.title).Label(a.T("automation.title")).
				Placeholder(a.T("automation.daily_report")).AutoFocus()

			nativeText(c, design.TypeXXS, t.LabelTertiary, a.T("automation.prompt"))
			ui.TextArea(c, &st.automationDraft.prompt).Label(a.T("automation.prompt")).
				Placeholder(a.T("automation.summarize_yesterday_s_changes")).
				Height(th.Space(23))

			nativeText(c, design.TypeXXS, t.LabelTertiary, a.T("automation.schedule"))
			ui.TextInput(c, &st.automationDraft.cron).Label(a.T("automation.schedule")).
				Placeholder("*/15 * * * *")
			nativeText(c, design.TypeXXXS, t.LabelTertiary, a.T("automation.cron_15_0_9_hourly_or_daily"))

			// Run binding: which workspace the run's session is created in,
			// its access mode and its model. Empty means the app default.
			a.automationWorkspaceField(c, t)
			a.automationPermissionField(c, t)
			a.automationModelField(c, t)

			ui.Checkbox(c, &st.automationDraft.enabled, a.T("automation.enabled"))

			ui.Row(c).Justify(ui.End).Gap(th.Space(2)).Padding(th.Space(2), 0, 0, 0).
				Children(func() {
					if ui.Button(c, a.T("automation.cancel")).Clicked() {
						st.automationDialog = false
					}
					if ui.PrimaryButton(c, a.T("automation.save")).Clicked() {
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
		ID:          draft.id,
		Title:       title,
		Prompt:      strings.TrimSpace(draft.prompt),
		Cron:        cron,
		Enabled:     draft.enabled,
		WorkspaceID: strings.TrimSpace(draft.workspaceID),
		Permission:  strings.TrimSpace(draft.permission),
		Model:       strings.TrimSpace(draft.model),
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

// beginAutomationDelete opens the delete confirmation for task. Deleting is
// never one click: the row's Delete only asks, and the modal's Danger button
// carries the irreversible action.
func (a *App) beginAutomationDelete(task store.Automation) {
	a.automationDeleteID = task.ID
	a.automationDeleteTitle = task.Title
	a.automationDeleteOpen = true
	a.shellUI().automationDialog = false
	a.closeSelectors()
}

// automationDeleteDialog confirms deleting an automation task. It reuses the
// session/workspace delete modal pattern: cancel (focused first, so Enter
// never deletes by accident), Escape or a click outside dismiss without
// touching the store, and only the Danger button deletes.
func (a *App) automationDeleteDialog(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Modal(c, &a.automationDeleteOpen, func() {
		ui.Column(c).Width(th.Space(105)).Gap(th.Space(3)).Children(func() {
			nativeText(c, design.TypeM, t.LabelPrimary, a.T("automation.delete_automation_task")).
				FontWeight(600)
			nativeText(c, design.TypeXS, t.LabelSecondary,
				fmt.Sprintf(a.T("automation.delete_named"), a.automationDeleteTitle)).MaxLines(3)
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("automation.cancel")).AutoFocus().Clicked() {
					a.automationDeleteOpen = false
				}
				if ui.Button(c, a.T("automation.delete_task")).TextColor(t.Danger).Clicked() {
					a.confirmAutomationDelete(c)
				}
			})
		})
	})
}

// confirmAutomationDelete removes the task after the confirmation. A failed
// delete keeps the dialog open and reports through the toast, so the user can
// retry or cancel instead of losing the target silently.
func (a *App) confirmAutomationDelete(c *ui.Context) {
	if a.Ctrl == nil {
		a.automationDeleteOpen = false
		return
	}
	if err := a.Ctrl.Automations().Delete(a.automationDeleteID); err != nil {
		c.Toast(a.T("automation.could_not_delete_automation_task"))
		return
	}
	a.automationDeleteOpen = false
	a.automationDeleteID = ""
	a.automationDeleteTitle = ""
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

// automationWorkspaceField is the run's workspace selector: the current
// default when empty, otherwise one of the registered workspaces.
func (a *App) automationWorkspaceField(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	label := a.menuText("Default workspace", "默认工作区")
	options := []settingsOption{{
		label: label, selected: st.automationDraft.workspaceID == "",
		choose: func() { st.automationDraft.workspaceID = "" },
		desc:   a.menuText("Use the workspace the app has selected.", "使用应用当前选中的工作区。"),
	}}
	if a.Ctrl != nil {
		if workspaces, err := a.Ctrl.ListWorkspaces(context.Background()); err == nil {
			for _, ws := range workspaces {
				workspace := ws
				options = append(options, settingsOption{
					label:    workspace.Name,
					selected: st.automationDraft.workspaceID == workspace.ID,
					choose:   func() { st.automationDraft.workspaceID = workspace.ID },
					desc:     workspace.Root,
				})
			}
		}
	}
	current := label
	for _, option := range options {
		if option.selected {
			current = option.label
			break
		}
	}
	nativeText(c, design.TypeXXS, t.LabelTertiary, a.menuText("Workspace", "工作区"))
	a.settingsSelectorLiveNamed(c, t,
		a.menuText("Run workspace, current: ", "运行工作区，当前：")+current,
		current, &st.automationWorkspaceOpen, options)
}

// automationPermissionField is the run's access mode: the four UI spellings
// the app normalizes when it executes the task.
func (a *App) automationPermissionField(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	type perm struct{ label, value, desc string }
	perms := []perm{
		{a.T("settings.read_only"), "read-only", a.menuText("Read tools only.", "仅使用读取类工具。")},
		{a.T("settings.workspace_write"), "workspace", a.menuText("Write inside the workspace.", "可在工作区内写入。")},
		{a.menuText("Auto review", "自动审查"), "auto-review", a.menuText("The model reviews mutating calls.", "由模型审查修改类调用。")},
		{a.T("settings.full_access"), "full", a.menuText("No approval gate.", "不过审批。")},
	}
	current := a.menuText("Default", "默认")
	options := make([]settingsOption, 0, len(perms)+1)
	options = append(options, settingsOption{
		label: a.menuText("Default permission", "默认权限"), selected: st.automationDraft.permission == "",
		choose: func() { st.automationDraft.permission = "" },
		desc:   a.menuText("Use the app's default access mode.", "使用应用默认访问模式。"),
	})
	if st.automationDraft.permission == "" {
		current = a.menuText("Default permission", "默认权限")
	}
	for _, p := range perms {
		p := p
		options = append(options, settingsOption{
			label: p.label, selected: st.automationDraft.permission == p.value,
			choose: func() { st.automationDraft.permission = p.value }, desc: p.desc,
		})
		if st.automationDraft.permission == p.value {
			current = p.label
		}
	}
	nativeText(c, design.TypeXXS, t.LabelTertiary, a.menuText("Permission", "权限"))
	a.settingsSelectorLiveNamed(c, t,
		a.menuText("Run permission, current: ", "运行权限，当前：")+current,
		current, &st.automationPermissionOpen, options)
}

// automationModelField is the run's model: the app default or one of the
// configured models.
func (a *App) automationModelField(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	current := a.menuText("Default model", "默认模型")
	options := []settingsOption{{
		label: current, selected: st.automationDraft.model == "",
		choose: func() { st.automationDraft.model = "" },
		desc:   a.menuText("Use the default model.", "使用默认模型。"),
	}}
	if a.Ctrl != nil {
		if models, err := a.Ctrl.Models().List(); err == nil {
			for _, model := range models {
				m := model
				name := modelDisplayName(m)
				options = append(options, settingsOption{
					label: name, selected: st.automationDraft.model == m.ID,
					choose: func() { st.automationDraft.model = m.ID }, desc: m.Model,
				})
				if st.automationDraft.model == m.ID {
					current = name
				}
			}
		}
	}
	nativeText(c, design.TypeXXS, t.LabelTertiary, a.menuText("Model", "模型"))
	a.settingsSelectorLiveNamed(c, t,
		a.menuText("Run model, current: ", "运行模型，当前：")+current,
		current, &st.automationModelOpen, options)
}

// automationLatestRun is the newest recorded run of one task, or nil.
func (a *App) automationLatestRun(taskID string) *store.AutomationRun {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return nil
	}
	run, err := a.Ctrl.Store().LatestAutomationRun(taskID)
	if err != nil {
		return nil
	}
	return run
}

// automationFailureText maps the runtime's English failure reasons to readable
// text in the UI language. Reasons without a known mapping stay as recorded:
// inventing a translation for an unknown error would hide the real cause.
func (a *App) automationFailureText(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "no agent configured"),
		strings.Contains(lower, "no model configured"),
		strings.Contains(lower, "model is not configured"):
		return a.menuText("No model is configured yet; configure a model in Settings.",
			"尚未配置模型；请先在设置中配置模型。")
	case strings.Contains(lower, "context canceled"),
		strings.Contains(lower, "turn canceled"),
		strings.Contains(lower, "context deadline exceeded"):
		return a.menuText("The run was canceled.", "运行已取消。")
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"):
		return a.menuText("The provider rate limit was hit.", "触及服务商限流。")
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"),
		strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication"):
		return a.menuText("The provider rejected the credentials.", "服务商拒绝了凭据。")
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "network is unreachable"):
		return a.menuText("The provider is unreachable.", "无法连接到服务商。")
	default:
		return reason
	}
}

// automationRunLabel localizes one run's status.
func (a *App) automationRunLabel(status string) string {
	switch status {
	case store.AutomationRunRunning:
		return a.menuText("Running", "运行中")
	case store.AutomationRunCompleted:
		return a.menuText("Completed", "已完成")
	case store.AutomationRunFailed:
		return a.menuText("Failed", "失败")
	case store.AutomationRunStopped:
		return a.menuText("Stopped", "已停止")
	default:
		return status
	}
}

// automationRunSummary is the one-line latest-run state a task card shows.
func (a *App) automationRunSummary(run *store.AutomationRun) string {
	if run == nil {
		return a.menuText("Never ran", "尚未运行")
	}
	label := a.automationRunLabel(run.Status)
	if when := a.sessionWhen(run.StartedAt); when != "" {
		label += " · " + when
	}
	return label
}
