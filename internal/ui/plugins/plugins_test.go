package plugins_test

// Headless tests for the Plugins screen: the MCP plugin cards with their
// status/refresh, and the searchable category-grouped tool catalogue. Backends
// are optional; missing ones render per-section empty states.

import (
	"context"
	"strings"
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

	// Collapsed cards show name, status and tool count only.
	h.assertTexts(tester,
		"MCP 插件", "刷新",
		"files", "已连接", "3 个工具", "详情",
		"broken", "连接失败")
	h.assertNoTexts(tester, "fake-files", "http://127.0.0.1:9000/mcp")

	// Expanding the first card reveals the connection details.
	h.click(tester, "详情")
	h.assertTexts(tester, "fake-files", "0.1.0", "2024-11-05",
		"http://127.0.0.1:9000/mcp")
}

func TestMCPEmptyState(t *testing.T) {
	service := &fakePlugins{}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	// The empty state is user-facing: no internal config term (the audit caught
	// "assembly 的 mcpServers"), and it offers the page that adds servers.
	h.assertTexts(tester, "还没有配置 MCP 服务器", "打开设置中的 MCP 页")
	h.assertNoTexts(tester, "assembly", "mcpServers")
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
	deadline := time.Now().Add(2 * time.Second)
	for service.calls() != 1 && time.Now().Before(deadline) {
		tester.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	if calls := service.calls(); calls != 1 {
		t.Fatalf("Refresh calls = %d, want 1", calls)
	}
	waitForPluginText(t, tester, "new-server")
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

// --- H12: failed connections, pending restarts, scannable descriptions -------

func TestMCPFailedRowOffersInlineRetry(t *testing.T) {
	service := &fakePlugins{
		mcp: []contracts.MCPPlugin{{
			Name: "broken", Status: "连接失败: dial tcp 127.0.0.1:9: connection refused", Tools: 0,
		}},
		refreshed: []contracts.MCPPlugin{{Name: "broken", Status: "已连接", Tools: 2}},
	}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "broken", "连接失败", "重试连接")
	// The failure cause is localized; the raw network error is not in the row.
	h.assertTexts(tester, "无法连接到服务器")
	h.assertNoTexts(tester, "dial tcp 127.0.0.1:9: connection refused")

	h.click(tester, "重试连接")
	// The retry runs off the UI thread: wait for the posted result.
	waitForPluginText(t, tester, "已连接")
	if calls := service.calls(); calls != 1 {
		t.Fatalf("Refresh calls = %d, want the inline retry's one", calls)
	}
	h.assertTexts(tester, "2 个工具")
}

func TestToolSwitchShowsPendingRestart(t *testing.T) {
	store := newFakePluginSettings(nil)
	h := newHarnessWithSettings(t, nil, newToolRegistry(), plugins.Config{}, store)
	tester := h.tester()
	h.assertNoTexts(tester, "待重启")

	h.click(tester, "启用工具 read_file")
	h.assertTexts(tester, "待重启")
	h.assertForTextCount(tester, "待重启", 1)
}

func TestToolDescriptionIsScannableAndKeepsFullTextInTooltip(t *testing.T) {
	long := "Read a UTF-8 text file inside the tool root and return numbered lines. " +
		"Use offset (1-based line) and limit (max lines) to page through it. " +
		"This sentence is documentation the row should not show in full."
	registry := rigtools.NewRegistry(fakeTool{name: "read_file", desc: long})
	h := newHarness(t, nil, registry, plugins.Config{})
	tester := h.tester()
	// A Chinese UI shows the product summary; the English documentation is the
	// row tooltip only, never a second line in the list.
	h.assertTexts(tester, "读取工作区文件内容")
	h.assertNoTexts(tester, "Read a UTF-8 text file inside the tool root",
		"This sentence is documentation the row should not show in full")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Read a UTF-8 text file inside the tool root and return numbered lines")
	h.assertNoTexts(tester, "This sentence is documentation the row should not show in full")
}

// waitForPluginText drives frames until the async refresh result is visible.
func waitForPluginText(t *testing.T, tester *ui.Tester, want string) {
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

// assertForTextCount checks how many text nodes contain want.
func (h *harness) assertForTextCount(tester *ui.Tester, want string, count int) {
	h.t.Helper()
	got := 0
	for _, text := range tester.Texts() {
		if strings.Contains(text, want) {
			got++
		}
	}
	if got != count {
		h.t.Fatalf("%q shows %d times, want %d; texts = %v", want, got, count, tester.Texts())
	}
}

// --- second audit batch: async MCP re-connect, localized causes, filter ------

// TestMCPRefreshRunsOffUIThread pins E-14: Refresh can block on a hung server, so
// the page shows a busy state and keeps rendering until the posted result lands.
func TestMCPRefreshRunsOffUIThread(t *testing.T) {
	service := &fakePlugins{
		mcp:            []contracts.MCPPlugin{{Name: "old", Status: "已连接", Tools: 1}},
		refreshed:      []contracts.MCPPlugin{{Name: "new", Status: "已连接", Tools: 2}},
		refreshStart:   make(chan struct{}, 1),
		refreshRelease: make(chan struct{}),
	}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "刷新")

	h.click(tester, "刷新")
	select {
	case <-service.refreshStart:
	case <-time.After(2 * time.Second):
		t.Fatal("Refresh did not start")
	}
	// The frame keeps rendering while the backend call is blocked.
	h.assertTexts(tester, "正在重连…")
	h.assertNoTexts(tester, "已刷新")

	close(service.refreshRelease)
	waitForPluginText(t, tester, "已刷新 1 个 MCP 服务器")
	h.assertTexts(tester, "刷新", "new", "2 个工具")
	h.assertNoTexts(tester, "正在重连…", "old")
}

// TestMCPFailureCausesAreLocalized pins E-15: the collapsed row names the cause
// in product language for timeout/auth/handshake/connect failures, in both
// languages, and never echoes the raw English error.
func TestMCPFailureCausesAreLocalized(t *testing.T) {
	service := &fakePlugins{mcp: []contracts.MCPPlugin{
		{Name: "connect", Status: "连接失败: dial tcp 10.0.0.1:1: connect: connection refused"},
		{Name: "timeout", Status: "连接失败: context deadline exceeded"},
		{Name: "auth", Status: "连接失败: 401 Unauthorized"},
		{Name: "protocol", Status: "连接失败: mcp: initialize: unexpected EOF"},
	}}
	h := newHarness(t, service, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "无法连接到服务器", "连接超时（服务器未及时响应）",
		"鉴权失败（请检查访问令牌）", "协议握手失败（MCP 初始化未完成）")
	h.assertNoTexts(tester, "dial tcp", "deadline exceeded", "401 Unauthorized",
		"unexpected EOF", "mcp: initialize")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Could not reach the server",
		"Connection timed out (the server did not answer in time)",
		"Authentication failed (check the access token)",
		"Protocol handshake failed (MCP initialization did not complete)")
}

