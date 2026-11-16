package toolkit

// Tests for root-build lifetime and background-job completion notices:
//
//   - a workspace switch retires the old tool build and releases its
//     background jobs/terminals once no call is still running against it
//     (previously the retired build was kept until module unload, leaking
//     processes and ports that the model could no longer see or kill);
//   - the agent loop's rig.NoticeSource drain reaches the root's job manager,
//     so Rig's documented "You are notified in-session when a job finishes"
//     contract (DSH's job completion notifications) actually holds.

import (
	"context"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
)

// waitBuildClosed waits for the build's release hook to run.
func waitBuildClosed(t *testing.T, scoped *rootScoped) {
	t.Helper()
	select {
	case <-scoped.closedChan():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the root-scoped build to close")
	}
}

// findProxy returns the named proxy from one root build's proxy list.
func findProxy(t *testing.T, proxies []rig.Tool, name string) rig.Tool {
	t.Helper()
	for _, tool := range proxies {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("proxy %q not found in %d tools", name, len(proxies))
	return nil
}

// TestWorkspaceSwitchRetainsOldBuild covers the execution-domain rule: after
// the current workspace root changes, the previous build stays alive (its jobs
// and terminals must remain controllable); module unload releases every build.
func TestWorkspaceSwitchRetainsOldBuild(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()

	scopedA := group.forRoot()
	if scopedA.root != rootA {
		t.Fatalf("first build root = %q, want %q", scopedA.root, rootA)
	}
	current = rootB
	scopedB := group.forRoot()
	if scopedB == scopedA {
		t.Fatal("root change reused the previous build")
	}
	if scopedB.root != rootB {
		t.Fatalf("second build root = %q, want %q", scopedB.root, rootB)
	}
	if scopedA.isClosed() {
		t.Fatal("workspace switch released the previous build; its jobs/terminals would be lost")
	}
	select {
	case <-scopedB.closedChan():
		t.Fatal("new build was closed by the root change")
	default:
	}

	group.close()
	if !scopedA.waitClosed(5*time.Second) || !scopedB.waitClosed(5*time.Second) {
		t.Fatal("module unload did not release every retained build")
	}
}

// blockingTool blocks one Invoke until the test releases it.
type blockingTool struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (t *blockingTool) Name() string           { return "blocking_tool" }
func (t *blockingTool) Description() string    { return "blocks until released" }
func (t *blockingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *blockingTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	t.startOnce.Do(func() { close(t.started) })
	<-t.release
	return rig.ToolResult{Content: "released"}, nil
}

// TestRetainedBuildKeepsInFlightCall covers the lifetime rule: a call already
// running against a build completes normally while a new build is installed for
// the new root; the old build is not released by the switch.
func TestRetainedBuildKeepsInFlightCall(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()
	blocking := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	scopedA := &rootScoped{
		root:   rootA,
		mode:   sandbox.ModeWorkspaceWrite,
		tools:  []rig.Tool{blocking},
		byName: map[string]rig.Tool{blocking.Name(): blocking},
		done:   make(chan struct{}),
	}
	group.mu.Lock()
	group.current = scopedA
	group.addBuildLocked(group.buildKey(rootA, sandbox.ModeWorkspaceWrite, nil), scopedA)
	group.mu.Unlock()
	proxy := &boundTool{meta: blocking, group: group}

	result := make(chan rig.ToolResult, 1)
	go func() {
		invoked, err := proxy.Invoke(context.Background(), rig.ToolCall{Name: blocking.Name()})
		if err != nil {
			t.Errorf("in-flight invoke failed: %v", err)
		}
		result <- invoked
	}()
	<-blocking.started

	current = rootB
	scopedB := group.forRoot()
	if scopedB == scopedA {
		t.Fatal("root change reused the previous build")
	}
	if scopedA.isClosed() {
		t.Fatal("workspace switch released the build an in-flight call was using")
	}

	close(blocking.release)
	if invoked := <-result; invoked.Content != "released" {
		t.Fatalf("in-flight result = %+v", invoked)
	}
	group.close()
	if !scopedA.waitClosed(5 * time.Second) {
		t.Fatal("module unload did not release the retained build")
	}
}

// TestJobCompletionNoticesReachAgentLoop covers Rig's documented notification
// contract: every root-scoped proxy is a rig.NoticeSource whose drain returns
// the previous root's settled-job notices exactly once, so the agent loop can
// append them as a model-visible message (DSH's in-session job notification).
func TestJobCompletionNoticesReachAgentLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	root := t.TempDir()
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, nil, nil)
	defer group.close()
	proxyTools := group.proxies(group.forRoot())

	// A notice must be deliverable no matter which tool the next round called,
	// because Rig only drains the tools of the round: every proxy reports.
	for _, tool := range proxyTools {
		if _, ok := tool.(rig.NoticeSource); !ok {
			t.Fatalf("proxy %q does not implement rig.NoticeSource; a job notice would be dropped", tool.Name())
		}
	}

	start := findProxy(t, proxyTools, "bash")
	started, err := start.Invoke(context.Background(), rig.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"echo notice-me","run_in_background":true}`,
	})
	if err != nil {
		t.Fatalf("bash run_in_background: %v", err)
	}
	if !strings.Contains(started.Content, "started background job") {
		t.Fatalf("bash run_in_background result = %q, want a job id", started.Content)
	}

	var notices []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if notices = group.drainNotices(); len(notices) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(notices) == 0 {
		t.Fatal("no background-job completion notice was queued")
	}
	joined := strings.Join(notices, "\n")
	for _, want := range []string{"finished", "job_output"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("notice %q lacks %q", joined, want)
		}
	}
	// The drain is consuming: a second round must not repeat the same notice.
	if again := group.drainNotices(); len(again) != 0 {
		t.Fatalf("drained notices resurfaced: %v", again)
	}
}

// jobIDPattern extracts the id from a job_start/job_output result.
var jobIDPattern = regexp.MustCompile(`(?:job_id:|started background job)\s*(\S+)`)

// TestWorkspaceSwitchKeepsOldRootJobs covers the execution-domain persistence:
// a job started under root A stays controllable by id after switching to root B
// (job_output/job_kill route to the owner build), while job_list only merges the
// caller root's builds. Module unload releases the job's process.
func TestWorkspaceSwitchKeepsOldRootJobs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()
	proxyTools := group.proxies(group.forRoot())

	start := findProxy(t, proxyTools, "bash")
	started, err := start.Invoke(context.Background(), rig.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"tail -f /dev/null","run_in_background":true}`,
	})
	if err != nil || started.IsError {
		t.Fatalf("bash run_in_background = %q (err %v)", started.Content, err)
	}
	match := jobIDPattern.FindStringSubmatch(started.Content)
	if match == nil {
		t.Fatalf("bash run_in_background result = %q, want a job id", started.Content)
	}
	jobID := match[1]
	if listed, listErr := findProxy(t, proxyTools, "job_list").Invoke(context.Background(), rig.ToolCall{Name: "job_list"}); listErr != nil ||
		!strings.Contains(listed.Content, jobID) {
		t.Fatalf("job_list after start = %q (err %v), want job %s", listed.Content, listErr, jobID)
	}

	current = rootB
	// A follow-up by id reaches the retained build (the old job is still running).
	output, err := findProxy(t, proxyTools, "job_output").Invoke(context.Background(), rig.ToolCall{
		Name: "job_output", Arguments: `{"job_id":"` + jobID + `"}`,
	})
	if err != nil || output.IsError || !strings.Contains(output.Content, "job_id: "+jobID) {
		t.Fatalf("job_output(%s) after switch = %q (err %v), want the running job", jobID, output.Content, err)
	}
	// job_list only merges the caller root's builds: root B has no jobs.
	if listed, listErr := findProxy(t, proxyTools, "job_list").Invoke(context.Background(), rig.ToolCall{Name: "job_list"}); listErr != nil ||
		strings.Contains(listed.Content, jobID) {
		t.Fatalf("job_list on root B = %q (err %v), want no jobs from another root", listed.Content, listErr)
	}
	// The old job is still killable by id from the current root.
	kill, err := findProxy(t, proxyTools, "job_kill").Invoke(context.Background(), rig.ToolCall{
		Name: "job_kill", Arguments: `{"job_id":"` + jobID + `"}`,
	})
	if err != nil || kill.IsError {
		t.Fatalf("job_kill(%s) after switch = %q (err %v)", jobID, kill.Content, err)
	}

	// Module unload releases every build (which kills/owns its processes).
	group.close()
	if !group.buildsSnapshotIsEmpty() {
		t.Fatal("group.close left retained builds registered")
	}
}

// buildsSnapshotIsEmpty reports whether the group released every build (tests).
func (g *rebindGroup) buildsSnapshotIsEmpty() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.builds) == 0 && len(g.order) == 0 && g.current == nil
}
