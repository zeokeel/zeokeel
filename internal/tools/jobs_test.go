package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

func waitForJobStatus(t *testing.T, output runtime.Tool, id, want string) string {
	t.Helper()
	var last string
	waitUntil(t, 8*time.Second, func() bool {
		out, err := invokeTool(t, output, map[string]any{"id": id})
		if err != nil {
			t.Fatalf("job_output(%s): %v", id, err)
		}
		last = out
		return strings.Contains(out, "[status: "+want)
	})
	return last
}

func TestJobsRunOutputAndStatus(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewJobsTools(JobsConfig{WorkspaceRoot: root, MaxOutputBytes: 1024, MaxJobs: 2})
	start := findTool(t, ts, "job_start")
	list := findTool(t, ts, "job_list")
	output := findTool(t, ts, "job_output")
	kill := findTool(t, ts, "job_kill")

	if got := mustInvoke(t, list, map[string]any{}); got != "(no background jobs)" {
		t.Fatalf("empty list = %q", got)
	}

	out := mustInvoke(t, start, map[string]any{"command": "printf hello"})
	id := testToolID(t, out, "job-")
	if !strings.Contains(out, "started background job "+id) {
		t.Fatalf("start output = %q", out)
	}
	out = waitForJobStatus(t, output, id, "done")
	if !strings.Contains(out, "hello") || !strings.Contains(out, "exit code 0") {
		t.Fatalf("done output = %q", out)
	}

	out = mustInvoke(t, start, map[string]any{"command": "printf out; printf err >&2; exit 3"})
	failID := testToolID(t, out, "job-")
	out = waitForJobStatus(t, output, failID, "failed")
	if !strings.Contains(out, "out") || !strings.Contains(out, "err") || !strings.Contains(out, "exit code 3") {
		t.Fatalf("failed output = %q", out)
	}

	out = mustInvoke(t, list, map[string]any{})
	if !strings.Contains(out, id+" [done]") || !strings.Contains(out, failID+" [failed]") {
		t.Fatalf("list output = %q", out)
	}

	// The retained output is capped at 1024 bytes and marked truncated.
	out = mustInvoke(t, start, map[string]any{
		"command": "i=0; while [ $i -lt 500 ]; do printf abcdefghij; i=$((i+1)); done",
	})
	capID := testToolID(t, out, "job-")
	out = waitForJobStatus(t, output, capID, "done")
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("capped output has no truncation marker: %q", out)
	}
	if len(out) > 2000 {
		t.Fatalf("capped output is %d bytes, want a bounded result", len(out))
	}

	if _, err := invokeTool(t, output, map[string]any{"id": "nope"}); err == nil {
		t.Fatal("unknown job id should fail")
	}
	if _, err := invokeTool(t, start, map[string]any{"command": "  "}); err == nil {
		t.Fatal("blank command should fail")
	}
	out, err := invokeTool(t, kill, map[string]any{"id": capID})
	if err != nil {
		t.Fatalf("kill finished job: %v", err)
	}
	if !strings.Contains(out, "already finished") {
		t.Fatalf("kill finished job output = %q", out)
	}
}

func TestJobsKillAndLimit(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewJobsTools(JobsConfig{WorkspaceRoot: root, MaxJobs: 2, MaxOutputBytes: 1024})
	start := findTool(t, ts, "job_start")
	output := findTool(t, ts, "job_output")
	kill := findTool(t, ts, "job_kill")

	first := testToolID(t, mustInvoke(t, start, map[string]any{"command": "sleep 30"}), "job-")
	second := testToolID(t, mustInvoke(t, start, map[string]any{"command": "sleep 30"}), "job-")
	if _, err := invokeTool(t, start, map[string]any{"command": "sleep 30"}); err == nil || !strings.Contains(err.Error(), "maximum of 2") {
		t.Fatalf("third start error = %v", err)
	}

	out := mustInvoke(t, kill, map[string]any{"id": first, "reason": "test cleanup"})
	if !strings.Contains(out, "requested cancellation of job "+first) {
		t.Fatalf("kill output = %q", out)
	}
	out = waitForJobStatus(t, output, first, "killed")
	if !strings.Contains(out, "test cleanup") {
		t.Fatalf("killed output = %q", out)
	}
	out = mustInvoke(t, kill, map[string]any{"id": second})
	if !strings.Contains(out, "requested cancellation") {
		t.Fatalf("second kill output = %q", out)
	}
	waitForJobStatus(t, output, second, "killed")

	// A second kill reports the already-finished state without failing.
	out = mustInvoke(t, kill, map[string]any{"id": first})
	if !strings.Contains(out, "already finished") {
		t.Fatalf("second kill on finished job = %q", out)
	}
}

func TestJobsTimeout(t *testing.T) {
	skipUnlessUnixShell(t)
	root := t.TempDir()
	ts := NewJobsTools(JobsConfig{WorkspaceRoot: root, MaxJobs: 4})
	start := findTool(t, ts, "job_start")
	output := findTool(t, ts, "job_output")

	out := mustInvoke(t, start, map[string]any{"command": "sleep 5", "timeout_seconds": 1})
	id := testToolID(t, out, "job-")
	out = waitForJobStatus(t, output, id, "failed")
	if !strings.Contains(out, "timed out after 1s") {
		t.Fatalf("timeout output = %q", out)
	}
}

func TestJobsConfigErrors(t *testing.T) {
	ts := NewJobsTools(JobsConfig{WorkspaceRoot: filepath.Join(t.TempDir(), "missing")})
	start := findTool(t, ts, "job_start")
	if _, err := invokeTool(t, start, map[string]any{"command": "true"}); err == nil {
		t.Fatal("missing workspace root should fail")
	}

	root := t.TempDir()
	notDir := filepath.Join(root, "file")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts = NewJobsTools(JobsConfig{WorkspaceRoot: notDir})
	if _, err := invokeTool(t, findTool(t, ts, "job_start"), map[string]any{"command": "true"}); err == nil {
		t.Fatal("file workspace root should fail")
	}
}

// TestJobOutputWait proves wait:true actually blocks until the job reaches a
// terminal status (the prompt/marketing claimed a notification or wait that
// the tool silently ignored), and that the wait is bounded.
func TestJobOutputWait(t *testing.T) {
	skipUnlessUnixShell(t)
	ts := NewJobsTools(JobsConfig{WorkspaceRoot: t.TempDir()})
	start := findTool(t, ts, "job_start")
	output := findTool(t, ts, "job_output")

	out := mustInvoke(t, start, map[string]any{"command": "sleep 0.2; printf waited"})
	id := testToolID(t, out, "job-")
	started := time.Now()
	out, err := invokeTool(t, output, map[string]any{"id": id, "wait": true, "timeout_seconds": 5})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("job_output wait: %v", err)
	}
	if !strings.Contains(out, "waited") || !strings.Contains(out, "[status: done") {
		t.Fatalf("wait output = %q", out)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("wait returned after %v, before the job finished", elapsed)
	}

	out = mustInvoke(t, start, map[string]any{"command": "sleep 5"})
	slow := testToolID(t, out, "job-")
	out, err = invokeTool(t, output, map[string]any{"id": slow, "wait": true, "timeout_seconds": 1})
	if err != nil {
		t.Fatalf("bounded wait: %v", err)
	}
	if !strings.Contains(out, "[wait: timed out") || !strings.Contains(out, "[status: running") {
		t.Fatalf("bounded wait output = %q", out)
	}
	if _, err := invokeTool(t, findTool(t, ts, "job_kill"), map[string]any{"id": slow}); err != nil {
		t.Fatalf("kill slow job: %v", err)
	}
}
