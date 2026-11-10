package uikit

import (
	"math"
	"runtime"
	"strconv"
	"strings"
)

// FormatCompactCount renders a count with compact human units, the way the
// product shows token counts and other large numbers (DSH-style): values under
// one thousand stay exact ("460"), and larger values use one decimal at most
// with a K/M/B suffix, trimmed when the decimal is zero:
//
//	1_234         → "1.2K"
//	173_635       → "173.6K"
//	8_400_000     → "8.4M"
//	1_200_000_000 → "1.2B"
//
// Rounding carries into the next unit ("999_950" → "1M") instead of rendering
// "1000K". Zero and negative values are safe: negatives keep their sign
// ("-1.2K") and zero stays "0".
func FormatCompactCount(count int) string {
	if count > -1000 && count < 1000 {
		return strconv.Itoa(count)
	}
	units := [...]string{"K", "M", "B"}
	negative := count < 0
	value := float64(count)
	if negative {
		value = -value
	}
	unit := 0
	value /= 1000
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	// One decimal at most; a rounded 1000 is promoted to the next unit so a
	// count near a unit boundary never renders as "1000K".
	rounded := math.Round(value*10) / 10
	if rounded >= 1000 && unit < len(units)-1 {
		rounded /= 1000
		unit++
	}
	text := strconv.FormatFloat(rounded, 'f', 1, 64)
	text = strings.TrimSuffix(text, ".0")
	if negative {
		text = "-" + text
	}
	return text + units[unit]
}

// FormatShortcut normalizes one of the product's shortcut notations into the
// single display form the UI shows: modifiers joined with " + ", the command
// modifier spelled for the platform ("Cmd" on macOS, "Ctrl" elsewhere), and
// ordinary keys uppercase. It accepts every notation already in the repository
// — "CmdOrCtrl+Shift+Space", "Cmd/Ctrl + N", "Ctrl+Alt+P", "cmd+k" — so a
// keyboard reference table, the command palette and the inspector cards can
// share one formatter instead of three spellings (V11).
func FormatShortcut(raw string) string {
	return formatShortcutFor(runtime.GOOS, raw)
}

// formatShortcutFor is FormatShortcut with the platform named, so tests do not
// depend on the machine running them.
func formatShortcutFor(goos, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	fields := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '+' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return ""
	}
	primary := "Ctrl"
	if goos == "darwin" {
		primary = "Cmd"
	}
	parts := make([]string, 0, len(fields))
	key := fields[len(fields)-1]
	for _, field := range fields[:len(fields)-1] {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "cmdorctrl", "cmd/ctrl", "cmd", "command", "commandorcontrol":
			parts = append(parts, primary)
		case "ctrl", "control":
			if primary == "Cmd" {
				parts = append(parts, "Ctrl")
			} else {
				parts = append(parts, "Ctrl")
			}
		case "shift":
			parts = append(parts, "Shift")
		case "alt", "option", "opt":
			parts = append(parts, "Alt")
		default:
			// An unknown modifier token stays visible rather than silently
			// dropping a key from a shortcut the user must press.
			if text := strings.TrimSpace(field); text != "" {
				parts = append(parts, text)
			}
		}
	}
	parts = append(parts, formatShortcutKey(key))
	return strings.Join(parts, " + ")
}

// formatShortcutKey renders the final key of a shortcut: letters uppercase,
// punctuation as the character itself, and the named keys the desktop uses.
func formatShortcutKey(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
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
	case "backquote":
		return "`"
	case "comma":
		return ","
	case "period":
		return "."
	case "slash":
		return "/"
	case "backslash":
		return "\\"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "up":
		return "Up"
	case "down":
		return "Down"
	}
	key = strings.TrimSpace(key)
	if len(key) == 1 {
		return strings.ToUpper(key)
	}
	return key
}
