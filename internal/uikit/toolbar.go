package uikit

// The composer's quiet action row (P2-12 in docs/UI-UX-UPGRADE-2026-11-15.md):
// the conversation screen and Home put the attach/model/session (or
// workspace/model/mode) switchers in one official ui.Toolbar instead of a row of
// bordered buttons. In a toolbar mygo gives its buttons no face of their own
// until hovered, pressed or focused — the native state machine owns that look,
// so nothing here draws or fakes a button state — and the controls that do not
// fit fold into the toolbar's trailing overflow menu. The send/queue/stop
// control stays in ComposerShell's action row, outside this toolbar, so no
// overflow menu can ever take the primary action.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// ComposerToolbarOptions is one composer's quiet toolbar.
type ComposerToolbarOptions struct {
	// Key is the toolbar's element key; empty uses a shared default.
	Key string
	// Label is the toolbar's accessible name (the three switchers it holds).
	Label string
	// Locale localizes the default label; nil falls back to Chinese.
	Locale contracts.LocaleService
	// Items builds the toolbar's controls (buttons, popover menu buttons and
	// toggles). Build them straight in the callback: a ui.Row around them would
	// hide the controls from the toolbar's keyboard and overflow handling.
	Items func(c *ui.Context)
}

// ComposerToolbar draws the composer's quiet action row as one native
// ui.Toolbar: one Tab stop, Left/Right between its controls, native
// hover/pressed/focus faces, and an overflow menu for the controls that do not
// fit. It returns the toolbar element so a caller can anchor to it or read its
// bounds.
func ComposerToolbar(c *ui.Context, o ComposerToolbarOptions) ui.Element {
	key := o.Key
	if key == "" {
		key = "uikit.composer.toolbar"
	}
	label := o.Label
	if label == "" {
		label = T(o.Locale, "输入框工具栏", "Composer toolbar")
	}
	// mygo keys an element at construction (Element.Key panics on a widget whose
	// state was initialized), so the key goes through the Context the toolbar is
	// built with.
	return ui.Toolbar(c.Key(key), func() {
		if o.Items != nil {
			o.Items(c)
		}
	}).Label(label).Height(design.RowCompactHeight)
}

// ComposerAttachOptions is the composer's attach control: a quiet toolbar
// button showing a file glyph and its label.
type ComposerAttachOptions struct {
	// Key is the button's element key; empty leaves the toolbar's own keying.
	Key string
	// Label is the visible and accessible copy ("附件"/"Attach", or
	// "选择中…"/"Choosing…" while Busy). Empty falls back to 附件/Attach.
	Label string
	// Tooltip describes what clicking does; the picker's own busy copy replaces
	// it while Busy is set.
	Tooltip string
	// Locale localizes the default copy; nil falls back to Chinese.
	Locale contracts.LocaleService
	// Busy disables the control while the native file picker runs: mygo's dialog
	// blocks its caller, so the picker runs off the UI thread and a second click
	// must not start a second pick.
	Busy bool
	// OnPick opens the surface's native picker (startAttachmentPick). nil leaves
	// the control inert.
	OnPick func()
}

// ComposerAttachButton is the composer's attach control: clicking it opens the
// native file picker directly (there is no menu between the button and the
// picker). Drag-and-drop and pasted images stay the other attachment paths; a
// picker that is unavailable reports through the composer's error row. The
// button is a plain ui.Button so inside ComposerToolbar it takes mygo's toolbar
// face (none until hovered/pressed/focused) instead of a hand-written state.
func ComposerAttachButton(c *ui.Context, o ComposerAttachOptions) ui.Element {
	t := design.Of(c)
	label := o.Label
	if label == "" {
		label = T(o.Locale, "附件", "Attach")
	}
	tooltip := o.Tooltip
	if o.Busy {
		tooltip = T(o.Locale, "文件选择器打开中…", "The file picker is open…")
	}
	cc := c
	if o.Key != "" {
		cc = c.Key(o.Key)
	}
	// A key is set at construction (Element.Key panics on a control whose state
	// was initialized), so it goes through the Context the button is built with.
	button := ui.Button(cc, "").Label(label)
	if tooltip != "" {
		button = button.Tooltip(tooltip)
	}
	if o.Busy {
		button = button.Disabled(true)
	}
	// 28 DIP, the compact control height: the toolbar row is 32 and its controls
	// sit one step inside it (B-spec §3.2.7: toolbar 32, buttons 28).
	design.Apply(button, t.TextMuted(design.TypeXS)).Height(design.ControlCompactHeight)
	button.Children(func() {
		ui.Icon(c, icons.File()).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS))
	})
	if button.Clicked() && !o.Busy && o.OnPick != nil {
		o.OnPick()
	}
	return button
}
