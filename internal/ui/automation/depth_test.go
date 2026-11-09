package automation_test

// Depth tests for the second audit batch: the catalog model picker (E-09), the
// schedule validation + next-run preview + localized errors (E-10), the Run now
// in-flight state with "已投递" semantics and the session link (E-11), and the
// Esc-cancel/inline-form empty-state rules (E-12).

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

// --- minimal host services -------------------------------------------------

type fakeAutomationSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeAutomationSettings(values map[string]string) *fakeAutomationSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakeAutomationSettings{values: values}
}

func (s *fakeAutomationSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakeAutomationSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *fakeAutomationSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

// fakeChatStub is the slice of contracts.ChatService the session link touches.
type fakeChatStub struct {
	mu     sync.Mutex
	active string
}

func (f *fakeChatStub) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (f *fakeChatStub) Sessions() []contracts.ChatSessionInfo { return nil }
func (f *fakeChatStub) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (f *fakeChatStub) ActiveSession() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}
func (f *fakeChatStub) SetActiveSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = sessionID
	return nil
}
func (f *fakeChatStub) NewSession(string) (string, error)      { return "session-stub", nil }
func (f *fakeChatStub) DeleteSession(string) error             { return nil }
func (f *fakeChatStub) Send(string, string) error              { return nil }
func (f *fakeChatStub) Cancel(string) error                    { return nil }
func (f *fakeChatStub) Subscribe(func(string)) (func(), error) { return func() {}, nil }

func (f *fakeChatStub) activeSession() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// newDepthHarness activates the screen with optional settings/chat services.
func newDepthHarness(t *testing.T, service contracts.AutomationService,
	settings contracts.SettingsService, chat contracts.ChatService) *harness {
	t.Helper()
	return newHarnessWithSetup(t, service, nil, automation.Config{}, func(ctx cordis.Context) {
		if settings != nil {
			provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
		}
		if chat != nil {
			provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
		}
	})
}

// fillCreateForm fills the three required fields.
func fillCreateForm(t *testing.T, h *harness, tester *ui.Tester, name, prompt, schedule string) {
	t.Helper()
	h.click(tester, "任务名称")
	tester.Type(name)
	h.click(tester, "任务提示词")
	tester.Type(prompt)
	h.click(tester, "运行计划")
	tester.Type(schedule)
	tester.Frame()
}

// --- E-09: model picker ----------------------------------------------------

func TestCreateTaskModelPickerUsesCatalog(t *testing.T) {
	settings := newFakeAutomationSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"catalog-x","name":"目录模型"}]`,
	})
	service := newFakeAutomation()
	h := newDepthHarness(t, service, settings, nil)
	tester := h.tester()
	h.click(tester, "新建任务")
	h.assertTexts(tester, "跟随默认模型")
	h.click(tester, "跟随默认模型")
	h.click(tester, "目录模型")
	h.assertTexts(tester, "目录模型")
	h.assertNoTexts(tester, "该模型不在内置目录中")
	fillCreateForm(t, h, tester, "带目录模型", "p", "every 60s")
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].Model != "catalog-x" {
		t.Fatalf("Create model = %+v, want the catalog id catalog-x", created)
	}
}

func TestCreateTaskCustomModelKeepsWarning(t *testing.T) {
	settings := newFakeAutomationSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"catalog-x","name":"目录模型"}]`,
	})
	service := newFakeAutomation()
	h := newDepthHarness(t, service, settings, nil)
	tester := h.tester()
	h.click(tester, "新建任务")
	h.click(tester, "跟随默认模型")
	h.click(tester, "自定义模型…")
	h.click(tester, "自定义模型 ID")
	tester.Type("typo-model")
	tester.Frame()
	h.assertTexts(tester, "该模型不在内置目录中")
	fillCreateForm(t, h, tester, "自定义模型", "p", "every 60s")
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].Model != "typo-model" {
		t.Fatalf("Create model = %+v, want the typed custom id", created)
	}
}

// --- E-10: schedule validation + preview + localized errors ----------------

func TestAutomationScheduleValidationAndPreview(t *testing.T) {
	service := newFakeAutomation()
	h := newDepthHarness(t, service, nil, nil)
	tester := h.tester()
	h.click(tester, "新建任务")
	h.assertTexts(tester,
		"支持 */N * * * *（每 N 分钟）、M H * * *（每天 HH:MM）、@hourly、@daily、every <N>s、after <N>s。")

	fillCreateForm(t, h, tester, "计划任务", "p", "not-a-cron")
	h.assertTexts(tester, "运行计划无效：请检查格式与取值。")
	h.click(tester, "保存")
	if got := len(service.created()); got != 0 {
		t.Fatalf("Create calls with an invalid schedule = %d, want 0", got)
	}

	// Fixing the draft clears the field error and previews the next run.
	h.click(tester, "运行计划")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("0 9 * * *")
	tester.Frame()
	h.assertNoTexts(tester, "运行计划无效：请检查格式与取值。")
	h.assertTexts(tester, "下次运行：", "每天 09:00")
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].Schedule != "0 9 * * *" {
		t.Fatalf("Create = %+v, want the valid schedule", created)
	}
}

