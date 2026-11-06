package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// workspaceWriteTool is a test-only file tool: it writes one workspace file,
// which is how the integration test stands in for the real write_file tool
// without importing the tools package's sandbox.
type workspaceWriteTool struct {
	dir     string
	path    string
	content string
}

func (t *workspaceWriteTool) Name() string        { return "write_file" }
func (t *workspaceWriteTool) Description() string { return "test-only workspace writer" }

func (t *workspaceWriteTool) Invoke(_ context.Context, _ json.RawMessage) (string, error) {
	if err := os.WriteFile(filepath.Join(t.dir, t.path), []byte(t.content), 0o600); err != nil {
		return "", err
	}
	return "written", nil
}

// scriptedFileTool is a test-only file tool named after the real write_file:
// each path's Invoke writes the mutation in its map (empty content deletes the
// path). The runtime's ToolStart event names the tool and the path, which is
// what the changed-file report attributes the edit by.
type scriptedFileTool struct {
	root      string
	mutations map[string]string
}

func (t *scriptedFileTool) Name() string        { return "write_file" }
func (t *scriptedFileTool) Description() string { return "test-only scripted file tool" }

func (t *scriptedFileTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	content, ok := t.mutations[a.Path]
	if !ok {
		return "", nil
	}
	path := filepath.Join(t.root, a.Path)
	if content == "" {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		return "removed", nil
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return "written", nil
}

// TestWorkspaceChangesDiffPersistedAndReplayed pins the inline-diff upgrade:
// a file-tool edit during a turn becomes a state.WorkspaceChanges event with a
// unified diff, a shell-only change in a non-Git workspace stays out, and the
// diff survives a controller restart through event replay.
func TestWorkspaceChangesDiffPersistedAndReplayed(t *testing.T) {
	var root string
	var writer *workspaceWriteTool
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call == 0 {
			// A shell-visible change in a workspace that is not a Git
			// repository must not be attributed to the turn.
			_ = os.WriteFile(filepath.Join(root, "shell-only.txt"), []byte("shell\n"), 0o600)
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{
					ID:        "call-write",
					Name:      "write_file",
					Arguments: json.RawMessage(`{"path":"notes.txt"}`),
				}},
				Done: true,
			}}
		}
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		writer = &workspaceWriteTool{}
		if err := cfg.Tools.Register(writer); err != nil {
			t.Fatalf("register write_file: %v", err)
		}
	})
	root = h.dir
	writer.dir, writer.path, writer.content = root, "notes.txt", "beta\n"
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatalf("seed notes.txt: %v", err)
	}

	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "workspace diff")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "edit notes"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	snap := h.ctrl.Snapshot()
	if snap.WorkspaceChangesTurn == "" {
		t.Fatalf("snapshot has no workspace/changes turn: %+v", snap)
	}
	changeIndex := -1
	for i, got := range snap.WorkspaceChanges {
		if got.Path == "shell-only.txt" {
			t.Fatalf("shell-only change was reported in a non-Git workspace: %+v", snap.WorkspaceChanges)
		}
		if got.Path == "notes.txt" {
			changeIndex = i
		}
	}
	if changeIndex < 0 {
		t.Fatalf("notes.txt missing from workspace changes: %+v", snap.WorkspaceChanges)
	}
	change := snap.WorkspaceChanges[changeIndex]
	if change.Status != "modified" {
		t.Fatalf("notes.txt status = %q, want modified", change.Status)
	}
	for _, want := range []string{"-alpha", "+beta"} {
		if !strings.Contains(change.Diff, want) {
			t.Fatalf("notes.txt event diff missing %q: %q", want, change.Diff)
		}
	}

	// The diff is persisted in the session event, so a reopen can replay it.
	events, err := h.ctrl.Events(ctx, id)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type != session.EventWorkspaceChanges {
			continue
		}
		found = true
		payload := string(event.Payload)
		if !strings.Contains(payload, `"diff":"`) {
			t.Fatalf("persisted workspace/changes payload has no diff: %s", payload)
		}
		if !strings.Contains(payload, `"added":1`) || !strings.Contains(payload, `"removed":1`) {
			t.Fatalf("persisted workspace/changes payload has no line counts: %s", payload)
		}
	}
	if !found {
		t.Fatalf("no workspace/changes event persisted: %+v", events)
	}

	// Restart over the same store: the replayed snapshot still carries the
	// diff, so a reopened session shows it without re-scanning the workspace.
	h.restart()
	reopened, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	replayed := false
	for _, got := range reopened.WorkspaceChanges {
		if got.Path != "notes.txt" {
			continue
		}
		replayed = true
		if !strings.Contains(got.Diff, "-alpha") || !strings.Contains(got.Diff, "+beta") {
			t.Fatalf("replayed diff = %q, want the unified diff", got.Diff)
		}
		if got.Added != 1 || got.Removed != 1 {
			t.Fatalf("replayed added/removed = %d/%d, want 1/1", got.Added, got.Removed)
		}
	}
	if !replayed {
		t.Fatalf("reopened snapshot lost the workspace diff: %+v", reopened.WorkspaceChanges)
	}
}
