package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// planHarness is a live shell/agent/controller stack whose runtime resolves
// plan mode through the controller, exactly like the production app's
// registryOptions.PlanModeFunc wiring.
type planHarness struct {
	app      *App
	ctrl     *session.Controller
	provider *m6Provider
	store    *store.Store
}

func newPlanHarness(t *testing.T, steps ...m6Step) *planHarness {
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
		PlanModeFunc: func() bool {
			if ctrl == nil {
				return false
			}
			return ctrl.PlanMode(ctrl.CurrentSessionID())
		},
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

	app := &App{Screen: Session, Ctrl: ctrl}
	// Pin the UI language so the card's button labels are stable.
	app.Language = LangEN
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	modeUIStates.Delete(app)
	sessionRenderStates.Delete(app)
	ctrl.OnUpdate(app.SetSnapshot)
	if _, err := ctrl.CreateSession(context.Background(), "plan"); err != nil {
		t.Fatalf("CreateSession: %v", err)
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
// plan mode is on and a present artifact titled "Plan" exists, and that the
// header toggle is what turns it on.
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
	if !tt.HasText("Plan mode") {
		t.Fatalf("header plan-mode toggle missing: %v", tt.Texts())
	}

	// The header toggle is the user-facing switch; clicking it persists plan
	// mode and the card appears on the next frame.
	if err := tt.Click("Plan mode"); err != nil {
		t.Fatalf("click plan-mode toggle: %v", err)
	}
	if !h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
		t.Fatal("header toggle did not enable plan mode")
	}
	tt.Frame()
	if !tt.HasText("Plan review") {
		t.Fatalf("plan review card missing after enabling plan mode: %v", tt.Texts())
	}
	for _, want := range []string{"Plan", "Approve", "Exit plan mode"} {
		if !tt.HasText(want) {
			t.Fatalf("plan review card missing %q: %v", want, tt.Texts())
		}
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
