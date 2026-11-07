package approvals

// Permission-mode tests (written before the implementation, TDD): the
// approvals service reads contracts.SettingAgentPermission on every call and
// maps ask/workspace/full onto the queueing / auto-allow policies.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeSettings is a mutex-protected contracts.SettingsService map.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeSettings(values map[string]string) *fakeSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakeSettings{values: values}
}

func (s *fakeSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *fakeSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *fakeSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

func (s *fakeSettings) setPermission(mode string) { _ = s.Set(contracts.SettingAgentPermission, mode) }

// newPermissionService builds an approvals service with a permissions store.
func newPermissionService(t *testing.T, mode string, settings *fakeSettings) (*service, *fakeLogger) {
	t.Helper()
	logger := &fakeLogger{}
	svc, err := newServiceWithSettings(mode, logger, settings)
	if err != nil {
		t.Fatalf("newServiceWithSettings: %v", err)
	}
	return svc, logger
}

// snapshotMode reads Mode on the service.
func snapshotMode(t *testing.T, svc *service) string {
	t.Helper()
	return svc.Mode()
}

// --- ask / default ----------------------------------------------------------

func TestPermissionDefaultAskQueues(t *testing.T) {
	settings := newFakeSettings(nil)
	svc, _ := newPermissionService(t, ModeAsk, settings)
	notifications := atomic.Int32{}
	if _, err := svc.Subscribe(func() { notifications.Add(1) }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "bash", Summary: "run"})
	waitFor(t, "pending request", func() bool { return len(svc.Pending()) == 1 })
	if got := notifications.Load(); got != 1 {
		t.Fatalf("notifications = %d, want 1", got)
	}
	if mode := snapshotMode(t, svc); mode != ModeAsk {
		t.Fatalf("Mode = %q, want ask", mode)
	}
	pending := svc.Pending()
	if err := svc.Resolve(pending[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result := waitResult(t, results); result.err != nil || result.decision != contracts.ApprovalAllow {
		t.Fatalf("Request result = %+v, want allow", result)
	}
}

func TestPermissionFullAllowsWithoutQueueing(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionFull)
	svc, _ := newPermissionService(t, ModeAsk, settings)
	notifications := atomic.Int32{}
	if _, err := svc.Subscribe(func() { notifications.Add(1) }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"})
	if err != nil || decision != contracts.ApprovalAllow {
		t.Fatalf("Request = %q/%v, want allow", decision, err)
	}
	if pending := svc.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %v, want none", pending)
	}
	if got := notifications.Load(); got != 0 {
		t.Fatalf("notifications = %d, want 0", got)
	}
	if mode := snapshotMode(t, svc); mode != ModeAuto {
		t.Fatalf("Mode = %q, want auto for full", mode)
	}
}

func TestPermissionWorkspaceAllowsReadOnlyToolsOnly(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionWorkspace)
	svc, _ := newPermissionService(t, ModeAsk, settings)
	notifications := atomic.Int32{}
	if _, err := svc.Subscribe(func() { notifications.Add(1) }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	for _, tool := range []string{"read_file", "list_dir", "glob", "grep"} {
		decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: tool})
		if err != nil || decision != contracts.ApprovalAllow {
			t.Fatalf("%s decision = %q/%v, want allow", tool, decision, err)
		}
	}
	if pending := svc.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %v, want none after read-only tools", pending)
	}
	if got := notifications.Load(); got != 0 {
		t.Fatalf("notifications = %d, want 0 for the read-only tools", got)
	}
	if mode := snapshotMode(t, svc); mode != contracts.PermissionWorkspace {
		t.Fatalf("Mode = %q, want workspace", mode)
	}

	// A mutating tool still queues.
	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "bash"})
	waitFor(t, "bash pending", func() bool { return len(svc.Pending()) == 1 })
	if got := notifications.Load(); got != 1 {
		t.Fatalf("notifications = %d, want 1 after bash", got)
	}
	pending := svc.Pending()
	if err := svc.Resolve(pending[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result := waitResult(t, results); result.decision != contracts.ApprovalAllow {
		t.Fatalf("bash decision = %q, want allow", result.decision)
	}
}

func TestPermissionSwitchBackToAsk(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionFull)
	svc, _ := newPermissionService(t, ModeAsk, settings)

	if decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"}); err != nil ||
		decision != contracts.ApprovalAllow {
		t.Fatalf("full Request = %q/%v, want allow", decision, err)
	}

	settings.setPermission(contracts.PermissionAsk)
	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "bash"})
	waitFor(t, "ask pending after switch", func() bool { return len(svc.Pending()) == 1 })
	if mode := snapshotMode(t, svc); mode != ModeAsk {
		t.Fatalf("Mode after switching back = %q, want ask", mode)
	}
	pending := svc.Pending()
	if err := svc.Resolve(pending[0].ID, contracts.ApprovalDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result := waitResult(t, results); result.decision != contracts.ApprovalDeny {
		t.Fatalf("decision = %q, want deny", result.decision)
	}
}

