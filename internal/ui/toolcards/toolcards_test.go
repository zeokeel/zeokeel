package toolcards_test

// Headless tests for the real tool cards: every registered name renders through
// UIRuntime.RenderTool, status badges use the documented labels, summaries are
// native collapsibles and the demo card keeps its historical output.

import (
	"strings"
	"time"

	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
	"github.com/zeokeel/zeokeel/internal/uikit"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// requiredToolNames mirrors the coverage list in the module documentation.
var requiredToolNames = []string{
	"bash", "read_file", "write_file", "edit_file", "str_replace_editor",
	"list_dir", "glob", "grep", "read_image", "web_fetch", "web_search",
	"job_start", "job_output", "terminal_send", "terminal_read",
	"enter_worktree", "list_worktrees", "subagent", "workflow", "ralph",
	"team_task_create", "team_task_update", "todo", "skill", "plan_review",
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
		if name == "plan_review" {
			// The plan card is titled with its localized product name.
			if !tester.HasText("计划审阅") {
				t.Fatalf("frame does not render the plan card; texts = %v", tester.Texts())
			}
			continue
		}
		// The visible copy is the friendly localized tool name; the raw protocol
		// id survives only as the row's tooltip/AT description.
		friendly := uikit.ToolDisplayName(h.ui.Locale(), name)
		if !tester.HasText(friendly) {
			t.Fatalf("frame does not render %q as %q; texts = %v", name, friendly, tester.Texts())
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
		"ok")
	h.assertNoTexts(tester, "demo-1")
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
	h.assertTexts(tester, "运行中", "失败", "完成")
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
	// H15: a short result starts expanded; the row is one official disclosure
	// named by the friendly tool name (no separate 详情 button).
	h.assertTexts(tester, "运行命令", "完成", summary)
	h.assertNoTexts(tester, "b1", "bash", "详情") // call ID and raw name are hidden
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok || !info.Expanded {
		t.Fatalf("summary disclosure: %+v found %v; texts = %v", info, ok, tester.Texts())
	}
	if info.Description != "bash" {
		t.Fatalf("summary row description = %q, want the raw tool id", info.Description)
	}

	h.click(tester, "运行命令")
	tester.Advance(300 * time.Millisecond) // the 200 ms close animation
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); info.Expanded {
		t.Fatalf("click did not fold the summary: %+v", info)
	}

	tester.Key(0, ui.KeySpace)
	tester.Frame()
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成"); !info.Expanded {
		t.Fatalf("Space did not unfold the summary: %+v", info)
	}
}

func TestDisposeRemovesCards(t *testing.T) {
	h := newHarness(t)
	fiber := activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	matched := map[string]bool{}
	h.showCalls([]contracts.ToolCall{{ID: "b1", Name: "bash", Status: "ok"}}, matched, nil)
	tester := h.tester()
	tester.Frame()
	if !matched["bash"] || !tester.HasText("运行命令") {
		t.Fatalf("bash card missing before dispose; texts = %v", tester.Texts())
	}

	if err := fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	tester.Frame()
	if matched["bash"] {
		t.Fatal("RenderTool still matched after module disposal")
	}
	if tester.HasText("运行命令") {
		t.Fatalf("card still rendered after disposal; texts = %v", tester.Texts())
	}
}

// TestCancelledStatusIsNeutral covers S-05 for the registered tool cards: a
// user-cancelled call reads 已取消 in the neutral color and never 失败.
func TestCancelledStatusIsNeutral(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{
		{ID: "c1", Name: "bash", Status: "cancelled"},
		{ID: "f1", Name: "bash", Status: "failed"},
	}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "已取消", "失败")
	h.assertNoTexts(tester, "cancelled")
}

// TestShortSummaryRenderedOnce covers S-12 for the registered cards: a short
// result starts expanded (H15), so the open panel is the one place its text
// reads; the trigger must not repeat it as a preview.
func TestShortSummaryRenderedOnce(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	summary := "stdout: 全部测试通过"
	h.showCalls([]contracts.ToolCall{{ID: "b1", Name: "bash", Status: "ok", Summary: summary}}, nil, nil)
	tester := h.tester()
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok || !info.Expanded {
		t.Fatalf("short result disclosure: %+v found %v", info, ok)
	}
	count := 0
	for _, text := range tester.Texts() {
		if strings.Contains(text, summary) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("short summary rendered %d times, want once; texts = %v", count, tester.Texts())
	}
}

