package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// alwaysAllowProvider scripts turns as tool call / final answer pairs, so a
// second and third Submit can prove whether the gate still prompts. Even calls
// request the gated echo tool; odd calls answer.
type alwaysAllowProvider struct {
	mu    sync.Mutex
	calls int
}

var _ runtime.Provider = (*alwaysAllowProvider)(nil)

func (p *alwaysAllowProvider) Stream(_ context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.mu.Unlock()

	var chunks []runtime.Chunk
	if call%2 == 0 {
		chunks = []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        fmt.Sprintf("call-%d", call/2+1),
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			Done: true,
		}}
	} else {
		chunks = []runtime.Chunk{{Text: "done", Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

func (p *alwaysAllowProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// approvalEchoTool is the gated tool every scripted turn calls.
type approvalEchoTool struct{ calls atomic.Int64 }

func (t *approvalEchoTool) Name() string        { return "echo" }
func (t *approvalEchoTool) Description() string { return "Echoes its JSON arguments." }
func (t *approvalEchoTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	t.calls.Add(1)
	return string(args), nil
}

// approvalApp is a real controller + agent over a temp store, with a fake
// provider and an approval gate that requires the echo tool.
type approvalApp struct {
	app  *App
	ctrl *session.Controller
	tool *approvalEchoTool
}

func newApprovalApp(t *testing.T, provider runtime.Provider) *approvalApp {
	t.Helper()
	return newApprovalAppWithReviewer(t, provider, nil)
}

// newApprovalAppWithReviewer is newApprovalApp with an automatic reviewer wired
// into the agent, so a reviewer denial falls back to the approval card.
func newApprovalAppWithReviewer(t *testing.T, provider runtime.Provider, reviewer runtime.ToolReviewer) *approvalApp {
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

	tool := &approvalEchoTool{}
	registry := runtime.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	gate := runtime.NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "echo"
	})
	agent, err := runtime.New(runtime.Config{
		Provider:     provider,
		Tools:        registry,
		Instruction:  "test agent",
		Gate:         gate,
		ToolReviewer: reviewer,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	ctrl, err := session.New(session.Config{
		Store:       st,
		Broker:      broker,
		Projector:   projector,
		Agent:       agent,
		Gate:        gate,
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
	// Pin the UI language so the card's button labels are stable regardless of
	// the product default or a developer's persisted setting.
	app.Language = LangEN
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	ctrl.OnUpdate(app.SetSnapshot)
	if _, err := ctrl.CreateSession(context.Background(), "approval"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	app.SetSnapshot(ctrl.Snapshot())
	return &approvalApp{app: app, ctrl: ctrl, tool: tool}
}

func waitApprovalPending(t *testing.T, ctrl *session.Controller) state.ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pending := ctrl.PendingApprovals(); len(pending) > 0 {
			return pending[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a pending approval; snapshot: %+v", ctrl.Snapshot())
	return state.ApprovalRequest{}
}

func waitApprovalGone(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(ctrl.PendingApprovals()) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pending approvals did not clear: %+v", ctrl.PendingApprovals())
}

func waitApprovalIdle(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().Phase == state.PhaseIdle {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for idle; snapshot: %+v", ctrl.Snapshot())
}

// TestApprovalAlwaysAllowButton drives the real controller, agent and approval
// queue through the shell: the card's third button must clear the pending call,
// run it, and let the next turn through without a prompt. Clearing the rules
// must restore the prompt.
func TestApprovalAlwaysAllowButton(t *testing.T) {
	provider := &alwaysAllowProvider{}
	h := newApprovalApp(t, provider)
	id := h.ctrl.CurrentSessionID()

	submit := func(prompt string) {
		t.Helper()
		if err := h.ctrl.Submit(context.Background(), id, prompt); err != nil {
			t.Fatalf("Submit(%q): %v", prompt, err)
		}
	}

	submit("first turn")
	req := waitApprovalPending(t, h.ctrl)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("Always allow") {
		t.Fatalf("approval card missing %q; texts: %v", "Always allow", tt.Texts())
	}
	if !tt.HasText("Allow") || !tt.HasText("Deny") {
		t.Fatalf("approval card missing the existing decisions; texts: %v", tt.Texts())
	}
	if err := tt.Click("Always allow"); err != nil {
		t.Fatalf("click Always allow: %v", err)
	}
	waitApprovalGone(t, h.ctrl)
	waitApprovalIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := h.tool.calls.Load(); got != 1 {
		t.Fatalf("tool calls after Always allow = %d, want 1", got)
	}
	if req.Name != "echo" {
		t.Fatalf("pending request name = %q, want echo", req.Name)
	}

	// The rule survives the turn: a second gated call runs without a prompt.
	submit("second turn")
	waitApprovalIdle(t, h.ctrl)
	h.ctrl.Wait()
	if pending := h.ctrl.PendingApprovals(); len(pending) != 0 {
		t.Fatalf("Always allow did not suppress the prompt: %+v", pending)
	}
	if got := h.tool.calls.Load(); got != 2 {
		t.Fatalf("tool calls after the always-allowed turn = %d, want 2", got)
	}
	if got := provider.callCount(); got != 4 {
		t.Fatalf("provider calls = %d, want 4 (two tool loops)", got)
	}

	// ClearApprovalRules is the escape hatch: the next call prompts again.
	h.ctrl.ClearApprovalRules()
	submit("third turn")
	req = waitApprovalPending(t, h.ctrl)
	if req.Name != "echo" {
		t.Fatalf("pending request after clearing rules = %+v, want echo", req)
	}
	if !h.ctrl.Approve(req.CallID) {
		t.Fatalf("Approve(%q) = false, want true", req.CallID)
	}
	waitApprovalGone(t, h.ctrl)
	waitApprovalIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := h.tool.calls.Load(); got != 3 {
		t.Fatalf("tool calls after clearing rules = %d, want 3", got)
	}

	if h.ctrl.ApproveAlways("missing-call") {
		t.Fatal("ApproveAlways(unknown) = true, want false")
	}
}

// TestControllerAllowAlwaysPassthrough covers the controller-level helper the
// app can call outside the card: AllowAlways suppresses future prompts and
// ApproveAlways reports false for an id it never saw.
func TestControllerAllowAlwaysPassthrough(t *testing.T) {
	provider := &alwaysAllowProvider{}
	h := newApprovalApp(t, provider)
	id := h.ctrl.CurrentSessionID()

	h.ctrl.AllowAlways("echo")
	if err := h.ctrl.Submit(context.Background(), id, "no prompt"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApprovalIdle(t, h.ctrl)
	h.ctrl.Wait()
	if pending := h.ctrl.PendingApprovals(); len(pending) != 0 {
		t.Fatalf("AllowAlways left a pending request: %+v", pending)
	}
	if got := h.tool.calls.Load(); got != 1 {
		t.Fatalf("tool calls = %d, want 1", got)
	}
	if h.ctrl.ApproveAlways("not-pending") {
		t.Fatal("ApproveAlways(unknown) = true, want false")
	}
}

// denyReviewer is an automatic reviewer that recommends denial with a reason.
func denyReviewer(reason string) runtime.ToolReviewer {
	return func(_ context.Context, _ runtime.ToolReviewInput) (bool, string, error) {
		return false, reason, nil
	}
}

// TestApprovalCardShowsAutoReviewReason proves the reviewer fallback renders on
// the real approval card: the card appears with the reviewer's recommended
// denial, and Allow overrides the recommendation and runs the tool.
func TestApprovalCardShowsAutoReviewReason(t *testing.T) {
	const reason = "deletes a pre-existing file"
	provider := &alwaysAllowProvider{}
	h := newApprovalAppWithReviewer(t, provider, denyReviewer(reason))
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "delete the file"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	request := waitApprovalPending(t, h.ctrl)
	if request.ReviewReason != reason {
		t.Fatalf("pending ReviewReason = %q, want %q", request.ReviewReason, reason)
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	wantLine := "Auto review suggested denial: " + reason
	if !tt.HasText(wantLine) {
		t.Fatalf("approval card missing the reviewer reason %q; texts: %v", wantLine, tt.Texts())
	}
	if !tt.HasText("Allow") || !tt.HasText("Deny") {
		t.Fatalf("approval card missing its decisions; texts: %v", tt.Texts())
	}

	// The user overrides the reviewer: the call runs.
	if err := tt.Click("Allow"); err != nil {
		t.Fatalf("click Allow: %v", err)
	}
	waitApprovalGone(t, h.ctrl)
	waitApprovalIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := h.tool.calls.Load(); got != 1 {
		t.Fatalf("tool calls after overriding a reviewer denial = %d, want 1", got)
	}
	tt.Frame()
	if tt.HasText(wantLine) {
		t.Fatalf("reviewer reason still rendered after the decision: %v", tt.Texts())
	}
}

// TestApprovalCardAutoReviewReasonDenyPath proves the user can confirm the
// reviewer's recommendation: Deny rejects the call, the tool never runs, and
// the model receives the ordinary user-rejection tool error.
func TestApprovalCardAutoReviewReasonDenyPath(t *testing.T) {
	const reason = "writes outside the workspace"
	provider := &alwaysAllowProvider{}
	h := newApprovalAppWithReviewer(t, provider, denyReviewer(reason))
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "write outside"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitApprovalPending(t, h.ctrl)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Auto review suggested denial: " + reason) {
		t.Fatalf("approval card missing the reviewer reason; texts: %v", tt.Texts())
	}
	if err := tt.Click("Deny"); err != nil {
		t.Fatalf("click Deny: %v", err)
	}
	waitApprovalGone(t, h.ctrl)
	waitApprovalIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := h.tool.calls.Load(); got != 0 {
		t.Fatalf("user-denied tool ran %d times, want 0", got)
	}
	if got := provider.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (tool call + final answer)", got)
	}
}
