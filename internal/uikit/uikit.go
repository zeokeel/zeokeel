// Package uikit is ZeoKeel's shared UI library: small compositions of mygo's
// own native controls (ui.Text, ui.Row, ui.Column, ui.Divider, ui.Spacer,
// ui.Field, ui.Button, ui.PrimaryButton). It never hand-draws a control and it
// builds with the design tokens, so every module gets the same page/section/
// dialog look without copying layout code.
//
// Build helpers take the frame's *ui.Context and are only valid inside a build
// callback (see docs/ARCHITECTURE.md §4/§9): they must build, not act.
package uikit

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// PageHeader draws a page title with an optional muted subtitle. This is the
// first block of a screen or settings page body; the host's screen header slot
// stays the place for actions.
func PageHeader(c *ui.Context, title, subtitle string) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
		if subtitle != "" {
			design.Apply(ui.Text(c, subtitle), t.TextMuted(design.TypeXS)).MaxLines(2)
		}
	})
}

// Section draws one titled body block: a title above a hairline divider, then
// the body. It is the default grouping for settings and detail pages; cards
// are for independent objects, not every block.
func Section(c *ui.Context, title string, body func(body *ui.Context)) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeM)).FontWeight(600).TextColor(t.LabelPrimary)
		ui.Divider(c)
		if body != nil {
			body(c)
		}
	})
}

// Bullet draws one bullet list row: a muted bullet glyph and the text.
func Bullet(c *ui.Context, text string) {
	t := design.Of(c)
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		design.Apply(ui.Text(c, "•"), t.Font(design.TypeXS)).TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.LabelSecondary).Grow(1)
	})
}

// KeyValue is one row of KeyValueRows.
type KeyValue struct {
	Key   string
	Value string
}

// KeyValueRows draws label/value rows with the label muted on the left and the
// value right-aligned. It is the read-only "property sheet" pattern used by
// the About and Diagnostics pages.
func KeyValueRows(c *ui.Context, rows []KeyValue) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		for _, row := range rows {
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Center).Children(func() {
				design.Apply(ui.Text(c, row.Key), t.TextMuted(design.TypeXS))
				ui.Spacer(c).Grow(1)
				design.Apply(ui.Text(c, row.Value), t.Font(design.TypeXS)).TextColor(t.LabelPrimary)
			})
		}
	})
}

// EmptyState is the muted placeholder a module shows when an injection point
// has no contribution yet (home regions, empty settings surfaces).
func EmptyState(c *ui.Context, text string) {
	t := design.Of(c)
	design.Apply(ui.Text(c, text), t.TextMuted(design.TypeXS)).MaxLines(3)
}

// EmptyStateBlock is an explicit empty state with a next action: a muted title,
// an optional hint line and the caller's action row (a button, usually).
func EmptyStateBlock(c *ui.Context, title, hint string, action func()) {
	t := design.Of(c)
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space6).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeBody)).FontWeight(600).
			TextColor(t.LabelSecondary)
		if hint != "" {
			design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXS)).MaxLines(2)
		}
		if action != nil {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(action)
		}
	})
}

// ErrorLine draws one inline validation/runtime error: danger text with a small
// "Error" marker, in flow, close to what failed. It replaces the six per-module
// error rows.
func ErrorLine(c *ui.Context, text string) {
	t := design.Of(c)
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, "!"), t.Font(design.TypeXS)).FontWeight(600).
			TextColor(t.DangerText)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText).Grow(1)
	})
}

// DialogAction is one action of a DialogFooter.
type DialogAction struct {
	Label    string
	Primary  bool
	Danger   bool
	Disabled bool
	OnClick  func()
}

// DialogFooter is the one dialog action bar of the product: actions right
// aligned, in the desktop order — danger and secondary actions first, the
// primary action last (rightmost, where Enter and the eye expect it). It
// replaces the hand-written footers that put the primary action on the left.
func DialogFooter(c *ui.Context, actions []DialogAction) {
	t := design.Of(c)
	ordered := make([]DialogAction, 0, len(actions))
	for _, action := range actions {
		if action.Danger {
			ordered = append(ordered, action)
		}
	}
	for _, action := range actions {
		if !action.Danger && !action.Primary {
			ordered = append(ordered, action)
		}
	}
	for _, action := range actions {
		if action.Primary {
			ordered = append(ordered, action)
		}
	}
	ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).
		Gap(design.Space2).Children(func() {
		for _, action := range ordered {
			action := action
			var button ui.Element
			switch {
			case action.Primary:
				button = design.Apply(ui.PrimaryButton(c, action.Label), t.PrimaryButton())
			case action.Danger:
				button = design.Apply(ui.Button(c, action.Label), t.DangerButton())
			default:
				button = design.Apply(ui.Button(c, action.Label), t.SecondaryButton())
			}
			if action.Disabled {
				button.Disabled(true)
			}
			if action.OnClick != nil && button.Clicked() {
				action.OnClick()
			}
		}
	})
}
