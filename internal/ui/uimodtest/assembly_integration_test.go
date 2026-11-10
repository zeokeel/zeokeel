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
	for _, entryID := range []string{"appinfo", "store", "approvals", "workspace", "terminal", "skills", "memory", "toolkit", "hosttools", "agent", "automation", "update", "importer", "modelcatalog", "ui", "home", "toolcards", "about", "sidebar", "session", "settings", "approval", "rightpanel", "automation-ui", "plugins-ui"} {
		instance, ok := loader.Instance(entryID)
		if !ok {
			t.Fatalf("desktop assembly has no instance %q; instances = %v", entryID, instanceIDs(loader))
		}
		if state := instance.State(); state != cordis.Active {
			t.Fatalf("instance %q state = %s, want Active", entryID, state)
		}
	}

	tester := ui.NewTester(uiRoot.View, 1200, 800)
	// The product lands on the session screen (the DSH shell): its empty
	// conversation hero, the brand sidebar's new-chat action and the header's
	// agent-team entry are the demo modules' visible contributions.
	for _, want := range []string{
		"开始新对话",
		"还没有消息，开始一段新对话吧。",
		"新对话",
		"智能体团队",
	} {
		if !tester.HasText(want) {
			t.Fatalf("desktop frame does not show %q; texts = %v", want, tester.Texts())
		}
	}

	// about's settings page is visible in the settings surface with its build
	// information section (the build identity lives on the page, not in a
	// header above every page). The grouped navigation starts on the first
	// general business page, so open About through the host's page deep link.
	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	if err := uiRoot.OpenSettingsPage("about"); err != nil {
		t.Fatalf("open about settings page: %v", err)
	}
	tester.Frame()
	if !tester.HasText("构建信息") {
		t.Fatalf("settings frame does not show about's page; texts = %v", tester.Texts())
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
