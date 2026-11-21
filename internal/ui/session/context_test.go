package session_test

// Frame tests for the session Context tab: the tab exists only when the backend
// provides contracts.KeyContext, it renders the folded overview/snapshot, the
// per-model-call table, the compaction/spill events and the optional cost, shows
// an honest empty state, reads per session and localizes in English.

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeContextInspector is a scripted contracts.ContextInspector. It also
// implements the optional contracts.ContextArchiveService, so the Context pane's
// archive read-back is exercised by the same fake (a test that needs the
// capability absent uses contextInspectorOnly).
type fakeContextInspector struct {
	mu       sync.Mutex
	data     map[string]contracts.SessionContext
	archives map[string]map[int64]contracts.ContextArchive
	calls    int
}

func newFakeContextInspector() *fakeContextInspector {
	return &fakeContextInspector{
		data:     make(map[string]contracts.SessionContext),
		archives: make(map[string]map[int64]contracts.ContextArchive),
	}
}

func (f *fakeContextInspector) set(sessionID string, data contracts.SessionContext) {
	f.mu.Lock()
	f.data[sessionID] = data
	f.mu.Unlock()
}

func (f *fakeContextInspector) SessionContext(sessionID string) (contracts.SessionContext, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	data, ok := f.data[sessionID]
	return data, ok
}

func (f *fakeContextInspector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// setArchive scripts one readable compact_archive event.
func (f *fakeContextInspector) setArchive(sessionID string, seq int64, archive contracts.ContextArchive) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bySeq := f.archives[sessionID]
	if bySeq == nil {
		bySeq = make(map[int64]contracts.ContextArchive)
		f.archives[sessionID] = bySeq
	}
	bySeq[seq] = archive
}

// ContextArchive implements contracts.ContextArchiveService.
func (f *fakeContextInspector) ContextArchive(sessionID string, seq int64) (contracts.ContextArchive, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	archive, ok := f.archives[sessionID][seq]
	return archive, ok
}

// contextInspectorOnly is a contracts.ContextInspector without the optional
// archive read-back capability: the archive action must not render for it.
type contextInspectorOnly struct {
	data map[string]contracts.SessionContext
}

func (f *contextInspectorOnly) SessionContext(sessionID string) (contracts.SessionContext, bool) {
	data, ok := f.data[sessionID]
	return data, ok
}

// newContextHarness activates the session module with a scripted context
// inspector (the optional backend the tab is registered for).
func newContextHarness(t *testing.T, chat *fakeChat, inspector contracts.ContextInspector) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.ContextInspector](t, ctx, contracts.KeyContext, inspector)
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// contextSession is a conversation with one completed turn.
func contextSession(id string) contracts.ChatSession {
	return contracts.ChatSession{
		ID: id, Title: "上下文会话",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"},
		},
	}
}

// fullContextData is a session with a snapshot, two calls (priced), one exact
// compaction, one inferred compaction and one spill.
func fullContextData() contracts.SessionContext {
	at := time.Unix(1_760_000_000, 0)
	return contracts.SessionContext{
		Revision: 7,
		Snapshot: contracts.ContextSnapshot{
			At: at, Seq: 10, Model: "test-model", Window: 131072,
			SystemPromptBytes: 2048, SystemPromptTokens: 512, EstimatedPrompt: 900,
			Tools: []contracts.ContextTool{
				{Name: "bash", Group: "shell", Tokens: 120},
				{Name: "read_file", Group: "file", Tokens: 80},
			},
			History:  contracts.ContextHistory{Messages: 4, User: 2, Assistant: 1, Tool: 1},
			Injected: contracts.ContextInjected{AgentsMD: true, Memory: true},
		},
		Requests: []contracts.ContextRequest{
			{Seq: 11, Iteration: 1, At: at, Model: "test-model", Prompt: 1200, Completion: 80, Cached: 100, Total: 1280, ContextTokens: 1200, CostUSD: 0.0012, CostKnown: true},
			{Seq: 12, Iteration: 2, At: at.Add(time.Second), Model: "test-model", Prompt: 1600, Completion: 90, Cached: 900, Total: 1690, ContextTokens: 1600, CostUSD: 0.0021, CostKnown: true},
		},
		Events: []contracts.ContextEvent{
			{Seq: 20, Kind: contracts.ContextEventCompact, At: at, Detail: "手动压缩",
				Before: 12345, After: 3200, Replaced: 12, Saved: 4},
			{Seq: 21, Kind: contracts.ContextEventCompact, At: at.Add(time.Minute), Inferred: true,
				Detail: "自动压缩（推断）", Before: 3200, After: 2100},
			{Seq: 22, Kind: contracts.ContextEventSpill, At: at.Add(2 * time.Minute),
				Detail: "工具结果外溢", Path: "/tmp/zeokeel/spill-1.txt", Bytes: 1536},
		},
		Used:         32768,
		Window:       131072,
		TotalCostUSD: 0.0033,
		CostKnown:    true,
	}
}

