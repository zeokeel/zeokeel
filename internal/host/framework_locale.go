package host

import (
	"strings"

	// The framework's own localization: mygo localizes its native controls,
	// dialogs, menus and accessibility strings separately from the product's
	// copy. Importing the catalog registers it so SetLocale("zh-Hans") has
	// something to translate with; the English source strings are built in.
	_ "github.com/egoist/mygo/locales/zh-Hans"
)

// frameworkLocale maps the product's language setting (contracts.LocaleService:
// "zh" or "en") to the tag mygo's own strings use. Simplified Chinese is
// mygo's "zh-Hans" catalog; every other product language falls back to the
// framework's built-in English, which is what an unset locale shows.
//
// The framework persists the tag App.SetLocale writes, but the product is the
// source of truth: every run sets it again from the stored ui.language setting
// (and a runtime switch mirrors it through uiroot.Config.OnLanguageChange), so
// the two cannot drift apart.
func frameworkLocale(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch {
	case lang == "zh", strings.HasPrefix(lang, "zh-"), strings.HasPrefix(lang, "zh_"):
		return "zh-Hans"
	default:
		return "en"
	}
}
