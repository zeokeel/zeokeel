package workflow_test

// The workflow backend (M2): definitions are validated documents, a run is a
// projection of board task states, and Advance is idempotent.
//
// The property that matters most here: the runner never invents "done". A stage
// is finished when the task the runner created is completed by the audit loop —
// the same loop M1 built. That is what makes a workflow a template for the
// existing machinery instead of a second scheduler.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/workflow"
)

type board struct {
	mu    sync.Mutex
	rows  map[string]contracts.BoardTask
	order []string
	next  int
}

func newBoard() *board {
	return &board{rows: map[string]contracts.BoardTask{}}
}

func (b *board) List(context.Context) ([]contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]contracts.BoardTask, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, b.rows[id])
	}
	return out, nil
}

func (b *board) Create(_ context.Context, subject, description string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := "task-" + subject
	task := contracts.BoardTask{ID: id, Subject: subject, Description: description, Status: contracts.TaskStatusPending}
	b.rows[id] = task
	b.order = append(b.order, id)
	return task, nil
}

func (b *board) SetStatus(_ context.Context, id, status string) (contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.rows[id]
	if !ok {
		return contracts.BoardTask{}, errors.New("unknown task")
	}
	task.Status = status
	b.rows[id] = task
	return task, nil
}

func (b *board) Delete(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.rows, id)
	return nil
}

// complete marks one task accepted, the way the audit loop's Accept does.
func (b *board) complete(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task := b.rows[id]
	task.Status = contracts.TaskStatusCompleted
	b.rows[id] = task
}

type dispatch struct {
	title, session, prompt string
	preset                 string
	task                   string
}

type fakeDispatcher struct {
	mu     sync.Mutex
	next   int
	calls  []dispatch
	preset map[string]string
	task   map[string]string
}

func newDispatcher() *fakeDispatcher {
	return &fakeDispatcher{preset: map[string]string{}, task: map[string]string{}}
}

func (d *fakeDispatcher) NewSession(title string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.next++
	return "sess-" + title, nil
}

func (d *fakeDispatcher) Send(sessionID, prompt string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, dispatch{session: sessionID, prompt: prompt, preset: d.preset[sessionID], task: d.task[sessionID]})
	return nil
}

func (d *fakeDispatcher) BindSessionTask(_ context.Context, sessionID, taskID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.task[sessionID] = taskID
	return nil
}

func (d *fakeDispatcher) SetSessionPreset(sessionID, presetID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.preset[sessionID] = presetID
	return nil
}

func pipeline() contracts.Workflow {
	return contracts.Workflow{
		ID:   "release",
		Name: "发布流程",
		Stages: []contracts.WorkflowStage{
			{ID: "collect", Role: contracts.PresetExecutorID, Prompt: "收集变更", Acceptance: "列出文件"},
			{ID: "test", Role: contracts.PresetExecutorID, Prompt: "跑测试", Acceptance: "全部通过", DependsOn: []string{"collect"}},
			{ID: "audit", Role: contracts.PresetAuditorID, Prompt: "复核报告", Acceptance: "有证据", DependsOn: []string{"test"}},
		},
	}
}

func newService(t *testing.T) (*workflow.Service, *board, *fakeDispatcher) {
	t.Helper()
	board := newBoard()
	dispatcher := newDispatcher()
	clock := time.Unix(1_700_000_000, 0)
	return workflow.New(board, dispatcher, workflow.Config{Now: func() time.Time { return clock }}), board, dispatcher
}

func TestPutValidatesAndNormalizes(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	wf := pipeline()
	wf.Stages[0].Name = "" // defaulted from the id
	stored, err := svc.Put(ctx, wf)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored.Stages[0].Name != "collect" {
		t.Fatalf("stored stage name = %q, want the id", stored.Stages[0].Name)
	}
	got, ok, err := svc.Get(ctx, "release")
	if err != nil || !ok || got.Name != "发布流程" {
		t.Fatalf("Get = (%+v, %v, %v)", got, ok, err)
	}

	broken := pipeline()
	broken.Stages[1].DependsOn = []string{"nope"}
	if _, err := svc.Put(ctx, broken); !errors.Is(err, contracts.ErrWorkflowInvalid) {
		t.Fatalf("Put(broken) err = %v, want ErrWorkflowInvalid", err)
	}
	if _, ok, _ := svc.Get(ctx, "release"); !ok {
		t.Fatal("a refused Put removed the previous definition")
	}
}

