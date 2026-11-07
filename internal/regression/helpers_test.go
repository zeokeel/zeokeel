package regression

// Shared harness for the adversarial audit: a kernel root with the product's
// host services (uiroot), plus a descriptor adapter that activates one linked
// module at a time exactly like the assembly loader does. UI assertions run the
// real Root.View through mygo's in-memory Tester; no display is involved.
//
// This package is test-only: it never ships in the product and it only consumes
// public module/contract APIs. Production code is never modified by the audit.

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// memSettings is the SettingsService the process host provides, kept in memory.
type memSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemSettings(values map[string]string) *memSettings {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return &memSettings{values: copied}
}

func (s *memSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *memSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *memSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

// recLogger is a goroutine-safe contracts.Logger test double that records lines.
type recLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *recLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// harness is one headless product root: kernel services + uiroot + frame tester.
type harness struct {
	t        *testing.T
	ctx      cordis.Context
	ui       *uiroot.Root
	settings *memSettings
	logger   *recLogger
	dataDir  string
}

// newHarness starts a kernel root with the host services the product provides:
// uiroot's UI services and bridges, the settings store, logger and data dir.
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessCore(t, t.TempDir(), true)
}

// newBareHarness starts a harness with only the required host services: the
// optional bridges (file drop, interaction, artifact, plan, goal) are absent, as
// in a minimal host.
func newBareHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessCore(t, t.TempDir(), false)
}

// newHarnessWithDataDir starts a harness that reuses dataDir, for restart and
// reopen tests of the persistent backends.
func newHarnessWithDataDir(t *testing.T, dataDir string) *harness {
	t.Helper()
	return newHarnessCore(t, dataDir, true)
}

func newHarnessCore(t *testing.T, dataDir string, withBridges bool) *harness {
	t.Helper()
	local := newMemSettings(nil)
	return newHarnessCoreWith(t, dataDir, withBridges, local, local)
}

// newHarnessWithSettingsService starts a harness whose kernel-provided settings
// service is provided (a fault-injection wrapper); h.settings is the writable
// in-memory store the wrapper delegates to.
func newHarnessWithSettingsService(t *testing.T, provided contracts.SettingsService) *harness {
	t.Helper()
	local := newMemSettings(nil)
	return newHarnessCoreWith(t, t.TempDir(), true, local, provided)
}

func newHarnessCoreWith(t *testing.T, dataDir string, withBridges bool, local *memSettings,
	provided contracts.SettingsService) *harness {
	t.Helper()
	logger := &recLogger{}
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Settings: provided})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	h := &harness{t: t, ctx: ctx, ui: uiRoot, settings: local, logger: logger, dataDir: dataDir}
	provide(t, ctx, contracts.KeyDataDir, h.dataDir)
	provide(t, ctx, contracts.KeySettings, provided)
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(logger))
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	if withBridges {
		provide(t, ctx, contracts.KeyFileDrop, contracts.FileDropService(uiRoot.FileDrop()))
		provide(t, ctx, contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction()))
		provide(t, ctx, contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter()))
		provide(t, ctx, contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer()))
		provide(t, ctx, contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker()))
	}
	return h
}

// tester starts one headless tester over the product's single view function.
func (h *harness) tester(width, height int) *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.ui.View, width, height)
}

// registerScreen installs a minimal screen so frames have a screen body.
func (h *harness) registerScreen(id, title, text string) {
	h.t.Helper()
	if _, err := h.ui.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    id,
		Title: title,
		Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, text) },
	}); err != nil {
		h.t.Fatalf("register screen %q: %v", id, err)
	}
}

func (h *harness) push(screenID string) {
	h.t.Helper()
	if err := h.ui.Navigator().Push(screenID); err != nil {
		h.t.Fatalf("Push(%q): %v", screenID, err)
	}
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// requireService reads one provided service, failing the test when it is absent.
func requireService[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T]) T {
	t.Helper()
	value, err := ctx.Require(key)
	if err != nil {
		t.Fatalf("require %s: %v", key.Name(), err)
	}
	return value
}

