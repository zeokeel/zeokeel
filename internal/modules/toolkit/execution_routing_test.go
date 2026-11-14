package toolkit

// Tests for the execution-domain persistence: retained builds, ID routing across
// workspace/permission rebuilds, list merging, cross-build notices and durable
// execution lifecycle events.

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// executionTestStore captures durable events (only AppendEvent is used by the
// toolkit's execution recorder).
type executionTestStore struct {
	contracts.Store
	mu     sync.Mutex
	events []rig.SessionEvent
}

func (s *executionTestStore) AppendEvent(_ context.Context, event rig.SessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *executionTestStore) byType(eventType string) []rig.SessionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rig.SessionEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

// executionCall invokes one proxy with the session stamped into the context.
func executionCall(t *testing.T, proxy rig.Tool, sessionID, arguments string) rig.ToolResult {
	t.Helper()
	ctx := context.Background()
	if sessionID != "" {
		ctx = contracts.WithAgentSession(ctx, sessionID)
	}
	result, err := proxy.Invoke(ctx, rig.ToolCall{ID: "call-1", Name: proxy.Name(), Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", proxy.Name(), err)
	}
	return result
}

// executionArgumentID extracts one id from a proxy result.
func executionArgumentID(t *testing.T, result rig.ToolResult, key string) string {
	t.Helper()
	id := executionResultID(result.Content, key)
	if id == "" {
		t.Fatalf("result = %q, want a %q line", result.Content, key)
	}
	return id
}

// waitJobOutput polls job_output until the output contains want or the deadline
// passes.
func waitJobOutput(t *testing.T, proxy rig.Tool, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result := executionCall(t, proxy, "", `{"job_id":"`+jobID+`"}`)
		if !result.IsError && strings.Contains(result.Content, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job_output(%s) never contained %q", jobID, want)
}

// TestJobRoutesAcrossWorkspaceSwitch covers the core rule: a job started before
// a workspace switch stays readable and killable by id afterwards.
func TestJobRoutesAcrossWorkspaceSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())

	started := executionCall(t, findProxy(t, proxies, "job_start"), "sess-job", `{"command":"tail -f /dev/null"}`)
	jobID := executionArgumentID(t, started, "job_id")

	current = rootB
	scopedB := group.forRoot()
	if scopedB.root != rootB {
		t.Fatalf("new build root = %q, want %q", scopedB.root, rootB)
	}
	group.mu.Lock()
	owner := group.jobOwners[jobID]
	group.mu.Unlock()
	if owner == nil || owner.root != rootA {
		t.Fatalf("job owner = %+v, want the root-A build", owner)
	}

	output := executionCall(t, findProxy(t, proxies, "job_output"), "sess-job", `{"job_id":"`+jobID+`"}`)
	if output.IsError || !strings.Contains(output.Content, "job_id: "+jobID) {
		t.Fatalf("job_output after switch = %q, want the retained job", output.Content)
	}
	killed := executionCall(t, findProxy(t, proxies, "job_kill"), "sess-job", `{"job_id":"`+jobID+`"}`)
	if killed.IsError {
		t.Fatalf("job_kill after switch = %q", killed.Content)
	}
}

