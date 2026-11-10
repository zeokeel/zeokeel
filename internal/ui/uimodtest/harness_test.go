package uimodtest

// Shared headless test harness for the demo UI modules.
//
// It builds the same stack the product does, minus the window: a kernel root
// with the host services provided (uiroot), the product appinfo backend module, and a
// small adapter that activates a module.Descriptor through the kernel plugin
// interfaces. Frames are built by mygo's in-memory Tester (ui.NewTester), which
// runs Root.View without a display, and assertions read the text the user would
// see — the same technique the later module ports use.
//
// This directory contains only _test.go files: it is a test-only package and is
// never linked into the product.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/appinfo"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// clickRightSidebarPage clicks one of the right sidebar's fixed page tabs
// (开始/文件/终端). The sidebar must already be open; opening it is the host's
// job (the header toggle, the inspector shortcut or SetRightPanelOpen).
func clickRightSidebarPage(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	tester.Frame()
	if err := tester.Click(label); err != nil {
		t.Fatalf("open right sidebar page %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// sidebarGuideVisible reports whether the right sidebar shows its 开始 guide:
// the landing of the new DSH-aligned sidebar with no page opened.
func sidebarGuideVisible(tester *ui.Tester) bool {
	return tester.HasText("工作区文件") && tester.HasText("命令运行")
}

// requireRightSidebarGuide fails unless the right sidebar's 开始 guide is
// showing (the new docked inspector content).
func requireRightSidebarGuide(t *testing.T, tester *ui.Tester, where string) {
	t.Helper()
	if !sidebarGuideVisible(tester) {
		t.Fatalf("%s: right sidebar 开始 guide missing; texts = %v", where, tester.Texts())
	}
}

// harness is one headless product: kernel root, uiroot services and product appinfo.
type harness struct {
	t   *testing.T
	ctx cordis.Context
	ui  *uiroot.Root
}

// newHarness starts a headless product with only the host services and the
// product appinfo backend active, exactly like the desktop assembly does before any UI
// module loads.
func newHarness(t *testing.T) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())

	h := &harness{t: t, ctx: ctx, ui: uiRoot}
	activate(t, ctx, appinfo.Module(), appinfo.Config{
		Name:      "ZeoKeel",
		Version:   "0.1.0",
		Commit:    "test-commit",
		BuildTime: "2026-01-01T00:00:00Z",
	})
	return h
}

// tester builds one headless mygo frame of the product's single view function.
func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.ui.View, 1200, 800)
}

// assertTexts checks that every wanted string is visible in the current frame.
func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

// assertNoTexts checks that none of the unwanted strings is visible.
func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame still shows %q; texts = %v", text, tester.Texts())
		}
	}
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// descriptorPlugin adapts a module.Descriptor to the kernel plugin interfaces.
// The module layer has the same adapter internally (module/load.go), but it is
// unexported and needs the assembly's pre-flight; the tests drive one module at
// a time through the kernel directly.
type descriptorPlugin[T any] struct {
	descriptor *module.Descriptor[T]
	config     T
	inject     []string
}

func (p *descriptorPlugin[T]) Name() string { return p.descriptor.ID }

// Inject implements cordis.PluginInjector: the module's required services gate
// activation, exactly as the assembly loader does.
func (p *descriptorPlugin[T]) Inject() []string { return p.inject }

// Activate implements cordis.Plugin.
func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

// activate starts one module like the assembly would and fails the test when it
// is not Active (a Pending or Failed module would make every frame assertion
// misleading).
func activate[T any](t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[T], config T) *cordis.Fiber {
	t.Helper()
	plugin := &descriptorPlugin[T]{descriptor: descriptor, config: config}
	inject := make([]string, 0, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		if !requirement.Optional {
			inject = append(inject, requirement.Name)
		}
	}
	plugin.inject = inject
	fiber, err := ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
	return fiber
}
