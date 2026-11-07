package rightpanel_test

// Headless tests for the session inspector module: the host frame
// (uiroot.View) renders a real session screen that draws the right-panel slot,
// so every assertion is about visible text and clicks go through the native
// tab strip. The chat service is scripted per test.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// --- fake ChatService -------------------------------------------------------

type fakeChat struct {
	mu        sync.Mutex
	config    contracts.ChatConfig
	active    string
	sessions  map[string]contracts.ChatSession
	subs      []func(string)
	unsub     bool
	nextID    int
	notifyIDs []string
}

func newFakeChat() *fakeChat {
	return &fakeChat{sessions: make(map[string]contracts.ChatSession)}
}

func (f *fakeChat) Config() contracts.ChatConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.config
}

func (f *fakeChat) Sessions() []contracts.ChatSessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	infos := make([]contracts.ChatSessionInfo, 0, len(f.sessions))
	for _, session := range f.sessions {
		infos = append(infos, contracts.ChatSessionInfo{
			ID:           session.ID,
			Title:        session.Title,
			MessageCount: len(session.Messages),
			Running:      session.Running,
			UpdatedAt:    session.UpdatedAt,
		})
	}
	return infos
}

func (f *fakeChat) Session(sessionID string) (contracts.ChatSession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[sessionID]
	if !ok {
		return contracts.ChatSession{}, false
	}
	return copySession(session), true
}

func (f *fakeChat) ActiveSession() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *fakeChat) SetActiveSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = sessionID
	return nil
}

func (f *fakeChat) NewSession(title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("sess-%d", f.nextID)
	f.sessions[id] = contracts.ChatSession{ID: id, Title: title}
	f.active = id
	return id, nil
}

func (f *fakeChat) DeleteSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, sessionID)
	if f.active == sessionID {
		f.active = ""
	}
	return nil
}

func (f *fakeChat) Send(string, string) error { return nil }

func (f *fakeChat) Cancel(string) error { return nil }

func (f *fakeChat) Subscribe(fn func(sessionID string)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fn)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unsub = true
	}, nil
}

func (f *fakeChat) addSession(session contracts.ChatSession, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[session.ID] = session
	if active {
		f.active = session.ID
	}
}

func (f *fakeChat) notify(sessionID string) {
	f.mu.Lock()
	subs := append([]func(string){}, f.subs...)
	f.notifyIDs = append(f.notifyIDs, sessionID)
	f.mu.Unlock()
	for _, fn := range subs {
		fn(sessionID)
	}
}

func (f *fakeChat) isUnsubscribed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsub
}

// copySession deep-copies the read model so frames never share mutable slices.
func copySession(session contracts.ChatSession) contracts.ChatSession {
	session.Messages = append([]contracts.ChatMessage(nil), session.Messages...)
	for index := range session.Messages {
		session.Messages[index].Tools = append([]contracts.ToolCall(nil), session.Messages[index].Tools...)
	}
	return session
}

// --- harness ----------------------------------------------------------------

type harness struct {
	t      *testing.T
	ctx    cordis.Context
	uiRoot *uiroot.Root
	chat   *fakeChat
	fiber  *cordis.Fiber
}

func newHarness(t *testing.T, chat *fakeChat, cfg rightpanel.Config) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, nil, nil)
}

// newHarnessWithWorkspace is newHarness with an optional workspace backend
// provided as contracts.KeyWorkspace (nil means the Files pane has no backend).
func newHarnessWithWorkspace(t *testing.T, chat *fakeChat, cfg rightpanel.Config, workspace contracts.WorkspaceService) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, workspace, nil)
}

// newHarnessWithTerminal is newHarness with an optional terminal backend
// provided as contracts.KeyTerminal.
func newHarnessWithTerminal(t *testing.T, chat *fakeChat, cfg rightpanel.Config, terminal contracts.TerminalService) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, nil, terminal)
}

// newHarnessFull builds the product frame with the optional workspace and
// terminal backends.
func newHarnessFull(t *testing.T, chat *fakeChat, cfg rightpanel.Config, workspace contracts.WorkspaceService, terminal contracts.TerminalService) *harness {
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
	if workspace != nil {
		provide[contracts.WorkspaceService](t, ctx, contracts.KeyWorkspace, workspace)
	}
	if terminal != nil {
		provide[contracts.TerminalService](t, ctx, contracts.KeyTerminal, terminal)
	}

	// The host (session screen) owns the layout: this test screen renders the
	// frozen right-panel slot, exactly like the conversation screen will.
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {
			uiRoot.Runtime().RenderSlot(c, contracts.SlotSessionPanelRight)
		},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat}
	h.fiber = activate(t, ctx, rightpanel.Module(), cfg)
	return h
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

