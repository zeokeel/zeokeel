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
	"github.com/zeokeel/zeokeel/internal/hosttools"
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

func (l *fakeLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// chatCall is one recorded chat execution.
type chatCall struct {
	SessionID string
	Title     string
	Prompt    string
}

// fakeSession is one chat session's read model inside fakeChat.
type fakeSession struct {
	title    string
	running  bool
	err      string
	messages []contracts.ChatMessage
}

// fakeChat is a contracts.ChatService test double that records NewSession/Send
// and models a running session like the agent backend: Send marks the session
// running and notifies subscribers, and finish/Complete expose the terminal
// state. With hold unset Send completes immediately (a fast mocked run).
type fakeChat struct {
	mu            sync.Mutex
	next          int
	sessions      map[string]*fakeSession
	calls         chan chatCall
	subscribers   map[uint64]func(string)
	nextSub       uint64
	hold          bool
	sends         int
	newSessionErr error
	sendErr       error
}

func newFakeChat() *fakeChat {
	return &fakeChat{
		sessions:    map[string]*fakeSession{},
		calls:       make(chan chatCall, 64),
		subscribers: map[uint64]func(string){},
	}
}

// setHold makes following sends stay running until finish is called.
func (c *fakeChat) setHold(hold bool) {
	c.mu.Lock()
	c.hold = hold
	c.mu.Unlock()
}

func (c *fakeChat) setSendError(err error) {
	c.mu.Lock()
	c.sendErr = err
	c.mu.Unlock()
}

// sessionCount reports how many sessions the fake knows.
func (c *fakeChat) sessionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sessions)
}

// sendCount reports how many prompts were accepted.
func (c *fakeChat) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sends
}

// seedSession registers a chat session the fake should know about (a restart
// test reconstructing a conversation from durable state).
func (c *fakeChat) seedSession(sessionID, title string, running bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[sessionID] = &fakeSession{title: title, running: running}
}

// sessionTitle returns one session's title ("" when unknown).
func (c *fakeChat) sessionTitle(sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if session, ok := c.sessions[sessionID]; ok {
		return session.title
	}
	return ""
}

// subscriberCount reports the live chat-session subscribers.
func (c *fakeChat) subscriberCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.subscribers)
}

// finish settles one running session like the agent's run settle: the trailing
// assistant message carries the error (kind) and the session stops running.
func (c *fakeChat) finish(sessionID, errText, errorKind string) {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if !ok {
		c.mu.Unlock()
		return
	}
	session.running = false
	session.err = errText
	message := contracts.ChatMessage{Role: contracts.ChatRoleAssistant}
	if errText != "" {
		message.Error = errText
		message.ErrorKind = errorKind
	}
	session.messages = append(session.messages, message)
	c.mu.Unlock()
	c.notify(sessionID)
}

// complete settles a session successfully.
func (c *fakeChat) complete(sessionID string) { c.finish(sessionID, "", "") }

// fail settles a session with a failed turn.
func (c *fakeChat) fail(sessionID, message string) {
	c.finish(sessionID, message, contracts.MessageErrorKindOther)
}

// interrupt settles a session with a user cancellation.
func (c *fakeChat) interrupt(sessionID string) {
	c.finish(sessionID, "cancelled", contracts.MessageErrorKindCancelled)
}

func (c *fakeChat) Config() contracts.ChatConfig { return contracts.ChatConfig{} }

func (c *fakeChat) Sessions() []contracts.ChatSessionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	infos := make([]contracts.ChatSessionInfo, 0, len(c.sessions))
	for id, session := range c.sessions {
		infos = append(infos, contracts.ChatSessionInfo{
			ID: id, Title: session.title, Running: session.running,
			MessageCount: len(session.messages),
		})
	}
	return infos
}

func (c *fakeChat) Session(sessionID string) (contracts.ChatSession, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	session, ok := c.sessions[sessionID]
	if !ok {
		return contracts.ChatSession{}, false
	}
	return contracts.ChatSession{
		ID: sessionID, Title: session.title, Running: session.running,
		Error: session.err, Messages: append([]contracts.ChatMessage(nil), session.messages...),
	}, true
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
	c.sessions[id] = &fakeSession{title: title}
	return id, nil
}

func (c *fakeChat) DeleteSession(sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sessionID)
	return nil
}

