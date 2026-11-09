package welcome_test

// Conformance tests for the example welcome module: it contributes the Home
// hero and one command that actually copies the app version (the old command
// only invalidated a frame).

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
	"github.com/zeokeel/zeokeel/internal/ui/welcome"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newHarness loads home + welcome into one host frame and returns the tester.
func newHarness(t *testing.T, language string, info contracts.AppInfo) (*uiroot.Root, *ui.Tester) {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyAppInfo, info)

	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyUIRuntime).AddBuiltin(contracts.KeyAppInfo).
		AddBuiltin(contracts.KeyNavigator)
	if err := registry.Register(home.Module()); err != nil {
		t.Fatalf("register home: %v", err)
	}
	if err := registry.Register(welcome.Module()); err != nil {
		t.Fatalf("register welcome: %v", err)
	}
	entries := []module.Entry{
		{ID: "home", Module: "zeokeel-ui/home"},
		{ID: "welcome", Module: "zeokeel-ui/welcome"},
	}
	if _, err := module.Load(ctx, registry, entries); err != nil {
		t.Fatalf("load modules: %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	return uiRoot, tester
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// TestWelcomeHeroAndCopyVersionCommand covers the visible contribution and the
// command's behavior: it copies the version the app info carries.
func TestWelcomeHeroAndCopyVersionCommand(t *testing.T) {
	uiRoot, tester := newHarness(t, "zh", contracts.AppInfo{Name: "ZeoKeel", Version: "1.2.3"})
	if !tester.HasText("欢迎使用 ZeoKeel") {
		t.Fatalf("welcome hero missing; texts = %v", tester.Texts())
	}
	var copyVersion *contracts.CommandSpec
	for _, spec := range uiRoot.Runtime().Commands() {
		if spec.ID == "welcome.copy-version" {
			spec := spec
			copyVersion = &spec
			break
		}
	}
	if copyVersion == nil {
		t.Fatalf("copy-version command missing from %v", uiRoot.Runtime().Commands())
	}
	if copyVersion.Title != "复制版本" {
		t.Fatalf("Chinese command title = %q", copyVersion.Title)
	}
	if copyVersion.Run == nil {
		t.Fatal("copy-version command has no Run")
	}
	if err := copyVersion.Run(); err != nil {
		t.Fatalf("run copy-version: %v", err)
	}
	if got := tester.Clipboard(); got != "1.2.3" {
		t.Fatalf("clipboard = %q, want the app version 1.2.3", got)
	}
}

// TestWelcomeCommandEnglishCopy covers the localized command label.
func TestWelcomeCommandEnglishCopy(t *testing.T) {
	uiRoot, tester := newHarness(t, "en", contracts.AppInfo{Name: "ZeoKeel", Version: "9.9.9"})
	for _, spec := range uiRoot.Runtime().Commands() {
		if spec.ID != "welcome.copy-version" {
			continue
		}
		if spec.Title != "Copy version" {
			t.Fatalf("English command title = %q", spec.Title)
		}
		if err := spec.Run(); err != nil {
			t.Fatalf("run copy-version: %v", err)
		}
		if got := tester.Clipboard(); got != "9.9.9" {
			t.Fatalf("clipboard = %q, want 9.9.9", got)
		}
		return
	}
	t.Fatalf("copy-version command missing from %v", uiRoot.Runtime().Commands())
}
