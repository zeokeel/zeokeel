package uikit

// The single navigation mapping for registered screen ids, shared by the
// expanded sidebar rows and the host's collapsed rail. It lives in uikit (the
// shared composition layer) so both internal/ui/sidebar and internal/uiroot can
// read one table without either importing the other's implementation — the
// module boundary forbids uiroot importing a UI module (2026-11 UI review,
// boundary ruling). A new screen adds its entry here once and neither surface
// can drift onto a different glyph or an untranslated label.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// ScreenNavSpec is the single navigation mapping for a registered screen id:
// its glyph and its localized labels. The expanded sidebar rows and the host's
// collapsed rail must both read it (the host rail draws the same destinations),
// so a new screen adds its entry here once and the rail cannot drift onto a
// different glyph or an untranslated label.
func ScreenNavSpec(id string) (icon *ui.SVG, zh, en string) {
	switch id {
	case "home":
		return icons.Home(), "首页", "Home"
	case "session":
		return icons.Message(), "对话", "Chat"
	case "automation":
		return icons.Clock(), "自动化", "Automation"
	case "insights":
		return icons.Activity(), "洞察", "Insights"
	case "plugins":
		return icons.Plugin(), "能力", "Capabilities"
	case "settings":
		return icons.Sliders(), "设置", "Settings"
	default:
		return icons.Data(), "", ""
	}
}

// ScreenNavIcon is ScreenNavSpec's glyph, for the sidebar rows.
func ScreenNavIcon(id string) *ui.SVG {
	icon, _, _ := ScreenNavSpec(id)
	return icon
}

// ScreenNavLabel is ScreenNavSpec's localized label; an unregistered id falls
// back to the id itself.
func ScreenNavLabel(locale contracts.LocaleService, id string) string {
	_, zh, en := ScreenNavSpec(id)
	if zh == "" && en == "" {
		return id
	}
	return T(locale, zh, en)
}
