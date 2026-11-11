package toolkit

// Tests for the durable shared task board: tasks created through the model
// tools (or the board API) are written to <dataDir>/team-tasks.json and replayed
// into a fresh rig task manager after a restart, including dependencies,
// owners, write scopes and deleted tombstones.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// bootTaskBoard activates the toolkit module with one explicit data directory so
// a test can close it and activate a fresh instance to simulate a restart.
func bootTaskBoard(t *testing.T, dataDir string) (cordis.Context, *module.Loader) {
	t.Helper()
	root := cordis.Root()
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	return root, loader
}

// invokeTaskTool runs one registered model-facing tool.
func invokeTaskTool(t *testing.T, root cordis.Context, name, arguments string) (string, bool) {
	t.Helper()
	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("invoke %s: %v", name, err)
	}
	return result.Content, !result.IsError
}

// boardTasks reads the shared board read model.
func boardTasks(t *testing.T, root cordis.Context) []contracts.BoardTask {
	t.Helper()
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	tasks, err := board.List(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	return tasks
}

// TestTaskBoardPersistsAcrossRestart is the product test: tasks created in one
// process are still on the board after a fresh activation, with their ids,
// dependencies and numbering intact.
func TestTaskBoardPersistsAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create",
		`{"subject":"持久任务","description":"跨重启保留","write_scopes":["internal/"]}`); !ok {
		t.Fatal("team_task_create failed")
	}
	if _, ok := invokeTaskTool(t, root, "team_task_create",
		`{"subject":"依赖任务","blocked_by":["task-1"]}`); !ok {
		t.Fatal("second team_task_create failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName)); err != nil {
		t.Fatalf("task board document not written: %v", err)
	}

	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	tasks := boardTasks(t, root2)
	if len(tasks) != 2 {
		t.Fatalf("restored tasks = %+v, want two", tasks)
	}
	if tasks[0].ID != "task-1" || tasks[0].Subject != "持久任务" || len(tasks[0].WriteScopes) != 1 {
		t.Fatalf("restored task-1 = %+v", tasks[0])
	}
	if tasks[1].ID != "task-2" || len(tasks[1].BlockedBy) != 1 || tasks[1].BlockedBy[0] != "task-1" {
		t.Fatalf("restored task-2 = %+v", tasks[1])
	}
	// The manager's numbering continued: the next auto id is task-3.
	content, ok := invokeTaskTool(t, root2, "team_task_create", `{"subject":"重启后新任务"}`)
	if !ok || !strings.Contains(content, "task-3") {
		t.Fatalf("new task after restart = %q (ok=%v), want task-3", content, ok)
	}
}

// TestTaskBoardPersistsTombstones pins the deleted-task behaviour across a
// restart: the board API hides the tombstoned task while the manager keeps its
// id and the next created task continues the sequence.
func TestTaskBoardPersistsTombstones(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"要删除的任务"}`); !ok {
		t.Fatal("team_task_create failed")
	}
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	if err := board.Delete(context.Background(), "task-1"); err != nil {
		t.Fatalf("delete task-1: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}

	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	if tasks := boardTasks(t, root2); len(tasks) != 0 {
		t.Fatalf("restored tasks = %+v, want the tombstone hidden", tasks)
	}
	content, ok := invokeTaskTool(t, root2, "team_task_create", `{"subject":"墓碑之后的新任务"}`)
	if !ok || !strings.Contains(content, "task-2") {
		t.Fatalf("new task after restart = %q (ok=%v), want task-2", content, ok)
	}
}

// TestTaskBoardQuarantinesCorruptDocument pins the resilience rule.
func TestTaskBoardQuarantinesCorruptDocument(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, taskBoardFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt board: %v", err)
	}
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	if tasks := boardTasks(t, root); len(tasks) != 0 {
		t.Fatalf("corrupt board loaded tasks: %+v", tasks)
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("corrupt board not quarantined: %v %v", matches, err)
	}
}

// TestTaskBoardWithoutDataDirStaysInMemory pins the optional path: compositions
// without a data directory keep the old in-memory board.
func TestTaskBoardWithoutDataDirStaysInMemory(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	defer func() { _ = loader.Close() }()
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"内存任务"}`); !ok {
		t.Fatal("team_task_create failed")
	}
	if tasks := boardTasks(t, root); len(tasks) != 1 {
		t.Fatalf("in-memory tasks = %+v", tasks)
	}
}
