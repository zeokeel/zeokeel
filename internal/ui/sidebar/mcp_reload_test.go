package sidebar_test

// Workspace switches rebuild the workspace-scoped MCP connectors: the switch
// dialog, the rail and the session-bound switch all trigger the toolkit's
// reloader so tools/plugins follow the new folder immediately.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

type recordingMCPReloader struct {
	mu    sync.Mutex
	calls int
}

func (r *recordingMCPReloader) Reload(context.Context) ([]contracts.MCPPlugin, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return nil, nil
}

func (r *recordingMCPReloader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// waitMCPReload waits for the asynchronous reload trigger.
func waitMCPReload(t *testing.T, reloader *recordingMCPReloader, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reloader.count() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("MCP reload calls = %d, want >= %d", reloader.count(), want)
}

func TestWorkspaceSwitchDialogReloadsMCP(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())}
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	reloader := &recordingMCPReloader{}
	provide[contracts.MCPReloader](t, h.ctx, contracts.KeyMCPReloader, reloader)
	tester := openSwitchDialog(t, h)
	if err := tester.Click("项目二"); err != nil {
		t.Fatalf("choose 项目二: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.click(tester, "打开")
	if calls := workspace.openCalls; len(calls) != 1 || calls[0] != "w2" {
		t.Fatalf("Open calls = %v, want [w2]", calls)
	}
	waitMCPReload(t, reloader, 1)
}

func TestWorkspaceRailSwitchReloadsMCP(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	reloader := &recordingMCPReloader{}
	provide[contracts.MCPReloader](t, h.ctx, contracts.KeyMCPReloader, reloader)
	tester := h.tester()
	if err := tester.Click("切换工作文件夹：项目二"); err != nil {
		t.Fatalf("click the rail item: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	waitMCPReload(t, reloader, 1)
}
