package settings

// The appearance page's global-hotkey section (H6): the accelerator is shown in
// the same friendly form as the shortcut page (Cmd/Ctrl + Shift + Space), the
// user can record a combination by pressing it instead of writing Electron
// accelerator syntax, and quick presets cover the common choices. Typing is
// still accepted: the display form "Cmd/Ctrl + Shift + K" and the stored form
// "CmdOrCtrl+Shift+K" both parse. The host registers the accelerator at the next
// start; the page validates the format and persists the trimmed value.

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// hotkeyPresets are the one-click accelerators offered under the recorder.
var hotkeyPresets = []string{
	"CmdOrCtrl+Shift+Space",
	"CmdOrCtrl+Alt+Space",
	"CmdOrCtrl+Shift+K",
}

// hotkeyDisplayToken maps one canonical accelerator token to its display form.
func hotkeyDisplayToken(token string) string {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "cmdorctrl", "commandorcontrol", "cmd/ctrl":
		return "Cmd/Ctrl"
	case "cmd", "command":
		return "Cmd"
	case "ctrl", "control":
		return "Ctrl"
	case "alt", "option":
		return "Alt"
	case "shift":
		return "Shift"
	case "super", "meta", "win":
		return "Super"
	case "space":
		return "Space"
	case "enter", "return":
		return "Enter"
	case "escape", "esc":
		return "Esc"
	case "tab":
		return "Tab"
	case "backspace":
		return "Backspace"
	case "delete", "del":
		return "Delete"
	case "insert", "ins":
		return "Insert"
	case "up":
		return "Up"
	case "down":
		return "Down"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "home":
		return "Home"
	case "end":
		return "End"
	case "pageup":
		return "PageUp"
	case "pagedown":
		return "PageDown"
	}
	upper := strings.ToUpper(strings.TrimSpace(token))
	if len(upper) == 1 || (strings.HasPrefix(upper, "F") && len(upper) <= 3) {
		return upper
	}
	return strings.TrimSpace(token)
}

// formatHotkeyDisplay renders an accelerator in the shortcut page's style:
// "CmdOrCtrl+Shift+Space" becomes "Cmd/Ctrl + Shift + Space".
func formatHotkeyDisplay(accelerator string) string {
	accelerator = strings.TrimSpace(accelerator)
	if accelerator == "" {
		return ""
	}
	tokens := strings.Split(accelerator, "+")
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if display := hotkeyDisplayToken(token); display != "" {
			parts = append(parts, display)
		}
	}
	return strings.Join(parts, " + ")
}

// parseHotkeyDisplay converts a friendly or canonical accelerator to the stored
// canonical form; ok=false when a token is not a modifier or a supported key.
func parseHotkeyDisplay(text string) (accelerator string, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", true
	}
	tokens := strings.Split(text, "+")
	modifiers := make([]string, 0, len(tokens)-1)
	seen := map[string]bool{}
	key := ""
	for index, raw := range tokens {
		token := strings.ToLower(strings.TrimSpace(raw))
		token = strings.ReplaceAll(token, "cmd/ctrl", "cmdorctrl")
		token = strings.ReplaceAll(token, "cmd/c", "cmdorctrl")
		if index == len(tokens)-1 && validGlobalHotkeyKey(strings.TrimSpace(raw)) {
			key = canonicalHotkeyKeyDisplay(raw)
			continue
		}
		modifier, isModifier := canonicalHotkeyModifierSetting(token)
		if !isModifier || seen[modifier] {
			return "", false
		}
		seen[modifier] = true
		modifiers = append(modifiers, modifier)
	}
	if key == "" {
		return "", false
	}
	return strings.Join(append(modifiers, key), "+"), true
}

// canonicalHotkeyKeyDisplay normalizes one key token for the stored form.
func canonicalHotkeyKeyDisplay(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	if canonical, ok := namedHotkeyKeysSetting[lower]; ok {
		return canonical
	}
	if len(trimmed) == 1 {
		return strings.ToUpper(trimmed)
	}
	if strings.HasPrefix(lower, "f") {
		if parsed, err := strconv.Atoi(lower[1:]); err == nil && parsed >= 1 && parsed <= 12 &&
			strconv.Itoa(parsed) == lower[1:] {
			return "F" + lower[1:]
		}
	}
	return trimmed
}