func TestAutomationScheduleAcceptsEveryForm(t *testing.T) {
	service := newFakeAutomation()
	h := newDepthHarness(t, service, nil, nil)
	tester := h.tester()
	h.click(tester, "新建任务")
	fillCreateForm(t, h, tester, "每小时任务", "p", "@hourly")
	tester.Frame()
	h.assertTexts(tester, "每小时", "下次运行：")
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].Schedule != "@hourly" {
		t.Fatalf("Create = %+v, want @hourly", created)
	}
}

func TestAutomationBackendErrorIsLocalized(t *testing.T) {
	service := newFakeAutomation()
	service.createErr = fmt.Errorf("automation: schedule %q: want 5 cron fields, @hourly, @daily, every <N>s or after <N>s", "x")
	h := newDepthHarness(t, service, nil, nil)
	tester := h.tester()
	h.click(tester, "新建任务")
	fillCreateForm(t, h, tester, "后端拒绝", "p", "every 60s")
	h.click(tester, "保存")
	h.assertTexts(tester, "运行计划无效，请检查格式后重试。")
	h.assertNoTexts(tester, "want 5 cron fields", "automation:")
}

// --- E-11: Run now in-flight + 已投递 semantics + session link --------------

// completeRun simulates the backend finishing a run (a new history record).
func completeRun(service *fakeAutomation, taskID, sessionID string) {
	service.mu.Lock()
	for index, task := range service.tasks {
		if task.ID != taskID {
			continue
		}
		now := time.Now().UTC()
		task.LastRun = now
		task.RunHistory = append([]contracts.AutomationRunRecord{{
			StartedAt: now, FinishedAt: now, Status: contracts.AutomationRunSuccess,
			Summary: "ran in chat session " + sessionID,
		}}, task.RunHistory...)
		service.tasks[index] = task
	}
	service.mu.Unlock()
	service.notify()
}

func TestRunNowShowsPendingThenDeliveredAndSessionLink(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "p", Schedule: "every 60s", Enabled: true,
	})
	chat := &fakeChatStub{}
	h := newDepthHarness(t, service, nil, chat)
	tester := h.tester()
	h.assertNoTexts(tester, "运行中…")

	h.click(tester, "立即运行")
	if got := service.runs(); len(got) != 1 || got[0] != "task-1" {
		t.Fatalf("RunNow calls = %v, want [task-1]", got)
	}
	h.assertTexts(tester, "运行中…")
	// The in-flight state is the card's, not a claim that the run finished.
	h.assertNoTexts(tester, "已投递")

	completeRun(service, "task-1", "sess-9")
	tester.Frame()
	h.assertNoTexts(tester, "运行中…")
	h.assertTexts(tester, "已投递", "结果由会话决定", "打开会话")

	h.click(tester, "打开会话")
	if got := chat.activeSession(); got != "sess-9" {
		t.Fatalf("active session = %q, want sess-9", got)
	}
}

// --- E-12: Esc cancels the form; empty-state CTA suppressed while it is open -

func TestCreateFormEscapeAndEmptyStateSuppression(t *testing.T) {
	service := newFakeAutomation()
	h := newDepthHarness(t, service, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "创建第一个任务")

	h.click(tester, "新建任务")
	h.assertNoTexts(tester, "创建第一个任务")
	h.assertTexts(tester, "填写上方表单并保存即可创建任务。")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "新建自动化任务", "填写上方表单并保存即可创建任务。")
	h.assertTexts(tester, "创建第一个任务")
}

func TestFiltersEmptyStateExplainsWhenFormOpen(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "巡检", Prompt: "p", Schedule: "every 60s", Enabled: true,
	})
	h := newDepthHarness(t, service, nil, nil)
	tester := h.tester()
	h.click(tester, "新建任务")
	// Search for a name that does not exist while the form is open.
	h.click(tester, "搜索任务")
	tester.Type("no-such-task")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的任务。")
	if strings.Contains(strings.Join(tester.Texts(), " "), "换个关键词或清空筛选试试") {
		t.Fatalf("the full filtered empty state competes with the open form; texts = %v", tester.Texts())
	}
	_ = errors.New // keep the import set stable for future error tests
}