// descriptorPlugin adapts a module.Descriptor to the kernel plugin interfaces,
// the same way module.load does inside the assembly loader.
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

// activate starts one module like the assembly would and fails the test when it
// is not Active.
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

// requireHasText / requireNoText / frame helpers ---------------------------------

func requireHasText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	if !tester.HasText(want) {
		t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
	}
}

// hasExactText reports whether the frame shows a label exactly equal to want.
func hasExactText(tester *ui.Tester, want string) bool {
	for _, text := range tester.Texts() {
		if text == want {
			return true
		}
	}
	return false
}

// requireExactText / requireNoExactText assert on a whole label (unlike the
// substring-based helpers), for short labels such as badges.
func requireExactText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	if !hasExactText(tester, want) {
		t.Fatalf("frame has no label exactly %q; texts = %v", want, tester.Texts())
	}
}

func requireNoExactText(t *testing.T, tester *ui.Tester, unwanted string) {
	t.Helper()
	if hasExactText(tester, unwanted) {
		t.Fatalf("frame unexpectedly has a label exactly %q; texts = %v", unwanted, tester.Texts())
	}
}

func requireNoText(t *testing.T, tester *ui.Tester, unwanted string) {
	t.Helper()
	if tester.HasText(unwanted) {
		t.Fatalf("frame unexpectedly shows %q; texts = %v", unwanted, tester.Texts())
	}
}

// waitFor polls a condition until it holds or the short deadline expires. It is
// only used where the work hops through the UI thread queue by design.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- fake ChatService -------------------------------------------------------

// fakeChat is a scripted contracts.ChatService: tests install ChatSession read
// models and the UI module renders them, with no agent backend involved.
type fakeChat struct {
	mu          sync.Mutex
	active      string
	order       []string
	sessions    map[string]contracts.ChatSession
	subs        []func(string)
	sends       []string
	cancels     []string
	sendErr     error
	feedbackErr error
}

func newFakeChat() *fakeChat {
	return &fakeChat{sessions: make(map[string]contracts.ChatSession)}
}

func (f *fakeChat) Config() contracts.ChatConfig {
	return contracts.ChatConfig{Model: "audit-model"}
}

func (f *fakeChat) Sessions() []contracts.ChatSessionInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	infos := make([]contracts.ChatSessionInfo, 0, len(f.order))
	for _, id := range f.order {
		chatSession, ok := f.sessions[id]
		if !ok {
			continue
		}
		infos = append(infos, contracts.ChatSessionInfo{
			ID:           chatSession.ID,
			Title:        chatSession.Title,
			MessageCount: len(chatSession.Messages),
			Running:      chatSession.Running,
			UpdatedAt:    chatSession.UpdatedAt,
		})
	}
	return infos
}

func (f *fakeChat) Session(sessionID string) (contracts.ChatSession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chatSession, ok := f.sessions[sessionID]
	if !ok {
		return contracts.ChatSession{}, false
	}
	copied := chatSession
	copied.Messages = append([]contracts.ChatMessage(nil), chatSession.Messages...)
	return copied, true
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
			return fmt.Errorf("fakeChat: unknown session %q", sessionID)
		}
	}
	f.active = sessionID
	return nil
}

func (f *fakeChat) NewSession(title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("sess-%d", len(f.order)+1)
	now := time.Unix(1700000000, 0)
	f.sessions[id] = contracts.ChatSession{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}
	f.order = append([]string{id}, f.order...)
	f.active = id
	return id, nil
}

func (f *fakeChat) DeleteSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[sessionID]; !ok {
		return fmt.Errorf("fakeChat: unknown session %q", sessionID)
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
	}
	return nil
}

func (f *fakeChat) Send(sessionID, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[sessionID]; !ok {
		return fmt.Errorf("fakeChat: unknown session %q", sessionID)
	}
	f.sends = append(f.sends, sessionID+":"+prompt)
	if f.sendErr != nil {
		return f.sendErr
	}
	return nil
}

