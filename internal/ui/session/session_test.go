package session_test

// Headless tests for the conversation UI module. They build the real product
// view (uiroot.Root.View) through mygo's in-memory Tester, with a scripted
// contracts.ChatService, so every assertion is visible text or a recorded
// service call rather than internal bookkeeping.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// --- fake ChatService -------------------------------------------------------

type sendCall struct {
	sessionID string
	prompt    string
}

type fakeChat struct {
	mu        sync.Mutex
	config    contracts.ChatConfig
	active    string
	order     []string
	sessions  map[string]contracts.ChatSession
	subs      []func(string)
	sendCalls []sendCall
	// sendAttempts counts every Send call that reached the backend, including
	// the scripted failures: the queue-retry tests assert a failed delivery is
	// not retried on every frame.
	sendAttempts int
	sendErr      error
	cancelIDs    []string
	cancelErr    error
	newCalls     []string
	nextID       int
	// allowedTools backs the optional contracts.SessionToolPermissions half:
	// the approval dialog's "always allow in this session" choices.
	allowedTools      map[string][]string
	clearAllowedCalls []string
	clearAllowedErr   error
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
	infos := make([]contracts.ChatSessionInfo, 0, len(f.order))
	for _, id := range f.order {
		session, ok := f.sessions[id]
		if !ok {
			continue
		}
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
	if sessionID != "" {
		if _, ok := f.sessions[sessionID]; !ok {
			return fmt.Errorf("fake: unknown session %q", sessionID)
		}
	}
	f.active = sessionID
	return nil
}

func (f *fakeChat) NewSession(title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("sess-%d", f.nextID)
	now := time.Unix(int64(1700000000+f.nextID), 0)
	f.sessions[id] = contracts.ChatSession{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}
	f.order = append([]string{id}, f.order...)
	f.active = id
	f.newCalls = append(f.newCalls, id)
	return id, nil
}

func (f *fakeChat) DeleteSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[sessionID]; !ok {
		return fmt.Errorf("fake: unknown session %q", sessionID)
	}
	delete(f.sessions, sessionID)
	kept := f.order[:0]
	for _, id := range f.order {
		if id != sessionID {
			kept = append(kept, id)
		}
	}
	f.order = kept
	if f.active == sessionID {
		f.active = ""
		if len(f.order) > 0 {
			f.active = f.order[0]
		}
	}
	return nil
}

func (f *fakeChat) Send(sessionID, prompt string) error {
	f.mu.Lock()
	f.sendAttempts++
	session, ok := f.sessions[sessionID]
	if !ok {
		f.mu.Unlock()
		return fmt.Errorf("fake: unknown session %q", sessionID)
	}
	if session.Running {
		f.mu.Unlock()
		return errors.New("fake: session is already running")
	}
	if f.sendErr != nil {
		err := f.sendErr
		f.mu.Unlock()
		return err
	}
	f.sendCalls = append(f.sendCalls, sendCall{sessionID: sessionID, prompt: prompt})
	session.Running = true
	session.Messages = append(session.Messages, contracts.ChatMessage{
		ID:        fmt.Sprintf("m-%d", len(session.Messages)+1),
		Role:      contracts.ChatRoleUser,
		Text:      prompt,
		CreatedAt: time.Unix(1700000100, 0),
	})
	f.sessions[sessionID] = session
	f.mu.Unlock()
	f.emit(sessionID)
	return nil
}

func (f *fakeChat) Cancel(sessionID string) error {
	f.mu.Lock()
	if f.cancelErr != nil {
		err := f.cancelErr
		f.mu.Unlock()
		return err
	}
	session, ok := f.sessions[sessionID]
	if !ok {
		f.mu.Unlock()
		return fmt.Errorf("fake: unknown session %q", sessionID)
	}
	f.cancelIDs = append(f.cancelIDs, sessionID)
	session.Running = false
	f.sessions[sessionID] = session
	f.mu.Unlock()
	f.emit(sessionID)
	return nil
}

