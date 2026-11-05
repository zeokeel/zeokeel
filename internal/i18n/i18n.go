// Package i18n owns ZeoKeel's user-visible UI string catalog.
//
// The shell (and any other UI surface) resolves a message through T with the
// language it currently shows; the catalog lives here so the God package stays
// a renderer (AR-04). Keys are grouped by surface (shell, home, session,
// settings, automation, ...). Every entry has both languages; T falls back to
// the key itself so a missing entry is visible in development rather than
// silently blank.
package i18n

import (
	"fmt"
)

// Lang is a UI language code persisted as ui.language.
type Lang string

const (
	// LangEN is English.
	LangEN Lang = "en"
	// LangZH is Simplified Chinese.
	LangZH Lang = "zh"
)

// Entry is one catalog message in both languages.
type Entry struct {
	EN string
	ZH string
}

// liveDefaultLanguage is the product default for a live shell whose
// ui.language was never stored: Chinese.
var liveDefaultLanguage = LangZH

// testDefaultLanguage pins the unresolved live default during tests. It is
// empty in the real binary.
var testDefaultLanguage Lang

// Catalog returns the complete catalog. Callers must not mutate it.
func Catalog() map[string]Entry { return translations }

// Has reports whether key is in the catalog.
func Has(key string) bool {
	_, ok := translations[key]
	return ok
}

// T returns key's text in lang. Optional args are formatted with fmt.Sprintf,
// so catalog entries may use verbs. An unknown key returns the key itself.
func T(lang Lang, key string, args ...any) string {
	entry, ok := translations[key]
	if !ok {
		return key
	}
	s := entry.EN
	if lang == LangZH {
		s = entry.ZH
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// Normalize validates a stored language code. Anything else is returned as the
// empty Lang so callers can fall through to their next source.
func Normalize(v string) Lang {
	switch Lang(v) {
	case LangEN, LangZH:
		return Lang(v)
	}
	return ""
}

// DefaultLive is the language a fresh live shell shows when ui.language was
// never stored. The test hook overrides it; the zero Lang restores the
// product default.
func DefaultLive() Lang {
	if testDefaultLanguage != "" {
		return testDefaultLanguage
	}
	return liveDefaultLanguage
}

// SetDefaultLanguageForTest pins the fallback live language for tests. Tests
// restore it with the zero Lang.
func SetDefaultLanguageForTest(l Lang) { testDefaultLanguage = l }