func openContextTab(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
}

func TestContextTabRegisteredOnlyWithBackend(t *testing.T) {
	// Without the inspector the conversation keeps its three tabs.
	plain := newHarness(t, newFakeChat())
	plainTester := plain.tester()
	plainTester.Frame()
	if _, ok := plainTester.AccessByRole(ui.RoleTab, "上下文"); ok {
		t.Fatalf("Context tab registered without a backend; texts = %v", plainTester.Texts())
	}

	h := newContextHarness(t, contextSessionChat(), newFakeContextInspector())
	tester := h.tester()
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleTab, "上下文"); !ok {
		t.Fatalf("Context tab missing with the backend; texts = %v", tester.Texts())
	}
}

func contextSessionChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contextSession("sess-ctx"), true)
	return chat
}

// TestContextBodyAlignsWithChatReadingColumn pins the Context pane's axis fix:
// the pane used to add its own Grow(1) to the reading column, which widened the
// column ~13% and moved its left edge (~64 DIP against the conversation), so
// switching tabs jumped the text column. Both panes now build on
// uikit.ReadingColumn and share one left edge at wide and narrow widths.
func TestContextBodyAlignsWithChatReadingColumn(t *testing.T) {
	for _, size := range [][2]int{{1512, 900}, {1024, 720}} {
		chat := contextSessionChat()
		inspector := newFakeContextInspector()
		inspector.set("sess-ctx", fullContextData())
		h := newContextHarness(t, chat, inspector)
		tester := h.tester()
		tester.SetSize(size[0], size[1])
		tester.Frame()
		// The assistant reply is drawn at the transcript column's left edge.
		chatBody, ok := tester.Find("完成")
		if !ok {
			t.Fatalf("%dx%d: chat body text missing; texts = %v", size[0], size[1], tester.Texts())
		}
		openContextTab(t, h, tester)
		contextBody, ok := tester.Find("概览")
		if !ok {
			t.Fatalf("%dx%d: Context body section missing; texts = %v", size[0], size[1], tester.Texts())
		}
		if math.Abs(float64(chatBody.X-contextBody.X)) > 1 {
			t.Fatalf("%dx%d: Context body left = %v, chat body left = %v; the tab panes do not share the reading column",
				size[0], size[1], contextBody.X, chatBody.X)
		}
		if contextBody.X <= 0 {
			t.Fatalf("%dx%d: Context body has no visible left edge (%v)", size[0], size[1], contextBody.X)
		}
	}
}

func TestContextPaneRendersOverviewSnapshotRequestsAndEvents(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()

	openContextTab(t, h, tester)
	h.assertTexts(tester, "概览", "上下文用量", "32.8K / 131.1K tokens (25%)",
		"累计成本", "$0.0033", "最近一次请求", "系统提示", "2.0 KB", "512 tokens",
		"估算输入", "900 tokens", "4 条（用户 2 / 助手 1 / 工具 1）",
		"工具目录", "2 个 · bash, read_file", "注入来源", "AGENTS.md", "记忆")
	// Requests table: headers + both calls (newest first, but both present). The
	// prompt column header is localized like every other column, never the raw
	// English "Prompt" (P3-5 residue).
	h.assertTexts(tester, "模型调用", "轮次", "模型", "提示词", "完成", "缓存", "上下文", "时间", "成本",
		"1.2K", "1.6K", "80", "90", "100", "900", "$0.0012", "$0.0021")
	h.assertNoTexts(tester, "Prompt")
	// Events: exact compaction, inferred compaction and the spill path/bytes.
	h.assertTexts(tester, "上下文事件", "压缩", "压缩（推断）", "溢出",
		"12.3K → 3.2K tokens", "消息 12 → 4", "/tmp/zeokeel/spill-1.txt · 1.5 KB")
}

func TestContextCostHiddenWithoutPricing(t *testing.T) {
	data := fullContextData()
	data.CostKnown = false
	data.TotalCostUSD = 0
	for index := range data.Requests {
		data.Requests[index].CostKnown = false
		data.Requests[index].CostUSD = 0
	}
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", data)
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertNoTexts(tester, "累计成本", "成本", "$0.0012", "$0.0033")
	h.assertTexts(tester, "上下文用量", "模型调用")
}

