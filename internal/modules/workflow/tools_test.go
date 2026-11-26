package workflow_test

// Stage tool bindings (M3b): a stage that names tools must have that binding
// recorded on its task before the executor's prompt is sent — and must fail
// loudly when the product cannot record it, because a permission boundary that
// silently disappears is worse than a workflow that refuses to start.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/workflow"
)

type fakeBinder struct {
	mu       sync.Mutex
	tasks    []string
	bindings map[string][]string
	err      error
}

func (b *fakeBinder) SetTools(_ context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return contracts.TaskTools{}, b.err
	}
	if b.bindings == nil {
		b.bindings = map[string][]string{}
	}
	b.tasks = append(b.tasks, taskID)
	b.bindings[taskID] = append([]string(nil), tools...)
	return contracts.TaskTools{TaskID: taskID, Tools: tools}, nil
}

func pipelineWithTools() contracts.Workflow {
	wf := pipeline()
	wf.Stages[0].Tools = []string{"read_file", "grep"}
	return wf
}

func TestStageToolsAreBoundBeforeDispatch(t *testing.T) {
	board := newBoard()
	dispatcher := newDispatcher()
	binder := &fakeBinder{}
	svc := workflow.New(board, dispatcher, workflow.Config{}).WithToolBinder(binder)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipelineWithTools()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	run, err := svc.Start(ctx, "release")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	root := run.Stages[0]
	if len(binder.tasks) != 1 || binder.tasks[0] != root.TaskID {
		t.Fatalf("bound tasks = %v, want %q", binder.tasks, root.TaskID)
	}
	if got := binder.bindings[root.TaskID]; strings.Join(got, ",") != "read_file,grep" {
		t.Fatalf("binding = %v, want the stage's tools", got)
	}
	// The stage without tools must not be bound at all: no binding means "the
	// role decides", and inventing one would silently narrow it.
	if len(binder.tasks) != 1 {
		t.Fatalf("bound %d tasks, want only the stage that named tools", len(binder.tasks))
	}
}

func TestStageToolsFailClosedWithoutALeaseLayer(t *testing.T) {
	board := newBoard()
	dispatcher := newDispatcher()
	svc := workflow.New(board, dispatcher, workflow.Config{}) // no binder
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipelineWithTools()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := svc.Start(ctx, "release"); err != nil {
		// Start surfaces the dispatch failure through the run, not as an error.
		t.Fatalf("Start: %v", err)
	}
	report, err := svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.Errors) == 0 {
		t.Fatalf("report = %+v, want an error: the stage binds tools and nothing can record them", report)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("dispatched %d stage(s), want none: the executor must not start without its ceiling", len(dispatcher.calls))
	}
}

func TestStageToolsBindingErrorStopsTheStage(t *testing.T) {
	board := newBoard()
	dispatcher := newDispatcher()
	binder := &fakeBinder{err: contracts.ErrTaskToolsInvalid}
	svc := workflow.New(board, dispatcher, workflow.Config{}).WithToolBinder(binder)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipelineWithTools()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := svc.Start(ctx, "release"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	report, err := svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(report.Errors) == 0 || len(dispatcher.calls) != 0 {
		t.Fatalf("report=%+v calls=%d, want the stage refused", report, len(dispatcher.calls))
	}
}
