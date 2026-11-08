package sidebar_test

// Headless tests for the sidebar UI module: the real host frame (uiroot.View)
// is rendered through mygo's in-memory Tester, so assertions are about what the
// user sees in the sidebar, and clicks go through the native controls.

import (
	"errors"
	"strings"
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
	deleted      []string
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

func (f *fakeChat) DeleteSession(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	kept := f.sessions[:0]
	for _, session := range f.sessions {
		if session.ID != id {
			kept = append(kept, session)
		}
	}
	f.sessions = kept
	if f.active == id {
		f.active = ""
	}
	return nil
}

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

func (f *fakeChat) deletedSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
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
	fiber  *cordis.Fiber
}

func newHarness(t *testing.T, chat contracts.ChatService, cfg sidebar.Config) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, nil, nil, nil, nil)
}

// newHarnessWithWorkspace is newHarness with an optional workspace backend
// provided as contracts.KeyWorkspace (nil keeps the legacy flat session list).
func newHarnessWithWorkspace(t *testing.T, chat contracts.ChatService, cfg sidebar.Config, workspace contracts.WorkspaceService) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, workspace, nil, nil, nil)
}

// newHarnessWithStore is newHarness with an optional durable store provided as
// contracts.KeyStore (its MessageSearcher half powers the message-search area).
func newHarnessWithStore(t *testing.T, chat contracts.ChatService, store contracts.Store, cfg sidebar.Config) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, nil, store, nil, nil)
}

// newHarnessWithSettings is newHarness with an optional settings store (the
// archived-sessions view toggle lives there).
func newHarnessWithSettings(t *testing.T, chat contracts.ChatService, settings contracts.SettingsService, cfg sidebar.Config) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, nil, nil, settings, nil)
}

// newHarnessWithSettingsAndWorkspace adds both the workspace and settings
// backends (the workspace tree's archived group counts need both).
func newHarnessWithSettingsAndWorkspace(t *testing.T, chat contracts.ChatService, settings contracts.SettingsService, workspace contracts.WorkspaceService, cfg sidebar.Config) *harness {
	t.Helper()
	return newHarnessFull(t, chat, cfg, workspace, nil, settings, nil)
}

// newHarnessFull builds the sidebar harness with the optional workspace and
// durable store backends.
// newHarnessWithNotifier is newHarness with the host toast bridge provided, for
// the toast-action tests (archive success + undo).
func newHarnessWithNotifier(t *testing.T, chat contracts.ChatService, notifier contracts.Notifier) *harness {
	t.Helper()
	return newHarnessFull(t, chat, sidebar.Config{}, nil, nil, nil, notifier)
}

func newHarnessFull(t *testing.T, chat contracts.ChatService, cfg sidebar.Config, workspace contracts.WorkspaceService, store contracts.Store, settings contracts.SettingsService, notifier contracts.Notifier) *harness {
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
	if workspace != nil {
		provide[contracts.WorkspaceService](t, ctx, contracts.KeyWorkspace, workspace)
	}
	if store != nil {
		provide[contracts.Store](t, ctx, contracts.KeyStore, store)
	}
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}

	// The destination screens must exist for nav.Push to succeed. The host no
	// longer paints a screen list: the sidebar module contributes the Home
	// destination and the session rows.
	for _, spec := range []contracts.ScreenSpec{
		{ID: "home", Title: "首页", Order: 5, Build: func(c *ui.Context) { ui.Text(c, "home screen") }},
		{ID: "session", Title: "会话", Order: 20, Build: func(c *ui.Context) { ui.Text(c, "会话屏幕") }},
	} {
		if _, err := uiRoot.Runtime().RegisterScreen(spec); err != nil {
			t.Fatalf("register screen %q: %v", spec.ID, err)
		}
	}

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot}
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
	// One frame processes the click; a second one lets panel navigation (which
	// is queued so the host's native Sidebar reconciliation does not override
	// it) settle.
	tester.Frame()
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

// TestSidebarRendersSessionsAndActions covers the session list, the active row
// state, the primary new-chat action and session switching.
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

	h.assertTexts(tester, "新对话", "会话", "项目讨论", "另一个会话", "设置")
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
	// The active state is the accent icon + selected-row tint; no badge text.
	h.assertNoTexts(tester, "当前")
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

// --- P2: search, rename, delete --------------------------------------------

// renamerChat is a fakeChat that also implements contracts.SessionRenamer.
type renamerChat struct {
	*fakeChat
	renameMu  sync.Mutex
	renamed   []renameCall
	renameErr error
}

