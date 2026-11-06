package shell

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// FP-05: the transcript's workspace-changes block. The runtime attaches each
// completed turn's added/modified/deleted files — with their persisted unified
// diffs — to the snapshot (the workspace/changes session event); the shell
// renders them as a compact card under the turn. Every file with a comparison
// is a disclosure: collapsed by default, expanding to a monospace diff with
// hunk headers, colored +/- lines and binary/oversized/coarse/truncated hints.
// Clicking the file row still opens the file in the Files tab. An empty
// summary renders nothing, and a reopened session replays the same event into
// the same card.

// maxWorkspaceDiffViewLines bounds the lines one inline diff draws in the
// transcript. The persisted diff is already bounded; this keeps a very large
// (coarse) comparison from building thousands of text elements per frame.
const maxWorkspaceDiffViewLines = 400

// workspaceDiffStates holds the expand state of each App's diff rows. The state
// is keyed by App because the shell is rebuilt from the snapshot every frame;
// it is reset whenever the summary's turn changes so a new turn starts fully
// collapsed, and it lives here (not in liveUIState) because this file owns the
// workspace-changes widget.
var workspaceDiffStates sync.Map // map[*App]*workspaceDiffState

// workspaceDiffState is one App's expand state for the latest turn's files.
type workspaceDiffState struct {
	turn string
	open map[string]bool
}

// workspaceDiffStateFor returns the App's expand state for turn, starting from
// all-collapsed when the displayed turn changed.
func (a *App) workspaceDiffStateFor(turn string) *workspaceDiffState {
	if raw, ok := workspaceDiffStates.Load(a); ok {
		st := raw.(*workspaceDiffState)
		if st.turn != turn {
			st.turn = turn
			st.open = make(map[string]bool)
		}
		return st
	}
	st := &workspaceDiffState{turn: turn, open: make(map[string]bool)}
	actual, _ := workspaceDiffStates.LoadOrStore(a, st)
	return actual.(*workspaceDiffState)
}

// liveWorkspaceChanges draws the latest turn's changed-file summary.
func (a *App) liveWorkspaceChanges(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if snap == nil || len(snap.WorkspaceChanges) == 0 {
		return
	}
	th := t.Theme()
	count := len(snap.WorkspaceChanges)
	summary := a.menuText(fmt.Sprintf("%d files changed", count), fmt.Sprintf("%d 个文件变更", count))
	if count == 1 {
		summary = a.menuText("1 file changed", "1 个文件变更")
	}
	card := ui.Column(c).Key("workspace-changes").FillWidth().Gap(th.Space(1)).
		Padding(th.Space(2)).Radius(design.RadiusSM).
		Border(1, t.BorderL2).Background(t.BgRaised)
	card.Children(func() {
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.menuText("Workspace changes", "工作区变更")).
				FontSize(11).FixedLineHeight(16).FontWeight(600).
				TextColor(t.LabelSecondary)
			ui.Text(c, summary).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelTertiary).Grow(1)
		})
		st := a.workspaceDiffStateFor(snap.WorkspaceChangesTurn)
		for _, change := range snap.WorkspaceChanges {
			change := change
			a.workspaceChangeRow(c, t, st, change)
		}
	})
}

// workspaceChangeExpandable reports whether a change has anything to show when
// expanded: diff text or a marker explaining why there is none.
func workspaceChangeExpandable(change state.WorkspaceChange) bool {
	return change.Diff != "" || change.Binary || change.Oversized || change.Coarse || change.Truncated
}

