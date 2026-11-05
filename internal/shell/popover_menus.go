package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The app's popover menus (composer and header chips, the sidebar's account and
// View options menus, the right panel's New tab / shell / breadcrumb menus)
// share one brand surface and one row look.
//
// mygo's ui.Popover supplies placement, dismissal and focus containment only:
// its panel is a bare box with no look (see ui's popover in base.go), so every
// menu body must paint the surface itself. These helpers are that one
// implementation, so a popover cannot drift back into an unstyled list of
// default buttons floating over the screen.
const (
	// popoverMenuWidthMin/Max are the compact menu width range in DIPs. The
	// menu never grows past the composer column it hangs from.
	popoverMenuWidthMin float32 = 260
	popoverMenuWidthMax float32 = 320
)

// popoverMenuWidth clamps a menu's width to the composer column, keeping the
// compact 260–320 range whenever the column allows it. On the minimum window
// the composer column is wider than 320, so the cap is what the menus use.
func (a *App) popoverMenuWidth(c *ui.Context) float32 {
	width := popoverMenuWidthMax
	if limit := a.composerPopupWidthFor(c); limit > 0 {
		switch {
		case limit < popoverMenuWidthMin:
			width = limit
		case limit < width:
			width = limit
		}
	}
	if width < 1 {
		width = popoverMenuWidthMax
	}
	return width
}

// popoverMenuPanel paints the branded menu surface: the same Panel tokens as
// the composer cards (raised fill, 1 DIP border, the brand corner), a compact
// 4 DIP padding, one-DIP row gaps and a soft shadow so the popover reads as a
// surface above the screen.
func (a *App) popoverMenuPanel(c *ui.Context, t *design.Tokens, build func()) {
	a.popoverMenuPanelIn(c, t, 0, build)
}

// popoverMenuPanelIn is popoverMenuPanel clamped to the column the menu opens
// in: the sidebar or the right panel have their own width, and a menu there
// must not spill wider than the column it hangs from. maxWidth <= 0 keeps the
// composer column as the only clamp.
func (a *App) popoverMenuPanelIn(c *ui.Context, t *design.Tokens, maxWidth float32, build func()) {
	width := a.popoverMenuWidth(c)
	if maxWidth > 0 && width > maxWidth {
		width = maxWidth
	}
	if width < 1 {
		width = popoverMenuWidthMax
	}
	design.Apply(ui.Column(c), t.Panel()).
		Width(width).
		Padding(design.Space1).
		Gap(1).
		Shadow(0, 8, 24, 0, homeCardShadow(t)).
		Children(build)
}

// popoverMenuRow is one branded radio row of a menu: an optional leading icon,
// a 13 DIP label with an optional 11 DIP secondary line under it, the brand
// hover tint, and the accent check on the selected row. The row is a real menu
// item radio (Focusable; Enter/Space activate it through OnClick) so the menu
// stays keyboard-reachable.
func (a *App) popoverMenuRow(c *ui.Context, t *design.Tokens, label, desc string, selected bool, leading func(), onClick func()) ui.Element {
	row := ui.Row(c).FillWidth().MinHeight(design.Space8).
		Padding(design.Space1, design.Space2).
		Gap(design.Space2).Radius(design.RadiusSM).AlignItems(ui.Center).
		Label(label).Role(ui.RoleMenuItemRadio).Checked(selected).Focusable()
	if selected || row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		if leading != nil {
			leading()
		}
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			labelEl := ui.Text(c, label).FontSize(13).FixedLineHeight(18).
				TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			if selected {
				labelEl = labelEl.FontWeight(500)
			}
			if desc != "" {
				ui.Text(c, desc).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).MaxLines(4)
			}
		})
		if selected {
			ui.Text(c, "✓").FontSize(12).FixedLineHeight(18).TextColor(t.Accent)
		}
	})
	if onClick != nil {
		row.OnClick(onClick)
	}
	return row
}

// popoverMenuActionRow is a plain action row (no radio state) for menu entries
// that run a command, such as the session header's More actions, the account
// menu or the right panel's New tab picker.
func (a *App) popoverMenuActionRow(c *ui.Context, t *design.Tokens, label string, leading func(), onClick func()) ui.Element {
	return a.popoverMenuRoleRow(c, t, label, ui.RoleMenuItem, leading, onClick)
}

// popoverMenuRoleRow is popoverMenuActionRow with an explicit role, for the few
// menus whose entries keep a non-menu role (the Files breadcrumb's folder
// buttons were plain buttons).
func (a *App) popoverMenuRoleRow(c *ui.Context, t *design.Tokens, label string, role ui.Role, leading func(), onClick func()) ui.Element {
	row := ui.Row(c).FillWidth().MinHeight(design.Space8).
		Padding(design.Space1, design.Space2).
		Gap(design.Space2).Radius(design.RadiusSM).AlignItems(ui.Center).
		Label(label).Role(role).Focusable()
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		if leading != nil {
			leading()
		}
		ui.Text(c, label).FontSize(13).FixedLineHeight(18).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
	})
	if onClick != nil {
		row.OnClick(onClick)
	}
	return row
}

// popoverMenuCheckRow is one branded checkable row (RoleMenuItemCheckBox) for
// menus whose entries are independent choices, as the sidebar's View options
// groups are. The chosen row carries the accent check in the leading slot the
// check box row already used, under the same hover and selected tint.
func (a *App) popoverMenuCheckRow(c *ui.Context, t *design.Tokens, label string, selected bool, onClick func()) ui.Element {
	row := ui.Row(c).FillWidth().MinHeight(design.Space8).
		Padding(design.Space1, design.Space2).Gap(design.Space2).
		Radius(design.RadiusSM).AlignItems(ui.Center).
		Label(label).Role(ui.RoleMenuItemCheckBox).Checked(selected).Focusable()
	if selected || row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		mark := ""
		if selected {
			mark = "✓"
		}
		ui.Text(c, mark).FontSize(12).FixedLineHeight(18).TextColor(t.Accent).
			Width(design.Space3)
		ui.Text(c, label).FontSize(13).FixedLineHeight(18).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
	})
	if onClick != nil {
		row.OnClick(onClick)
	}
	return row
}
