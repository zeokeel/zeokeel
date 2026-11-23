package session_test

// TDD frame tests for the current-session reminders: the conversation ⋯ menu
// gains 创建提醒… / 本会话提醒（N） only when the automation backend exists, the
// create dialog builds an after/daily/at schedule (quick times plus an optional
// exact instant) and creates an AutomationTask bound to the active session, and
// the list dialog shows only this session's tasks with schedule, next run, an
// enable switch, confirmed deletion and a jump to the automation screen.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uikit"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeAutomationService is the automation backend the reminder surfaces read
// and write: the tasks are scripted and every mutation is recorded.
type fakeAutomationService struct {
	mu      sync.Mutex
	tasks   []contracts.AutomationTask
	creates []contracts.AutomationTask
	updates []contracts.AutomationTask
	deletes []string
	listErr error
	nextID  int
}

var _ contracts.AutomationService = (*fakeAutomationService)(nil)

func (f *fakeAutomationService) List(context.Context) ([]contracts.AutomationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]contracts.AutomationTask(nil), f.tasks...), nil
}

func (f *fakeAutomationService) Create(_ context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	task.ID = "reminder-" + string(rune('0'+f.nextID))
	task.Name = "自动化 " + string(rune('0'+f.nextID))
	f.creates = append(f.creates, task)
	f.tasks = append(f.tasks, task)
	return task, nil
}

func (f *fakeAutomationService) Update(_ context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, task)
	for index := range f.tasks {
		if f.tasks[index].ID == task.ID {
			f.tasks[index] = task
		}
	}
	return task, nil
}

func (f *fakeAutomationService) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, id)
	for index := range f.tasks {
		if f.tasks[index].ID == id {
			f.tasks = append(f.tasks[:index], f.tasks[index+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeAutomationService) RunNow(context.Context, string) error { return nil }

func (f *fakeAutomationService) Subscribe(func()) (func(), error) { return func() {}, nil }

func (f *fakeAutomationService) created() []contracts.AutomationTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.AutomationTask(nil), f.creates...)
}

func (f *fakeAutomationService) updated() []contracts.AutomationTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.AutomationTask(nil), f.updates...)
}

func (f *fakeAutomationService) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// newReminderHarness activates the session screen with the optional automation
// backend and notifier.
func newReminderHarness(t *testing.T, chat *fakeChat, automation contracts.AutomationService,
	notifier contracts.Notifier) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, uinative.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if automation != nil {
		provide[contracts.AutomationService](t, ctx, contracts.KeyAutomation, automation)
	}
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// reminderChat is one active conversation the reminders are bound to.
func reminderChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "提醒会话", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: contracts.ChatRoleUser, Text: "帮我盯着发布"},
	}}, true)
	return chat
}

// reminderNotifierHasText reports whether one recorded toast contains text.
func reminderNotifierHasText(notifier *fakeSessionNotifier, want string) bool {
	for _, toast := range notifier.toasts() {
		if strings.Contains(toast.text, want) {
			return true
		}
	}
	return false
}

// reminderTask is one scripted task bound to a conversation.
func reminderTask(id, sessionID, name, schedule string, enabled bool, nextRun time.Time) contracts.AutomationTask {
	return contracts.AutomationTask{
		ID: id, SessionID: sessionID, Name: name, Prompt: name,
		Schedule: schedule, Enabled: enabled, NextRun: nextRun,
	}
}

// openCreateReminder opens the create dialog from the conversation menu.
func openCreateReminder(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	openConversationActions(t, h, tester)
	h.assertTexts(tester, "创建提醒…")
	h.click(tester, "创建提醒…")
	tester.Frame()
	h.assertTexts(tester, "提醒内容", "30 分钟后", "明天 09:00", "每天 09:00")
}

