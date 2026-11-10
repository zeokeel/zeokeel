package about_test

// i18n frame tests: the About page content and its host-rendered settings
// navigation title follow the host language, including an in-session switch.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel-kernel/module"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/about"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newHarness builds the host frame with the About module active.
func newHarness(t *testing.T, language string, cfg about.Config, info contracts.AppInfo) *uiroot.Root {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	if _, err := ctx.Provide(contracts.KeyUIRuntime, uiRoot.Runtime()); err != nil {
		t.Fatalf("provide ui runtime: %v", err)
	}
	if _, err := ctx.Provide(contracts.KeyNavigator, uiRoot.Navigator()); err != nil {
		t.Fatalf("provide navigator: %v", err)
	}
	if _, err := ctx.Provide(contracts.KeyTheme, uiRoot.Theme()); err != nil {
		t.Fatalf("provide theme: %v", err)
	}
	if _, err := ctx.Provide(contracts.KeyLocale, uiRoot.Locale()); err != nil {
		t.Fatalf("provide locale: %v", err)
	}
	if _, err := ctx.Provide(contracts.KeyAppInfo, info); err != nil {
		t.Fatalf("provide app info: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyUIRuntime).AddBuiltin(contracts.KeyAppInfo)
	if err := registry.Register(about.Module()); err != nil {
		t.Fatalf("register about: %v", err)
	}
	entry := module.Entry{ID: "about", Module: "zeokeel-ui/about"}
	if cfg.Title != "" {
		entry.Config = map[string]any{"title": cfg.Title}
	}
	if _, err := module.Load(ctx, registry, []module.Entry{entry}); err != nil {
		t.Fatalf("activate about: %v", err)
	}
	uiRoot.Navigator().ToggleSettings()
	return uiRoot
}

// aboutHarness wraps the bare root and tester with the click helper the shared
// settings harness provides.
type aboutHarness struct {
	root   *uiroot.Root
	tester *ui.Tester
}

func (h *aboutHarness) click(label string) {
	if err := h.tester.Click(label); err != nil {
		panic("click " + label + ": " + err.Error())
	}
	h.tester.Frame()
}

func TestAboutEnglishAndLanguageSwitch(t *testing.T) {
	uiRoot := newHarness(t, "en", about.Config{}, contracts.AppInfo{
		Name: "ZeoKeel", Version: "1.2.3", Commit: "abc123",
		BuildTime: "2026-01-02", GoVersion: "go1.27.3",
	})
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	for _, want := range []string{"About", "About ZeoKeel",
		"Build information", "Development information", "Architecture notes",
		"Name", "Version", "Commit", "Build time", "1.2.3", "abc123", "go1.27.3"} {
		if !tester.HasText(want) {
			t.Fatalf("English About frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	for _, unwanted := range []string{"关于", "构建信息", "架构"} {
		if tester.HasText(unwanted) {
			t.Fatalf("English About frame still shows %q", unwanted)
		}
	}
	// The developer notes are folded: expanding the disclosure reveals them.
	if tester.HasText("The UI is built from native mygo controls") {
		t.Fatal("the architecture notes are expanded by default")
	}
	h := &aboutHarness{root: uiRoot, tester: tester}
	h.click("Architecture notes")
	if !tester.HasText("The UI is built from native mygo controls; there is no browser or webview.") {
		t.Fatalf("expanding Architecture notes did not reveal the notes; texts = %v", tester.Texts())
	}

	// Switching back to zh updates the navigation and the page content.
	uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	// The disclosure keeps its state across the language switch; expand it if
	// it is closed here before checking the localized bullets.
	if !tester.HasText("UI 是 mygo 原生控件") {
		h.click("架构说明")
	}
	for _, want := range []string{"设置", "关于", "关于 ZeoKeel", "构建信息",
		"开发信息", "架构说明", "名称", "版本", "提交", "构建时间",
		"UI 是 mygo 原生控件，没有浏览器/webview。"} {
		if !tester.HasText(want) {
			t.Fatalf("Chinese About frame missing %q; texts = %v", want, tester.Texts())
		}
	}
}

// TestAboutConfigTitleWins pins that an explicit assembly title overrides the
// localization.
func TestAboutConfigTitleWins(t *testing.T) {
	uiRoot := newHarness(t, "en", about.Config{Title: "Credits"}, contracts.AppInfo{Name: "ZeoKeel", Version: "9"})
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Credits ZeoKeel") || tester.HasText("About ZeoKeel") {
		t.Fatalf("config title not honored; texts = %v", tester.Texts())
	}
}

// TestAboutUnknownBuildValuesFriendly pins H22: empty build metadata renders a
// friendly placeholder instead of the raw "unknown".
func TestAboutUnknownBuildValuesFriendly(t *testing.T) {
	uiRoot := newHarness(t, "en", about.Config{}, contracts.AppInfo{
		Name: "ZeoKeel", Commit: "unknown", BuildTime: "unknown",
	})
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	if tester.HasText("unknown") {
		t.Fatalf("About shows the raw unknown placeholder; texts = %v", tester.Texts())
	}
	if !tester.HasText("Unknown") {
		t.Fatalf("About lacks the friendly placeholder; texts = %v", tester.Texts())
	}
}
