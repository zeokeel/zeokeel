package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// staticGate is a Gate with a fixed approval requirement and no waiting: a
// delegated run must fail before ever reaching Wait.
type staticGate struct {
	require bool
	waited  bool
}

func (g *staticGate) RequireApproval(name string, args json.RawMessage) bool { return g.require }

func (g *staticGate) Wait(ctx context.Context, callID string) (bool, error) {
	g.waited = true
	return false, nil
}

// TestDelegatedApprovalFailsClosedWithGuidance pins RT-05's safe half: a gated
// call inside a delegated run (Agent Teams member / subagent) fails with an
// actionable, non-retry message and never blocks on an approval channel that
// does not exist.
func TestDelegatedApprovalFailsClosedWithGuidance(t *testing.T) {
	gate := &staticGate{require: true}
	ctx := WithDelegatedApproval(context.Background())
	err := awaitGateApproval(ctx, gate, "call-1", "bash", nil, "")
	if err == nil {
		t.Fatal("awaitGateApproval in a delegated run = nil error, want fail-closed guidance")
	}
	if !strings.Contains(err.Error(), "no approval channel") {
		t.Fatalf("error = %q, want it to name the missing approval channel", err)
	}
	if !strings.Contains(err.Error(), "non-mutating tools") {
		t.Fatalf("error = %q, want actionable non-mutating tool guidance", err)
	}
	if gate.waited {
		t.Fatal("delegated run reached Gate.Wait even though it has no approval channel")
	}
}

// TestDelegatedApprovalSkipsAutoReview pins that the fail-closed check runs
// before the automatic reviewer: a reviewer allowed to see empty authorization
// evidence must not be able to auto-allow a gated call in a delegated run.
func TestDelegatedApprovalSkipsAutoReview(t *testing.T) {
	gate := &staticGate{require: true}
	reviewed := false
	tool := newEinoTool(&jsonTool{name: "bash"}, gate, func(ctx context.Context, in ToolReviewInput) (bool, string, error) {
		reviewed = true
		return true, "allowed", nil // would auto-allow if consulted
	}, nil)
	ctx := WithDelegatedApproval(context.Background())
	err := tool.awaitApproval(ctx, nil)
	if err == nil {
		t.Fatal("awaitApproval in a delegated run = nil error, want fail-closed guidance")
	}
	if reviewed {
		t.Fatal("automatic reviewer was consulted for a delegated run without authorization evidence")
	}
	if !strings.Contains(err.Error(), "no approval channel") {
		t.Fatalf("error = %q, want the delegated guidance", err)
	}
}

// TestNormalAgentTurnStillUsesExistingError pins that non-delegated runs keep
// their previous error text (no behavior change for ordinary tool use outside
// a turn).
func TestNormalAgentTurnStillUsesExistingError(t *testing.T) {
	gate := &staticGate{require: true}
	err := awaitGateApproval(context.Background(), gate, "call-2", "bash", nil, "")
	if err == nil || !strings.Contains(err.Error(), "outside an agent turn") {
		t.Fatalf("error = %v, want the existing outside-an-agent-turn error", err)
	}
}

// TestAcquireChildAgentDoesNotShareBusyAgent pins RT-05's concurrency half: two
// overlapping runs never receive the same eino ChatModelAgent (which eino does
// not support), and a released entry is reusable afterwards.
func TestAcquireChildAgentDoesNotShareBusyAgent(t *testing.T) {
	m := newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil })
	agent := newTestAgent(t, Config{einoModel: m})

	a1, _, release1, err := agent.acquireChildAgent(context.Background(), "worker")
	if err != nil {
		t.Fatalf("acquireChildAgent #1: %v", err)
	}

	a2, _, release2, err := agent.acquireChildAgent(context.Background(), "worker")
	if err != nil {
		t.Fatalf("acquireChildAgent #2: %v", err)
	}
	if a1 == a2 {
		t.Fatal("overlapping runs share one eino ChatModelAgent; eino agents must not be driven concurrently")
	}

	release2()
	release1()

	a3, _, release3, err := agent.acquireChildAgent(context.Background(), "worker")
	if err != nil {
		t.Fatalf("acquireChildAgent #3: %v", err)
	}
	defer release3()
	// release2 was a no-op (a2 was the private child of the overlapping run);
	// a3 must reuse the cached entry a1 that release1 marked idle again.
	if a3 != a1 {
		t.Fatalf("released cached child was not reused: got %p, want cached %p", a3, a1)
	}
}
