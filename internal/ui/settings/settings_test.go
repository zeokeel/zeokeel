package settings_test

// Headless tests for the model settings page: the real host settings surface
// (uiroot.View) is rendered through mygo's in-memory Tester; the page is opened
// with the host navigator, and saving is asserted against a fake settings store.

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeSettings is a contracts.SettingsService backed by a map.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
	sets   []string
	setErr error
	// failKey makes Set fail for one specific settings row (rollback tests).
	failKey string
	getKeys []string
}

func newFakeSettings(values map[string]string) *fakeSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakeSettings{values: values}
}

func (s *fakeSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getKeys = append(s.getKeys, key)
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakeSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	if s.failKey != "" && key == s.failKey {
		return errors.New("row is read-only")
	}
	s.sets = append(s.sets, key)
	s.values[key] = value
	return nil
}

func (s *fakeSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *fakeSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func (s *fakeSettings) setKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sets...)
}

// fakeChat is a minimal contracts.ChatService: the settings pages need its
// effective config, active session and (for the data page) a scriptable session
// list.
type fakeChat struct {
	mu        sync.Mutex
	config    contracts.ChatConfig
	active    string
	sessions  []contracts.ChatSession
	deleteErr error
	deleted   []string

	// Archive half (the archived-sessions settings page): the archive flag
	// lives in the chat service's session list for the archive page.
	archived     map[string]bool
	archiveErr   error
	archiveCalls []string
	// archiveMarks records the last requested archive flag per session (the
	// open-archived test needs to see the session was restored, not only that
	// SetSessionArchived ran).
	archiveMarks map[string]bool
	activeErr    error
}

func (f *fakeChat) Config() contracts.ChatConfig { f.mu.Lock(); defer f.mu.Unlock(); return f.config }

func (f *fakeChat) Sessions() []contracts.ChatSessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	infos := make([]contracts.ChatSessionInfo, 0, len(f.sessions))
	for _, session := range f.sessions {
		infos = append(infos, contracts.ChatSessionInfo{
			ID: session.ID, Title: session.Title, MessageCount: len(session.Messages),
			UpdatedAt: session.UpdatedAt, Archived: f.archived[session.ID],
		})
	}
	return infos
}

func (f *fakeChat) Session(sessionID string) (contracts.ChatSession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, session := range f.sessions {
		if session.ID == sessionID {
			session.Messages = append([]contracts.ChatMessage(nil), session.Messages...)
			return session, true
		}
	}
	return contracts.ChatSession{}, false
}

func (f *fakeChat) ActiveSession() string { f.mu.Lock(); defer f.mu.Unlock(); return f.active }

func (f *fakeChat) SetActiveSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.activeErr != nil {
		return f.activeErr
	}
	f.active = sessionID
	return nil
}

// SetSessionArchived implements contracts.SessionArchiver for the archive page.
func (f *fakeChat) SetSessionArchived(sessionID string, archived bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archiveCalls = append(f.archiveCalls, sessionID)
	if f.archiveMarks == nil {
		f.archiveMarks = map[string]bool{}
	}
	f.archiveMarks[sessionID] = archived
	if f.archiveErr != nil {
		return f.archiveErr
	}
	if f.archived == nil {
		f.archived = map[string]bool{}
	}
	if archived {
		f.archived[sessionID] = true
	} else {
		delete(f.archived, sessionID)
	}
	return nil
}

// setArchived marks one scripted session archived before the page opens.
func (f *fakeChat) setArchived(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.archived == nil {
		f.archived = map[string]bool{}
	}
	f.archived[sessionID] = true
}

// archivedCalls returns the SetSessionArchived calls recorded so far.
func (f *fakeChat) archivedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.archiveCalls...)
}

// isArchived reports the last archive flag SetSessionArchived was called with
// for one session (false when it was never called).
func (f *fakeChat) isArchived(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.archiveMarks[sessionID]
}

func (f *fakeChat) NewSession(string) (string, error) { return "new-1", nil }

func (f *fakeChat) DeleteSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, sessionID)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	remaining := f.sessions[:0]
	for _, session := range f.sessions {
		if session.ID != sessionID {
			remaining = append(remaining, session)
		}
	}
	f.sessions = remaining
	if f.active == sessionID {
		f.active = ""
	}
	return nil
}

// addSession appends one scripted session for the data-page tests.
func (f *fakeChat) addSession(session contracts.ChatSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, session)
}

// deletions returns the DeleteSession calls recorded so far.
func (f *fakeChat) deletions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func (f *fakeChat) Send(string, string) error { return nil }

func (f *fakeChat) Cancel(string) error { return nil }

func (f *fakeChat) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// harness is one headless product with the host UI services and the settings
// module active.
type harness struct {
	t        *testing.T
	ctx      cordis.Context
	uiRoot   *uiroot.Root
	settings *fakeSettings
	chat     *fakeChat
	fiber    *cordis.Fiber
}

func newHarness(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config) *harness {
	t.Helper()
	return newHarnessFull(t, chat, store, cfg, nil, nil, contracts.AppInfo{}, nil)
}

// newHarnessWithSkills is newHarness with an optional skills backend provided
// as contracts.KeySkills (nil keeps the page's "技能后端未启用" empty state).
func newHarnessWithSkills(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config, skills contracts.SkillsService) *harness {
	t.Helper()
	return newHarnessFull(t, chat, store, cfg, skills, nil, contracts.AppInfo{}, nil)
}