func TestContextEmptyStateWithoutData(t *testing.T) {
	chat := contextSessionChat()
	h := newContextHarness(t, chat, newFakeContextInspector())
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "本会话还没有上下文数据",
		"运行一轮对话后，这里会显示请求快照、模型调用明细与压缩/溢出事件。")
	h.assertNoTexts(tester, "累计成本", "轮次", "上下文用量")
}

func TestContextReadsPerSessionAndSwitchesIsolation(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contextSession("sess-a"), true)
	chat.addSession(contextSession("sess-b"), false)
	inspector := newFakeContextInspector()
	dataA := fullContextData()
	dataA.Snapshot.Model = "model-a"
	for index := range dataA.Requests {
		dataA.Requests[index].Model = "model-a"
	}
	inspector.set("sess-a", dataA)
	dataB := fullContextData()
	dataB.Revision = 9
	dataB.Used = 1000
	dataB.Window = 2000
	dataB.Snapshot.Model = "model-b"
	for index := range dataB.Requests {
		dataB.Requests[index].Model = "model-b"
	}
	inspector.set("sess-b", dataB)
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()

	openContextTab(t, h, tester)
	h.assertTexts(tester, "model-a", "32.8K / 131.1K tokens (25%)")
	h.assertNoTexts(tester, "model-b", "1K / 2K tokens")

	if err := chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("switch session: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "model-b", "1K / 2K tokens (50%)")
	h.assertNoTexts(tester, "model-a", "32.8K / 131.1K tokens (25%)")
}

func TestContextDoesNotRereadOnStableFrames(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)
	afterOpen := inspector.callCount()
	for frame := 0; frame < 4; frame++ {
		tester.Frame()
	}
	if got := inspector.callCount(); got != afterOpen {
		t.Fatalf("stable frames re-read the inspector: %d → %d calls", afterOpen, got)
	}
	// A chat change marks the fold dirty; the next frame re-reads it once.
	chat.emit("sess-ctx")
	tester.Frame()
	tester.Frame()
	if got := inspector.callCount(); got <= afterOpen {
		t.Fatalf("chat change did not refresh the context read model (%d calls)", got)
	}
}

// withContextSections adds task-20 prompt sections to the folded snapshot.
func withContextSections(data contracts.SessionContext) contracts.SessionContext {
	data.Snapshot.Sections = []contracts.ContextSection{
		{ID: "identity", Label: "identity", Source: "builtin", Bytes: 1024, Tokens: 256, Hash: "hash-identity"},
		{ID: "environment", Label: "environment", Source: "builtin", Bytes: 512, Tokens: 128, Hash: "hash-env"},
		{ID: "date", Label: "date", Source: "builtin", Bytes: 38, Tokens: 10, Hash: "hash-date"},
		{ID: "agents-md", Label: "agents-md", Source: "workspace", Bytes: 1536, Tokens: 384, Hash: "hash-agents"},
	}
	data.Snapshot.EstimatedPrompt = 16000
	return data
}

// withoutContextCost strips every price from a folded view, as an unpriced
// catalog does.
func withoutContextCost(data contracts.SessionContext) contracts.SessionContext {
	data.CostKnown = false
	data.TotalCostUSD = 0
	for index := range data.Requests {
		data.Requests[index].CostKnown = false
		data.Requests[index].CostUSD = 0
	}
	return data
}

func TestContextCostFollowsCurrentPricing(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	unpriced := withoutContextCost(fullContextData())
	inspector.set("sess-ctx", unpriced)
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	// No pricing: no total, no cost column, no fake $0.
	h.assertNoTexts(tester, "累计成本", "成本", "$0.00", "—")
	h.assertTexts(tester, "上下文用量", "模型调用")

	// The backend folds the same events with the newly configured prices (the
	// catalog is read at the current prices): the revision moves and the UI
	// picks the priced view up on the next chat change.
	priced := fullContextData()
	priced.Revision = unpriced.Revision + 1
	inspector.set("sess-ctx", priced)
	chat.emit("sess-ctx")
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "累计成本", "$0.0033", "成本", "$0.0012", "$0.0021")

	// Clearing the prices hides the total and the column again instead of
	// leaving stale costs at $0.
	cleared := withoutContextCost(fullContextData())
	cleared.Revision = priced.Revision + 1
	inspector.set("sess-ctx", cleared)
	chat.emit("sess-ctx")
	tester.Frame()
	tester.Frame()
	h.assertNoTexts(tester, "累计成本", "成本", "$0.0033", "$0.0012", "$0.0021")
}

