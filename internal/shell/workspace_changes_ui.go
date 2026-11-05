package shell

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/state"
)

// FP-05: the transcript's workspace-changes block. The runtime attaches each
// completed turn's added/modified/deleted files to the snapshot (the
// workspace/changes session event); the shell renders them as a compact card
// under the turn, with the A/M/D state and a click action that opens the file
// in the Files tab. An empty summary renders nothing, and a reopened session
// replays the same event into the same block.

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
		for _, change := range snap.WorkspaceChanges {
			change := change
			letter, statusLabel, statusColor := a.workspaceChangeStatusColor(t, change.Status)
			rowLabel := change.Path + " · " + statusLabel
			row := ui.ButtonBase(c).FillWidth().Height(th.Space(7)).PaddingX(th.Space(1)).
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
				ui.Text(c, statusLabel).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).NoWrap()
			})
			row.OnClick(func() { a.openWorkspaceChangeFile(change.Path) })
		}
	})
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
