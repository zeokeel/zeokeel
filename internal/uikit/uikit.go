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
	"github.com/zeokeel/zeokeel/internal/icons"
)

// PageHeader draws a page title with an optional muted subtitle. This is the
// first block of a screen or settings page body; the host's screen header slot
// stays the place for actions. The title is a level-1 heading for assistive
// technology, so a screen reader can jump between pages by heading.
func PageHeader(c *ui.Context, title, subtitle string) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(600).
			TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(1)
		if subtitle != "" {
			design.Apply(ui.Text(c, subtitle), t.TextMuted(design.TypeXS)).MaxLines(2)
		}
	})
}

// Section draws one titled body block: a title above a hairline divider, then
// the body. It is the default grouping for settings and detail pages; cards
// are for independent objects, not every block. The title is a level-2 heading
// for assistive technology.
func Section(c *ui.Context, title string, body func(body *ui.Context)) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeM)).FontWeight(600).
			TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(2)
		ui.Divider(c)
		if body != nil {
			body(c)
		}
	})
}

// PageHeaderAction draws the page header with the page's immediate-effect
// actions at its right edge (new/refresh/check updates). A page whose primary
// action is an explicit save keeps that button in the host footer instead; this
// helper is the documented home of the page-level utility actions.
func PageHeaderAction(c *ui.Context, title, subtitle string, actions func(*ui.Context)) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space3).Children(func() {
		ui.Column(c).FillWidth().Grow(1).Children(func() {
			PageHeader(c, title, subtitle)
		})
		if actions != nil {
			actions(c)
		}
	})
}

// SectionAction draws a section heading with one utility action at its right
// edge: the action belongs to the section it acts on (restore defaults, export
// a report), so it does not sit at the bottom of a form as if it were the
// page's submit. The page's single save action stays in the host footer.
func SectionAction(c *ui.Context, title string, action func(*ui.Context), body func(*ui.Context)) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, title), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary).Role(ui.RoleHeading).Level(2).Grow(1)
			if action != nil {
				action(c)
			}
		})
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
// the About and Diagnostics pages. Values are selectable, as mygo's text guide
// recommends for identifiers and paths the user may copy elsewhere.
func KeyValueRows(c *ui.Context, rows []KeyValue) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		for _, row := range rows {
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Center).Children(func() {
				design.Apply(ui.Text(c, row.Key), t.TextMuted(design.TypeXS))
				ui.Spacer(c).Grow(1)
				design.Apply(ui.Text(c, row.Value), t.Font(design.TypeXS)).
					TextColor(t.LabelPrimary).Selectable()
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

// ErrorLine draws one inline validation/runtime error: a danger triangle, the
// message, in flow and close to what failed. It is a role-alert live region, so
// a screen reader announces it when it appears, and the message is selectable:
// users copy errors into a search or a bug report (mygo text.md: Selectable is
// for an error message or an identifier to paste elsewhere).
func ErrorLine(c *ui.Context, text string) {
	t := design.Of(c)
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).
		Role(ui.RoleAlert).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(design.Space3, design.Space3).
			TextColor(t.DangerText)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).
			TextColor(t.DangerText).Grow(1).Selectable()
	})
}

// DialogAction is one action of a DialogFooter.
type DialogAction struct {
	Label    string
	Primary  bool
	Danger   bool
	Disabled bool
	// Default focuses the button when the dialog opens, so Enter presses it.
	// Set it on the primary action of a dialog that has no text input. A
	// dialog whose input should keep the keyboard focus submits on the
	// input's Submitted() instead (see EnterTo).
	Default bool
	OnClick func()
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
			if action.Default && !action.Disabled {
				button = button.AutoFocus()
			}
			if action.OnClick != nil && button.Clicked() {
				action.OnClick()
			}
		}
	})
}

// ToolIcon maps a tool name to the brand glyph the transcript tool row, the
// trajectory card and the registered tool-card renderers all draw: one mapping,
// so the same tool call never shows two different icons. The semantic glyphs
// follow the official icon-set rule (mygo icon.md): file and image reads use
// the file and image glyphs, web reads the globe, searches the magnifier.
func ToolIcon(name string) *ui.SVG {
	switch name {
	case "bash", "job_start", "job_output", "terminal_send", "terminal_read":
		return icons.Terminal()
	case "read_image":
		return icons.Image()
	case "read_file":
		return icons.File()
	case "web_fetch", "web_search":
		return icons.Globe()
	case "grep", "glob":
		return icons.Search()
	case "todo", "team_task_create", "team_task_update":
		return icons.Data()
	case "list_dir", "write_file", "edit_file", "str_replace_editor",
		"enter_worktree", "list_worktrees":
		return icons.Folder()
	case "subagent", "workflow", "ralph", "skill", "plan_review":
		return icons.Sparkles()
	default:
		return icons.Message()
	}
}

// EnterTo adapts a single-line text input's Enter to an action, for the
// dialogs and inline add rows whose input should keep the keyboard focus. It
// replaces the copies the modules used to carry. Check the key itself rather
// than Submitted() when the input is a text area (which has no Submitted).
func EnterTo(action func()) func(ui.TextKey) bool {
	return func(k ui.TextKey) bool {
		if k.Key != ui.KeyEnter || k.Mods != 0 {
			return false
		}
		if action != nil {
			action()
		}
		return true
	}
}
