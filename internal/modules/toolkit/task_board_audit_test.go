package toolkit

// The close-audit path of the shared task board (2026-11-26 audit, 高).
//
// Two entry points can move a task into completed without a lease review: the
// gateway's `task.status` command (recorded by the gateway, which owns that
// method) and the model's `team_task_update` tool (recorded here, at the one place
// that can see the tool's effect). Both must leave a row, and `SetStatus` must NOT
// record one by itself — TaskLeaseService.Accept completes the board through that
// same method right after recording its own reviewed decision.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// closeRecorder is the audit sink double: it keeps every recorded direct close.
type closeRecorder struct {
	rows []recordedClose
}

type recordedClose struct {
	taskID string
	actor  string
	source string
}

func (r *closeRecorder) RecordDirectClose(_ context.Context, taskID, actor, source string) error {
	r.rows = append(r.rows, recordedClose{taskID: taskID, actor: actor, source: source})
	return nil
}

// taskTool finds one model-facing task tool by name.
func taskTool(t *testing.T, tools []rig.Tool, name string) rig.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("%s tool missing", name)
	return nil
}

// TestTeamTaskUpdateCloseLeavesAnAuditRow is the model half of the finding: a
// completed task used to leave nothing behind but the board status.
func TestTeamTaskUpdateCloseLeavesAnAuditRow(t *testing.T) {
	tools, board, _ := activateTaskBoardToolkit(t)
	setter, ok := board.(contracts.TaskCloseObserverSetter)
	if !ok {
		t.Fatalf("task board %T cannot receive a close observer", board)
	}
	recorder := &closeRecorder{}
	setter.SetTaskCloseObserver(recorder)

	ctx := context.Background()
	task, err := board.Create(ctx, "ship it", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	update := taskTool(t, tools.Tools(), "team_task_update")
	result, err := update.Invoke(ctx, rig.ToolCall{
		ID: "u1", Name: "team_task_update",
		Arguments: `{"id":"` + task.ID + `","action":"complete"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("team_task_update = %+v/%v", result, err)
	}
	if len(recorder.rows) != 1 {
		t.Fatalf("recorded closes = %+v, want one", recorder.rows)
	}
	row := recorder.rows[0]
	if row.taskID != task.ID || row.actor != "agent" || row.source != "team_task_update" {
		t.Fatalf("recorded close = %+v, want the agent closing %s via the tool", row, task.ID)
	}

	// A second identical close is a no-op update on an already-completed task, so
	// it must not look like a fresh close.
	if _, err := update.Invoke(ctx, rig.ToolCall{
		ID: "u2", Name: "team_task_update",
		Arguments: `{"id":"` + task.ID + `","action":"complete"}`,
	}); err != nil {
		t.Fatalf("team_task_update again: %v", err)
	}
	if len(recorder.rows) != 1 {
		t.Fatalf("recorded closes after a no-op update = %+v, want still one", recorder.rows)
	}
}

// TestTeamTaskUpdateEditWithStatusIsACloseToo: the SDK also allows
// action=edit + status=completed, which is the same close spelled differently.
func TestTeamTaskUpdateEditWithStatusIsACloseToo(t *testing.T) {
	tools, board, _ := activateTaskBoardToolkit(t)
	recorder := &closeRecorder{}
	board.(contracts.TaskCloseObserverSetter).SetTaskCloseObserver(recorder)

	ctx := context.Background()
	task, err := board.Create(ctx, "ship it", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	update := taskTool(t, tools.Tools(), "team_task_update")
	result, err := update.Invoke(ctx, rig.ToolCall{
		ID: "u1", Name: "team_task_update",
		Arguments: `{"id":"` + task.ID + `","action":"edit","status":"completed"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("team_task_update edit = %+v/%v", result, err)
	}
	if len(recorder.rows) != 1 || recorder.rows[0].taskID != task.ID {
		t.Fatalf("recorded closes = %+v, want the edit-close recorded", recorder.rows)
	}
}

// TestSetStatusAloneDoesNotRecordAClose pins the boundary the other way round: the
// lease's own Accept completes the board through SetStatus, so a hook at that
// layer would stamp an "unaudited" row onto every properly reviewed task. The
// audited entry points record themselves; this one must stay silent.
func TestSetStatusAloneDoesNotRecordAClose(t *testing.T) {
	_, board, _ := activateTaskBoardToolkit(t)
	recorder := &closeRecorder{}
	board.(contracts.TaskCloseObserverSetter).SetTaskCloseObserver(recorder)

	ctx := context.Background()
	task, err := board.Create(ctx, "ship it", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := board.SetStatus(ctx, task.ID, contracts.TaskStatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if len(recorder.rows) != 0 {
		t.Fatalf("SetStatus recorded %+v; accepted leases complete tasks through it and must not be stamped unaudited", recorder.rows)
	}
	// The gateway's own command records the close itself, so the fact is not lost —
	// see the gateway test that drives task.status end to end.
	if rows, err := board.List(ctx); err != nil || len(rows) != 1 || rows[0].Status != contracts.TaskStatusCompleted {
		t.Fatalf("board after SetStatus = %+v/%v", rows, err)
	}
}

// TestBoardWithoutObserverIsInert: an assembly with no lease module has nowhere to
// record a close, and that must not panic or block the write.
func TestBoardWithoutObserverIsInert(t *testing.T) {
	tools, board, _ := activateTaskBoardToolkit(t)
	ctx := context.Background()
	task, err := board.Create(ctx, "ship it", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	update := taskTool(t, tools.Tools(), "team_task_update")
	result, err := update.Invoke(ctx, rig.ToolCall{
		ID: "u1", Name: "team_task_update",
		Arguments: `{"id":"` + task.ID + `","action":"complete"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("team_task_update without an observer = %+v/%v", result, err)
	}
	tasks, err := board.List(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].Status != contracts.TaskStatusCompleted {
		t.Fatalf("board after the close = %+v/%v", tasks, err)
	}
}