// typeReminderPrompt types into the create dialog's prompt field.
func typeReminderPrompt(t *testing.T, h *harness, tester *ui.Tester, prompt string) {
	t.Helper()
	input, ok := tester.Find("提醒内容")
	if !ok {
		t.Fatalf("the reminder prompt field is missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type(prompt)
	tester.Frame()
}

// TestReminderMenuHiddenWithoutBackend covers the boundary: a product without
// the automation backend shows no reminder entries at all.
func TestReminderMenuHiddenWithoutBackend(t *testing.T) {
	h := newReminderHarness(t, reminderChat(), nil, nil)
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	h.assertTexts(tester, "导出 Markdown")
	h.assertNoTexts(tester, "创建提醒", "本会话提醒")
}

// TestReminderCreateDefaultsToThirtyMinutes covers the create path: the menu
// entry opens the native dialog, and creating with the default quick time
// registers an AutomationTask bound to the active session with an after
// schedule and a success toast.
func TestReminderCreateDefaultsToThirtyMinutes(t *testing.T) {
	automation := &fakeAutomationService{}
	notifier := &fakeSessionNotifier{}
	h := newReminderHarness(t, reminderChat(), automation, notifier)
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	h.assertTexts(tester, "创建提醒…", "本会话提醒（0）")
	h.click(tester, "创建提醒…")
	tester.Frame()
	h.assertTexts(tester, "提醒内容", "30 分钟后", "明天 09:00", "每天 09:00")

	typeReminderPrompt(t, h, tester, "检查发布清单")
	h.click(tester, "创建")
	tester.Frame()

	created := automation.created()
	if len(created) != 1 {
		t.Fatalf("Create calls = %d, want 1", len(created))
	}
	task := created[0]
	if task.SessionID != "sess-1" {
		t.Fatalf("created SessionID = %q, want sess-1 (the active conversation)", task.SessionID)
	}
	if task.Prompt != "检查发布清单" {
		t.Fatalf("created Prompt = %q, want the typed prompt", task.Prompt)
	}
	if task.Schedule != "after 1800s" {
		t.Fatalf("created Schedule = %q, want after 1800s (30 minutes)", task.Schedule)
	}
	if !task.Enabled {
		t.Fatal("a reminder was created disabled: it would never fire")
	}
	// The dialog closed and the success toast was raised.
	h.assertNoTexts(tester, "提醒内容")
	if !reminderNotifierHasText(notifier, "提醒已创建") {
		t.Fatalf("success toast missing; toasts = %v", notifier.toasts())
	}
}

// TestReminderCreateQuickTimesAndCustomAt covers the schedule choices: the
// quick segmented times and the optional exact instant all resolve to the
// contract's after/daily/at forms.
func TestReminderCreateQuickTimesAndCustomAt(t *testing.T) {
	now := time.Now()
	today := now.In(contracts.AutomationDefaultLocation())

	t.Run("tomorrow at nine", func(t *testing.T) {
		automation := &fakeAutomationService{}
		h := newReminderHarness(t, reminderChat(), automation, &fakeSessionNotifier{})
		tester := h.tester()
		tester.Frame()
		openCreateReminder(t, h, tester)
		typeReminderPrompt(t, h, tester, "明日检查")
		h.click(tester, "明天 09:00")
		h.click(tester, "创建")
		tester.Frame()

		created := automation.created()
		if len(created) != 1 {
			t.Fatalf("Create calls = %d, want 1", len(created))
		}
		schedule, err := contracts.ParseAutomationSchedule(created[0].Schedule)
		if err != nil || schedule.Kind() != contracts.AutomationScheduleOnceAt {
			t.Fatalf("created Schedule = %q (%v), want an absolute at schedule", created[0].Schedule, err)
		}
		instant, _ := schedule.At()
		local := instant.In(contracts.AutomationDefaultLocation())
		wantDay := today.AddDate(0, 0, 1)
		if local.Year() != wantDay.Year() || local.Month() != wantDay.Month() || local.Day() != wantDay.Day() ||
			local.Hour() != 9 || local.Minute() != 0 {
			t.Fatalf("tomorrow's reminder fires %v, want %v 09:00", local, wantDay.Format("2006-01-02"))
		}
	})

	t.Run("daily at nine", func(t *testing.T) {
		automation := &fakeAutomationService{}
		h := newReminderHarness(t, reminderChat(), automation, &fakeSessionNotifier{})
		tester := h.tester()
		tester.Frame()
		openCreateReminder(t, h, tester)
		typeReminderPrompt(t, h, tester, "每日检查")
		h.click(tester, "每天 09:00")
		h.click(tester, "创建")
		tester.Frame()

		created := automation.created()
		if len(created) != 1 {
			t.Fatalf("Create calls = %d, want 1", len(created))
		}
		schedule, err := contracts.ParseAutomationSchedule(created[0].Schedule)
		if err != nil || schedule.Kind() != contracts.AutomationScheduleDaily {
			t.Fatalf("created Schedule = %q (%v), want a daily schedule", created[0].Schedule, err)
		}
		hour, minute, _, _, _ := schedule.TimeOfDay()
		if hour != 9 || minute != 0 {
			t.Fatalf("daily schedule fires at %02d:%02d, want 09:00", hour, minute)
		}
	})

	t.Run("exact instant", func(t *testing.T) {
		automation := &fakeAutomationService{}
		h := newReminderHarness(t, reminderChat(), automation, &fakeSessionNotifier{})
		tester := h.tester()
		tester.Frame()
		openCreateReminder(t, h, tester)
		typeReminderPrompt(t, h, tester, "精确提醒")
		atInput, ok := tester.Find("精确时间（可选）")
		if !ok {
			t.Fatalf("the exact-time field is missing; texts = %v", tester.Texts())
		}
		tester.ClickAt(atInput.X+atInput.W/2, atInput.Y+atInput.H/2)
		tester.Frame()
		tester.Type("2030-01-02 09:30")
		tester.Frame()
		h.click(tester, "创建")
		tester.Frame()

		created := automation.created()
		if len(created) != 1 {
			t.Fatalf("Create calls = %d, want 1", len(created))
		}
		schedule, err := contracts.ParseAutomationSchedule(created[0].Schedule)
		if err != nil || schedule.Kind() != contracts.AutomationScheduleOnceAt {
			t.Fatalf("created Schedule = %q (%v), want an absolute at schedule", created[0].Schedule, err)
		}
		instant, _ := schedule.At()
		local := instant.In(contracts.AutomationDefaultLocation())
		if local.Year() != 2030 || local.Month() != time.January || local.Day() != 2 || local.Hour() != 9 || local.Minute() != 30 {
			t.Fatalf("exact-time reminder fires %v, want 2030-01-02 09:30", local)
		}
	})

	t.Run("invalid exact instant", func(t *testing.T) {
		automation := &fakeAutomationService{}
		h := newReminderHarness(t, reminderChat(), automation, &fakeSessionNotifier{})
		tester := h.tester()
		tester.Frame()
		openCreateReminder(t, h, tester)
		typeReminderPrompt(t, h, tester, "无效时间")
		atInput, ok := tester.Find("精确时间（可选）")
		if !ok {
			t.Fatalf("the exact-time field is missing; texts = %v", tester.Texts())
		}
		tester.ClickAt(atInput.X+atInput.W/2, atInput.Y+atInput.H/2)
		tester.Frame()
		tester.Type("下周某个时候")
		tester.Frame()
		h.click(tester, "创建")
		tester.Frame()

		if created := automation.created(); len(created) != 0 {
			t.Fatalf("an unparseable exact time created a task: %+v", created)
		}
		h.assertTexts(tester, "精确时间无法解析")
	})
}

// TestReminderListShowsOnlyThisSession covers the list dialog: only the tasks
// bound to the active conversation appear, with schedule, next run and the
// enable switch.
func TestReminderListShowsOnlyThisSession(t *testing.T) {
	next := time.Date(2026, 12, 1, 9, 0, 0, 0, contracts.AutomationDefaultLocation())
	automation := &fakeAutomationService{tasks: []contracts.AutomationTask{
		reminderTask("r1", "sess-1", "每日发布检查", "daily 09:00:00", true, next),
		reminderTask("r2", "sess-1", "明日提醒", "at 2026-12-02T01:00:00.000Z", false, time.Time{}),
		reminderTask("r3", "sess-2", "别的会话提醒", "after 600s", true, next),
	}}
	h := newReminderHarness(t, reminderChat(), automation, &fakeSessionNotifier{})
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	h.assertTexts(tester, "本会话提醒（2）")
	h.click(tester, "本会话提醒（2）")
	tester.Frame()

	// The absolute "at" instant is rendered in the host's local zone (the same
	// DateTime formatting every other timestamp uses), so CI's UTC clock and a
	// developer's Asia/Shanghai clock agree on the frame.
	onceLabel := uikit.FormatDateTime(h.ui.Locale(),
		time.Date(2026, 12, 2, 1, 0, 0, 0, time.UTC)) + " 运行一次"
	h.assertTexts(tester, "本会话提醒", "每日发布检查", "每天 09:00",
		"明日提醒", onceLabel, "下次运行", "打开自动化页")
	h.assertNoTexts(tester, "别的会话提醒")

	// The enable switch updates the task and keeps its other fields.
	sw, ok := tester.AccessByRole(ui.RoleSwitch, "启用提醒 r1")
	if !ok {
		t.Fatalf("the reminder list has no enable switch; texts = %v", tester.Texts())
	}
	tester.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	tester.Frame()
	updates := automation.updated()
	if len(updates) != 1 || updates[0].ID != "r1" || updates[0].Enabled {
		t.Fatalf("updates after disabling = %+v, want r1 disabled", updates)
	}

	// Deletion asks first, then removes the task.
	h.click(tester, "删除提醒 r1")
	tester.Frame()
	h.assertTexts(tester, "删除提醒", "取消", "确认删除")
	h.click(tester, "确认删除")
	tester.Frame()
	if got := automation.deleted(); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("Delete calls = %v, want [r1]", got)
	}
}

// TestReminderListJumpsToAutomationScreen covers the jump: the list dialog's
// action opens the automation screen.
func TestReminderListJumpsToAutomationScreen(t *testing.T) {
	automation := &fakeAutomationService{tasks: []contracts.AutomationTask{
		reminderTask("r1", "sess-1", "每日发布检查", "daily 09:00:00", true, time.Now().Add(time.Hour)),
	}}
	h := newReminderHarness(t, reminderChat(), automation, &fakeSessionNotifier{})
	// The automation screen is contributed by its own module; the test registers
	// a stand-in under the stable screen ID.
	if _, err := h.ui.Runtime().RegisterScreen(uinative.ScreenSpec{
		ID: "automation", Title: "自动化", Order: 30,
		Build: func(c *ui.Context) { ui.Text(c, "自动化页") },
	}); err != nil {
		t.Fatalf("register automation screen: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	openConversationActions(t, h, tester)
	h.click(tester, "本会话提醒（1）")
	tester.Frame()
	h.click(tester, "打开自动化页")
	tester.Frame()

	if got := h.ui.Navigator().Current(); got != "automation" {
		t.Fatalf("navigator.Current() = %q, want automation", got)
	}
	h.assertTexts(tester, "自动化页")
}

// TestReminderAtInputParsing covers the contract helper the create dialog's
// exact-time field uses.
func TestReminderAtInputParsing(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, contracts.AutomationDefaultLocation())
	location := contracts.AutomationDefaultLocation()
	tests := []struct {
		name  string
		input string
		want  time.Time
	}{
		{
			name:  "local minute form",
			input: "2026-12-01 09:30",
			want:  time.Date(2026, 12, 1, 9, 30, 0, 0, location),
		},
		{
			name:  "local seconds form",
			input: "2026-12-01 09:30:15",
			want:  time.Date(2026, 12, 1, 9, 30, 15, 0, location),
		},
		{
			name:  "RFC3339 with offset",
			input: "2026-12-01T09:30:00+08:00",
			want:  time.Date(2026, 12, 1, 9, 30, 0, 0, location),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := contracts.ParseAutomationAtInput(test.input, now)
			if err != nil {
				t.Fatalf("ParseAutomationAtInput(%q): %v", test.input, err)
			}
			if !got.Equal(test.want) {
				t.Fatalf("ParseAutomationAtInput(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}

	if _, err := contracts.ParseAutomationAtInput("下周某个时候", now); err == nil {
		t.Fatal("ParseAutomationAtInput accepted garbage")
	}
	if _, err := contracts.ParseAutomationAtInput("2026-01-02 09:30", now); err == nil {
		t.Fatal("ParseAutomationAtInput accepted a past instant")
	}
	if !strings.HasPrefix(contracts.AutomationScheduleAfter(30*time.Minute), "after ") {
		t.Fatal("AutomationScheduleAfter does not use the after selector")
	}
}
