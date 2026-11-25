package agent

// Tests for the session ⇄ task binding enforcement (M5): a session bound to a
// board task may only call the tools that task's binding names, and every case
// where there is nothing to enforce stays byte-for-byte today's behaviour.
//
// The readability of the denial matters as much as the decision: the reason
// travels through the SDK's optional DenialExplainer channel (rig.Gate carries
// only allow/deny), so these tests assert on it — a model that cannot see why a
// call was refused retries it unchanged.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// taskBindingLease is the lease half the task-binding gate needs: a per-task
// binding map, an injectable read error, and a call counter. It embeds the
// interface instead of stubbing seventeen methods on purpose: any other call the
// gate might make panics, which is the assertion that the gate only reads Tools.
type taskBindingLease struct {
	contracts.TaskLeaseService
	mu       sync.Mutex
	bindings map[string][]string
	err      error
	calls    int
}

func (l *taskBindingLease) Tools(_ context.Context, taskID string) (contracts.TaskTools, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.err != nil {
		return contracts.TaskTools{}, false, l.err
	}
	tools, ok := l.bindings[taskID]
	if !ok {
		return contracts.TaskTools{}, false, nil
	}
	return contracts.TaskTools{TaskID: taskID, Tools: tools}, true, nil
}

// countContaining counts the log lines holding one fragment: the "at most
// once" half of the log assertions (fakeLogger.contains only says "a line
// exists").
func (l *fakeLogger) countContaining(fragment string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			count++
		}
	}
	return count
}

func (l *taskBindingLease) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// newTaskBindingService builds one session over an approval-allowing backend.
func newTaskBindingService(t *testing.T) (*service, string, *fakeSettings, *fakeLogger) {
	t.Helper()
	settings := newFakeSettings(nil)
	logger := &fakeLogger{}
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		logger, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, sessionID, settings, logger
}

// bindSessionTask is the write half, through the public API on purpose.
func bindSessionTask(t *testing.T, svc *service, sessionID, taskID string) {
	t.Helper()
	if err := svc.BindSessionTask(context.Background(), sessionID, taskID); err != nil {
		t.Fatalf("BindSessionTask(%q): %v", taskID, err)
	}
}

// taskGateReason asks the gate for one decision plus its model-visible denial
// reason (empty means "the SDK keeps its historical text").
func taskGateReason(t *testing.T, svc *service, sessionID, name string) (rig.Decision, string) {
	t.Helper()
	tool := &gateTestTool{name: name}
	call := rig.ToolCall{Name: name}
	gate := svc.gate(sessionID)
	decision, err := gate.Approve(context.Background(), tool, call)
	if err != nil {
		t.Fatalf("Approve(%q): %v", name, err)
	}
	explainer, ok := gate.(interface {
		ExplainDenial(rig.Tool, rig.ToolCall) string
	})
	if !ok {
		t.Fatalf("gate %T does not implement the denial explainer channel", gate)
	}
	return decision, explainer.ExplainDenial(tool, call)
}

func TestGateDeniesToolOutsideTaskBinding(t *testing.T) {
	svc, sessionID, _, _ := newTaskBindingService(t)
	svc.taskLease = &taskBindingLease{bindings: map[string][]string{"task-1": {"browser_open"}}}
	bindSessionTask(t, svc, sessionID, "task-1")

	decision, reason := taskGateReason(t, svc, sessionID, "read_file")
	if decision != rig.DecisionDeny {
		t.Fatalf("read_file decision = %q, want deny", decision)
	}
	for _, want := range []string{"任务 task-1", "browser_open", "read_file", "被拒绝"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("denial reason %q is missing %q", reason, want)
		}
	}
}

func TestGateAllowsToolInsideTaskBinding(t *testing.T) {
	svc, sessionID, _, _ := newTaskBindingService(t)
	svc.taskLease = &taskBindingLease{bindings: map[string][]string{"task-1": {"browser_open", "read_file"}}}
	bindSessionTask(t, svc, sessionID, "task-1")

	decision, reason := taskGateReason(t, svc, sessionID, "browser_open")
	if decision != rig.DecisionAllow {
		t.Fatalf("browser_open decision = %q, want allow", decision)
	}
	if reason != "" {
		t.Fatalf("an allowed call carried a denial reason: %q", reason)
	}
}

