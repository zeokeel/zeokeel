package uimodtest

// Headless integration tests for the demo UI modules and the injection-point
// model. Every test renders the real product view (Root.View) through mygo's
// in-memory Tester, so "the module appears in the right slot" is asserted as
// visible text, not as registry bookkeeping.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/about"
	"github.com/zeokeel/zeokeel/internal/ui/home"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
	"github.com/zeokeel/zeokeel/internal/ui/welcome"
)

// TestHomeRendersInjectionPointRegions proves home is only a composition
// screen: a third party can inject into home.hero (and therefore the hero
// region is controlled by modules, not by home).
func TestHomeRendersInjectionPointRegions(t *testing.T) {
	h := newHarness(t)
	if err := h.ui.Validate(); err == nil {
		t.Fatal("Validate passed before any screen was registered")
	}
	activate(t, h.ctx, home.Module(), home.Config{})
	if err := h.ui.Validate(); err != nil {
		t.Fatalf("Validate after home activation: %v", err)
	}

	tester := h.tester()
	h.assertTexts(tester, "首页", "编译期模块化的桌面 AI agent。",
		"这是 home 的默认 Hero 空态。", "最近会话", "工具卡", "打开设置")
	// With no renderer module active, home shows its own demo-call empty state.
	h.assertTexts(tester, "还没有 demo 工具卡渲染器（由 ui/toolcards 模块提供）。")

	// A third-party contribution to home.hero replaces the module's default
	// hero; home is not edited for this.
	unregister, err := h.ui.Runtime().RegisterSlotItem(contracts.SlotItem{
		Slot:  contracts.SlotHomeHero,
		ID:    "test.hero",
		Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "来自测试模块的 hero") },
	})
	if err != nil {
		t.Fatalf("register test hero: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "来自测试模块的 hero")
	h.assertNoTexts(tester, "这是 home 的默认 Hero 空态。") // home's default hero is replaced

	unregister()
	tester.Frame()
	h.assertNoTexts(tester, "来自测试模块的 hero")
	h.assertTexts(tester, "这是 home 的默认 Hero 空态。") // default hero is back
}

// TestWelcomeExtendsTopBarAndHomeHero is the "new module, no host change" proof:
// ui/welcome is activated on top of the existing product and its contributions
// appear in the host top bar and inside home's hero region; disposing it
// removes both again.
func TestWelcomeExtendsTopBarAndHomeHero(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, home.Module(), home.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "via ui/welcome", "欢迎使用 ZeoKeel")

	welcomeFiber := activate(t, h.ctx, welcome.Module(), welcome.Config{})
	tester.Frame()
	h.assertTexts(tester, "via ui/welcome", "欢迎使用 ZeoKeel",
		"这是一个独立编译的 UI 模块，通过注入点出现在宿主中。",
		"复制版本") // command rendered by the host's command surface

	// No host or home edit was needed for any of the above. Disposing the
	// module unwinds the contributions through its kernel effects.
	if err := welcomeFiber.Dispose(); err != nil {
		t.Fatalf("dispose welcome: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "via ui/welcome", "欢迎使用 ZeoKeel")
	h.assertTexts(tester, "编译期模块化的桌面 AI agent。") // home's own hero fallback
}

// TestToolcardsRenderDemoCall proves the tool-card injection point: home always
// renders its example call through UIRuntime.RenderTool, and a tool-card module
// activated later starts decorating it.
func TestToolcardsRenderDemoCall(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, home.Module(), home.Config{})
	tester := h.tester()
	h.assertTexts(tester, "还没有 demo 工具卡渲染器（由 ui/toolcards 模块提供）。")

	toolcardsFiber := activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	tester.Frame()
	h.assertTexts(tester, "工具调用: demo", "示例工具调用：由 ui/toolcards 模块渲染", "demo-1", "ok")
	h.assertNoTexts(tester, "还没有 demo 工具卡渲染器（由 ui/toolcards 模块提供）。")

	if err := toolcardsFiber.Dispose(); err != nil {
		t.Fatalf("dispose toolcards: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "还没有 demo 工具卡渲染器（由 ui/toolcards 模块提供）。")
}

// TestAboutExtendsSettingsSurface proves a module can contribute a settings
// page and extend the settings header slot without changing the host: the host
// renders settings.header for every page and about's item appears there.
func TestAboutExtendsSettingsSurface(t *testing.T) {
	h := newHarness(t)
	aboutFiber := activate(t, h.ctx, about.Module(), about.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester()
	h.assertTexts(tester,
		"关于",                   // sidebar page + page title
		"关于 ZeoKeel",           // page header (title + app name)
		"构建信息",                 // page body section
		"ZeoKeel v0.1.0",       // settings.header slot item contributed by about
		"2026-01-01T00:00:00Z", // build time from appinfo config
	)

	if err := aboutFiber.Dispose(); err != nil {
		t.Fatalf("dispose about: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "构建信息", "ZeoKeel v0.1.0")
	h.assertTexts(tester, "还没有注册任何设置页。") // host empty state
}

// TestModuleManifests pins each module's contract: stable ID, required host
// services (so the assembly pre-flight can resolve them) and no provided
// service a UI module does not need.
func TestModuleManifests(t *testing.T) {
	assertManifest(t, home.Module(), "zeokeel-ui/home",
		contracts.KeyUIRuntime.Name(), contracts.KeyNavigator.Name(), contracts.KeyAppInfo.Name())
	assertManifest(t, about.Module(), "zeokeel-ui/about",
		contracts.KeyUIRuntime.Name(), contracts.KeyAppInfo.Name())
	assertManifest(t, welcome.Module(), "zeokeel-ui/welcome",
		contracts.KeyUIRuntime.Name(), contracts.KeyAppInfo.Name())
	assertManifest(t, toolcards.Module(), "zeokeel-ui/toolcards",
		contracts.KeyUIRuntime.Name())
}

func assertManifest[T any](t *testing.T, descriptor *module.Descriptor[T], wantID string, wantRequires ...string) {
	t.Helper()
	if descriptor.ID != wantID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, wantID)
	}
	if descriptor.Factory == nil {
		t.Fatalf("module %s has a nil factory", wantID)
	}
	if len(descriptor.Provides) != 0 {
		t.Fatalf("UI module %s provides %v, want no provided services", wantID, descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		if requirement.Optional {
			t.Fatalf("module %s declares optional requirement %q; UI host services are mandatory", wantID, requirement.Name)
		}
		required[requirement.Name] = true
	}
	for _, want := range wantRequires {
		if !required[want] {
			t.Fatalf("module %s does not require %q; requires = %v", wantID, want, required)
		}
	}
}
