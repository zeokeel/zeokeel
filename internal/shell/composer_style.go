package shell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// composerTextKey implements the composer keys for both live screens:
// Enter sends, Shift+Enter breaks the line. Taking the key here keeps the
// multiline editor from inserting a newline on a plain Enter, and OnSubmit is
// never involved, so Shift+Enter cannot send.
func (a *App) composerTextKey(c *ui.Context, home bool) func(k ui.TextKey) bool {
	return func(k ui.TextKey) bool {
		// Commands triggered from the keyboard run outside a build pass; hand
		// them this frame's context so helpers that need one (the file picker,
		// the Home staging path) are not context-free.
		a.startUI().lastComposerContext = c
		// While the "/" palette is open the composer owns its keys too:
		// Up/Down move the highlight, Tab or Enter run the highlighted command,
		// and Escape closes the palette without touching the draft. A complete
		// command line with arguments (the palette is already hidden once a
		// space ends its token) still executes as typed, so "/permission full"
		// keeps its argument.
		if k.Mods == 0 && a.slashMenuVisible() {
			switch k.Key {
			case ui.KeyDown:
				a.moveSlash(1)
				return true
			case ui.KeyUp:
				a.moveSlash(-1)
				return true
			case ui.KeyTab:
				if a.chooseSlash(k) {
					return true
				}
				return false
			case ui.KeyEnter:
				if a.chooseSlash(k) {
					return true
				}
				// No match for the query: fall through to the typed-line
				// dispatch, which sends an unknown command to the model.
			case ui.KeyEscape:
				if a.dismissComposerPopup() {
					return true
				}
			}
		}
		// While the "@" menu is open the composer owns its keys: Up/Down move
		// the highlighted row, Enter inserts it, and Tab moves the focus into
		// the focusable list rows. Without this the menu was mouse-only.
		if k.Mods == 0 && !a.slashMenuVisible() {
			if entries := a.visibleMentionEntries(); len(entries) > 0 {
				switch k.Key {
				case ui.KeyDown:
					a.moveMention(entries, 1)
					return true
				case ui.KeyUp:
					a.moveMention(entries, -1)
					return true
				case ui.KeyTab:
					// Tab drills the highlighted folder in place; only plain
					// files and sessions fall back to focusing the list.
					if a.drillMentionAt(entries) {
						k.SetText(a.draft)
						return true
					}
					a.mentionRef.Focus(c)
					return true
				case ui.KeyEnter:
					if a.chooseMention(entries) {
						k.SetText(a.draft)
						return true
					}
				}
			}
		}
		if k.Key != ui.KeyEnter || k.Mods != 0 {
			return false
		}
		text := k.Text()
		if strings.TrimSpace(text) == "" {
			return true
		}
		a.draft = text
		if line := strings.TrimSpace(text); isSlashCommand(line) {
			a.runSlashCommand(line)
		} else if home {
			a.sendHomeDraft(c)
		} else {
			a.sendSessionDraft(c)
		}
		if strings.TrimSpace(a.draft) == "" {
			k.SetText("")
		}
		return true
	}
}

// sendIdleFill is a composer Send control's fill while it is disabled: the
// brand accent at a low opacity over the composer surface, so a disabled Send
// still belongs to the brand control family instead of the retired pale-blue
// sample. Home and Session share it, so both composers disable the same way.
func sendIdleFill(t *design.Tokens) ui.Color {
	return t.Accent.Alpha(0.16).Over(t.BgRaised)
}

// sendIdleGlyph is the disabled Send glyph: the brand accent, muted by the
// disabled control's own 50% paint opacity against the idle fill.
func sendIdleGlyph(t *design.Tokens) ui.Color { return t.Accent }