// TestTaskBindingIsACeilingNotAHint proves the check runs before the branches
// that can allow on their own: a full session permission and a remembered
// "always allow" would both skip the dialog, and must still lose to the task.
func TestTaskBindingIsACeilingNotAHint(t *testing.T) {
	svc, sessionID, settings, _ := newTaskBindingService(t)
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	svc.rememberAllowedTool(sessionID, "bash")
	svc.taskLease = &taskBindingLease{bindings: map[string][]string{"task-1": {"read_file"}}}
	bindSessionTask(t, svc, sessionID, "task-1")
	if settings.Get(contracts.SettingAgentMode, "") != "" {
		t.Fatal("the plan-mode setting must stay unset in this test")
	}

	if decision, reason := taskGateReason(t, svc, sessionID, "bash"); decision != rig.DecisionDeny ||
		!strings.Contains(reason, "bash") {
		t.Fatalf("bash = (%q, %q), want deny with the tool named", decision, reason)
	}
	// The unremembered, un-overridden tool in the binding still passes.
	if decision, _ := taskGateReason(t, svc, sessionID, "read_file"); decision != rig.DecisionAllow {
		t.Fatalf("read_file decision = %q, want allow", decision)
	}
}

// TestTaskBindingBeatsPlanModeWhitelist: plan mode has its own whitelist, and it
// must not widen a task's policy either (the check sits in front of it).
func TestTaskBindingBeatsPlanModeWhitelist(t *testing.T) {
	svc, sessionID, settings, _ := newTaskBindingService(t)
	settings.Set(contracts.SettingAgentMode, contracts.AgentModePlan)
	svc.taskLease = &taskBindingLease{bindings: map[string][]string{"task-1": {"browser_open"}}}
	bindSessionTask(t, svc, sessionID, "task-1")

	decision, reason := taskGateReason(t, svc, sessionID, "read_file")
	if decision != rig.DecisionDeny || !strings.Contains(reason, "任务 task-1") {
		t.Fatalf("plan-mode read_file = (%q, %q), want the task binding's denial", decision, reason)
	}
}

