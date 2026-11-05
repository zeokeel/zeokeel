package shell

import (
	"errors"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/i18n"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// The UI string catalog itself lives in internal/i18n (AR-04): the shell keeps
// only the App-dependent resolution and the small positional helpers. The
// alias keeps every call site (`Lang`, `LangEN`, `LangZH`) unchanged.

// Lang is a UI language code persisted as ui.language.
type Lang = i18n.Lang

const (
	// LangEN is English.
	LangEN = i18n.LangEN
	// LangZH is Simplified Chinese.
	LangZH = i18n.LangZH
)

// testDefaultLanguage mirrors the i18n test hook so the package's existing
// tests can snapshot and restore it directly. Production code reads the value
// through defaultLiveLang.
var testDefaultLanguage Lang

// translation and translations are a compatibility view of the catalog for the
// shell's catalog tests (which iterate entries and their en/zh fields).
// Production rendering goes through the i18n package directly.
type translation struct {
	en, zh string
}

var translations = shellTranslationView()

// shellTranslationView mirrors i18n.Catalog() into the historical struct shape.
func shellTranslationView() map[string]translation {
	catalog := i18n.Catalog()
	view := make(map[string]translation, len(catalog))
	for key, entry := range catalog {
		view[key] = translation{en: entry.EN, zh: entry.ZH}
	}
	return view
}

// SetLanguageForTest pins an App's language for a test. It is a tiny exported
// hook because many shell tests construct App{} and must stay deterministic.
func (a *App) SetLanguageForTest(l Lang) { a.Language = l }

// SetDefaultLanguageForTest pins the fallback live language used when
// ui.language has no stored value. Tests restore it with the zero Lang.
func SetDefaultLanguageForTest(l Lang) {
	testDefaultLanguage = l
	i18n.SetDefaultLanguageForTest(l)
}

// T returns key's text in the app's current language. Optional args are
// formatted with fmt.Sprintf, so catalog entries may use verbs.
func (a *App) T(key string, args ...any) string {
	return i18n.T(a.lang(), key, args...)
}

// tr is the legacy positional helper kept for small call sites that have not
// been moved onto the key catalog yet.
func (a *App) tr(en, zh string) string {
	if a.lang() == LangZH {
		return zh
	}
	return en
}

// lang resolves the UI language once per settings change. An explicit
// App.Language wins (tests and embedders); otherwise a static App is English
// and a live App starts Chinese unless ui.language explicitly says English.
func (a *App) lang() Lang {
	if a.Language != "" {
		return a.Language
	}
	if a.langLoaded {
		return a.langValue
	}
	a.langValue = a.resolveLanguage()
	a.langLoaded = true
	return a.langValue
}

// persistResolvedLanguage writes the language a fresh live shell resolved
// from the product default, so Settings reports the same value the UI shows
// and a later unrelated change cannot flip it. It runs once per App and never
// overwrites an explicit ui.language row.
func (a *App) persistResolvedLanguage() {
	if a.Ctrl == nil || a.langPersisted {
		return
	}
	a.langPersisted = true
	if _, err := a.Ctrl.Store().GetSetting(session.SettingLanguage); !errors.Is(err, store.ErrNotFound) {
		return
	}
	_ = a.Ctrl.Store().SetSetting(session.SettingLanguage, string(a.lang()))
}

func (a *App) resolveLanguage() Lang {
	// The static demo shell is the English pixel baseline.
	if a.Ctrl == nil && a.settings == nil {
		return LangEN
	}
	// A live shell reads the raw setting so an absent key means the product
	// default, which DefaultSettings.Language now matches.
	if a.Ctrl != nil {
		if v, err := a.Ctrl.Store().GetSetting(session.SettingLanguage); err == nil {
			if l := normalizeLang(v); l != "" {
				return l
			}
		} else if errors.Is(err, store.ErrNotFound) {
			return defaultLiveLang()
		}
	}
	cfg := a.currentSettings()
	if l := normalizeLang(cfg.Language); l != "" {
		return l
	}
	return defaultLiveLang()
}

func normalizeLang(v string) Lang { return i18n.Normalize(v) }

func defaultLiveLang() Lang {
	if testDefaultLanguage != "" {
		return testDefaultLanguage
	}
	return i18n.DefaultLive()
}

// hasTranslation reports whether key is in the catalog.
func hasTranslation(key string) bool { return i18n.Has(key) }

// phaseLabel localizes a session phase for the live header.
func (a *App) phaseLabel(p state.Phase) string {
	key := "session.phase." + string(p)
	if !hasTranslation(key) {
		return string(p)
	}
	return a.T(key)
}

// todoStatusLabel localizes a todo status, falling back to the raw value for
// a status a future tool may add.
func (a *App) todoStatusLabel(status string) string {
	key := "session.todo." + status
	if !hasTranslation(key) {
		return status
	}
	return a.T(key)
}

// workingLabel is the "Deep diving for Xm Ys" indicator with its animated
// dots. The English text must stay byte-identical to indicatorLabel.
func (a *App) workingLabel(elapsed time.Duration) string {
	if a.lang() != LangZH {
		return indicatorLabel(elapsed)
	}
	return a.T("session.working", formatDiveTime(elapsed)) +
		strings.Repeat(".", design.DotsAt(elapsed))
}

// trajectoryLabelText localizes one trajectory row label at render time. The
// event data itself stays language-neutral; only the class name is translated.
func (a *App) trajectoryLabelText(label string) string {
	base, rest, ok := strings.Cut(label, " · ")
	key := "session.trajectory_entry." + base
	if hasTranslation(key) {
		base = a.T(key)
	}
	if !ok {
		return base
	}
	if base == a.T("session.trajectory_entry.status") {
		if p := a.phaseLabel(state.Phase(rest)); p != "session.phase."+rest {
			rest = p
		}
	}
	return base + " · " + rest
}