// hotkeyTokenForKey maps one pressed key to its accelerator token ("" when the
// key is not usable in an accelerator).
func hotkeyTokenForKey(key ui.Key) string {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return string(rune('A' + int(key-ui.KeyA)))
	case key >= ui.Key0 && key <= ui.Key9:
		return string(rune('0' + int(key-ui.Key0)))
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		return "F" + strconv.Itoa(1+int(key-ui.KeyF1))
	}
	switch key {
	case ui.KeySpace:
		return "Space"
	case ui.KeyEnter:
		return "Enter"
	case ui.KeyEscape:
		return "Escape"
	case ui.KeyTab:
		return "Tab"
	case ui.KeyBackspace:
		return "Backspace"
	case ui.KeyDelete:
		return "Delete"
	case ui.KeyInsert:
		return "Insert"
	case ui.KeyUp:
		return "Up"
	case ui.KeyDown:
		return "Down"
	case ui.KeyLeft:
		return "Left"
	case ui.KeyRight:
		return "Right"
	case ui.KeyHome:
		return "Home"
	case ui.KeyEnd:
		return "End"
	case ui.KeyPageUp:
		return "PageUp"
	case ui.KeyPageDown:
		return "PageDown"
	}
	return ""
}

// captureHotkey records one pressed combination as the accelerator draft; Esc
// cancels the recording. It is the TextKey callback of the capture field.
func (st *appearanceState) captureHotkey(k ui.TextKey, locale contracts.LocaleService) bool {
	if k.Key == ui.KeyEscape && k.Mods == 0 {
		st.capturing = false
		st.captureText = ""
		return true
	}
	key := hotkeyTokenForKey(k.Key)
	if key == "" {
		return true
	}
	modifiers := make([]string, 0, 3)
	if k.Mods&ui.Super != 0 {
		modifiers = append(modifiers, "Cmd")
	}
	if k.Mods&ui.Ctrl != 0 {
		if ui.Cmd == ui.Ctrl {
			modifiers = append(modifiers, "CmdOrCtrl")
		} else {
			modifiers = append(modifiers, "Ctrl")
		}
	}
	if k.Mods&ui.Alt != 0 {
		modifiers = append(modifiers, "Alt")
	}
	if k.Mods&ui.Shift != 0 {
		modifiers = append(modifiers, "Shift")
	}
	if len(modifiers) == 0 {
		st.hotkeyError = localized(locale,
			"请按住 Cmd/Ctrl、Alt 或 Shift 再按键",
			"Hold Cmd/Ctrl, Alt or Shift while pressing the key")
		return true
	}
	accelerator := strings.Join(append(modifiers, key), "+")
	if !validGlobalHotkeyValue(accelerator) {
		st.hotkeyError = localized(locale, "该组合键不可用", "That combination cannot be used")
		return true
	}
	st.globalHotkey = accelerator
	st.hotkeyText = formatHotkeyDisplay(accelerator)
	st.hotkeyError = ""
	st.capturing = false
	st.captureText = ""
	return true
}

// syncHotkeyDraft parses the visible hotkey text into the canonical accelerator
// the page persists, recording the inline error when it is not a valid chord.
func (st *appearanceState) syncHotkeyDraft(locale contracts.LocaleService) {
	accelerator, ok := parseHotkeyDisplay(st.hotkeyText)
	if !ok {
		st.hotkeyError = globalHotkeyInvalidMessage(locale)
		st.globalHotkey = strings.TrimSpace(st.hotkeyText)
		return
	}
	st.globalHotkey = accelerator
	if !validGlobalHotkeyValue(accelerator) {
		st.hotkeyError = globalHotkeyInvalidMessage(locale)
		return
	}
	st.hotkeyError = ""
}

