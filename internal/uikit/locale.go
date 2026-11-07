package uikit

import "github.com/zeokeel/zeokeel/internal/contracts"

// T returns the English string when the locale service is in English mode and
// the Chinese (product default) string otherwise. A nil locale falls back to
// Chinese, so modules can localize without requiring the service.
func T(locale contracts.LocaleService, zh, en string) string {
	if locale == nil {
		return zh
	}
	if locale.Language() == "en" {
		return en
	}
	return zh
}

// Language reports the effective language code ("zh" when no locale service).
func Language(locale contracts.LocaleService) string {
	if locale == nil {
		return "zh"
	}
	return locale.Language()
}