// TestTerminalRoutesAcrossWorkspaceSwitch covers the terminal half: open before
// the switch, then send/read/close from the new build.
func TestTerminalRoutesAcrossWorkspaceSwitch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("terminal pipe transport needs /bin/sh")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())

	opened := executionCall(t, findProxy(t, proxies, "terminal_open"), "sess-term", `{"type":"pipe","name":"main"}`)
	sessionID := executionArgumentID(t, opened, "session_id")

	current = rootB
	group.forRoot()
	group.mu.Lock()
	owner := group.terminalOwners[sessionID]
	group.mu.Unlock()
	if owner == nil || owner.root != rootA {
		t.Fatalf("terminal owner = %+v, want the root-A build", owner)
	}

	sent := executionCall(t, findProxy(t, proxies, "terminal_send"), "sess-term",
		`{"sessionId":"`+sessionID+`","text":"echo routed-ok\n"}`)
	if sent.IsError {
		t.Fatalf("terminal_send after switch = %q", sent.Content)
	}
	read := executionCall(t, findProxy(t, proxies, "terminal_read"), "sess-term",
		`{"sessionId":"`+sessionID+`","offset":0,"count":50}`)
	if read.IsError || !strings.Contains(read.Content, "routed-ok") {
		t.Fatalf("terminal_read after switch = %q, want routed-ok", read.Content)
	}
	closed := executionCall(t, findProxy(t, proxies, "terminal_close"), "sess-term", `{"sessionId":"`+sessionID+`"}`)
	if closed.IsError {
		t.Fatalf("terminal_close after switch = %q", closed.Content)
	}
	// The routing entry is dropped once closed: a later send falls back to the
	// current build and reports Rig's unknown-session error.
	again := executionCall(t, findProxy(t, proxies, "terminal_send"), "sess-term",
		`{"sessionId":"`+sessionID+`","text":"echo late\n"}`)
	if !again.IsError {
		t.Fatalf("terminal_send after close = %q, want an unknown-session error", again.Content)
	}
}

// TestExecutionListsMergeOnlyCallerRootBuilds covers the list semantics: jobs
// from every retained build of the caller's root are merged (permission switch)
// with an accurate count, while another root's jobs are never mixed in.
func TestExecutionListsMergeOnlyCallerRootBuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	mode := sandbox.ModeWorkspaceWrite
	group := newRebindGroup(Config{}, 5*time.Second,
		func() string { return current },
		func() (sandbox.Mode, sandbox.Runner) { return mode, nil }, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())

	jobA1 := executionArgumentID(t, executionCall(t, findProxy(t, proxies, "job_start"), "sess-list",
		`{"command":"tail -f /dev/null"}`), "job_id")

	// A permission-preset change builds a new identity for the SAME root; the
	// old build is retained and its job must stay listed for this workspace.
	mode = sandbox.ModeDangerFullAccess
	group.forRoot()
	jobA2 := executionArgumentID(t, executionCall(t, findProxy(t, proxies, "job_start"), "sess-list",
		`{"command":"tail -f /dev/null"}`), "job_id")
	group.mu.Lock()
	secondOwner := group.jobOwners[jobA2]
	firstOwner := group.jobOwners[jobA1]
	group.mu.Unlock()
	if firstOwner == secondOwner || secondOwner == nil || secondOwner.mode != sandbox.ModeDangerFullAccess {
		t.Fatalf("new job not started under the new policy build: first=%+v second=%+v", firstOwner, secondOwner)
	}

	merged := executionCall(t, findProxy(t, proxies, "job_list"), "sess-list", `{}`)
	if !strings.Contains(merged.Content, "jobs: 2\n") ||
		!strings.Contains(merged.Content, jobA1) || !strings.Contains(merged.Content, jobA2) {
		t.Fatalf("merged job_list = %q, want both root-A jobs and count 2", merged.Content)
	}

	// Switch the current workspace to root B: its list must not leak root-A jobs.
	current = rootB
	group.forRoot()
	other := executionCall(t, findProxy(t, proxies, "job_list"), "sess-list", `{}`)
	if strings.Contains(other.Content, jobA1) || strings.Contains(other.Content, jobA2) {
		t.Fatalf("root-B job_list = %q, must not list another root's jobs", other.Content)
	}
}

