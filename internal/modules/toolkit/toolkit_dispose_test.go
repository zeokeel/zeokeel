package toolkit

// Module-unload regression (2026-11 audit): the SUCCESSFUL activation path must
// release everything it started. The error paths already closed the MCP
// controller and the root-scoped build group, but the success path registered
// only the LSP close as its effect cleanup — so after loader.Close+DisposeAll
// (the host shutdown sequence) background jobs kept running as orphan process
// groups, MCP connections stayed open and the controller's refresh loop never
// stopped. These tests pin the composed cleanup: dispose kills running jobs,
// closes the controller and leaves no refresh-loop goroutine behind.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
)

// activateToolkitForDispose loads the real module against rootDir with a shell
// allowlist that can run the marker job (sleep/touch).
func activateToolkitForDispose(t *testing.T, rootDir string) (cordis.Context, *module.Loader) {
	t.Helper()
	root := cordis.Root()
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit",
		Config: map[string]any{
			"root":                rootDir,
			"commandAllowlist":    []any{"sleep", "touch", "bash"},
			"allowShellOperators": true,
		},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	return root, loader
}

// startMarkerJob starts "sleep 1; touch <marker>" in the background and returns
// the job id. The marker file appearing later proves the process group survived.
func startMarkerJob(t *testing.T, root cordis.Context, marker string) string {
	t.Helper()
	tools, err := root.Require(hosttools.KeyRegistry)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	result, err := tools.Invoke(context.Background(), rig.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"sleep 1; touch ` + marker + `","run_in_background":true}`,
	})
	if err != nil {
		t.Fatalf("bash invoke: %v (content %q)", err, result.Content)
	}
	id := regexp.MustCompile(`bash-[0-9a-f]+`).FindString(result.Content)
	if id == "" {
		t.Fatalf("no job id in background result: %q", result.Content)
	}
	return id
}

// countRefreshLoopGoroutines counts goroutines currently inside the MCP
// controller's refresh loop (one per live controller).
func countRefreshLoopGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	stack := string(buf[:n])
	return strings.Count(stack, "refreshLoop") + strings.Count(stack, "mcp/auth.(*Runtime).Start")
}

// disposeToolkit runs the same teardown sequence as the process host's
// shutdown: loader.Close followed by root.DisposeAll.
func disposeToolkit(t *testing.T, root cordis.Context, loader *module.Loader) {
	t.Helper()
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
}

// TestToolkitDisposeKillsJobsAndStopsLoops is the main regression: after the
// host shutdown sequence no background job may survive, the MCP controller must
// be closed and its refresh loop must have exited.
func TestToolkitDisposeKillsJobsAndStopsLoops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootDir := t.TempDir()
	marker := filepath.Join(rootDir, "job-survived.txt")
	baseline := countRefreshLoopGoroutines()
	root, loader := activateToolkitForDispose(t, rootDir)
	jobID := startMarkerJob(t, root, marker)
	t.Logf("started background job %s", jobID)

	reloader, err := root.Require(contracts.KeyMCPReloader)
	if err != nil {
		t.Fatalf("require reloader: %v", err)
	}
	controller, ok := reloader.(*mcpController)
	if !ok {
		t.Fatalf("reloader type = %T, want *mcpController", reloader)
	}

	disposeToolkit(t, root, loader)

	controller.mu.Lock()
	closed := controller.closed
	controller.mu.Unlock()
	if !closed {
		t.Errorf("controller.closed=false after dispose: the success path never registered controller.close")
	}
	deadline := time.Now().Add(2 * time.Second)
	for countRefreshLoopGoroutines() > baseline && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := countRefreshLoopGoroutines(); n > baseline {
		t.Errorf("refreshLoop goroutines leaked: before=%d afterDispose=%d", baseline, n)
	}

	// The job sleeps 1s before touching the marker; waiting past that proves
	// dispose killed the process group instead of the job merely being slow.
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("background job survived loader.Close+DisposeAll (marker %s exists): orphan process group", marker)
	}
}

// TestToolkitDisposeControlKillJob is the methodology control: the same
// activation and marker job, but killed explicitly through contracts.JobService
// before dispose. The marker must never appear — proving the marker check
// distinguishes killed jobs from survivors.
func TestToolkitDisposeControlKillJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	rootDir := t.TempDir()
	marker := filepath.Join(rootDir, "job-killed.txt")
	root, loader := activateToolkitForDispose(t, rootDir)
	jobID := startMarkerJob(t, root, marker)

	jobsService, err := root.Require(contracts.KeyJobs)
	if err != nil {
		t.Fatalf("require jobs: %v", err)
	}
	if err := jobsService.KillJob(jobID); err != nil {
		t.Fatalf("KillJob(%s): %v", jobID, err)
	}
	disposeToolkit(t, root, loader)
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("control invalid: explicitly killed job still produced marker %s", marker)
	}
}
