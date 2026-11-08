package uikit

import (
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

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

// FormatDateTime formats instant for the user's language: the Chinese product
// default keeps the compact "2006-01-02 15:04" form, English uses the readable
// "Jan 2, 2006 3:04 PM" form. One helper keeps session, inspector, memory and
// automation timestamps consistent.
func FormatDateTime(locale contracts.LocaleService, instant time.Time) string {
	if instant.IsZero() {
		return ""
	}
	instant = instant.Local()
	if Language(locale) == "en" {
		return instant.Format("Jan 2, 2006 3:04 PM")
	}
	return instant.Format("2006-01-02 15:04")
}

// FormatClock formats the time of day for event rows and logs.
func FormatClock(locale contracts.LocaleService, instant time.Time) string {
	if instant.IsZero() {
		return ""
	}
	instant = instant.Local()
	if Language(locale) == "en" {
		return instant.Format("3:04:05 PM")
	}
	return instant.Format("15:04:05")
}

// Language reports the effective language code ("zh" when no locale service).
func Language(locale contracts.LocaleService) string {
	if locale == nil {
		return "zh"
	}
	return locale.Language()
}
