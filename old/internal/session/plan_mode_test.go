package session_test

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// TestPlanModePersistsPerSession covers the controller contract behind the
// runtime gate: plan mode defaults off for new sessions, is stored as
// session.<id>.plan_mode, stays scoped to its session, and is read back from
// the settings table by a fresh controller (a restart).
func TestPlanModePersistsPerSession(t *testing.T) {
	provider := &toolCallsThenTextProvider{}
	h := newModeHarness(t, provider, nil)
	id := h.sessionID

	if h.ctrl.PlanMode(id) {
		t.Fatal("new session starts in plan mode, want off")
	}
	if h.ctrl.PlanMode("") {
		t.Fatal("PlanMode(\"\") = true, want false")
	}

	if err := h.ctrl.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode(true): %v", err)
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("PlanMode after SetPlanMode(true) = false, want true")
	}
	raw, err := h.store.GetSetting(session.SessionPlanModeKey(id))
	if err != nil || raw != "true" {
		t.Fatalf("session.%s.plan_mode = %q, %v; want \"true\"", id, raw, err)
	}

	// A second session must not inherit the first one's plan mode.
	ctx := context.Background()
	other, err := h.ctrl.CreateSession(ctx, "other")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if h.ctrl.PlanMode(other) {
		t.Fatal("new sibling session inherited plan mode")
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(other)); raw != "false" {
		t.Fatalf("new session plan_mode = %q, want explicit \"false\"", raw)
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("creating another session changed the first session's plan mode")
	}

	// Opening the original session hydrates the persisted flag into the
	// controller cache instead of keeping the sibling's state.
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if !h.ctrl.PlanMode(id) {
		t.Fatal("reopened session lost plan mode")
	}
	if h.ctrl.PlanMode(other) {
		t.Fatal("opening the plan session leaked plan mode to the sibling")
	}

	// A fresh controller over the same store reads the durable value, exactly
	// like a restart.
	broker := state.NewBroker()
	t.Cleanup(func() { broker.Close() })
	restarted, err := session.New(session.Config{
		Store:     h.store,
		Broker:    broker,
		Projector: h.projector,
	})
	if err != nil {
		t.Fatalf("session.New restart: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if !restarted.PlanMode(id) {
		t.Fatal("restart lost persisted plan mode")
	}
	if restarted.PlanMode(other) {
		t.Fatal("restart applied plan mode to the sibling session")
	}

	if err := h.ctrl.SetPlanMode(id, false); err != nil {
		t.Fatalf("SetPlanMode(false): %v", err)
	}
	if h.ctrl.PlanMode(id) {
		t.Fatal("PlanMode after SetPlanMode(false) = true, want false")
	}
	if raw, _ := h.store.GetSetting(session.SessionPlanModeKey(id)); raw != "false" {
		t.Fatalf("after disable plan_mode = %q, want \"false\"", raw)
	}
}
