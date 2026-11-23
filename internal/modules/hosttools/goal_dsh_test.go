package hosttools

// Behavioural tests for the DSH goal trio (get_goal/create_goal/update_goal) as
// the model actually sees them: real Rig tools resolved through the shared
// toolkit registry, backed by the per-session durable goal service seam.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// loadBridgeProductWithGoals loads hosttools with an optional per-session goal
// service (the DSH goal trio backend) provided on the root.
//
// The window-owned bridges (interaction/artifacts/plan) are optional services:
// this product is interactive=true but provides none of them, which is exactly
// what the goal trio needs — it reaches the goal service directly.
func loadBridgeProductWithGoals(t *testing.T, interactive bool, goals contracts.GoalService) *product {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if goals != nil {
		if _, err := root.Provide(contracts.KeyGoal, goals); err != nil {
			t.Fatalf("provide goal service: %v", err)
		}
	}
	rigRegistry := rigtool.NewRegistry()
	registry := newKernelRegistry(t, rigRegistry, nil)
	loader, err := module.Load(root, registry, []module.Entry{
		{ID: "toolkit", Module: "test/fake-toolkit"},
		{ID: "hosttools", Module: ModuleID, Config: map[string]any{"interactive": interactive}},
	})
	if err != nil {
		t.Fatalf("load hosttools product: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	return &product{root: root, loader: loader, registry: rigRegistry}
}

// goalToolOutput mirrors the Rig goal tool result.
type goalToolOutput struct {
	Goal *struct {
		ID            string `json:"id"`
		Revision      int    `json:"revision"`
		Objective     string `json:"objective"`
		Phase         string `json:"phase"`
		RoundsStarted int    `json:"roundsStarted"`
		MaxGoalRounds int    `json:"maxGoalRounds"`
		BlockedReason *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"blockedReason"`
	} `json:"goal"`
	Activation string `json:"activation"`
}

// goalCall invokes one goal tool with a session/origin-stamped context.
func goalCall(t *testing.T, product *product, sessionID, origin, name, arguments string) (rig.ToolResult, goalToolOutput) {
	t.Helper()
	ctx := context.Background()
	if sessionID != "" {
		ctx = contracts.WithAgentSession(ctx, sessionID)
	}
	ctx = contracts.WithAgentRunOrigin(ctx, origin)
	result, err := product.registry.Invoke(ctx, rig.ToolCall{ID: "call-1", Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var output goalToolOutput
	if !result.IsError {
		if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
			t.Fatalf("%s: decode output %q: %v", name, result.Content, err)
		}
	}
	return result, output
}

// goalCallOK invokes a goal tool and fails the test on a tool error.
func goalCallOK(t *testing.T, product *product, sessionID, origin, name, arguments string) goalToolOutput {
	t.Helper()
	result, output := goalCall(t, product, sessionID, origin, name, arguments)
	if result.IsError {
		t.Fatalf("%s returned a model-facing error: %s", name, result.Content)
	}
	return output
}

func newGoalProduct(t *testing.T, goals contracts.GoalService) *product {
	t.Helper()
	return loadBridgeProductWithGoals(t, true, goals)
}

// TestGoalTrioReplacesLegacyGoalTool pins the model surface: the DSH trio is
// registered and the retired Goal v1 `goal` tool is gone.
func TestGoalTrioReplacesLegacyGoalTool(t *testing.T) {
	product := loadProduct(t, nil, true)
	names := make(map[string]bool)
	for _, spec := range product.registry.Specs() {
		names[spec.Name] = true
	}
	for _, name := range []string{ToolGetGoal, ToolCreateGoal, ToolUpdateGoal} {
		if !names[name] {
			t.Fatalf("registry lacks DSH goal tool %q (have %v)", name, names)
		}
	}
	if names[ToolGoal] {
		t.Fatal("the retired Goal v1 `goal` tool is still registered")
	}
}

func TestGoalTrioRoundTrip(t *testing.T) {
	goals := newFakeGoalService()
	product := newGoalProduct(t, goals)

	empty := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolGetGoal, `{}`)
	if empty.Goal != nil {
		t.Fatalf("get_goal on a bare session = %+v, want a null goal", empty.Goal)
	}

	created := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"ship the release"}`)
	if created.Goal == nil || created.Goal.Objective != "ship the release" || created.Goal.Phase != "active" {
		t.Fatalf("create_goal = %+v, want an active goal", created.Goal)
	}
	if created.Goal.MaxGoalRounds != 256 {
		t.Fatalf("create_goal max rounds = %d, want the DSH default 256", created.Goal.MaxGoalRounds)
	}
	if created.Activation != "armed" {
		t.Fatalf("create_goal activation = %q, want armed", created.Activation)
	}

	current := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolGetGoal, `{}`)
	if current.Goal == nil || current.Goal.ID != created.Goal.ID || current.Goal.Revision != created.Goal.Revision {
		t.Fatalf("get_goal = %+v, want the created goal", current.Goal)
	}

	paused := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":1,"action":"pause"}`)
	if paused.Goal == nil || paused.Goal.Phase != "paused" || paused.Activation != "disarmed" {
		t.Fatalf("pause = %+v, want paused/disarmed", paused.Goal)
	}

	// A stale revision is rejected instead of overwriting the paused state.
	stale, _ := goalCall(t, product, "sess-1", contracts.AgentRunOriginUser, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":1,"action":"resume"}`)
	if !stale.IsError || !strings.Contains(stale.Content, "stale goal reference") {
		t.Fatalf("stale update = %q, want a stale-ref tool error", stale.Content)
	}

	resumed := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":2,"action":"resume"}`)
	if resumed.Goal == nil || resumed.Goal.Phase != "active" || resumed.Activation != "armed" {
		t.Fatalf("resume = %+v, want active/armed", resumed.Goal)
	}

	completed := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":3,"action":"complete"}`)
	if completed.Goal == nil || completed.Goal.Phase != "complete" {
		t.Fatalf("complete = %+v, want complete", completed.Goal)
	}
}

func TestGoalTrioRequiresDirectHumanTurn(t *testing.T) {
	goals := newFakeGoalService()
	product := newGoalProduct(t, goals)

	// An automatic continuation round can never create a goal.
	create, _ := goalCall(t, product, "sess-1", contracts.AgentRunOriginGoal, ToolCreateGoal, `{"objective":"self-imposed"}`)
	if !create.IsError || !strings.Contains(create.Content, "direct human turn") {
		t.Fatalf("create_goal from a goal round = %q, want a direct-human-turn error", create.Content)
	}

	// Seed a paused goal through the human path, then prove an automation run
	// cannot resume it.
	created := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"ship"}`)
	goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":1,"action":"pause"}`)
	resume, _ := goalCall(t, product, "sess-1", contracts.AgentRunOriginAutomation, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":2,"action":"resume"}`)
	if !resume.IsError || !strings.Contains(resume.Content, "direct human turn") {
		t.Fatalf("resume from automation = %q, want a direct-human-turn error", resume.Content)
	}
	// Completing from a goal round is the normal DSH completion path.
	completed := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginGoal, ToolUpdateGoal,
		`{"goal_id":"`+created.Goal.ID+`","revision":2,"action":"complete"}`)
	if completed.Goal == nil || completed.Goal.Phase != "complete" {
		t.Fatalf("complete from a goal round = %+v, want complete", completed.Goal)
	}
}

