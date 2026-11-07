package rightpanel

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// maxDiffLines bounds the unified diff rendered for one file; the rest is
// summarized as truncated.
const maxDiffLines = 400

// buildChangesPane is the inspector's Changes tab: the current workspace's
// uncommitted files, one collapsible native card per file with its unified
// diff. It degrades to readable empty states without a backend, without an open
// workspace and without a differ.
func buildChangesPane(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService, runtime contracts.UIRuntime, st *state) {
	if workspace == nil {
		buildEmptyPane(c, t, icons.Activity(), uikit.T(st.locale, "工作区未启用", "Workspace is not enabled"))
		return
	}
	if _, ok, err := workspace.Current(context.Background()); err != nil {
		buildErrorLine(c, t, uikit.T(st.locale, "读取工作区失败：", "Failed to read the workspace: ")+err.Error())
		return
	} else if !ok {
		buildEmptyPane(c, t, icons.Folder(), uikit.T(st.locale, "还没有打开工作区", "No workspace is open"))
		return
	}
	differ, ok := workspace.(contracts.WorkspaceDiffer)
	if !ok {
		buildEmptyPane(c, t, icons.Activity(), uikit.T(st.locale, "当前工作区不支持变更视图", "The current workspace does not support the changes view"))
		return
	}
	if !st.changesLoaded {
		st.changes, st.changesErr = differ.Changes(context.Background())
		st.changesLoaded = true
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Text(c, uikit.T(st.locale, "变更", "Changes")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			if st.changesErr == nil {
				design.Apply(ui.Badge(c, fmt.Sprintf("%d", len(st.changes))), t.Badge())
			}
			ui.Spacer(c).Grow(1)
			refresh := ui.Button(c, uikit.T(st.locale, "刷新", "Refresh")).
				Tooltip(uikit.T(st.locale, "重新读取工作区变更", "Reload the workspace changes"))
			if refresh.Clicked() {
				st.changesLoaded = false
				st.changesErr = nil
				runtime.Invalidate()
			}
		})
		if st.changesErr != nil {
			buildErrorLine(c, t, uikit.T(st.locale, "读取变更失败：", "Failed to read changes: ")+st.changesErr.Error())
			return
		}
		if len(st.changes) == 0 {
			design.Apply(ui.Text(c, uikit.T(st.locale, "工作区没有未提交的变更", "No uncommitted changes in the workspace")), t.TextMuted(design.TypeXS))
			return
		}
		for _, change := range st.changes {
			buildChangeCard(c, t, st, change)
		}
	})
}

// buildChangeCard is one changed file: status badge, path, +/- stats and a
// native collapsible with the unified diff (collapsed by default).
func buildChangeCard(c *ui.Context, t *design.Tokens, st *state, change contracts.FileChange) {
	path := firstNonEmpty(change.Path, uikit.T(st.locale, "(未知路径)", "(unknown path)"))
	key := path + "\x00" + change.Status
	additions, deletions := diffLineStats(change.Diff)

	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, changeStatusLabel(change.Status, st.locale)), t.Badge()).
				TextColor(changeStatusColor(t, change.Status))
			design.Apply(ui.Text(c, path), t.MonoFamily()).FontSize(13).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			if additions > 0 {
				design.Apply(ui.Text(c, fmt.Sprintf("+%d", additions)), t.Font(design.TypeXXXS)).
					TextColor(t.Success)
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
			buildDiff(c, t, change.Diff, st.locale)
		})
		st.changesOpen[key] = open
	})
}

// buildDiff renders a unified diff line by line with the brand colors: additions
// success, deletions danger, hunk/file headers tertiary and context secondary.
// The diff is bounded to maxDiffLines.
func buildDiff(c *ui.Context, t *design.Tokens, diff string, locale contracts.LocaleService) {
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
		return t.Success
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
		return t.Success
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