// TestToolCatalogDisabledOnlyFilter pins E-29's filter: a 70-row list of switches
// needs a view of only the tools that are turned off.
func TestToolCatalogDisabledOnlyFilter(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingToolsDisabled: `["read_file"]`,
	})
	h := newHarnessWithSettings(t, nil, newToolRegistry(), plugins.Config{}, store)
	tester := h.tester()
	h.assertTexts(tester, "仅显示已停用", "6 个工具")

	h.click(tester, "仅显示已停用")
	h.assertTexts(tester, "read_file", "1 个工具")
	h.assertNoTexts(tester, "bash", "web_fetch", "memory_search")

	h.click(tester, "仅显示已停用")
	h.assertTexts(tester, "read_file", "bash", "6 个工具")
}

// newEnglishDocToolRegistry is a catalogue whose model-facing descriptions are
// English documentation, as the product's real built-in tools are: the page
// shows a localized one-line summary for each, so the search must match the
// visible copy, not the hidden documentation.
func newEnglishDocToolRegistry() *rigtools.Registry {
	return rigtools.NewRegistry(
		fakeTool{name: "memory", desc: "Manage the agent's memory entries through a host-provided store."},
		fakeTool{name: "team", desc: "Manage the session's team members (in-memory manager)."},
		fakeTool{name: "read_file", desc: "Read a file from the workspace."},
	)
}

// TestToolSearchMatchesVisibleLocalizedCopy covers the search haystack: it
// matches the name, the localized summary the row shows and the category label,
// and never the hidden English description. "memory" used to hit "team" through
// "in-memory manager" while the team row showed an unrelated Chinese summary.
func TestToolSearchMatchesVisibleLocalizedCopy(t *testing.T) {
	h := newHarness(t, nil, newEnglishDocToolRegistry(), plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "3 个工具", "记忆读写与搜索", "创建团队任务")

	// A Chinese query matches the visible localized summary.
	h.click(tester, "搜索工具")
	tester.Type("记忆")
	tester.Frame()
	h.assertTexts(tester, "1 个工具", "记忆读写与搜索")
	h.assertNoTexts(tester, "创建团队任务", "team")

	// The hidden English documentation is no longer searchable.
	h.click(tester, "搜索工具")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("memory")
	tester.Frame()
	h.assertTexts(tester, "1 个工具", "memory")
	h.assertNoTexts(tester, "创建团队任务", "team")

	// An English name still matches, as does a category label.
	h.click(tester, "搜索工具")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("read_file")
	tester.Frame()
	h.assertTexts(tester, "1 个工具", "read_file")
}
