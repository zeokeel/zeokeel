package runtime

import (
	"context"
	"testing"
)

// testTurnContextAgent builds an idle agent for the queue tests.
func testTurnContextAgent(t *testing.T) *Agent {
	t.Helper()
	return newTestAgent(t, Config{
		Provider: &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
			return []Chunk{{Text: "ok", Done: true}}, nil
		}},
	})
}

// TestTurnContextQueueIsPerAgent covers the reviewed leak: the queue must live
// on the agent instance (one process can rebuild agents for a session's
// workspace/permission and must not keep the old ones alive), it is consumed by
// exactly one turn, and Clear removes a pending message.
func TestTurnContextQueueIsPerAgent(t *testing.T) {
	agentA := testTurnContextAgent(t)
	agentB := testTurnContextAgent(t)

	if err := agentA.QueueTurnContext("s1", Message{Role: RoleUser, Content: "snapshot"}); err != nil {
		t.Fatalf("QueueTurnContext: %v", err)
	}
	if got := len(agentA.turnContexts); got != 1 {
		t.Fatalf("agent A pending contexts = %d, want 1", got)
	}
	if got := len(agentB.turnContexts); got != 0 {
		t.Fatalf("rebuilt agent leaked %d pending contexts from another agent", got)
	}

	got := agentA.takeTurnContext("s1")
	if len(got) != 1 || got[0].Content != "snapshot" || got[0].Role != RoleUser {
		t.Fatalf("takeTurnContext = %+v, want the queued user snapshot", got)
	}
	if len(agentA.turnContexts) != 0 {
		t.Fatalf("pending contexts after consume = %d, want 0", len(agentA.turnContexts))
	}

	if err := agentA.QueueTurnContext("s1", Message{Role: RoleUser, Content: "abandoned"}); err != nil {
		t.Fatalf("QueueTurnContext: %v", err)
	}
	agentA.ClearTurnContext("s1")
	if len(agentA.turnContexts) != 0 {
		t.Fatalf("pending contexts after Clear = %d, want 0", len(agentA.turnContexts))
	}
}
