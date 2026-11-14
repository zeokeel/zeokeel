package regression

// Adversarial audit: the session Context tab. It exists only with the backend
// inspector, reads the folded view on demand (tab entry/session switch/chat
// change, never every frame), shows honest empty states, hides unpriced cost,
// and never leaks one conversation's context into another.

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// auditContextInspector is a scripted contracts.ContextInspector.
type auditContextInspector struct {
	mu    sync.Mutex
	data  map[string]contracts.SessionContext
	calls int
}

func newAuditContextInspector() *auditContextInspector {
	return &auditContextInspector{data: make(map[string]contracts.SessionContext)}
}

func (f *auditContextInspector) set(sessionID string, data contracts.SessionContext) {
	f.mu.Lock()
	f.data[sessionID] = data
	f.mu.Unlock()
}

func (f *auditContextInspector) SessionContext(sessionID string) (contracts.SessionContext, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	data, ok := f.data[sessionID]
	return data, ok
}

func (f *auditContextInspector) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// startContextUI boots the session screen with an optional context inspector.
func startContextUI(t *testing.T, inspector contracts.ContextInspector,
	sessions ...contracts.ChatSession) *sessionHarness {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	for index, chatSession := range sessions {
		chat.addSession(chatSession, index == 0)
	}
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyFileDrop, contracts.FileDropService(h.ui.FileDrop()))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	if inspector != nil {
		provide(t, h.ctx, contracts.KeyContext, contracts.ContextInspector(inspector))
	}
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &sessionHarness{h: h, chat: chat, tester: h.tester(1280, 800), notifier: notifier}
	harness.tester.Frame()
	return harness
}

func auditContextData(model string) contracts.SessionContext {
	at := time.Unix(1_760_000_000, 0)
	return contracts.SessionContext{
		Revision: 3,
		Snapshot: contracts.ContextSnapshot{
			At: at, Model: model, Window: 128000,
			SystemPromptBytes: 4096, SystemPromptTokens: 1024, EstimatedPrompt: 2000,
			Tools:    []contracts.ContextTool{{Name: "bash"}},
			History:  contracts.ContextHistory{Messages: 6, User: 3, Assistant: 2, Tool: 1},
			Injected: contracts.ContextInjected{AgentsMD: true},
		},
		Requests: []contracts.ContextRequest{
			{Seq: 1, Iteration: 1, At: at, Model: model, Prompt: 2000, Completion: 100, ContextTokens: 2000, CostUSD: 0.01, CostKnown: true},
		},
		Events: []contracts.ContextEvent{
			{Seq: 2, Kind: contracts.ContextEventCompact, At: at, Detail: "manual", Before: 9000, After: 3000, Replaced: 8, Saved: 3},
			{Seq: 3, Kind: contracts.ContextEventSpill, At: at, Detail: "tool spill", Path: "/tmp/x.txt", Bytes: 2048},
		},
		Used: 5000, Window: 128000, TotalCostUSD: 0.01, CostKnown: true,
	}
}

func auditContextSession(id string) contracts.ChatSession {
	return contracts.ChatSession{
		ID: id, Title: "Context session",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"}},
	}
}

func TestContextTabExistsOnlyWithBackend(t *testing.T) {
	noBackend := startContextUI(t, nil, auditContextSession("sess-a"))
	if _, ok := noBackend.tester.AccessByRole(ui.RoleTab, "上下文"); ok {
		t.Fatalf("Context tab rendered without the backend; texts = %v", noBackend.tester.Texts())
	}

	inspector := newAuditContextInspector()
	inspector.set("sess-a", auditContextData("model-a"))
	harness := startContextUI(t, inspector, auditContextSession("sess-a"))
	if _, ok := harness.tester.AccessByRole(ui.RoleTab, "上下文"); !ok {
		t.Fatalf("Context tab missing with the backend; texts = %v", harness.tester.Texts())
	}
}

func TestContextPaneContentEmptyStateAndCost(t *testing.T) {
	inspector := newAuditContextInspector()
	inspector.set("sess-a", auditContextData("model-a"))
	harness := startContextUI(t, inspector, auditContextSession("sess-a"))
	if err := harness.tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	for _, want := range []string{
		"概览", "上下文用量", "5K / 128K tokens (3%)", "累计成本", "$0.010",
		"最近一次请求", "model-a", "4.0 KB · 1K tokens", "bash",
		"6 条（用户 3 / 助手 2 / 工具 1）", "AGENTS.md",
		"模型调用", "轮次", "上下文", "上下文事件", "压缩", "溢出", "/tmp/x.txt · 2.0 KB",
	} {
		requireHasText(t, harness.tester, want)
	}

	// A session without folded data shows the empty state, never zeros.
	inspector.set("sess-b", contracts.SessionContext{})
	empty := startContextUI(t, inspector, auditContextSession("sess-b"))
	if err := empty.tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v", err)
	}
	empty.tester.Frame()
	requireHasText(t, empty.tester, "本会话还没有上下文数据")
	requireNoText(t, empty.tester, "累计成本")
}