func TestContextSystemPromptSections(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", withContextSections(fullContextData()))
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "系统提示分段",
		"仅展示分段的元数据（大小/估算 tokens），正文不进入事件。",
		// Localized known IDs and their metadata.
		"身份", "运行环境", "日期", "AGENTS.md", "builtin", "workspace",
		"1.0 KB", "256", "512", "128", "1.5 KB", "384",
		"分段合计", "3.0 KB · 778 tokens",
		"估算输入占比", "16K / 131.1K tokens (12%)")
	// The raw section IDs stay out of the visible table (tooltip only).
	h.assertNoTexts(tester, "identity", "environment", "working-directory", "date")
}

func TestContextSystemPromptSectionsHiddenWhenEmpty(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData()) // P0/old snapshot: no sections
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertNoTexts(tester, "系统提示分段", "分段合计", "估算输入占比")
	h.assertTexts(tester, "概览", "模型调用")
}

func TestContextRecoveryEvents(t *testing.T) {
	data := fullContextData()
	data.Events = []contracts.ContextEvent{
		{Seq: 30, Kind: contracts.ContextEventRecovery, At: time.Unix(1_760_000_000, 0),
			Detail: "context-recovery", Before: 12345, After: 3200},
		{Seq: 31, Kind: contracts.ContextEventRecovery, At: time.Unix(1_760_000_100, 0),
			Detail: "context-recovery-failed:compact-failed", Before: 12345, After: 12345},
		{Seq: 32, Kind: contracts.ContextEventRecovery, At: time.Unix(1_760_000_200, 0),
			Detail: "context-recovery-failed:failed", Before: 12345, After: 3200},
	}
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", data)
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "自动压缩重试（成功）", "上下文溢出：自动压缩后重试了本回合。",
		"自动压缩重试（压缩失败）", "自动压缩失败，本回合没有重试。",
		"自动压缩重试（失败）", "自动压缩后重试本回合失败。",
		"12.3K → 3.2K tokens")
	// The recovery's Saved field is a retry count, not a message delta.
	h.assertNoTexts(tester, "消息 0 → 1")
}

// compactOutcomeFailuresData is a folded view with one event per skipped/failed
// automatic compaction outcome (plus an unknown one) so every row is visible in
// the bounded event list.
func compactOutcomeFailuresData() contracts.SessionContext {
	data := fullContextData()
	at := time.Unix(1_760_100_000, 0)
	data.Events = []contracts.ContextEvent{
		{Seq: 50, Kind: contracts.ContextEventCompact, At: at,
			Detail: "auto", Outcome: "failed", Before: 12000, After: 12000,
			Replaced: 10, Saved: 0},
		{Seq: 51, Kind: contracts.ContextEventCompact, At: at.Add(time.Minute),
			Detail: "auto", Outcome: "no-reduction", Before: 12000, After: 11000,
			Replaced: 10, Saved: 10},
		{Seq: 52, Kind: contracts.ContextEventCompact, At: at.Add(2 * time.Minute),
			Detail: "auto", Outcome: "archive-failed", Before: 12000, After: 9000,
			Replaced: 10, Saved: 6},
		{Seq: 53, Kind: contracts.ContextEventCompact, At: at.Add(3 * time.Minute),
			Detail: "auto", Outcome: "replace-failed", Before: 12000, After: 9000,
			Replaced: 10, Saved: 6},
		{Seq: 54, Kind: contracts.ContextEventCompact, At: at.Add(4 * time.Minute),
			Detail: "auto", Outcome: "mystery", Before: 4000, After: 4000},
	}
	return data
}

// compactProvenanceData covers exact success, the legacy inferred event and a
// manual /compact row.
func compactProvenanceData() contracts.SessionContext {
	data := fullContextData()
	at := time.Unix(1_760_100_000, 0)
	data.Events = []contracts.ContextEvent{
		{Seq: 60, Kind: contracts.ContextEventCompact, At: at,
			Detail: "auto", Before: 12000, After: 8000, Replaced: 10, Saved: 5},
		{Seq: 61, Kind: contracts.ContextEventCompact, At: at.Add(time.Minute),
			Detail: "auto", Inferred: true, Before: 8000, After: 7000},
		{Seq: 62, Kind: contracts.ContextEventCompact, At: at.Add(2 * time.Minute),
			Detail: "manual", Before: 7000, After: 4000, Replaced: 8, Saved: 3},
	}
	return data
}