// --- optional SessionToolPermissions half ---------------------------------

// AllowedTools returns the remembered tool names of one session (never nil).
func (f *fakeChat) AllowedTools(sessionID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.allowedTools[sessionID]...)
}

// ClearAllowedTools forgets the remembered tools of one session.
func (f *fakeChat) ClearAllowedTools(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearAllowedCalls = append(f.clearAllowedCalls, sessionID)
	if f.clearAllowedErr != nil {
		return f.clearAllowedErr
	}
	delete(f.allowedTools, sessionID)
	return nil
}

// setAllowedTools scripts the tools the approval dialog remembered.
func (f *fakeChat) setAllowedTools(sessionID string, tools []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allowedTools == nil {
		f.allowedTools = make(map[string][]string)
	}
	f.allowedTools[sessionID] = append([]string{}, tools...)
}

// clearedAllowedSessions reports the sessions ClearAllowedTools forgot.
func (f *fakeChat) clearedAllowedSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.clearAllowedCalls...)
}

func (f *fakeChat) Subscribe(fn func(string)) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	f.mu.Lock()
	f.subs = append(f.subs, fn)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for index, sub := range f.subs {
			if sub != nil && fmt.Sprintf("%p", sub) == fmt.Sprintf("%p", fn) {
				f.subs = append(f.subs[:index], f.subs[index+1:]...)
				return
			}
		}
	}, nil
}

// --- test control helpers ---------------------------------------------------

// addSession installs a scripted session, optionally making it active.
func (f *fakeChat) addSession(session contracts.ChatSession, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[session.ID] = session
	f.order = append([]string{session.ID}, f.order...)
	if active {
		f.active = session.ID
	}
}

// updateSession replaces one session and notifies subscribers.
func (f *fakeChat) updateSession(session contracts.ChatSession) {
	f.mu.Lock()
	f.sessions[session.ID] = session
	f.mu.Unlock()
	f.emit(session.ID)
}

// emit calls every subscriber synchronously, like the agent backend does.
// setCancelError scripts the next Cancel failure.
func (f *fakeChat) setCancelError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelErr = err
}

func (f *fakeChat) emit(sessionID string) {
	f.mu.Lock()
	subs := append([]func(string){}, f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		if sub != nil {
			sub(sessionID)
		}
	}
}

// attempts reports every Send call that reached the fake backend, including
// failures (sends only records the successful deliveries).
func (f *fakeChat) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sendAttempts
}

func (f *fakeChat) sends() []sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sendCall(nil), f.sendCalls...)
}

func (f *fakeChat) cancels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancelIDs...)
}

func (f *fakeChat) newSessionIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.newCalls...)
}

func (f *fakeChat) subscriberCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// copySession deep-copies the read model so frames never share slices with the
// fake's mutable state.
func copySession(session contracts.ChatSession) contracts.ChatSession {
	session.Messages = append([]contracts.ChatMessage(nil), session.Messages...)
	for index := range session.Messages {
		session.Messages[index].Tools = append([]contracts.ToolCall(nil), session.Messages[index].Tools...)
	}
	return session
}

// --- harness ----------------------------------------------------------------

type harness struct {
	t    *testing.T
	ctx  cordis.Context
	ui   *uiroot.Root
	chat *fakeChat
}

func newHarness(t *testing.T, chat *fakeChat) *harness {
	t.Helper()
	return newHarnessWithMemory(t, chat, nil)
}

// newVerboseWorkStepsHarness builds the conversation screen with the work-steps
// mode verbose: the transcript always shows the full timeline (no fold, no
// grouping), so the tests that cover message/timeline/tool-row anatomy keep the
// classic transcript shape while the default mode folds a finished process.
func newVerboseWorkStepsHarness(t *testing.T, chat *fakeChat) *harness {
	t.Helper()
	return newPermissionHarness(t, chat, newPermissionSettings(map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose,
	}))
}