// TestTerminalListMergesRetainedBuilds covers terminal_list across two builds of
// the same root (permission rebuild): both sessions are listed.
func TestTerminalListMergesRetainedBuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("terminal pipe transport needs /bin/sh")
	}
	root := t.TempDir()
	mode := sandbox.ModeWorkspaceWrite
	group := newRebindGroup(Config{}, 5*time.Second,
		func() string { return root },
		func() (sandbox.Mode, sandbox.Runner) { return mode, nil }, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())

	first := executionArgumentID(t, executionCall(t, findProxy(t, proxies, "terminal_open"), "sess-term-list",
		`{"type":"pipe","name":"one"}`), "session_id")
	mode = sandbox.ModeDangerFullAccess
	group.forRoot()
	second := executionArgumentID(t, executionCall(t, findProxy(t, proxies, "terminal_open"), "sess-term-list",
		`{"type":"pipe","name":"two"}`), "session_id")

	listed := executionCall(t, findProxy(t, proxies, "terminal_list"), "sess-term-list", `{}`)
	if listed.IsError || !strings.Contains(listed.Content, first) || !strings.Contains(listed.Content, second) {
		t.Fatalf("merged terminal_list = %q, want both sessions %s/%s", listed.Content, first, second)
	}
}

// TestNewJobsUseTheNewPolicy covers the policy half: after a permission rebuild
// the retained job is still controllable, while a NEW job is built under the new
// (read-only) policy and is refused by it.
func TestNewJobsUseTheNewPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	root := t.TempDir()
	mode := sandbox.ModeWorkspaceWrite
	group := newRebindGroup(Config{}, 5*time.Second,
		func() string { return root },
		func() (sandbox.Mode, sandbox.Runner) { return mode, nil }, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())
	start := findProxy(t, proxies, "job_start")
	jobID := executionArgumentID(t, executionCall(t, start, "", `{"command":"tail -f /dev/null"}`), "job_id")

	mode = sandbox.ModeReadOnly
	group.forRoot()
	refused := executionCall(t, start, "", `{"command":"true"}`)
	if !refused.IsError {
		t.Fatalf("job_start under the read-only rebuild = %q, want the policy refusal", refused.Content)
	}
	// The retained pre-rebuild job is still controllable by id.
	output := executionCall(t, findProxy(t, proxies, "job_output"), "", `{"job_id":"`+jobID+`"}`)
	if output.IsError || !strings.Contains(output.Content, jobID) {
		t.Fatalf("old job after read-only rebuild = %q, want it still reachable", output.Content)
	}
}

// failingExecutionStore fails every durable event append.
type failingExecutionStore struct{ contracts.Store }

func (failingExecutionStore) AppendEvent(context.Context, rig.SessionEvent) error {
	return context.DeadlineExceeded
}

// TestExecutionEventsFailuresDoNotAffectTools covers the audit rule: a store
// error never changes the tool result.
func TestExecutionEventsFailuresDoNotAffectTools(t *testing.T) {
	root := t.TempDir()
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, nil, nil)
	group.events = &executionEvents{store: failingExecutionStore{}}
	defer group.close()
	proxies := group.proxies(group.forRoot())
	if listed := executionCall(t, findProxy(t, proxies, "job_list"), "sess-fail", `{}`); listed.IsError {
		t.Fatalf("job_list with a failing event store = %q", listed.Content)
	}
}

