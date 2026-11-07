package rightpanel

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
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
		buildEmptyPane(c, t, icons.Activity(), "工作区未启用")
		return
	}
	if _, ok, err := workspace.Current(context.Background()); err != nil {
		buildErrorLine(c, t, "读取工作区失败："+err.Error())
		return
	} else if !ok {
		buildEmptyPane(c, t, icons.Folder(), "还没有打开工作区")
		return
	}
	differ, ok := workspace.(contracts.WorkspaceDiffer)
	if !ok {
		buildEmptyPane(c, t, icons.Activity(), "当前工作区不支持变更视图")
		return
	}
	if !st.changesLoaded {
		st.changes, st.changesErr = differ.Changes(context.Background())
		st.changesLoaded = true
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Text(c, "变更"), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			if st.changesErr == nil {
				design.Apply(ui.Badge(c, fmt.Sprintf("%d", len(st.changes))), t.Badge())
			}
			ui.Spacer(c).Grow(1)
			refresh := ui.Button(c, "刷新").Tooltip("重新读取工作区变更")
			if refresh.Clicked() {
				st.changesLoaded = false
				st.changesErr = nil
				runtime.Invalidate()
			}
		})
		if st.changesErr != nil {
			buildErrorLine(c, t, "读取变更失败："+st.changesErr.Error())
			return
		}
		if len(st.changes) == 0 {
			design.Apply(ui.Text(c, "工作区没有未提交的变更"), t.TextMuted(design.TypeXS))
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
	path := firstNonEmpty(change.Path, "(未知路径)")
	key := path + "\x00" + change.Status
	additions, deletions := diffLineStats(change.Diff)

	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space2).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, changeStatusLabel(change.Status)), t.Badge()).
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
			design.Apply(ui.Text(c, "无文本 diff"), t.TextMuted(design.TypeXXXS))
			return
		}
		open := st.changesOpen[key]
		ui.Collapsible(c, "查看 diff", &open, func() {
			buildDiff(c, t, change.Diff)
		})
		st.changesOpen[key] = open
	})
}

// buildDiff renders a unified diff line by line with the brand colors: additions
// success, deletions danger, hunk/file headers tertiary and context secondary.
// The diff is bounded to maxDiffLines.
func buildDiff(c *ui.Context, t *design.Tokens, diff string) {
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
			design.Apply(ui.Text(c, "diff 已截断"), t.Font(design.TypeXXXS)).
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

// changeStatusLabel renders the short git status as a Chinese label.
func changeStatusLabel(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "M":
		return "修改"
	case "A":
		return "新增"
	case "D":
		return "删除"
	case "R":
		return "重命名"
	case "??", "U":
		return "未跟踪"
	case "":
		return "未知"
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
