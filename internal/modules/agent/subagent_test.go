package agent

// Tests for the synchronous subagent delegation seam: service.Run, the two
// registered tools, provider/model propagation, fork guards and concurrency.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newSubagentModuleService activates the agent module and returns both the
// concrete service and the context (for KeySubagents).
func newSubagentModuleService(t *testing.T) (*service, cordis.Context) {
	t.Helper()
	ctx := cordis.Root()
	registry := rigtool.NewRegistry()
	settings := newFakeSettings(nil)
	logger := &fakeLogger{}
	store := newFakeStore()
	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(settings))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(logger))
	provide(t, ctx, contracts.KeyStore, contracts.Store(store))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	provide(t, ctx, contracts.KeyDataDir, t.TempDir())
	if err := Module().Factory(ctx, Config{}); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	chat, err := ctx.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require KeyChat: %v", err)
	}
	svc, ok := chat.(*service)
	if !ok {
		t.Fatalf("KeyChat = %T, want *service", chat)
	}
	if _, err := ctx.Require(contracts.KeySubagents); err != nil {
		t.Fatalf("require KeySubagents: %v", err)
	}
	return svc, ctx
}

// registryTool returns a registered tool by name.
func registryTool(t *testing.T, registry *rigtool.Registry, name string) rig.Tool {
	t.Helper()
	for _, tool := range registry.Tools() {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return nil
}

func TestRunSubagentDelegatesSynchronously(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	result, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "  summarize the repo  "})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "ok" {
		t.Fatalf("result text = %q, want the subagent output", result.Text)
	}
	if result.Name == "" || result.AgentID == "" || result.AgentID != result.Name {
		t.Fatalf("result identity = %+v", result)
	}
	if !strings.HasPrefix(result.Name, "sub-") {
		t.Fatalf("auto name = %q, want sub-<n>", result.Name)
	}
	request := provider.lastRequest()
	if request.Model == "" {
		t.Fatal("subagent provider request has an empty model")
	}
	provider.mu.Lock()
	calls := len(provider.requests)
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider calls = %d, want exactly one subagent delegation", calls)
	}
}

func TestRunSubagentValidationAndForkGuard(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)

	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "   "}); err == nil ||
		!strings.Contains(err.Error(), "prompt") {
		t.Fatalf("Run(blank prompt) = %v, want prompt error", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Run(cancelled, contracts.SubagentRequest{Prompt: "work"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled ctx) = %v, want context.Canceled", err)
	}
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "work", Fork: true}); err == nil ||
		!strings.Contains(err.Error(), "active session") {
		t.Fatalf("Run(fork without session) = %v, want active-session error", err)
	}
	if _, err := svc.NewSession("parent"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Fork with an active session (empty history) is allowed.
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "fork work", Fork: true}); err != nil {
		t.Fatalf("Run(fork with active session): %v", err)
	}
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "bad budget", MaxIterations: -1}); err == nil {
		t.Fatal("Run(negative MaxIterations) returned nil error")
	}
}

func TestRunSubagentModelOverrideAndConcurrency(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)

	type outcome struct {
		result contracts.SubagentResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, model := range []string{"model-a", "model-b"} {
		model := model
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "work " + model, Model: model})
			outcomes <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)
	names := make(map[string]bool)
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent Run: %v", outcome.err)
		}
		if outcome.result.Name == "" {
			t.Fatal("concurrent Run returned an empty name")
		}
		names[outcome.result.Name] = true
	}
	if len(names) != 2 {
		t.Fatalf("subagent names = %v, want unique auto names", names)
	}
	provider.mu.Lock()
	var models []string
	for _, request := range provider.requests {
		models = append(models, request.Model)
	}
	provider.mu.Unlock()
	seen := map[string]bool{}
	for _, model := range models {
		seen[model] = true
	}
	for _, want := range []string{"model-a", "model-b"} {
		if !seen[want] {
			t.Fatalf("provider models = %v, want both overrides", models)
		}
	}
}

