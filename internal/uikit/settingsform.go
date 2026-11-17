package uikit

// The shared settings-form compositions (2026-11 UI review, phase 2): the static
// risk disclosure, the fixed page footer and the boolean toggle row. They live in
// uikit so every settings/detail page draws one anatomy instead of each module
// keeping a local copy; the modules switch to these helpers (the local builds in
// internal/ui/settings are the reference anatomy these export).

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// WarningLine draws one static risk/trust disclosure in the warning semantic: the
// alert triangle and the message use the warning tokens, keeping red (danger ink)
// for real errors and destructive actions (DESIGN.md §5.2 — a disclosure is a
// caution, not a failure). It mirrors ErrorLine's anatomy (icon + selectable
// message, role-alert live region) but never uses danger ink. Use it for static
// disclosures (sandbox/permission trust notes); use ErrorLine for runtime errors.
func WarningLine(c *ui.Context, t *design.Tokens, text string) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).
		Role(ui.RoleAlert).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(design.Space3, design.Space3).
			TextColor(t.WarningText)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).
			TextColor(t.WarningText).Grow(1).Selectable()
	})
}

// PageFooterOptions is one settings page's fixed footer: a single primary action
// (the page's explicit save) plus the secondary actions beside it.
type PageFooterOptions struct {
	// Key scopes the footer's identity across frames (empty leaves it unkeyed).
	Key string
	// Primary is the page's one primary action, drawn rightmost. A disabled
	// primary takes the composer's neutral disabled look (t.DisabledPrimary +
	// the native Disabled state), not mygo's 50% accent wash.
	Primary DialogAction
	// Secondary are the non-primary actions (danger ones lead, as in DialogFooter).
	Secondary []DialogAction
}

// PageFooter draws the settings page's fixed action bar: a top divider then a
// right-aligned action row in the desktop order — danger and secondary actions
// first, the single primary last (rightmost, where Enter and the eye expect it),
// the same order as DialogFooter. A disabled primary uses t.DisabledPrimary()+
// Disabled(true) so it matches the composer's disabled send color (2026-11 UI
// review P2/phase 2). The page's single primary lives here, not mid-page.
func PageFooter(c *ui.Context, opts PageFooterOptions) {
	t := design.Of(c)
	ordered := make([]DialogAction, 0, len(opts.Secondary)+1)
	for _, action := range opts.Secondary {
		if action.Danger {
			ordered = append(ordered, action)
		}
	}
	for _, action := range opts.Secondary {
		if !action.Danger {
			ordered = append(ordered, action)
		}
	}
	primary := opts.Primary
	primary.Primary = true
	ordered = append(ordered, primary)
	if opts.Key != "" {
		c.Key(opts.Key)
	}
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Divider(c)
		ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).
			Gap(design.Space2).Children(func() {
			for _, action := range ordered {
				action := action
				var button ui.Element
				switch {
				case action.Primary && action.Disabled:
					button = design.Apply(ui.Button(c, action.Label), t.DisabledPrimary())
				case action.Primary:
					button = design.Apply(ui.PrimaryButton(c, action.Label), t.PrimaryButton())
				case action.Danger:
					button = design.Apply(ui.Button(c, action.Label), t.DangerButton())
				default:
					button = design.Apply(ui.Button(c, action.Label), t.SecondaryButton())
				}
				// No .Label: these are text buttons, so the visible label already is the
				// accessible name (as in DialogFooter). An extra .Label adds a second
				// text node and doubles the label in frame tests (2026-11 UI review).
				if action.Disabled {
					// Keep mygo's native disabled state. DisabledPrimary paints BgStrong
					// and the native disabled wash renders it at 50% over the panel —
					// pixel-identical to the composer's disabled send (same preset, same
					// mechanism). The wash is intentional, not an override to BgBase, and
					// native disabled also blocks the click, so the action is truly
					// non-executable (2026-11 UI review; measured fill 248,249,250 on a
					// white panel for both footer and composer).
					button = button.Disabled(true)
				}
				if action.Default && !action.Disabled {
					button = button.AutoFocus()
				}
				if action.OnClick != nil && button.Clicked() {
					action.OnClick()
				}
			}
		})
	})
}

// SwitchRowOptions is one boolean setting row.
type SwitchRowOptions struct {
	// Label is the visible caption at the section's left edge; it names the
	// switch for assistive technology and toggles it on click (LabelledBy).
	Label string
	// Description is the muted help line below, aligned with the section's left
	// edge (not indented under a control column). Empty hides it.
	Description string
	// Value is the caller-owned bound boolean.
	Value *bool
	// Disabled takes the switch out of reach (the native disabled state).
	Disabled bool
}

// SwitchRow renders one boolean setting as a full-width row: the visible label at
// the section's left edge, the native switch at the row's trailing edge, and the
// description below aligned with the section edge. Every toggle row shares this
// anatomy so the switch sits at the same x on every page (the macOS-style ui.Form
// right-aligned label column made multi-switch sections ragged and orphaned the
// description under the control). LabelledBy ties the caption to the switch for
// assistive technology and gives a caption click to the switch, as a Field label
// does (mygo form.md "Labels of your own"). It reports whether the value changed
// this frame; the caller handles the change where the setting lives.
func SwitchRow(c *ui.Context, opts SwitchRowOptions) bool {
	t := design.Of(c)
	var changed bool
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			caption := design.Apply(ui.Text(c, opts.Label), t.Font(design.TypeXS)).
				TextColor(t.LabelPrimary).Grow(1)
			sw := ui.Switch(c, opts.Value).LabelledBy(caption)
			if opts.Disabled {
				sw.Disabled(true)
			}
			changed = sw.Changed()
		})
		if opts.Description != "" {
			design.Apply(ui.Text(c, opts.Description), t.TextMuted(design.TypeXXS))
		}
	})
	return changed
}
