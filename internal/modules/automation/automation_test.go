package automation

// Tests for the automation backend: CRUD + persistence, the scheduler ticker,
// RunNow/one-shot semantics, error recording, subscriptions, concurrency and
// the optional Rig schedule_* toolkit adapter.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

// chatCall is one recorded chat execution.
type chatCall struct {
	Title  string
	Prompt string
}

// fakeChat is a contracts.ChatService test double that records NewSession/Send.
type fakeChat struct {
	mu            sync.Mutex
	next          int
	sessions      map[string]string
	calls         chan chatCall
	newSessionErr error
	sendErr       error
}

func newFakeChat() *fakeChat {
	return &fakeChat{sessions: map[string]string{}, calls: make(chan chatCall, 64)}
}

func (c *fakeChat) setSendError(err error) {
	c.mu.Lock()
	c.sendErr = err
	c.mu.Unlock()
}

func (c *fakeChat) Config() contracts.ChatConfig { return contracts.ChatConfig{} }

func (c *fakeChat) Sessions() []contracts.ChatSessionInfo { return nil }

func (c *fakeChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}

func (c *fakeChat) ActiveSession() string { return "" }

func (c *fakeChat) SetActiveSession(string) error { return nil }

func (c *fakeChat) NewSession(title string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.newSessionErr != nil {
		return "", c.newSessionErr
	}
	c.next++
	id := fmt.Sprintf("sess-%d", c.next)
	c.sessions[id] = title
	return id, nil
}

func (c *fakeChat) DeleteSession(string) error { return nil }

func (c *fakeChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	sendErr := c.sendErr
	title := c.sessions[sessionID]
	c.mu.Unlock()
	if sendErr != nil {
		return sendErr
	}
	select {
	case c.calls <- chatCall{Title: title, Prompt: prompt}:
	default:
	}
	return nil
}

func (c *fakeChat) Cancel(string) error { return nil }