// buildGlobalHotkeySection renders the hotkey input (with an inline error), the
// record button and the quick presets.
func buildGlobalHotkeySection(c *ui.Context, t *design.Tokens, state *appearanceState,
	store contracts.SettingsService, locale contracts.LocaleService) {
	uikit.Section(c, localized(locale, "全局热键", "Global hotkey"), func(*ui.Context) {
		// The section is already titled 全局热键; the field names the value itself
		// so assistive technology does not read the same label twice (L-02).
		ui.Field(c, localized(locale, "组合键", "Shortcut"), func() {
			if state.capturing {
				capture := ui.TextInput(c, &state.captureText).
					Label(localized(locale, "录制全局热键", "Record global hotkey")).
					Placeholder(localized(locale, "按下组合键…", "Press a combination…")).
					FillWidth()
				capture = capture.Bind(&state.captureInput)
				capture.OnTextKey(func(k ui.TextKey) bool {
					return state.captureHotkey(k, locale)
				})
				if state.captureFocus {
					state.captureFocus = false
					state.captureInput.Focus(c)
				}
			} else {
				input := ui.TextInput(c, &state.hotkeyText).
					Label(inputLabel(locale, localized(locale, "组合键", "Shortcut"))).
					Placeholder("Cmd/Ctrl + Shift + Space").FillWidth()
				if input.Changed() {
					state.syncHotkeyDraft(locale)
				}
			}
		}).Description(localized(locale,
			"留空关闭；按「录入」直接按组合键，或输入如 Cmd/Ctrl + Shift + Space；修改后重启应用生效。",
			"Leave empty to disable; press Record and press the keys, or type e.g. Cmd/Ctrl + Shift + Space; takes effect after restart.")).
			Error(state.hotkeyError)
		if current := strings.TrimSpace(store.Get(contracts.SettingGlobalHotkey, "")); current != "" {
			design.Apply(ui.Text(c, localized(locale, "当前：", "Current: ")+formatHotkeyDisplay(current)),
				t.Font(design.TypeXS), t.MonoFamily()).TextColor(t.LabelSecondary)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			recordLabel := localized(locale, "录入", "Record")
			if state.capturing {
				recordLabel = localized(locale, "取消录入", "Cancel recording")
			}
			if design.Apply(ui.Button(c, recordLabel), t.SecondaryButton()).Clicked() {
				state.capturing = !state.capturing
				state.captureFocus = state.capturing
				state.captureText = ""
				state.hotkeyError = ""
				c.Invalidate()
			}
			if state.capturing {
				design.Apply(ui.Text(c, localized(locale, "按下组合键，Esc 取消",
					"Press the combination; Esc cancels")), t.TextMuted(design.TypeXS))
			}
			ui.Spacer(c).Grow(1)
			if state.globalHotkey != state.savedHotkey {
				design.Apply(ui.Text(c, localized(locale, "未应用", "Not applied")),
					t.TextMuted(design.TypeXS))
			}
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			for _, preset := range hotkeyPresets {
				preset := preset
				if design.Apply(ui.Button(c, formatHotkeyDisplay(preset)),
					t.SecondaryButton()).Clicked() {
					state.globalHotkey = preset
					state.hotkeyText = formatHotkeyDisplay(preset)
					state.hotkeyError = ""
					state.capturing = false
					c.Invalidate()
				}
			}
		})
	})
}

// --- accelerator validation (mirrors the host's normalizeHotkey) -------------

// globalHotkeyInvalidMessage is the localized validation error.
func globalHotkeyInvalidMessage(locale contracts.LocaleService) string {
	return localized(locale,
		"全局热键格式无效：请按下「录入」按组合键，或输入如 Cmd/Ctrl + Shift + Space",
		"Invalid global hotkey: press Record and press the combination, or type e.g. Cmd/Ctrl + Shift + Space")
}

// validGlobalHotkeyValue mirrors the host's accelerator rules: empty disables;
// otherwise at least one modifier plus one supported key.
func validGlobalHotkeyValue(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	parts := strings.Split(trimmed, "+")
	if len(parts) < 2 {
		return false
	}
	seen := make(map[string]bool, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		modifier, ok := canonicalHotkeyModifierSetting(part)
		if !ok {
			return false
		}
		if seen[modifier] {
			return false
		}
		seen[modifier] = true
	}
	return validGlobalHotkeyKey(parts[len(parts)-1])
}

// canonicalHotkeyModifierSetting maps one modifier token to its canonical name.
func canonicalHotkeyModifierSetting(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "cmdorctrl", "commandorcontrol":
		return "CmdOrCtrl", true
	case "cmd", "command":
		return "Cmd", true
	case "ctrl", "control":
		return "Ctrl", true
	case "alt", "option":
		return "Alt", true
	case "shift":
		return "Shift", true
	case "super", "meta", "win":
		return "Super", true
	default:
		return "", false
	}
}

// validGlobalHotkeyKey accepts A-Z, 0-9, F1-F12 and the same named keys as the
// host's parser.
func validGlobalHotkeyKey(raw string) bool {
	key := strings.TrimSpace(raw)
	if key == "" {
		return false
	}
	runes := []rune(key)
	if len(runes) == 1 {
		switch {
		case runes[0] >= 'a' && runes[0] <= 'z', runes[0] >= 'A' && runes[0] <= 'Z':
			return true
		case runes[0] >= '0' && runes[0] <= '9':
			return true
		default:
			return false
		}
	}
	lower := strings.ToLower(key)
	if _, ok := namedHotkeyKeysSetting[lower]; ok {
		return true
	}
	if len(lower) >= 2 && lower[0] == 'f' {
		if parsed, err := strconv.Atoi(lower[1:]); err == nil && parsed >= 1 && parsed <= 12 && strconv.Itoa(parsed) == lower[1:] {
			return true
		}
	}
	return false
}

// namedHotkeyKeysSetting are the supported named keys (mirrors the host).
var namedHotkeyKeysSetting = map[string]string{
	"space": "Space", "enter": "Enter", "return": "Enter", "escape": "Escape", "esc": "Escape",
	"tab": "Tab", "backspace": "Backspace", "delete": "Delete", "del": "Delete",
	"insert": "Insert", "ins": "Insert", "up": "Up", "down": "Down", "left": "Left",
	"right": "Right", "home": "Home", "end": "End", "pageup": "PageUp", "pagedown": "PageDown",
}