// --- Batch B: tool input/output visibility ---------------------------------

// TestBashCardShowsInputOutputAndCopies covers S-02/S-03/S-20: a bash card's
// expanded panel shows the command it ran and the full bounded output, each
// copyable.
func TestBashCardShowsInputOutputAndCopies(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{{
		ID: "b1", Name: "bash", Status: "ok",
		Arguments: `{"command":"go test ./internal/ui/toolcards -count=1"}`,
		Summary:   "ok  \nok\nFAIL internal/ui/session",
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "运行命令", "命令", "输出",
		"go test ./internal/ui/toolcards -count=1", "FAIL internal/ui/session")
	h.click(tester, "复制命令")
	if got := tester.Clipboard(); got != "go test ./internal/ui/toolcards -count=1" {
		t.Fatalf("clipboard = %q, want the command", got)
	}
	h.click(tester, "复制输出")
	if got := tester.Clipboard(); got != "ok  \nok\nFAIL internal/ui/session" {
		t.Fatalf("clipboard = %q, want the tool output", got)
	}
}

// TestEditCardShowsBeforeAfter covers S-02 for edits: the panel shows the path
// and both sides of the replacement.
func TestEditCardShowsBeforeAfter(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{{
		ID: "e1", Name: "edit_file", Status: "ok",
		Arguments: `{"path":"internal/app.go","old_string":"return nil","new_string":"return err"}`,
		Summary:   "edited internal/app.go",
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "编辑文件", "路径", "替换前", "替换后",
		"internal/app.go", "return nil", "return err")
}

// TestGenericToolInputPrettyJSON covers the fallback input of a tool without a
// specialized renderer: arguments read as indented JSON.
func TestGenericToolInputPrettyJSON(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	h.showCalls([]contracts.ToolCall{{
		ID: "g1", Name: "grep", Status: "ok",
		Arguments: `{"pattern":"func main","path":"cmd"}`,
		Summary:   "cmd/main.go:3:func main()",
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "输入", "pattern", "func main")
}

// TestTruncatedOutputShowsHint covers S-03's truncation notice: a read-model
// summary cut at the backend bound says so instead of pretending it is whole.
func TestTruncatedOutputShowsHint(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	summary := strings.Repeat("x", 3999) + "…"
	h.showCalls([]contracts.ToolCall{{
		ID: "t1", Name: "bash", Status: "ok",
		Arguments: `{"command":"seq 1 400"}`,
		Summary:   summary,
	}}, nil, nil)
	tester := h.tester()
	// The long output starts folded (H15): open the row to read the panel.
	h.click(tester, "运行命令")
	h.assertTexts(tester, "输出已截断，仅显示前部分")
}

// TestTeamToolFooterRunsInspectorCommand covers the cross-navigation footer:
// a subagent card opens the inspector's team pane through the stable command,
// and a host without the command shows no footer action at all.
func TestTeamToolFooterRunsInspectorCommand(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	ran := 0
	if _, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    "rightpanel.team.open",
		Title: "打开团队面板",
		Run:   func() error { ran++; return nil },
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	h.showCalls([]contracts.ToolCall{{
		ID: "s1", Name: "subagent", Status: "ok",
		Arguments: `{"prompt":"总结当前工作目录"}`,
		Summary:   "子代理已完成",
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "在团队中查看")
	h.click(tester, "在团队中查看")
	if ran != 1 {
		t.Fatalf("team command runs = %d, want 1", ran)
	}

	// No command registered: the action is not shown (never a dead button).
	plain := newHarness(t)
	activate(t, plain.ctx, toolcards.Module(), toolcards.Config{})
	plain.showCalls([]contracts.ToolCall{{
		ID: "s2", Name: "subagent", Status: "ok", Summary: "子代理已完成",
	}}, nil, nil)
	if plain.tester().HasText("在团队中查看") {
		t.Fatal("team footer shown without the inspector command")
	}
}
