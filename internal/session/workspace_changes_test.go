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
	// The fake model call mutates the workspace mid-turn: that is the work a
	// real turn's tools would do.
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, _ int) []runtime.Chunk {
		_ = os.Remove(filepath.Join(root, "to-delete.txt"))
		_ = os.WriteFile(filepath.Join(root, "turn-added.txt"), []byte("new"), 0o600)
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarness(t, provider)
	root = h.dir
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
