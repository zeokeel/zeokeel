package shell

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// planHarness is a live shell/agent/controller stack. Plan mode is the
// session's persisted flag; since the runtime tool gate was removed (ADR 0008)
// the shell reads it through the controller, the same flag the prompt policy
// uses for the next turn.
type planHarness struct {
	app      *App
	ctrl     *session.Controller
	provider *m6Provider
	store    *store.Store
}

// planBadgeLabelEN is the composer plan badge's accessible name (and value) in
// the English UI, as the badge's Label.
const planBadgeLabelEN = "Plan mode on; click to exit"

// planBadgeRoleButton is platform.AccessRole's RoleButton value, as the
// badge's accessibility node reports it.
const planBadgeRoleButton uint8 = 2

// accessFocusableState is platform.AccessStates' AccessFocusable bit.
const accessFocusableState uint64 = 1 << 0

func newPlanHarness(t *testing.T, steps ...m6Step) *planHarness {
	t.Helper()
	return newPlanHarnessWithSession(t, true, steps...)
}

// newPlanHarnessNoSession is newPlanHarness without opening a session: its
// live Home composer has no current session, the "/plan" no-session case.
func newPlanHarnessNoSession(t *testing.T, steps ...m6Step) *planHarness {
	t.Helper()
	return newPlanHarnessWithSession(t, false, steps...)
}

func newPlanHarnessWithSession(t *testing.T, openSession bool, steps ...m6Step) *planHarness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ws, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	registry := runtime.NewRegistry()
	if err := registry.Register(tools.NewPresentTool()); err != nil {
		t.Fatalf("register present: %v", err)
	}
	provider := &m6Provider{steps: steps}
	var ctrl *session.Controller
	agent, err := runtime.New(runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "test agent",
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	ctrl, err = session.New(session.Config{
		Store:       st,
		Broker:      broker,
		Projector:   projector,
		Agent:       agent,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})

	screen := Session
	if !openSession {
		screen = Home
	}
	app := &App{Screen: screen, Ctrl: ctrl}
	// Pin the UI language so the card's button labels are stable.
	app.Language = LangEN
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	modeUIStates.Delete(app)
	sessionRenderStates.Delete(app)
	ctrl.OnUpdate(app.SetSnapshot)
	if openSession {
		if _, err := ctrl.CreateSession(context.Background(), "plan"); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	app.SetSnapshot(ctrl.Snapshot())
	return &planHarness{app: app, ctrl: ctrl, provider: provider, store: st}
}

// submit runs one turn and leaves the app showing its idle snapshot.
func (h *planHarness) submit(t *testing.T, text string) {
	t.Helper()
	if err := h.ctrl.Submit(context.Background(), h.ctrl.CurrentSessionID(), text); err != nil {
		t.Fatalf("Submit(%q): %v", text, err)
	}
	m6WaitIdle(t, h.ctrl)
	h.ctrl.Wait()
	h.app.SetSnapshot(h.ctrl.Snapshot())
}

// reload mirrors the next frame's snapshot after a direct controller call.
func (h *planHarness) reload() {
	h.app.SetSnapshot(h.ctrl.Snapshot())
}

// userMessages returns the session's persisted user messages, oldest first.
func (h *planHarness) userMessages(t *testing.T) []store.Message {
	t.Helper()
	messages, err := h.store.ListMessages(h.ctrl.CurrentSessionID())
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var out []store.Message
	for _, message := range messages {
		if message.Role == string(state.RoleUser) {
			out = append(out, message)
		}
	}
	return out
}

// callCount reports how many model calls the scripted provider served.
func (p *m6Provider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// planPresentStep is the scripted present call every card test uses.
var planPresentStep = m6Step{
	tool: "present",
	args: `{"title":"Plan","markdown":"# Plan\n1. Inspect internal/shell in read-only mode\n2. Add the plan review card"}`,
}

// TestPlanModePresentPlanCardShown proves the review card appears only when
// plan mode is on and a present artifact titled "Plan" exists. The session
// header no longer carries a plan-mode switch (it duplicated the composer's
// read-only permission), so the persisted flag is set through the controller,
// which is the same flag the prompt policy and the composer badge read.
func TestPlanModePresentPlanCardShown(t *testing.T) {
	h := newPlanHarness(t,
		planPresentStep,
		m6Step{text: "Plan ready for review."},
	)
	h.submit(t, "plan the change")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Plan") {
		t.Fatalf("presented artifact title missing from the transcript: %v", tt.Texts())
	}
	if tt.HasText("Plan review") || tt.HasText("Approve") || tt.HasText("Exit plan mode") {
		t.Fatalf("plan review card rendered while plan mode is off: %v", tt.Texts())
	}
	// The redundant header checkbox is gone: with plan mode off the header
	// shows only the title, the mode chip and the screen actions. HasText is a
	// substring match, so check the retired toggle's exact label instead: the
	// composer badge's longer accessible name also starts with "Plan mode".
	if hasLabel(tt, "Plan mode: Off") || hasLabel(tt, "Plan mode: On") {
		t.Fatalf("header plan-mode toggle still rendered: %v", tt.Texts())
	}

	// Turn plan mode on through the controller; the card and the composer
	// badge appear on the next frame, with no header switch involved.
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()
	tt.Frame()
	if !tt.HasText("Plan review") {
		t.Fatalf("plan review card missing after enabling plan mode: %v", tt.Texts())
	}
	for _, want := range []string{"Plan", "Approve", "Exit plan mode"} {
		if !tt.HasText(want) {
			t.Fatalf("plan review card missing %q: %v", want, tt.Texts())
		}
	}
	// Plan mode on also shows the composer badge (ADR 0008)…
	if !hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("composer plan badge missing with plan mode on: %v", tt.Texts())
	}
	// …but the header toggle does not come back.
	if hasLabel(tt, "Plan mode: Off") || hasLabel(tt, "Plan mode: On") {
		t.Fatalf("header plan-mode toggle rendered with plan mode on: %v", tt.Texts())
	}
	// The Markdown body is rendered, not collapsed to a source dump.
	if !tt.HasText("Inspect internal/shell in read-only mode") {
		t.Fatalf("plan Markdown step missing: %v", tt.Texts())
	}
	if !tt.HasText("Add the plan review card") {
		t.Fatalf("plan Markdown second step missing: %v", tt.Texts())
	}
}