func (c *fakeChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

// newTestService builds a service directly and closes its ticker on cleanup.
func newTestService(t *testing.T, dataDir string, cfg Config, chat contracts.ChatService) *service {
	t.Helper()
	service, err := newService(dataDir, cfg, &fakeLogger{}, chat)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	t.Cleanup(service.Close)
	return service
}

// waitTask polls List until predicate is true.
func waitTask(t *testing.T, service contracts.AutomationService, id string,
	predicate func(contracts.AutomationTask) bool) contracts.AutomationTask {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := service.List(context.Background())
		if err == nil {
			for _, task := range tasks {
				if task.ID == id && predicate(task) {
					return task
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %q did not reach the expected state", id)
	return contracts.AutomationTask{}
}

// waitCall waits for one recorded chat execution.
func waitCall(t *testing.T, chat *fakeChat) chatCall {
	t.Helper()
	select {
	case call := <-chat.calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the automation to run")
		return chatCall{}
	}
}

// mustCreate registers a task and fails on error.
func mustCreate(t *testing.T, service contracts.AutomationService, task contracts.AutomationTask) contracts.AutomationTask {
	t.Helper()
	created, err := service.Create(context.Background(), task)
	if err != nil {
		t.Fatalf("Create(%+v): %v", task, err)
	}
	return created
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/automation" {
		t.Fatalf("module ID = %q", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyAutomation.Name() {
		t.Fatalf("provides = %+v, want KeyAutomation", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{contracts.KeyDataDir.Name(), contracts.KeyLogger.Name(), contracts.KeyChat.Name()} {
		if !required[name] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, name)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	for _, cfg := range []Config{{}, {Path: "nested/automations.json", TickMs: 100}} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{TickMs: -1}); err == nil {
		t.Fatal("Validate accepted a negative tickMs")
	}
	if err := descriptor.Validate(Config{Path: "bad\x00path"}); err == nil {
		t.Fatal("Validate accepted a NUL path")
	}
}

func TestCreateListUpdateDeleteAndPersistence(t *testing.T) {
	dataDir := t.TempDir()
	chat := newFakeChat()
	service := newTestService(t, dataDir, Config{TickMs: 3600000}, chat)
	ctx := context.Background()

	disabled := mustCreate(t, service, contracts.AutomationTask{})
	if !strings.HasPrefix(disabled.ID, "auto-") || disabled.Name != "自动化 1" {
		t.Fatalf("default task = %+v", disabled)
	}
	if disabled.NextRun != (time.Time{}) || disabled.CreatedAt.IsZero() || disabled.UpdatedAt.IsZero() {
		t.Fatalf("default task times = %+v", disabled)
	}
	enabled := mustCreate(t, service, contracts.AutomationTask{
		Name: "Daily", Prompt: "digest", Schedule: "@daily", Enabled: true,
	})
	if enabled.NextRun.IsZero() || !enabled.NextRun.After(time.Now()) {
		t.Fatalf("enabled NextRun = %s, want a future instant", enabled.NextRun)
	}
	if _, err := service.Create(ctx, contracts.AutomationTask{ID: enabled.ID, Name: "dup"}); err == nil {
		t.Fatal("Create accepted a duplicate ID")
	}
	if _, err := service.Create(ctx, contracts.AutomationTask{
		Name: "bad", Schedule: "not a schedule", Enabled: true,
	}); err == nil {
		t.Fatal("Create accepted an invalid enabled schedule")
	}

	tasks, err := service.List(ctx)
	if err != nil || len(tasks) != 2 || tasks[0].ID != disabled.ID || tasks[1].ID != enabled.ID {
		t.Fatalf("List = %+v/%v, want creation order", tasks, err)
	}

	updated, err := service.Update(ctx, contracts.AutomationTask{
		ID: enabled.ID, Name: "Updated", Prompt: "digest v2", Schedule: "every 60s", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Updated" || updated.Schedule != "every 60s" || !updated.CreatedAt.Equal(enabled.CreatedAt) {
		t.Fatalf("updated task = %+v", updated)
	}
	if updated.NextRun.Before(time.Now()) {
		t.Fatalf("updated NextRun = %s, want future", updated.NextRun)
	}
	if _, err := service.Update(ctx, contracts.AutomationTask{ID: "missing", Name: "x"}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrTaskNotFound", err)
	}
	disabledUpdate, err := service.Update(ctx, contracts.AutomationTask{ID: disabled.ID, Name: disabled.Name, Enabled: false})
	if err != nil || disabledUpdate.NextRun != (time.Time{}) {
		t.Fatalf("disable update = %+v/%v", disabledUpdate, err)
	}

	registryPath := filepath.Join(dataDir, DefaultFileName)
	if _, err := os.Stat(registryPath); err != nil {
		t.Fatalf("registry file missing: %v", err)
	}
	if err := service.Delete(ctx, "missing"); err != nil {
		t.Fatalf("Delete(unknown) = %v, want nil no-op", err)
	}
	if err := service.Delete(ctx, disabled.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	restarted := newTestService(t, dataDir, Config{TickMs: 3600000}, newFakeChat())
	tasks, err = restarted.List(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].ID != enabled.ID || tasks[0].Name != "Updated" {
		t.Fatalf("restored tasks = %+v/%v", tasks, err)
	}
	if tasks[0].NextRun.IsZero() || tasks[0].Schedule != "every 60s" {
		t.Fatalf("restored task = %+v", tasks[0])
	}

	// Corrupt registry is an activation error.
	if err := os.WriteFile(registryPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt registry: %v", err)
	}
	if _, err := newService(dataDir, Config{}, &fakeLogger{}, chat); err == nil {
		t.Fatal("newService loaded a corrupt registry")
	}
}

func TestTickFiresDueTasks(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 10}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "ticker", Prompt: "hello tick", Schedule: "every 1s", Enabled: true,
	})
	call := waitCall(t, chat)
	if call.Title != "自动化: ticker" || call.Prompt != "hello tick" {
		t.Fatalf("executed call = %+v", call)
	}
	ran := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return !task.LastRun.IsZero()
	})
	if ran.LastError != "" {
		t.Fatalf("tick LastError = %q", ran.LastError)
	}
	if ran.NextRun.IsZero() || !ran.NextRun.After(ran.LastRun) {
		t.Fatalf("tick reschedule = last %s next %s", ran.LastRun, ran.NextRun)
	}
}

func TestRunNowAndAfterOneShot(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "once", Prompt: "one shot", Schedule: "after 3600s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if call := waitCall(t, chat); call.Prompt != "one shot" {
		t.Fatalf("RunNow call = %+v", call)
	}
	ran := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return !task.LastRun.IsZero()
	})
	if ran.Enabled || ran.NextRun != (time.Time{}) {
		t.Fatalf("after-task after run = enabled %v next %s, want disabled/zero", ran.Enabled, ran.NextRun)
	}
	if err := service.RunNow(context.Background(), "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("RunNow(missing) = %v, want ErrTaskNotFound", err)
	}
}

func TestExecutionErrorRecordedAndCleared(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "failing", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	chat.setSendError(errors.New("chat exploded"))
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	failed := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return strings.Contains(task.LastError, "chat exploded")
	})
	if failed.LastRun.IsZero() {
		t.Fatal("failed run did not record LastRun")
	}
	chat.setSendError(nil)
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow after recovery: %v", err)
	}
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return task.LastError == ""
	})
}

