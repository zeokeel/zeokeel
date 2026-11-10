package rightpanel

import (
	"context"
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// maxDiffLines bounds the plain-text fallback rendered for a diff the
	// official parser rejects (a truncated or partial patch); the rest is
	// summarized as truncated.
	maxDiffLines = 400
	// diffFitLines is how many patch lines the official viewer shows at its
	// natural height. Longer diffs get a bounded height and scroll inside the
	// viewer instead of stretching the file tab.
	diffFitLines = 20
)

// errNoDiffPatch marks a diff string the official parser read no file from.
var errNoDiffPatch = errors.New("rightpanel: the diff has no readable file patch")

// diffView is one changed file's cached official diff viewer: the editor's
// DiffState kept across frames (its scroll, folds and selections) and how many
// patch lines it shows, to lay it out at its natural or its bounded height.
type diffView struct {
	state *editor.DiffState
	lines int
	// diff is the patch text the viewer was built from: the cache is only
	// reused while the workspace reports the same content, so a reload (or a
	// workspace switch) can never show a stale diff even when the file's
	// path/status key is unchanged.
	diff string
}

// buildFileDiffFold renders the file tab's fold-out unified diff when the
// current workspace reports the opened file as changed: the retired Changes
// pane is folded into the file tab, and the session's "view changes" action
// lands on the same tab. It is absent for a clean file, a workspace without a
// differ, and while the first changes read is still in flight.
func buildFileDiffFold(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	st *state, path string) {
	differ, ok := workspace.(contracts.WorkspaceDiffer)
	if !ok {
		return
	}
	loading := st.ensureChangesLoaded(workspace, differ, st.runtime)
	if st.changesErr != nil {
		uikit.ErrorLine(c, st.tr("读取变更失败：", "Failed to read changes: ")+st.changesErr.Error())
		return
	}
	if loading && st.changes == nil {
		return
	}
	change, found := findFileChange(st.changes, path)
	if !found {
		return
	}
	open := st.changesOpen[path]
	ui.Collapsible(c, st.tr("查看改动", "View changes"), &open, func() {
		additions, deletions := diffLineStats(change.Diff)
		if additions > 0 || deletions > 0 {
			design.Apply(ui.Text(c, "+"+itoa(additions)+" -"+itoa(deletions)),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		}
		if strings.TrimSpace(change.Diff) == "" {
			design.Apply(ui.Text(c, st.tr("无文本 diff", "No text diff")),
				t.TextMuted(design.TypeXXXS))
			return
		}
		buildDiff(c, t, st, change)
	})
	st.changesOpen[path] = open
}

// findFileChange finds the workspace change of one relative path.
func findFileChange(changes []contracts.FileChange, path string) (contracts.FileChange, bool) {
	for _, change := range changes {
		if strings.TrimSuffix(change.Path, "/") == strings.TrimSuffix(path, "/") {
			return change, true
		}
	}
	return contracts.FileChange{}, false
}

// itoa is a tiny positive integer renderer for the diff stat suffix.
func itoa(value int) string {
	if value <= 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

// ensureChangesLoaded starts the workspace-diff read off the UI thread when the
// cache was invalidated (first changed-file tab, a workspace switch or the
// Files page's refresh). It reports whether a read is in flight, so the tab can
// keep the cached rows while the read lands. A result for a workspace the user
// already switched away from is dropped.
func (st *state) ensureChangesLoaded(workspace contracts.WorkspaceService,
	differ contracts.WorkspaceDiffer, runtime contracts.UIRuntime) bool {
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		st.changesLoaded = true
		st.changesWorkspace = ""
		st.changesErr = err
		return false
	}
	workspaceKey := current.ID + "\x00" + current.Root
	// The cached snapshot and the per-file diff viewers belong to one
	// workspace: a switch invalidates both instead of showing another project's
	// uncommitted files. Bumping the generation drops a read that was already
	// in flight for the old workspace.
	if st.changesWorkspace != workspaceKey {
		st.changesWorkspace = workspaceKey
		st.changesLoaded = false
		st.changes = nil
		st.changesErr = nil
		st.changesOpen = make(map[string]bool)
		st.diffs = make(map[string]*diffView)
		st.changesLoading = false
		st.changesGeneration++
	}
	if st.changesLoaded {
		return false
	}
	if st.changesLoading {
		return true
	}
	if runtime == nil {
		// A bare state (unit tests) keeps the old synchronous read.
		st.changes, st.changesErr = differ.Changes(context.Background())
		st.changesLoaded = true
		return false
	}
	st.changesLoading = true
	generation := st.changesGeneration
	go func() {
		changes, changesErr := differ.Changes(context.Background())
		runtime.Post(func() {
			// A refresh (or a workspace switch) after the read started
			// superseded it: never apply the pre-refresh snapshot over the
			// fresh state.
			if st.changesGeneration != generation {
				return
			}
			st.changesLoading = false
			now, ok := st.currentWorkspaceKey(workspace)
			if !ok || now != workspaceKey {
				runtime.Invalidate()
				return
			}
			st.changes, st.changesErr = changes, changesErr
			st.changesLoaded = true
			runtime.Invalidate()
		})
	}()
	return true
}

// buildDiff renders one file's unified diff: the official editor.DiffView of a
// cached editor.DiffState (docs/ui/diff-viewer.md), with the unchanged lines
// folded by the viewer itself. A diff the official parser reads no file from
// (a truncated or headerless patch) falls back to bounded plain-text lines, so
// a broken patch still explains itself instead of showing an empty box.
func buildDiff(c *ui.Context, t *design.Tokens, st *state, change contracts.FileChange) {
	view, err := st.diffViewFor(change)
	if err == nil && view != nil {
		viewer := editor.DiffView(c, view.state).FillWidth()
		if !view.state.FitContent {
			// A long diff gets a bounded body and scrolls inside the viewer
			// rather than stretching the file tab.
			viewer.Height(design.DialogBodyMax)
		}
		return
	}
	buildDiffFallback(c, t, change.Diff, st.locale)
}

// diffViewFor returns the cached official diff viewer for one changed file,
// building it with editor.ParsePatch/SetPatch the first time and keeping it
// across frames (scroll position, folds and keyboard focus live in it).
func (st *state) diffViewFor(change contracts.FileChange) (*diffView, error) {
	if st.diffs == nil {
		st.diffs = make(map[string]*diffView)
	}
	key := change.Path + "\x00" + change.Status
	if cached, ok := st.diffs[key]; ok && cached.diff == change.Diff {
		return cached, nil
	}
	patches, err := editor.ParsePatch(change.Diff)
	if len(patches) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errNoDiffPatch
	}
	// A truncated patch still parses the hunks before the cut: show them.
	patch := patches[0]
	lines := patchLineCount(patch)
	view := &diffView{
		state: &editor.DiffState{Layout: editor.DiffUnified, FitContent: lines <= diffFitLines},
		lines: lines,
		diff:  change.Diff,
	}
	view.state.SetPatch(patch)
	st.diffs[key] = view
	return view, nil
}