func TestSubagentToolsRegisteredAndInvokable(t *testing.T) {
	svc, _ := newSubagentModuleService(t)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	for _, name := range []string{"subagent", "subagent_fork"} {
		tool := registryTool(t, svc.registry, name)
		if strings.TrimSpace(tool.Description()) == "" {
			t.Fatalf("%s has an empty description", name)
		}
		if tool.Schema()["type"] != "object" {
			t.Fatalf("%s schema type = %v, want object", name, tool.Schema()["type"])
		}
	}

	subagent := registryTool(t, svc.registry, "subagent")
	result, err := subagent.Invoke(context.Background(), rig.ToolCall{
		ID: "call-1", Name: "subagent",
		Arguments: `{"prompt":"do the thing","model":"model-override","max_iterations":3}`,
	})
	if err != nil {
		t.Fatalf("subagent Invoke: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "ok") || !strings.Contains(result.Content, "sub-") {
		t.Fatalf("subagent result = %+v", result)
	}
	if request := provider.lastRequest(); request.Model != "model-override" {
		t.Fatalf("subagent model = %q, want override", request.Model)
	}

	fork := registryTool(t, svc.registry, "subagent_fork")
	result, err = fork.Invoke(context.Background(), rig.ToolCall{
		ID: "call-2", Name: "subagent_fork", Arguments: `{"prompt":"fork the thing"}`,
	})
	if err != nil {
		t.Fatalf("subagent_fork Invoke: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "active session") {
		t.Fatalf("fork without active session = %+v, want an error result", result)
	}
	if _, err := svc.NewSession("parent"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	result, err = fork.Invoke(context.Background(), rig.ToolCall{
		ID: "call-3", Name: "subagent_fork", Arguments: `{"prompt":"fork the thing"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("fork with active session = %+v, %v", result, err)
	}

	if _, err := subagent.Invoke(context.Background(), rig.ToolCall{
		ID: "call-4", Name: "subagent", Arguments: `not-json`,
	}); err == nil {
		t.Fatal("subagent Invoke(invalid JSON) returned nil error")
	}
	if result, err := subagent.Invoke(context.Background(), rig.ToolCall{
		ID: "call-5", Name: "subagent", Arguments: `{"prompt":"   "}`,
	}); err != nil || !result.IsError || !strings.Contains(result.Content, "prompt") {
		t.Fatalf("subagent Invoke(blank prompt) = %+v, %v", result, err)
	}
}

func TestSubagentExcludedTools(t *testing.T) {
	names := subagentExcludedTools()
	want := []string{"subagent", "subagent_fork", "workflow", "team", "ralph", "todo", "spawn_teammate"}
	for _, name := range want {
		found := false
		for _, excluded := range names {
			if excluded == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("excluded tools %v lack %q", names, name)
		}
	}
	// The helper must return a fresh slice: mutating it cannot change the next
	// child's exclusions.
	names[0] = "mutated"
	again := subagentExcludedTools()
	if len(again) == 0 || again[0] == "mutated" {
		t.Fatalf("subagentExcludedTools leaked mutable state: %v", again)
	}
}

func TestSubagentToolsUnregisterOnDispose(t *testing.T) {
	ctx := cordis.Root()
	registry := rigtool.NewRegistry()
	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(newFakeSettings(nil)))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(&fakeLogger{}))
	provide(t, ctx, contracts.KeyStore, contracts.Store(newFakeStore()))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	provide(t, ctx, contracts.KeyDataDir, t.TempDir())
	if err := Module().Factory(ctx, Config{}); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	for _, name := range []string{"subagent", "subagent_fork"} {
		registryTool(t, registry, name)
	}
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	for _, tool := range registry.Tools() {
		if tool.Name() == "subagent" || tool.Name() == "subagent_fork" {
			t.Fatalf("tool %q survived module disposal", tool.Name())
		}
	}
}

// --- synchronous subagent activity registry (inspector 团队 pane) ------------

// newActivityService builds a service whose provider is scripted by the test;
// the subagent activity registry is the real one.
func newActivityService(t *testing.T, provider rig.Provider) *service {
	t.Helper()
	svc := newService(Config{}, rigtool.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc
}

// gatedSubagentProvider blocks the child's first model call until the test
// releases it or the run's context is cancelled, so the running state is
// observable.
type gatedSubagentProvider struct {
	entered chan struct{}
	release chan struct{}
}

func newGatedSubagentProvider() *gatedSubagentProvider {
	return &gatedSubagentProvider{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (p *gatedSubagentProvider) Name() string { return "gated" }

func (p *gatedSubagentProvider) Stream(ctx context.Context, _ rig.Request) (rig.Stream, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return &scriptedStream{chunks: []rig.Chunk{
			{Delta: "ok"},
			{FinishReason: "stop"},
		}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// waitStarted blocks until the gated provider's first call starts.
func (p *gatedSubagentProvider) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the subagent never reached the provider")
	}
}

// failingSubagentProvider fails the child's first model call.
type failingSubagentProvider struct{ err error }

func (p *failingSubagentProvider) Name() string { return "failing" }

func (p *failingSubagentProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	return nil, p.err
}

// waitForSubagentActivities polls the lister until want accepts a snapshot or
// the timeout expires.
func waitForSubagentActivities(t *testing.T, lister contracts.SubagentActivityLister,
	want func([]contracts.SubagentActivity) bool) []contracts.SubagentActivity {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		activities := lister.SubagentActivities()
		if want(activities) {
			return activities
		}
		if time.Now().After(deadline) {
			t.Fatalf("subagent activities did not settle; last = %+v", activities)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSubagentActivityRunningThenDone covers the visible lifecycle of one
// delegation: it is registered as running while the child works (name, model,
// parent session, bounded prompt and fork flag recorded) and becomes done with
// the child's answer when it returns.
func TestSubagentActivityRunningThenDone(t *testing.T) {
	provider := newGatedSubagentProvider()
	svc := newActivityService(t, provider)
	parentSession, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var notifiedMu sync.Mutex
	notified := 0
	unsubscribe, err := svc.Subscribe(func(string) {
		notifiedMu.Lock()
		notified++
		notifiedMu.Unlock()
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	type outcome struct {
		result contracts.SubagentResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := svc.Run(context.Background(), contracts.SubagentRequest{
			Prompt: "  audit\n  the   repo  ", Name: "研究员", Model: "model-x", Fork: true,
		})
		done <- outcome{result: result, err: runErr}
	}()
	provider.waitStarted(t)

	activities := waitForSubagentActivities(t, svc, func(activities []contracts.SubagentActivity) bool {
		return len(activities) == 1 && activities[0].Status == contracts.SubagentStatusRunning
	})
	running := activities[0]
	if running.Name != "研究员" {
		t.Fatalf("running activity name = %q, want 研究员", running.Name)
	}
	if running.Model != "model-x" {
		t.Fatalf("running activity model = %q, want the request override", running.Model)
	}
	if running.SessionID != parentSession {
		t.Fatalf("running activity session = %q, want %q", running.SessionID, parentSession)
	}
	if running.Prompt != "audit the repo" {
		t.Fatalf("running activity prompt = %q, want the collapsed summary", running.Prompt)
	}
	if !running.Fork {
		t.Fatal("running activity lost the fork flag")
	}
	if running.Started.IsZero() || !running.Finished.IsZero() || running.Detail != "" {
		t.Fatalf("running activity timestamps/detail = %+v", running)
	}
	if running.ID == "" {
		t.Fatal("running activity has no stable ID")
	}

	close(provider.release)
	finished := <-done
	if finished.err != nil {
		t.Fatalf("Run: %v", finished.err)
	}
	if finished.result.Text != "ok" {
		t.Fatalf("Run result = %+v, want the child's answer", finished.result)
	}

	activities = waitForSubagentActivities(t, svc, func(activities []contracts.SubagentActivity) bool {
		return len(activities) == 1 && activities[0].Status == contracts.SubagentStatusDone
	})
	succeeded := activities[0]
	if succeeded.ID != running.ID {
		t.Fatalf("finished activity ID = %q, want the running ID %q", succeeded.ID, running.ID)
	}
	if succeeded.Finished.IsZero() || succeeded.Detail != "ok" {
		t.Fatalf("finished activity = %+v, want a finish time and the answer", succeeded)
	}

	// The inspector redraws on these two notifications (start and finish).
	notifiedMu.Lock()
	calls := notified
	notifiedMu.Unlock()
	if calls < 2 {
		t.Fatalf("subagent Run notified subscribers %d times, want the start and finish frames", calls)
	}
}

// TestSubagentActivityFailure covers a child whose provider fails: the row is
// kept and marked failed with the bounded error text.
func TestSubagentActivityFailure(t *testing.T) {
	svc := newActivityService(t, &failingSubagentProvider{err: errors.New("provider exploded")})
	result, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "fail the task", Name: "失败者"})
	if err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("Run = %+v, %v, want the provider error", result, err)
	}
	activities := svc.SubagentActivities()
	if len(activities) != 1 {
		t.Fatalf("activities = %+v, want the failed delegation", activities)
	}
	failed := activities[0]
	if failed.Status != contracts.SubagentStatusError {
		t.Fatalf("failed activity status = %q, want error", failed.Status)
	}
	if !strings.Contains(failed.Detail, "provider exploded") {
		t.Fatalf("failed activity detail = %q, want the error text", failed.Detail)
	}
	if failed.Finished.IsZero() {
		t.Fatal("failed activity has no finish time")
	}
}

// TestSubagentActivityCancelled covers a cancelled parent context: the row is
// a cancellation (已取消), not a generic failure.
func TestSubagentActivityCancelled(t *testing.T) {
	provider := newGatedSubagentProvider()
	svc := newActivityService(t, provider)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Run(ctx, contracts.SubagentRequest{Prompt: "long task", Name: "可取消"})
		done <- err
	}()
	provider.waitStarted(t)
	waitForSubagentActivities(t, svc, func(activities []contracts.SubagentActivity) bool {
		return len(activities) == 1 && activities[0].Status == contracts.SubagentStatusRunning
	})
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) = %v, want context.Canceled", err)
	}
	activities := waitForSubagentActivities(t, svc, func(activities []contracts.SubagentActivity) bool {
		return len(activities) == 1 && activities[0].Status == contracts.SubagentStatusCancelled
	})
	if activities[0].Finished.IsZero() {
		t.Fatal("cancelled activity has no finish time")
	}
	if activities[0].Detail != "" {
		t.Fatalf("cancelled activity detail = %q, want none", activities[0].Detail)
	}
}

// TestSubagentActivityValidationIsNotTracked pins that requests rejected before
// any run starts (blank prompt, already-cancelled context) leave no row: the
// pane shows delegations, not validation noise.
func TestSubagentActivityValidationIsNotTracked(t *testing.T) {
	svc := newActivityService(t, newGatedSubagentProvider())
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "  "}); err == nil {
		t.Fatal("Run(blank prompt) returned nil error")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Run(cancelled, contracts.SubagentRequest{Prompt: "work"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled context) = %v, want context.Canceled", err)
	}
	if activities := svc.SubagentActivities(); len(activities) != 0 {
		t.Fatalf("activities after validation failures = %+v, want none", activities)
	}
}

// TestSubagentActivityRegistryBoundsFinishedRows covers the pruning bound: the
// newest finished delegations stay, older ones are dropped, and a running row
// is never pruned.
func TestSubagentActivityRegistryBoundsFinishedRows(t *testing.T) {
	var registry subagentActivityRegistry
	registry.start(contracts.SubagentActivity{Name: "长期任务"})
	for index := 0; index < subagentActivityFinishedKeep+4; index++ {
		activity := registry.start(contracts.SubagentActivity{Name: fmt.Sprintf("done-%d", index)})
		registry.finish(activity.ID, contracts.SubagentStatusDone, "ok")
	}
	activities := registry.snapshot()
	finished := 0
	foundRunning := false
	names := make(map[string]bool, len(activities))
	for _, activity := range activities {
		names[activity.Name] = true
		if activity.Status == contracts.SubagentStatusRunning {
			foundRunning = true
			continue
		}
		finished++
	}
	if finished != subagentActivityFinishedKeep {
		t.Fatalf("finished rows = %d, want the %d-row bound", finished, subagentActivityFinishedKeep)
	}
	if !foundRunning || !names["长期任务"] {
		t.Fatalf("running row was pruned: %+v", activities)
	}
	// The oldest finished rows are the ones dropped.
	if names["done-0"] || names["done-3"] {
		t.Fatalf("pruning kept the oldest finished rows: %v", names)
	}
	if !names[fmt.Sprintf("done-%d", subagentActivityFinishedKeep+3)] {
		t.Fatalf("pruning dropped the newest finished row: %v", names)
	}
	// A second finish (defensive) cannot rewrite an already recorded outcome.
	if len(activities) < 2 || activities[1].Status != contracts.SubagentStatusDone {
		t.Fatalf("snapshot = %+v, want the running row then a finished row", activities)
	}
	recorded := activities[1]
	registry.finish(recorded.ID, contracts.SubagentStatusError, "late error")
	for _, activity := range registry.snapshot() {
		if activity.ID == recorded.ID && activity.Status != contracts.SubagentStatusDone {
			t.Fatalf("duplicate finish rewrote a recorded row: %+v", activity)
		}
	}
}

// TestSubagentActivitySnapshotsAreCopies pins that the lister never aliases the
// registry: mutating a snapshot (or its slice) cannot change later snapshots.
func TestSubagentActivitySnapshotsAreCopies(t *testing.T) {
	var registry subagentActivityRegistry
	registry.start(contracts.SubagentActivity{Name: "original", Prompt: "task"})
	first := registry.snapshot()
	if len(first) != 1 {
		t.Fatalf("snapshot = %+v, want one row", first)
	}
	first[0].Name = "mutated"
	first[0].Prompt = "mutated"
	first = append(first, contracts.SubagentActivity{Name: "injected"})

	second := registry.snapshot()
	if len(second) != 1 || second[0].Name != "original" || second[0].Prompt != "task" {
		t.Fatalf("snapshot aliases the registry: %+v", second)
	}
}

// TestSubagentActivityConcurrentAccess exercises the registry from concurrent
// runs and readers (the run goroutines vs. the inspector thread): reads only
// ever see copies, so -race can prove the mutex discipline.
func TestSubagentActivityConcurrentAccess(t *testing.T) {
	var registry subagentActivityRegistry
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		index := index
		wait.Add(2)
		go func() {
			defer wait.Done()
			activity := registry.start(contracts.SubagentActivity{Name: fmt.Sprintf("run-%d", index), Prompt: "work"})
			registry.finish(activity.ID, contracts.SubagentStatusDone, "answer")
		}()
		go func() {
			defer wait.Done()
			for read := 0; read < 20; read++ {
				_ = registry.snapshot()
			}
		}()
	}
	wait.Wait()
	finished := 0
	for _, activity := range registry.snapshot() {
		if activity.Status == contracts.SubagentStatusDone {
			finished++
		}
	}
	if finished == 0 || finished > subagentActivityFinishedKeep {
		t.Fatalf("finished rows after concurrent access = %d, want 1..%d", finished, subagentActivityFinishedKeep)
	}
	// The service satisfies the optional contract the inspector discovers.
	var lister contracts.SubagentActivityLister = newActivityService(t, newGatedSubagentProvider())
	if activities := lister.SubagentActivities(); len(activities) != 0 {
		t.Fatalf("fresh service lister = %+v, want no rows", activities)
	}
}

// TestModuleProvidesSubagentActivityLister pins the inspector wiring: the
// kernel-provided KeySubagents service must also carry the optional activity
// lister the right panel type-asserts (a wrapped provider that does not would
// silently lose the delegation rows).
func TestModuleProvidesSubagentActivityLister(t *testing.T) {
	_, ctx := newSubagentModuleService(t)
	subagents, err := ctx.Require(contracts.KeySubagents)
	if err != nil {
		t.Fatalf("require KeySubagents: %v", err)
	}
	lister, ok := any(subagents).(contracts.SubagentActivityLister)
	if !ok {
		t.Fatalf("provided KeySubagents service %T does not implement contracts.SubagentActivityLister", subagents)
	}
	if activities := lister.SubagentActivities(); len(activities) != 0 {
		t.Fatalf("fresh service activities = %+v, want none", activities)
	}
}