// newHarnessWithMemory is newHarness with an optional memory backend provided
// as contracts.KeyMemory (nil means the session module runs without one).
func newHarnessWithMemory(t *testing.T, chat *fakeChat, memory contracts.MemoryStore) *harness {
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
	if memory != nil {
		provide[contracts.MemoryStore](t, ctx, contracts.KeyMemory, memory)
	}

	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
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

// assertNoExactTexts asserts no element shows exactly one of the unwanted
// labels. Unlike assertNoTexts (substring), it distinguishes a control's label
// from the same word inside a sentence (e.g. the running hint says 结束后可发送).
func (h *harness) assertNoExactTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if _, ok := tester.Find(text); ok {
			h.t.Fatalf("frame still shows the label %q; texts = %v", text, tester.Texts())
		}
	}
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// clickExact clicks the first element whose text or label equals label exactly.
// Tester.Click matches substrings, which is ambiguous now that the composer's
// 会话设置 button contains the slash command title 设置.
func (h *harness) clickExact(tester *ui.Tester, label string) {
	h.t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		h.t.Fatalf("no element shows exactly %q; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// typeSlash opens the slash-command menu. The standalone "/" toolbar button is
// gone after the composer redesign: the menu is keyboard-first, so focus the
// composer input and type the slash.
func (h *harness) typeSlash(tester *ui.Tester) {
	h.t.Helper()
	focusComposer(h.t, tester)
	tester.Type("/")
	tester.Frame()
}

// sessionMenuLabels are the labels the single 会话设置 button can show: the
// quiet default plus the contextual permission/mode state that owns it.
var sessionMenuLabels = []string{
	"会话设置", "所有权限", "工作区可写", "只读", "计划模式", "精简", "创作",
	"Session settings", "Settings", "All permissions", "Workspace-write", "Read-only",
	"Plan mode", "Minimal", "Creator",
}

// openSessionMenu clicks the composer's single session menu by whichever label
// it currently shows (a non-default security/mode state owns the label).
func (h *harness) openSessionMenu(tester *ui.Tester) {
	h.t.Helper()
	for _, label := range sessionMenuLabels {
		for _, text := range tester.Texts() {
			if text == label {
				h.clickExact(tester, label)
				return
			}
		}
	}
	h.t.Fatalf("session menu button not found; texts = %v", tester.Texts())
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

// message builds one chat message in tests.
func message(id, role, text string) contracts.ChatMessage {
	return contracts.ChatMessage{ID: id, Role: role, Text: text, CreatedAt: time.Unix(1700000000, 0)}
}

// --- tests ------------------------------------------------------------------

func TestModuleMetadata(t *testing.T) {
	if session.ModuleID != "zeokeel-ui/session" {
		t.Fatalf("ModuleID = %q", session.ModuleID)
	}
	if session.ScreenID != "session" {
		t.Fatalf("ScreenID = %q", session.ScreenID)
	}
	descriptor := session.Module()
	if descriptor.ID != session.ModuleID {
		t.Fatalf("descriptor ID = %q", descriptor.ID)
	}
	requires := make([]string, 0, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		requires = append(requires, requirement.Name)
	}
	sort.Strings(requires)
	wantRequires := []string{contracts.KeyChat.Name(), contracts.KeyNavigator.Name(), contracts.KeyUIRuntime.Name()}
	sort.Strings(wantRequires)
	if strings.Join(requires, ",") != strings.Join(wantRequires, ",") {
		t.Fatalf("descriptor requires = %v, want %v", requires, wantRequires)
	}
}

func TestEmptyStateAndNewConversation(t *testing.T) {
	chat := newFakeChat()
	h := newHarness(t, chat)
	tester := h.tester()

	h.assertTexts(tester, "对话", "开始新对话", "还没有消息，开始一段新对话吧。", "新对话")
	if len(chat.sends()) != 0 {
		t.Fatalf("send calls = %v, want none", chat.sends())
	}

	h.click(tester, "新对话")
	if got := chat.newSessionIDs(); len(got) != 1 {
		t.Fatalf("NewSession calls = %v, want 1", got)
	}
	if active := chat.ActiveSession(); active == "" {
		t.Fatal("active session is still empty after 新对话")
	}
	// A fresh frame of the new active session: the branded hero plus the
	// composer card with its hint and send control.
	h.assertTexts(tester, "开始新对话", "还没有消息，开始一段新对话吧。", "发送")
}

func TestRendersMessagesAndRunningBadges(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:      "sess-1",
		Title:   "修复登录",
		Running: true,
		Error:   "上一次运行失败：超时",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "帮我修复登录"),
			{
				ID:        "m2",
				Role:      contracts.ChatRoleAssistant,
				Text:      "好的，正在检查。",
				Reasoning: "内部推理：先看认证模块",
				Streaming: true,
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go"},
				},
				CreatedAt: time.Unix(1700000001, 0),
			},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()

	h.assertTexts(tester,
		"修复登录",       // session title in the brand header
		"运行中",        // running badge
		"帮我修复登录",     // user bubble text
		"好的，正在检查。",   // assistant text
		"思考中…",       // open reasoning disclosure while streaming
		"生成中",        // the official generation spinner and its label
		"读取文件",       // tool fallback row, friendly localized name
		"上一次运行失败：超时", // session error
		"停止",         // stop button while running
	)
	// The compact tool row shows a one-line preview inline; the raw protocol id
	// stays in the row's tooltip/description, and the internal call ID is hidden.
	h.assertTexts(tester, "auth.go", "完成")
	h.assertNoTexts(tester, "read_file", "t1", "详情")
	// The reasoning disclosure is open while the model is thinking, so the
	// thinking text (rendered as Markdown) is visible without a click.
	h.assertTexts(tester, "内部推理：先看认证模块")
	if info, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成"); !ok || !info.Expanded {
		t.Fatalf("H15 tool row should start expanded: %+v found %v", info, ok)
	}
	h.click(tester, "读取文件")
	tester.Advance(300 * time.Millisecond) // the disclosure animation
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成"); info.Expanded {
		t.Fatalf("click did not fold the tool row: %+v", info)
	}
	// The composer never disappears while a reply streams: queueing is on by
	// default, so the input stays visible/editable and the send position shows
	// 排队 + 停止.
	h.assertTexts(tester, "输入消息…", "排队", "停止")
	h.assertNoTexts(tester, "发送")

	// The reasoning disclosure toggles like the official control it is, and its
	// accessible name switches to 思考 once the turn ends.
	h.click(tester, "思考中…")
	tester.Advance(300 * time.Millisecond)
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "思考中…"); info.Expanded {
		t.Fatalf("click did not fold the reasoning panel: %+v", info)
	}

	// Stopping calls Cancel on the active session.
	h.click(tester, "停止")
	if got := chat.cancels(); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("Cancel calls = %v, want [sess-1]", got)
	}
}

