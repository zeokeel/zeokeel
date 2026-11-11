package agent

// Tests for per-run workspace-root scoping: a session bound to workspace A must
// run its prompt and tools against A even when the current workspace is B.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// multiRootWorkspace is a two-workspace registry for the scoping tests.
type multiRootWorkspace struct {
	roots     map[string]string
	currentID string
}

func (w *multiRootWorkspace) workspaces() []contracts.Workspace {
	ids := make([]string, 0, len(w.roots))
	for id := range w.roots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	workspaces := make([]contracts.Workspace, 0, len(ids))
	for _, id := range ids {
		workspaces = append(workspaces, contracts.Workspace{ID: id, Name: id, Root: w.roots[id]})
	}
	return workspaces
}

func (w *multiRootWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return w.workspaces(), nil
}

func (w *multiRootWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	for _, workspace := range w.workspaces() {
		if workspace.ID == w.currentID {
			return workspace, true, nil
		}
	}
	return contracts.Workspace{}, false, nil
}

func (w *multiRootWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (w *multiRootWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	w.currentID = id
	return contracts.Workspace{ID: id, Root: w.roots[id]}, nil
}

func (w *multiRootWorkspace) Delete(context.Context, string) error { return nil }
func (w *multiRootWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (w *multiRootWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}
func (w *multiRootWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func TestRunUsesSessionWorkspaceRootNotCurrent(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	for root, instruction := range map[string]string{rootA: "ROOT-A-INSTRUCTION", rootB: "ROOT-B-INSTRUCTION"} {
		if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(instruction), 0o644); err != nil {
			t.Fatalf("write AGENTS.md in %s: %v", root, err)
		}
	}
	workspace := &multiRootWorkspace{
		roots:     map[string]string{"w1": rootA, "w2": rootB},
		currentID: "w2",
	}
	provider := &captureProvider{}
	svc := newService(Config{SystemPrompt: "base"}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.workspace = workspace
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionWorkspace(sessionID, "w1"); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	system := requestSystemText(t, provider.lastRequest())
	if !strings.Contains(system, "Working directory: "+rootA) {
		t.Fatalf("system prompt working directory is not the session workspace %q: %s", rootA, system)
	}
	if strings.Contains(system, rootB) {
		t.Fatalf("system prompt leaked the current (other) workspace %q: %s", rootB, system)
	}
	if !strings.Contains(system, "ROOT-A-INSTRUCTION") {
		t.Fatalf("AGENTS.md from the session workspace was not injected: %s", system)
	}
	if strings.Contains(system, "ROOT-B-INSTRUCTION") {
		t.Fatalf("AGENTS.md from the current (other) workspace was injected: %s", system)
	}
}

// recordingRootTool records the workspace-root override it receives.
type recordingRootTool struct {
	seen string
}

func (t *recordingRootTool) Name() string           { return "recording_root" }
func (t *recordingRootTool) Description() string    { return "records the run workspace root" }
func (t *recordingRootTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *recordingRootTool) Invoke(ctx context.Context, _ rig.ToolCall) (rig.ToolResult, error) {
	t.seen = contracts.WorkspaceRootFromContext(ctx)
	return rig.ToolResult{Content: "ok"}, nil
}

func TestWithWorkspaceRootStampsToolContext(t *testing.T) {
	tool := &recordingRootTool{}
	scoped := withWorkspaceRoot([]rig.Tool{tool}, "/work/project")
	if len(scoped) != 1 {
		t.Fatalf("wrapped tools = %d, want 1", len(scoped))
	}
	if _, err := scoped[0].Invoke(context.Background(), rig.ToolCall{Name: tool.Name()}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if tool.seen != "/work/project" {
		t.Fatalf("tool saw workspace root %q, want /work/project", tool.seen)
	}
	// No root (unbound/headless) leaves the tools untouched.
	if unchanged := withWorkspaceRoot([]rig.Tool{tool}, ""); unchanged[0] != rig.Tool(tool) {
		t.Fatalf("empty root should not wrap the tools")
	}
}
