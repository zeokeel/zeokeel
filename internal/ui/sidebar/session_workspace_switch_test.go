package sidebar_test

// A conversation belongs to a workspace: opening a session whose workspace is
// not current must make that workspace current, otherwise the agent's file and
// shell tools keep operating on whatever project the user opened last.

import (
	"context"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// switchingWorkspace records Open calls and updates the current workspace, like
// the real workspace service.
type switchingWorkspace struct {
	*fakeWorkspace
	mu     sync.Mutex
	opened []string
}

func (w *switchingWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = append(w.opened, id)
	for _, workspace := range w.workspaces {
		if workspace.ID == id {
			w.current = workspace
			w.hasCurrent = true
			return workspace, nil
		}
	}
	return contracts.Workspace{ID: id}, nil
}

func (w *switchingWorkspace) openedIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.opened...)
}

func TestOpeningSessionSwitchesToItsWorkspace(t *testing.T) {
	workspace := &switchingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	workspace.current = contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"}
	workspace.hasCurrent = true
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	// C 会话 belongs to w2 while w1 is current: opening it must open w2.
	if err := tester.Click("C 会话"); err != nil {
		t.Fatalf("Click(C 会话): %v", err)
	}
	tester.Frame()
	opened := workspace.openedIDs()
	if len(opened) != 1 || opened[0] != "w2" {
		t.Fatalf("workspace opens after clicking C 会话 = %v, want [w2]", opened)
	}

	// A 会话 belongs to w1: opening it switches back to w1.
	if err := tester.Click("A 会话"); err != nil {
		t.Fatalf("Click(A 会话): %v", err)
	}
	tester.Frame()
	opened = workspace.openedIDs()
	if len(opened) != 2 || opened[0] != "w2" || opened[1] != "w1" {
		t.Fatalf("workspace opens after clicking A 会话 = %v, want [w2 w1]", opened)
	}

	// B 会话 also belongs to the now-current w1: no further open.
	if err := tester.Click("B 会话"); err != nil {
		t.Fatalf("Click(B 会话): %v", err)
	}
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 2 {
		t.Fatalf("opening an already-current workspace session reopened: %v", opened)
	}
}

// TestOpeningUngroupedSessionKeepsCurrentWorkspace pins the no-op case: a
// session without a workspace binding must not change the current workspace.
func TestOpeningUngroupedSessionKeepsCurrentWorkspace(t *testing.T) {
	workspace := &switchingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	workspace.current = contracts.Workspace{ID: "w1", Name: "项目一", Root: "/work/one"}
	workspace.hasCurrent = true
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	if err := tester.Click("D 会话"); err != nil {
		t.Fatalf("Click(D 会话): %v", err)
	}
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("ungrouped session switched the workspace: %v", opened)
	}
}