func TestTabPlaceholdersAreNativeTabs(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:       "sess-1",
		Title:    "会话",
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "第一条消息")},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "对话", "轨迹", "记忆", "第一条消息")
	h.assertNoTexts(tester, "本次会话还没有工具调用。")

	h.click(tester, "轨迹")
	h.assertTexts(tester, "本次会话还没有工具调用。")
	h.assertNoTexts(tester, "第一条消息")

	h.click(tester, "记忆")
	h.assertTexts(tester, "记忆后端未启用")

	// Return to the first tab: clicking "对话" is ambiguous (the sidebar also
	// shows the screen title), so use the native tab keyboard navigation
	// (Home selects the first tab while the tab strip has focus).
	tester.Key(0, ui.KeyHome)
	tester.Frame()
	h.assertTexts(tester, "第一条消息")
}

func TestToolRendererModuleIsUsedWhenPresent(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "工具会话",
		Messages: []contracts.ChatMessage{{
			ID:   "m1",
			Role: contracts.ChatRoleAssistant,
			Text: "调用工具",
			Tools: []contracts.ToolCall{
				{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go"},
			},
		}},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	unregister, err := h.ui.Runtime().RegisterToolRenderer(contracts.ToolRenderer{
		Tool:  "read_file",
		ID:    "test.read_file",
		Order: 10,
		Build: func(c *ui.Context, call contracts.ToolCall) {
			ui.Text(c, "工具卡:"+call.Name+"/"+call.Status)
		},
	})
	if err != nil {
		t.Fatalf("register tool renderer: %v", err)
	}
	t.Cleanup(unregister)
	tester := h.tester()
	h.assertTexts(tester, "工具卡:read_file/ok")
}

func TestSubscriptionUpdatesFrame(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "流式会话",
		Messages: []contracts.ChatMessage{{
			ID: "m1", Role: contracts.ChatRoleAssistant, Text: "第一段", Streaming: true,
		}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "第一段")
	if chat.subscriberCount() != 1 {
		t.Fatalf("subscribers = %d, want 1", chat.subscriberCount())
	}

	// The agent backend appends streamed text and notifies; the module posts to
	// the UI thread and requests a frame.
	chat.updateSession(contracts.ChatSession{
		ID:       "sess-1",
		Title:    "流式会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "第一段第二段", Streaming: true}},
	})
	tester.Frame()
	h.assertTexts(tester, "第一段第二段")
}

func TestComposerSendCallsService(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "发送测试"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "发送")

	// Focus the native TextArea (it is directly above the send button), type
	// and send. Text inputs paint their text, so the assertion is the service
	// call, not a text node.
	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send button missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(sendRect.X-120, sendRect.Y-50)
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("TextArea did not take focus after click; texts = %v", tester.Texts())
	}
	tester.Type("你好，世界")
	tester.Frame()
	h.click(tester, "发送")
	tester.Frame()

	sends := chat.sends()
	if len(sends) != 1 {
		t.Fatalf("Send calls = %v, want one", sends)
	}
	if sends[0].sessionID != "sess-1" || sends[0].prompt != "你好，世界" {
		t.Fatalf("Send call = %+v, want sess-1/你好，世界", sends[0])
	}
	// The fake appended the user message; the subscribe notification redrew it.
	h.assertTexts(tester, "你好，世界")
}