func TestContextCompactOutcomeEvents(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", compactOutcomeFailuresData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester,
		"自动压缩跳过/失败：压缩失败", "自动压缩未能完成，历史保持不变。",
		"自动压缩跳过/失败：没有可减少的内容", "自动压缩没有减少内容，历史保持不变。",
		"自动压缩跳过/失败：归档原文失败", "归档压缩前的原文失败，历史保持不变。",
		"自动压缩跳过/失败：替换历史失败", "替换压缩后的历史失败，历史保持不变。",
		"自动压缩跳过/失败：mystery", "自动压缩未完成，历史保持不变：mystery")
	// A failed/skipped attempt leaves the history untouched: no success copy and
	// no misleading message delta.
	h.assertNoTexts(tester, "已压缩", "消息 10 → 0", "消息 10 → 10", "压缩失败：auto")
}

func TestContextCompactSuccessInferredAndManual(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", compactProvenanceData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester,
		"自动压缩", "运行前自动压缩", "12K → 8K tokens", "消息 10 → 5",
		"压缩（推断）", "8K → 7K tokens",
		"压缩", "手动压缩", "7K → 4K tokens", "消息 8 → 3")
	h.assertNoTexts(tester, "自动压缩跳过/失败")
}

func TestContextCompactOutcomeEnglish(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", compactOutcomeFailuresData())
	h := newContextHarness(t, chat, inspector)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("Context"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester,
		"Auto-compaction skipped/failed: compaction failed",
		"The automatic compaction failed; the history is unchanged.",
		"Auto-compaction skipped/failed: no usable reduction",
		"Auto-compaction skipped/failed: archiving the raw history failed",
		"Auto-compaction skipped/failed: replacing the history failed")

	provenanceInspector := newFakeContextInspector()
	provenanceInspector.set("sess-ctx", compactProvenanceData())
	provenanceHarness := newContextHarness(t, contextSessionChat(), provenanceInspector)
	provenanceHarness.ui.Locale().SetLanguage("en")
	provenanceTester := provenanceHarness.tester()
	provenanceTester.Frame()
	if err := provenanceTester.Click("Context"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, provenanceTester.Texts())
	}
	provenanceTester.Frame()
	provenanceHarness.assertTexts(provenanceTester,
		"Auto-compaction", "Automatic pre-run compaction", "messages 10 → 5",
		"Compaction (inferred)", "Compaction", "Manual compaction")
}

func TestContextArchiveEvents(t *testing.T) {
	data := fullContextData()
	data.Events = []contracts.ContextEvent{
		{Seq: 40, Kind: contracts.ContextEventArchive, At: time.Unix(1_760_000_000, 0),
			Detail: "manual archive: 12 messages archived, 3 omitted (truncated)",
			Before: 12345, After: 3200, Replaced: 12, Saved: 4,
			ArchivedMessages: 12, ArchivedBytes: 4096, Truncated: true},
		{Seq: 41, Kind: contracts.ContextEventArchive, At: time.Unix(1_760_000_100, 0),
			Detail: "manual archive: 8 messages archived",
			Before: 9000, After: 3000, Replaced: 8, Saved: 2,
			ArchivedMessages: 8, ArchivedBytes: 8192},
	}
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", data)
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "归档（截断）",
		"归档了 12 条消息；另 3 条超出上限未保留（截断）。",
		"4.0 KB", "12.3K → 3.2K tokens",
		"归档", "归档了 8 条消息；原始正文另行持久化，不在事件中。",
		"8.0 KB", "9K → 3K tokens")
	// The archive's raw text never appears: only its metadata is in the read model.
	h.assertNoTexts(tester, "manual archive")
}

func TestContextSectionsAndRecoveryEnglish(t *testing.T) {
	data := withContextSections(fullContextData())
	data.Events = []contracts.ContextEvent{
		{
			Seq: 30, Kind: contracts.ContextEventRecovery, At: time.Unix(1_760_000_000, 0),
			Detail: "context-recovery-failed:failed", Before: 9000, After: 3000,
		},
		{
			Seq: 31, Kind: contracts.ContextEventArchive, At: time.Unix(1_760_000_100, 0),
			Detail: "manual archive: 5 messages archived, 2 omitted (truncated)",
			Before: 9000, After: 3000, Replaced: 5, Saved: 2,
			ArchivedMessages: 5, ArchivedBytes: 2048, Truncated: true,
		},
	}
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", data)
	h := newContextHarness(t, chat, inspector)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("Context"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "System prompt sections",
		"Only section metadata is shown (size/estimated tokens); the text is not part of the event.",
		"Identity", "Environment", "AGENTS.md",
		"Section total", "Estimated prompt share",
		"Recovery retry (failed)", "The turn was retried after compaction and failed again.",
		"Archive (truncated)",
		"Archived 5 messages; 2 more exceeded the bound and were not kept (truncated).")
}

