package session

// Current-session reminders: the conversation ⋯ menu's 创建提醒… / 本会话提醒（N）
// entries and their native dialogs.
//
// A reminder is an automation task bound to the conversation on screen
// (AutomationTask.SessionID): when its schedule fires, the automation backend
// delivers the prompt back into that conversation. The product does not add a
// second scheduler: the create dialog only builds the existing schedule strings
// (after / daily / at, see contracts.AutomationSchedule*), and the list manages
// the same persisted task set the automation screen owns.
//
// Both entries appear only when the automation backend is provided (headless or
// stripped products keep the previous menu). The list reads the backend's
// in-memory snapshot synchronously (the automation screen reads the same List
// in its frame), so a frame never waits on a backend round trip.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// automationScreenID is the automation screen's stable ID. The ui/automation
// module owns the screen; a UI module may not import another module's
// implementation, so the host navigation contract is repeated here (like the
// session screen ID in ui/approval).
const automationScreenID = "automation"

// reminderQuickChoices are the create dialog's quick times, in order. The
// schedule strings they resolve to are built per frame (tomorrow moves with the
// calendar; a dialog left open overnight must not remind "tomorrow" of
// yesterday).
var reminderQuickChoices = []struct {
	zh, en string
}{
	{"30 分钟后", "In 30 minutes"},
	{"明天 09:00", "Tomorrow 09:00"},
	{"每天 09:00", "Daily 09:00"},
}

// reminderState is the current-session reminder surfaces' UI-thread state: the
// create dialog (prompt, quick-time choice, optional exact instant), the list
// dialog's list/scroll state and the destructive delete confirmation.
type reminderState struct {
	createOpen   bool
	createFocus  bool
	createInput  ui.Handle
	createPrompt string
	// createChoice is the quick-time choice (0=30 分钟后, 1=明天 09:00,
	// 2=每天 09:00). createAtText overrides it when non-empty.
	createChoice int
	createAtText string
	createErr    string

	listOpen   bool
	listList   ui.ListState
	listScroll ui.ScrollState

	// deleteID/deleteName are the reminder awaiting the destructive
	// confirmation (ConfirmDanger's safe action is the Default).
	deleteID   string
	deleteName string
}

// automationService returns the optional automation backend the reminder
// entries and dialogs read. A host without it shows no reminder entries.
func (st *state) automationService() contracts.AutomationService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyAutomation)
	if !ok || raw == nil {
		return nil
	}
	service, _ := raw.(contracts.AutomationService)
	return service
}

// sessionReminderTasks returns the scheduled tasks bound to one conversation.
// The automation backend keeps the task set in memory (its screen reads the
// same List synchronously), so the menu count and the dialog rows are an
// in-memory read, never a frame-blocking round trip.
func (st *state) sessionReminderTasks(sessionID string) ([]contracts.AutomationTask, error) {
	service := st.automationService()
	sessionID = strings.TrimSpace(sessionID)
	if service == nil || sessionID == "" {
		return nil, nil
	}
	tasks, err := service.List(context.Background())
	if err != nil {
		return nil, err
	}
	bound := make([]contracts.AutomationTask, 0, len(tasks))
	for _, task := range tasks {
		if strings.TrimSpace(task.SessionID) == sessionID {
			bound = append(bound, task)
		}
	}
	return bound, nil
}

// openReminderCreate resets and opens the create dialog.
func (st *state) openReminderCreate() {
	if st == nil {
		return
	}
	st.reminders.createOpen = true
	st.reminders.createFocus = true
	st.reminders.createPrompt = ""
	st.reminders.createChoice = 0
	st.reminders.createAtText = ""
	st.reminders.createErr = ""
}

// reminderSchedule resolves the create dialog's choice into a canonical
// schedule string (contracts' after/daily/at forms; there is deliberately no
// cron editor here). Empty or unparseable exact times return a localized error.
func (st *state) reminderSchedule(now time.Time) (string, error) {
	if atText := strings.TrimSpace(st.reminders.createAtText); atText != "" {
		instant, err := contracts.ParseAutomationAtInput(atText, now)
		if err != nil {
			return "", fmt.Errorf("%s", st.tr(
				"精确时间无法解析：请填写 2026-12-01 09:30 或 RFC 3339，且需晚于现在。",
				"Could not parse the exact time: use 2026-12-01 09:30 or RFC 3339, later than now."))
		}
		return contracts.AutomationScheduleAt(instant), nil
	}
	switch st.reminders.createChoice {
	case 1:
		location := contracts.AutomationDefaultLocation()
		tomorrow := now.In(location).AddDate(0, 0, 1)
		instant := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, location)
		return contracts.AutomationScheduleAt(instant), nil
	case 2:
		return contracts.AutomationScheduleDailyAt(9, 0), nil
	default:
		return contracts.AutomationScheduleAfter(30 * time.Minute), nil
	}
}