// TestExecutionListUsesContextWorkspaceRoot covers the per-run root override:
// concurrent sessions in different projects list only their own root's jobs.
func TestExecutionListUsesContextWorkspaceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return rootA }, nil, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())

	startCtx := contracts.WithWorkspaceRoot(context.Background(), rootA)
	started, err := findProxy(t, proxies, "job_start").Invoke(startCtx, rig.ToolCall{
		Name: "job_start", Arguments: `{"command":"tail -f /dev/null"}`,
	})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	jobID := executionArgumentID(t, started, "job_id")
	// A concurrent run in another root gets its own build and job.
	bCtx := contracts.WithWorkspaceRoot(context.Background(), rootB)
	startedB, err := findProxy(t, proxies, "job_start").Invoke(bCtx, rig.ToolCall{
		Name: "job_start", Arguments: `{"command":"tail -f /dev/null"}`,
	})
	if err != nil {
		t.Fatalf("job_start(rootB): %v", err)
	}
	jobB := executionArgumentID(t, startedB, "job_id")
	group.mu.Lock()
	ownerA, ownerB := group.jobOwners[jobID], group.jobOwners[jobB]
	group.mu.Unlock()
	if ownerA == nil || ownerB == nil || ownerA.root != rootA || ownerB.root != rootB || ownerA == ownerB {
		t.Fatalf("multi-root job owners = %+v/%+v, want one build per root", ownerA, ownerB)
	}
	// Follow-ups by id route to their own root regardless of the current root.
	for _, testCase := range []struct{ id, wantRoot string }{{jobID, rootA}, {jobB, rootB}} {
		output, outputErr := findProxy(t, proxies, "job_output").Invoke(startCtx, rig.ToolCall{
			Name: "job_output", Arguments: `{"job_id":"` + testCase.id + `"}`,
		})
		if outputErr != nil || output.IsError || !strings.Contains(output.Content, testCase.id) {
			t.Fatalf("job_output(%s) = %q (err %v), want the %s job", testCase.id, output.Content, outputErr, testCase.wantRoot)
		}
	}

	listCtx := contracts.WithWorkspaceRoot(context.Background(), rootB)
	listed, err := findProxy(t, proxies, "job_list").Invoke(listCtx, rig.ToolCall{Name: "job_list"})
	if err != nil {
		t.Fatalf("job_list(rootB): %v", err)
	}
	if strings.Contains(listed.Content, jobID) {
		t.Fatalf("job_list(rootB) = %q, must not list the root-A job", listed.Content)
	}
	listA, err := findProxy(t, proxies, "job_list").Invoke(startCtx, rig.ToolCall{Name: "job_list"})
	if err != nil || !strings.Contains(listA.Content, jobID) || strings.Contains(listA.Content, jobB) {
		t.Fatalf("job_list(rootA) = %q (err %v), want only the root-A job", listA.Content, err)
	}
}

// TestDrainNoticesCoversRetainedBuilds covers the notification rule: a job that
// finished under a previous build still notifies the model after a switch.
func TestDrainNoticesCoversRetainedBuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())

	started := executionCall(t, findProxy(t, proxies, "job_start"), "", `{"command":"true"}`)
	jobID := executionArgumentID(t, started, "job_id")
	// Wait for the job to settle before switching, then switch to a new root.
	waitJobOutput(t, findProxy(t, proxies, "job_output"), jobID, "state: exited")
	current = rootB
	group.forRoot()

	deadline := time.Now().Add(5 * time.Second)
	var notices []string
	for time.Now().Before(deadline) {
		notices = group.drainNotices()
		if len(notices) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	joined := strings.Join(notices, "\n")
	if len(notices) == 0 {
		t.Fatal("no completion notice from the retained build")
	}
	if !strings.Contains(joined, jobID) && !strings.Contains(joined, "exited") {
		t.Fatalf("notice = %q, want the settled job", joined)
	}
	if again := group.drainNotices(); len(again) != 0 {
		t.Fatalf("drained notice resurfaced: %v", again)
	}
	// Finished jobs stay retained by their owner manager: a later read by id
	// still routes to it (Rig's job_output keeps the final state/output).
	finishedRead := executionCall(t, findProxy(t, proxies, "job_output"), "", `{"job_id":"`+jobID+`"}`)
	if finishedRead.IsError || !strings.Contains(finishedRead.Content, "job_id: "+jobID) {
		t.Fatalf("job_output after finish = %q, want the retained finished job", finishedRead.Content)
	}
}