// TestTaskBindingDoesNotGrantApproval: the binding is a subset filter, never an
// authorization. A tool the task allows still goes through the approval policy.
func TestTaskBindingDoesNotGrantApproval(t *testing.T) {
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(newFakeSettings(nil)),
		&fakeLogger{}, newFakeStore(), denyApprovals{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.taskLease = &taskBindingLease{bindings: map[string][]string{"task-1": {"bash"}}}
	bindSessionTask(t, svc, sessionID, "task-1")

	if decision, _ := taskGateReason(t, svc, sessionID, "bash"); decision != rig.DecisionDeny {
		t.Fatalf("bash decision = %q, want the approvals backend to deny it", decision)
	}
}

// TestGateUnknownTaskOrMissingBindingIsInert: a stale row (task deleted) and a
// task nobody bound tools to both keep today's flow.
func TestGateUnknownTaskOrMissingBindingIsInert(t *testing.T) {
	svc, sessionID, _, logger := newTaskBindingService(t)
	lease := &taskBindingLease{bindings: map[string][]string{"task-open": {}}}
	svc.taskLease = lease

	bindSessionTask(t, svc, sessionID, "task-gone")
	decision, reason := taskGateReason(t, svc, sessionID, "write_file")
	if decision != rig.DecisionAllow || reason != "" {
		t.Fatalf("unknown task = (%q, %q), want the unchanged approval flow", decision, reason)
	}

	bindSessionTask(t, svc, sessionID, "task-open")
	decision, reason = taskGateReason(t, svc, sessionID, "write_file")
	if decision != rig.DecisionAllow || reason != "" {
		t.Fatalf("task without binding = (%q, %q), want the unchanged approval flow", decision, reason)
	}
	if lease.callCount() < 2 {
		t.Fatalf("lease Tools calls = %d, want the gate to have asked each time", lease.callCount())
	}
	if !logger.contains("task-gone") {
		t.Fatalf("the inert unknown-task case logged nothing")
	}
}

// TestGateWithoutLeaseServiceIsInertAndLogsOnce: an assembly without the lease
// module records a binding it cannot enforce. That must never fail a run, and it
// must not log per tool call either.
func TestGateWithoutLeaseServiceIsInertAndLogsOnce(t *testing.T) {
	svc, sessionID, _, logger := newTaskBindingService(t)
	svc.taskLease = nil
	bindSessionTask(t, svc, sessionID, "task-1")

	for i := 0; i < 3; i++ {
		if decision, reason := taskGateReason(t, svc, sessionID, "write_file"); decision != rig.DecisionAllow || reason != "" {
			t.Fatalf("call %d = (%q, %q), want the unchanged approval flow", i, decision, reason)
		}
	}
	if got := logger.countContaining("no task lease service"); got != 1 {
		t.Fatalf("log lines for the missing lease service = %d, want exactly 1", got)
	}
}

// TestLeaseReadErrorIsInert: a broken lease read must not brick the session.
func TestLeaseReadErrorIsInert(t *testing.T) {
	svc, sessionID, _, _ := newTaskBindingService(t)
	svc.taskLease = &taskBindingLease{err: errors.New("board unavailable")}
	bindSessionTask(t, svc, sessionID, "task-1")

	if decision, reason := taskGateReason(t, svc, sessionID, "write_file"); decision != rig.DecisionAllow || reason != "" {
		t.Fatalf("lease error = (%q, %q), want the unchanged approval flow", decision, reason)
	}
}

func TestBindSessionTaskPersistsValidatesAndClears(t *testing.T) {
	svc, sessionID, settings, _ := newTaskBindingService(t)
	key := contracts.SessionBoundTaskSettingKey(sessionID)

	if bound, err := svc.SessionTask(sessionID); err != nil || bound != "" {
		t.Fatalf("SessionTask before binding = (%q, %v), want empty", bound, err)
	}
	bindSessionTask(t, svc, " "+sessionID+" ", "  task-1  ")
	if got := settings.Get(key, ""); got != "task-1" {
		t.Fatalf("settings row = %q, want the trimmed task-1", got)
	}
	if bound, err := svc.SessionTask(sessionID); err != nil || bound != "task-1" {
		t.Fatalf("SessionTask = (%q, %v), want task-1", bound, err)
	}

	// An empty taskId clears the row (the un-bind path).
	bindSessionTask(t, svc, sessionID, "   ")
	if got := settings.Get(key, ""); got != "" {
		t.Fatalf("settings row after clear = %q, want empty", got)
	}
	if bound, err := svc.SessionTask(sessionID); err != nil || bound != "" {
		t.Fatalf("SessionTask after clear = (%q, %v), want empty", bound, err)
	}

	// Malformed requests are refused and leave the row alone.
	for _, taskID := range []string{"bad task", "task\n1", strings.Repeat("t", contracts.MaxSessionBoundTaskLen+1)} {
		if err := svc.BindSessionTask(context.Background(), sessionID, taskID); !errors.Is(err, contracts.ErrSessionTaskBindingInvalid) {
			t.Fatalf("BindSessionTask(%q) = %v, want ErrSessionTaskBindingInvalid", taskID, err)
		}
	}
	if err := svc.BindSessionTask(context.Background(), "  ", "task-1"); !errors.Is(err, contracts.ErrSessionTaskBindingInvalid) {
		t.Fatalf("BindSessionTask on an empty session = %v, want ErrSessionTaskBindingInvalid", err)
	}
	if _, err := svc.SessionTask(" "); !errors.Is(err, contracts.ErrSessionTaskBindingInvalid) {
		t.Fatalf("SessionTask on an empty session = %v, want ErrSessionTaskBindingInvalid", err)
	}

	// A stored row that is no longer a valid id still reads back (never an
	// error): the binding is inert at the gate, not a broken conversation.
	settings.Set(key, "bad task")
	if bound, err := svc.SessionTask(sessionID); err != nil || bound != "bad task" {
		t.Fatalf("SessionTask with a legacy row = (%q, %v)", bound, err)
	}
}

func TestBindSessionTaskWithoutSettings(t *testing.T) {
	svc := newService(Config{}, rigtools.NewRegistry(), nil, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	if err := svc.BindSessionTask(context.Background(), "sess-1", "task-1"); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("BindSessionTask = %v, want ErrSettingsUnavailable", err)
	}
	if _, err := svc.SessionTask("sess-1"); !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("SessionTask = %v, want ErrSettingsUnavailable", err)
	}
}

func TestClearingTheBindingRestoresTheGate(t *testing.T) {
	svc, sessionID, _, _ := newTaskBindingService(t)
	svc.taskLease = &taskBindingLease{bindings: map[string][]string{"task-1": {"browser_open"}}}
	bindSessionTask(t, svc, sessionID, "task-1")

	if decision, _ := taskGateReason(t, svc, sessionID, "write_file"); decision != rig.DecisionDeny {
		t.Fatalf("bound session write_file = %q, want deny", decision)
	}
	bindSessionTask(t, svc, sessionID, "")
	if decision, reason := taskGateReason(t, svc, sessionID, "write_file"); decision != rig.DecisionAllow || reason != "" {
		t.Fatalf("after clear = (%q, %q), want the unchanged approval flow", decision, reason)
	}
}
