package workflow_test

// The durable half (M2): definitions and runs survive a restart together, and a
// corrupt document is reported instead of silently emptied.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/workflow"
)

func TestDefinitionsAndRunsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	clock := time.Unix(1_700_000_000, 0)
	cfg := workflow.Config{Now: func() time.Time { return clock }}
	ctx := context.Background()
	board := newBoard()
	dispatcher := newDispatcher()

	first, err := workflow.Open(dir, board, dispatcher, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := first.Put(ctx, pipeline()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	run, err := first.Start(ctx, "release")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	second, err := workflow.Open(dir, board, dispatcher, cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok, err := second.Get(ctx, "release"); err != nil || !ok {
		t.Fatalf("definition after reopen = (%v, %v), want present", ok, err)
	}
	runs, err := second.Runs(ctx)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs after reopen = (%+v, %v), want the one run", runs, err)
	}
	if runs[0].Stages[0].TaskID != run.Stages[0].TaskID {
		t.Fatalf("stage task after reopen = %q, want %q", runs[0].Stages[0].TaskID, run.Stages[0].TaskID)
	}
}

func TestCorruptStoreIsReportedAndUsable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, workflow.StoreFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	service, err := workflow.Open(dir, newBoard(), newDispatcher(), workflow.Config{})
	if !errors.Is(err, workflow.ErrStoreCorrupt) {
		t.Fatalf("err = %v, want ErrStoreCorrupt", err)
	}
	if service == nil {
		t.Fatal("Open returned no service; the caller needs one to keep serving")
	}
	list, listErr := service.List(context.Background())
	if listErr != nil || len(list) != 0 {
		t.Fatalf("List = (%+v, %v), want an empty usable service", list, listErr)
	}
	// The file itself must still be there for the caller to quarantine.
	if _, err := os.Stat(filepath.Join(dir, workflow.StoreFileName)); err != nil {
		t.Fatalf("stat store: %v", err)
	}
}

// A definition replaced mid-run keeps the run's stage rows intact: the runner
// reports a stage that vanished rather than dropping the run.
func TestReplaceDefinitionKeepsLiveRun(t *testing.T) {
	svc, board, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.Put(ctx, pipeline()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	run, err := svc.Start(ctx, "release")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	trimmed := contracts.Workflow{ID: "release", Name: "发布流程", Stages: []contracts.WorkflowStage{{ID: "collect", Prompt: "收集变更"}}}
	if _, err := svc.Put(ctx, trimmed); err != nil {
		t.Fatalf("Put(replace): %v", err)
	}
	board.complete(run.Stages[0].TaskID)
	report, err := svc.Advance(ctx)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	// The old run still has stage rows for "test" and "audit"; the replaced
	// definition no longer has them. The run cannot complete, and that is
	// reported instead of the run hanging silently.
	runs, _ := svc.Runs(ctx)
	if runs[0].Status != contracts.WorkflowRunRunning {
		t.Fatalf("run = %+v, want still running (its stages have no definition)", runs[0])
	}
	if len(report.Errors) == 0 {
		t.Fatalf("report = %+v, want an error naming the vanished stage", report)
	}
}