// workspaceChangeRow draws one changed file: a disclosure caret (when the file
// has a comparison), the row that opens the file, and the expanded diff panel.
func (a *App) workspaceChangeRow(c *ui.Context, t *design.Tokens, st *workspaceDiffState, change state.WorkspaceChange) {
	th := t.Theme()
	expandable := workspaceChangeExpandable(change)
	open := expandable && st.open[change.Path]
	letter, statusLabel, statusColor := a.workspaceChangeStatusColor(t, change.Status)
	rowLabel := change.Path + " · " + statusLabel

	ui.Column(c).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Height(th.Space(7)).AlignItems(ui.Center).Children(func() {
			if expandable {
				caret := "›"
				if open {
					caret = "⌄"
				}
				toggle := ui.ButtonBase(c).Height(th.Space(7)).PaddingX(th.Space(0.5)).
					Radius(design.RadiusSM).Role(ui.RoleDisclosure).Expanded(open).
					Label(a.workspaceDiffToggleLabel(change.Path)).
					Tooltip(a.menuText("Show or hide the comparison", "显示或隐藏对比"))
				if toggle.Hovered() {
					toggle = toggle.Background(t.BgHover)
				}
				toggle.Children(func() {
					ui.Text(c, caret).FontSize(11).FixedLineHeight(16).
						TextColor(t.LabelTertiary).NoWrap()
				})
				toggle.OnClick(func() { st.open[change.Path] = !open })
			}

			row := ui.ButtonBase(c).Grow(1).Height(th.Space(7)).PaddingX(th.Space(1)).
				Gap(th.Space(2)).Radius(design.RadiusSM).AlignItems(ui.Center).
				Label(rowLabel).Tooltip(a.menuText("Open in Files", "在文件中打开"))
			if row.Hovered() {
				row = row.Background(t.BgHover)
			}
			row.Children(func() {
				ui.Text(c, letter).FontSize(11).FixedLineHeight(16).FontWeight(600).
					TextColor(statusColor).NoWrap()
				ui.Text(c, filepath.ToSlash(change.Path)).FontSize(12).FixedLineHeight(18).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				if change.Added > 0 {
					ui.Text(c, fmt.Sprintf("+%d", change.Added)).FontSize(11).
						FixedLineHeight(16).FontWeight(600).TextColor(t.Success).NoWrap()
				}
				if change.Removed > 0 {
					ui.Text(c, fmt.Sprintf("-%d", change.Removed)).FontSize(11).
						FixedLineHeight(16).FontWeight(600).TextColor(t.Danger).NoWrap()
				}
				ui.Text(c, statusLabel).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).NoWrap()
			})
			row.OnClick(func() { a.openWorkspaceChangeFile(change.Path) })
		})
		if open {
			a.workspaceDiffPanel(c, t, change)
		}
	})
}

// workspaceDiffToggleLabel names the disclosure for assistive technology and
// the tests. It is stable while open and closed: a disclosure names what it
// shows, and its Expanded state reports whether it is open.
func (a *App) workspaceDiffToggleLabel(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	return a.menuText("Diff for "+path, path+" 的对比")
}

// workspaceDiffPanel draws the expanded comparison below a file row.
func (a *App) workspaceDiffPanel(c *ui.Context, t *design.Tokens, change state.WorkspaceChange) {
	th := t.Theme()
	panel := ui.Column(c).FillWidth().Gap(th.Space(0.5)).
		Margin(0, 0, th.Space(1), 0).
		Padding(th.Space(1.5), th.Space(2)).Radius(design.RadiusSM).
		Background(t.BgHover)
	panel.Children(func() {
		hint := func(en, zh string) {
			ui.Text(c, a.menuText(en, zh)).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelSecondary).NoWrap()
		}
		if change.Binary {
			hint("Binary file; no text comparison", "二进制文件，无法文本对比")
		}
		if change.Oversized {
			hint("File too large for a comparison", "文件过大，无法对比")
		}
		if change.Coarse {
			hint("Large change; whole-file comparison", "变更过大，显示整文件对比")
		}
		if change.Truncated {
			hint("Diff truncated", "对比已截断")
		}
		if change.Diff == "" {
			return
		}
		lines := strings.Split(change.Diff, "\n")
		for i, line := range lines {
			if i >= maxWorkspaceDiffViewLines {
				remaining := len(lines) - i
				ui.Text(c, a.menuText(
					fmt.Sprintf("… %d more lines", remaining),
					fmt.Sprintf("… 其余 %d 行", remaining),
				)).FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary).NoWrap()
				break
			}
			ui.Text(c, line).Font(design.MonoFontStack()).FontSize(11).
				FixedLineHeight(16).TextColor(a.workspaceDiffLineColor(t, line)).NoWrap()
		}
	})
}

// workspaceDiffLineColor maps one unified-diff line to its color: added lines
// use the success token, removed the danger token, hunk headers the tertiary
// label and context the secondary label.
func (a *App) workspaceDiffLineColor(t *design.Tokens, line string) ui.Color {
	switch {
	case strings.HasPrefix(line, "@@"):
		return t.LabelTertiary
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		return t.LabelTertiary
	case strings.HasPrefix(line, "+"):
		return t.Success
	case strings.HasPrefix(line, "-"):
		return t.Danger
	default:
		return t.LabelSecondary
	}
}

// workspaceChangeStatusColor maps the state to a token color for the letter.
func (a *App) workspaceChangeStatusColor(t *design.Tokens, status string) (string, string, ui.Color) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "added", "add", "a":
		return "A", a.menuText("Added", "新增"), t.Success
	case "deleted", "delete", "removed", "d":
		return "D", a.menuText("Deleted", "删除"), t.Danger
	default:
		return "M", a.menuText("Modified", "修改"), t.Accent
	}
}

// openWorkspaceChangeFile opens one changed path in the Files tab, through the
// same preview the file tree uses. The path is workspace-relative; the preview
// resolves it under the workspace root and refuses to escape it.
func (a *App) openWorkspaceChangeFile(path string) {
	rel := filepath.ToSlash(strings.TrimSpace(path))
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" {
		return
	}
	a.ensureFilesSession()
	a.rightOpen = true
	a.rightTab = filesTab
	a.openFilePreview(rel)
}
