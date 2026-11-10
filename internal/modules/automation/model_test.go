package automation

// Tests for the per-task model override: persistence (create/update/roundtrip,
// legacy registry compatibility) and applying the model to the fresh automation
// session through contracts.SessionModelSwitcher (including the unsupported and
// failure fallbacks). Written before the implementation (TDD).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// modelChat is a fakeChat that also implements contracts.SessionModelSwitcher.
type modelChat struct {
	*fakeChat
	mu     sync.Mutex
	calls  []modelCall
	setErr error
}

type modelCall struct {
	sessionID string
	model     string
}

func newModelChat() *modelChat {
	return &modelChat{fakeChat: newFakeChat()}
}

func (c *modelChat) SetSessionModel(sessionID, model string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, modelCall{sessionID: sessionID, model: model})
	return c.setErr
}

func (c *modelChat) modelCalls() []modelCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]modelCall(nil), c.calls...)
}

// plainChat hides the optional SessionModelSwitcher half of its inner service.
type plainChat struct{ contracts.ChatService }

// newStoredService opens a second service on the same registry file.
func newStoredService(t *testing.T, dataDir string, chat contracts.ChatService) *service {
	t.Helper()
	service, err := newService(dataDir, Config{TickMs: 3600000}, &fakeLogger{}, chat, nil)
	if err != nil {
		t.Fatalf("newService(reload): %v", err)
	}
	t.Cleanup(service.Close)
	return service
}

func TestCreateUpdatePersistTaskModel(t *testing.T) {
	dataDir := t.TempDir()
	chat := newFakeChat()
	service := newTestService(t, dataDir, Config{TickMs: 3600000}, chat)

	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "带模型", Prompt: "p", Schedule: "every 60s", Model: "  gpt-4o  ",
	})
	if created.Model != "gpt-4o" {
		t.Fatalf("created.Model = %q, want the trimmed model", created.Model)
	}

	reloaded := newStoredService(t, dataDir, chat)
	tasks, err := reloaded.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Model != "gpt-4o" {
		t.Fatalf("reloaded tasks = %+v/%v, want model gpt-4o", tasks, err)
	}

	updatedTask := created
	updatedTask.Model = "  gpt-4.1-mini  "
	updated, err := service.Update(context.Background(), updatedTask)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Model != "gpt-4.1-mini" {
		t.Fatalf("updated.Model = %q, want trimmed gpt-4.1-mini", updated.Model)
	}
	body, err := os.ReadFile(filepath.Join(dataDir, DefaultFileName))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if !strings.Contains(string(body), `"model": "gpt-4.1-mini"`) {
		t.Fatalf("registry does not persist the model:\n%s", body)
	}

	// Clearing the model removes the override.
	updatedTask.Model = " "
	cleared, err := service.Update(context.Background(), updatedTask)
	if err != nil {
		t.Fatalf("Update(clear): %v", err)
	}
	if cleared.Model != "" {
		t.Fatalf("cleared.Model = %q, want empty", cleared.Model)
	}
}

func TestLegacyRegistryWithoutModel(t *testing.T) {
	dataDir := t.TempDir()
	legacy := `{"tasks":[{"id":"legacy-1","name":"旧任务","prompt":"p","schedule":"every 60s","enabled":false}]}`
	if err := os.WriteFile(filepath.Join(dataDir, DefaultFileName), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy registry: %v", err)
	}
	service := newStoredService(t, dataDir, newFakeChat())
	tasks, err := service.List(context.Background())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("List = %+v/%v", tasks, err)
	}
	if tasks[0].Model != "" {
		t.Fatalf("legacy task model = %q, want empty", tasks[0].Model)
	}
	updated, err := service.Update(context.Background(), contracts.AutomationTask{
		ID: tasks[0].ID, Name: tasks[0].Name, Prompt: tasks[0].Prompt,
		Schedule: tasks[0].Schedule, Model: "gpt-legacy",
	})
	if err != nil || updated.Model != "gpt-legacy" {
		t.Fatalf("Update legacy/model = %+v/%v", updated, err)
	}
}

func TestRunNowAppliesTaskModel(t *testing.T) {
	chat := newModelChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "模型任务", Prompt: "use the task model", Schedule: "every 60s", Model: "gpt-task",
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat.fakeChat)
	if call.Prompt != "use the task model" {
		t.Fatalf("Send prompt = %q", call.Prompt)
	}
	calls := chat.modelCalls()
	if len(calls) != 1 {
		t.Fatalf("SetSessionModel calls = %+v, want one", calls)
	}
	if calls[0].sessionID != "sess-1" || calls[0].model != "gpt-task" {
		t.Fatalf("SetSessionModel(%q, %q), want (sess-1, gpt-task)", calls[0].sessionID, calls[0].model)
	}
}

func TestRunNowSkipsSetModelForEmptyTaskModel(t *testing.T) {
	chat := newModelChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "默认模型", Prompt: "p", Schedule: "every 60s",
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitCall(t, chat.fakeChat)
	if calls := chat.modelCalls(); len(calls) != 0 {
		t.Fatalf("SetSessionModel calls with an empty model = %+v, want none", calls)
	}
}

func TestRunNowWithoutModelSwitcherIsSafe(t *testing.T) {
	inner := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, plainChat{ChatService: inner})
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "无切换器", Prompt: "still sent", Schedule: "every 60s", Model: "gpt-unsupported",
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, inner)
	if call.Prompt != "still sent" {
		t.Fatalf("Send prompt = %q", call.Prompt)
	}
	ran := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return !task.LastRun.IsZero()
	})
	if ran.LastError != "" {
		t.Fatalf("run error = %q, want none (model support is optional)", ran.LastError)
	}
}

func TestRunNowSetModelFailureStillSends(t *testing.T) {
	chat := newModelChat()
	chat.setErr = errors.New("model not allowed")
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "模型失败", Prompt: "prompt after failure", Schedule: "every 60s", Model: "bad-model",
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat.fakeChat)
	if call.Prompt != "prompt after failure" {
		t.Fatalf("Send prompt = %q, want the send to continue after a SetSessionModel failure", call.Prompt)
	}
	if calls := chat.modelCalls(); len(calls) != 1 || calls[0].model != "bad-model" {
		t.Fatalf("SetSessionModel calls = %+v", calls)
	}
}
