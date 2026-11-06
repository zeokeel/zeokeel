package shell

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
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
// the English UI, as the badge's Label, when plan mode is active and no plan
// is waiting for review.
const planBadgeLabelEN = "Plan mode on; click to exit"

// planBadgePendingLabelEN is the badge's label while a submitted plan waits
// for the user's decision.
const planBadgePendingLabelEN = "Plan review pending; click to open"

// planExitStep is the scripted exit_plan_mode call the review tests use. The
// plan starts with a "#" heading because the tool requires that shape.
var planExitStep = m6Step{
	tool: "exit_plan_mode",
	args: `{"plan":"# Plan\n1. Inspect internal/shell in read-only mode\n2. Add the exit tool and review card"}`,
}

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

	var ctrl *session.Controller
	registry := runtime.NewRegistry()
	if err := registry.Register(tools.NewPresentTool()); err != nil {
		t.Fatalf("register present: %v", err)
	}
	// exit_plan_mode resolves plan mode per calling session, exactly like the
	// production app's registryOptions.PlanModeForSession wiring.
	if err := registry.Register(tools.NewExitPlanModeTool(tools.ExitPlanModeConfig{
		PlanMode: func(sessionID string) bool {
			return ctrl != nil && ctrl.PlanMode(sessionID)
		},
	})); err != nil {
		t.Fatalf("register exit_plan_mode: %v", err)
	}
	provider := &m6Provider{steps: steps}
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
	if !hasLabel(tt, "Plan review") {
		t.Fatalf("plan review card missing after enabling plan mode: %v", tt.Texts())
	}
	for _, want := range []string{"Plan", "Approve", "Exit plan mode"} {
		if !tt.HasText(want) {
			t.Fatalf("plan review card missing %q: %v", want, tt.Texts())
		}
	}
	// Plan mode on with a submitted plan shows the composer badge in its
	// pending review state (the plan waits for Approve / Keep planning / Exit).
	if !hasLabel(tt, planBadgePendingLabelEN) {
		t.Fatalf("pending composer plan badge missing with plan under review: %v", tt.Texts())
	}
	if hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("plan badge shows the active state while review is pending: %v", tt.Texts())
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
	if tt.HasText("Exit plan mode") || hasLabel(tt, "Plan review") {
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
	if hasLabel(tt, "Plan review") || tt.HasText("Approve") {
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

// TestPlanModeExitToolShowsReviewCard proves the independent exit_plan_mode
// tool submits the plan for review instead of leaving plan mode: the review
// card shows the tool's plan, the composer badge switches to pending, and
// Approve then clears the mode and submits exactly one continuation.
func TestPlanModeExitToolShowsReviewCard(t *testing.T) {
	h := newPlanHarness(t,
		planExitStep,
		m6Step{text: "Plan submitted for review."},
		m6Step{text: "Executing the approved plan."},
	)
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()
	h.submit(t, "plan the change")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !hasLabel(tt, "Plan review") {
		t.Fatalf("exit_plan_mode did not open the review card: %v", tt.Texts())
	}
	for _, want := range []string{"Approve", "Keep planning", "Exit plan mode",
		"Inspect internal/shell in read-only mode", "Add the exit tool and review card"} {
		if !tt.HasText(want) {
			t.Fatalf("review card missing %q: %v", want, tt.Texts())
		}
	}
	if !hasLabel(tt, planBadgePendingLabelEN) {
		t.Fatalf("pending badge missing while the plan waits for review: %v", tt.Texts())
	}
	if !tt.HasText("Pending") {
		t.Fatalf("pending badge text missing: %v", tt.Texts())
	}
	// The pending state also carries its own accessible name/value and stays a
	// focusable button, exactly like the active badge.
	role, value, states, ok := testerAccessNode(t, tt, planBadgePendingLabelEN)
	if !ok {
		t.Fatalf("pending badge has no accessibility node labeled %q", planBadgePendingLabelEN)
	}
	if role != planBadgeRoleButton || value != planBadgePendingLabelEN || states&accessFocusableState == 0 {
		t.Fatalf("pending badge a11y = role %d value %q states %d, want a focusable button", role, value, states)
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("exit_plan_mode left plan mode before the user decided")
	}

	beforeCalls := h.provider.callCount()
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
		t.Fatalf("provider calls after Approve = %d, want %d", got, beforeCalls+1)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "false" {
		t.Fatalf("persisted plan_mode after Approve = %q, want \"false\"", raw)
	}
	tt.Frame()
	if hasLabel(tt, "Plan review") {
		t.Fatalf("review card still visible after Approve: %v", tt.Texts())
	}
}

// TestPlanModeKeepPlanningSubmitsFeedback proves Keep planning keeps plan mode
// and sends the feedback text as the next user message, then returns the badge
// to its active state until a revised plan arrives.
func TestPlanModeKeepPlanningSubmitsFeedback(t *testing.T) {
	h := newPlanHarness(t,
		planExitStep,
		m6Step{text: "Plan submitted for review."},
		m6Step{text: "Revising the plan."},
	)
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()
	h.submit(t, "plan the change")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	beforeCalls := h.provider.callCount()
	if err := tt.Click("Feedback for the agent (optional)"); err != nil {
		t.Fatalf("click feedback input: %v", err)
	}
	tt.Type("Add tests for the review branches")
	tt.Frame()
	if err := tt.Click("Keep planning"); err != nil {
		t.Fatalf("click Keep planning: %v", err)
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("Keep planning left plan mode")
	}
	m6WaitIdle(t, h.ctrl)
	h.ctrl.Wait()
	h.reload()
	if got := h.provider.callCount(); got != beforeCalls+1 {
		t.Fatalf("provider calls after Keep planning = %d, want %d", got, beforeCalls+1)
	}
	messages := h.userMessages(t)
	if len(messages) == 0 || messages[len(messages)-1].Content != "Add tests for the review branches" {
		t.Fatalf("last user message = %+v, want the feedback text", messages)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "true" {
		t.Fatalf("persisted plan_mode after Keep planning = %q, want \"true\"", raw)
	}
	tt.Frame()
	if hasLabel(tt, "Plan review") {
		t.Fatalf("review card still visible after Keep planning: %v", tt.Texts())
	}
	if hasLabel(tt, planBadgePendingLabelEN) {
		t.Fatalf("badge still pending after the user decided: %v", tt.Texts())
	}
	if !hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("active plan badge missing after Keep planning: %v", tt.Texts())
	}
}

// TestPlanModeReviewDismissKeepsPlan proves Dismiss and Escape close the review
// card without leaving plan mode, keep the badge pending, and that clicking the
// pending badge reopens the review.
func TestPlanModeReviewDismissKeepsPlan(t *testing.T) {
	h := newPlanHarness(t, planExitStep, m6Step{text: "Plan submitted for review."})
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()
	h.submit(t, "plan the change")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Dismiss"); err != nil {
		t.Fatalf("click Dismiss: %v", err)
	}
	tt.Frame()
	if !h.ctrl.PlanMode(id) {
		t.Fatal("Dismiss left plan mode")
	}
	if hasLabel(tt, "Plan review") {
		t.Fatalf("review card still visible after Dismiss: %v", tt.Texts())
	}
	if !hasLabel(tt, planBadgePendingLabelEN) {
		t.Fatalf("pending badge missing after Dismiss: %v", tt.Texts())
	}

	// The pending badge reopens the same review instead of leaving the mode.
	if err := tt.Click(planBadgePendingLabelEN); err != nil {
		t.Fatalf("click pending badge: %v", err)
	}
	tt.Frame()
	if !h.ctrl.PlanMode(id) {
		t.Fatal("clicking the pending badge left plan mode")
	}
	if !hasLabel(tt, "Plan review") {
		t.Fatalf("pending badge did not reopen the review: %v", tt.Texts())
	}

	// Escape is the global topmost-overlay shortcut: it dismisses the review
	// card and keeps plan mode (it must not fall through to stopping turns or
	// leaving the mode).
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !h.ctrl.PlanMode(id) {
		t.Fatal("Escape left plan mode")
	}
	if hasLabel(tt, "Plan review") {
		t.Fatalf("review card still visible after Escape: %v", tt.Texts())
	}
	if !hasLabel(tt, planBadgePendingLabelEN) {
		t.Fatalf("pending badge missing after Escape dismissed the review: %v", tt.Texts())
	}
}

// TestPlanModePresentArtifactStillShowsReview proves the legacy present path
// keeps working next to exit_plan_mode: a present artifact titled "Plan" opens
// the review card while plan mode is on.
func TestPlanModePresentArtifactStillShowsReview(t *testing.T) {
	h := newPlanHarness(t,
		planPresentStep,
		m6Step{text: "Plan ready for review."},
	)
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}
	h.reload()
	h.submit(t, "plan the change")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !hasLabel(tt, "Plan review") {
		t.Fatalf("present plan did not open the review card: %v", tt.Texts())
	}
	if !hasLabel(tt, planBadgePendingLabelEN) {
		t.Fatalf("pending badge missing for the present plan: %v", tt.Texts())
	}
}