func TestGoalTrioBlockedNeedsThreeGoalRounds(t *testing.T) {
	goals := newFakeGoalService()
	product := newGoalProduct(t, goals)
	created := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"ship"}`)
	ref := `"goal_id":"` + created.Goal.ID + `","revision":1`

	goals.setRounds("sess-1", 2)
	early, _ := goalCall(t, product, "sess-1", contracts.AgentRunOriginGoal, ToolUpdateGoal,
		`{`+ref+`,"action":"blocked","blocked_reason":"needs credentials"}`)
	if !early.IsError || !strings.Contains(early.Content, "at least 3") {
		t.Fatalf("blocked at round 2 = %q, want the 3-round threshold error", early.Content)
	}

	goals.setRounds("sess-1", 3)
	blocked := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginGoal, ToolUpdateGoal,
		`{`+ref+`,"action":"blocked","blocked_reason":"needs credentials"}`)
	if blocked.Goal == nil || blocked.Goal.Phase != "blocked" || blocked.Goal.BlockedReason == nil {
		t.Fatalf("blocked at round 3 = %+v, want a blocked goal with a reason", blocked.Goal)
	}
	if blocked.Goal.BlockedReason.Code != contracts.GoalBlockedCodeModelReported {
		t.Fatalf("blocked reason code = %q, want %q", blocked.Goal.BlockedReason.Code, contracts.GoalBlockedCodeModelReported)
	}
	if blocked.Activation != "disarmed" {
		t.Fatalf("blocked activation = %q, want disarmed", blocked.Activation)
	}
}

func TestGoalTrioCreateDefaultsAndValidation(t *testing.T) {
	goals := newFakeGoalService()
	product := newGoalProduct(t, goals)

	custom := goalCallOK(t, product, "sess-1", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"ship","max_goal_rounds":12}`)
	if custom.Goal == nil || custom.Goal.MaxGoalRounds != 12 {
		t.Fatalf("custom cap = %+v, want 12", custom.Goal)
	}
	duplicate, _ := goalCall(t, product, "sess-1", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"second"}`)
	if !duplicate.IsError || !strings.Contains(duplicate.Content, "already exists") {
		t.Fatalf("create_goal over a live goal = %q, want the conflict error", duplicate.Content)
	}
	if blank, _ := goalCall(t, product, "sess-1", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"   "}`); !blank.IsError {
		t.Fatal("create_goal with a blank objective succeeded")
	}
	if missing, _ := goalCall(t, product, "", contracts.AgentRunOriginUser, ToolCreateGoal, `{"objective":"ship"}`); !missing.IsError ||
		!strings.Contains(missing.Content, "session-scoped") {
		t.Fatalf("create_goal without a session = %q, want the session-scoped error", missing.Content)
	}
}
