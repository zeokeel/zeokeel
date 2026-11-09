package toolkit

// Tests for the shared task-board service and its model-facing tools.

import (
	"context"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// activateTaskBoardToolkit activates the toolkit and returns the registry, the
// task board and the loader.
func activateTaskBoardToolkit(t *testing.T) (*rigtool.Registry, contracts.TaskBoard, *module.Loader) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: map[string]any{"root": "."},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require KeyTaskBoard: %v", err)
	}
	return tools, board, loader
}

func TestTaskBoardCreateListStatusDelete(t *testing.T) {
	_, board, _ := activateTaskBoardToolkit(t)
	ctx := context.Background()
	task, err := board.Create(ctx, "  Ship the task board  ", " details ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID == "" || task.Subject != "Ship the task board" || task.Description != "details" ||
		task.Status != contracts.TaskStatusPending || task.Created.IsZero() || task.Updated.IsZero() {
		t.Fatalf("created task = %+v", task)
	}
	tasks, err := board.List(ctx)
	if err != nil || len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("List = %+v/%v", tasks, err)
	}
	updated, err := board.SetStatus(ctx, task.ID, contracts.TaskStatusInProgress)
	if err != nil || updated.Status != contracts.TaskStatusInProgress {
		t.Fatalf("SetStatus = %+v/%v", updated, err)
	}
	completed, err := board.SetStatus(ctx, task.ID, contracts.TaskStatusCompleted)
	if err != nil || completed.Status != contracts.TaskStatusCompleted {
		t.Fatalf("SetStatus completed = %+v/%v", completed, err)
	}
	if err := board.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if tasks, err := board.List(ctx); err != nil || len(tasks) != 0 {
		t.Fatalf("List after delete = %+v/%v", tasks, err)
	}
	if _, err := board.SetStatus(ctx, task.ID, contracts.TaskStatusPending); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("SetStatus after delete = %v, want unknown id", err)
	}
}

func TestTaskBoardValidation(t *testing.T) {
	_, board, _ := activateTaskBoardToolkit(t)
	ctx := context.Background()
	if _, err := board.Create(ctx, "   ", "description"); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("Create(blank subject) = %v", err)
	}
	task, err := board.Create(ctx, "valid", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := board.SetStatus(ctx, task.ID, "bogus"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("SetStatus(bogus) = %v", err)
	}
	if _, err := board.SetStatus(ctx, "missing", contracts.TaskStatusPending); err == nil {
		t.Fatal("SetStatus(unknown) returned nil error")
	}
	if err := board.Delete(ctx, "missing"); err == nil {
		t.Fatal("Delete(unknown) returned nil error")
	}
}

