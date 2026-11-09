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

// TestWorkspaceSwitchClosesRetiredBuild covers the leak: after the current
// workspace root changes, the previous build must be released, while the new
// build stays open.
func TestWorkspaceSwitchClosesRetiredBuild(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil)
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
	waitBuildClosed(t, scopedA)
	if !scopedA.isClosed() {
		t.Fatal("retired build reports not closed after release")
	}
	select {
	case <-scopedB.closedChan():
		t.Fatal("active build was closed by the root change")
	default:
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

// TestWorkspaceSwitchDoesNotAbortInFlightCall covers the lifetime rule: a build
// retired while a tool call is still running against it must close only after
// that call finishes, so switching the workspace cannot abort in-flight work.
func TestWorkspaceSwitchDoesNotAbortInFlightCall(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := &rebindGroup{cfg: Config{}, timeout: 5 * time.Second, rootOf: func() string { return current }}
	blocking := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	scopedA := &rootScoped{
		root:   rootA,
		tools:  []rig.Tool{blocking},
		byName: map[string]rig.Tool{blocking.Name(): blocking},
		done:   make(chan struct{}),
	}
	group.current = scopedA
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
	defer group.close()

	select {
	case <-scopedA.closedChan():
		t.Fatal("retired build closed while a tool call was still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	close(blocking.release)
	if invoked := <-result; invoked.Content != "released" {
		t.Fatalf("in-flight result = %+v", invoked)
	}
	waitBuildClosed(t, scopedA)
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
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, nil)
	defer group.close()
	proxyTools := group.proxies(group.forRoot())

	// A notice must be deliverable no matter which tool the next round called,
	// because Rig only drains the tools of the round: every proxy reports.
	for _, tool := range proxyTools {
		if _, ok := tool.(rig.NoticeSource); !ok {
			t.Fatalf("proxy %q does not implement rig.NoticeSource; a job notice would be dropped", tool.Name())
		}
	}

	start := findProxy(t, proxyTools, "job_start")
	started, err := start.Invoke(context.Background(), rig.ToolCall{
		Name:      "job_start",
		Arguments: `{"command":"echo notice-me"}`,
	})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	if !strings.Contains(started.Content, "job_id:") {
		t.Fatalf("job_start result = %q, want a job id", started.Content)
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
var jobIDPattern = regexp.MustCompile(`job_id:\s*(\S+)`)

// TestWorkspaceSwitchReleasesOldRootJobs covers the process leak the real
// Windows session showed (six background jobs left behind): after a workspace
// switch the old root's job table must be gone, so those processes are killed
// instead of running invisibly until app exit.
func TestWorkspaceSwitchReleasesOldRootJobs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil)
	defer group.close()

	scopedA := group.forRoot()
	start := scopedA.byName["job_start"]
	started, err := start.Invoke(context.Background(), rig.ToolCall{
		Name:      "job_start",
		Arguments: `{"command":"tail -f /dev/null"}`,
	})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	match := jobIDPattern.FindStringSubmatch(started.Content)
	if match == nil {
		t.Fatalf("job_start result = %q, want a job id", started.Content)
	}
	jobID := match[1]
	if listed, listErr := scopedA.byName["job_list"].Invoke(context.Background(), rig.ToolCall{Name: "job_list"}); listErr != nil ||
		!strings.Contains(listed.Content, jobID) {
		t.Fatalf("job_list after start = %q (err %v), want job %s", listed.Content, listErr, jobID)
	}

	current = rootB
	group.forRoot()
	waitBuildClosed(t, scopedA)

	// The retired manager's table is cleared, so the model can never reach the
	// orphaned job again and the process was terminated by CloseAll.
	listed, err := scopedA.byName["job_list"].Invoke(context.Background(), rig.ToolCall{Name: "job_list"})
	if err != nil {
		t.Fatalf("job_list after close: %v", err)
	}
	if !strings.Contains(listed.Content, "no jobs") {
		t.Fatalf("retired build still lists jobs: %q", listed.Content)
	}
}
