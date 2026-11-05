package shell

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/automation"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/store"
)

// automationLivePage is the Automation screen with real persisted tasks,
// laid out on the brand page column: native header action, segmented filter,
// native search field, task cards and the create/edit dialog.
func (a *App) automationLivePage(c *ui.Context, t *design.Tokens) {
	a.automationHeadingLive(c, t)
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
		a.automationDangerAction(c, t, a.T("automation.delete"), func() {
			a.beginAutomationDelete(task)
		})
	}
	card := func(el ui.Element) ui.Element {
		return el.Padding(th.Space(3)).Gap(th.Space(3)).Radius(design.RadiusLG).
			Background(t.BgRaised).Border(1, t.BorderL2)
	}
	if stacked {
		card(ui.Column(c)).Children(func() {
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				title()
				sw()
			})
			ui.Row(c).Gap(th.Space(1)).Wrap().AlignItems(ui.Center).Children(actions)
		})
		return
	}
	card(ui.Row(c)).AlignItems(ui.Center).Children(func() {
		title()
		sw()
		// The action strip sits to the right of the switch on the one-line
		// layout; the gap comes from the card's own theme gap.
		ui.Row(c).Gap(th.Space(1)).Shrink(0).Children(actions)
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
