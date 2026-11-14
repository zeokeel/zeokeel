package session_test

// Frame tests for the session Context tab: the tab exists only when the backend
// provides contracts.KeyContext, it renders the folded overview/snapshot, the
// per-model-call table, the compaction/spill events and the optional cost, shows
// an honest empty state, reads per session and localizes in English.

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeContextInspector is a scripted contracts.ContextInspector.
type fakeContextInspector struct {
	mu    sync.Mutex
	data  map[string]contracts.SessionContext
	calls int
}

func newFakeContextInspector() *fakeContextInspector {
	return &fakeContextInspector{data: make(map[string]contracts.SessionContext)}
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
	// Requests table: headers + both calls (newest first, but both present).
	h.assertTexts(tester, "模型调用", "轮次", "模型", "Prompt", "完成", "缓存", "上下文", "时间", "成本",
		"1.2K", "1.6K", "80", "90", "100", "900", "$0.0012", "$0.0021")
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
		"身份", "运行环境", "AGENTS.md", "builtin", "workspace",
		"1.0 KB", "256", "512", "128", "1.5 KB", "384",
		"分段合计", "3.0 KB · 768 tokens",
		"估算输入占比", "16K / 131.1K tokens (12%)")
	// The raw section IDs stay out of the visible table (tooltip only).
	h.assertNoTexts(tester, "identity", "environment", "working-directory")
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
		"System prompt", "Tool catalog", "Injected", "Model calls", "Completion",
		"Context events", "Compaction", "Spill")
}