// TestPlanModeApproveClearsAndContinues proves Approve clears the persisted
// flag and submits exactly one continuation with the documented prompt.
func TestPlanModeApproveClearsAndContinues(t *testing.T) {
	h := newPlanHarness(t,
		planPresentStep,
		m6Step{text: "Plan ready for review."},
		m6Step{text: "Executing the approved plan."},
	)
	h.submit(t, "plan the change")
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()

	beforeMessages := len(h.userMessages(t))
	beforeCalls := h.provider.callCount()

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Approve") {
		t.Fatalf("plan review card missing Approve: %v", tt.Texts())
	}
	if err := tt.Click("Approve"); err != nil {
		t.Fatalf("click Approve: %v", err)
	}
	if h.ctrl.PlanMode(id) {
		t.Fatal("Approve left plan mode on")
	}
	h.ctrl.Wait()
	m6WaitIdle(t, h.ctrl)
	h.reload()

	if got := h.provider.callCount(); got != beforeCalls+1 {
		t.Fatalf("provider calls after Approve = %d, want %d (exactly one continuation)",
			got, beforeCalls+1)
	}
	messages := h.userMessages(t)
	if len(messages) != beforeMessages+1 {
		t.Fatalf("user messages after Approve = %d, want %d", len(messages), beforeMessages+1)
	}
	continuations := 0
	for _, message := range messages {
		if message.Content == planApprovedPrompt {
			continuations++
		}
	}
	if continuations != 1 {
		t.Fatalf("continuation prompts = %d, want exactly 1", continuations)
	}
	if last := messages[len(messages)-1]; last.Content != planApprovedPrompt {
		t.Fatalf("last user message = %q, want %q", last.Content, planApprovedPrompt)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "false" {
		t.Fatalf("persisted plan_mode after Approve = %q, want \"false\"", raw)
	}

	tt.Frame()
	if tt.HasText("Exit plan mode") || tt.HasText("Plan review") {
		t.Fatalf("plan review card still visible after Approve: %v", tt.Texts())
	}
}

