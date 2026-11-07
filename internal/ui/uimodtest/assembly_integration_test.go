package uimodtest

// Product-level integration test: it loads the real embedded desktop assembly
// through internal/assembly (the same registry and assembly.json the shipped
// product uses) and asserts the demo modules appear in the real product frame.
//
// The kernel-harness tests in integration_test.go isolate one module at a time;
// this one is the end-to-end proof that assembly registration, kernel pre-flight
// and the UI frame all agree on the new modules. It never edits the assembly
// package: it only consumes its public Load API.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// memSettings is the SettingsService the process host provides in the real
// product; the test keeps it in memory.
type memSettings struct{ values map[string]string }

func (s *memSettings) Get(key, fallback string) string {
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *memSettings) Set(key, value string) error { s.values[key] = value; return nil }

func (s *memSettings) All() map[string]string { return s.values }

// discardLogger is the Logger the process host provides in the real product.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

func TestDesktopProductAssemblyShowsAllDemoModules(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	// The product host provides the UI and process services before loading.
	provide(t, root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, root, contracts.KeyTheme, uiRoot.Theme())
	provide(t, root, contracts.KeyLocale, uiRoot.Locale())
	provide(t, root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	provide(t, root, contracts.KeySettings, contracts.SettingsService(&memSettings{values: map[string]string{}}))
	provide(t, root, contracts.KeyLogger, contracts.Logger(discardLogger{}))

	loader, err := assembly.Load(root, assembly.ProductDesktop)
	if err != nil {
		t.Fatalf("assembly.Load(%s): %v", assembly.ProductDesktop, err)
	}
	defer func() { _ = loader.Close() }()

	// Every demo module is Active in the real desktop product.
	for _, entryID := range []string{"appinfo", "store", "approvals", "workspace", "terminal", "skills", "memory", "toolkit", "hosttools", "agent", "ui", "home", "toolcards", "about", "sidebar", "session", "settings", "approval", "rightpanel"} {
		instance, ok := loader.Instance(entryID)
		if !ok {
			t.Fatalf("desktop assembly has no instance %q; instances = %v", entryID, instanceIDs(loader))
		}
		if state := instance.State(); state != cordis.Active {
			t.Fatalf("instance %q state = %s, want Active", entryID, state)
		}
	}

	tester := ui.NewTester(uiRoot.View, 1200, 800)
	// Home screen + the brand sidebar controls.
	for _, want := range []string{
		"首页",
		"新对话",
		"开始新对话",
	} {
		if !tester.HasText(want) {
			t.Fatalf("desktop frame does not show %q; texts = %v", want, tester.Texts())
		}
	}

	// about's settings page and settings.header extension are visible in the
	// settings surface.
	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	// Version comes from the product assembly's appinfo entry ("dev" here), so
	// the assertion checks the header's shape rather than one build number.
	if !tester.HasText("构建信息") || !tester.HasText("ZeoKeel v") {
		t.Fatalf("settings frame does not show about's page/header; texts = %v", tester.Texts())
	}
	uiRoot.Navigator().CloseSettings()

	// Closing the loader runs every module's effect cleanups: the contributions
	// are gone from the next frame.
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	tester.Frame()
	if tester.HasText("开始新对话") {
		t.Fatalf("module contributions survived loader.Close; texts = %v", tester.Texts())
	}
}

func instanceIDs(loader *module.Loader) []string {
	ids := make([]string, 0)
	for _, instance := range loader.Instances() {
		ids = append(ids, instance.ID())
	}
	return ids
}
