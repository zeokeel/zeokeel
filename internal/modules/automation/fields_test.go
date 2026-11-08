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
	return nil
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
	summary, err := service.runTask(contracts.AutomationTask{
		ID: "task-1", Name: "run", Prompt: "hello",
		WorkspaceID: "ws-1", Permission: "full", Model: "m1",
	})
	if err != nil || summary == "" {
		t.Fatalf("runTask = %q, %v", summary, err)
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

func TestRunTaskSwitchFailuresAreWarnings(t *testing.T) {
	chat := newSwitchChat()
	chat.workspaceErr = errors.New("workspace boom")
	chat.permissionErr = errors.New("permission boom")
	logger := &fakeLogger{}
	service, err := newService(t.TempDir(), Config{TickMs: 3600000}, logger, chat)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	t.Cleanup(service.Close)
	if _, err := service.runTask(contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello", WorkspaceID: "ws", Permission: "never"}); err != nil {
		t.Fatalf("runTask should continue after switch failures: %v", err)
	}
	logged := strings.Join(logger.linesSnapshot(), "\n")
	if !strings.Contains(logged, "workspace") || !strings.Contains(logged, "permission") {
		t.Fatalf("missing warnings: %v", logger.linesSnapshot())
	}
}

func TestRunTaskWithoutSwitchersIsSafe(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, plainChat{chat})
	if _, err := service.runTask(contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello", WorkspaceID: "ws", Permission: "full"}); err != nil {
		t.Fatalf("runTask without switchers = %v", err)
	}
	select {
	case <-chat.calls:
	default:
		t.Fatal("prompt was not sent")
	}
}

func (l *fakeLogger) linesSnapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}
