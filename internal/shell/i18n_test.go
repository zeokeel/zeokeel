package shell

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
)

// TestMain pins the fallback live language to English for the package's
// existing English-oriented UI tests. The production default (no persisted
// ui.language) is Chinese, which TestLiveLanguageDefault exercises.
func TestMain(m *testing.M) {
	SetDefaultLanguageForTest(LangEN)
	os.Exit(m.Run())
}

func TestCatalogComplete(t *testing.T) {
	if len(translations) < 300 {
		t.Fatalf("catalog has %d keys, want the full UI surface (>=300)", len(translations))
	}
	for key, tr := range translations {
		if strings.TrimSpace(tr.en) == "" {
			t.Errorf("%s: empty English text", key)
		}
		if strings.TrimSpace(tr.zh) == "" {
			t.Errorf("%s: empty Chinese text", key)
		}
	}
	// Every key a.T("...") in the package must be in the catalog.
	re := regexp.MustCompile(`a\.T\("([^"]+)"\)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			seen++
			if !hasTranslation(m[1]) {
				t.Errorf("%s: a.T(%q) has no catalog entry", name, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no a.T call sites found")
	}
}

func TestCatalogFixedKeys(t *testing.T) {
	fixed := []string{
		"shell.new_session", "shell.plugins", "shell.automation_tasks",
		"shell.workspaces", "shell.search_sessions", "shell.view_options",
		"home.tagline", "home.composer_hint", "home.composer_placeholder",
		"session.chat", "session.trajectory", "session.working_indicator",
		"session.send", "session.stop", "session.todos", "session.question",
		"session.send_answer", "session.presented", "session.trajectory_empty",
		"settings.settings", "settings.general", "settings.language",
		"settings.permission", "settings.font_size", "settings.work_details",
		"settings.keyboard_shortcuts", "settings.appearance", "settings.settings_saved",
		"plugins.title", "plugins.built_in_plugins", "plugins.experimental_plugins",
		"plugins.experimental_badge", "plugins.add", "plugins.more",
		"menu.more", "menu.settings", "menu.about", "menu.quit",
		"menu.rename", "menu.delete", "menu.cancel", "menu.save",
		"automation.automation_tasks", "automation.new", "automation.search",
		"automation.empty", "automation.run_now", "automation.edit",
		"models.models", "models.add_model", "models.save", "models.cancel",
		"workspace.add_workspace", "workspace.rename_workspace",
		"files.files", "files.terminal", "files.run", "files.clear",
		"composer.approval_required", "composer.allow", "composer.deny",
	}
	for _, key := range fixed {
		tr, ok := translations[key]
		if !ok {
			t.Errorf("fixed key %q missing", key)
			continue
		}
		if tr.en == "" || tr.zh == "" {
			t.Errorf("fixed key %q incomplete: %+v", key, tr)
		}
	}
}

// TestLiveAppEnglish covers the package's English test default end to end on
// the controller-backed Home screen (TestMain pins the fallback language to
// English for the existing English-oriented UI tests).
func TestLiveAppEnglish(t *testing.T) {
	h := newLiveAppAt(t, Home)
	if got := h.app.lang(); got != LangEN {
		t.Fatalf("live App language = %q, want %q", got, LangEN)
	}
	if got := h.app.T("home.tagline"); got != "What will you build today?" {
		t.Fatalf("live T(home.tagline) = %q", got)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("What will you build today?") || !tt.HasText("More") {
		t.Fatalf("live App did not render English: %q", tt.Texts())
	}
	if tt.HasText("今天想构建什么？") {
		t.Fatalf("live App rendered Chinese: %q", tt.Texts())
	}
	// The retired DSH hero copy must not come back: the Home header is the
	// brand wordmark and an owned tagline now.
	if tt.HasText("Into the Unknown") || tt.HasText("Preview") || tt.HasText("驶向未知") || tt.HasText("预览") {
		t.Fatalf("live App still renders the retired DSH hero: %q", tt.Texts())
	}
	// Explicit test hook and explicit field both work on a live App; the next
	// frame follows the switch immediately.
	h.app.SetLanguageForTest(LangZH)
	if got := h.app.T("home.tagline"); got != "今天想构建什么？" {
		t.Fatalf("SetLanguageForTest(zh) on live App: %q", got)
	}
	tt.Frame()
	if !tt.HasText("今天想构建什么？") {
		t.Fatalf("live App did not rerender in Chinese: %q", tt.Texts())
	}
}

func TestLanguageSwitchRerenders(t *testing.T) {
	render := func(lang Lang) string {
		h := newLiveAppAt(t, Home)
		h.app.SetLanguageForTest(lang)
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		return strings.Join(tt.Texts(), "\n")
	}
	enText := render(LangEN)
	zhText := render(LangZH)

	if enText == zhText {
		t.Fatal("English and Chinese renders produced identical text")
	}
	for _, want := range []string{"What will you build today?", "Workspace Write", "More"} {
		if !strings.Contains(enText, want) {
			t.Errorf("English render missing %q", want)
		}
	}
	for _, want := range []string{"今天想构建什么？", "工作区写入", "更多"} {
		if !strings.Contains(zhText, want) {
			t.Errorf("Chinese render missing %q", want)
		}
	}
	if strings.Contains(zhText, "What will you build today?") || strings.Contains(enText, "今天想构建什么？") {
		t.Fatal("render leaked the other language")
	}
}

func TestFormatAndLookupFallback(t *testing.T) {
	en := &App{Language: LangEN}
	zh := &App{Language: LangZH}
	if got := en.T("time.minutes_short", 6); got != "6min" {
		t.Errorf("en minutes = %q", got)
	}
	if got := zh.T("time.minutes_short", 6); got != "6 分钟" {
		t.Errorf("zh minutes = %q", got)
	}
	if got := en.T("session.usage_context"); got != "26%" {
		t.Errorf("no-arg percent text = %q", got)
	}
	if got := en.T("does.not.exist"); got != "does.not.exist" {
		t.Errorf("missing key = %q, want key itself", got)
	}
	if got := zh.T("settings.language_chinese"); got != "中文" {
		t.Errorf("zh language option = %q", got)
	}
}

// fakeSettings is the injected settings store used by the persistence test.
type fakeSettings struct {
	cfg   session.Settings
	loads int
	saves int
}

func (f *fakeSettings) Load() (session.Settings, error) {
	f.loads++
	return f.cfg, nil
}

func (f *fakeSettings) Save(cfg session.Settings) error {
	f.saves++
	f.cfg = cfg
	return nil
}

func TestPersistencePath(t *testing.T) {
	fake := &fakeSettings{cfg: session.DefaultSettings()}
	app := &App{settings: fake}
	// DefaultSettings.Language matches the live shell's product default
	// (Chinese), so a fresh install shows and reports the same language.
	if got := app.T("home.tagline"); got != "今天想构建什么？" {
		t.Fatalf("default language = %q, want Chinese from ui.language default", got)
	}
	if fake.saves != 0 || fake.cfg.Language != session.LanguageChinese {
		t.Fatalf("fresh fake settings were rewritten: %+v (saves %d)", fake.cfg, fake.saves)
	}
	app.updateSettings(nil, func(s *session.Settings) { s.Language = session.LanguageChinese })
	if fake.saves != 1 || fake.cfg.Language != session.LanguageChinese {
		t.Fatalf("saved settings = %+v (saves %d), want zh", fake.cfg, fake.saves)
	}
	if got := app.T("home.tagline"); got != "今天想构建什么？" {
		t.Fatalf("after switch to zh: %q", got)
	}
	app.updateSettings(nil, func(s *session.Settings) { s.Language = session.LanguageEnglish })
	if got := app.T("home.tagline"); got != "What will you build today?" {
		t.Fatalf("after switch back to en: %q", got)
	}
}

// TestLiveLanguageDefault covers the controller-backed path end to end:
// no stored ui.language means Chinese in production, an explicit en means
// English, and a switch through updateSettings applies immediately.
func TestLiveLanguageDefault(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})

	old := testDefaultLanguage
	SetDefaultLanguageForTest("")
	t.Cleanup(func() { SetDefaultLanguageForTest(old) })

	// No persisted ui.language: product default is Chinese.
	if got := (&App{Ctrl: h.ctrl}).lang(); got != LangZH {
		t.Fatalf("fresh live language = %q, want zh", got)
	}

	// Explicit en in ui.language wins over the product default.
	if err := h.ctrl.Settings().Save(session.Settings{Language: session.LanguageEnglish}); err != nil {
		t.Fatalf("Save en: %v", err)
	}
	en := &App{Ctrl: h.ctrl}
	if got := en.lang(); got != LangEN {
		t.Fatalf("persisted en language = %q, want en", got)
	}
	if got := en.T("home.tagline"); got != "What will you build today?" {
		t.Fatalf("persisted en text = %q", got)
	}

	// Switching through the settings UI persists and applies immediately.
	en.updateSettings(nil, func(s *session.Settings) { s.Language = session.LanguageChinese })
	if got := en.T("home.tagline"); got != "今天想构建什么？" {
		t.Fatalf("immediate zh switch = %q", got)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil || cfg.Language != session.LanguageChinese {
		t.Fatalf("persisted language = %q, %v; want zh", cfg.Language, err)
	}
}

// TestFirstRunLanguageConsistent reproduces the probe without the package's
// English override: a fresh live store displays Chinese, Settings reports
// Chinese, and changing another setting writes the same language instead of
// flipping the UI.
func TestFirstRunLanguageConsistent(t *testing.T) {
	old := testDefaultLanguage
	SetDefaultLanguageForTest("")
	t.Cleanup(func() { SetDefaultLanguageForTest(old) })

	h := newLiveApp(t, &shellProvider{})
	app := h.app
	app.Screen = Home
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	if got := app.lang(); got != LangZH {
		t.Fatalf("fresh live language = %q, want zh", got)
	}
	if !tt.HasText("今天想构建什么？") {
		t.Fatalf("fresh live Home is not Chinese: %q", tt.Texts())
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != session.LanguageChinese {
		t.Fatalf("Settings language = %q, want zh (same as the UI)", cfg.Language)
	}
	// The first frame persisted the resolved default, not an English default.
	raw, err := h.ctrl.Store().GetSetting(session.SettingLanguage)
	if err != nil || raw != session.LanguageChinese {
		t.Fatalf("ui.language = %q, %v; want zh", raw, err)
	}

	// Change only the theme: the language must not flip.
	tt.Key(ui.Cmd, ui.KeyComma)
	if app.Screen != Settings {
		t.Fatalf("settings did not open")
	}
	if err := tt.Click("深色"); err != nil {
		// The tester locale follows app.lang(), which is Chinese here.
		t.Fatalf("click dark theme: %v", err)
	}
	tt.Frame()
	if got := app.lang(); got != LangZH {
		t.Fatalf("language flipped to %q after a theme change", got)
	}
	raw, err = h.ctrl.Store().GetSetting(session.SettingLanguage)
	if err != nil || raw != session.LanguageChinese {
		t.Fatalf("ui.language = %q, %v after theme change; want zh", raw, err)
	}
}