func TestTaskBoardSharesManagerWithTools(t *testing.T) {
	tools, board, _ := activateTaskBoardToolkit(t)
	var create rig.Tool
	for _, tool := range tools.Tools() {
		if tool.Name() == "team_task_create" {
			create = tool
			break
		}
	}
	if create == nil {
		t.Fatalf("team_task_create tool missing: %v", tools.Specs())
	}
	result, err := create.Invoke(context.Background(), rig.ToolCall{
		ID: "create-1", Name: "team_task_create", Arguments: `{"subject":"from tool","description":"shared"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("team_task_create = %+v/%v", result, err)
	}
	tasks, err := board.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Subject != "from tool" || tasks[0].Status != contracts.TaskStatusPending {
		t.Fatalf("board list after tool create = %+v/%v", tasks, err)
	}
}

func TestTaskBoardConcurrent(t *testing.T) {
	_, board, _ := activateTaskBoardToolkit(t)
	var wg sync.WaitGroup
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := board.Create(context.Background(), "concurrent", ""); err != nil {
				t.Errorf("Create: %v", err)
			}
			if _, err := board.List(context.Background()); err != nil {
				t.Errorf("List: %v", err)
			}
		}()
	}
	wg.Wait()
	tasks, err := board.List(context.Background())
	if err != nil || len(tasks) != 8 {
		t.Fatalf("tasks = %d/%v, want 8", len(tasks), err)
	}
}

func TestTaskBoardServiceRemovedOnDispose(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: map[string]any{"root": "."},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	if _, err := root.Require(contracts.KeyTaskBoard); err != nil {
		t.Fatalf("require KeyTaskBoard: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if _, err := root.Require(contracts.KeyTaskBoard); err == nil {
		t.Fatal("KeyTaskBoard survived module disposal")
	}
}

// createTaskWithTool creates one task through the model-facing team_task_create
// tool (the board API has no dependency/write-scope parameters).
func createTaskWithTool(t *testing.T, tools *rigtool.Registry, arguments string) rig.ToolResult {
	t.Helper()
	result, err := tools.Invoke(context.Background(), rig.ToolCall{
		ID: "create-deps", Name: "team_task_create", Arguments: arguments,
	})
	if err != nil || result.IsError {
		t.Fatalf("team_task_create %s = %+v/%v", arguments, result, err)
	}
	return result
}

// TestTaskBoardMapsDependenciesAndWriteScopes pins that the product BoardTask
// carries the rig board's BlockedBy/WriteScopes (the old conversion dropped
// them, so the inspector could never show a dependency or a blocker).
func TestTaskBoardMapsDependenciesAndWriteScopes(t *testing.T) {
	tools, board, _ := activateTaskBoardToolkit(t)
	ctx := context.Background()
	first, err := board.Create(ctx, "first task", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	createTaskWithTool(t, tools, `{"subject":"second task","blocked_by":["`+first.ID+`"],"write_scopes":["docs/a.md","docs/b.md"]}`)
	tasks, err := board.List(ctx)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("List = %+v/%v", tasks, err)
	}
	var second contracts.BoardTask
	for _, task := range tasks {
		if task.Subject == "second task" {
			second = task
		}
	}
	if len(second.BlockedBy) != 1 || second.BlockedBy[0] != first.ID {
		t.Fatalf("BlockedBy = %v, want [%s]", second.BlockedBy, first.ID)
	}
	if len(second.WriteScopes) != 2 || second.WriteScopes[0] != "docs/a.md" {
		t.Fatalf("WriteScopes = %v, want the tool's scopes", second.WriteScopes)
	}
	// The lists are copies: mutating the returned task cannot touch the board.
	second.BlockedBy[0] = "mutated"
	tasks, _ = board.List(ctx)
	for _, task := range tasks {
		if task.Subject == "second task" && task.BlockedBy[0] != first.ID {
			t.Fatalf("BlockedBy aliased the manager: %v", task.BlockedBy)
		}
	}
}

// TestTaskBoardAssign pins the optional contracts.TaskAssigner: claim,
// reassign, the pseudo lead owner of an in-progress task without an assignee,
// and the manager's in-progress-owner rule.
func TestTaskBoardAssign(t *testing.T) {
	_, board, _ := activateTaskBoardToolkit(t)
	ctx := context.Background()
	assigner, ok := board.(contracts.TaskAssigner)
	if !ok {
		t.Fatalf("task board %T does not implement contracts.TaskAssigner", board)
	}
	task, err := board.Create(ctx, "claim me", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assigned, err := assigner.Assign(ctx, task.ID, "研究员")
	if err != nil || assigned.Owner != "研究员" {
		t.Fatalf("Assign = %+v/%v, want owner 研究员", assigned, err)
	}
	// Starting a task without an owner stamps the board API's pseudo owner, and
	// the contract names that value so the UI can show it as the lead.
	unowned, err := board.Create(ctx, "unowned work", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	started, err := board.SetStatus(ctx, unowned.ID, contracts.TaskStatusInProgress)
	if err != nil || started.Owner != contracts.TaskOwnerUnassigned {
		t.Fatalf("SetStatus(in_progress) = %+v/%v, want owner %q", started, err, contracts.TaskOwnerUnassigned)
	}
	if _, err := board.SetStatus(ctx, task.ID, contracts.TaskStatusInProgress); err != nil {
		t.Fatalf("SetStatus(owned in_progress): %v", err)
	}
	// Clearing the owner of an in-progress task is rejected by the manager.
	if _, err := assigner.Assign(ctx, task.ID, ""); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("Assign(clear in-progress) = %v, want owner-required error", err)
	}
	reassigned, err := assigner.Assign(ctx, task.ID, "审阅者")
	if err != nil || reassigned.Owner != "审阅者" {
		t.Fatalf("Assign(in progress) = %+v/%v", reassigned, err)
	}
	if _, err := assigner.Assign(ctx, "missing", "研究员"); err == nil {
		t.Fatal("Assign(unknown task) returned nil error")
	}
	if err := board.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := assigner.Assign(ctx, task.ID, "研究员"); err == nil {
		t.Fatal("Assign(deleted task) returned nil error")
	}
}
