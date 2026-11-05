package agentteam

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// ctxCaptureRunner records the context each member run receives and returns
// immediately.
type ctxCaptureRunner struct {
	runCtx chan context.Context
}

func (r *ctxCaptureRunner) RunSubagent(ctx context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	select {
	case r.runCtx <- ctx:
	default:
	}
	return runtime.SubagentResult{Agent: req.Agent, Task: req.Task, Output: "done", Depth: req.Depth}, nil
}

// TestMemberRunInheritsSpawnContextValues pins the RT-05 seam on the Agent Teams
// side: the member's run context is derived from the spawning tool context, so
// values placed there (notably the turn approval channel) reach the delegated
// run.
func TestMemberRunInheritsSpawnContextValues(t *testing.T) {
	type spawnKey string
	const key spawnKey = "spawn-marker"
	runner := &ctxCaptureRunner{runCtx: make(chan context.Context, 4)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawnCtx := context.WithValue(waitCtx(t), key, "from-spawn")
	member, err := mgr.Spawn(spawnCtx, SpawnRequest{Task: "inspect", Agent: "worker"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case got := <-runner.runCtx:
		if value := got.Value(key); value != "from-spawn" {
			t.Fatalf("member run context value = %v, want from-spawn (spawn context passthrough)", value)
		}
		if runtime.HasTurnApproval(got) {
			t.Fatal("plain spawn context unexpectedly carries a turn approval channel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("member run never started")
	}
	if _, err := mgr.Wait(waitCtx(t), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// TestMemberNotCancelledBySpawnContext pins that the member keeps running on
// the manager-owned context: cancelling the spawning tool context must not
// interrupt an asynchronous member (only Interrupt/Close do).
func TestMemberNotCancelledBySpawnContext(t *testing.T) {
	runner := &scriptedRunner{started: make(chan runtime.SubagentRequest, 1), gate: make(chan struct{})}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawnCtx, cancelSpawn := context.WithCancel(waitCtx(t))
	member, err := mgr.Spawn(spawnCtx, SpawnRequest{Task: "long task"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("member run never started")
	}
	cancelSpawn() // the spawning turn ends while the member is still working
	close(runner.gate)

	got, err := mgr.Wait(waitCtx(t), member.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("member status = %q, want completed (spawn-context cancellation must not interrupt it)", got.Status)
	}
}