// TestExecutionLifecycleEventsDurable covers the optional durable audit events.
func TestExecutionLifecycleEventsDurable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("terminal pipe transport needs /bin/sh")
	}
	store := &executionTestStore{}
	root := t.TempDir()
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, nil, nil)
	group.events = &executionEvents{store: store}
	defer group.close()
	proxies := group.proxies(group.forRoot())

	started := executionCall(t, findProxy(t, proxies, "job_start"), "sess-durable", `{"command":"true","cwd":"."}`)
	jobID := executionArgumentID(t, started, "job_id")
	opened := executionCall(t, findProxy(t, proxies, "terminal_open"), "sess-durable", `{"type":"pipe","name":"audit"}`)
	sessionID := executionArgumentID(t, opened, "session_id")
	executionCall(t, findProxy(t, proxies, "terminal_close"), "sess-durable", `{"sessionId":"`+sessionID+`"}`)

	// Wait for the job notice drain to record job_finished.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(store.byType(contracts.ExecutionJobFinished)) == 0 {
		group.drainNotices()
		time.Sleep(10 * time.Millisecond)
	}

	startedEvents := store.byType(contracts.ExecutionJobStarted)
	if len(startedEvents) != 1 || startedEvents[0].SessionID != "sess-durable" {
		t.Fatalf("job_started events = %+v, want one for sess-durable", startedEvents)
	}
	var startPayload map[string]any
	if err := json.Unmarshal(startedEvents[0].Payload, &startPayload); err != nil {
		t.Fatalf("decode job_started: %v", err)
	}
	if startPayload["id"] != jobID || startPayload["command"] != "true" || startPayload["root"] == "" {
		t.Fatalf("job_started payload = %+v", startPayload)
	}
	openedEvents := store.byType(contracts.ExecutionTerminalOpened)
	if len(openedEvents) != 1 || openedEvents[0].SessionID != "sess-durable" {
		t.Fatalf("terminal_opened events = %+v", openedEvents)
	}
	var openPayload map[string]any
	if err := json.Unmarshal(openedEvents[0].Payload, &openPayload); err != nil {
		t.Fatalf("decode terminal_opened: %v", err)
	}
	if openPayload["id"] != sessionID || openPayload["transport"] != "pipe" {
		t.Fatalf("terminal_opened payload = %+v", openPayload)
	}
	closedEvents := store.byType(contracts.ExecutionTerminalClosed)
	if len(closedEvents) != 1 || closedEvents[0].SessionID != "sess-durable" {
		t.Fatalf("terminal_closed events = %+v", closedEvents)
	}
	finished := store.byType(contracts.ExecutionJobFinished)
	if len(finished) != 1 || finished[0].SessionID != "sess-durable" {
		t.Fatalf("job_finished events = %+v, want one for sess-durable", finished)
	}
	var finishPayload map[string]any
	if err := json.Unmarshal(finished[0].Payload, &finishPayload); err != nil {
		t.Fatalf("decode job_finished: %v", err)
	}
	if finishPayload["id"] != jobID || finishPayload["state"] == "" {
		t.Fatalf("job_finished payload = %+v", finishPayload)
	}
	// The audit events never carry the full process output.
	for _, event := range store.events {
		if strings.Contains(string(event.Payload), "output") {
			t.Fatalf("execution event payload carries output: %s", event.Payload)
		}
	}
}

// TestExecutionEventsWithoutStoreAreNoop covers the optional store: without one
// the tools work exactly the same and nothing is written.
func TestExecutionEventsWithoutStoreAreNoop(t *testing.T) {
	root := t.TempDir()
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, nil, nil)
	defer group.close()
	proxies := group.proxies(group.forRoot())
	// No executionEvents at all, then an executionEvents with a nil store.
	executionCall(t, findProxy(t, proxies, "job_list"), "sess-no-store", `{}`)
	group.events = &executionEvents{}
	executionCall(t, findProxy(t, proxies, "job_list"), "sess-no-store", `{}`)
}

// TestGroupCloseReleasesRetainedBuildsExecutions covers module unload: every
// retained build is closed, exactly once (idempotent close).
func TestGroupCloseReleasesRetainedBuildsExecutions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA := t.TempDir()
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return rootA }, nil, nil)
	scoped := group.forRoot()
	proxies := group.proxies(scoped)
	executionCall(t, findProxy(t, proxies, "job_start"), "", `{"command":"tail -f /dev/null"}`)
	group.forRootValue(t.TempDir())
	group.close()
	group.close()
	if !scoped.isClosed() {
		t.Fatal("module unload did not close the retained build")
	}
	if !group.buildsSnapshotIsEmpty() {
		t.Fatal("group.close left retained builds registered")
	}
}
