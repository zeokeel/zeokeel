package plugins_test

// Headless tests for the Plugins screen: the MCP plugin cards with their
// status/refresh, and the searchable category-grouped tool catalogue. Backends
// are optional; missing ones render per-section empty states.

import (
	"context"
	"strings"
	"sync"
	"testing"

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
}

func (f *fakePlugins) MCP() []contracts.MCPPlugin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.MCPPlugin(nil), f.mcp...)
}

func (f *fakePlugins) Refresh(context.Context) []contracts.MCPPlugin {
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

func newToolRegistry() *rigtools.Registry {
	return rigtools.NewRegistry(
		fakeTool{name: "read_file", desc: "读取文件内容"},
		fakeTool{name: "bash", desc: "运行 shell 命令"},
		fakeTool{name: "web_fetch", desc: "抓取网页"},
		fakeTool{name: "memory_search", desc: "搜索记忆"},
		fakeTool{name: "team_task_create", desc: "创建团队任务"},
		fakeTool{name: "odd_tool", desc: "其他工具"},
	)
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
	if service != nil {
		provide[contracts.PluginsService](t, ctx, contracts.KeyPlugins, service)
	}
	if toolkit != nil {
		provide(t, ctx, contracts.KeyToolkit, toolkit)
	}
	activate(t, ctx, plugins.Module(), cfg)
	return &harness{t: t, uiRoot: uiRoot, ctx: ctx}
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
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

// --- tests ------------------------------------------------------------------

func TestSectionsWithoutBackends(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester,
		"插件", "查看已连接的 MCP 服务器与产品内置的工具目录。",
		"MCP 插件", "插件服务未启用",
		"工具目录", "工具目录未启用")
	h.assertNoTexts(tester, "刷新")
}

func TestMCPListRenders(t *testing.T) {
	service := &fakePlugins{mcp: []contracts.MCPPlugin{
		{
			Name: "files", URL: "http://127.0.0.1:9000/mcp",
			ServerName: "fake-files", Version: "0.1.0", Protocol: "2024-11-05",
			Tools: 3, Status: "已连接",
		},
		{Name: "broken", Status: "连接失败", Tools: 0},
	}}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()

	h.assertTexts(tester,
		"MCP 插件", "刷新",
		"files", "已连接", "3 个工具",
		"fake-files · 0.1.0 · 2024-11-05", "http://127.0.0.1:9000/mcp",
		"broken", "连接失败")
}

func TestMCPEmptyState(t *testing.T) {
	service := &fakePlugins{}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "还没有配置 MCP 服务器（assembly 的 mcpServers）")
}

func TestMCPRefreshUpdates(t *testing.T) {
	service := &fakePlugins{
		mcp:       []contracts.MCPPlugin{{Name: "old-server", Status: "已连接"}},
		refreshed: []contracts.MCPPlugin{{Name: "new-server", Status: "已连接", Tools: 2}},
	}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "old-server")

	h.click(tester, "刷新")
	if calls := service.calls(); calls != 1 {
		t.Fatalf("Refresh calls = %d, want 1", calls)
	}
	h.assertTexts(tester, "new-server", "2 个工具")
	h.assertNoTexts(tester, "old-server")
}

func TestToolCatalogRenders(t *testing.T) {
	h := newHarness(t, nil, newToolRegistry(), plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester,
		"工具目录", "6 个工具", "搜索工具",
		"文件", "read_file", "读取文件内容",
		"终端与任务", "bash", "运行 shell 命令",
		"网络", "web_fetch", "抓取网页",
		"会话与记忆", "memory_search", "搜索记忆",
		"团队与编排", "team_task_create", "创建团队任务",
		"其他", "odd_tool", "其他工具")
	h.assertNoTexts(tester, "工具目录未启用", "没有匹配的工具")
}

func TestToolSearchFiltersNameAndDescription(t *testing.T) {
	h := newHarness(t, nil, newToolRegistry(), plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "read_file", "web_fetch")

	h.click(tester, "搜索工具")
	tester.Type("web")
	tester.Frame()
	h.assertTexts(tester, "web_fetch", "网络")
	h.assertNoTexts(tester, "read_file", "bash", "团队与编排")

	// A description match also narrows the catalogue.
	h.click(tester, "搜索工具")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("抓取")
	tester.Frame()
	h.assertTexts(tester, "web_fetch")
	h.assertNoTexts(tester, "read_file")

	// No match shows the empty copy.
	h.click(tester, "搜索工具")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的工具")

	// Clearing restores the whole catalogue.
	h.click(tester, "搜索工具")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.assertTexts(tester, "read_file", "bash", "web_fetch")
}

func TestCustomConfig(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{
		Title:        "Plugins",
		Description:  "Custom description",
		MCPSection:   "MCP servers",
		ToolsSection: "Tools",
	})
	tester := h.tester()
	h.assertTexts(tester, "Plugins", "Custom description", "MCP servers", "Tools")
}

func TestModuleMetadata(t *testing.T) {
	if plugins.ModuleID != "zeokeel-ui/plugins" {
		t.Fatalf("ModuleID = %q", plugins.ModuleID)
	}
	if plugins.ScreenID != "plugins" {
		t.Fatalf("ScreenID = %q", plugins.ScreenID)
	}
	if strings.TrimSpace(plugins.Module().ID) != plugins.ModuleID {
		t.Fatalf("descriptor ID = %q", plugins.Module().ID)
	}
}
