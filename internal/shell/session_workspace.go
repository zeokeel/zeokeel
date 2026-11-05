package shell

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// sessionWorkspace resolves the workspace of the session the screen shows, for
// the header's workspace chip. The result is cached until the current session
// changes: the header draws every frame, and a SQLite query per frame would be
// wasteful. ok is false when there is no current session or no workspace.
func (a *App) sessionWorkspace() (name, root string, ok bool) {
	if a.Ctrl == nil {
		return "", "", false
	}
	sessionID := strings.TrimSpace(a.Ctrl.CurrentSessionID())
	if sessionID == "" {
		return "", "", false
	}
	st := a.uiState()
	if st.workspaceLoaded && st.workspaceSessionID == sessionID {
		return st.workspaceName, st.workspaceRoot, true
	}
	sess, err := a.Ctrl.CurrentSession()
	if err != nil {
		return "", "", false
	}
	ws, err := a.Ctrl.Store().GetWorkspace(sess.WorkspaceID)
	if err != nil || ws == nil {
		return "", "", false
	}
	st.workspaceSessionID = sessionID
	st.workspaceName = strings.TrimSpace(ws.Name)
	st.workspaceRoot = strings.TrimSpace(ws.Root)
	st.workspaceLoaded = true
	return st.workspaceName, st.workspaceRoot, true
}

// sessionWorkspaceLabel is the chip's accessible label. It names the workspace
// only: the absolute root stays in the tooltip instead of the spoken label, so
// a long path (or a test-generated temp directory) cannot spill into unrelated
// UI text and screen-reader output.
func (a *App) sessionWorkspaceLabel(name string) string {
	return fmt.Sprintf(a.tr("Workspace: %s", "工作区：%s"), name)
}

// sessionWorkspaceChip draws the session's workspace next to the header title:
// a folder icon and the workspace name, with the root as its tooltip and
// accessible label. This is the visible "which workspace is the agent actually
// in" surface the session screen was missing; the only previous hint was the
// right sidebar's Files breadcrumb. The chip shrinks and ellipsizes before the
// header's controls, and is omitted on very narrow main columns.
func (a *App) sessionWorkspaceChip(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	name, root, ok := a.sessionWorkspace()
	if !ok {
		return
	}
	label := strings.TrimSpace(name)
	if label == "" && root != "" {
		label = filepath.Base(root)
	}
	if label == "" {
		return
	}
	// Very narrow windows: the header's title, mode and actions win over the
	// indicator, which the right sidebar's Files breadcrumb still shows.
	if a.contentWidth(c) < th.Space(90) {
		return
	}
	a11y := a.sessionWorkspaceLabel(label)
	tooltip := root
	if tooltip == "" {
		tooltip = label
	}
	chip := ui.Row(c).Height(th.Space(7)).PaddingX(th.Space(2)).
		Gap(th.Space(1)).Radius(design.RadiusSM).AlignItems(ui.Center).
		Shrink(1).Label(a11y).Tooltip(tooltip)
	if chip.Hovered() {
		chip = chip.Background(t.BgHover)
	}
	chip.Children(func() {
		ui.Icon(c, icons.Folder()).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.LabelTertiary)
		text := design.Apply(ui.Text(c, label), t.Font(design.TypeXS))
		text.TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…").Shrink(1)
	})
	ui.Box(c).Width(th.Space(1))
}
