package sidebar_test

// A conversation belongs to a workspace: opening one must make its workspace
// current, otherwise the agent's file and shell tools keep operating on
// whatever project the user opened last. After the single-workspace move the
// sidebar only lists the open folder's conversations, so the cross-folder half
// of the rule moved to the switch dialog: switching the folder is the explicit
// gesture, and opening a listed conversation never changes it.

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

// TestOpeningScopedSessionKeepsTheOpenFolder: a conversation in the open folder
// is already bound to it, so clicking it must not re-open the workspace (the
// list only shows this folder's rows).
func TestOpeningScopedSessionKeepsTheOpenFolder(t *testing.T) {
	workspace := &switchingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	workspace.current = currentWorkspace()
	workspace.hasCurrent = true
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	// C 会话 belongs to the other folder and is not listed; A 会话 is this one.
	h.assertNoTexts(tester, "C 会话")
	if err := tester.Click("A 会话"); err != nil {
		t.Fatalf("Click(A 会话): %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("opening an already-bound conversation reopened %v", opened)
	}
	// B 会话 is also this folder's: still no open.
	if err := tester.Click("B 会话"); err != nil {
		t.Fatalf("Click(B 会话): %v", err)
	}
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("opening a second scoped conversation reopened %v", opened)
	}
}

// TestSwitchDialogMakesTheChosenFolderCurrent pins the cross-folder gesture: the
// dialog's Open makes the other folder current, and the list then shows its
// conversation — the sidebar follows the folder, and session tools follow the
// sidebar.
func TestSwitchDialogMakesTheChosenFolderCurrent(t *testing.T) {
	workspace := &switchingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	workspace.current = currentWorkspace()
	workspace.hasCurrent = true
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	if err := tester.Click("管理工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	if err := tester.Click("项目二"); err != nil {
		t.Fatalf("choose 项目二: %v", err)
	}
	tester.Frame()
	h.click(tester, "打开")
	if opened := workspace.openedIDs(); len(opened) != 1 || opened[0] != "w2" {
		t.Fatalf("workspace opens after choosing 项目二 = %v, want [w2]", opened)
	}
	// The list now shows the other folder's conversation, and only that one.
	h.assertTexts(tester, "项目二", "C 会话")
	h.assertNoTexts(tester, "A 会话", "B 会话")
}

// TestOpeningUnboundSessionKeepsCurrentWorkspace pins the no-op case: a session
// without a workspace binding must not change the current workspace.
func TestOpeningUnboundSessionKeepsCurrentWorkspace(t *testing.T) {
	workspace := &switchingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	workspace.current = currentWorkspace()
	workspace.hasCurrent = true
	h := newHarnessWithWorkspace(t, treeChat(), sidebar.Config{}, workspace)
	tester := h.tester()

	if err := tester.Click("D 会话"); err != nil {
		t.Fatalf("Click(D 会话): %v", err)
	}
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 0 {
		t.Fatalf("an unbound session switched the workspace: %v", opened)
	}
}
