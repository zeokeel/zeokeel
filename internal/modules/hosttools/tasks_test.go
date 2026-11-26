package hosttools

// The task tools (M1): what an executor and an auditor actually call.
//
// Two names, two directions:
//   - task_submit: the executor proves the work is done (evidence required);
//     the first submission claims the lease if nobody holds it yet, so an
//     automatically dispatched run needs no separate claim step.
//   - task_review: the auditor decides (accept / reject). The reviewer is the
//     auditor ROLE, which is what the lease layer checks; the tool cannot widen
//     that, it can only phrase the call.
//
// Both refuse loudly instead of guessing: an unbound session, a task held by
// someone else, a missing evidence list and an unknown decision are all
// model-facing errors, never a silent no-op.

import (
	"context"
	"errors"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeLease struct {
	held     *contracts.TaskLease
	claims   int
	submits  int
	accepts  int
	rejects  int
	evidence []contracts.Evidence
	reviewer string
	note     string
}

func (f *fakeLease) Claim(_ context.Context, taskID, owner string) (contracts.TaskLease, error) {
	f.claims++
	if f.held != nil {
		return contracts.TaskLease{}, contracts.ErrLeaseHeld
	}
	f.held = &contracts.TaskLease{TaskID: taskID, Owner: owner, State: contracts.LeaseStateClaimed}
	return *f.held, nil
}

func (f *fakeLease) Lease(_ context.Context, taskID string) (contracts.TaskLease, bool, error) {
	if f.held == nil || f.held.TaskID != taskID {
		return contracts.TaskLease{}, false, nil
	}
	return *f.held, true, nil
}

func (f *fakeLease) Submit(_ context.Context, taskID, owner string, evidence []contracts.Evidence) (contracts.TaskLease, error) {
	if f.held == nil || f.held.Owner != owner {
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	f.submits++
	f.evidence = evidence
	f.held.State = contracts.LeaseStateReview
	return *f.held, nil
}

func (f *fakeLease) Accept(_ context.Context, taskID, reviewer, note string) (contracts.TaskLease, error) {
	f.accepts++
	f.reviewer, f.note = reviewer, note
	return contracts.TaskLease{TaskID: taskID, State: contracts.LeaseStateDone}, nil
}

func (f *fakeLease) Reject(_ context.Context, taskID, reviewer, reason string) (contracts.TaskLease, error) {
	f.rejects++
	f.reviewer, f.note = reviewer, reason
	return contracts.TaskLease{TaskID: taskID, State: contracts.LeaseStateRejected}, nil
}

func (f *fakeLease) SetTools(_ context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	return contracts.TaskTools{TaskID: taskID, Tools: tools}, nil
}

type fakeBinder struct{ task string }

func (b fakeBinder) SessionTask(string) (string, error) { return b.task, nil }

// taskTools builds the two tools over fixed fakes, the way the module builds
// them over lazy providers.
func taskTools(lease *fakeLease, binder fakeBinder) map[string]rig.Tool {
	out := map[string]rig.Tool{}
	for _, tool := range newTaskTools(taskDeps{
		lease:  func() (taskLeaseService, bool) { return lease, lease != nil },
		binder: func() (taskBinder, bool) { return binder, binder.task != "" },
	}) {
		out[tool.Name()] = tool
	}
	return out
}

func invoke(t *testing.T, tool rig.Tool, session, args string) rig.ToolResult {
	t.Helper()
	ctx := context.Background()
	if session != "" {
		ctx = contracts.WithAgentSession(ctx, session)
	}
	result, err := tool.Invoke(ctx, rig.ToolCall{Name: tool.Name(), Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool.Name(), err)
	}
	return result
}

func TestTaskSubmitClaimsThenSubmits(t *testing.T) {
	lease := &fakeLease{}
	tools := taskTools(lease, fakeBinder{task: "task-1"})

	result := invoke(t, tools[ToolTaskSubmit], "s1",
		`{"evidence":[{"kind":"output","ref":"go test ./...","note":"all green"}]}`)
	if result.IsError {
		t.Fatalf("task_submit errored: %q", result.Content)
	}
	if lease.claims != 1 || lease.submits != 1 {
		t.Fatalf("claims=%d submits=%d, want 1/1", lease.claims, lease.submits)
	}
	if len(lease.evidence) != 1 || lease.evidence[0].Ref != "go test ./..." {
		t.Fatalf("evidence = %+v, want the one submitted item", lease.evidence)
	}
	if !strings.Contains(result.Content, "task-1") {
		t.Fatalf("content = %q, want the task id", result.Content)
	}
}

func TestTaskSubmitRefusesWithoutEvidence(t *testing.T) {
	lease := &fakeLease{}
	tools := taskTools(lease, fakeBinder{task: "task-1"})
	result := invoke(t, tools[ToolTaskSubmit], "s1", `{"evidence":[]}`)
	if !result.IsError || lease.submits != 0 || lease.claims != 0 {
		t.Fatalf("result=%+v claims=%d submits=%d, want a refusal before any lease write", result, lease.claims, lease.submits)
	}
}

func TestTaskSubmitRefusesAnUnboundSession(t *testing.T) {
	lease := &fakeLease{}
	tools := taskTools(lease, fakeBinder{})
	if result := invoke(t, tools[ToolTaskSubmit], "s1", `{"evidence":[{"kind":"link","ref":"x"}]}`); !result.IsError {
		t.Fatalf("result = %+v, want an error for an unbound session", result)
	}
	if result := invoke(t, tools[ToolTaskSubmit], "", `{"evidence":[{"kind":"link","ref":"x"}]}`); !result.IsError {
		t.Fatalf("result = %+v, want an error without a session", result)
	}
}

func TestTaskSubmitRefusesATaskHeldByAnotherOwner(t *testing.T) {
	lease := &fakeLease{held: &contracts.TaskLease{TaskID: "task-1", Owner: "bot-9", State: contracts.LeaseStateClaimed}}
	tools := taskTools(lease, fakeBinder{task: "task-1"})
	result := invoke(t, tools[ToolTaskSubmit], "s1", `{"evidence":[{"kind":"link","ref":"x"}]}`)
	if !result.IsError || lease.submits != 0 {
		t.Fatalf("result=%+v submits=%d, want a refusal and no submit", result, lease.submits)
	}
	if !strings.Contains(result.Content, "bot-9") {
		t.Fatalf("content = %q, want the current owner named", result.Content)
	}
}

func TestTaskReviewAcceptsAsTheAuditorRole(t *testing.T) {
	lease := &fakeLease{held: &contracts.TaskLease{TaskID: "task-1", Owner: contracts.PresetExecutorID, State: contracts.LeaseStateReview}}
	tools := taskTools(lease, fakeBinder{task: "task-1"})

	result := invoke(t, tools[ToolTaskReview], "s1", `{"decision":"accept","note":"证据齐全"}`)
	if result.IsError || lease.accepts != 1 {
		t.Fatalf("result=%+v accepts=%d, want one acceptance", result, lease.accepts)
	}
	if lease.reviewer != contracts.PresetAuditorID || lease.note != "证据齐全" {
		t.Fatalf("reviewer=%q note=%q, want the auditor role and the note", lease.reviewer, lease.note)
	}
}

func TestTaskReviewRejectRequiresANote(t *testing.T) {
	lease := &fakeLease{held: &contracts.TaskLease{TaskID: "task-1", Owner: contracts.PresetExecutorID, State: contracts.LeaseStateReview}}
	tools := taskTools(lease, fakeBinder{task: "task-1"})
	if result := invoke(t, tools[ToolTaskReview], "s1", `{"decision":"reject"}`); !result.IsError || lease.rejects != 0 {
		t.Fatalf("result=%+v rejects=%d, want a refusal for a reasonless rejection", result, lease.rejects)
	}
	if result := invoke(t, tools[ToolTaskReview], "s1", `{"decision":"reject","note":"缺来源"}`); result.IsError || lease.rejects != 1 {
		t.Fatalf("result=%+v rejects=%d, want one rejection", result, lease.rejects)
	}
}

func TestTaskReviewRefusesAnUnknownDecision(t *testing.T) {
	lease := &fakeLease{held: &contracts.TaskLease{TaskID: "task-1", Owner: contracts.PresetExecutorID, State: contracts.LeaseStateReview}}
	tools := taskTools(lease, fakeBinder{task: "task-1"})
	result := invoke(t, tools[ToolTaskReview], "s1", `{"decision":"maybe","note":"x"}`)
	if !result.IsError || lease.accepts != 0 || lease.rejects != 0 {
		t.Fatalf("result=%+v accepts=%d rejects=%d, want a refusal", result, lease.accepts, lease.rejects)
	}
}

// A lease layer that is not assembled must be a readable tool error: the model
// can then say "this product has no task board" instead of crashing the run.
func TestTaskToolsWithoutALeaseLayer(t *testing.T) {
	tools := map[string]rig.Tool{}
	for _, tool := range newTaskTools(taskDeps{}) {
		tools[tool.Name()] = tool
	}
	result := invoke(t, tools[ToolTaskSubmit], "s1", `{"evidence":[{"kind":"link","ref":"x"}]}`)
	if !result.IsError {
		t.Fatalf("result = %+v, want an error without a lease layer", result)
	}
}

// errors.Is plumbing: the tool must not swallow the context's cancellation.
func TestTaskToolsPropagateContextCancellation(t *testing.T) {
	lease := &fakeLease{}
	tools := taskTools(lease, fakeBinder{task: "task-1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tools[ToolTaskSubmit].Invoke(ctx, rig.ToolCall{Arguments: `{"evidence":[{"kind":"link","ref":"x"}]}`}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
