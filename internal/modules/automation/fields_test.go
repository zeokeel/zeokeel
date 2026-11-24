package automation

// Tests for the automation task workspace/permission fields and their run-time
// application through the optional chat switcher interfaces.

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

// switchChat records the order of the optional per-session switch calls.
type switchChat struct {
	*fakeChat
	mu            sync.Mutex
	order         []string
	workspace     string
	permission    string
	workspaceErr  error
	permissionErr error
	modelErr      error
}

func newSwitchChat() *switchChat { return &switchChat{fakeChat: newFakeChat()} }

func (c *switchChat) SetSessionWorkspace(sessionID, workspaceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, "workspace")
	c.workspace = sessionID + "|" + workspaceID
	return c.workspaceErr
}
func (c *switchChat) SetSessionPermission(sessionID, mode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, "permission")
	c.permission = sessionID + "|" + mode
	return c.permissionErr
}
func (c *switchChat) SetSessionModel(sessionID, model string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, "model")
	return c.modelErr
}
func (c *switchChat) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.order...)
}

func TestCreateUpdatePersistWorkspaceAndPermission(t *testing.T) {
	dataDir := t.TempDir()
	service := newTestService(t, dataDir, Config{TickMs: 3600000}, newFakeChat())
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "字段", Prompt: "p", Schedule: "every 60s",
		WorkspaceID: "  ws-1  ", Permission: "  full  ",
	})
	if created.WorkspaceID != "ws-1" || created.Permission != "full" {
		t.Fatalf("created = %+v", created)
	}
	reloaded := newStoredService(t, dataDir, newFakeChat())
	tasks, err := reloaded.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].WorkspaceID != "ws-1" || tasks[0].Permission != "full" {
		t.Fatalf("reloaded = %+v, %v", tasks, err)
	}
	updated, err := service.Update(context.Background(), contracts.AutomationTask{
		ID: created.ID, Name: "字段2", Prompt: "p2", Schedule: "every 60s",
		WorkspaceID: " ws-2 ", Permission: " never ",
	})
	if err != nil || updated.WorkspaceID != "ws-2" || updated.Permission != "never" {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
}

func TestAutomationRegistryLegacyJSONWithoutFields(t *testing.T) {
	dataDir := t.TempDir()
	legacy := `{"tasks":[{"id":"legacy","name":"旧任务","prompt":"p","schedule":"every 60s","enabled":false}]}`
	if err := os.WriteFile(filepath.Join(dataDir, DefaultFileName), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy registry: %v", err)
	}
	service := newTestService(t, dataDir, Config{TickMs: 3600000}, newFakeChat())
	tasks, err := service.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].WorkspaceID != "" || tasks[0].Permission != "" {
		t.Fatalf("legacy tasks = %+v, %v", tasks, err)
	}
}

func TestRunTaskAppliesWorkspaceAndPermission(t *testing.T) {
	chat := newSwitchChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	delivery, err := service.runTask(contracts.AutomationTask{
		ID: "task-1", Name: "run", Prompt: "hello",
		WorkspaceID: "ws-1", Permission: "full", Model: "m1",
	})
	if err != nil || delivery.Summary == "" || delivery.SessionID != "sess-1" {
		t.Fatalf("runTask = %+v, %v", delivery, err)
	}
	if got := chat.calls(); strings.Join(got, ",") != "workspace,permission,model" {
		t.Fatalf("switch order = %v", got)
	}
	chat.mu.Lock()
	workspace, permission := chat.workspace, chat.permission
	chat.mu.Unlock()
	if workspace != "sess-1|ws-1" || permission != "sess-1|full" {
		t.Fatalf("switch args = %q/%q", workspace, permission)
	}
}

// TestRunTaskSwitchFailuresFailTheRun covers the audit rule (2026-11-24 B-2):
// the workspace and permission overrides ARE the task's authorization boundary,
// so a switch that cannot be applied must fail the run — running anyway would
// use the current workspace and the global permission (with `full`, silently
// dropping the boundary the user configured) and the user would only find out
// from a log line.
func TestRunTaskSwitchFailuresFailTheRun(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		task  contracts.AutomationTask
		apply func(*switchChat)
	}{
		{
			name:  "workspace",
			task:  contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello", WorkspaceID: "ws"},
			apply: func(chat *switchChat) { chat.workspaceErr = errors.New("workspace boom") },
		},
		{
			name:  "permission",
			task:  contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello", Permission: "never"},
			apply: func(chat *switchChat) { chat.permissionErr = errors.New("permission boom") },
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chat := newSwitchChat()
			testCase.apply(chat)
			logger := &fakeLogger{}
			service, err := newService(t.TempDir(), Config{TickMs: 3600000}, logger, chat, nil)
			if err != nil {
				t.Fatalf("newService: %v", err)
			}
			t.Cleanup(service.Close)
			if _, err := service.runTask(testCase.task); err == nil {
				t.Fatalf("runTask continued after a %s override failed", testCase.name)
			}
			if sends := chat.sendCount(); sends != 0 {
				t.Fatalf("prompt was sent (%d) although the override failed", sends)
			}
		})
	}
}