// TestPlanCommandHomeCreatesSession proves /plan on the Home screen with no
// session now creates the draft session and turns plan mode on instead of only
// asking for a session.
func TestPlanCommandHomeCreatesSession(t *testing.T) {
	h := newPlanHarnessNoSession(t)
	if id := h.ctrl.CurrentSessionID(); id != "" {
		t.Fatalf("fixture unexpectedly has a current session %q", id)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("/plan")
	tt.Frame()
	if err := tt.Click("/plan"); err != nil {
		t.Fatalf("click /plan row: %v", err)
	}
	id := h.ctrl.CurrentSessionID()
	if id == "" {
		t.Fatal("/plan on Home did not create a draft session")
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("/plan on Home did not turn plan mode on")
	}
	if h.app.Screen != Session {
		t.Fatalf("screen after /plan on Home = %v, want Session", h.app.Screen)
	}
	if h.app.draft != "" {
		t.Fatalf("/plan on Home left the draft %q", h.app.draft)
	}
	if got := h.provider.callCount(); got != 0 {
		t.Fatalf("bare /plan model calls = %d, want 0", got)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "true" {
		t.Fatalf("persisted plan_mode after Home /plan = %q, want \"true\"", raw)
	}
	// Exactly one session was created: the command must not re-enter the Home
	// creation path once the Session screen is up.
	sessions, err := h.ctrl.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions after Home /plan = %d, want exactly 1", len(sessions))
	}
	tt.Frame()
	if !hasLabel(tt, planBadgeLabelEN) {
		t.Fatalf("active plan badge missing after Home /plan: %v", tt.Texts())
	}
}

// TestPlanCommandHomeWithMessageSubmitsFirstRequest proves /plan <message> on
// Home creates the session, turns plan mode on, and submits the message as the
// session's first request (never the slash line).
func TestPlanCommandHomeWithMessageSubmitsFirstRequest(t *testing.T) {
	h := newPlanHarnessNoSession(t, m6Step{text: "planning started"})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	typeComposer(t, tt, "/plan write the plan tests")
	m6WaitIdle(t, h.ctrl)
	h.ctrl.Wait()
	h.reload()

	id := h.ctrl.CurrentSessionID()
	if id == "" {
		t.Fatal("/plan <message> on Home did not create a session")
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("/plan <message> on Home did not turn plan mode on")
	}
	if got := h.provider.callCount(); got != 1 {
		t.Fatalf("model calls after Home /plan <message> = %d, want 1", got)
	}
	messages := h.userMessages(t)
	if len(messages) != 1 {
		t.Fatalf("user messages after Home /plan <message> = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "write the plan tests" {
		t.Fatalf("first user message = %q, want %q", got, "write the plan tests")
	}
	if h.app.Screen != Session {
		t.Fatalf("screen after Home /plan <message> = %v, want Session", h.app.Screen)
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "true" {
		t.Fatalf("persisted plan_mode = %q, want \"true\"", raw)
	}
}