// createReminderFromDialog validates and creates the reminder through the
// automation backend, scoped to the conversation on screen.
func (st *state) createReminderFromDialog(runtime uinative.UIRuntime,
	chat contracts.ChatService, now time.Time) {
	service := st.automationService()
	sessionID := ""
	if chat != nil {
		sessionID = strings.TrimSpace(chat.ActiveSession())
	}
	prompt := strings.TrimSpace(st.reminders.createPrompt)
	switch {
	case service == nil:
		st.reminders.createErr = st.tr("提醒不可用：自动化后端未启用。",
			"Reminders are unavailable: the automation backend is not enabled.")
		return
	case sessionID == "":
		st.reminders.createErr = st.tr("无法创建提醒：当前没有会话。",
			"Cannot create the reminder: there is no active conversation.")
		return
	case prompt == "":
		st.reminders.createErr = st.tr("请填写提醒内容。", "Write the reminder first.")
		return
	}
	schedule, err := st.reminderSchedule(now)
	if err != nil {
		st.reminders.createErr = err.Error()
		runtime.Invalidate()
		return
	}
	task := contracts.AutomationTask{
		SessionID: sessionID,
		Prompt:    prompt,
		Schedule:  schedule,
		// A reminder exists to fire: the backend would otherwise store it
		// disabled with no next run.
		Enabled: true,
	}
	if _, err := service.Create(context.Background(), task); err != nil {
		st.reminders.createErr = st.tr("创建提醒失败：", "Failed to create the reminder: ") + err.Error()
		runtime.Invalidate()
		return
	}
	st.reminders.createOpen = false
	st.reminders.createFocus = false
	st.reminders.createErr = ""
	st.toast(contracts.NotifySuccess, st.tr("提醒已创建", "Reminder created"))
	runtime.Invalidate()
}

// buildReminderDialogs renders the create dialog, the session reminder list and
// the destructive delete confirmation. It is called from the screen build so
// the dialogs live above the whole conversation.
func buildReminderDialogs(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime,
	st *state, chat contracts.ChatService) {
	buildReminderCreateDialog(c, t, runtime, st, chat)
	buildReminderListDialog(c, t, runtime, st, chat)
	buildReminderDeleteConfirm(c, runtime, st)
}

// buildReminderCreateDialog is the native create-reminder modal: the prompt,
// the quick-time segmented choice, an optional exact instant and the footer.
func buildReminderCreateDialog(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime,
	st *state, chat contracts.ChatService) {
	if st == nil || !st.reminders.createOpen {
		return
	}
	if st.automationService() == nil {
		st.reminders.createOpen = false
		return
	}
	create := func() { st.createReminderFromDialog(runtime, chat, time.Now()) }
	ui.Modal(c, &st.reminders.createOpen, func() {
		dialogEnter(c, create)
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("创建提醒", "Create reminder")), t.Font(design.TypeL)).
				FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr(
				"到点后自动把提醒发送到当前会话；提醒会保留在自动化页面，可随时启停或删除。",
				"The reminder is delivered into this conversation when it fires; it stays on the automation page, where it can be enabled, disabled or deleted.")),
				t.TextMuted(design.TypeXS)).MaxLines(3)

			ui.TextInput(c, &st.reminders.createPrompt).
				Label(st.tr("提醒内容", "Reminder")).
				Placeholder(st.tr("例如：检查发布清单", "For example: check the release checklist")).
				FillWidth().Bind(&st.reminders.createInput).
				OnTextKey(enterTo(create))
			if st.reminders.createFocus {
				st.reminders.createFocus = false
				st.reminders.createInput.Focus(c)
			}

			ui.Field(c, st.tr("提醒时间", "Reminder time"), func() {
				labels := make([]string, 0, len(reminderQuickChoices))
				for _, choice := range reminderQuickChoices {
					labels = append(labels, st.tr(choice.zh, choice.en))
				}
				uikit.SegmentedControl(c, uikit.SegmentedOptions{
					Key:      "session.reminder.time",
					Label:    st.tr("提醒时间", "Reminder time"),
					Labels:   labels,
					Selected: &st.reminders.createChoice,
				})
			})
			ui.TextInput(c, &st.reminders.createAtText).
				Label(st.tr("精确时间（可选）", "Exact time (optional)")).
				Placeholder(st.tr("2026-12-01 09:30 或 RFC 3339",
					"2026-12-01 09:30 or RFC 3339")).
				FillWidth().OnTextKey(enterTo(create))
			if st.reminders.createErr != "" {
				buildDialogError(c, t, st.reminders.createErr)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Spacer(c)
				cancel := design.Apply(ui.Button(c, st.tr("取消", "Cancel")), t.SecondaryButton())
				if cancel.Clicked() {
					st.reminders.createOpen = false
					st.reminders.createErr = ""
					runtime.Invalidate()
				}
				createButton := design.Apply(ui.PrimaryButton(c, st.tr("创建", "Create")),
					t.PrimaryButton())
				createButton.Disabled(strings.TrimSpace(st.reminders.createPrompt) == "")
				if createButton.Clicked() && strings.TrimSpace(st.reminders.createPrompt) != "" {
					create()
					runtime.Invalidate()
				}
			})
		})
	})
}