// patchLineCount counts the lines a patch shows, for the viewer's layout.
func patchLineCount(patch editor.FilePatch) int {
	lines := 0
	for _, hunk := range patch.Hunks {
		for _, line := range hunk.Lines {
			if line == "" || strings.HasPrefix(line, "\\") {
				continue
			}
			lines++
		}
	}
	return lines
}

// buildDiffFallback renders a diff the official parser rejected line by line
// with the brand colors: additions success, deletions danger, hunk/file headers
// tertiary and context secondary. The diff is bounded to maxDiffLines.
func buildDiffFallback(c *ui.Context, t *design.Tokens, diff string, locale contracts.LocaleService) {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	truncated := false
	if len(lines) > maxDiffLines {
		lines = lines[:maxDiffLines]
		truncated = true
	}
	ui.Column(c).FillWidth().Children(func() {
		for _, line := range lines {
			if line == "" {
				continue
			}
			design.Apply(ui.Text(c, line), t.MonoText(design.TypeXXXS)).
				TextColor(diffLineColor(t, line))
		}
		if truncated {
			design.Apply(ui.Text(c, uikit.T(locale, "diff 已截断", "Diff truncated")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		}
	})
}

// diffLineColor maps one unified-diff line to its brand color.
func diffLineColor(t *design.Tokens, line string) ui.Color {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
		strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff "),
		strings.HasPrefix(line, "index "), strings.HasPrefix(line, "new file"),
		strings.HasPrefix(line, "deleted file"), strings.HasPrefix(line, "similarity"),
		strings.HasPrefix(line, "rename"):
		return t.LabelTertiary
	case strings.HasPrefix(line, "+"):
		return t.SuccessText
	case strings.HasPrefix(line, "-"):
		return t.DangerText
	default:
		return t.LabelSecondary
	}
}

// diffLineStats counts the added and removed lines of a unified diff.
func diffLineStats(diff string) (additions, deletions int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
			strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}