func TestPermissionUnknownSettingFallsBackToAsk(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission("weird-mode")
	svc, _ := newPermissionService(t, ModeAsk, settings)

	if mode := snapshotMode(t, svc); mode != ModeAsk {
		t.Fatalf("Mode = %q, want ask for unknown setting", mode)
	}
	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "read_file"})
	waitFor(t, "unknown-mode pending", func() bool { return len(svc.Pending()) == 1 })
	pending := svc.Pending()
	if err := svc.Resolve(pending[0].ID, contracts.ApprovalAllow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_ = waitResult(t, results)
}

func TestPermissionNoSettingsUsesConfigMode(t *testing.T) {
	svc, err := newServiceWithSettings(ModeAuto, &fakeLogger{}, nil)
	if err != nil {
		t.Fatalf("newServiceWithSettings: %v", err)
	}
	if mode := svc.Mode(); mode != ModeAuto {
		t.Fatalf("Mode = %q, want auto (config)", mode)
	}
	if decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"}); err != nil ||
		decision != contracts.ApprovalAllow {
		t.Fatalf("Request = %q/%v, want allow", decision, err)
	}
}

func TestPermissionNeverStillDenies(t *testing.T) {
	settings := newFakeSettings(nil)
	svc, _ := newPermissionService(t, ModeNever, settings)
	if mode := snapshotMode(t, svc); mode != ModeNever {
		t.Fatalf("Mode = %q, want never", mode)
	}
	if decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"}); err != nil ||
		decision != contracts.ApprovalDeny {
		t.Fatalf("Request = %q/%v, want deny", decision, err)
	}

	// A non-empty full setting overrides the assembly never mode.
	settings.setPermission(contracts.PermissionFull)
	if decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"}); err != nil ||
		decision != contracts.ApprovalAllow {
		t.Fatalf("Request with full setting = %q/%v, want allow", decision, err)
	}
}

func TestPermissionAutoSettingAllows(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(ModeAuto)
	svc, _ := newPermissionService(t, ModeAsk, settings)
	if decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: "bash"}); err != nil ||
		decision != contracts.ApprovalAllow {
		t.Fatalf("Request = %q/%v, want allow for legacy auto setting", decision, err)
	}
}

func TestPermissionModeReflectsSettings(t *testing.T) {
	settings := newFakeSettings(nil)
	svc, _ := newPermissionService(t, ModeAsk, settings)
	cases := []struct {
		setting string
		want    string
	}{
		{"", ModeAsk},
		{contracts.PermissionAsk, ModeAsk},
		{contracts.PermissionWorkspace, contracts.PermissionWorkspace},
		{contracts.PermissionFull, ModeAuto},
		{ModeAuto, ModeAuto},
		{"unknown", ModeAsk},
	}
	for _, tc := range cases {
		if tc.setting == "" {
			settings.mu.Lock()
			delete(settings.values, contracts.SettingAgentPermission)
			settings.mu.Unlock()
		} else {
			settings.setPermission(tc.setting)
		}
		if got := svc.Mode(); got != tc.want {
			t.Fatalf("Mode with setting %q = %q, want %q", tc.setting, got, tc.want)
		}
	}
}

// TestPermissionConcurrentSwitchAndRequest exercises the settings read on a
// thread-safe store while the permission flips concurrently (-race catches an
// unsynchronized read).
func TestPermissionConcurrentSwitchAndRequest(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionAsk})
	svc, _ := newPermissionService(t, ModeAsk, settings)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The toggler flips the permission while request goroutines read it; the
	// -race build flags any unsynchronized access.
	stop := make(chan struct{})
	var toggler sync.WaitGroup
	toggler.Add(1)
	go func() {
		defer toggler.Done()
		modes := []string{contracts.PermissionAsk, contracts.PermissionWorkspace, contracts.PermissionFull}
		for index := 0; ; index++ {
			select {
			case <-stop:
				return
			default:
				settings.setPermission(modes[index%len(modes)])
			}
		}
	}()

	const requests = 40
	decisions := make(chan contracts.ApprovalDecision, requests)
	var requesters sync.WaitGroup
	for range requests {
		requesters.Add(1)
		go func() {
			defer requesters.Done()
			decision, err := svc.Request(ctx, contracts.ApprovalRequest{Tool: "bash"})
			if err != nil {
				decisions <- ""
				return
			}
			decisions <- decision
		}()
	}

	requestsDone := make(chan struct{})
	go func() {
		requesters.Wait()
		close(requestsDone)
	}()
	resolved := 0
	for {
		for _, request := range svc.Pending() {
			if err := svc.Resolve(request.ID, contracts.ApprovalAllow); err == nil {
				resolved++
			}
		}
		select {
		case <-requestsDone:
			close(stop)
			toggler.Wait()
			for _, request := range svc.Pending() {
				if err := svc.Resolve(request.ID, contracts.ApprovalAllow); err == nil {
					resolved++
				}
			}
			close(decisions)
			allowed := 0
			for decision := range decisions {
				if decision == contracts.ApprovalAllow {
					allowed++
				}
			}
			if allowed+resolved < requests {
				t.Fatalf("resolved %d queued and %d immediate decisions for %d requests", resolved, allowed, requests)
			}
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
