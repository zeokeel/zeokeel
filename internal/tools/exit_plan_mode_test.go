package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// planModeSession returns a context carrying sessionID with optional plan
// mode.
func planModeTool(t *testing.T, planOn bool) *ExitPlanModeTool {
	t.Helper()
	return NewExitPlanModeTool(ExitPlanModeConfig{PlanMode: func(sessionID string) bool {
		return planOn && sessionID == "s1"
	}})
}

func TestExitPlanModeToolSubmitsPlanForReview(t *testing.T) {
	tool := planModeTool(t, true)
	ctx := runtime.WithSessionID(context.Background(), "s1")
	args := json.RawMessage(`{"plan":"# Add plan parity\n1. Add the exit tool\n2. Keep present working"}`)
	out, err := tool.Invoke(ctx, args)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var result ExitPlanModeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, out)
	}
	if result.Kind != ExitPlanModeResultKind || !result.Submitted {
		t.Fatalf("result = %+v, want kind %q submitted", result, ExitPlanModeResultKind)
	}
	if result.Title != "Add plan parity" {
		t.Fatalf("title = %q, want the heading text", result.Title)
	}
	if !strings.Contains(result.Markdown, "Add the exit tool") {
		t.Fatalf("result lost the plan body: %q", result.Markdown)
	}
}

func TestExitPlanModeToolRejectsOutsidePlanMode(t *testing.T) {
	cases := []struct {
		name string
		tool *ExitPlanModeTool
		ctx  context.Context
	}{
		{"plan mode off", planModeTool(t, false), runtime.WithSessionID(context.Background(), "s1")},
		{"no session", planModeTool(t, true), context.Background()},
		{"unknown session", planModeTool(t, true), runtime.WithSessionID(context.Background(), "other")},
		{"no resolver", NewExitPlanModeTool(ExitPlanModeConfig{}), runtime.WithSessionID(context.Background(), "s1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.tool.Invoke(tc.ctx, json.RawMessage(`{"plan":"# Plan"}`))
			if err == nil {
				t.Fatal("Invoke outside plan mode succeeded")
			}
			if !strings.Contains(err.Error(), "plan mode is not active") {
				t.Fatalf("error = %q, want the plan-mode explanation", err)
			}
		})
	}
}

func TestExitPlanModeToolRejectsBadPlans(t *testing.T) {
	tool := planModeTool(t, true)
	ctx := runtime.WithSessionID(context.Background(), "s1")
	cases := []struct {
		name string
		args string
		want string
	}{
		{"missing plan", `{}`, `"plan" is required`},
		{"blank plan", `{"plan":"   "}`, `"plan" is required`},
		{"no heading", `{"plan":"1. do the thing"}`, `starting with a "#" heading`},
		{"heading without title", `{"plan":"#\n1. x"}`, `starting with a "#" heading`},
		{"invalid JSON", `{"plan":`, "invalid arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Invoke(ctx, json.RawMessage(tc.args))
			if err == nil {
				t.Fatal("Invoke accepted a bad plan")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestExitPlanModeToolRejectsSubagentCalls(t *testing.T) {
	tool := planModeTool(t, true)
	// A child agent runs with the session id and a subagent depth > 0; the
	// review surface belongs to the root agent's session.
	depthCtx := runtime.WithSubagentDepth(runtime.WithSessionID(context.Background(), "s1"), 1)
	_, err := tool.Invoke(depthCtx, json.RawMessage(`{"plan":"# Plan"}`))
	if err == nil || !strings.Contains(err.Error(), "root agent") {
		t.Fatalf("subagent call error = %v, want root-agent rejection", err)
	}
}

func TestPlanHeadingTitle(t *testing.T) {
	cases := []struct {
		plan  string
		title string
		ok    bool
	}{
		{"# Add parity\n1. x", "Add parity", true},
		{"\n\n  ## Nested heading\nbody", "Nested heading", true},
		{"#\tTab after hash\nbody", "Tab after hash", true},
		{"#Nospace\nbody", "", false},
		{"body first\n# Heading", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		title, ok := planHeadingTitle(tc.plan)
		if ok != tc.ok || title != tc.title {
			t.Errorf("planHeadingTitle(%q) = (%q, %v), want (%q, %v)", tc.plan, title, ok, tc.title, tc.ok)
		}
	}
}
