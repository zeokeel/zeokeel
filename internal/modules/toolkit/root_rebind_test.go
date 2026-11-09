package toolkit

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeWorkspace is a mutable WorkspaceService: set changes the current
// workspace, which is all the toolkit needs to re-root its tools.
type fakeWorkspace struct {
	mu      sync.Mutex
	current contracts.Workspace
	open    bool
}

func (f *fakeWorkspace) set(root string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = contracts.Workspace{ID: "ws-" + filepath.Base(root), Name: filepath.Base(root), Root: root}
	f.open = true
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) { return nil, nil }

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current, f.open, nil
}

func (f *fakeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, os.ErrInvalid
}

func (f *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, os.ErrInvalid
}

func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, os.ErrNotExist
}

func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// TestToolRootFollowsWorkspace pins the fix for the desktop working-directory
// bug: the agent tools must be sandboxed to the workspace that is current when
// the call runs (not to the process working directory captured at launch), and
// must fail closed while no workspace is open.
func TestToolRootFollowsWorkspace(t *testing.T) {
	programDir := t.TempDir() // simulates D:\Programs\zeokeel (CWD of a double-clicked exe)
	t.Chdir(programDir)
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()

	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	workspace := &fakeWorkspace{}
	if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}

	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	defer func() { _ = loader.Close() }()

	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}

	// The Windows shell stack must be registered (bash alone is unusable there).
	names := make(map[string]bool)
	for _, spec := range tools.Specs() {
		names[spec.Name] = true
	}
	for _, want := range []string{"pwsh", "bash", "write_file", "working_directory"} {
		if !names[want] {
			t.Fatalf("registry lacks tool %q (have %v)", want, names)
		}
	}

	writeFile := func(path string) rig.ToolResult {
		t.Helper()
		result, invokeErr := tools.Invoke(context.Background(), rig.ToolCall{
			Name:      "write_file",
			Arguments: `{"path":` + quoteJSON(path) + `,"content":"snake"}`,
		})
		if invokeErr != nil {
			t.Fatalf("invoke write_file %q: %v", path, invokeErr)
		}
		return result
	}

	// No workspace yet: writes must be denied instead of landing in the
	// program directory.
	if result := writeFile("snake.html"); !result.IsError {
		t.Fatalf("write without workspace succeeded: %+v", result)
	}
	if _, statErr := os.Stat(filepath.Join(programDir, "snake.html")); statErr == nil {
		t.Fatal("write without workspace leaked into the process working directory")
	}

	// Open workspace A: the same tool instance writes inside A.
	workspace.set(workspaceA)
	if result := writeFile("snake.html"); result.IsError {
		t.Fatalf("write in workspace A failed: %+v", result)
	}
	if _, statErr := os.Stat(filepath.Join(workspaceA, "snake.html")); statErr != nil {
		t.Fatalf("snake.html not in workspace A: %v", statErr)
	}

	// Switch to workspace B: the next call follows the switch.
	workspace.set(workspaceB)
	if result := writeFile("snake.html"); result.IsError {
		t.Fatalf("write in workspace B failed: %+v", result)
	}
	if _, statErr := os.Stat(filepath.Join(workspaceB, "snake.html")); statErr != nil {
		t.Fatalf("snake.html not in workspace B: %v", statErr)
	}
	if result := writeFile("only-b.html"); result.IsError {
		t.Fatalf("second write in workspace B failed: %+v", result)
	}
	if _, statErr := os.Stat(filepath.Join(workspaceA, "only-b.html")); statErr == nil {
		t.Fatal("write after workspace switch leaked into workspace A")
	}

	// working_directory reports the tool root as "." (relative), unchanged.
	result, err := tools.Invoke(context.Background(), rig.ToolCall{Name: "working_directory", Arguments: "{}"})
	if err != nil || result.IsError || result.Content != "." {
		t.Fatalf("working_directory = (%q, %v, %v), want \".\"", result.Content, result.IsError, err)
	}
}

// quoteJSON quotes a string for embedding in a JSON object.
func quoteJSON(value string) string {
	return `"` + value + `"`
}
