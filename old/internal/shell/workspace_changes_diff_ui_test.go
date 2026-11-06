package shell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// diffTurnSnapshot is a completed turn whose changed files carry persisted
// inline diffs and markers, the shape the runtime emits after the FP-05
// upgrade.
func diffTurnSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.SessionID = "workspace-diff"
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "fix the parser", Seq: 1, LastSeq: 1},
		{MsgID: "a1", Role: state.RoleAssistant, Text: "Done.", Seq: 2, LastSeq: 2},
	}
	snap.WorkspaceChanges = []state.WorkspaceChange{
		{
			Path:   "parser.go",
			Status: "modified",
			Diff:   "@@ -1,3 +1,3 @@\n context\n-old line\n+new line\n tail",
			Added:  1, Removed: 1,
		},
		{
			Path:    "legacy.go",
			Status:  "deleted",
			Diff:    "@@ -1,1 +0,0 @@\n-bye",
			Removed: 1,
		},
	}
	snap.WorkspaceChangesTurn = "u1"
	return snap
}

// expandableTurnSnapshot is one changed file whose diff carries the given
// markers, for the marker-hint subtests.
func expandableTurnSnapshot(change state.WorkspaceChange) *state.Snapshot {
	snap := state.NewSnapshot()
	snap.SessionID = "workspace-diff-marker"
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "fix it", Seq: 1, LastSeq: 1},
	}
	change.Path = "file.txt"
	change.Status = "modified"
	snap.WorkspaceChanges = []state.WorkspaceChange{change}
	snap.WorkspaceChangesTurn = "u1"
	return snap
}

// TestWorkspaceChangesDiffExpandCollapse pins the inline diff rows: the diff is
// collapsed by default, the per-file toggle expands it into a unified diff
// (hunk header, context and +/- lines) and collapses it again.
func TestWorkspaceChangesDiffExpandCollapse(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, diffTurnSnapshot())
	workspaceDiffStates.Delete(h.app)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText("Workspace changes") {
		t.Fatalf("workspace changes block missing: %q", tt.Texts())
	}
	if tt.HasText("-old line") || tt.HasText("@@ -1,3 +1,3 @@") {
		t.Fatalf("diff rendered while collapsed: %q", tt.Texts())
	}

	if err := tt.Click("Diff for parser.go"); err != nil {
		t.Fatalf("expand parser.go diff: %v", err)
	}
	tt.Frame()
	for _, want := range []string{"@@ -1,3 +1,3 @@", " context", "-old line", "+new line", " tail"} {
		if !tt.HasText(want) {
			t.Fatalf("expanded diff missing %q: %q", want, tt.Texts())
		}
	}
	// Opening one file's diff leaves the other row collapsed.
	if tt.HasText("-bye") {
		t.Fatalf("expanding parser.go expanded legacy.go too: %q", tt.Texts())
	}

	if err := tt.Click("Diff for parser.go"); err != nil {
		t.Fatalf("collapse parser.go diff: %v", err)
	}
	tt.Frame()
	if tt.HasText("-old line") {
		t.Fatalf("diff still rendered after collapse: %q", tt.Texts())
	}
}

// TestWorkspaceChangesDiffMarkerHints pins the non-text comparisons: binary,
// oversized, coarse and truncated diffs expand into an explanatory hint.
func TestWorkspaceChangesDiffMarkerHints(t *testing.T) {
	cases := []struct {
		name   string
		change state.WorkspaceChange
		want   string
	}{
		{"binary", state.WorkspaceChange{Binary: true}, "Binary file; no text comparison"},
		{"oversized", state.WorkspaceChange{Oversized: true}, "File too large for a comparison"},
		{"coarse", state.WorkspaceChange{Coarse: true, Diff: "@@ -1,1 +1,1 @@\n-a\n+b", Added: 1, Removed: 1}, "Large change; whole-file comparison"},
		{"truncated", state.WorkspaceChange{Truncated: true, Diff: "@@ -1,1 +1,1 @@\n-a\n+b", Added: 1, Removed: 1}, "Diff truncated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveSnapshotApp(t, Session, expandableTurnSnapshot(tc.change))
			workspaceDiffStates.Delete(h.app)
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			if err := tt.Click("Diff for file.txt"); err != nil {
				t.Fatalf("expand %s diff: %v", tc.name, err)
			}
			tt.Frame()
			if !tt.HasText(tc.want) {
				t.Fatalf("%s hint %q missing: %q", tc.name, tc.want, tt.Texts())
			}
		})
	}
}

