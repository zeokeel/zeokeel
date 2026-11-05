package shell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// composerTextKey implements DSH's composer keys for both live screens:
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

// sendIdleFill is the disabled Send control's fill in the current theme. It is
// the accent at 40% over the composer surface: #B2C7F4's role in the light
// baseline and #4B5E82 in dark, where the old hard-coded #B2C7F4 and BgStrong
// were both wrong. The static Home, live Home, static Session and live Session
// all call this one helper.
func sendIdleFill(t *design.Tokens) ui.Color {
	if !t.Dark {
		// The exact light baseline value; the accent blend is visually the
		// same but one channel off after 8-bit rounding.
		return homeSendIdle
	}
	return t.Accent.Alpha(0.4).Over(t.BgRaised)
}

// sendIdleGlyph is the disabled Send arrow: white on the pale light fill,
// light grey on the dark blue-grey fill, where OnPrimary (#151517) is
// unreadable.
func sendIdleGlyph(t *design.Tokens) ui.Color {
	if t.Dark {
		return t.LabelTertiary
	}
	return t.OnPrimary
}

// switchTrack returns the track and knob colors of an on/off switch. On is
// the inverse pair DSH paints (light: dark track with a white knob; dark: a
// near-white track with a dark knob); off is the static pluginSwitch's
// BorderL3 track with a white knob in both themes. Keeping the white knob in
// dark matches the baseline (#3B3B3C track, white knob, 11.2:1); returning
// BgBase for the dark off knob left a 1.22:1 near-invisible control. Plugins,
// Automation and Settings all share it so an enabled switch can never look
// like a disabled one.
func switchTrack(t *design.Tokens, on bool) (track, knob ui.Color) {
	if on {
		return t.LabelPrimary, t.BgBase
	}
	return t.BorderL3, t.White
}