func TestDeleteRemovesTheDefinition(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipeline()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := svc.Delete(ctx, "release"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := svc.Get(ctx, "release"); ok {
		t.Fatal("definition still there after Delete")
	}
	if err := svc.Delete(ctx, "missing"); !errors.Is(err, contracts.ErrWorkflowNotFound) {
		t.Fatalf("Delete(missing) err = %v, want ErrWorkflowNotFound", err)
	}
}

func TestStartDispatchesOnlyTheRoots(t *testing.T) {
	svc, board, dispatcher := newService(t)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipeline()); err != nil {
		t.Fatalf("Put: %v", err)
	}

	run, err := svc.Start(ctx, "release")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.Status != contracts.WorkflowRunRunning || len(run.Stages) != 3 {
		t.Fatalf("run = %+v, want running with three stage rows", run)
	}
	// Only the root stage has a task and a session; the rest wait for it.
	root := run.Stages[0]
	if root.Status != contracts.WorkflowStageDispatched || root.TaskID == "" || root.SessionID == "" {
		t.Fatalf("root stage = %+v, want dispatched with task and session", root)
	}
	for _, stage := range run.Stages[1:] {
		if stage.Status != contracts.WorkflowStagePending || stage.TaskID != "" {
			t.Fatalf("dependent stage = %+v, want pending and undispatched", stage)
		}
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("dispatches = %d, want 1", len(dispatcher.calls))
	}
	call := dispatcher.calls[0]
	if call.preset != contracts.PresetExecutorID || call.task != root.TaskID {
		t.Fatalf("dispatch = %+v, want executor bound to the stage task", call)
	}
	if !strings.Contains(call.prompt, "收集变更") || !strings.Contains(call.prompt, "列出文件") {
		t.Fatalf("prompt = %q, want the stage prompt and its acceptance line", call.prompt)
	}

	// The stage task exists on the board.
	tasks, err := board.List(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].ID != root.TaskID {
		t.Fatalf("board = (%+v, %v), want the one stage task", tasks, err)
	}
}

func TestAdvanceFollowsBoardCompletion(t *testing.T) {
	svc, board, dispatcher := newService(t)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipeline()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	run, err := svc.Start(ctx, "release")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Nothing changed: Advance is a no-op for a waiting run.
	report, err := svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if report.Dispatched != 0 || len(dispatcher.calls) != 1 {
		t.Fatalf("report = %+v calls=%d, want nothing new", report, len(dispatcher.calls))
	}

	// The audit loop accepts the first stage's task.
	board.complete(run.Stages[0].TaskID)
	report, err = svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance after accept: %v", err)
	}
	if report.Dispatched != 1 {
		t.Fatalf("report = %+v, want the second stage dispatched", report)
	}
	if len(dispatcher.calls) != 2 {
		t.Fatalf("dispatches = %d, want 2", len(dispatcher.calls))
	}

	// Second stage completes → third stage (auditor role) dispatched.
	runs, err := svc.Runs(ctx)
	if err != nil || len(runs) != 1 {
		t.Fatalf("Runs = (%+v, %v)", runs, err)
	}
	board.complete(runs[0].Stages[1].TaskID)
	if _, err := svc.Advance(ctx); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if dispatcher.calls[2].preset != contracts.PresetAuditorID {
		t.Fatalf("third dispatch preset = %q, want auditor", dispatcher.calls[2].preset)
	}

	// Last stage completes → the run is done and further sweeps are quiet.
	board.complete(runs[0].Stages[2].TaskID)
	report, err = svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if report.Completed != 1 {
		t.Fatalf("report = %+v, want one completed run", report)
	}
	runs, _ = svc.Runs(ctx)
	if runs[0].Status != contracts.WorkflowRunDone {
		t.Fatalf("run = %+v, want done", runs[0])
	}
	before := len(dispatcher.calls)
	if report, err := svc.Advance(ctx); err != nil || report.Dispatched != 0 || len(dispatcher.calls) != before {
		t.Fatalf("final Advance = (%+v, %v) calls=%d, want a quiet pass", report, err, len(dispatcher.calls))
	}
}

func TestAdvanceReportsUnknownWorkflow(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipeline()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	run, err := svc.Start(ctx, "release")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Someone deleted the definition while the run was in flight: the run cannot
	// advance, and that must be reported rather than silently dropped.
	if err := svc.Delete(ctx, "release"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	board := newBoard()
	_ = board
	report, err := svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.Errors) == 0 || report.Completed != 0 {
		t.Fatalf("report = %+v, want an error for run %s", report, run.ID)
	}
}

func TestStartUnknownWorkflow(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Start(context.Background(), "nope"); !errors.Is(err, contracts.ErrWorkflowNotFound) {
		t.Fatalf("err = %v, want ErrWorkflowNotFound", err)
	}
}
