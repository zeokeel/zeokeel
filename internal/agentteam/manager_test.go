package agentteam

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// scriptedRunner records calls and either returns immediately or blocks on a
// gate until the test releases it.
type scriptedRunner struct {
	mu      sync.Mutex
	calls   []runtime.SubagentRequest
	started chan runtime.SubagentRequest
	gate    chan struct{}
	err     error
}

func (r *scriptedRunner) RunSubagent(ctx context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	if r.started != nil {
		r.started <- req
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return runtime.SubagentResult{}, ctx.Err()
		}
	}
	if r.err != nil {
		return runtime.SubagentResult{}, r.err
	}
	return runtime.SubagentResult{Agent: req.Agent, Task: req.Task, Output: "done:" + req.Task, Depth: req.Depth}, nil
}

func (r *scriptedRunner) requests() []runtime.SubagentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.SubagentRequest(nil), r.calls...)
}

// blockingRunner blocks every call until the member context is canceled.
type blockingRunner struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (r *blockingRunner) RunSubagent(ctx context.Context, _ runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return runtime.SubagentResult{}, ctx.Err()
}

func (r *blockingRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func waitCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestManagerSpawnCompletesAndLists(t *testing.T) {
	runner := &scriptedRunner{}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawned, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "first task", Agent: "worker"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if spawned.ID == "" || spawned.Name != "worker" || spawned.Status != StatusRunning {
		t.Fatalf("spawned = %+v", spawned)
	}
	settled, err := mgr.Wait(waitCtx(t), spawned.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if settled.Status != StatusCompleted || settled.Output != "done:first task" || settled.Turns != 1 {
		t.Fatalf("settled = %+v", settled)
	}
	members := mgr.Members()
	if len(members) != 1 || members[0].ID != spawned.ID {
		t.Fatalf("Members = %+v", members)
	}
	reqs := runner.requests()
	if len(reqs) != 1 || reqs[0].Task != "first task" || reqs[0].Agent != "worker" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestManagerSendQueuesNextTurn(t *testing.T) {
	runner := &scriptedRunner{gate: make(chan struct{}), started: make(chan runtime.SubagentRequest, 4)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawned, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "first"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-runner.started

	delivery, err := mgr.Send(spawned.ID, "second")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if delivery.MessageID == "" || delivery.AgentID != spawned.ID || delivery.Pending != 1 {
		t.Fatalf("delivery = %+v", delivery)
	}
	close(runner.gate)

	settled, err := mgr.Wait(waitCtx(t), spawned.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if settled.Status != StatusCompleted || settled.Turns != 2 || settled.Output != "done:second" {
		t.Fatalf("settled = %+v", settled)
	}
	reqs := runner.requests()
	if len(reqs) != 2 || reqs[0].Task != "first" || reqs[1].Task != "second" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestManagerInterruptCancelsAndParksQueue(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}, 1)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawned, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "long", Agent: "worker"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-runner.started

	if _, err := mgr.Send(spawned.ID, "parked"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := mgr.Interrupt("worker"); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	settled, err := mgr.Wait(waitCtx(t), spawned.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if settled.Status != StatusInterrupted || settled.QueueLen != 1 {
		t.Fatalf("settled = %+v, want interrupted with one parked message", settled)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1 (parked message must not auto-start)", runner.callCount())
	}
}

func TestManagerSendResumesInterruptedMember(t *testing.T) {
	runner := &scriptedRunner{started: make(chan runtime.SubagentRequest, 4)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawned, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "first"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Wait(waitCtx(t), spawned.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if _, err := mgr.Send(spawned.ID, "follow-up"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	settled, err := mgr.Wait(waitCtx(t), spawned.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if settled.Status != StatusCompleted || settled.Turns != 2 {
		t.Fatalf("settled = %+v", settled)
	}
}

func TestManagerValidationAndTargetResolution(t *testing.T) {
	mgr := New(Config{})
	defer mgr.Close()
	if _, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "x"}); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("Spawn without runner error = %v, want ErrNoRunner", err)
	}
	if _, err := mgr.Spawn(context.Background(), SpawnRequest{Task: " "}); !errors.Is(err, ErrEmptyTask) {
		t.Fatalf("Spawn with blank task error = %v, want ErrEmptyTask", err)
	}
	mgr.SetRunner(&scriptedRunner{})
	if _, err := mgr.Send("missing", "hello"); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("Send unknown target error = %v, want ErrUnknownTarget", err)
	}
	if _, err := mgr.Send("agent-1", " "); !errors.Is(err, ErrEmptyMessage) {
		t.Fatalf("Send blank message error = %v, want ErrEmptyMessage", err)
	}

	first, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "one", Agent: "dup"})
	if err != nil {
		t.Fatalf("Spawn first: %v", err)
	}
	second, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "two", Agent: "dup"})
	if err != nil {
		t.Fatalf("Spawn second: %v", err)
	}
	if _, err := mgr.Wait(waitCtx(t), first.ID); err != nil {
		t.Fatalf("Wait first: %v", err)
	}
	if _, err := mgr.Wait(waitCtx(t), second.ID); err != nil {
		t.Fatalf("Wait second: %v", err)
	}
	if _, err := mgr.Send("dup", "hello"); !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("Send duplicate name error = %v, want ErrAmbiguousTarget", err)
	}
	byID, err := mgr.Send(first.ID, "hello")
	if err != nil {
		t.Fatalf("Send by id: %v", err)
	}
	if byID.AgentName != "dup" {
		t.Fatalf("delivery = %+v", byID)
	}
	if _, err := mgr.Wait(waitCtx(t), first.ID); err != nil {
		t.Fatalf("Wait first after send: %v", err)
	}
}

func TestManagerFailureAndLimit(t *testing.T) {
	runner := &scriptedRunner{err: fmt.Errorf("boom")}
	mgr := New(Config{Runner: runner, MaxAgents: 1})
	defer mgr.Close()

	spawned, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "fail"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	settled, err := mgr.Wait(waitCtx(t), spawned.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if settled.Status != StatusFailed || settled.Error != "boom" {
		t.Fatalf("settled = %+v", settled)
	}
	if _, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "second"}); !errors.Is(err, ErrTooManyAgents) {
		t.Fatalf("Spawn past limit error = %v, want ErrTooManyAgents", err)
	}
}