// TestRunTaskModelOverrideStaysBestEffort: the model is a preference, not a
// boundary — a model switch failure must not fail the run.
func TestRunTaskModelOverrideStaysBestEffort(t *testing.T) {
	chat := newSwitchChat()
	chat.modelErr = errors.New("model boom")
	logger := &fakeLogger{}
	service, err := newService(t.TempDir(), Config{TickMs: 3600000}, logger, chat, nil)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	t.Cleanup(service.Close)
	if _, err := service.runTask(contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello", Model: "m1"}); err != nil {
		t.Fatalf("runTask should continue after a model switch failure: %v", err)
	}
	if !strings.Contains(strings.Join(logger.linesSnapshot(), "\n"), "model") {
		t.Fatalf("missing model warning: %v", logger.linesSnapshot())
	}
}

// TestRunTaskWithoutSwitchersIsSafe: a task that configures no override needs no
// switching capability at all.
func TestRunTaskWithoutSwitchersIsSafe(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, plainChat{chat})
	if _, err := service.runTask(contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello"}); err != nil {
		t.Fatalf("runTask without overrides = %v", err)
	}
	select {
	case <-chat.calls:
	default:
		t.Fatal("prompt was not sent")
	}
}

// TestRunTaskWithoutSwitcherCapabilityFails: an override with no capability to
// apply it is a failed run, not a silent downgrade.
func TestRunTaskWithoutSwitcherCapabilityFails(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, plainChat{chat})
	if _, err := service.runTask(contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello", WorkspaceID: "ws"}); err == nil {
		t.Fatal("runTask accepted a workspace override with no switcher")
	}
	select {
	case <-chat.calls:
		t.Fatal("prompt was sent although the override could not be applied")
	default:
	}
}

func (l *fakeLogger) linesSnapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// TestAutomationTasksKeepTheirWorkspaceBindings covers the automation workspace
// boundary: every task carries its own WorkspaceID through List, and a run
// applies that binding to its own new session instead of leaking it into (or
// delivering into) another workspace's run.
func TestAutomationTasksKeepTheirWorkspaceBindings(t *testing.T) {
	chat := newSwitchChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3_600_000}, chat)
	taskA := mustCreate(t, service, contracts.AutomationTask{
		Name: "workspace A", Prompt: "run in A", Schedule: "every 3600s",
		WorkspaceID: "ws-a", Enabled: false,
	})
	taskB := mustCreate(t, service, contracts.AutomationTask{
		Name: "workspace B", Prompt: "run in B", Schedule: "every 3600s",
		WorkspaceID: "ws-b", Enabled: false,
	})

	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 2 {
		t.Fatalf("List = %+v/%v, want both tasks", listed, err)
	}
	byID := make(map[string]string, len(listed))
	for _, task := range listed {
		byID[task.ID] = task.WorkspaceID
	}
	if byID[taskA.ID] != "ws-a" || byID[taskB.ID] != "ws-b" {
		t.Fatalf("listed workspace bindings = %v, want A=ws-a B=ws-b", byID)
	}

	deliveryA, err := service.runTask(taskA)
	if err != nil {
		t.Fatalf("runTask(A): %v", err)
	}
	chat.mu.Lock()
	workspaceA, sessionA := chat.workspace, deliveryA.SessionID
	chat.mu.Unlock()
	if workspaceA != sessionA+"|ws-a" {
		t.Fatalf("run A switched %q, want %q", workspaceA, sessionA+"|ws-a")
	}

	deliveryB, err := service.runTask(taskB)
	if err != nil {
		t.Fatalf("runTask(B): %v", err)
	}
	chat.mu.Lock()
	workspaceB, sessionB := chat.workspace, deliveryB.SessionID
	chat.mu.Unlock()
	if workspaceB != sessionB+"|ws-b" {
		t.Fatalf("run B switched %q, want %q", workspaceB, sessionB+"|ws-b")
	}
	if deliveryA.SessionID == deliveryB.SessionID {
		t.Fatalf("both workspace runs reused session %q", deliveryA.SessionID)
	}
}
