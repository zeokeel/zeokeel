package uiroot

import (
	"strings"
	"sync"
)

// localeService is the host locale state (contracts.LocaleService). Copy lives
// in one place (uiroot + later internal/i18n); modules ask for keys, never
// hard-code bilingual strings in layouts.
type localeService struct {
	root *Root
	mu   sync.Mutex
	lang string
}

// localeText holds ZeoKeel's built-in copy. Keys are stable IDs; unknown keys
// fall back to the key itself (visible in tests, never blank UI).
var localeText = map[string]map[string]string{
	"en": {
		"app.name":         "ZeoKeel",
		"nav.screens":      "Screens",
		"nav.settings":     "Settings",
		"nav.back":         "Back",
		"settings.title":   "Settings",
		"settings.none":    "No settings pages registered yet.",
		"screen.empty":     "ZeoKeel is ready.",
		"action.about":     "About",
		"action.home":      "Home",
		"status.modules":   "Modules",
		"status.no-screen": "No screen registered",
	},
	"zh": {
		"app.name":         "ZeoKeel",
		"nav.screens":      "界面",
		"nav.settings":     "设置",
		"nav.back":         "返回",
		"settings.title":   "设置",
		"settings.none":    "还没有注册任何设置页。",
		"screen.empty":     "ZeoKeel 已就绪。",
		"action.about":     "关于",
		"action.home":      "首页",
		"status.modules":   "模块",
		"status.no-screen": "未注册界面",
	},
}

func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch {
	case strings.HasPrefix(lang, "zh"):
		return "zh"
	case strings.HasPrefix(lang, "en"):
		return "en"
	default:
		return "zh"
	}
}

func (l *localeService) Language() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lang
}

func (l *localeService) SetLanguage(lang string) {
	lang = normalizeLang(lang)
	l.mu.Lock()
	changed := l.lang != lang
	l.lang = lang
	l.mu.Unlock()
	if !changed {
		return
	}
	// The framework's own copy (native controls, dialogs and accessibility
	// text) is localized by mygo, not by this service; the host mirrors the new
	// language into it through the hook (fix round SH-06).
	if hook := l.root.onLanguageChange; hook != nil {
		hook(lang)
	}
	l.root.requestFrame()
}

func (l *localeService) T(key string) string {
	l.mu.Lock()
	lang := l.lang
	l.mu.Unlock()
	if table, ok := localeText[lang]; ok {
		if text, ok := table[key]; ok {
			return text
		}
	}
	if table, ok := localeText["en"]; ok {
		if text, ok := table[key]; ok {
			return text
		}
	}
	return key
}
