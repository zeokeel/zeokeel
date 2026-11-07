package settings

// The appearance page's global-hotkey panel: a native input for an Electron-style
// accelerator that the process host registers system-wide at the next start.
// The host owns registration; the page validates the format (the same rules as
// the host's normalizeHotkey) and persists the trimmed value.

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// buildGlobalHotkeyPanel renders the hotkey input, the currently applied value
// and the restart/empty-to-disable hint.
func buildGlobalHotkeyPanel(c *ui.Context, t *design.Tokens, state *appearanceState,
	store contracts.SettingsService, locale contracts.LocaleService) {
	panel(c, t, func() {
		sectionTitle(c, t, localized(locale, "全局热键", "Global hotkey"))
		if current := strings.TrimSpace(store.Get(contracts.SettingGlobalHotkey, "")); current != "" {
			design.Apply(ui.Text(c, localized(locale, "当前：", "Current: ")+current),
				t.MonoFamily()).FontSize(12).TextColor(t.LabelSecondary)
		}
		ui.TextInput(c, &state.globalHotkey).
			Label(inputLabel(locale, localized(locale, "全局热键", "Global hotkey"))).
			Placeholder("CmdOrCtrl+Shift+Space").FillWidth()
		design.Apply(ui.Text(c, localized(locale,
			"留空关闭；需要桌面支持；修改后重启应用生效。",
			"Leave empty to disable; requires desktop support; takes effect after restart.")),
			t.TextMuted(design.TypeXXS)).MaxLines(1).Ellipsis("…")
	})
}

// globalHotkeyInvalidMessage is the localized validation error.
func globalHotkeyInvalidMessage(locale contracts.LocaleService) string {
	return localized(locale,
		"全局热键格式无效：请使用如 CmdOrCtrl+Shift+Space 的组合",
		"Invalid global hotkey: use a combination like CmdOrCtrl+Shift+Space")
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