func TestCustomConfigLabels(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "配置"}, true)
	h := newHarnessConfig(t, chat, session.Config{
		Title:               "Chat",
		EmptyText:           "It is quiet here.",
		ComposerPlaceholder: "Type a message",
		SendLabel:           "Send",
		StopLabel:           "Stop",
		TabChat:             "Chat",
		TabTrajectory:       "Trajectory",
		TabMemory:           "Memory",
		ComposerHint:        "Press Enter to send",
	})
	tester := h.tester()
	h.assertTexts(tester, "Chat", "Trajectory", "Memory", "Type a message", "Send")
}

func newHarnessConfig(t *testing.T, chat *fakeChat, config session.Config) *harness {
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
	activate(t, ctx, session.Module(), config)
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

func TestTrajectoryTimelineShowsToolCalls(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "轨迹会话",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "运行一下"),
			{
				ID:   "m2",
				Role: contracts.ChatRoleAssistant,
				Text: "好的",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "bash", Status: "running", Summary: "stdout: all tests passed"},
				},
			},
			{
				ID:   "m3",
				Role: contracts.ChatRoleAssistant,
				Text: "完成",
				Tools: []contracts.ToolCall{
					{ID: "t2", Name: "read_file", Status: "error"},
				},
			},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	h.click(tester, "轨迹")
	h.assertTexts(tester,
		"步骤 1", "运行命令", "运行中",
		"步骤 2", "读取文件", "失败",
		"详情")
	// H15: short results start expanded, internal call IDs are hidden, and the
	// friendly name replaces the raw protocol id (kept as its tooltip).
	h.assertTexts(tester, "stdout: all tests passed")
	h.assertNoTexts(tester, "t1", "t2", "bash", "read_file")
	h.assertNoTexts(tester, "运行一下")
	if info, ok := tester.AccessByRole(ui.RoleProgress, "运行中"); !ok || info.Role != ui.RoleProgress {
		t.Fatalf("running trajectory card spinner: %+v found %v", info, ok)
	}

	// On the chat tab the two finished steps now fold into one process row
	// (one row per multi-step turn); expanding it brings the timeline back.
	// Re-focus the tab strip (the summary click moved focus) and press Home for
	// tab 0.
	h.click(tester, "轨迹")
	tester.Key(0, ui.KeyHome)
	tester.Frame()
	h.assertTexts(tester, "运行一下", "过程 · 2 个工具调用")
	h.assertNoTexts(tester, "运行命令")
	h.click(tester, "过程 · 2 个工具调用")
	h.assertTexts(tester, "运行命令", "读取文件")
}

