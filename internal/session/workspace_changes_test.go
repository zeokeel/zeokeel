package session_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// TestWorkspaceChangesReportedAfterTurn is the FP-05 controller regression: the
// turn's workspace baseline is captured at Submit, the post-turn diff is emitted
// as state.WorkspaceChanges, and the snapshot names the files the turn changed.
func TestWorkspaceChangesReportedAfterTurn(t *testing.T) {
	var root string
	fileTool := &scriptedFileTool{}
	// The fake tools mutate the workspace mid-turn: that is the work a real
	// turn's write_file/delete calls would do. A non-Git workspace attributes
	// only file-tool edits to the turn, so the fake model cannot write the
	// files directly.
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call > 0 {
			return []runtime.Chunk{{Text: "done", Done: true}}
		}
		return []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{
				{ID: "call-add", Name: "write_file", Arguments: []byte(`{"path":"turn-added.txt"}`)},
				{ID: "call-delete", Name: "write_file", Arguments: []byte(`{"path":"to-delete.txt"}`)},
			},
			Done: true,
		}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		if err := cfg.Tools.Register(fileTool); err != nil {
			t.Fatalf("register file tool: %v", err)
		}
	})
	root = h.dir
	fileTool.root = root
	fileTool.mutations = map[string]string{
		"turn-added.txt": "new",
		"to-delete.txt":  "",
	}
	if err := os.WriteFile(filepath.Join(root, "to-delete.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "workspace changes")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "change some files"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	snap := h.ctrl.Snapshot()
	if snap.WorkspaceChangesTurn == "" {
		t.Fatalf("snapshot has no workspace/changes turn: %+v", snap)
	}
	statuses := make(map[string]string, len(snap.WorkspaceChanges))
	for _, change := range snap.WorkspaceChanges {
		statuses[change.Path] = change.Status
	}
	if got := statuses["turn-added.txt"]; got != "added" {
		t.Errorf("turn-added.txt status = %q, want added; changes=%+v", got, snap.WorkspaceChanges)
	}
	if got := statuses["to-delete.txt"]; got != "deleted" {
		t.Errorf("to-delete.txt status = %q, want deleted; changes=%+v", got, snap.WorkspaceChanges)
	}
}