// setSendError makes the next and following sends fail (automation history).
func (f *fakeChat) setSendError(err error) {
	f.mu.Lock()
	f.sendErr = err
	f.mu.Unlock()
}

// SetMessageFeedback implements contracts.SessionFeedbacker: it mirrors the
// rating into the session read model (fault injection via setFeedbackError).
func (f *fakeChat) SetMessageFeedback(sessionID, messageID, feedback string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.feedbackErr != nil {
		return f.feedbackErr
	}
	session, ok := f.sessions[sessionID]
	if !ok {
		return fmt.Errorf("fakeChat: unknown session %q", sessionID)
	}
	for index := range session.Messages {
		if session.Messages[index].ID != messageID {
			continue
		}
		session.Messages[index].Feedback = feedback
		f.sessions[sessionID] = session
		return nil
	}
	return fmt.Errorf("fakeChat: unknown message %q in session %q", messageID, sessionID)
}

// setFeedbackError injects a feedback write failure.
func (f *fakeChat) setFeedbackError(err error) {
	f.mu.Lock()
	f.feedbackErr = err
	f.mu.Unlock()
}

// chatWithoutFeedback hides the optional SessionFeedbacker methods from the
// wrapped chat service.
type chatWithoutFeedback struct{ inner contracts.ChatService }

func (c chatWithoutFeedback) Config() contracts.ChatConfig { return c.inner.Config() }

func (c chatWithoutFeedback) Sessions() []contracts.ChatSessionInfo { return c.inner.Sessions() }

func (c chatWithoutFeedback) Session(sessionID string) (contracts.ChatSession, bool) {
	return c.inner.Session(sessionID)
}

func (c chatWithoutFeedback) ActiveSession() string { return c.inner.ActiveSession() }

func (c chatWithoutFeedback) SetActiveSession(sessionID string) error {
	return c.inner.SetActiveSession(sessionID)
}

func (c chatWithoutFeedback) NewSession(title string) (string, error) {
	return c.inner.NewSession(title)
}

func (c chatWithoutFeedback) DeleteSession(sessionID string) error {
	return c.inner.DeleteSession(sessionID)
}

func (c chatWithoutFeedback) Send(sessionID, prompt string) error {
	return c.inner.Send(sessionID, prompt)
}

func (c chatWithoutFeedback) Cancel(sessionID string) error { return c.inner.Cancel(sessionID) }

func (c chatWithoutFeedback) Subscribe(fn func(string)) (func(), error) {
	return c.inner.Subscribe(fn)
}

func (f *fakeChat) Cancel(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, sessionID)
	return nil
}

func (f *fakeChat) Subscribe(fn func(string)) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	f.mu.Lock()
	f.subs = append(f.subs, fn)
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			for index, sub := range f.subs {
				if fmt.Sprintf("%p", sub) == fmt.Sprintf("%p", fn) {
					f.subs = append(f.subs[:index], f.subs[index+1:]...)
					return
				}
			}
		})
	}, nil
}

// addSession installs a scripted session read model and optionally activates it.
func (f *fakeChat) addSession(chatSession contracts.ChatSession, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[chatSession.ID] = chatSession
	f.order = append([]string{chatSession.ID}, f.order...)
	if active {
		f.active = chatSession.ID
	}
}

// --- fake interaction bridges for the UI module -----------------------------

// fakeInteraction is a contracts.UserInteraction whose handler the test drives.
type fakeInteraction struct {
	mu      sync.Mutex
	handler func(contracts.UserQuestion)
	answers []string
	cancels []string
}

func (f *fakeInteraction) SetHandler(fn func(contracts.UserQuestion)) {
	f.mu.Lock()
	f.handler = fn
	f.mu.Unlock()
}

func (f *fakeInteraction) Ask(context.Context, string, []string) (string, error) {
	return "", fmt.Errorf("fakeInteraction: Ask is not scripted")
}

func (f *fakeInteraction) Answer(id, answer string) {
	f.mu.Lock()
	f.answers = append(f.answers, id+"="+answer)
	f.mu.Unlock()
}

