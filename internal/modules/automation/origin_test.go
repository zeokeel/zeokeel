package automation

// Tests for the run-session provenance the automation module records when it
// starts a scheduled run: the sidebar hides run sessions by this explicit
// origin instead of guessing from their title (audit F6).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// originSettings is a contracts.SettingsService test double with optional write
// failure injection; runTask writes from the scheduler goroutine, so every
// access is locked.
type originSettings struct {
	mu     sync.Mutex
	values map[string]string
	setErr error
	sets   []string
}

func newOriginSettings() *originSettings {
	return &originSettings{values: map[string]string{}}
}

func (s *originSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *originSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = value
	s.sets = append(s.sets, key+"="+value)
	return nil
}

func (s *originSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *originSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

// runOriginProduct activates the real automation module with a fake chat service
// and a settings store, as the product host does (the settings service is an
// optional dependency read with ctx.Get).
func runOriginProduct(t *testing.T, settings contracts.SettingsService) (*originSettings, *fakeChat, contracts.AutomationService) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	chat := newFakeChat()
	logger := &fakeLogger{}
	for _, provide := range []func() error{
		func() error { _, err := root.Provide(contracts.KeyDataDir, t.TempDir()); return err },
		func() error { _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); return err },
		func() error { _, err := root.Provide(contracts.KeyChat, contracts.ChatService(chat)); return err },
	} {
		if err := provide(); err != nil {
			t.Fatalf("provide: %v", err)
		}
	}
	store, _ := settings.(*originSettings)
	if settings != nil {
		if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger).AddBuiltin(contracts.KeyChat)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register automation: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "automation", Module: ModuleID, Config: map[string]any{"tickMs": 3600000},
	}})
	if err != nil {
		t.Fatalf("load automation: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	service, ok := root.Get(contracts.KeyAutomation)
	if !ok {
		t.Fatal("KeyAutomation was not provided")
	}
	return store, chat, service
}

// TestRunRecordsSessionOrigin is the audit-F6 backend half: starting a
// scheduled run must persist the run session's provenance so the chat read model
// and the sidebar can filter it without looking at the title.
func TestRunRecordsSessionOrigin(t *testing.T) {
	store := newOriginSettings()
	_, chat, service := runOriginProduct(t, store)

	task, err := service.Create(context.Background(), contracts.AutomationTask{
		Name: "ticker", Prompt: "hello tick", Schedule: "every 60s", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	// RunNow executes asynchronously; the chat call is recorded after NewSession,
	// which is where the origin is written.
	var call chatCall
	select {
	case call = <-chat.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not reach the chat service")
	}
	if call.Title != "自动化: ticker" {
		t.Fatalf("run session title = %q, want the product convention", call.Title)
	}
	// The fake chat names its sessions sess-<n>; the first run creates sess-1.
	if got := store.get(contracts.SessionOriginSettingKey("sess-1")); got != contracts.SessionOriginAutomation {
		t.Fatalf("session origin = %q, want %q (sets = %v)",
			got, contracts.SessionOriginAutomation, store.sets)
	}
}

// TestRunWithoutSettingsStillRuns covers hosts without a settings service (or
// embedded use): recording provenance is best effort and never blocks the run.
func TestRunWithoutSettingsStillRuns(t *testing.T) {
	_, chat, service := runOriginProduct(t, nil)

	task, err := service.Create(context.Background(), contracts.AutomationTask{
		Name: "no-settings", Prompt: "go", Schedule: "every 60s", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := service.RunNow(context.Background(), task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	select {
	case call := <-chat.calls:
		if call.Title != "自动化: no-settings" {
			t.Fatalf("run session title = %q", call.Title)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not reach the chat service without settings")
	}
}

// TestRunSurvivesOriginWriteFailure pins the best-effort contract: a failing
// provenance write is logged and the run still reaches the chat service (the
// sidebar filter is the only thing lost).
func TestRunSurvivesOriginWriteFailure(t *testing.T) {
	settings := newOriginSettings()
	settings.setErr = errors.New("settings disk full")
	chat := newFakeChat()
	logger := &fakeLogger{}
	service, err := newService(t.TempDir(), Config{TickMs: 3600000}, logger, chat, settings)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	t.Cleanup(service.Close)

	if _, err := service.runTask(contracts.AutomationTask{ID: "task-1", Name: "run", Prompt: "hello"}); err != nil {
		t.Fatalf("runTask with a failing origin write: %v", err)
	}
	select {
	case call := <-chat.calls:
		if call.Title != "自动化: run" {
			t.Fatalf("run session title = %q", call.Title)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not reach the chat service")
	}
	logger.mu.Lock()
	logged := strings.Join(logger.lines, "\n")
	logger.mu.Unlock()
	if !strings.Contains(logged, "record session origin") {
		t.Fatalf("origin write failure was not logged; logs = %q", logged)
	}
}