// TestPlanModeExitNoContinuation proves Exit only clears the flag: no submit,
// no provider call, and the plan card disappears.
func TestPlanModeExitNoContinuation(t *testing.T) {
	h := newPlanHarness(t,
		planPresentStep,
		m6Step{text: "Plan ready for review."},
	)
	h.submit(t, "plan the change")
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()

	beforeMessages := len(h.userMessages(t))
	beforeCalls := h.provider.callCount()

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Exit plan mode") {
		t.Fatalf("plan review card missing Exit: %v", tt.Texts())
	}
	if err := tt.Click("Exit plan mode"); err != nil {
		t.Fatalf("click Exit plan mode: %v", err)
	}
	if h.ctrl.PlanMode(id) {
		t.Fatal("Exit plan mode left plan mode on")
	}
	h.ctrl.Wait()
	h.reload()

	if got := h.provider.callCount(); got != beforeCalls {
		t.Fatalf("provider calls after Exit = %d, want %d (no continuation)", got, beforeCalls)
	}
	if got := len(h.userMessages(t)); got != beforeMessages {
		t.Fatalf("user messages after Exit = %d, want %d (no continuation)", got, beforeMessages)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "false" {
		t.Fatalf("persisted plan_mode after Exit = %q, want \"false\"", raw)
	}
	tt.Frame()
	if tt.HasText("Plan review") || tt.HasText("Approve") {
		t.Fatalf("plan review card still visible after Exit: %v", tt.Texts())
	}
	if !tt.HasText("Presented") {
		// The presented artifact stays in the transcript; only the review
		// actions go away.
		t.Fatalf("presented plan disappeared from the transcript: %v", tt.Texts())
	}
}

// hasLabel reports whether some element of the last frame shows exactly text.
// HasText is a substring match, which cannot tell the retired header toggle
// ("Plan mode: Off") from the composer badge's longer accessible name.
func hasLabel(tt *ui.Tester, text string) bool {
	for _, shown := range tt.Texts() {
		if shown == text {
			return true
		}
	}
	return false
}

// testerAccessNode returns the accessibility role, value and states of the
// node labeled label. It reuses testerAccessStates (a11y_expanded_test.go) to
// turn the headless tester's accessibility tree on, then reads the node itself.
func testerAccessNode(t *testing.T, tt *ui.Tester, label string) (role uint8, value string, states uint64, ok bool) {
	t.Helper()
	states, ok = testerAccessStates(t, tt)[label]
	if !ok {
		return 0, "", 0, false
	}
	v := reflect.ValueOf(tt).Elem().FieldByName("h")
	if !v.IsValid() {
		t.Fatal("ui.Tester has no h field; update the accessibility test")
	}
	h := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	access := h.Elem().FieldByName("access")
	if !access.IsValid() || access.IsNil() {
		t.Fatal("ui.Tester did not build an accessibility tree")
	}
	tree := reflect.NewAt(access.Type(), unsafe.Pointer(access.UnsafeAddr())).Elem().Elem()
	nodes := tree.FieldByName("Nodes")
	for i := 0; i < nodes.Len(); i++ {
		n := nodes.Index(i)
		if n.FieldByName("Label").String() != label {
			continue
		}
		return uint8(n.FieldByName("Role").Uint()), n.FieldByName("Value").String(), states, true
	}
	return 0, "", states, false
}

// TestPlanCommandEnablesModeAndBadge is the /plan entry path: the command
// turns the persisted flag on, never reaches the model, and the composer shows
// the plan badge as an accessible button that leaves the mode when clicked.
func TestPlanCommandEnablesModeAndBadge(t *testing.T) {
	h := newPlanHarness(t)
	id := h.ctrl.CurrentSessionID()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge rendered while plan mode is off: %v", tt.Texts())
	}

	h.app.runSlashCommand("/plan")
	if !h.ctrl.PlanMode(id) {
		t.Fatal("/plan did not enable plan mode")
	}
	if h.app.draft != "" {
		t.Fatalf("/plan left the draft %q", h.app.draft)
	}
	if got := h.provider.callCount(); got != 0 {
		t.Fatalf("/plan model calls = %d, want 0 (the command runs locally)", got)
	}
	h.reload()
	tt.Frame()
	if !hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge missing with plan mode on: %v", tt.Texts())
	}
	if !tt.HasText("Plan") {
		t.Fatalf("plan badge label text missing: %v", tt.Texts())
	}

	// The badge is an accessible button, not a decorative chip.
	role, value, states, ok := testerAccessNode(t, tt, planBadgeLabelEN)
	if !ok {
		t.Fatalf("plan badge has no accessibility node labeled %q", planBadgeLabelEN)
	}
	if role != planBadgeRoleButton {
		t.Fatalf("plan badge role = %d, want button (%d)", role, planBadgeRoleButton)
	}
	if value != planBadgeLabelEN {
		t.Fatalf("plan badge value = %q, want %q", value, planBadgeLabelEN)
	}
	if states&accessFocusableState == 0 {
		t.Fatal("plan badge is not focusable")
	}

	// Clicking the badge leaves plan mode, exactly like /plan off.
	if err := tt.Click(planBadgeLabelEN); err != nil {
		t.Fatalf("click plan badge: %v", err)
	}
	if h.ctrl.PlanMode(id) {
		t.Fatal("clicking the plan badge left plan mode on")
	}
	tt.Frame()
	if hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge still rendered after the click: %v", tt.Texts())
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "false" {
		t.Fatalf("persisted plan_mode after badge click = %q, want \"false\"", raw)
	}
}