func TestSubscribeReceivesChanges(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)

	var mu sync.Mutex
	first, second := 0, 0
	unsubFirst, err := service.Subscribe(func() {
		mu.Lock()
		first++
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := service.Subscribe(func() {
		mu.Lock()
		second++
		mu.Unlock()
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	created := mustCreate(t, service, contracts.AutomationTask{Name: "sub", Enabled: false})
	if _, err := service.Update(context.Background(), contracts.AutomationTask{
		ID: created.ID, Name: "sub2", Enabled: false,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := service.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mu.Lock()
	firstCount, secondCount := first, second
	mu.Unlock()
	if firstCount != 3 || secondCount != 3 {
		t.Fatalf("notifications = %d/%d, want 3/3", firstCount, secondCount)
	}
	unsubFirst()
	unsubFirst() // idempotent
	if _, err := service.Create(context.Background(), contracts.AutomationTask{Name: "sub3"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mu.Lock()
	firstCount, secondCount = first, second
	mu.Unlock()
	if firstCount != 3 || secondCount != 4 {
		t.Fatalf("notifications after unsubscribe = %d/%d, want 3/4", firstCount, secondCount)
	}
}

func TestConcurrentCRUD(t *testing.T) {
	chat := newFakeChat()
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(number int) {
			defer waitGroup.Done()
			task, err := service.Create(context.Background(), contracts.AutomationTask{
				ID: fmt.Sprintf("auto-concurrent-%d", number), Name: fmt.Sprintf("worker-%d", number),
			})
			if err != nil {
				errs <- err
				return
			}
			if _, err := service.List(context.Background()); err != nil {
				errs <- err
				return
			}
			if _, err := service.Update(context.Background(), contracts.AutomationTask{
				ID: task.ID, Name: task.Name, Prompt: "updated",
			}); err != nil {
				errs <- err
			}
		}(index)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	tasks, err := service.List(context.Background())
	if err != nil || len(tasks) != workers {
		t.Fatalf("List after concurrent CRUD = %d/%v, want %d", len(tasks), err, workers)
	}
}

func TestCloseStopsTicker(t *testing.T) {
	chat := newFakeChat()
	service, err := newService(t.TempDir(), Config{TickMs: 10}, &fakeLogger{}, chat)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	service.Close()
	service.Close() // idempotent
	if _, err := service.Create(context.Background(), contracts.AutomationTask{
		Name: "after-close", Prompt: "no run", Schedule: "every 1s", Enabled: true,
	}); err != nil {
		t.Fatalf("Create after Close: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	select {
	case call := <-chat.calls:
		t.Fatalf("ticker kept running after Close: %+v", call)
	default:
	}
}

// TestToolkitAdapterReplacesScheduleTools covers the optional Rig toolkit
// registration: the durable adapter replaces the toolkit's in-memory
// schedule_* tools and the tools operate on the automation service.
func TestToolkitAdapterReplacesScheduleTools(t *testing.T) {
	dataDir := t.TempDir()
	chat := newFakeChat()
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	logger := &fakeLogger{}
	for _, provide := range []func() error{
		func() error { _, err := root.Provide(contracts.KeyDataDir, dataDir); return err },
		func() error { _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); return err },
		func() error { _, err := root.Provide(contracts.KeyChat, contracts.ChatService(chat)); return err },
	} {
		if err := provide(); err != nil {
			t.Fatalf("provide: %v", err)
		}
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger).AddBuiltin(contracts.KeyChat)
	if err := registry.Register(toolkit.Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register automation: %v", err)
	}
	toolkitLoader, err := module.Load(root, registry, []module.Entry{{ID: "toolkit", Module: "zeokeel/toolkit"}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	defer func() { _ = toolkitLoader.Close() }()
	automationLoader, err := module.Load(root, registry, []module.Entry{{
		ID: "automation", Module: ModuleID, Config: map[string]any{"tickMs": 3600000},
	}})
	if err != nil {
		t.Fatalf("load automation: %v", err)
	}
	defer func() { _ = automationLoader.Close() }()

	toolRegistry, ok := root.Get(contracts.KeyToolkit)
	if !ok {
		t.Fatal("KeyToolkit was not provided")
	}
	for _, name := range []string{"schedule_create", "schedule_list", "schedule_update", "schedule_delete"} {
		found := false
		for _, tool := range toolRegistry.Tools() {
			if tool.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("tool %q is not registered", name)
		}
	}

	result, err := toolRegistry.Invoke(context.Background(), rig.ToolCall{
		ID: "call-create", Name: "schedule_create",
		Arguments: `{"name":"from-tool","prompt":"do it","schedule":"@daily","enabled":true}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("schedule_create = %+v/%v", result, err)
	}
	automationService, ok := root.Get(contracts.KeyAutomation)
	if !ok {
		t.Fatal("KeyAutomation was not provided")
	}
	tasks, err := automationService.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Name != "from-tool" || tasks[0].Schedule != "@daily" {
		t.Fatalf("tasks after schedule_create = %+v/%v", tasks, err)
	}
	result, err = toolRegistry.Invoke(context.Background(), rig.ToolCall{
		ID: "call-list", Name: "schedule_list",
	})
	if err != nil || result.IsError || !strings.Contains(result.Content, "from-tool") {
		t.Fatalf("schedule_list = %+v/%v", result, err)
	}
	result, err = toolRegistry.Invoke(context.Background(), rig.ToolCall{
		ID: "call-delete", Name: "schedule_delete",
		Arguments: fmt.Sprintf(`{"id":%q}`, tasks[0].ID),
	})
	if err != nil || result.IsError {
		t.Fatalf("schedule_delete = %+v/%v", result, err)
	}
	tasks, err = automationService.List(context.Background())
	if err != nil || len(tasks) != 0 {
		t.Fatalf("tasks after schedule_delete = %+v/%v", tasks, err)
	}
}
