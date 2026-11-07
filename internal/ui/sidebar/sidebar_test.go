package sidebar_test

// Headless tests for the sidebar UI module: the real host frame (uiroot.View)
// is rendered through mygo's in-memory Tester, so assertions are about what the
// user sees in the sidebar, and clicks go through the native controls.

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeChat is a minimal contracts.ChatService: it records the calls the sidebar
// makes and lets a test publish a changed session.
type fakeChat struct {
	mu           sync.Mutex
	config       contracts.ChatConfig
	sessions     []contracts.ChatSessionInfo
	active       string
	newSessions  []string
	setActive    []string
	subscribers  []func(string)
	unsubscribed bool
	nextID       string
}

func (f *fakeChat) Config() contracts.ChatConfig { f.mu.Lock(); defer f.mu.Unlock(); return f.config }

func (f *fakeChat) Sessions() []contracts.ChatSessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ChatSessionInfo(nil), f.sessions...)
}

func (f *fakeChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}

func (f *fakeChat) ActiveSession() string { f.mu.Lock(); defer f.mu.Unlock(); return f.active }

func (f *fakeChat) SetActiveSession(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setActive = append(f.setActive, id)
	f.active = id
	return nil
}

func (f *fakeChat) NewSession(title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID
	if id == "" {
		id = "new-1"
	}
	f.newSessions = append(f.newSessions, title)
	f.sessions = append(f.sessions, contracts.ChatSessionInfo{ID: id, Title: title})
	f.active = id
	return id, nil
}

func (f *fakeChat) DeleteSession(string) error { return nil }

func (f *fakeChat) Send(string, string) error { return nil }

func (f *fakeChat) Cancel(string) error { return nil }

func (f *fakeChat) Subscribe(fn func(sessionID string)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribers = append(f.subscribers, fn)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unsubscribed = true
	}, nil
}

func (f *fakeChat) notify(sessionID string) {
	f.mu.Lock()
	subscribers := append([]func(string){}, f.subscribers...)
	f.mu.Unlock()
	for _, fn := range subscribers {
		fn(sessionID)
	}
}

func (f *fakeChat) isUnsubscribed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsubscribed
}

func (f *fakeChat) activeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.setActive...)
}

func (f *fakeChat) createdSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.newSessions...)
}

// harness is one headless product with the host UI services and the sidebar
// module active.
type harness struct {
	t      *testing.T
	ctx    cordis.Context
	uiRoot *uiroot.Root
	chat   *fakeChat
	fiber  *cordis.Fiber
}

func newHarness(t *testing.T, chat *fakeChat, cfg sidebar.Config) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))

	// A session screen must exist for nav.Push("session") to succeed.
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) { ui.Text(c, "会话屏幕") },
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat}
	h.fiber = activate(t, ctx, sidebar.Module(), cfg)
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

// TestSidebarRendersSessionsAndActions covers the session list, the active
// badge, the primary new-chat action and session switching.
func TestSidebarRendersSessionsAndActions(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "项目讨论"},
			{ID: "s2", Title: "另一个会话"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	h.assertTexts(tester, "新对话", "会话", "项目讨论", "另一个会话", "当前", "设置")
	h.assertNoTexts(tester, "还没有会话")

	// The primary action creates a session (which NewSession marks active) and
	// navigates to the session screen.
	h.click(tester, "新对话")
	if got := chat.createdSessions(); len(got) != 1 {
		t.Fatalf("NewSession calls = %v, want one", got)
	}
	if active := chat.ActiveSession(); active != "new-1" {
		t.Fatalf("active session = %q, want new-1", active)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}

	// Clicking a session switches the active session and navigates to it.
	h.click(tester, "另一个会话")
	if active := chat.ActiveSession(); active != "s2" {
		t.Fatalf("active session after click = %q, want s2", active)
	}
	if calls := chat.activeCalls(); len(calls) == 0 || calls[len(calls)-1] != "s2" {
		t.Fatalf("SetActiveSession calls = %v, want s2", calls)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	tester.Frame()
	h.assertTexts(tester, "当前") // the badge moved to s2 (same text, rendered once)
}

// TestSidebarEmptyStateAndSettingsButton covers the empty session state and the
// footer settings entry.
func TestSidebarEmptyStateAndSettingsButton(t *testing.T) {
	chat := &fakeChat{}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "还没有会话", "新对话", "设置")

	h.click(tester, "设置")
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("settings did not open after clicking the footer button")
	}
}

// TestSidebarSubscribeInvalidatesFrames covers the chat subscription: a session
// change posts Invalidate, and the next frame is built.
func TestSidebarSubscribeInvalidatesFrames(t *testing.T) {
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "会话一"}}, active: "s1"}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	before := h.uiRoot.Frames()

	chat.notify("s1")
	tester.Frame()
	if after := h.uiRoot.Frames(); after <= before {
		t.Fatalf("frames did not advance after the chat change: before=%d after=%d", before, after)
	}
	if chat.isUnsubscribed() {
		t.Fatal("chat subscription was unwound before disposal")
	}
}

// TestSidebarConfigLabels covers the assembly-config labels.
func TestSidebarConfigLabels(t *testing.T) {
	chat := &fakeChat{}
	h := newHarness(t, chat, sidebar.Config{
		NewChatLabel:  "开始对话",
		SettingsLabel: "偏好设置",
		SessionsTitle: "对话列表",
	})
	tester := h.tester()
	h.assertTexts(tester, "开始对话", "偏好设置", "对话列表", "还没有会话")
}

// TestSidebarDisposeRemovesContributions covers kernel effect unwinding: the
// slots and the chat subscription disappear with the module.
func TestSidebarDisposeRemovesContributions(t *testing.T) {
	chat := &fakeChat{}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "新对话", "设置")

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose sidebar module: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "新对话", "还没有会话")
	if !chat.isUnsubscribed() {
		t.Fatal("chat subscription was not unwound on disposal")
	}
}

// TestModuleManifest pins the module contract.
func TestModuleManifest(t *testing.T) {
	descriptor := sidebar.Module()
	if descriptor.ID != "zeokeel-ui/sidebar" {
		t.Fatalf("module ID = %q, want zeokeel-ui/sidebar", descriptor.ID)
	}
	if descriptor.Factory == nil {
		t.Fatal("module has a nil factory")
	}
	if len(descriptor.Provides) != 0 {
		t.Fatalf("ui module provides %v, want none", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{
		contracts.KeyUIRuntime.Name(),
		contracts.KeyNavigator.Name(),
		contracts.KeyChat.Name(),
	} {
		if !required[name] {
			t.Fatalf("module does not require %q; requires = %v", name, descriptor.Requires)
		}
	}
}
