package toolcards_test

// Headless tests for the real tool cards: every registered name renders through
// UIRuntime.RenderTool, status badges use the documented labels, summaries are
// native collapsibles and the demo card keeps its historical output.

import (
	"fmt"
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

// TestFoldStateMapIsBounded covers S-07: the per-card disclosure state must not
// grow for the lifetime of the process (one long agent run can render thousands
// of calls). Past the bound the least recently rendered cards are evicted and
// fall back to their H15 default; a card that stays on screen keeps its state.
func TestFoldStateMapIsBounded(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})

	var calls []contracts.ToolCall
	if _, err := h.ui.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "test.fold-state",
		Title: "折叠状态",
		Build: func(c *ui.Context) {
			for _, call := range calls {
				h.ui.Runtime().RenderTool(c, call)
			}
		},
	}); err != nil {
		t.Fatalf("register test screen: %v", err)
	}
	tester := h.tester()

	// A long successful result starts folded (H15); the reader expands it, which
	// is the state that must survive while the card is on screen.
	longSummary := strings.Repeat("细节", 100) // > 160 runes: the H15 default is folded
	kept := contracts.ToolCall{ID: "keep-1", Name: "bash", Status: "ok", Summary: longSummary}
	calls = []contracts.ToolCall{kept}
	tester.Frame()
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok {
		t.Fatalf("kept card disclosure missing")
	}
	if info.Expanded {
		t.Fatalf("long successful result should default folded")
	}
	tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tester.Frame()
	info, ok = tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok || !info.Expanded {
		t.Fatalf("the reader's expansion was not kept: %+v found %v", info, ok)
	}

	// Flood the fold map well past its bound while the kept card is off screen:
	// it becomes the least recently rendered entry and is evicted.
	calls = make([]contracts.ToolCall, 0, 1024)
	for index := 0; index < 1024; index++ {
		calls = append(calls, contracts.ToolCall{
			ID: fmt.Sprintf("fill-%d", index), Name: "bash", Status: "ok", Summary: longSummary,
		})
	}
	tester.Frame()

	// Back to the kept card alone: the evicted state is gone, so it starts from
	// the H15 default again instead of the map growing forever.
	calls = []contracts.ToolCall{kept}
	tester.Frame()
	info, ok = tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 完成")
	if !ok {
		t.Fatalf("kept card disclosure missing after the flood")
	}
	if info.Expanded {
		t.Fatalf("the fold map kept an evicted entry; the state is not bounded")
	}
}

// TestToolRowCollapsedPreviewIsLocalized covers P3-5: a collapsed tool row's
// preview is the product's localized action line taken from the call's
// arguments, not the backend's raw English result text. The row is folded (the
// result is long) so the collapsed preview is the only summary copy on screen.
func TestToolRowCollapsedPreviewIsLocalized(t *testing.T) {
	h := newHarness(t)
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	raw := "created shared team task\nid: task-1\nsubject: 修复工具栏溢出\n" +
		strings.Repeat("detail line of the raw backend result ", 8)
	h.showCalls([]contracts.ToolCall{{
		ID: "tt-1", Name: "team_task_create", Status: "ok",
		Arguments: `{"subject":"修复工具栏溢出","description":"toolbar overflow"}`,
		Summary:   raw,
	}}, nil, nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "创建团队任务：修复工具栏溢出")
	h.assertNoTexts(tester, "created shared team task", "id: task-1")
}

// previewLocale is a minimal LocaleService for the pure preview-table tests.
type previewLocale struct{ lang string }

func (l previewLocale) Language() string        { return l.lang }
func (l previewLocale) SetLanguage(lang string) { l.lang = lang }
func (l previewLocale) T(key string) string     { return key }

