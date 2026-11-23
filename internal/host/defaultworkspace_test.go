package host

// Tests for the host's single default workspace: a launch without a current
// workspace creates <dataDir>/workspace and opens it; an existing current
// workspace is left exactly as it was (the legacy folder is never replaced).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeWorkspace is the registry seam of the tests: it records Create/Open and
// answers Current from the workspace it holds.
type fakeWorkspace struct {
	current *contracts.Workspace
	created []string
	opened  []string
	// currentErr makes Current fail (a registry read failure).
	currentErr error
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	if f.current == nil {
		return nil, nil
	}
	return []contracts.Workspace{*f.current}, nil
}

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	if f.currentErr != nil {
		return contracts.Workspace{}, false, f.currentErr
	}
	if f.current == nil {
		return contracts.Workspace{}, false, nil
	}
	return *f.current, true, nil
}

func (f *fakeWorkspace) Create(_ context.Context, _ string, root string) (contracts.Workspace, error) {
	created := contracts.Workspace{ID: "ws-default", Name: filepath.Base(root), Root: root}
	f.current = &created
	f.created = append(f.created, root)
	return created, nil
}

func (f *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	if f.current == nil || f.current.ID != id {
		return contracts.Workspace{}, errors.New("unknown workspace " + id)
	}
	f.opened = append(f.opened, id)
	return *f.current, nil
}

func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// recordingLogger captures the startup lines so a test can assert the clear log
// line the host promises.
type recordingLogger struct{ lines []string }

func (l *recordingLogger) Printf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// workspaceKernel provides the fake registry on a fresh root.
func workspaceKernel(t *testing.T, workspace contracts.WorkspaceService) cordis.Context {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	if _, err := root.Provide(contracts.KeyWorkspace, workspace); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	return root
}

// TestEnsureDefaultWorkspaceCreatesMissingFolder covers the first-launch case:
// no current workspace means <dataDir>/workspace is created, registered and
// opened, and the startup log says so.
func TestEnsureDefaultWorkspaceCreatesMissingFolder(t *testing.T) {
	dataDir := t.TempDir()
	workspace := &fakeWorkspace{}
	kernel := workspaceKernel(t, workspace)
	logger := &recordingLogger{}

	ensureDefaultWorkspace(kernel, dataDir, logger)

	want := filepath.Join(dataDir, defaultWorkspaceDirName)
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("default workspace folder %q was not created: %v", want, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", want)
	}
	if len(workspace.created) != 1 || workspace.created[0] != want {
		t.Fatalf("created = %v, want [%q]", workspace.created, want)
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		t.Fatalf("Current after ensure = (%+v, %v, %v), want the created workspace", current, ok, err)
	}
	if current.Root != want {
		t.Fatalf("current root = %q, want %q", current.Root, want)
	}
	if len(workspace.opened) != 1 || workspace.opened[0] != current.ID {
		t.Fatalf("opened = %v, want [%q]", workspace.opened, current.ID)
	}
	if len(logger.lines) != 1 || !strings.Contains(logger.lines[0], "opened the default workspace folder") {
		t.Fatalf("startup log = %v, want the default-workspace line", logger.lines)
	}
}

// TestEnsureDefaultWorkspaceKeepsExistingFolder covers the legacy case: a
// registry with a current workspace is untouched (no folder created, no Open),
// so the user keeps the folder they had open.
func TestEnsureDefaultWorkspaceKeepsExistingFolder(t *testing.T) {
	dataDir := t.TempDir()
	legacy := contracts.Workspace{ID: "ws-legacy", Name: "legacy", Root: filepath.Join(dataDir, "legacy")}
	workspace := &fakeWorkspace{current: &legacy}
	kernel := workspaceKernel(t, workspace)
	logger := &recordingLogger{}

	ensureDefaultWorkspace(kernel, dataDir, logger)

	if len(workspace.created) != 0 {
		t.Fatalf("created = %v, want none for an existing current workspace", workspace.created)
	}
	if len(workspace.opened) != 0 {
		t.Fatalf("opened = %v, want none for an existing current workspace", workspace.opened)
	}
	if _, err := os.Stat(filepath.Join(dataDir, defaultWorkspaceDirName)); !os.IsNotExist(err) {
		t.Fatalf("default folder was created for an existing current workspace (stat err = %v)", err)
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok || current.Root != legacy.Root {
		t.Fatalf("current after ensure = (%+v, %v, %v), want the legacy workspace unchanged", current, ok, err)
	}
}

// TestEnsureDefaultWorkspaceNoBackendIsNoop covers a composition without the
// workspace module (a stripped embedder): startup must not panic or create a
// folder.
func TestEnsureDefaultWorkspaceNoBackendIsNoop(t *testing.T) {
	dataDir := t.TempDir()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	logger := &recordingLogger{}

	ensureDefaultWorkspace(root, dataDir, logger)

	if len(logger.lines) != 0 {
		t.Fatalf("log = %v, want silence without a workspace backend", logger.lines)
	}
	if _, err := os.Stat(filepath.Join(dataDir, defaultWorkspaceDirName)); !os.IsNotExist(err) {
		t.Fatalf("default folder was created without a workspace backend (stat err = %v)", err)
	}
}

// TestEnsureDefaultWorkspaceReadFailureDoesNotCreate covers the registry read
// error: the host cannot know whether a folder is open, so it must not register
// a second workspace.
func TestEnsureDefaultWorkspaceReadFailureDoesNotCreate(t *testing.T) {
	dataDir := t.TempDir()
	workspace := &fakeWorkspace{currentErr: errors.New("registry unreadable")}
	kernel := workspaceKernel(t, workspace)
	logger := &recordingLogger{}

	ensureDefaultWorkspace(kernel, dataDir, logger)

	if len(workspace.created) != 0 {
		t.Fatalf("created = %v, want none after a read failure", workspace.created)
	}
	if len(logger.lines) != 1 || !strings.Contains(logger.lines[0], "read the current workspace") {
		t.Fatalf("log = %v, want the read-failure line", logger.lines)
	}
}
