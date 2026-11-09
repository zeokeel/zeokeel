package home_test

// Home workspace context: the composer names the workspace a new chat belongs
// to (and can switch it), the recent rows name theirs, and the recent order is
// pinned-first like the sidebar's.

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// fakeWorkspace is a minimal contracts.WorkspaceService for the Home context
// tests: it records the current workspace and switches it like the real
// backend does.
type fakeWorkspace struct {
	workspaces []contracts.Workspace
	currentID  string
	opened     []string
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return append([]contracts.Workspace(nil), f.workspaces...), nil
}

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	for _, ws := range f.workspaces {
		if ws.ID == f.currentID {
			return ws, true, nil
		}
	}
	return contracts.Workspace{}, false, nil
}

func (f *fakeWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	ws := contracts.Workspace{ID: "ws-" + name, Name: name, Root: root}
	f.workspaces = append(f.workspaces, ws)
	f.currentID = ws.ID
	return ws, nil
}

func (f *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	for _, ws := range f.workspaces {
		if ws.ID == id {
			f.currentID = id
			f.opened = append(f.opened, id)
			return ws, nil
		}
	}
	return contracts.Workspace{}, errorsNew("workspace " + id + " not found")
}

func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// errorsNew is a tiny errors.New without importing errors in the test file's
// top block (kept local so the fault message reads with the fake).
func errorsNew(message string) error { return &fakeWorkspaceError{message: message} }

type fakeWorkspaceError struct{ message string }

func (e *fakeWorkspaceError) Error() string { return e.message }

// homeHarnessWithWorkspace activates Home with both the fake chat and the fake
// workspace backend.
func homeHarnessWithWorkspace(t *testing.T, chat contracts.ChatService, workspace contracts.WorkspaceService) *harness {
	t.Helper()
	h := newHarness(t, chat)
	provide(t, h.ctx, contracts.KeyWorkspace, contracts.WorkspaceService(workspace))
	activate(t, h, home.Module(), home.Config{})
	return h
}

// TestHomeComposerNamesAndSwitchesWorkspace covers D-09's Home half: the
// composer's context row shows the workspace new chats belong to, and its menu
// switches the workspace through the workspace service.
func TestHomeComposerNamesAndSwitchesWorkspace(t *testing.T) {
	alpha := contracts.Workspace{ID: "w-alpha", Name: "probe-alpha", Root: "/tmp/probe-alpha"}
	beta := contracts.Workspace{ID: "w-beta", Name: "probe-beta", Root: "/tmp/probe-beta"}
	workspace := &fakeWorkspace{workspaces: []contracts.Workspace{alpha, beta}, currentID: "w-alpha"}
	h := homeHarnessWithWorkspace(t, &fakeChat{config: contracts.ChatConfig{Model: "test-model"}}, workspace)
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	h.assertTexts(tester, "probe-alpha")
	chip, ok := tester.AccessByRole(ui.RoleMenuButton, "probe-alpha")
	if !ok {
		t.Fatalf("composer has no workspace chip; texts = %v", tester.Texts())
	}
	tester.ClickAt(chip.Bounds.X+chip.Bounds.W/2, chip.Bounds.Y+chip.Bounds.H/2)
	tester.Frame()
	if err := tester.ChooseMenuItem("probe-beta"); err != nil {
		t.Fatalf("choose workspace probe-beta: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if len(workspace.opened) != 1 || workspace.opened[0] != "w-beta" {
		t.Fatalf("workspace Opened = %v, want [w-beta]", workspace.opened)
	}
	h.assertTexts(tester, "probe-beta")
	if current, ok, _ := workspace.Current(context.Background()); !ok || current.ID != "w-beta" {
		t.Fatalf("current workspace = %+v (ok=%v), want w-beta", current, ok)
	}
}

// TestHomeRecentSessionsNameWorkspaceAndPinFirst covers D-09's recent-row half
// and D-17 ordering: each recent row names its workspace, and a pinned older
// conversation sorts above a newer unpinned one, as in the sidebar.
func TestHomeRecentSessionsNameWorkspaceAndPinFirst(t *testing.T) {
	workspace := &fakeWorkspace{workspaces: []contracts.Workspace{
		{ID: "w-alpha", Name: "probe-alpha", Root: "/tmp/probe-alpha"},
		{ID: "w-beta", Name: "probe-beta", Root: "/tmp/probe-beta"},
	}, currentID: "w-alpha"}
	now := time.Now()
	chat := &fakeChat{
		config: contracts.ChatConfig{Model: "test-model"},
		sessions: []contracts.ChatSessionInfo{
			{ID: "old-pinned", WorkspaceID: "w-alpha", Title: "Pinned older chat", Pinned: true, UpdatedAt: now.Add(-2 * time.Hour)},
			{ID: "new-unpinned", WorkspaceID: "w-beta", Title: "Newer unpinned chat", UpdatedAt: now},
		},
	}
	h := homeHarnessWithWorkspace(t, chat, workspace)
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	h.assertTexts(tester, "Pinned older chat", "Newer unpinned chat", "probe-alpha", "probe-beta")
	pinnedRect, ok := tester.Find("Pinned older chat")
	if !ok {
		t.Fatalf("pinned recent row missing; texts = %v", tester.Texts())
	}
	newerRect, ok := tester.Find("Newer unpinned chat")
	if !ok {
		t.Fatalf("newer recent row missing; texts = %v", tester.Texts())
	}
	if pinnedRect.Y >= newerRect.Y {
		t.Fatalf("recent order: pinned y=%.1f is not above newer y=%.1f (D-17)", pinnedRect.Y, newerRect.Y)
	}
}