// buildReminderListDialog is the session reminders list: only the tasks bound
// to the conversation on screen, each with its schedule, next run, enable
// switch and delete action, plus the jump to the automation page.
func buildReminderListDialog(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime,
	st *state, chat contracts.ChatService) {
	if st == nil || !st.reminders.listOpen {
		return
	}
	service := st.automationService()
	if service == nil {
		st.reminders.listOpen = false
		return
	}
	sessionID := ""
	if chat != nil {
		sessionID = strings.TrimSpace(chat.ActiveSession())
	}
	tasks, listErr := st.sessionReminderTasks(sessionID)
	ui.Modal(c, &st.reminders.listOpen, func() {
		design.Apply(ui.Column(c).Width(design.DialogWidthL).Gap(design.Space3), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, st.tr("本会话提醒", "Session reminders")),
					t.Font(design.TypeL)).FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
				ui.Spacer(c)
				design.Apply(ui.Text(c, fmt.Sprintf(st.tr("%d 条", "%d total"), len(tasks))),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			})
			design.Apply(ui.Text(c, st.tr(
				"这些任务到点后会把提醒发送到当前会话。",
				"These tasks deliver their reminder into this conversation when they fire.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
			if listErr != nil {
				uikit.ErrorLine(c, st.tr("读取提醒失败：", "Failed to read reminders: ")+listErr.Error())
			}
			switch {
			case len(tasks) == 0:
				design.Apply(ui.Text(c, st.tr("还没有本会话提醒。",
					"No reminders for this conversation yet.")),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
			default:
				ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
					TrackScroll(&st.reminders.listScroll).Children(func() {
					ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
						st.reminders.listList.Key = func(index int) any {
							if index < 0 || index >= len(tasks) {
								return nil
							}
							return tasks[index].ID
						}
						st.reminders.listList.Label = func(index int) string {
							if index < 0 || index >= len(tasks) {
								return ""
							}
							return reminderDisplayName(tasks[index])
						}
						ui.List(c, &st.reminders.listList, len(tasks), func(index int) {
							buildReminderRow(c, t, runtime, st, service, tasks[index])
						}).FillWidth().Gap(design.Space2)
					})
				})
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				automation := design.Apply(ui.Button(c, st.tr("打开自动化页", "Open automation page")),
					t.SecondaryButton()).Height(design.RowCompactHeight).Shrink(0)
				if automation.Clicked() {
					st.reminders.listOpen = false
					if st.navigator != nil {
						_ = st.navigator.Push(automationScreenID)
					}
					runtime.Invalidate()
				}
				ui.Spacer(c)
				closeButton := design.Apply(ui.Button(c, st.tr("关闭", "Close")), t.SecondaryButton())
				if closeButton.Clicked() {
					st.reminders.listOpen = false
					runtime.Invalidate()
				}
			})
		})
	})
}

// buildReminderRow is one reminder card: displayed name, schedule label with
// the raw expression as its tooltip, next run, enable switch and delete action.
func buildReminderRow(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state,
	service contracts.AutomationService, task contracts.AutomationTask) {
	ui.Column(c).FillWidth().Gap(design.Space1).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, reminderDisplayName(task)), t.Font(design.TypeXS)).
				FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary).Grow(1).Shrink(1).
				MinWidthPercent(0).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Text(c, st.reminderScheduleLabel(task.Schedule)), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).NoWrap().Tooltip(
				st.tr("原始计划：", "Raw schedule: ") + task.Schedule)
			enabled := task.Enabled
			toggle := ui.Switch(c, &enabled).
				Label(st.tr("启用提醒 ", "Enable reminder ") + task.ID)
			if toggle.Changed() {
				updated := task
				updated.Enabled = enabled
				if _, err := service.Update(context.Background(), updated); err != nil {
					uikit.ErrorLine(c, st.tr("更新提醒失败：", "Failed to update the reminder: ")+err.Error())
					return
				}
				runtime.Invalidate()
			}
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.tr("下次运行：", "Next run: ")+reminderNextRunLabel(st, task.NextRun)),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
			uikit.IconButton(c, uikit.IconButtonOptions{
				Key:     "session.reminder.delete." + task.ID,
				Label:   st.tr("删除提醒 ", "Delete reminder ") + task.ID,
				Tooltip: st.tr("删除这条提醒（需确认）", "Delete this reminder (asks first)"),
				Icon:    icons.Trash(),
				Size:    design.IconSizeS,
				Variant: uikit.IconButtonDanger,
				OnClick: func() {
					st.reminders.deleteID = task.ID
					st.reminders.deleteName = reminderDisplayName(task)
					runtime.Invalidate()
				},
			})
		})
	})
}

