package gateway

// tasks.rework.get / tasks.rework.reset (M1): the two read/write halves of the
// rework ceiling that the UI needs — "how many automatic rounds are left" and
// "give this escalated task another try".
//
// The handlers are exercised directly (not over a socket): what is being pinned
// here is the shape of the answer and the type assertion that makes a backend
// without the counter say so, not the transport, which every other method
// already covers.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeReworkLease satisfies contracts.TaskLeaseService by embedding it and
// overriding only the rework half; a call to any other method would panic, which
// is the intent (this test must not touch the lease state machine).
type fakeReworkLease struct {
	contracts.TaskLeaseService
	rounds  int
	max     int
	resets  int
	lastID  string
	exhaust bool
}

func (f *fakeReworkLease) Rework(_ context.Context, taskID string) (contracts.TaskRework, error) {
	f.lastID = taskID
	return contracts.TaskRework{TaskID: taskID, Rounds: f.rounds, Max: f.max, Exhausted: f.exhaust}, nil
}

func (f *fakeReworkLease) ResetRework(_ context.Context, taskID string) error {
	f.lastID = taskID
	f.resets++
	f.rounds = 0
	f.exhaust = false
	return nil
}

func reworkGateway(t *testing.T, lease contracts.TaskLeaseService) *Gateway {
	t.Helper()
	gateway := newTestGateway(t, &fakeChat{})
	if lease != nil {
		if _, err := gateway.root.Provide(contracts.KeyTaskLease, lease); err != nil {
			t.Fatalf("provide task lease: %v", err)
		}
	}
	return gateway
}

func TestTaskReworkGetReportsTheCeiling(t *testing.T) {
	lease := &fakeReworkLease{rounds: 2, max: 3}
	gateway := reworkGateway(t, lease)

	payload, err := json.Marshal(taskReworkParams{ID: "task-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	answer, problem := gateway.taskReworkGet(context.Background(), payload)
	if problem != nil {
		t.Fatalf("taskReworkGet: %+v", problem)
	}
	dto, ok := answer.(taskReworkDTO)
	if !ok {
		t.Fatalf("answer = %T, want taskReworkDTO", answer)
	}
	if dto.TaskID != "task-1" || dto.Rounds != 2 || dto.Max != 3 || dto.Exhausted {
		t.Fatalf("dto = %+v, want 2/3 not exhausted", dto)
	}
	if lease.lastID != "task-1" {
		t.Fatalf("lease read %q, want task-1", lease.lastID)
	}
}

func TestTaskReworkResetClearsTheCounter(t *testing.T) {
	lease := &fakeReworkLease{rounds: 3, max: 3, exhaust: true}
	gateway := reworkGateway(t, lease)

	payload, err := json.Marshal(taskReworkParams{ID: "task-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	answer, problem := gateway.taskReworkReset(context.Background(), payload)
	if problem != nil {
		t.Fatalf("taskReworkReset: %+v", problem)
	}
	if lease.resets != 1 {
		t.Fatalf("resets = %d, want 1", lease.resets)
	}
	dto, ok := answer.(taskReworkDTO)
	if !ok {
		t.Fatalf("answer = %T, want taskReworkDTO", answer)
	}
	if dto.Rounds != 0 || dto.Exhausted {
		t.Fatalf("dto = %+v, want a cleared counter", dto)
	}
}

// A backend that only implements the lease interface (no counter) must say so
// instead of answering "0 rounds", which would read as "plenty of retries".
func TestTaskReworkWithoutTheCounterIsUnavailable(t *testing.T) {
	gateway := reworkGateway(t, nil)
	payload, err := json.Marshal(taskReworkParams{ID: "task-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, problem := gateway.taskReworkGet(context.Background(), payload); problem == nil {
		t.Fatal("taskReworkGet answered without a lease service, want a capability error")
	}
}
