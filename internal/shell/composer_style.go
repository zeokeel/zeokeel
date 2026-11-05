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

// switchTrack returns the track and knob colors of an on/off switch. On uses
// the inverse pair (light: dark track with a light knob; dark: a near-white
// track with a dark knob); off uses the BorderL3 track with a white knob in
// both themes, which stays visible in the dark appearance. Plugins,
// Automation and Settings all share it so an enabled switch can never look
// like a disabled one.
func switchTrack(t *design.Tokens, on bool) (track, knob ui.Color) {
	if on {
		return t.LabelPrimary, t.BgBase
	}
	return t.BorderL3, t.White
}