// TestPreviewRegistryLocalizesCommonTools covers the preview template registry:
// the tools the transcript sees most often get a localized one-line action from
// their arguments in both languages, so the row never reads the backend's raw
// English narration.
func TestPreviewRegistryLocalizesCommonTools(t *testing.T) {
	zh := previewLocale{lang: "zh"}
	en := previewLocale{lang: "en"}
	cases := []struct {
		name      string
		arguments string
		summary   string
		wantZh    string
		wantEn    string
	}{
		{"team_task_create", `{"subject":"修复工具栏溢出"}`,
			"created shared team task id: task-1", "创建团队任务：修复工具栏溢出", "Create team task: 修复工具栏溢出"},
		{"team_task_update", `{"id":"task-7","action":"claim","owner":"Alice"}`,
			"updated shared team task task-7", "更新任务 task-7（认领）", "Update task task-7 (claim)"},
		{"team_task_update", `{"id":"task-7","action":"set_dependencies"}`,
			"updated shared team task task-7", "更新任务 task-7（调整依赖）", "Update task task-7 (set dependencies)"},
		{"team_task_get", `{"id":"task-3"}`,
			"team task task-3", "查看任务：task-3", "View task: task-3"},
		{"team_task_list", `{"status":"in_progress","owner":"Alice"}`,
			"ID STATUS OWNER SUBJECT", "列出团队任务（进行中 · 负责人：Alice）",
			"List team tasks (in progress · owner: Alice)"},
		{"job_start", `{"command":"go test ./...","cwd":"/data/mygo"}`,
			"started background job job-1", "启动后台任务：go test ./...", "Start background job: go test ./..."},
		{"job_output", `{"job_id":"job-9"}`,
			"started background job job-9", "读取后台任务输出：job-9", "Read job output: job-9"},
		{"terminal_open", `{"cwd":"/data/mygo","name":"build"}`,
			"opened terminal", "打开终端：/data/mygo", "Open terminal: /data/mygo"},
		{"terminal_send", `{"sessionId":"term-1","text":"make test\n"}`,
			"Wrote 10 bytes", "向终端 term-1 发送：make test", "Send to terminal term-1: make test"},
		{"terminal_read", `{"session_id":"term-2","count":50}`,
			"terminal output", "读取终端输出：term-2", "Read terminal output: term-2"},
		{"terminal_signal", `{"session_id":"term-2","signal":"SIGINT"}`,
			"sent SIGINT", "向终端 term-2 发送信号：SIGINT", "Signal terminal term-2: SIGINT"},
		{"web_search", `{"query":"Go release notes"}`,
			"Found 5 results", "搜索网页：Go release notes", "Search the web: Go release notes"},
		{"web_search", `{"queries":["first query","second query"]}`,
			"Found 5 results", "搜索网页：first query", "Search the web: first query"},
		{"web_fetch", `{"url":"https://go.dev/doc"}`,
			"fetched page", "抓取网页：https://go.dev/doc", "Fetch page: https://go.dev/doc"},
		{"bash", `{"command":"go test ./internal/ui/toolcards"}`,
			"ok  package", "运行命令：go test ./internal/ui/toolcards", "Run command: go test ./internal/ui/toolcards"},
		{"grep", `{"pattern":"func Preview","path":"internal"}`,
			"toolcards.go:335:func Preview", "搜索代码：func Preview", "Search code: func Preview"},
		{"read_file", `{"path":"internal/ui/toolcards/toolcards.go"}`,
			"package toolcards", "读取文件：internal/ui/toolcards/toolcards.go",
			"Read file: internal/ui/toolcards/toolcards.go"},
	}
	for _, tc := range cases {
		call := contracts.ToolCall{Name: tc.name, Arguments: tc.arguments, Summary: tc.summary}
		if got := toolcards.Preview(zh, call); got != tc.wantZh {
			t.Errorf("%s zh preview = %q, want %q", tc.name, got, tc.wantZh)
		}
		if got := toolcards.Preview(en, call); got != tc.wantEn {
			t.Errorf("%s en preview = %q, want %q", tc.name, got, tc.wantEn)
		}
	}
}

// TestPreviewRegistryFallsBackToSummary covers the two fallback paths: a tool
// without a template keeps the backend summary verbatim, and a templated tool
// whose arguments lack the field keeps it too instead of showing an empty or
// invented line.
func TestPreviewRegistryFallsBackToSummary(t *testing.T) {
	zh := previewLocale{lang: "zh"}
	cases := []struct {
		name      string
		arguments string
		summary   string
		want      string
	}{
		{"totally_unknown_tool", `{"anything":"value"}`, "raw backend result text", "raw backend result text"},
		{"team_task_create", `{"description":"no subject here"}`, "created shared team task", "created shared team task"},
		{"bash", "", "package ok", "package ok"},
		{"web_search", `{"query":"   "}`, "Found nothing", "Found nothing"},
		{"terminal_send", `{"sessionId":"term-1"}`, "Wrote 0 bytes", "Wrote 0 bytes"},
	}
	for _, tc := range cases {
		got := toolcards.Preview(zh, contracts.ToolCall{Name: tc.name, Arguments: tc.arguments, Summary: tc.summary})
		if got != tc.want {
			t.Errorf("%s preview = %q, want the backend summary %q", tc.name, got, tc.want)
		}
	}
}

// TestPreviewIsOneLineAndBounded covers the collapsed row's shape: the template
// output is normalized to a single line and cut at the row's preview measure
// with the ellipsis, so a multi-line argument or a long subject cannot stretch
// the tool row.
func TestPreviewIsOneLineAndBounded(t *testing.T) {
	zh := previewLocale{lang: "zh"}
	multi := toolcards.Preview(zh, contracts.ToolCall{
		Name:      "terminal_send",
		Arguments: `{"sessionId":"term-1","text":"line one\nline two\tindented"}`,
	})
	if strings.ContainsAny(multi, "\n\r\t") {
		t.Fatalf("multi-line argument kept its line breaks in the preview: %q", multi)
	}
	if want := "向终端 term-1 发送：line one line two indented"; multi != want {
		t.Fatalf("multi-line preview = %q, want %q", multi, want)
	}

	long := toolcards.Preview(zh, contracts.ToolCall{
		Name:      "team_task_create",
		Arguments: `{"subject":"` + strings.Repeat("很长的任务标题", 20) + `"}`,
	})
	runes := []rune(long)
	if len(runes) != 97 || runes[len(runes)-1] != '…' {
		t.Fatalf("long preview = %d runes ending %q, want 96 runes plus the ellipsis", len(runes), runes[len(runes)-1])
	}
	if !strings.HasPrefix(long, "创建团队任务：") {
		t.Fatalf("long preview lost its localized prefix: %q", long)
	}
}