// newHarnessWithUpdate is newHarness with an optional update backend and the
// app info the 更新 page reads its current version from.
func newHarnessWithUpdate(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config,
	update contracts.UpdateService, info contracts.AppInfo) *harness {
	t.Helper()
	return newHarnessFull(t, chat, store, cfg, nil, update, info, nil)
}

// newHarnessFull builds the settings harness with the optional backends.
// newHarnessWithImporter is newHarness with an optional DSH importer backend.
func newHarnessWithImporter(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config,
	importer contracts.ImporterService) *harness {
	t.Helper()
	return newHarnessFull(t, chat, store, cfg, nil, nil, contracts.AppInfo{}, importer)
}

// newHarnessFull builds the settings harness with the optional backends; a
// trailing sandbox service is provided as contracts.KeySandbox before the
// settings module activates (the Behavior page reads it optionally).
func newHarnessFull(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config,
	skills contracts.SkillsService, update contracts.UpdateService, info contracts.AppInfo,
	importer contracts.ImporterService, sandboxes ...contracts.SandboxService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	if skills != nil {
		provide[contracts.SkillsService](t, ctx, contracts.KeySkills, skills)
	}
	if update != nil {
		provide[contracts.UpdateService](t, ctx, contracts.KeyUpdate, update)
	}
	if info.Name != "" || info.Version != "" {
		provide[contracts.AppInfo](t, ctx, contracts.KeyAppInfo, info)
	}
	if importer != nil {
		provide[contracts.ImporterService](t, ctx, contracts.KeyImporter, importer)
	}
	for _, sandbox := range sandboxes {
		if sandbox != nil {
			provide[contracts.SandboxService](t, ctx, contracts.KeySandbox, sandbox)
		}
	}

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store, chat: chat}
	h.fiber = activate(t, ctx, settings.Module(), cfg)
	uiRoot.Navigator().ToggleSettings()
	return h
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	// The settings navigation keeps the 高级 group collapsed by default (the
	// settings-complexity simplification). A test that asks for a low-frequency
	// page opens the group first, exactly like a user would.
	if _, ok := tester.Find(label); !ok {
		for _, advanced := range []string{"高级", "Advanced"} {
			if !hasExactLabel(tester, advanced) {
				continue
			}
			if _, expandable := tester.Find(advanced); expandable {
				if err := tester.Click(advanced); err == nil {
					tester.Frame()
				}
			}
			break
		}
	}
	h.ensureVisible(tester, label)
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
	// Native disclosures (Collapsible) animate their content into view: settle
	// the motion so the revealed inputs get real bounds before a test focuses
	// them. Harmless for ordinary clicks.
	tester.Advance(350 * time.Millisecond)
	tester.Frame()
}

// ensureVisible scrolls the settings page body (or the navigation column for
// sidebar items) until the element showing label sits inside the dialog's
// visible area. The headless tester only checks the window, so an element
// clipped by the scroll viewport would otherwise be clicked on the modal scrim
// and dismiss the settings surface.
func (h *harness) ensureVisible(tester *ui.Tester, label string) {
	h.t.Helper()
	ensureVisibleInFrame(h.t, tester, label)
}

// ensureVisibleInFrame is ensureVisible for test helpers that only hold the
// tester (the Behavior page's section helpers scroll like a user would).
func ensureVisibleInFrame(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	for range 24 {
		rect, ok := tester.Find(label)
		if !ok {
			return
		}
		centerX, centerY := rect.X+rect.W/2, rect.Y+rect.H/2
		if rect.W > 0 && rect.H > 0 &&
			centerX >= 200 && centerX <= 1000 && centerY >= 70 && centerY <= 768 {
			return
		}
		// Settings surface at the 1200x800 test size: navigation 190-390, page
		// body 390-1010, panel 24-776.
		x, y := float32(700), float32(500)
		if rect.X < 400 {
			x = 300
		}
		delta := centerY - 480
		if delta > 220 {
			delta = 220
		}
		if delta < -220 {
			delta = -220
		}
		if delta == 0 {
			delta = 120
		}
		tester.Scroll(x, y, 0, delta)
		tester.Frame()
	}
}

// hasExactLabel reports whether one frame label equals text exactly.
func hasExactLabel(tester *ui.Tester, text string) bool {
	for _, label := range tester.Texts() {
		if label == text {
			return true
		}
	}
	return false
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

// focusInput clicks the native control labeled label so typing goes into it.
func (h *harness) focusInput(tester *ui.Tester, label string) {
	h.t.Helper()
	h.ensureVisible(tester, label)
	rect, ok := tester.Find(label)
	if !ok {
		h.t.Fatalf("input labeled %q not found; texts = %v", label, tester.Texts())
	}
	if rect.W == 0 || rect.H == 0 {
		if err := tester.Click(label); err == nil {
			tester.Frame()
		}
	} else {
		tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
	}
}

// typeNumberInput replaces the value of a numeric field: an official
// ui.NumberInput (where the text field sits left of the ± buttons, so the click
// targets its area rather than the row's center) or an optional numeric text
// draft (the runtime limits, where empty means "use the module config").
func typeNumberInput(t *testing.T, h *harness, tester *ui.Tester, label, text string) {
	t.Helper()
	target := label + " 输入"
	h.ensureVisible(tester, target)
	rect, ok := tester.Find(target)
	if !ok {
		t.Fatalf("number input labeled %q not found; texts = %v", target, tester.Texts())
	}
	tester.ClickAt(rect.X+16, rect.Y+rect.H/2)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(text)
	tester.Frame()
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
