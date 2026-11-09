package rightpanel

import (
	"context"
	"errors"
	"fmt"
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
	// viewer instead of stretching the inspector pane.
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
}

// buildChangesPane is the inspector's Changes tab: the current workspace's
// uncommitted files, one collapsible native card per file with its unified
// diff. It degrades to readable empty states without a backend, without an open
// workspace and without a differ.
func buildChangesPane(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	if workspace == nil {
		buildEmptyPane(c, uikit.T(st.locale, "工作区未启用", "Workspace is not enabled"))
		return
	}
	if _, ok, err := workspace.Current(context.Background()); err != nil {
		uikit.ErrorLine(c, uikit.T(st.locale, "读取工作区失败：", "Failed to read the workspace: ")+err.Error())
		return
	} else if !ok {
		buildEmptyPane(c, uikit.T(st.locale, "还没有打开工作区", "No workspace is open"))
		return
	}
	differ, ok := workspace.(contracts.WorkspaceDiffer)
	if !ok {
		buildEmptyPane(c, uikit.T(st.locale, "当前工作区不支持变更视图", "The current workspace does not support the changes view"))
		return
	}
	if !st.changesLoaded {
		st.changes, st.changesErr = differ.Changes(context.Background())
		st.changesLoaded = true
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		// The refresh lives in the pane header; the body carries the count and
		// the changed files as rows.
		if st.changesErr == nil {
			design.Apply(ui.Text(c, uikit.T(st.locale,
				fmt.Sprintf("%d 个变更文件", len(st.changes)), fmt.Sprintf("%d changed files", len(st.changes)))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
		if st.changesErr != nil {
			uikit.ErrorLine(c, uikit.T(st.locale, "读取变更失败：", "Failed to read changes: ")+st.changesErr.Error())
			return
		}
		if len(st.changes) == 0 {
			design.Apply(ui.Text(c, uikit.T(st.locale, "工作区没有未提交的变更", "No uncommitted changes in the workspace")), t.TextMuted(design.TypeXS))
			return
		}
		for _, change := range st.changes {
			buildChangeRow(c, t, st, change)
		}
	})
}

// buildChangeRow is one changed file: status badge, path, +/- stats and a
// native collapsible with the official diff viewer (collapsed by default).
// Rows and dividers, not cards: a 320 DIP inspector only holds a card or two.
func buildChangeRow(c *ui.Context, t *design.Tokens, st *state, change contracts.FileChange) {
	path := firstNonEmpty(change.Path, uikit.T(st.locale, "(未知路径)", "(unknown path)"))
	key := path + "\x00" + change.Status
	c.Key("change/" + key)
	additions, deletions := diffLineStats(change.Diff)

	ui.Column(c).FillWidth().Padding(0, design.Space2).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, changeStatusLabel(change.Status, st.locale)), t.Badge()).
				TextColor(changeStatusColor(t, change.Status))
			design.Apply(ui.Text(c, path), t.MonoFamily()).FontSize(13).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			if additions > 0 {
				design.Apply(ui.Text(c, fmt.Sprintf("+%d", additions)), t.Font(design.TypeXXXS)).
					TextColor(t.SuccessText)
			}
			if deletions > 0 {
				design.Apply(ui.Text(c, fmt.Sprintf("-%d", deletions)), t.Font(design.TypeXXXS)).
					TextColor(t.DangerText)
			}
		})
		if strings.TrimSpace(change.Diff) == "" {
			design.Apply(ui.Text(c, uikit.T(st.locale, "无文本 diff", "No text diff")), t.TextMuted(design.TypeXXXS))
			return
		}
		open := st.changesOpen[key]
		ui.Collapsible(c, uikit.T(st.locale, "查看 diff", "View diff"), &open, func() {
			buildDiff(c, t, st, change)
		})
		st.changesOpen[key] = open
		ui.Divider(c)
	})
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
			// (the shared long-body bound), rather than stretching the pane.
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
	if cached, ok := st.diffs[key]; ok {
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
		// Short diffs take their natural height; long ones get a bounded body
		// and scroll inside the viewer (set in buildDiff).
		state: &editor.DiffState{Layout: editor.DiffUnified, FitContent: lines <= diffFitLines},
		lines: lines,
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
			design.Apply(ui.Text(c, line), t.MonoFamily()).FontSize(11).
				TextColor(diffLineColor(t, line))
		}
		if truncated {
			design.Apply(ui.Text(c, uikit.T(locale, "diff 已截断", "Diff truncated")), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary)
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

// changeStatusLabel renders the short git status as a localized label.
func changeStatusLabel(status string, locale contracts.LocaleService) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "M":
		return uikit.T(locale, "修改", "Modified")
	case "A":
		return uikit.T(locale, "新增", "Added")
	case "D":
		return uikit.T(locale, "删除", "Deleted")
	case "R":
		return uikit.T(locale, "重命名", "Renamed")
	case "??", "U":
		return uikit.T(locale, "未跟踪", "Untracked")
	case "":
		return uikit.T(locale, "未知", "Unknown")
	default:
		return strings.TrimSpace(status)
	}
}

// changeStatusColor colors the status badge.
func changeStatusColor(t *design.Tokens, status string) ui.Color {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "A":
		return t.SuccessText
	case "M":
		return t.WarningText
	case "D":
		return t.DangerText
	case "R":
		return t.Accent
	case "??":
		return t.LabelSecondary
	default:
		return t.LabelTertiary
	}
}
