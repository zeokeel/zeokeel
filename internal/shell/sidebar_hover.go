package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/store"
)

// hoverCloseDelay is how long a hover card stays after the pointer leaves
// the row and the card, so moving into the card across its margin does not
// dismiss it.
const hoverCloseDelay = 120 * time.Millisecond

// maybeWorkspaceHoverCard shows the live workspace hover card beside a row
// while the pointer is over it, and keeps it open while the pointer moves
// into the card (so its Copy path button is clickable). It closes after
// leaving both, when the pointer hovers another workspace, when the copy
// action runs, or when Popover reports an outside press or Escape.
func (a *App) maybeWorkspaceHoverCard(c *ui.Context, t *design.Tokens, row ui.Element, ws store.Workspace) {
	if a.Ctrl == nil {
		return
	}
	// A drag is not a hover: the card would cover the rows being dragged
	// over, and the pointer is busy reordering.
	if row.Dragging() {
		a.wsHoverID = ""
		a.wsHoverCloseAt = time.Time{}
		return
	}
	open := a.wsHoverID == ws.ID
	if row.Hovered() {
		a.wsHoverID = ws.ID
		a.wsHoverCloseAt = time.Time{}
		open = true
	}
	// Close the card in the build the grace period expires, before its content
	// is built. Doing it inside PopoverBase's body would paint the card once
	// more after the state already said it was closed (a frame with no
	// animation request would keep that stale paint).
	if open && !a.wsHoverCloseAt.IsZero() && !c.Now().Before(a.wsHoverCloseAt) {
		open = false
		a.wsHoverID = ""
		a.wsHoverCloseAt = time.Time{}
	}
	if open {
		// The card sits beside the row, not below it: a card below a
		// workspace would cover the next row and swallow its click or
		// context menu.
		ui.PopoverBase(c, row, &open, func(panel ui.Element) {
			panel.AttachTo(row, ui.AnchorEnd, ui.AnchorStart).MarginStart(design.Space2)
			design.Apply(panel, t.Panel()).Padding(design.Space3).
				Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
			a.workspaceHoverCard(c, t, ws)
			switch {
			case row.Hovered() || panel.Hovered():
				a.wsHoverCloseAt = time.Time{}
			case a.wsHoverCloseAt.IsZero():
				a.wsHoverCloseAt = c.Now().Add(hoverCloseDelay)
				c.After(hoverCloseDelay)
			case !c.Now().Before(a.wsHoverCloseAt):
				open = false
				a.wsHoverID = ""
				a.wsHoverCloseAt = time.Time{}
			}
		})
	}
	if !open && a.wsHoverID == ws.ID {
		a.wsHoverID = ""
	}
}

// workspaceHoverCard is DSH's WorkspaceHoverContent: the workspace title,
// its home-abbreviated root and its absolute creation time, plus a copy
// action for the root path.
func (a *App) workspaceHoverCard(c *ui.Context, t *design.Tokens, ws store.Workspace) {
	ui.Column(c).Width(240).Gap(design.Space1).Children(func() {
		ui.Text(c, ws.Name).FontSize(13).FixedLineHeight(18).FontWeight(600).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		ui.Text(c, abbreviateHomePath(ws.Root)).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		if created := formatCreatedAt(ws.CreatedAt); created != "" {
			ui.Text(c, fmt.Sprintf(a.tr("Created %s", "创建于 %s"), created)).
				FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary)
		}
		copyRow := ui.Row(c).Height(26).PaddingX(design.Space2).Radius(design.RadiusSM).
			AlignItems(ui.Center).Gap(design.Space2).
			Label(a.tr("Copy path", "复制路径")).Role(ui.RoleButton).Focusable()
		if copyRow.Hovered() {
			copyRow = copyRow.Background(t.BgHover)
		}
		copyRow.Children(func() {
			ui.Text(c, a.tr("Copy path", "复制路径")).FontSize(12).
				FixedLineHeight(18).TextColor(t.LabelPrimary)
		})
		copyRow.OnClick(func() {
			c.WriteClipboard(ws.Root)
			a.chromeToast(c, "Copied workspace path", "已复制工作区路径")
			a.wsHoverID = ""
		})
	})
}

// abbreviateHomePath replaces the user's home prefix with "~", as DSH does
// in the workspace hover card. Paths outside the home are left alone.
func abbreviateHomePath(path string) string {
	cleaned := filepath.Clean(path)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	home = filepath.Clean(home)
	if cleaned == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(cleaned, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(cleaned, prefix)
	}
	return path
}