func TestBrandedLayoutGeometryAndHints(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "sess-1",
		Title: "品牌会话",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "右边的问题"),
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "左边的回答", CreatedAt: time.Unix(1700000001, 0)},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "品牌会话", "对话", "轨迹", "记忆", "发送")

	titleRect, ok := tester.Find("品牌会话")
	if !ok || titleRect.H <= 0 || titleRect.W <= 0 {
		t.Fatalf("brand header title missing or empty: %+v ok=%v", titleRect, ok)
	}
	if titleRect.Y > 140 {
		t.Fatalf("header title at y=%.1f, want the page header near the top", titleRect.Y)
	}

	// The primary action sits in the composer at the bottom of the surface.
	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("composer send control missing; texts = %v", tester.Texts())
	}
	if sendRect.Y < 600 {
		t.Fatalf("send control at y=%.1f, want the bottom composer area", sendRect.Y)
	}

	userRect, ok := tester.Find("右边的问题")
	if !ok {
		t.Fatalf("user message missing; texts = %v", tester.Texts())
	}
	assistantRect, ok := tester.Find("左边的回答")
	if !ok {
		t.Fatalf("assistant message missing; texts = %v", tester.Texts())
	}
	if userRect.X+userRect.W/2 <= assistantRect.X+assistantRect.W/2 {
		t.Fatalf("user bubble (x=%.1f w=%.1f) is not right of the assistant text (x=%.1f w=%.1f)",
			userRect.X, userRect.W, assistantRect.X, assistantRect.W)
	}
}

func TestComposerEnterSends(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "回车测试"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "发送")

	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(sendRect.X-120, sendRect.Y-50)
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("TextArea did not take focus; texts = %v", tester.Texts())
	}
	tester.Type("回车发送的消息")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	sends := chat.sends()
	if len(sends) != 1 || sends[0].prompt != "回车发送的消息" {
		t.Fatalf("Enter send calls = %v, want one with the draft", sends)
	}
	// The fake marks the session running and emits; the composer shows Stop.
	h.assertTexts(tester, "回车发送的消息", "停止")
}

// --- P2: composer slash commands and the right inspection panel -------------

// registerTestCommands registers one enabled and one disabled command on the
// harness runtime and returns the enabled command's run counter.
func registerTestCommands(t *testing.T, h *harness, run func()) {
	t.Helper()
	unregister, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    "test.settings",
		Title: "打开偏好设置",
		Hint:  "打开设置面板",
		Order: 10,
		Run:   func() error { run(); return nil },
	})
	if err != nil {
		t.Fatalf("register command: %v", err)
	}
	t.Cleanup(unregister)
	disabled, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:      "test.disabled",
		Title:   "已禁用的命令",
		Hint:    "不应出现",
		Order:   20,
		Enabled: func() bool { return false },
		Run:     func() error { return nil },
	})
	if err != nil {
		t.Fatalf("register disabled command: %v", err)
	}
	t.Cleanup(disabled)
}

