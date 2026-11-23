package sidebar_test

// A conversation belongs to a folder: opening one must make its folder current,
// otherwise the agent's file and shell tools keep operating on whatever project
// the user opened last. The sidebar itself never switches folders (there is no
// rail or switch dialog any more); this rule survives only for a conversation
// whose folder is not the open one, which the list only shows when no folder is
// open (a fresh or just-emptied registry).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// recordingMCPReloader records the workspace-switch reload trigger.
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

// TestOpeningScopedSessionKeepsTheOpenFolder: a conversation in the open folder
// is already bound to it, so clicking it must not re-open the workspace.
func TestOpeningScopedSessionKeepsTheOpenFolder(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	// C 会话 belongs to the other folder and is not listed; A 会话 is this one.
	h.assertNoTexts(tester, "C 会话")
	h.click(tester, "A 会话")
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("opening an already-bound conversation reopened %v", opened)
	}
	h.click(tester, "B 会话")
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("opening a second scoped conversation reopened %v", opened)
	}
}

// TestOpeningUnboundSessionKeepsCurrentWorkspace: a conversation without a
// folder binding must not change the current workspace.
func TestOpeningUnboundSessionKeepsCurrentWorkspace(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...).withCurrent(currentWorkspace())
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	h.click(tester, "D 会话")
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("an unbound conversation switched the workspace: %v", opened)
	}
}

// TestOpeningCrossFolderSessionSwitchesWorkspace: with no folder open the list
// shows every conversation, so opening one bound to another folder switches the
// workspace and rebuilds the workspace-scoped MCP connectors.
func TestOpeningCrossFolderSessionSwitchesWorkspace(t *testing.T) {
	workspace := newFakeWorkspace(treeWorkspaces()...) // no current folder
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s2", Title: "W2 会话", WorkspaceID: "w2"},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	reloader := &recordingMCPReloader{}
	provide[contracts.MCPReloader](t, h.ctx, contracts.KeyMCPReloader, reloader)
	tester := h.tester()

	h.click(tester, "W2 会话")
	if opened := workspace.openedIDs(); len(opened) != 1 || opened[0] != "w2" {
		t.Fatalf("workspace opens = %v, want [w2]", opened)
	}
	waitMCPReload(t, reloader, 1)
	// The folder line follows the conversation's folder on the next frame.
	h.assertTexts(tester, "工作文件夹 /", "项目二")
}
