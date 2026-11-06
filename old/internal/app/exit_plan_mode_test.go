package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
)

// TestExitPlanModeToolWiring proves the production registry always exposes
// exit_plan_mode (a stable tool catalog), the tool is inert unless the calling
// session is in plan mode, and plan submission never asks for human approval.
// The prompt still delivers the legacy present-based plan for older sessions.
func TestExitPlanModeToolWiring(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)

	planOn := false
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		PlanModeForSession: func(sessionID string) bool {
			return planOn && sessionID == "session-1"
		},
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})
	for _, name := range []string{"exit_plan_mode", "present"} {
		if !registryHas(bundle, name) {
			t.Fatalf("%s is not model-visible: %v", name, bundle.Agent.ToolNames())
		}
	}
	if bundle.Gate == nil {
		t.Fatal("no approval gate")
	}
	if bundle.Gate.RequireApproval("exit_plan_mode", json.RawMessage(`{"plan":"# Plan"}`)) {
		t.Fatal("submitting a plan asks for human approval")
	}

	tool, ok := bundle.Agent.ToolByName("exit_plan_mode")
	if !ok {
		t.Fatal("exit_plan_mode tool not found")
	}
	planArgs := json.RawMessage(`{"plan":"# Ship plan parity\n1. Add the tool"}`)
	ctx := runtime.WithSessionID(context.Background(), "session-1")

	if _, err := tool.Invoke(ctx, planArgs); err == nil || !strings.Contains(err.Error(), "plan mode is not active") {
		t.Fatalf("call with plan mode off = %v, want plan-mode rejection", err)
	}

	planOn = true
	out, err := tool.Invoke(ctx, planArgs)
	if err != nil {
		t.Fatalf("call with plan mode on: %v", err)
	}
	var result struct {
		Kind     string `json:"kind"`
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("tool result is not JSON: %v", err)
	}
	if result.Kind != "plan_review" || result.Title != "Ship plan parity" || !strings.Contains(result.Markdown, "Add the tool") {
		t.Fatalf("tool result = %+v", result)
	}
	if _, err := tool.Invoke(runtime.WithSessionID(context.Background(), "session-2"), planArgs); err == nil {
		t.Fatal("another session inherited plan mode")
	}

	// Minimal mode hides every tool outside its terminal-oriented set; the
	// tool is exposed by the standard/creator presets, not everywhere.
	if err := bundle.Agent.SetMode(runtime.ModeMinimal); err != nil {
		t.Fatalf("SetMode(minimal): %v", err)
	}
	if _, ok := bundle.Agent.ToolByName("exit_plan_mode"); ok {
		t.Fatal("minimal mode still exposes exit_plan_mode")
	}
}

// TestExitPlanModeToolAutoReviewNotGated proves the converged Auto review
// pattern set does not add human approval for plan submission either: plan
// review has its own card, not the approval queue.
func TestExitPlanModeToolAutoReviewNotGated(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)

	bundle, err := buildAgentWithPermission(st, dir, "instruction", session.PermissionAutoReview)
	if err != nil {
		t.Fatalf("buildAgentWithPermission: %v", err)
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})
	if bundle.Gate.RequireApproval("exit_plan_mode", json.RawMessage(`{"plan":"# Plan"}`)) {
		t.Fatal("auto review asks for approval on exit_plan_mode")
	}
}