type renameCall struct {
	ID    string
	Title string
}

func (r *renamerChat) RenameSession(id, title string) error {
	r.renameMu.Lock()
	defer r.renameMu.Unlock()
	if r.renameErr != nil {
		return r.renameErr
	}
	r.renamed = append(r.renamed, renameCall{ID: id, Title: title})
	// Mirror the rename in the read model so the next frame shows the new title.
	r.fakeChat.mu.Lock()
	for index := range r.fakeChat.sessions {
		if r.fakeChat.sessions[index].ID == id {
			r.fakeChat.sessions[index].Title = title
		}
	}
	r.fakeChat.mu.Unlock()
	return nil
}

func (r *renamerChat) renameCalls() []renameCall {
	r.renameMu.Lock()
	defer r.renameMu.Unlock()
	return append([]renameCall(nil), r.renamed...)
}

func (r *renamerChat) setRenameErr(err error) {
	r.renameMu.Lock()
	defer r.renameMu.Unlock()
	r.renameErr = err
}

// clearTextField selects the whole text field content and deletes it.
func clearTextField(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	clickLabel(t, tester, label)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
}

// clickLabel clicks a control by its accessible label or visible text.

// openRowMenu opens a session row's menu by right-clicking its title: the row's
// own context menu is the same native popover menu as its hover More button.
func openRowMenu(t *testing.T, tester *ui.Tester, title string) {
	t.Helper()
	if err := tester.RightClick(title); err != nil {
		t.Fatalf("open row menu for %q: %v; texts = %v", title, err, tester.Texts())
	}
	tester.Frame()
}
func clickLabel(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

func TestSidebarSearchFiltersSessions(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "Project Alpha"},
			{ID: "s2", Title: "Beta Notes"},
			{ID: "s3", Title: "Gamma"},
		},
		active: "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "Project Alpha", "Beta Notes", "Gamma")

	// Type a case-insensitive query: only the matching title remains.
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("PROJECT")
	tester.Frame()
	h.assertTexts(tester, "Project Alpha")
	h.assertNoTexts(tester, "Beta Notes", "Gamma")

	// No match shows the empty state instead of an empty list.
	clearTextField(t, tester, "搜索会话与消息")
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的会话")
	h.assertNoTexts(tester, "Project Alpha", "Beta Notes", "Gamma")

	// Clearing the query restores every session.
	clearTextField(t, tester, "搜索会话与消息")
	h.assertTexts(tester, "Project Alpha", "Beta Notes", "Gamma")
	h.assertNoTexts(tester, "没有匹配的会话")
}

func TestSidebarSearchMatchesUntitledSessions(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: ""},
			{ID: "s2", Title: "Named session"},
		},
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "未命名会话")
	clickLabel(t, tester, "搜索会话与消息")
	tester.Type("未命名")
	tester.Frame()
	h.assertTexts(tester, "未命名会话")
	h.assertNoTexts(tester, "Named session")
}

func TestSidebarRenameFlow(t *testing.T) {
	base := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "旧标题"}},
		active:   "s1",
	}
	chat := &renamerChat{fakeChat: base}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	// The old double glyph "⋯ ⌄" is gone: the row menu is one More icon that
	// shows while the row is hovered or selected.
	h.assertNoTexts(tester, "⋯")

	openRowMenu(t, tester, "旧标题")
	clickLabel(t, tester, "重命名")
	h.assertTexts(tester, "重命名会话", "会话标题", "保存", "取消")

	// Edit the draft through the public text field, then save.
	clickLabel(t, tester, "会话标题")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("新标题")
	tester.Frame()
	clickLabel(t, tester, "保存")
	h.assertNoTexts(tester, "重命名会话")

	calls := chat.renameCalls()
	if len(calls) != 1 || calls[0].ID != "s1" || calls[0].Title != "新标题" {
		t.Fatalf("RenameSession calls = %+v, want s1/新标题", calls)
	}
	// The read model refreshed: the list shows the new title.
	h.assertTexts(tester, "新标题")
	h.assertNoTexts(tester, "旧标题")
}

