package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
)

// changedTurnSnapshot is one completed turn plus the workspace-changes summary
// the runtime attaches to the latest turn.
func changedTurnSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.SessionID = "workspace-changes"
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "fix the parser", Seq: 1, LastSeq: 1},
		{MsgID: "a1", Role: state.RoleAssistant, Text: "Done.", Seq: 2, LastSeq: 2},
	}
	snap.WorkspaceChanges = []state.WorkspaceChange{
		{Path: "parser.go", Status: "modified"},
		{Path: "parser_test.go", Status: "added"},
		{Path: "legacy.go", Status: "deleted"},
	}
	snap.WorkspaceChangesTurn = "u1"
	return snap
}

// TestWorkspaceChangesBlockRenders pins FP-05's shell side: a completed turn's
// changed files render as a transcript block with their A/M/D state and a
// summary count, and an empty summary renders nothing.
func TestWorkspaceChangesBlockRenders(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, changedTurnSnapshot())
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Workspace changes") {
		t.Fatalf("workspace changes block missing: %q", tt.Texts())
	}
	for _, want := range []string{"parser.go", "parser_test.go", "legacy.go"} {
		if !tt.HasText(want) {
			t.Fatalf("changed file %q missing: %q", want, tt.Texts())
		}
	}
	for _, want := range []string{"Added", "Modified", "Deleted"} {
		if !tt.HasText(want) {
			t.Fatalf("status label %q missing: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("3 files changed") {
		t.Fatalf("summary count missing: %q", tt.Texts())
	}

	// No summary: no block.
	empty := state.NewSnapshot()
	empty.Phase = state.PhaseIdle
	empty.Messages = []state.Message{{MsgID: "u1", Role: state.RoleUser, Text: "hi", Seq: 1, LastSeq: 1}}
	h2 := newLiveSnapshotApp(t, Session, empty)
	tt2 := ui.NewTester(h2.app.View, 1600, 1000)
	tt2.Frame()
	if tt2.HasText("Workspace changes") {
		t.Fatalf("empty snapshot rendered a workspace changes block: %q", tt2.Texts())
	}
}

// TestWorkspaceChangesClickOpensFile pins the row action: clicking a changed
// path opens it in the Files tab through the same preview the tree uses.
func TestWorkspaceChangesClickOpensFile(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, changedTurnSnapshot())
	root := h.app.workspaceRoot()
	if err := os.WriteFile(filepath.Join(root, "parser.go"), []byte("package parser\n"), 0o600); err != nil {
		t.Fatalf("write changed file: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("parser.go"); err != nil {
		t.Fatalf("click changed file: %v", err)
	}
	tt.Frame()
	if !h.app.rightOpen || h.app.rightTab != filesTab {
		t.Fatalf("right panel = open:%v tab:%v, want Files", h.app.rightOpen, h.app.rightTab)
	}
	if got := h.app.filePath; got != "parser.go" {
		t.Fatalf("opened file = %q, want parser.go", got)
	}
	if !tt.HasText("package parser") {
		t.Fatalf("file preview content missing: %q", tt.Texts())
	}
}

// TestWorkspaceChangesReplayOnReopen pins the event path: the changes summary
// arrives as a state.WorkspaceChanges event and survives the snapshot fold, so
// a reopened session renders it from the replayed record.
func TestWorkspaceChangesReplayOnReopen(t *testing.T) {
	snap := state.NewSnapshot()
	snap.SessionID = "replay"
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{{MsgID: "u1", Role: state.RoleUser, Text: "edit", Seq: 1, LastSeq: 1}}
	snap = state.Apply(snap, state.WorkspaceChanges{TurnID: "u1", Changes: []state.WorkspaceChange{
		{Path: "README.md", Status: "added"},
	}})
	if len(snap.WorkspaceChanges) != 1 || snap.WorkspaceChangesTurn != "u1" {
		t.Fatalf("snapshot fold lost the changes event: %+v", snap)
	}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("README.md") || !tt.HasText("1 file changed") {
		t.Fatalf("replayed changes summary missing: %q", tt.Texts())
	}
	_ = context.Background()
}