func (c *fakeChat) Send(sessionID, prompt string) error {
	c.mu.Lock()
	session, ok := c.sessions[sessionID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("fakeChat: unknown session %q", sessionID)
	}
	if session.running {
		c.mu.Unlock()
		return fmt.Errorf("fakeChat: session %q is already running", sessionID)
	}
	if c.sendErr != nil {
		err := c.sendErr
		c.mu.Unlock()
		return err
	}
	title := session.title
	hold := c.hold
	if hold {
		session.running = true
	} else {
		session.messages = append(session.messages, contracts.ChatMessage{Role: contracts.ChatRoleAssistant})
	}
	c.sends++
	select {
	case c.calls <- chatCall{SessionID: sessionID, Title: title, Prompt: prompt}:
	default:
	}
	c.mu.Unlock()
	c.notify(sessionID)
	return nil
}

func (c *fakeChat) Cancel(string) error { return nil }

func (c *fakeChat) Subscribe(fn func(string)) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	c.mu.Lock()
	c.nextSub++
	id := c.nextSub
	c.subscribers[id] = fn
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.subscribers, id)
			c.mu.Unlock()
		})
	}, nil
}

// notify calls every chat subscriber outside the fake's lock.
func (c *fakeChat) notify(sessionID string) {
	c.mu.Lock()
	listeners := make([]func(string), 0, len(c.subscribers))
	for _, listener := range c.subscribers {
		listeners = append(listeners, listener)
	}
	c.mu.Unlock()
	for _, listener := range listeners {
		if listener != nil {
			listener(sessionID)
		}
	}
}

// newTestService builds a service directly and closes its ticker on cleanup.
func newTestService(t *testing.T, dataDir string, cfg Config, chat contracts.ChatService) *service {
	t.Helper()
	service, err := newService(dataDir, cfg, &fakeLogger{}, chat, nil)
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
	// Generous budget: parallel loads can delay the asynchronous scheduler runs.
	deadline := time.Now().Add(10 * time.Second)
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
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the automation to run")
		return chatCall{}
	}
}

// mustCreate registers a task and fails on error.
// TestListSnapshotsDoNotAliasRunHistory pins the read model's independence: the
// scheduler settles runs on a background goroutine, so a List snapshot must own
// its RunHistory slice instead of aliasing the store (race detector caught the
// aliased read in TestModuleWiresOptionalNotifier).
func TestListSnapshotsDoNotAliasRunHistory(t *testing.T) {
	service := newTestService(t, t.TempDir(), Config{}, newFakeChat())
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "snapshot", Prompt: "run", Schedule: "every 60s",
	})
	service.mu.Lock()
	for index := range service.tasks {
		if service.tasks[index].ID == created.ID {
			service.tasks[index].RunHistory = []contracts.AutomationRunRecord{{
				ID: "run-1", Status: contracts.AutomationRunSuccess,
			}}
		}
	}
	service.mu.Unlock()

	snapshot, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snapshot) != 1 || len(snapshot[0].RunHistory) != 1 {
		t.Fatalf("List snapshot = %+v, want one task with one run", snapshot)
	}
	snapshot[0].RunHistory[0].Status = "mutated"
	again, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List again: %v", err)
	}
	if again[0].RunHistory[0].Status == "mutated" {
		t.Fatal("List exposed the store's RunHistory backing array")
	}
}

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

	// FR-06: a corrupt registry is quarantined and the service starts empty.
	if err := os.WriteFile(registryPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt registry: %v", err)
	}
	logger := &fakeLogger{}
	recovered, err := newService(dataDir, Config{}, logger, chat, nil)
	if err != nil {
		t.Fatalf("newService(corrupt registry) = %v, want quarantine", err)
	}
	defer recovered.Close()
	if tasks, listErr := recovered.List(ctx); listErr != nil || len(tasks) != 0 {
		t.Fatalf("tasks after quarantine = %+v/%v, want empty", tasks, listErr)
	}
	if _, statErr := os.Stat(registryPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("corrupt registry still at %s (err=%v)", registryPath, statErr)
	}
	backups, globErr := filepath.Glob(registryPath + ".corrupt-*")
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("quarantine backups = %v/%v, want exactly one", backups, globErr)
	}
	if !logger.contains("corrupt") || !logger.contains("quarantine") {
		t.Fatalf("recovery was not logged: %v", logger.lines)
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
	service, err := newService(t.TempDir(), Config{TickMs: 10}, &fakeLogger{}, chat, nil)
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

	toolRegistry, ok := root.Get(hosttools.KeyRegistry)
	if !ok {
		t.Fatal("tool registry (hosttools.KeyRegistry) was not provided")
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
