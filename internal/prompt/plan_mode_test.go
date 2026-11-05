package prompt

import (
	"strings"
	"testing"
)

// TestPlanModePromptPolicyIncluded proves the plan policy is gated by the
// explicit Context.PlanMode flag, tells the agent to investigate read-only and
// submit the plan through present with the title the shell's review card looks
// for, and then stop for approval.
func TestPlanModePromptPolicyIncluded(t *testing.T) {
	ctx := Context{
		WorkDir:  "/data/mygo",
		Language: "en",
		Tools:    allTools(),
	}
	off := Compose(ctx)
	if strings.Contains(off, "Plan mode is active") {
		t.Fatalf("plan policy present with PlanMode off:\n%s", off)
	}

	ctx.PlanMode = true
	on := Compose(ctx)
	for _, want := range []string{
		"Plan mode is active",
		"read-only",
		"`present`",
		`"Plan"`,
		"stop and wait for the user's approval",
	} {
		if !strings.Contains(on, want) {
			t.Fatalf("plan policy missing %q:\n%s", want, on)
		}
	}
	// The plan policy is still one section, in the DSH PLAN_POLICY slot, before
	// the file-reference guidance.
	planAt := strings.Index(on, "Plan mode is active")
	refAt := strings.Index(on, "Tokens prefixed with @")
	if planAt < 0 || refAt < 0 || planAt > refAt {
		t.Fatalf("plan policy order: plan@%d file-reference@%d\n%s", planAt, refAt, on)
	}

	zh := ctx
	zh.Language = "zh"
	if !strings.Contains(Compose(zh), "计划模式已启用") {
		t.Fatalf("Chinese plan policy missing:\n%s", Compose(zh))
	}
	// The legacy Mode spelling stays supported for prompt-only callers.
	legacy := Context{Language: "en", Mode: "plan", Tools: allTools()}
	if !strings.Contains(Compose(legacy), "Plan mode is active") {
		t.Fatalf("Mode=plan no longer includes the plan policy")
	}
}