func TestContextPaneAtNarrowWidth(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", withContextSections(fullContextData()))
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.SetSize(704, 720)
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "概览", "上下文用量", "系统提示分段", "身份", "模型调用", "上下文事件")
	// The requests table stays readable as a table at the narrow measure (its
	// columns may scroll sideways rather than wrapping the rows into noise).
	h.assertTexts(tester, "轮次", "上下文")
}

func TestContextEnglishCopy(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarness(t, chat, inspector)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("Context"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "Overview", "Context used", "Total cost", "Most recent request",
		"System prompt", "Tool catalog", "Injected", "Model calls", "Prompt", "Completion",
		"Context events", "Compaction", "Spill")
	h.assertNoTexts(tester, "提示词")
}

// --- task-22: Context header actions + archived-history read-back ------------

// archiveMessagesFixture is the raw history an archive event preserved.
func archiveMessagesFixture() []contracts.ContextArchiveMessage {
	return []contracts.ContextArchiveMessage{
		{Role: "user", Text: "把项目跑起来"},
		{Role: "assistant", Text: "我先看一下目录。", ToolCalls: []contracts.ContextArchiveToolCall{
			{Name: "bash", Arguments: `{"command":"ls"}`},
		}},
	}
}

// contextArchiveEvent is the folded metadata of one readable archive.
func contextArchiveEvent(seq int64) contracts.ContextEvent {
	return contracts.ContextEvent{
		Seq: seq, Kind: contracts.ContextEventArchive, At: time.Unix(1_760_000_000, 0),
		Detail:   "manual archive: 2 messages archived",
		Replaced: 2, Saved: 2, ArchivedMessages: 2, ArchivedBytes: 2048,
	}
}

// contextArchiveData is a small folded view whose only event is an archive, so
// the archive row sits near the top of the pane and its action is on screen.
func contextArchiveData() contracts.SessionContext {
	return contracts.SessionContext{Revision: 3, Events: []contracts.ContextEvent{contextArchiveEvent(41)}}
}

// contextArchiveAndCompactData is a manual compaction's timeline: the archive
// event recorded before the compact event (both address the same raw history).
func contextArchiveAndCompactData() contracts.SessionContext {
	compact := contracts.ContextEvent{
		Seq: 42, Kind: contracts.ContextEventCompact, At: time.Unix(1_760_000_200, 0),
		Detail: "manual", Before: 4000, After: 2000, Replaced: 2, Saved: 2,
	}
	return contracts.SessionContext{
		Revision: 4,
		Events:   []contracts.ContextEvent{contextArchiveEvent(41), compact},
	}
}

// newContextHarnessWithChat is newContextHarness for a chat that also implements
// an optional backend half (the compaction action's contracts.SessionCompactor).
func newContextHarnessWithChat(t *testing.T, chat contracts.ChatService, inspector contracts.ContextInspector) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.ContextInspector](t, ctx, contracts.KeyContext, inspector)
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot}
}

// compactContextChat is a compactable conversation with one turn.
func compactContextChat(running bool) *compactChat {
	chat := newCompactChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-ctx", Title: "上下文会话", Running: running,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "完成"},
		},
	}, true)
	return chat
}

// TestContextHeaderCompactAction covers the Context page header's maintenance
// action: a compactable idle session offers 压缩上下文, and the action runs the
// backend compaction through the shared runCompactCommand path.
func TestContextHeaderCompactAction(t *testing.T) {
	chat := compactContextChat(false)
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarnessWithChat(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "上下文", "压缩上下文", "上下文设置")
	info, ok := tester.AccessByRole(ui.RoleButton, "压缩上下文")
	if !ok || info.Disabled {
		t.Fatalf("压缩上下文 is not an enabled button: %+v, found %v", info, ok)
	}
	if err := tester.Click("压缩上下文"); err != nil {
		t.Fatalf("click 压缩上下文: %v; texts = %v", err, tester.Texts())
	}
	for attempt := 0; attempt < 50 && chat.callsCount() == 0; attempt++ {
		tester.Frame()
	}
	if got := chat.callsCount(); got != 1 {
		t.Fatalf("CompactNow calls = %d, want 1; texts = %v", got, tester.Texts())
	}
}

// TestContextHeaderCompactActionHiddenWithoutBackend covers the honest absence:
// a backend that does not implement contracts.SessionCompactor gets no compact
// action, while the settings deep link stays.
func TestContextHeaderCompactActionHiddenWithoutBackend(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "上下文设置")
	if _, ok := tester.Find("压缩上下文"); ok {
		t.Fatalf("compact action shown without a SessionCompactor backend; texts = %v", tester.Texts())
	}
}