func (f *fakeInteraction) Cancel(id string) {
	f.mu.Lock()
	f.cancels = append(f.cancels, id)
	f.mu.Unlock()
}

// trigger invokes the module's installed handler (the host would do this from
// whichever goroutine asks the question).
func (f *fakeInteraction) trigger(question contracts.UserQuestion) bool {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(question)
	return true
}

func (f *fakeInteraction) answerCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.answers...)
}

func (f *fakeInteraction) cancelCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancels...)
}

// fakePresenter is a contracts.ArtifactPresenter whose handler the test drives.
type fakePresenter struct {
	mu      sync.Mutex
	handler func(contracts.Artifact)
}

func (f *fakePresenter) SetArtifactHandler(fn func(contracts.Artifact)) {
	f.mu.Lock()
	f.handler = fn
	f.mu.Unlock()
}

func (f *fakePresenter) Present(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("fakePresenter: Present is not scripted")
}

func (f *fakePresenter) trigger(artifact contracts.Artifact) bool {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(artifact)
	return true
}

// fakeReviewer is a contracts.PlanReviewer whose handler/Exit the test drives.
type fakeReviewer struct {
	mu       sync.Mutex
	handler  func(contracts.PlanReview)
	state    string
	exitErr  error
	exitCall []string
}

func newFakeReviewer(state string) *fakeReviewer { return &fakeReviewer{state: state} }

func (f *fakeReviewer) SetPlanHandler(fn func(contracts.PlanReview)) {
	f.mu.Lock()
	f.handler = fn
	f.mu.Unlock()
}

func (f *fakeReviewer) Review(context.Context, string) (string, error) {
	return "", fmt.Errorf("fakeReviewer: Review is not scripted")
}

func (f *fakeReviewer) Exit(action string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exitCall = append(f.exitCall, action)
	if f.exitErr != nil {
		return f.state, f.exitErr
	}
	f.state = action
	return f.state, nil
}

func (f *fakeReviewer) State() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeReviewer) trigger(review contracts.PlanReview) bool {
	f.mu.Lock()
	handler := f.handler
	f.state = review.State
	f.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(review)
	return true
}

func (f *fakeReviewer) exits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.exitCall...)
}

// fakeGoalTracker is a contracts.GoalTracker the test drives.
type fakeGoalTracker struct {
	mu      sync.Mutex
	handler func(contracts.Goal, bool)
	goal    *contracts.Goal
	clears  int
}

func (f *fakeGoalTracker) SetHandler(fn func(contracts.Goal, bool)) {
	f.mu.Lock()
	f.handler = fn
	f.mu.Unlock()
}

func (f *fakeGoalTracker) Set(_ context.Context, text string) (string, error) {
	return "", fmt.Errorf("fakeGoalTracker: Set is not scripted")
}

func (f *fakeGoalTracker) Get() (string, string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.goal == nil {
		return "", "", "", false
	}
	return f.goal.ID, f.goal.Text, f.goal.Status, true
}

func (f *fakeGoalTracker) Clear() bool {
	f.mu.Lock()
	existed := f.goal != nil
	f.goal = nil
	f.clears++
	handler := f.handler
	f.mu.Unlock()
	// The host tracker notifies the change handler, like the uiroot service.
	if existed && handler != nil {
		handler(contracts.Goal{}, false)
	}
	return existed
}

func (f *fakeGoalTracker) trigger(goal contracts.Goal, ok bool) bool {
	f.mu.Lock()
	f.goal = &goal
	if !ok {
		f.goal = nil
	}
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(goal, ok)
	return true
}

// counter is an atomically maintained test state shared across goroutines.
type counter struct{ value atomic.Int64 }

func (c *counter) inc()          { c.value.Add(1) }
func (c *counter) add(n int64)   { c.value.Add(n) }
func (c *counter) load() int64   { return c.value.Load() }
func (c *counter) store(n int64) { c.value.Store(n) }

// itoa formats n for marker names.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestMain keeps expected containment logs (production recover paths log through
// the standard logger) out of the test output.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}
