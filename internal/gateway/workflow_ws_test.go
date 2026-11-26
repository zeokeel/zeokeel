package gateway

// workflow.* (M2): the definitions/runs snapshot, authoring, and one manual
// advance pass.
//
// The handlers are exercised directly, like the rework ones: what matters is the
// mapping between the wire shape and the backend contract (and that a build
// without the module says so), not the socket.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeWorkflows struct {
	workflows []contracts.Workflow
	runs      []contracts.WorkflowRun
	put       *contracts.Workflow
	deleted   string
	started   string
	advance   contracts.WorkflowReport
	putErr    error
}

func (f *fakeWorkflows) List(context.Context) ([]contracts.Workflow, error) { return f.workflows, nil }
func (f *fakeWorkflows) Get(_ context.Context, id string) (contracts.Workflow, bool, error) {
	for _, wf := range f.workflows {
		if wf.ID == id {
			return wf, true, nil
		}
	}
	return contracts.Workflow{}, false, nil
}
func (f *fakeWorkflows) Put(_ context.Context, wf contracts.Workflow) (contracts.Workflow, error) {
	if f.putErr != nil {
		return contracts.Workflow{}, f.putErr
	}
	f.put = &wf
	return wf, nil
}
func (f *fakeWorkflows) Delete(_ context.Context, id string) error             { f.deleted = id; return nil }
func (f *fakeWorkflows) Runs(context.Context) ([]contracts.WorkflowRun, error) { return f.runs, nil }
func (f *fakeWorkflows) Start(_ context.Context, id string) (contracts.WorkflowRun, error) {
	f.started = id
	return contracts.WorkflowRun{ID: "run-1", WorkflowID: id, Status: contracts.WorkflowRunRunning}, nil
}
func (f *fakeWorkflows) Advance(context.Context) (contracts.WorkflowReport, error) {
	return f.advance, nil
}

func workflowGateway(t *testing.T, service contracts.WorkflowService) *Gateway {
	t.Helper()
	gateway := newTestGateway(t, &fakeChat{})
	if service != nil {
		if _, err := gateway.root.Provide(contracts.KeyWorkflow, service); err != nil {
			t.Fatalf("provide workflow: %v", err)
		}
	}
	return gateway
}

func TestWorkflowSnapshotMapsDefinitionsAndRuns(t *testing.T) {
	service := &fakeWorkflows{
		workflows: []contracts.Workflow{{
			ID: "release", Name: "发布流程",
			Stages: []contracts.WorkflowStage{{ID: "collect", Name: "收集", Role: contracts.PresetExecutorID, DependsOn: []string{}, Tools: []string{"read_file"}}},
		}},
		runs: []contracts.WorkflowRun{{
			ID: "run-1", WorkflowID: "release", Status: contracts.WorkflowRunRunning,
			Stages: []contracts.WorkflowStageState{{StageID: "collect", TaskID: "task-1", SessionID: "s1", Status: contracts.WorkflowStageDispatched}},
		}},
	}
	gateway := workflowGateway(t, service)
	answer, problem := gateway.workflowSnapshot(context.Background(), nil)
	if problem != nil {
		t.Fatalf("workflowSnapshot: %+v", problem)
	}
	result, ok := answer.(workflowSnapshotResult)
	if !ok {
		t.Fatalf("answer = %T, want workflowSnapshotResult", answer)
	}
	if len(result.Workflows) != 1 || result.Workflows[0].Stages[0].ID != "collect" {
		t.Fatalf("workflows = %+v", result.Workflows)
	}
	if len(result.Workflows[0].Stages[0].Tools) != 1 {
		t.Fatalf("stage = %+v, want its tool binding carried over the wire", result.Workflows[0].Stages[0])
	}
	if len(result.Runs) != 1 || result.Runs[0].Stages[0].TaskID != "task-1" {
		t.Fatalf("runs = %+v", result.Runs)
	}
}

func TestWorkflowPutMapsTheDocument(t *testing.T) {
	service := &fakeWorkflows{}
	gateway := workflowGateway(t, service)
	body, err := json.Marshal(workflowPutParams{
		ID: "release", Name: "发布流程",
		Stages: []workflowStageParams{{ID: "collect", Role: "executor", Prompt: "收集变更", Acceptance: "列出文件"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, problem := gateway.workflowPut(context.Background(), body); problem != nil {
		t.Fatalf("workflowPut: %+v", problem)
	}
	if service.put == nil || service.put.ID != "release" || len(service.put.Stages) != 1 {
		t.Fatalf("stored = %+v, want the mapped document", service.put)
	}
	if service.put.Stages[0].Acceptance != "列出文件" {
		t.Fatalf("stage = %+v, want the acceptance line", service.put.Stages[0])
	}
}

// A validation failure is a request error (-32600), not an internal one: the
// client shows the message verbatim.
func TestWorkflowPutReportsValidationAsInvalidParams(t *testing.T) {
	service := &fakeWorkflows{putErr: contracts.ErrWorkflowInvalid}
	gateway := workflowGateway(t, service)
	body, _ := json.Marshal(workflowPutParams{ID: "x", Name: "y", Stages: []workflowStageParams{{ID: "a"}}})
	_, problem := gateway.workflowPut(context.Background(), body)
	if problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("problem = %+v, want codeInvalidParams", problem)
	}
}

func TestWorkflowDeleteAndRunStart(t *testing.T) {
	service := &fakeWorkflows{}
	gateway := workflowGateway(t, service)
	body, _ := json.Marshal(workflowIDParams{ID: "release"})
	if _, problem := gateway.workflowDelete(context.Background(), body); problem != nil {
		t.Fatalf("workflowDelete: %+v", problem)
	}
	if service.deleted != "release" {
		t.Fatalf("deleted = %q, want release", service.deleted)
	}
	answer, problem := gateway.workflowRunStart(context.Background(), body)
	if problem != nil {
		t.Fatalf("workflowRunStart: %+v", problem)
	}
	if service.started != "release" {
		t.Fatalf("started = %q, want release", service.started)
	}
	payload, ok := answer.(map[string]any)
	if !ok || payload["runId"] != "run-1" {
		t.Fatalf("answer = %+v, want the run id", answer)
	}
}

func TestWorkflowAdvanceReportsThePass(t *testing.T) {
	service := &fakeWorkflows{advance: contracts.WorkflowReport{Runs: 2, Dispatched: 3, Completed: 1}}
	gateway := workflowGateway(t, service)
	answer, problem := gateway.workflowAdvance(context.Background(), nil)
	if problem != nil {
		t.Fatalf("workflowAdvance: %+v", problem)
	}
	payload, ok := answer.(map[string]any)
	if !ok || payload["dispatched"] != 3 || payload["completed"] != 1 || payload["runs"] != 2 {
		t.Fatalf("answer = %+v", answer)
	}
}

// Without the module every workflow method is an explicit "unavailable", not an
// empty list that would read as "you have no workflows".
func TestWorkflowUnavailableWithoutTheModule(t *testing.T) {
	gateway := workflowGateway(t, nil)
	if _, problem := gateway.workflowSnapshot(context.Background(), nil); problem == nil {
		t.Fatal("workflowSnapshot answered without a backend")
	}
	if _, problem := gateway.workflowAdvance(context.Background(), nil); problem == nil {
		t.Fatal("workflowAdvance answered without a backend")
	}
}