// focusComposer focuses the native composer TextArea the same way the existing
// composer tests do.
func focusComposer(t *testing.T, tester *ui.Tester) {
	t.Helper()
	sendRect, ok := tester.Find("发送")
	if !ok {
		sendRect, ok = tester.Find("Send")
	}
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(sendRect.X-120, sendRect.Y-50)
	tester.Frame()
	if !tester.Focused("输入消息…") && !tester.Focused("Ask ZeoKeel…") {
		t.Fatalf("composer TextArea did not take focus; texts = %v", tester.Texts())
	}
}

// TestSlashCommandMenu covers the suggestion panel: "/" shows the enabled
// commands with hints, a disabled command is hidden and clicking a row clears
// the draft and runs the command.
func TestSlashCommandMenu(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "斜杠命令"}, true)
	h := newHarness(t, chat)
	runs := 0
	registerTestCommands(t, h, func() { runs++ })
	tester := h.tester()
	h.assertNoTexts(tester, "打开设置面板", "没有匹配的命令")

	focusComposer(t, tester)
	tester.Type("/")
	tester.Frame()

	h.assertTexts(tester, "打开偏好设置", "打开设置面板")
	h.assertNoTexts(tester, "已禁用的命令", "不应出现")

	// The menu is above the composer card: its row hint sits above the send
	// control.
	menuRect, ok := tester.Find("打开设置面板")
	if !ok {
		t.Fatalf("slash hint missing; texts = %v", tester.Texts())
	}
	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if menuRect.Y >= sendRect.Y {
		t.Fatalf("slash menu (y=%.1f) is not above the composer (y=%.1f)", menuRect.Y, sendRect.Y)
	}

	// Clicking the row's hint clicks the native button row itself.
	h.click(tester, "打开设置面板")
	if runs != 1 {
		t.Fatalf("command run calls = %d, want 1", runs)
	}
	h.assertNoTexts(tester, "打开设置面板", "没有匹配的命令")

	// The draft was cleared: Enter no longer submits anything.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("Send calls after a cleared draft = %v, want none", sends)
	}
}

// TestSlashCommandQueryFiltering covers query matching, the no-match copy and
// that Enter still submits a slash draft without a selection.
func TestSlashCommandQueryFiltering(t *testing.T) {
	t.Run("query matches title", func(t *testing.T) {
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "命令过滤"}, true)
		h := newHarness(t, chat)
		registerTestCommands(t, h, func() {})
		tester := h.tester()
		focusComposer(t, tester)

		tester.Type("/设置")
		tester.Frame()
		h.assertTexts(tester, "打开偏好设置", "打开设置面板")
		h.assertNoTexts(tester, "已禁用的命令", "没有匹配的命令")
	})

	t.Run("no match sends the slash draft", func(t *testing.T) {
		chat := newFakeChat()
		chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "命令过滤"}, true)
		h := newHarness(t, chat)
		registerTestCommands(t, h, func() {})
		tester := h.tester()
		focusComposer(t, tester)

		tester.Type("/xyz")
		tester.Frame()
		h.assertTexts(tester, "没有匹配的命令")
		h.assertNoTexts(tester, "打开设置面板")
		if !tester.Focused("输入消息…") {
			t.Fatalf("composer lost focus after the slash menu appeared; texts=%v", tester.Texts())
		}

		// Esc closes the menu and keeps the draft; typing reopens it.
		tester.Key(0, ui.KeyEscape)
		tester.Frame()
		h.assertNoTexts(tester, "没有匹配的命令")
		tester.Type("x")
		tester.Frame()
		h.assertTexts(tester, "没有匹配的命令")

		// Enter with no highlighted match sends the draft like the Send
		// button would (the dismissal does not turn "/xyz" into an error).
		tester.Key(0, ui.KeyEnter)
		tester.Frame()
		sends := chat.sends()
		if len(sends) != 1 || !strings.HasPrefix(sends[0].prompt, "/xyz") {
			t.Fatalf("Send calls = %v, want the slash draft sent", sends)
		}
	})
}