// buildReminderDeleteConfirm asks before deleting a reminder: the safe action
// (取消) is the Default; the danger action is explicit (DESIGN.md AP-12).
func buildReminderDeleteConfirm(c *ui.Context, runtime uinative.UIRuntime, st *state) {
	if st == nil || strings.TrimSpace(st.reminders.deleteID) == "" {
		return
	}
	service := st.automationService()
	if service == nil {
		st.reminders.deleteID = ""
		st.reminders.deleteName = ""
		return
	}
	id := st.reminders.deleteID
	name := st.reminders.deleteName
	if name == "" {
		name = id
	}
	open := true
	uikit.ConfirmDanger(c, &open, uikit.ConfirmDangerOptions{
		Key:   "session.reminder.confirm.delete." + id,
		Title: st.tr("删除提醒", "Delete reminder"),
		Message: fmt.Sprintf(st.tr("删除提醒「%s」？到点后将不再发送，且无法撤销。",
			"Delete the reminder %q? It will no longer fire, and this cannot be undone."), name),
		Confirm: st.tr("确认删除", "Delete reminder"),
		Cancel:  st.tr("取消", "Cancel"),
		OnConfirm: func() {
			st.reminders.deleteID = ""
			st.reminders.deleteName = ""
			if err := service.Delete(context.Background(), id); err != nil {
				st.toast(contracts.NotifyError,
					st.tr("删除提醒失败：", "Failed to delete the reminder: ")+err.Error())
			} else {
				st.toast(contracts.NotifySuccess, st.tr("提醒已删除", "Reminder deleted"))
			}
			runtime.Invalidate()
		},
	})
	if !open {
		st.reminders.deleteID = ""
		st.reminders.deleteName = ""
	}
}

// reminderDisplayName is the reminder row's name: the task name, else its
// prompt's first line (the backend names unnamed tasks generically).
func reminderDisplayName(task contracts.AutomationTask) string {
	if name := strings.TrimSpace(task.Name); name != "" {
		return name
	}
	prompt := strings.TrimSpace(task.Prompt)
	if index := strings.IndexAny(prompt, "\r\n"); index >= 0 {
		prompt = strings.TrimSpace(prompt[:index])
	}
	return truncateRunes(prompt, 80)
}

// reminderScheduleLabel renders the human shape of a stored schedule for the
// reminder row: the forms the create dialog builds (after/daily/at) read as the
// product's copy; any other stored expression keeps its canonical string (the
// reminder list is not a cron editor).
func (st *state) reminderScheduleLabel(raw string) string {
	schedule, err := contracts.ParseAutomationSchedule(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	switch schedule.Kind() {
	case contracts.AutomationScheduleOnceAfter:
		seconds := schedule.IntervalSeconds()
		if seconds%60 == 0 {
			return fmt.Sprintf(st.tr("%d 分钟后运行一次", "Once in %d minutes"), seconds/60)
		}
		return fmt.Sprintf(st.tr("%d 秒后运行一次", "Once in %d seconds"), seconds)
	case contracts.AutomationScheduleDaily:
		hour, minute, _, _, ok := schedule.TimeOfDay()
		if !ok {
			return schedule.String()
		}
		return fmt.Sprintf(st.tr("每天 %02d:%02d", "Daily at %02d:%02d"), hour, minute)
	case contracts.AutomationScheduleOnceAt:
		instant, ok := schedule.At()
		if !ok {
			return schedule.String()
		}
		return uikit.FormatDateTime(st.locale, instant) + st.tr(" 运行一次", " (once)")
	default:
		return schedule.String()
	}
}

// reminderNextRunLabel formats a reminder's next firing; an unscheduled task
// says so instead of printing the zero time.
func reminderNextRunLabel(st *state, next time.Time) string {
	if next.IsZero() {
		return st.tr("未排期", "Not scheduled")
	}
	return uikit.FormatDateTime(st.locale, next)
}