// TestContextCompactActionDisabledWhileRunning covers the running state: the
// action is disabled and explains why on hover (a disabled control shows no tip
// of its own, so the tip lives on the wrapper).
func TestContextCompactActionDisabledWhileRunning(t *testing.T) {
	chat := compactContextChat(true)
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarnessWithChat(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	info, ok := tester.AccessByRole(ui.RoleButton, "压缩上下文")
	if !ok || !info.Disabled {
		t.Fatalf("压缩上下文 is not disabled while running: %+v, found %v", info, ok)
	}
	rect, ok := tester.Find("压缩上下文")
	if !ok {
		t.Fatalf("压缩上下文 missing; texts = %v", tester.Texts())
	}
	tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Advance(900 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("会话正在运行") {
		t.Fatalf("running compact action has no tooltip reason; texts = %v", tester.Texts())
	}
	if err := tester.Click("压缩上下文"); err == nil {
		// A disabled button takes no click: the backend must not run.
		tester.Frame()
	}
	if got := chat.callsCount(); got != 0 {
		t.Fatalf("a disabled compact action ran the backend (%d calls)", got)
	}
}

// TestContextCompactActionDisabledWhileCompacting covers the in-flight state: the
// action is disabled and says the compaction is running.
func TestContextCompactActionDisabledWhileCompacting(t *testing.T) {
	chat := compactContextChat(false)
	chat.release = make(chan struct{})
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarnessWithChat(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	if err := tester.Click("压缩上下文"); err != nil {
		t.Fatalf("click 压缩上下文: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	info, ok := tester.AccessByRole(ui.RoleButton, "压缩上下文")
	if !ok || !info.Disabled {
		t.Fatalf("压缩上下文 is not disabled while compacting: %+v, found %v", info, ok)
	}
	rect, ok := tester.Find("压缩上下文")
	if !ok {
		t.Fatalf("压缩上下文 missing; texts = %v", tester.Texts())
	}
	tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Advance(900 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("正在压缩上下文") {
		t.Fatalf("compacting action has no tooltip reason; texts = %v", tester.Texts())
	}
	close(chat.release)
	for attempt := 0; attempt < 300; attempt++ {
		tester.Frame()
		if info, ok := tester.AccessByRole(ui.RoleButton, "压缩上下文"); ok && !info.Disabled {
			return
		}
	}
	t.Fatalf("压缩上下文 stayed disabled after the compaction finished; texts = %v", tester.Texts())
}

// clickContextScrolled scrolls the Context pane's scroll container until the
// first element showing label is inside the window, then clicks it: the events
// list sits below the fold at the headless 1200x800 size.
func clickContextScrolled(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	for attempt := 0; attempt < 24; attempt++ {
		rect, ok := tester.Find(label)
		if !ok {
			t.Fatalf("%q missing; texts = %v", label, tester.Texts())
		}
		centerX, centerY := rect.X+rect.W/2, rect.Y+rect.H/2
		if centerX > 0 && centerX < 1200 && centerY > 80 && centerY < 760 {
			tester.ClickAt(centerX, centerY)
			tester.Frame()
			return
		}
		delta := centerY - 420
		if delta > 150 {
			delta = 150
		}
		if delta < -150 {
			delta = -150
		}
		// The left margin is inside the pane's Scroll but outside the reading
		// column and its nested tables/lists.
		tester.Scroll(300, 500, 0, delta)
		tester.Frame()
	}
	t.Fatalf("%q did not scroll into the window; texts = %v", label, tester.Texts())
}

// TestContextSettingsActionOpensModelPageAtBudget covers the settings deep link:
// it opens the model page and reveals the context-budget field (the anchor), so
// the user lands on the control the Context page talks about.
func TestContextSettingsActionOpensModelPageAtBudget(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	// The settings module is not part of this harness; register the model page the
	// action deep-links to and record the anchor the host reveals.
	revealed := ""
	unregister, err := h.ui.Runtime().RegisterSettingsPage(contracts.SettingsPageSpec{
		ID: "model", Section: "常用", Title: "模型", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "fake-model-page") },
		Reveal: func(_ *ui.Context, anchorKey string) {
			revealed = anchorKey
		},
	})
	if err != nil {
		t.Fatalf("register model settings page: %v", err)
	}
	defer unregister()

	if err := tester.Click("上下文设置"); err != nil {
		t.Fatalf("click 上下文设置: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("上下文设置 did not open the settings surface")
	}
	h.assertTexts(tester, "fake-model-page")
	if revealed != "model.context-budget" {
		t.Fatalf("revealed anchor = %q, want model.context-budget", revealed)
	}
}

// TestContextArchiveViewerShowsRawHistory covers the archived-history viewer: a
// folded archive event offers 查看归档, which opens a read-only native modal with
// the archived messages (role, text, tool calls).
func TestContextArchiveViewerShowsRawHistory(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", contextArchiveData())
	inspector.setArchive("sess-ctx", 41, contracts.ContextArchive{
		Seq: 41, Kind: "manual", RawMessageCount: 2, RetainedMessages: 2,
		Bytes: 2048, Messages: archiveMessagesFixture(),
	})
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "查看归档")
	clickContextScrolled(t, tester, "查看归档")
	h.assertTexts(tester, "归档原文", "手动压缩", "2 条消息",
		"用户", "把项目跑起来", "助手", "我先看一下目录。", "工具调用：bash")
	h.assertNoTexts(tester, "读取归档原文…", "没有找到归档原文。")

	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close the archive modal: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "归档原文")
}

// TestContextArchiveViewerTruncated covers the honest truncation copy: the
// archive event's bound and the reader's text bound are both stated.
func TestContextArchiveViewerTruncated(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", contextArchiveData())
	inspector.setArchive("sess-ctx", 41, contracts.ContextArchive{
		Seq: 41, Kind: "manual", RawMessageCount: 5, RetainedMessages: 2,
		OmittedMessages: 3, Truncated: true, Bytes: 2048,
		Messages: []contracts.ContextArchiveMessage{
			{Role: "user", Text: "把项目跑起来"},
			{Role: "assistant", Text: "我先看一下目录。", Truncated: true},
		},
	})
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	clickContextScrolled(t, tester, "查看归档")
	h.assertTexts(tester, "归档原文", "归档已截断", "另有 3 条未保留", "文本已截断")
}

// TestContextArchiveViewerUnavailable covers a read that finds no archive (the
// durable event was pruned or belongs to another store): the modal says so
// instead of showing an empty list as if the archive were empty.
func TestContextArchiveViewerUnavailable(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", contextArchiveData())
	// No archive registered for sequence 41: the read misses.
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	clickContextScrolled(t, tester, "查看归档")
	h.assertTexts(tester, "归档原文", "没有找到归档原文。")
}

// TestContextArchiveButtonHiddenWithoutCapability covers the optional backend: a
// ContextInspector that does not implement contracts.ContextArchiveService offers
// no 查看归档 action.
func TestContextArchiveButtonHiddenWithoutCapability(t *testing.T) {
	chat := contextSessionChat()
	inspector := &contextInspectorOnly{data: map[string]contracts.SessionContext{
		"sess-ctx": contextArchiveData(),
	}}
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	h.assertTexts(tester, "归档")
	if _, ok := tester.Find("查看归档"); ok {
		t.Fatalf("查看归档 shown without a ContextArchiveService; texts = %v", tester.Texts())
	}
}

// TestContextArchiveButtonHiddenWithoutArchiveEvent covers a timeline with no
// archive event: no archive row, so no 查看归档 action.
func TestContextArchiveButtonHiddenWithoutArchiveEvent(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", fullContextData())
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	if _, ok := tester.Find("查看归档"); ok {
		t.Fatalf("查看归档 shown without an archive event; texts = %v", tester.Texts())
	}
}

// TestContextArchiveActionOnCompactionRow covers the pairing: a manual
// compaction's rows are the archive event plus the compact event recorded right
// after it, and both address the same raw history — so each offers the viewer.
func TestContextArchiveActionOnCompactionRow(t *testing.T) {
	chat := contextSessionChat()
	inspector := newFakeContextInspector()
	inspector.set("sess-ctx", contextArchiveAndCompactData())
	inspector.setArchive("sess-ctx", 41, contracts.ContextArchive{
		Seq: 41, Kind: "manual", RawMessageCount: 2, RetainedMessages: 2,
		Bytes: 2048, Messages: archiveMessagesFixture(),
	})
	h := newContextHarness(t, chat, inspector)
	tester := h.tester()
	tester.Frame()
	openContextTab(t, h, tester)

	if got := countExactText(tester, "查看归档"); got != 2 {
		t.Fatalf("查看归档 actions = %d, want one on the archive row and one on the compact row; texts = %v",
			got, tester.Texts())
	}
	clickContextScrolled(t, tester, "查看归档")
	h.assertTexts(tester, "归档原文", "把项目跑起来")
}