func TestSidebarRenameCancelAndEmptyTitle(t *testing.T) {
	base := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "keep me"}},
		active:   "s1",
	}
	chat := &renamerChat{fakeChat: base}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	// Cancel closes without renaming.
	openRowMenu(t, tester, "keep me")
	clickLabel(t, tester, "重命名")
	clickLabel(t, tester, "取消")
	h.assertNoTexts(tester, "重命名会话")
	if calls := chat.renameCalls(); len(calls) != 0 {
		t.Fatalf("cancel called RenameSession: %+v", calls)
	}

	// An empty draft is rejected with an inline error, and the dialog stays open.
	openRowMenu(t, tester, "keep me")
	clickLabel(t, tester, "重命名")
	clearTextField(t, tester, "会话标题")
	clickLabel(t, tester, "保存")
	h.assertTexts(tester, "标题不能为空", "重命名会话")
	if calls := chat.renameCalls(); len(calls) != 0 {
		t.Fatalf("empty title called RenameSession: %+v", calls)
	}
	clickLabel(t, tester, "取消")
}

func TestSidebarRenameErrorShown(t *testing.T) {
	base := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "标题"}},
		active:   "s1",
	}
	chat := &renamerChat{fakeChat: base}
	chat.setRenameErr(errors.New("rename failed on disk"))
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester, "标题")
	clickLabel(t, tester, "重命名")
	clickLabel(t, tester, "保存")
	h.assertTexts(tester, "rename failed on disk", "重命名会话")
	clickLabel(t, tester, "取消")
}

func TestSidebarRenameHiddenWithoutRenamer(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "Plain chat"}},
		active:   "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	openRowMenu(t, tester, "Plain chat")
	h.assertTexts(tester, "删除")
	h.assertNoTexts(tester, "重命名")

	// Escape dismisses the popover menu again.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "删除")
}

func TestSidebarRowMenuWithoutActiveSession(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "Not active"}},
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	// Row actions belong to the row (old shell), not to the active session.
	openRowMenu(t, tester, "Not active")
	h.assertTexts(tester, "删除", "设为当前")
	h.assertNoTexts(tester, "重命名")
}

func TestSidebarDeleteConfirmation(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "要删除的会话"}},
		active:   "s1",
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()

	// Cancel closes the alert without deleting.
	openRowMenu(t, tester, "要删除的会话")
	h.click(tester, "删除")
	h.assertTexts(tester, "删除会话", "取消")
	if !strings.Contains(strings.Join(tester.Texts(), "\n"), "要删除的会话") {
		t.Fatalf("alert does not name the session; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "重命名")
	h.click(tester, "取消")
	if deleted := chat.deletedSessions(); len(deleted) != 0 {
		t.Fatalf("cancel deleted sessions: %v", deleted)
	}
	h.assertNoTexts(tester, "删除会话")

	// Confirm deletes the session.
	openRowMenu(t, tester, "要删除的会话")
	h.click(tester, "删除")
	h.click(tester, "删除")
	if deleted := chat.deletedSessions(); len(deleted) != 1 || deleted[0] != "s1" {
		t.Fatalf("deleted sessions = %v, want [s1]", deleted)
	}
	h.assertNoTexts(tester, "删除会话", "要删除的会话")
}

// --- old-UI layout: Home destination + section order -----------------------

// textIndex returns the position of an exact text in the frame, or -1.
func textIndex(texts []string, want string) int {
	for index, text := range texts {
		if text == want {
			return index
		}
	}
	return -1
}

func TestSidebarHomeNavItem(t *testing.T) {
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "会话一"}}}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	h.assertTexts(tester, "首页", "会话", "新对话")

	// Navigate away from Home, then use the sidebar's Home item to return.
	if err := h.uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester.Frame()
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	h.click(tester, "首页")
	if current := h.uiRoot.Navigator().Current(); current != "home" {
		t.Fatalf("current screen after clicking 首页 = %q, want home", current)
	}
}

func TestSidebarNavOrderMatchesOldUI(t *testing.T) {
	chat := &fakeChat{
		sessions: []contracts.ChatSessionInfo{
			{ID: "s1", Title: "Alpha"},
			{ID: "s2", Title: "Beta"},
		},
	}
	h := newHarness(t, chat, sidebar.Config{})
	tester := h.tester()
	texts := tester.Texts()
	home := textIndex(texts, "首页")
	section := textIndex(texts, "会话")
	alpha := textIndex(texts, "Alpha")
	beta := textIndex(texts, "Beta")
	if home < 0 || section < 0 || alpha < 0 || beta < 0 {
		t.Fatalf("frame texts missing side bar rows: %v", texts)
	}
	if !(home < section) {
		t.Fatalf("nav order: 首页 at %d should precede 会话 at %d; texts = %v", home, section, texts)
	}
	if !(section < alpha && alpha < beta) {
		t.Fatalf("session rows should follow the 会话 label in order; indexes 会话=%d Alpha=%d Beta=%d; texts = %v",
			section, alpha, beta, texts)
	}
}
