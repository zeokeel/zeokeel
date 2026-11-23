package sidebar

// The permanent workspace rail (Discord-style, approved B): one 40 DIP item per
// registered folder, the current one tinted, and the add button below the list.
// It is the quick switcher; the sidebar header still names the open folder and
// opens the manage dialog.

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func buildWorkspaceRail(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, state *paneState) {
	workspace, ok := state.ctx.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		return
	}
	workspaces, err := workspace.List(context.Background())
	if err != nil {
		return
	}
	current, hasCurrent := contracts.Workspace{}, false
	if opened, found, currentErr := workspace.Current(context.Background()); currentErr == nil && found {
		current, hasCurrent = opened, true
	}
	ui.Column(c).FillWidth().FillHeight().Padding(design.Space2, design.Space1).
		Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		for _, item := range workspaces {
			buildWorkspaceRailItem(c, t, workspace, runtime, state, item, hasCurrent && item.ID == current.ID)
		}
		ui.Spacer(c).Grow(1)
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:   "sidebar.workspace.manage",
			Label: state.T("管理工作文件夹", "Manage workspace folders"),
			Icon:  icons.Sliders(),
			Size:  design.IconSizeS,
			OnClick: func() {
				state.switchOpen = true
				state.switchFocus = true
				runtime.Invalidate()
			},
		})
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key:   "sidebar.workspace.add",
			Label: state.T("添加工作文件夹", "Add workspace folder"),
			Icon:  icons.Plus(),
			Size:  design.IconSizeS,
			OnClick: func() {
				if _, ok := addWorkspace(state, workspace, runtime); ok {
					runtime.Invalidate()
				}
			},
		})
	})
}

func buildWorkspaceRailItem(c *ui.Context, t *design.Tokens, workspace contracts.WorkspaceService,
	runtime contracts.UIRuntime, state *paneState, item contracts.Workspace, active bool) {
	label := strings.TrimSpace(item.Name)
	if label == "" {
		label = item.Root
	}
	letter, _ := utf8.DecodeRuneInString(strings.ToUpper(label))
	if letter == utf8.RuneError || letter == 0 {
		letter = '?'
	}
	ui.Row(c).FillWidth().Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(design.Space1).Height(design.Space5).
			Background(workspaceColor(t, item.Color))
		row := design.Apply(ui.Button(c, string(letter)), t.SecondaryButton()).
			Width(design.WorkspaceRailItem).Height(design.WorkspaceRailItem).
			Label(state.T("切换工作文件夹：", "Switch workspace: ") + label)
		if active {
			row = row.Background(t.BgSelected)
		}
		if row.Clicked() {
			if _, err := workspace.Open(context.Background(), item.ID); err == nil {
				state.switchOpen = false
				runtime.Invalidate()
			}
		}
	})
}

// workspaceColor maps the stored palette id to a design token. Unknown or
// empty ids use the neutral secondary label color.
func workspaceColor(t *design.Tokens, id string) ui.Color {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "blue":
		return t.Accent
	case "green":
		return t.Success
	case "amber":
		return t.Warning
	case "red":
		return t.Danger
	default:
		return t.LabelTertiary
	}
}