// TestPlanCommandOffTurnsModeOff covers /plan off: the argument is
// case-insensitive, it clears the draft, and the composer badge disappears.
func TestPlanCommandOffTurnsModeOff(t *testing.T) {
	h := newPlanHarness(t)
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge missing before /plan off: %v", tt.Texts())
	}

	h.app.runSlashCommand("/plan OFF")
	if h.ctrl.PlanMode(id) {
		t.Fatal("/plan OFF left plan mode on")
	}
	if h.app.draft != "" {
		t.Fatalf("/plan off left the draft %q", h.app.draft)
	}
	if got := h.provider.callCount(); got != 0 {
		t.Fatalf("/plan off model calls = %d, want 0", got)
	}
	h.reload()
	tt.Frame()
	if hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge still rendered after /plan off: %v", tt.Texts())
	}
}

// TestPlanCommandWithMessageEnablesAndSubmits proves /plan <message> turns
// plan mode on and submits only the message (never the slash line) as the
// next turn.
func TestPlanCommandWithMessageEnablesAndSubmits(t *testing.T) {
	const message = "write the plan tests"
	h := newPlanHarness(t, m6Step{text: "planning"})
	id := h.ctrl.CurrentSessionID()

	h.app.runSlashCommand("/plan " + message)
	m6WaitIdle(t, h.ctrl)
	h.ctrl.Wait()
	h.reload()

	if !h.ctrl.PlanMode(id) {
		t.Fatal("/plan <message> did not enable plan mode")
	}
	if got := h.provider.callCount(); got != 1 {
		t.Fatalf("model calls after /plan <message> = %d, want 1", got)
	}
	messages := h.userMessages(t)
	if len(messages) != 1 {
		t.Fatalf("user messages after /plan <message> = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != message {
		t.Fatalf("submitted message = %q, want %q", got, message)
	}
	if strings.Contains(messages[0].Content, "/plan") {
		t.Fatalf("the /plan command reached the model input: %q", messages[0].Content)
	}
	if h.app.draft != "" {
		t.Fatalf("/plan <message> left the draft %q", h.app.draft)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "true" {
		t.Fatalf("persisted plan_mode after /plan <message> = %q, want \"true\"", raw)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge missing after /plan <message>: %v", tt.Texts())
	}
}

// TestPlanCommandWithoutSessionToastsAndDoesNotPanic checks the Home/no-session
// case: typing /plan explains that a session is needed, clears the draft, and
// never panics or reaches the model.
func TestPlanCommandWithoutSessionToastsAndDoesNotPanic(t *testing.T) {
	h := newPlanHarnessNoSession(t)
	if id := h.ctrl.CurrentSessionID(); id != "" {
		t.Fatalf("fixture unexpectedly has a current session %q", id)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	// Choose the palette row the user would click. ui.Context.Toast is only
	// live while a frame builds, and clicking runs the command there, which is
	// how the user sees the toast.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("/plan")
	tt.Frame()
	if err := tt.Click("/plan"); err != nil {
		t.Fatalf("click /plan row: %v", err)
	}
	if h.app.draft != "" {
		t.Fatalf("/plan without a session left the draft %q", h.app.draft)
	}
	if got := h.provider.callCount(); got != 0 {
		t.Fatalf("/plan without a session model calls = %d, want 0", got)
	}
	// The toast fades in; advance past that so its text is part of the frame.
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Open a session before enabling plan mode.") {
		t.Fatalf("missing no-session toast: %v", tt.Texts())
	}
}