// descriptorPlugin adapts a module.Descriptor to the kernel plugin interfaces.
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

func TestModuleMetadata(t *testing.T) {
	if rightpanel.ModuleID != "zeokeel-ui/rightpanel" {
		t.Fatalf("ModuleID = %q", rightpanel.ModuleID)
	}
	if rightpanel.SlotItemID != "rightpanel.inspector" {
		t.Fatalf("SlotItemID = %q", rightpanel.SlotItemID)
	}
	descriptor := rightpanel.Module()
	requires := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		requires[requirement.Name] = true
	}
	if !requires[contracts.KeyUIRuntime.Name()] || !requires[contracts.KeyChat.Name()] {
		t.Fatalf("descriptor requires = %v, want UIRuntime + Chat", descriptor.Requires)
	}
}

func TestOverviewRendersSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:      "s1",
		Title:   "项目讨论",
		Running: true,
		Error:   "上一次运行失败：超时",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "一"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "二"},
			{ID: "m3", Role: contracts.ChatRoleAssistant, Text: "三"},
		},
		UpdatedAt: time.Unix(1700000000, 0),
	}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()

	h.assertTexts(tester, "检查", "概览", "工具", "模型",
		"项目讨论", "运行中", "错误", "上一次运行失败：超时",
		"消息", "3", "更新时间", "会话 ID", "s1")
}

func TestOverviewEmptyState(t *testing.T) {
	h := newHarness(t, newFakeChat(), rightpanel.Config{})
	tester := h.tester()
	h.assertTexts(tester, "检查", "概览", "工具", "模型", "还没有活动会话")
}

func TestToolsPaneAggregatesAndFolds(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "s1",
		Title: "工具会话",
		Messages: []contracts.ChatMessage{
			{
				ID:   "m1",
				Role: contracts.ChatRoleAssistant,
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "bash", Status: "running", Summary: "stdout: all tests passed"},
					{ID: "t2", Name: "read_file", Status: "ok"},
				},
			},
			{
				ID:   "m2",
				Role: contracts.ChatRoleAssistant,
				Tools: []contracts.ToolCall{
					{ID: "t3", Name: "web_search", Status: "error"},
				},
			},
		},
	}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()
	h.click(tester, "工具")

	h.assertTexts(tester, "工具调用", "bash", "read_file", "web_search",
		"运行中", "完成", "失败", "摘要", "bash")
	h.assertNoTexts(tester, "stdout: all tests passed") // folded by default
	h.click(tester, "摘要")
	h.assertTexts(tester, "stdout: all tests passed")
}

func TestToolsPaneEmpty(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "空会话"}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()
	h.click(tester, "工具")
	h.assertTexts(tester, "还没有工具调用")
}

func TestModelPaneHidesAPIKey(t *testing.T) {
	const secret = "sk-super-secret-key"
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{
		Model:         "deepseek-chat",
		BaseURL:       "https://api.example.com/v1",
		APIKey:        secret,
		ContextBudget: 0,
		MaxIterations: 5,
	}
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "会话"}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, secret)
	h.click(tester, "模型")

	h.assertTexts(tester, "模型配置", "deepseek-chat",
		"https://api.example.com/v1", "API Key", "已设置（已隐藏）",
		"上下文预算", "默认", "最大迭代", "5",
		"API key 已打码，不会显示明文。")
	h.assertNoTexts(tester, secret)

	// The key must not leak from any pane.
	h.click(tester, "工具")
	h.assertNoTexts(tester, secret)
	h.click(tester, "概览")
	h.assertNoTexts(tester, secret)
}

func TestSubscriptionRequestsNewFrame(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "旧标题"}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()
	h.assertTexts(tester, "旧标题")

	chat.mu.Lock()
	session := chat.sessions["s1"]
	session.Title = "新标题"
	chat.sessions["s1"] = session
	chat.mu.Unlock()
	chat.notify("s1")
	tester.Frame()
	h.assertTexts(tester, "新标题")
}

func TestDisposeUnregistersSlot(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "会话"}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()
	h.assertTexts(tester, "检查", "概览")

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if !chat.isUnsubscribed() {
		t.Fatal("chat subscription was not unwound on dispose")
	}
	tester.Frame()
	h.assertNoTexts(tester, "检查", "概览", "还没有活动会话")
}

func TestCustomConfigLabels(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{Model: "m"}
	h := newHarness(t, chat, rightpanel.Config{
		Title:       "Inspector",
		OverviewTab: "Overview",
		ToolsTab:    "Tools",
		ModelTab:    "Model",
		EmptyText:   "No active session",
	})
	tester := h.tester()
	h.assertTexts(tester, "Inspector", "Overview", "Tools", "Model", "No active session")
	h.click(tester, "Model")
	h.assertTexts(tester, "模型配置", "m")
}
