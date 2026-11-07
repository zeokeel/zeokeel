package toolcards_test

// Headless tests for the real tool cards: every registered name renders through
// UIRuntime.RenderTool, status badges use the documented labels, summaries are
// native collapsibles and the demo card keeps its historical output.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// requiredToolNames mirrors the coverage list in the module documentation.
var requiredToolNames = []string{
	"bash", "read_file", "write_file", "edit_file", "str_replace_editor",
	"list_dir", "glob", "grep", "read_image", "web_fetch", "web_search",
	"job_start", "job_output", "terminal_send", "terminal_read",
	"enter_worktree", "list_worktrees", "subagent", "workflow", "ralph",
	"team_task_create", "team_task_update", "todo", "skill",
}

type harness struct {
	t   *testing.T
	ctx cordis.Context
	ui  *uiroot.Root
}

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
	return &harness{t: t, ctx: ctx, ui: uiRoot}
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.ui.View, 1200, 800)
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
			h.t.Fatalf("frame still shows %q; texts = %v", text, tester.Texts())
		}
	}
}

// showCalls registers a test screen that renders every call through the runtime
// tool-renderer injection point. It records whether a renderer matched.
func (h *harness) showCalls(calls []contracts.ToolCall, matched map[string]bool, unknownMatch *bool) {
	h.t.Helper()
	_, err := h.ui.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "test.cards",
		Title: "工具卡片测试",
		Build: func(c *ui.Context) {
			for _, call := range calls {
				ok := h.ui.Runtime().RenderTool(c, call)
				if matched != nil {
					matched[call.Name] = ok
				}
			}
			if unknownMatch != nil {
				*unknownMatch = h.ui.Runtime().RenderTool(c, contracts.ToolCall{
					ID: "unknown-1", Name: "totally_unknown_tool", Status: "ok",
				})
			}
		},
	})
	if err != nil {
		h.t.Fatalf("register test screen: %v", err)
	}
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

func activate(t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[toolcards.Config], config toolcards.Config) *cordis.Fiber {
	t.Helper()
	plugin := &descriptorPlugin{descriptor: descriptor, config: config}
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

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

type descriptorPlugin struct {
	descriptor *module.Descriptor[toolcards.Config]
	config     toolcards.Config
	inject     []string
}

func (p *descriptorPlugin) Name() string { return p.descriptor.ID }

func (p *descriptorPlugin) Inject() []string { return p.inject }

func (p *descriptorPlugin) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

func TestToolNameCoverage(t *testing.T) {
	names := toolcards.ToolNames()
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" {
			t.Fatal("ToolNames contains an empty name")
		}
		if seen[name] {
			t.Fatalf("ToolNames repeats %q", name)
		}
		seen[name] = true
	}
	if len(names) != len(toolcards.RealToolNames())+1 {
		t.Fatalf("ToolNames = %d names, want real + demo = %d", len(names), len(toolcards.RealToolNames())+1)
	}
	for _, required := range requiredToolNames {
		if !seen[required] {
			t.Fatalf("required tool %q has no card; ToolNames = %v", required, names)
		}
	}
	if len(requiredToolNames) != len(toolcards.RealToolNames()) {
		t.Fatalf("required list = %d, real cards = %d; update the docs/tests together",
			len(requiredToolNames), len(toolcards.RealToolNames()))
	}
}

func TestEveryToolNameHasRenderer(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})

	names := toolcards.ToolNames()
	calls := make([]contracts.ToolCall, 0, len(names))
	for _, name := range names {
		calls = append(calls, contracts.ToolCall{
			ID: "id-" + name, Name: name, Status: "ok",
		})
	}
	matched := make(map[string]bool, len(names))
	unknownMatch := true
	h.showCalls(calls, matched, &unknownMatch)
	tester := h.tester()
	tester.Frame()

	for _, name := range names {
		if !matched[name] {
			t.Fatalf("RenderTool reported no renderer for %q", name)
		}
		if !tester.HasText(name) {
			t.Fatalf("frame does not render a card for %q; texts = %v", name, tester.Texts())
		}
	}
	if unknownMatch {
		t.Fatal("RenderTool matched a tool name this module does not register")
	}
}

func TestDemoCardPreserved(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{{
		ID:      "demo-1",
		Name:    "demo",
		Status:  "ok",
		Summary: "示例工具调用：由 ui/toolcards 模块渲染",
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester,
		"工具调用: demo",
		"示例工具调用：由 ui/toolcards 模块渲染",
		"demo-1",
		"ok")
}

func TestStatusBadgesUseThemeSemantics(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{
		{ID: "b1", Name: "bash", Status: "running"},
		{ID: "r1", Name: "read_file", Status: "error"},
		{ID: "w1", Name: "write_file", Status: "ok"},
	}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "运行中", "失败", "完成", "shell", "文件")
	h.assertNoTexts(tester, "running", "error") // labels replace raw statuses
}

func TestSummaryCollapsible(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	summary := "stdout: 全部测试通过\nstderr: (空)"
	h.showCalls([]contracts.ToolCall{{
		ID: "b1", Name: "bash", Status: "ok", Summary: summary,
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "摘要", "bash", "b1", "完成")
	h.assertNoTexts(tester, summary) // collapsed by default

	h.click(tester, "摘要")
	h.assertTexts(tester, summary)

	h.click(tester, "摘要")
	h.assertNoTexts(tester, summary)
}

func TestDisposeRemovesCards(t *testing.T) {
	h := newHarness(t)
	fiber := activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	matched := map[string]bool{}
	h.showCalls([]contracts.ToolCall{{ID: "b1", Name: "bash", Status: "ok"}}, matched, nil)
	tester := h.tester()
	tester.Frame()
	if !matched["bash"] || !tester.HasText("bash") {
		t.Fatalf("bash card missing before dispose; texts = %v", tester.Texts())
	}

	if err := fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	tester.Frame()
	if matched["bash"] {
		t.Fatal("RenderTool still matched after module disposal")
	}
	if tester.HasText("bash") {
		t.Fatalf("card still rendered after disposal; texts = %v", tester.Texts())
	}
}