// TestComposerToolbar covers the redesigned composer action row: the attach
// menu, the model chip and the single session menu beside the fixed send
// control. The slash menu is keyboard-first now (type "/"), with no toolbar
// entry of its own.
func TestComposerToolbar(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "工具栏"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "未配置", "附件", "会话设置", "发送")
	h.assertNoTexts(tester, "命令", "更多工具")

	if _, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
		ID: "toolbar.demo", Title: "工具栏命令", Hint: "测试", Order: 10,
		Run: func() error { return nil },
	}); err != nil {
		t.Fatalf("register command: %v", err)
	}
	tester.Frame()
	h.typeSlash(tester)
	h.assertTexts(tester, "工具栏命令")

	// The model chip opens the settings surface (the chip itself targets the
	// model page; settings navigation is the host's).
	h.click(tester, "未配置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("model chip did not open the settings surface")
	}
}

// TestHeaderCopyActions covers the old session header's copy controls: the
// session ID and the transcript text land on the native clipboard.
func TestHeaderCopyActions(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "复制", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	openConversationActions(t, h, tester)
	h.click(tester, "复制会话 ID")
	if got := tester.Clipboard(); got != "sess-1" {
		t.Fatalf("clipboard = %q, want sess-1", got)
	}
	openConversationActions(t, h, tester)
	h.click(tester, "复制对话")
	got := tester.Clipboard()
	if !strings.Contains(got, "用户: 你好") || !strings.Contains(got, "助手: 你好呀") {
		t.Fatalf("clipboard = %q, want the transcript", got)
	}
}

// --- helpers for the fixed interaction tests --------------------------------

// openGoalActions opens the header goal chip's popover menu.
// openGoalActions opens the composer dock's goal ⋯ menu (the old header chip
// label is now the dock's status badge, so the entry point is the ⋯ button).
func openGoalActions(t *testing.T, h *harness, tester *ui.Tester, _ string) {
	t.Helper()
	if err := tester.Click("目标操作"); err == nil {
		tester.Frame()
		return
	}
	if err := tester.Click("Goal actions"); err == nil {
		tester.Frame()
		return
	}
	t.Fatalf("goal actions entry missing; texts = %v", tester.Texts())
}

// openConversationActions opens the B3 top band's conversation-actions ⋯ menu
// (the clipboard/export/archive entries; it replaced the old 更多 label).
func openConversationActions(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("会话操作"); err == nil {
		tester.Frame()
		return
	}
	if err := tester.Click("Conversation actions"); err == nil {
		tester.Frame()
		return
	}
	t.Fatalf("conversation actions entry missing; texts = %v", tester.Texts())
}

// TestComposerEnglishHintFitsWithoutEllipsis is the S-05 regression: the
// composer's single-line visible hint must show the whole English copy at the
// default 1280 window (it used to be truncated to "Shift+Enter fo…"), while the
// complete key set stays in the send button's tooltip.
func TestComposerEnglishHintFitsWithoutEllipsis(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "英文提示", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"},
	}}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()

	const want = "Enter to send · Shift+Enter newline"
	if !tester.HasText(want) {
		t.Fatalf("English composer hint %q missing or truncated; texts = %v", want, tester.Texts())
	}
	info, ok := tester.Access("Send")
	if !ok {
		t.Fatalf("send button has no accessibility entry; texts = %v", tester.Texts())
	}
	if !strings.Contains(info.Description, "Cmd/Ctrl+Enter") {
		t.Fatalf("send tooltip = %q, want the full key set including Cmd/Ctrl+Enter", info.Description)
	}
}
