package app

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// TestPlanModeProductionPromptWiring proves the app's registryOptions
// PlanModeFunc reaches the per-turn instruction, so the production agent gets
// the plan:policy section as soon as plan mode is on (and loses it when the
// mode is cleared), without rebuilding the registry.
func TestPlanModeProductionPromptWiring(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)

	planOn := false
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		PlanModeFunc: func() bool { return planOn },
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	if bundle == nil {
		t.Fatal("no agent bundle")
	}
	t.Cleanup(func() {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
	})

	if strings.Contains(bundle.Agent.Instruction(), "Plan mode is active") {
		t.Fatalf("plan policy present before plan mode:\n%s", bundle.Agent.Instruction())
	}

	// SetMode recomposes the instruction through InstructionFunc, the same path
	// Run uses at the start of every turn.
	planOn = true
	if err := bundle.Agent.SetMode(runtime.ModeStandard); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	on := bundle.Agent.Instruction()
	for _, want := range []string{"Plan mode is active", "`exit_plan_mode`", "`#` heading"} {
		if !strings.Contains(on, want) {
			t.Fatalf("production prompt after enabling plan mode missing %q:\n%s", want, on)
		}
	}

	planOn = false
	if err := bundle.Agent.SetMode(runtime.ModeStandard); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if strings.Contains(bundle.Agent.Instruction(), "Plan mode is active") {
		t.Fatalf("plan policy survived disabling plan mode:\n%s", bundle.Agent.Instruction())
	}
}