// TestWorkspaceDiffLineColors pins the diff line styling: added lines use the
// success token, removed lines the danger token, hunk headers the tertiary
// label and context the secondary label, in both themes.
func TestWorkspaceDiffLineColors(t *testing.T) {
	a := &App{}
	cases := []struct {
		line string
		want func(t *design.Tokens) ui.Color
	}{
		{"+new line", func(t *design.Tokens) ui.Color { return t.Success }},
		{"-old line", func(t *design.Tokens) ui.Color { return t.Danger }},
		{"@@ -1,3 +1,3 @@", func(t *design.Tokens) ui.Color { return t.LabelTertiary }},
		{" context", func(t *design.Tokens) ui.Color { return t.LabelSecondary }},
	}
	for _, tokens := range []*design.Tokens{design.Light(), design.Dark()} {
		for _, tc := range cases {
			if got := a.workspaceDiffLineColor(tokens, tc.line); got != tc.want(tokens) {
				t.Errorf("color(%q) = %v, want %v", tc.line, got, tc.want(tokens))
			}
		}
	}
}

// TestWorkspaceChangesDiffAccessibleKeyboard pins the accessibility contract:
// each toggle is a labeled disclosure exposing its expanded state, and it
// opens and closes from the keyboard (Enter) once focused.
func TestWorkspaceChangesDiffAccessibleKeyboard(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, diffTurnSnapshot())
	workspaceDiffStates.Delete(h.app)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The disclosure is named even when collapsed and reports collapsed.
	states := testerAccessStates(t, tt)
	before, ok := states["Diff for parser.go"]
	if !ok {
		t.Fatalf("accessibility tree has no disclosure labeled %q", "Diff for parser.go")
	}
	if before&accessExpandedState != 0 {
		t.Fatalf("%q is expanded while the diff is collapsed", "Diff for parser.go")
	}

	// A click focuses the disclosure and opens it.
	if err := tt.Click("Diff for parser.go"); err != nil {
		t.Fatalf("click disclosure: %v", err)
	}
	tt.Frame()
	states = testerAccessStates(t, tt)
	if states["Diff for parser.go"]&accessExpandedState == 0 {
		t.Fatal("disclosure did not expose AccessExpanded while the diff is open")
	}
	if !tt.HasText("-old line") {
		t.Fatalf("opened diff missing: %q", tt.Texts())
	}

	// The focused disclosure toggles from the keyboard.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tt.HasText("-old line") {
		t.Fatalf("Enter did not collapse the diff: %q", tt.Texts())
	}
	if states := testerAccessStates(t, tt); states["Diff for parser.go"]&accessExpandedState != 0 {
		t.Fatal("collapsed disclosure still exposes AccessExpanded")
	}
}

// TestWorkspaceChangesDiffRowStillOpensFile pins that the row action survives
// the expandable layout: clicking the path (not the disclosure) opens the file
// in the Files tab through the same preview the tree uses.
func TestWorkspaceChangesDiffRowStillOpensFile(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, diffTurnSnapshot())
	workspaceDiffStates.Delete(h.app)
	if err := os.WriteFile(filepath.Join(h.app.workspaceRoot(), "parser.go"), []byte("package parser\n"), 0o600); err != nil {
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
}
