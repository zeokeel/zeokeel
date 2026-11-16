package plugins_test

// Shared headless harness for the Capabilities screen frame tests: the real
// host UI root, the module under test, and fake optional backends. The shell's
// tab pages and their cards are exercised in capability_test.go, skills_test.go
// and connectors_test.go; this file keeps the harness and the module metadata.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// --- fake backends ----------------------------------------------------------

type fakePlugins struct {
	mu           sync.Mutex
	mcp          []contracts.MCPPlugin
	refreshed    []contracts.MCPPlugin
	refreshCalls int
	// refreshStart/refreshRelease make Refresh block, so a test can prove the
	// page runs it off the UI thread and shows a busy state meanwhile.
	refreshStart   chan struct{}
	refreshRelease chan struct{}
}

func (f *fakePlugins) MCP() []contracts.MCPPlugin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *fakePlugins) Refresh(context.Context) []contracts.MCPPlugin {
	if f.refreshStart != nil {
		select {
		case f.refreshStart <- struct{}{}:
		default:
		}
	}
	if f.refreshRelease != nil {
		<-f.refreshRelease
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCalls++
	if f.refreshed != nil {
		f.mcp = append([]contracts.MCPPlugin(nil), f.refreshed...)
	}
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *fakePlugins) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshCalls
}

type fakeTool struct {
	name string
	desc string
}

func (f fakeTool) Name() string           { return f.name }
func (f fakeTool) Description() string    { return f.desc }
func (f fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (f fakeTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{}, nil
}

// newToolRegistry is a populated toolkit backend. The capability page no longer
// renders a tool catalogue, so the registry only proves that removing the UI did
// not remove the backend.
func newToolRegistry() *rigtools.Registry {
	return rigtools.NewRegistry(
		fakeTool{name: "read_file", desc: "读取文件内容"},
		fakeTool{name: "bash", desc: "运行 shell 命令"},
	)
}

// fakePluginSettings is the settings service the tabs and diagnostics write
// through. It keeps the last values and records the written keys.
type fakePluginSettings struct {
	mu     sync.Mutex
	values map[string]string
	setErr error
	sets   []string
}

func newFakePluginSettings(values map[string]string) *fakePluginSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakePluginSettings{values: values}
}

func (s *fakePluginSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakePluginSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.sets = append(s.sets, key)
	s.values[key] = value
	return nil
}

func (s *fakePluginSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *fakePluginSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

// --- harness ----------------------------------------------------------------

type harness struct {
	t      *testing.T
	uiRoot *uiroot.Root
	ctx    cordis.Context
}

func newHarness(t *testing.T, service contracts.PluginsService, toolkit *rigtools.Registry, cfg plugins.Config) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeyDataDir, t.TempDir())
	if service != nil {
		provide[contracts.PluginsService](t, ctx, contracts.KeyPlugins, service)
	}
	if toolkit != nil {
		provide(t, ctx, contracts.KeyToolkit, toolkit)
	}
	activate(t, ctx, plugins.Module(), cfg)
	return &harness{t: t, uiRoot: uiRoot, ctx: ctx}
}

// newHarnessWithSettings provides a settings store on top of the base harness.
func newHarnessWithSettings(t *testing.T, service contracts.PluginsService, toolkit *rigtools.Registry,
	cfg plugins.Config, settings contracts.SettingsService) *harness {
	t.Helper()
	h := newHarness(t, service, toolkit, cfg)
	provide(t, h.ctx, contracts.KeySettings, settings)
	return h
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	// Frames settle the native List's measurement passes, and advancing the
	// clock finishes a disclosure's opening animation: a control inside a
	// panel that is still animating has no bounds yet, so a click aimed at its
	// text would miss.
	tester.Frame()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
}

func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame unexpectedly shows %q; texts = %v", text, tester.Texts())
		}
	}
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// waitForText frames until want appears (async posted results land a frame
// after the goroutine finishes).
func waitForText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		if tester.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; texts = %v", want, tester.Texts())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type descriptorPlugin[T any] struct {
	descriptor *module.Descriptor[T]
	config     T
	inject     []string
}

func (p *descriptorPlugin[T]) Name() string { return p.descriptor.ID }

func (p *descriptorPlugin[T]) Inject() []string { return p.inject }

func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

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

// --- module metadata --------------------------------------------------------

func TestModuleMetadata(t *testing.T) {
	if plugins.ModuleID != "zeokeel-ui/plugins" {
		t.Fatalf("ModuleID = %q", plugins.ModuleID)
	}
	if plugins.ScreenID != "plugins" {
		t.Fatalf("ScreenID = %q", plugins.ScreenID)
	}
	if plugins.Module().ID != plugins.ModuleID {
		t.Fatalf("descriptor ID = %q", plugins.Module().ID)
	}
}