func TestContextReadsOnDemandOnly(t *testing.T) {
	inspector := newAuditContextInspector()
	inspector.set("sess-a", auditContextData("model-a"))
	harness := startContextUI(t, inspector, auditContextSession("sess-a"))
	if err := harness.tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v", err)
	}
	harness.tester.Frame()
	afterOpen := inspector.callCount()
	for frame := 0; frame < 4; frame++ {
		harness.tester.Frame()
	}
	if got := inspector.callCount(); got != afterOpen {
		t.Fatalf("stable frames re-read the inspector: %d → %d", afterOpen, got)
	}
	// A chat change marks the fold dirty; exactly one more read lands.
	harness.chat.emitSessionChange("sess-a")
	harness.tester.Frame()
	harness.tester.Frame()
	if got := inspector.callCount(); got != afterOpen+1 {
		t.Fatalf("chat change reads = %d, want %d", got, afterOpen+1)
	}
}

// TestContextSectionsAndRecoveryAudit covers the P1 polish: the prompt-section
// table renders from the snapshot metadata (and stays hidden for an old snapshot
// without sections), and a context-overflow recovery event is labelled with its
// outcome instead of the raw backend kind.
func TestContextSectionsAndRecoveryAudit(t *testing.T) {
	data := auditContextData("model-a")
	data.Snapshot.Sections = []contracts.ContextSection{
		{ID: "identity", Label: "identity", Source: "builtin", Bytes: 2048, Tokens: 512, Hash: "h1"},
		{ID: "agents-md", Label: "agents-md", Source: "workspace", Bytes: 1024, Tokens: 256, Hash: "h2"},
	}
	data.Snapshot.EstimatedPrompt = 32000
	data.Events = append(data.Events,
		contracts.ContextEvent{
			Seq: 9, Kind: contracts.ContextEventRecovery, At: time.Unix(1_760_000_000, 0),
			Detail: "context-recovery-failed:compact-failed", Before: 40000, After: 40000,
		},
		contracts.ContextEvent{
			Seq: 10, Kind: contracts.ContextEventArchive, At: time.Unix(1_760_000_100, 0),
			Detail: "manual archive: 6 messages archived, 1 omitted (truncated)",
			Before: 40000, After: 30000, Replaced: 6, Saved: 2,
			ArchivedMessages: 6, ArchivedBytes: 2048, Truncated: true,
		},
		// task-27: an exact automatic compaction that failed to archive is
		// reported as skipped/failed, never as a successful compaction.
		contracts.ContextEvent{
			Seq: 11, Kind: contracts.ContextEventCompact, At: time.Unix(1_760_000_200, 0),
			Detail: "auto", Outcome: "archive-failed",
			Before: 40000, After: 40000, Replaced: 8, Saved: 0,
		},
	)
	inspector := newAuditContextInspector()
	inspector.set("sess-a", data)
	harness := startContextUI(t, inspector, auditContextSession("sess-a"))
	if err := harness.tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	for _, want := range []string{
		"系统提示分段", "身份", "AGENTS.md", "builtin", "workspace",
		"2.0 KB", "1.0 KB", "分段合计", "3.0 KB · 768 tokens",
		"自动压缩重试（压缩失败）", "自动压缩失败，本回合没有重试。",
		"归档（截断）", "归档了 6 条消息；另 1 条超出上限未保留（截断）。",
		"2.0 KB", "40K → 30K tokens",
		"自动压缩跳过/失败：归档原文失败", "归档压缩前的原文失败，历史保持不变。",
	} {
		requireHasText(t, harness.tester, want)
	}
	// Old snapshots without sections hide the block; a normal compact row does
	// not grow the recovery copy.
	legacy := auditContextData("model-a")
	legacy.Revision = data.Revision + 1
	inspector.set("sess-a", legacy)
	harness.chat.emitSessionChange("sess-a")
	harness.tester.Frame()
	harness.tester.Frame()
	requireNoText(t, harness.tester, "系统提示分段")
	requireNoText(t, harness.tester, "自动压缩重试")
}

func TestContextPerSessionNoLeak(t *testing.T) {
	inspector := newAuditContextInspector()
	inspector.set("sess-a", auditContextData("model-a"))
	inspector.set("sess-b", auditContextData("model-b"))
	harness := startContextUI(t, inspector, auditContextSession("sess-a"), auditContextSession("sess-b"))
	if err := harness.tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "model-a")
	requireNoText(t, harness.tester, "model-b")

	if err := harness.chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("switch session: %v", err)
	}
	harness.tester.Frame()
	harness.tester.Frame()
	requireHasText(t, harness.tester, "model-b")
	requireNoText(t, harness.tester, "model-a")
}
