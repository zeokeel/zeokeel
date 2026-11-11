package toolkit

// Tests for the durable shared task board: tasks created through the model
// tools (or the board API) are written to <dataDir>/team-tasks.json and replayed
// into a fresh rig task manager after a restart, including dependencies,
// owners, write scopes and deleted tombstones.

import (
	"context"
	"encoding/json"
	"fmt"
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
	return invokeTaskToolIn(t, root, context.Background(), name, arguments)
}

// invokeTaskToolIn runs one registered model-facing tool inside a run-scoped
// session context.
func invokeTaskToolIn(t *testing.T, root cordis.Context, ctx context.Context, name, arguments string) (string, bool) {
	t.Helper()
	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	result, err := registry.Invoke(ctx, rig.ToolCall{Name: name, Arguments: arguments})
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

// TestTaskBoardScopesBySession is the DSH-alignment test: every conversation
// owns its own shared task board; another session never sees or mutates it.
func TestTaskBoardScopesBySession(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	sessionA := contracts.WithAgentSession(context.Background(), "sess-a")
	sessionB := contracts.WithAgentSession(context.Background(), "sess-b")
	if _, ok := invokeTaskToolIn(t, root, sessionA, "team_task_create", `{"subject":"A 的任务"}`); !ok {
		t.Fatal("create session A task failed")
	}
	if _, ok := invokeTaskToolIn(t, root, sessionB, "team_task_create", `{"subject":"B 的任务"}`); !ok {
		t.Fatal("create session B task failed")
	}
	if _, ok := invokeTaskToolIn(t, root, sessionA, "team_task_create", `{"subject":"A 的第二个任务"}`); !ok {
		t.Fatal("create second session A task failed")
	}
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped, ok := board.(contracts.SessionTaskBoard)
	if !ok {
		t.Fatalf("task board %T does not implement SessionTaskBoard", board)
	}
	tasksA, err := scoped.TaskBoardForSession("sess-a").List(context.Background())
	if err != nil || len(tasksA) != 2 {
		t.Fatalf("session A tasks = %+v, %v, want 2", tasksA, err)
	}
	tasksB, err := scoped.TaskBoardForSession("sess-b").List(context.Background())
	if err != nil || len(tasksB) != 1 || tasksB[0].Subject != "B 的任务" {
		t.Fatalf("session B tasks = %+v, %v, want its own one task", tasksB, err)
	}
	// The aggregate view still lists both (host surfaces without a session).
	if all := boardTasks(t, root); len(all) != 3 {
		t.Fatalf("aggregate tasks = %+v, want 3", all)
	}
	// Restart: each session keeps its own file and numbering.
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	board2, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board after restart: %v", err)
	}
	scoped2 := board2.(contracts.SessionTaskBoard)
	if got, _ := scoped2.TaskBoardForSession("sess-a").List(context.Background()); len(got) != 2 {
		t.Fatalf("restored session A tasks = %+v, want 2", got)
	}
	if got, _ := scoped2.TaskBoardForSession("sess-b").List(context.Background()); len(got) != 1 {
		t.Fatalf("restored session B tasks = %+v, want 1", got)
	}
}

// TestTaskBoardMigratesLegacyBoard pins the upgrade path: v0.2.31's
// process-wide board moves into the first session that uses the board, and is
// retired so it is not listed twice.
func TestTaskBoardMigratesLegacyBoard(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"旧版任务","blocked_by":[]}`); !ok {
		t.Fatal("legacy team_task_create failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	// The legacy document exists before the first session board materializes.
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName)); err != nil {
		t.Fatalf("legacy board document missing: %v", err)
	}

	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	sessionA := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, sessionA, "team_task_list", `{}`); !ok {
		t.Fatal("team_task_list failed")
	}
	board, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	tasksA, err := scoped.TaskBoardForSession("sess-a").List(context.Background())
	if err != nil || len(tasksA) != 1 || tasksA[0].Subject != "旧版任务" {
		t.Fatalf("migrated session A tasks = %+v, %v, want the legacy task", tasksA, err)
	}
	if tasksB, _ := scoped.TaskBoardForSession("sess-b").List(context.Background()); len(tasksB) != 0 {
		t.Fatalf("session B inherited legacy tasks: %+v", tasksB)
	}
	if all := boardTasks(t, root2); len(all) != 1 {
		t.Fatalf("aggregate tasks after migration = %+v, want the single migrated task", all)
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName) + ".migrated"); err != nil {
		t.Fatalf("legacy board document was not retired: %v", err)
	}
}

// TestTaskBoardMigrationIsOneShot pins the terminal migration: after the v1
// board moved into a session board, a stale no-session writer cannot recreate
// the retired document and get it migrated a second time (duplicate tasks).
func TestTaskBoardMigrationIsOneShot(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"旧版任务"}`); !ok {
		t.Fatal("legacy team_task_create failed")
	}
	sessionA := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root, sessionA, "team_task_list", `{}`); !ok {
		t.Fatal("session team_task_list failed")
	}
	// A no-session mutation (the headless/devtools path) must not recreate the
	// retired document.
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"迟到的旧版任务"}`); !ok {
		t.Fatal("late legacy team_task_create failed")
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName)); err == nil {
		t.Fatal("retired legacy board document was recreated")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	board, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	tasks, err := scoped.TaskBoardForSession("sess-a").List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("session A tasks after restart = %+v, %v, want the single migrated task", tasks, err)
	}
	if all := boardTasks(t, root2); len(all) != 1 {
		t.Fatalf("aggregate tasks after restart = %+v, want no duplicate", all)
	}
}

// TestTaskBoardAssignStaysInSession: task ids repeat per conversation, so a
// session-scoped assignment must never fall back to another session's board.
func TestTaskBoardAssignStaysInSession(t *testing.T) {
	root, loader := bootTaskBoard(t, t.TempDir())
	defer func() { _ = loader.Close() }()
	sessionA := contracts.WithAgentSession(context.Background(), "sess-a")
	sessionB := contracts.WithAgentSession(context.Background(), "sess-b")
	if _, ok := invokeTaskToolIn(t, root, sessionA, "team_task_create", `{"subject":"A 的秘密任务"}`); !ok {
		t.Fatal("create session A task failed")
	}
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	assigner, ok := raw.(contracts.TaskAssigner)
	if !ok {
		t.Fatalf("task board %T does not implement TaskAssigner", raw)
	}
	if _, err := assigner.Assign(sessionB, "task-1", "mallory"); err == nil {
		t.Fatal("cross-session Assign succeeded, want ErrTaskNotFound")
	}
	tasksA, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	if len(tasksA) != 1 || tasksA[0].Owner == "mallory" {
		t.Fatalf("session A tasks = %+v, want untouched owner", tasksA)
	}
	if _, err := assigner.Assign(sessionA, "task-1", "研究员"); err != nil {
		t.Fatalf("session A Assign: %v", err)
	}
	tasksA, _ = raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	if len(tasksA) != 1 || tasksA[0].Owner != "研究员" {
		t.Fatalf("session A tasks after assign = %+v", tasksA)
	}
}

// TestTaskBoardCleanerRemovesSessionBoard covers conversation deletion.
func TestTaskBoardCleanerRemovesSessionBoard(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	sessionA := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root, sessionA, "team_task_create", `{"subject":"待删会话任务"}`); !ok {
		t.Fatal("create session task failed")
	}
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	cleaner, ok := raw.(contracts.TaskBoardCleaner)
	if !ok {
		t.Fatalf("task board %T does not implement TaskBoardCleaner", raw)
	}
	if err := cleaner.RemoveTaskBoardForSession("sess-a"); err != nil {
		t.Fatalf("RemoveTaskBoardForSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-a.json")); err == nil {
		t.Fatal("session board document was not removed")
	}
	if all := boardTasks(t, root); len(all) != 0 {
		t.Fatalf("aggregate tasks after cleanup = %+v, want empty", all)
	}
}

// TestTaskBoardUICacheMissKeepsPersistedTasks is the regression test for the
// audit's data-loss finding: once evicted, a UI read must re-read the document
// instead of caching an empty board that the next tool call overwrites.
func TestTaskBoardUICacheMissKeepsPersistedTasks(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	ctxA := contracts.WithAgentSession(context.Background(), "sess-keep")
	if _, ok := invokeTaskToolIn(t, root, ctxA, "team_task_create", `{"subject":"keep-me"}`); !ok {
		t.Fatal("create session task failed")
	}
	// Fill the cache past its bound so sess-keep is the evicted (oldest) board.
	for index := 0; index < 200; index++ {
		_ = scoped.TaskBoardForSession(fmt.Sprintf("sess-fill-%d", index)).(*taskBoardAdapter)
	}
	// A UI read after eviction must still show the persisted task.
	tasks, err := scoped.TaskBoardForSession("sess-keep").List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Subject != "keep-me" {
		t.Fatalf("UI tasks after eviction = %+v, %v, want keep-me", tasks, err)
	}
	// The next tool mutation must extend, not overwrite, the document.
	if _, ok := invokeTaskToolIn(t, root, ctxA, "team_task_create", `{"subject":"second"}`); !ok {
		t.Fatal("second create failed")
	}
	tasks, _ = scoped.TaskBoardForSession("sess-keep").List(context.Background())
	if len(tasks) != 2 {
		t.Fatalf("session tasks after mutation = %+v, want 2 (persisted + new)", tasks)
	}
}

// TestTaskBoardUIReadCreatesNoFile pins the read path: opening an unknown
// conversation's board creates no document and triggers no migration/write.
func TestTaskBoardUIReadCreatesNoFile(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	tasks, err := scoped.TaskBoardForSession("sess-new").List(context.Background())
	if err != nil || len(tasks) != 0 {
		t.Fatalf("new session tasks = %+v, %v", tasks, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-new.json")); err == nil {
		t.Fatal("UI read created a task-board document")
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName) + ".migrated"); err == nil {
		t.Fatal("UI read triggered a legacy migration")
	}
}

// TestTaskBoardCleanerRemovesUncachedBoard: cleanup removes the document even
// when the board was evicted from the cache, and a late tool call cannot
// recreate it (tombstone).
func TestTaskBoardCleanerRemovesUncachedBoard(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	ctxA := contracts.WithAgentSession(context.Background(), "sess-gone")
	if _, ok := invokeTaskToolIn(t, root, ctxA, "team_task_create", `{"subject":"gone"}`); !ok {
		t.Fatal("create session task failed")
	}
	for index := 0; index < 200; index++ {
		_ = scoped.TaskBoardForSession(fmt.Sprintf("sess-fill-%d", index))
	}
	cleaner := board.(contracts.TaskBoardCleaner)
	if err := cleaner.RemoveTaskBoardForSession("sess-gone"); err != nil {
		t.Fatalf("RemoveTaskBoardForSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-gone.json")); err == nil {
		t.Fatal("uncached session document survived cleanup")
	}
	// A late tool call must stay memory-only (tombstone).
	if _, ok := invokeTaskToolIn(t, root, ctxA, "team_task_create", `{"subject":"late"}`); !ok {
		t.Fatal("late create reported an error")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-gone.json")); err == nil {
		t.Fatal("late tool call recreated the deleted session document")
	}
}

// TestTaskBoardMigrationRecoversAfterCrash simulates a crash after the legacy
// document was claimed (renamed to .migrating) but before it was persisted into
// a session board: the next process retries once and leaves no duplicate.
func TestTaskBoardMigrationRecoversAfterCrash(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"旧版任务"}`); !ok {
		t.Fatal("legacy create failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	// Crash window: the source was claimed but the target was never recorded.
	if err := os.Rename(filepath.Join(dataDir, taskBoardFileName),
		filepath.Join(dataDir, taskBoardFileName)+".migrating"); err != nil {
		t.Fatalf("simulate claim: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	ctxA := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, ctxA, "team_task_list", `{}`); !ok {
		t.Fatal("session list failed")
	}
	board, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	tasks, _ := scoped.TaskBoardForSession("sess-a").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("recovered tasks = %+v, want the single old task", tasks)
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName) + ".migrated"); err != nil {
		t.Fatalf("migration marker missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName) + ".migrating"); err == nil {
		t.Fatal("migration journal survived the recovery")
	}
}

// TestTaskBoardMigrationTargetsJournalledSession: after a crash that recorded
// the target conversation, only that conversation finishes the migration; a
// different first conversation must not inherit the tasks.
func TestTaskBoardMigrationTargetsJournalledSession(t *testing.T) {
	dataDir := t.TempDir()
	document := taskBoardFile{
		Version: taskBoardVersion,
		Tasks: []taskBoardTask{{
			ID: "task-1", Subject: "旧版任务", Status: contracts.TaskStatusPending,
		}},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	migratingPath := filepath.Join(dataDir, taskBoardFileName) + ".migrating"
	if err := os.WriteFile(migratingPath, data, 0o600); err != nil {
		t.Fatalf("write migrating doc: %v", err)
	}
	if err := os.WriteFile(migratingPath+".target", []byte("sess-target"), 0o600); err != nil {
		t.Fatalf("write target journal: %v", err)
	}
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	other := contracts.WithAgentSession(context.Background(), "sess-other")
	if _, ok := invokeTaskToolIn(t, root, other, "team_task_list", `{}`); !ok {
		t.Fatal("other session list failed")
	}
	board, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := board.(contracts.SessionTaskBoard)
	if tasks, _ := scoped.TaskBoardForSession("sess-other").List(context.Background()); len(tasks) != 0 {
		t.Fatalf("non-target session inherited tasks: %+v", tasks)
	}
	target := contracts.WithAgentSession(context.Background(), "sess-target")
	if _, ok := invokeTaskToolIn(t, root, target, "team_task_list", `{}`); !ok {
		t.Fatal("target session list failed")
	}
	tasks, _ := scoped.TaskBoardForSession("sess-target").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("target tasks = %+v, want the migrated old task", tasks)
	}
}

// TestTaskBoardBoundToolCannotResurrectDeletedBoard covers the audit's in-flight
// writer hole: a tool resolved before RemoveTaskBoardForSession must not persist
// the deleted document again (cached and evicted adapter cases).
func TestTaskBoardBoundToolCannotResurrectDeletedBoard(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	service := raw.(*taskBoardService)
	session := contracts.WithAgentSession(context.Background(), "sess-bound")
	sessionID := contracts.AgentSessionFromContext(session)
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_create", `{"subject":"bound"}`); !ok {
		t.Fatal("create session task failed")
	}
	bound := service.toolFor(sessionID, "team_task_create")
	// Cached case.
	if err := service.RemoveTaskBoardForSession(sessionID); err != nil {
		t.Fatalf("RemoveTaskBoardForSession (cached): %v", err)
	}
	if bound == nil {
		t.Fatal("bound tool is nil")
	}
	if _, err := bound.Invoke(session, rig.ToolCall{Name: "team_task_create", Arguments: `{"subject":"late"}`}); err != nil {
		t.Fatalf("late bound invoke: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-bound.json")); err == nil {
		t.Fatal("cached bound tool recreated the deleted document")
	}
	// Evicted case: a new bound tool exists while the adapter leaves the cache.
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_create", `{"subject":"bound-2"}`); !ok {
		t.Fatal("second create failed")
	}
	evictedBound := service.toolFor(sessionID, "team_task_create")
	for index := 0; index < 200; index++ {
		_, _ = service.TaskBoardForSession(fmt.Sprintf("sess-fill-%d", index)).List(context.Background())
	}
	if err := service.RemoveTaskBoardForSession(sessionID); err != nil {
		t.Fatalf("RemoveTaskBoardForSession (evicted): %v", err)
	}
	if _, err := evictedBound.Invoke(session, rig.ToolCall{Name: "team_task_create", Arguments: `{"subject":"late-2"}`}); err != nil {
		t.Fatalf("evicted bound invoke: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-bound.json")); err == nil {
		t.Fatal("evicted bound tool recreated the deleted document")
	}
}

// TestTaskBoardMigrationKeepsJournalOnPersistFailure covers the audit's data-loss
// finding: a failed session-document write must not retire the only copy of the
// pre-upgrade tasks; the next call retries.
func TestTaskBoardMigrationKeepsJournalOnPersistFailure(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"旧版任务"}`); !ok {
		t.Fatal("legacy create failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	// Make the per-session document directory unwritable (a regular file blocks
	// MkdirAll while the parent data dir is writable).
	blocker := filepath.Join(dataDir, "team-tasks")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_list", `{}`); ok {
		t.Fatal("task tool served while the migration was blocked, want an error")
	}
	legacyBase := filepath.Join(dataDir, taskBoardFileName)
	if _, err := os.Stat(legacyBase + ".migrating"); err != nil {
		t.Fatalf("journal was removed on persist failure: %v", err)
	}
	if _, err := os.Stat(legacyBase + ".migrated"); err == nil {
		t.Fatal("migration finalized despite the persist failure")
	}
	// Repair the directory and retry: the journal is retried, not lost.
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_list", `{}`); !ok {
		t.Fatal("retry list failed")
	}
	raw, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("retried tasks = %+v, want the old task", tasks)
	}
	if _, err := os.Stat(legacyBase + ".migrated"); err != nil {
		t.Fatalf("migration marker missing after retry: %v", err)
	}
}

// TestTaskBoardDeletedJournalTargetReleases covers the stranded-journal edge: if
// the journalled target conversation is deleted, any conversation may finish the
// migration instead of leaving the legacy tasks unreachable.
func TestTaskBoardDeletedJournalTargetReleases(t *testing.T) {
	dataDir := t.TempDir()
	document := taskBoardFile{Version: taskBoardVersion, Tasks: []taskBoardTask{{
		ID: "task-1", Subject: "旧版任务", Status: contracts.TaskStatusPending,
	}}}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	migratingPath := filepath.Join(dataDir, taskBoardFileName) + ".migrating"
	if err := os.WriteFile(migratingPath, data, 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	if err := os.WriteFile(migratingPath+".target", []byte("sess-target"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	service := raw.(*taskBoardService)
	if err := service.RemoveTaskBoardForSession("sess-target"); err != nil {
		t.Fatalf("remove target session: %v", err)
	}
	other := contracts.WithAgentSession(context.Background(), "sess-other")
	if _, ok := invokeTaskToolIn(t, root, other, "team_task_list", `{}`); !ok {
		t.Fatal("other list failed")
	}
	tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-other").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("released journal tasks = %+v, want the old task", tasks)
	}
}

func entriesOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"ERR:" + err.Error()}
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

// TestTaskBoardMigrationBlocksToolsOnRenameFailure covers the audit's P2: when
// claiming the legacy document fails, the session's task tools refuse until the
// migration commits, so a mutation can never make a partial migration look done.
func TestTaskBoardMigrationBlocksToolsOnRenameFailure(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"旧版任务"}`); !ok {
		t.Fatal("legacy create failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	// A directory at the journal path makes the atomic rename fail. It is
	// created after activation so the legacy document loads normally first.
	journalDir := filepath.Join(dataDir, taskBoardFileName) + ".migrating"
	if err := os.Mkdir(journalDir, 0o700); err != nil {
		t.Fatalf("create journal blocker: %v", err)
	}
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_create", `{"subject":"NEW-TASK"}`); ok {
		t.Fatal("task tool served while the migration claim failed, want an error")
	}
	raw, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	if tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background()); len(tasks) != 0 {
		t.Fatalf("blocked session board = %+v, want empty", tasks)
	}
	t.Logf("after blocked attempt: data=%v", entriesOf(t, dataDir))
	// Repair: the source is still intact and the retry migrates it.
	if err := os.Remove(journalDir); err != nil {
		t.Fatalf("remove journal blocker: %v", err)
	}
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_list", `{}`); !ok {
		t.Fatal("retry after repair failed")
	}
	tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("recovered tasks = %+v, want only the legacy task", tasks)
	}
}

// TestTaskBoardRemoveDuringPersistCannotResurrect pins the atomic retirement:
// deletion waits for an in-flight save under the same store lock, so the
// document cannot reappear after cleanup.
func TestTaskBoardRemoveDuringPersistCannotResurrect(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	service := raw.(*taskBoardService)
	board := service.adapterForTool("sess-race")
	// Widen the write window with a full board.
	for index := 0; index < 200; index++ {
		if _, createErr := board.manager.Create(context.Background(), rig.TeamTask{
			Subject: fmt.Sprintf("task-%d", index),
		}); createErr != nil {
			t.Fatalf("seed task: %v", createErr)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = board.persist()
	}()
	if err := service.RemoveTaskBoardForSession("sess-race"); err != nil {
		t.Fatalf("RemoveTaskBoardForSession: %v", err)
	}
	<-done
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-race.json")); err == nil {
		t.Fatal("in-flight persist recreated the removed document")
	}
}

// TestTaskBoardRemoveRetiresSupersededStores: a bound tool that holds an evicted
// adapter's store must not resurrect the document after deletion.
func TestTaskBoardRemoveRetiresSupersededStores(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	service := raw.(*taskBoardService)
	session := contracts.WithAgentSession(context.Background(), "sess-bound")
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_create", `{"subject":"bound"}`); !ok {
		t.Fatal("create failed")
	}
	bound := service.toolFor("sess-bound", "team_task_create")
	// Evict the adapter, then materialize a second store for the same path.
	for index := 0; index < 200; index++ {
		_, _ = service.TaskBoardForSession(fmt.Sprintf("sess-fill-%d", index)).List(context.Background())
	}
	_, _ = service.TaskBoardForSession("sess-bound").List(context.Background())
	if err := service.RemoveTaskBoardForSession("sess-bound"); err != nil {
		t.Fatalf("RemoveTaskBoardForSession: %v", err)
	}
	if _, err := bound.Invoke(session, rig.ToolCall{Name: "team_task_create", Arguments: `{"subject":"late"}`}); err != nil {
		t.Fatalf("bound invoke: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "team-tasks", "sess-bound.json")); err == nil {
		t.Fatal("superseded adapter recreated the removed document")
	}
}

// TestTaskBoardFreshInstallWritesNoMarker: a brand-new data dir has nothing to
// migrate, so no `.migrated` marker (which would later hide a real v1 document).
func TestTaskBoardFreshInstallWritesNoMarker(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_list", `{}`); !ok {
		t.Fatal("fresh-install list failed")
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName) + ".migrated"); err == nil {
		t.Fatal("fresh install wrote a migration marker")
	}
	if all := boardTasks(t, root); len(all) != 0 {
		t.Fatalf("fresh-install aggregate tasks = %+v", all)
	}
}

// TestTaskBoardValidEmptyLegacyDocumentFinalizes: a parseable empty v1 document
// is migrated (retired) instead of blocking every session tool forever.
func TestTaskBoardValidEmptyLegacyDocumentFinalizes(t *testing.T) {
	dataDir := t.TempDir()
	document, err := json.Marshal(taskBoardFile{Version: taskBoardVersion})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, taskBoardFileName), document, 0o600); err != nil {
		t.Fatalf("write legacy empty doc: %v", err)
	}
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_list", `{}`); !ok {
		t.Fatal("valid empty legacy doc blocked the task tools")
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName) + ".migrated"); err != nil {
		t.Fatalf("valid empty doc was not finalized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, taskBoardFileName)); err == nil {
		t.Fatal("valid empty v1 document was left on disk after finalize")
	}
}

// TestTaskBoardPreloadSkipsCorruptBackups: quarantined backups are pruned but
// never loaded/re-quarantined at activation.
func TestTaskBoardPreloadSkipsCorruptBackups(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "team-tasks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	backup := filepath.Join(dir, "sess-bad.json.corrupt-123")
	if err := os.WriteFile(backup, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	_, loader := bootTaskBoard(t, dataDir)
	if err := loader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sess-bad.json.corrupt-123" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("activation rewrote/quarantined the backup again: %v", names)
	}
}

// TestTaskBoardMarkerRecoversInterruptedReMigration covers the rollback case: a
// marker exists, a later v1 document appeared and its re-migration was claimed
// but not finalized. The claim must be recovered, not deleted.
func TestTaskBoardMarkerRecoversInterruptedReMigration(t *testing.T) {
	dataDir := t.TempDir()
	// Marker from an earlier completed migration.
	if err := os.WriteFile(filepath.Join(dataDir, taskBoardFileName)+".migrated", []byte("migrated"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	document, err := json.Marshal(taskBoardFile{Version: taskBoardVersion, Tasks: []taskBoardTask{{
		ID: "task-1", Subject: "rollback-task", Status: contracts.TaskStatusPending,
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	migratingPath := filepath.Join(dataDir, taskBoardFileName) + ".migrating"
	if err := os.WriteFile(migratingPath, document, 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	if err := os.WriteFile(migratingPath+".target", []byte("sess-b"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	// Only the journalled target may finish the recovery.
	other := contracts.WithAgentSession(context.Background(), "sess-other")
	if _, ok := invokeTaskToolIn(t, root, other, "team_task_list", `{}`); !ok {
		t.Fatal("other list failed")
	}
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := raw.(contracts.SessionTaskBoard)
	if tasks, _ := scoped.TaskBoardForSession("sess-other").List(context.Background()); len(tasks) != 0 {
		t.Fatalf("non-target session inherited tasks: %+v", tasks)
	}
	target := contracts.WithAgentSession(context.Background(), "sess-b")
	if _, ok := invokeTaskToolIn(t, root, target, "team_task_list", `{}`); !ok {
		t.Fatal("target list failed")
	}
	tasks, _ := scoped.TaskBoardForSession("sess-b").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "rollback-task" {
		t.Fatalf("recovered rollback tasks = %+v, want rollback-task", tasks)
	}
	if _, err := os.Stat(migratingPath); err == nil {
		t.Fatal("journal survived the recovery")
	}
}

// TestTaskBoardHostSessionMutationBlockedDuringMigration: the host board API must
// not persist a partial target while a migration journal is pending.
func TestTaskBoardHostSessionMutationBlockedDuringMigration(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"旧版任务"}`); !ok {
		t.Fatal("legacy create failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	blocker := filepath.Join(dataDir, "team-tasks")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	board, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, createErr := board.Create(session, "HOST-NEW", ""); createErr == nil {
		t.Fatal("host session mutation succeeded while the migration was pending")
	}
}

// TestTaskBoardUnreadableSourceRecoversInProcess: repairing an unreadable v1
// source must unblock the tools without a restart.
func TestTaskBoardUnreadableSourceRecoversInProcess(t *testing.T) {
	dataDir := t.TempDir()
	blocker := filepath.Join(dataDir, taskBoardFileName)
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	root, loader := bootTaskBoard(t, dataDir)
	defer func() { _ = loader.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_list", `{}`); ok {
		t.Fatal("task tool served with an unreadable migration source")
	}
	// Repair the source in-process, then retry.
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove source dir: %v", err)
	}
	document, err := json.Marshal(taskBoardFile{Version: taskBoardVersion, Tasks: []taskBoardTask{{
		ID: "task-1", Subject: "旧版任务", Status: contracts.TaskStatusPending,
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(blocker, document, 0o600); err != nil {
		t.Fatalf("write repaired source: %v", err)
	}
	if _, ok := invokeTaskToolIn(t, root, session, "team_task_list", `{}`); !ok {
		t.Fatal("task tool still blocked after the source was repaired")
	}
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	if len(tasks) != 1 || tasks[0].Subject != "旧版任务" {
		t.Fatalf("recovered tasks = %+v, want the repaired legacy task", tasks)
	}
}

// TestTaskBoardDeletedTargetReleasesJournal: releasing a journal whose target
// was deleted must merge the legacy tasks even into a non-empty board.
func TestTaskBoardDeletedTargetReleasesJournal(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	sessionB := contracts.WithAgentSession(context.Background(), "sess-b")
	if _, ok := invokeTaskToolIn(t, root, sessionB, "team_task_create", `{"subject":"B-own"}`); !ok {
		t.Fatal("create B task failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	// A v1 document appears after the fact (rollback/old binary).
	document, err := json.Marshal(taskBoardFile{Version: taskBoardVersion, Tasks: []taskBoardTask{{
		ID: "task-1", Subject: "rollback-task", Status: contracts.TaskStatusPending,
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, taskBoardFileName), document, 0o600); err != nil {
		t.Fatalf("write rollback source: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	raw, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	service := raw.(*taskBoardService)
	sessionA := contracts.WithAgentSession(context.Background(), "sess-a")
	// Force the first migration attempt to fail at persist time (target = A).
	// The session boards are preloaded, so removing the document dir still keeps
	// B's in-memory board non-empty for the forced hydration.
	if err := os.RemoveAll(filepath.Join(dataDir, "team-tasks")); err != nil {
		t.Fatalf("remove board dir: %v", err)
	}
	blocker := filepath.Join(dataDir, "team-tasks")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if _, ok := invokeTaskToolIn(t, root2, sessionA, "team_task_list", `{}`); ok {
		t.Fatal("task tool served while the migration was pending")
	}
	if err := service.RemoveTaskBoardForSession("sess-a"); err != nil {
		t.Fatalf("remove failed target: %v", err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	if _, ok := invokeTaskToolIn(t, root2, sessionB, "team_task_list", `{}`); !ok {
		t.Fatal("session B still blocked after the target was released")
	}
	tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-b").List(context.Background())
	found := false
	for _, task := range tasks {
		if task.Subject == "rollback-task" {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy task lost after target deletion: tasks = %+v", tasks)
	}
}

// TestTaskBoardRollbackMergesIntoNonEmptyBoard covers the residual P2: a v1
// document appearing after the migration marker must be merged into the first
// board-touching conversation even when that board already has tasks, without
// duplicating the legacy tasks on a retry.
func TestTaskBoardRollbackMergesIntoNonEmptyBoard(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	sessionB := contracts.WithAgentSession(context.Background(), "sess-b")
	if _, ok := invokeTaskToolIn(t, root, sessionB, "team_task_create", `{"subject":"B-OWN"}`); !ok {
		t.Fatal("create B task failed")
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close first boot: %v", err)
	}
	// Marker from the earlier migration + a rollback v1 document.
	if err := os.WriteFile(filepath.Join(dataDir, taskBoardFileName)+".migrated", []byte("migrated"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	document, err := json.Marshal(taskBoardFile{Version: taskBoardVersion, Tasks: []taskBoardTask{{
		ID: "task-1", Subject: "rollback-task", Status: contracts.TaskStatusPending,
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, taskBoardFileName), document, 0o600); err != nil {
		t.Fatalf("write rollback doc: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	if _, ok := invokeTaskToolIn(t, root2, sessionB, "team_task_list", `{}`); !ok {
		t.Fatal("session B list failed")
	}
	raw, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	scoped := raw.(contracts.SessionTaskBoard)
	tasks, _ := scoped.TaskBoardForSession("sess-b").List(context.Background())
	subjects := make(map[string]int, len(tasks))
	for _, task := range tasks {
		subjects[task.Subject]++
	}
	if subjects["B-OWN"] != 1 || subjects["rollback-task"] != 1 {
		t.Fatalf("merged tasks = %+v, want B-OWN and rollback-task exactly once", subjects)
	}
	// Another retry (a second process or a repeated call) must not duplicate.
	if _, ok := invokeTaskToolIn(t, root2, sessionB, "team_task_list", `{}`); !ok {
		t.Fatal("second list failed")
	}
	tasks, _ = scoped.TaskBoardForSession("sess-b").List(context.Background())
	subjects = make(map[string]int, len(tasks))
	for _, task := range tasks {
		subjects[task.Subject]++
	}
	if subjects["B-OWN"] != 1 || subjects["rollback-task"] != 1 {
		t.Fatalf("retry duplicated tasks: %+v", subjects)
	}
}

// TestTaskBoardMigrationKeepsDuplicateContentTasks: duplicate-content legacy
// tasks are a legal state and must survive the multiset merge.
func TestTaskBoardMigrationKeepsDuplicateContentTasks(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	for index := 0; index < 2; index++ {
		if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"SAME","description":"same body"}`); !ok {
			t.Fatal("legacy create failed")
		}
	}
	if all := boardTasks(t, root); len(all) != 2 {
		t.Fatalf("legacy tasks = %d, want 2 duplicates", len(all))
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_list", `{}`); !ok {
		t.Fatal("session list failed")
	}
	raw, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	tasks, _ := raw.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	if len(tasks) != 2 {
		t.Fatalf("migrated tasks = %+v, want both duplicate-content tasks", tasks)
	}
}

// TestTaskBoardMigrationPreservesTombstones: a task deleted before the upgrade
// must stay deleted after the migration (its tombstone is remapped, not lost).
func TestTaskBoardMigrationPreservesTombstones(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"VISIBLE"}`); !ok {
		t.Fatal("create VISIBLE failed")
	}
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"DELETED"}`); !ok {
		t.Fatal("create DELETED failed")
	}
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	var deletedID string
	for _, task := range boardTasks(t, root) {
		if task.Subject == "DELETED" {
			deletedID = task.ID
		}
	}
	if deletedID == "" {
		t.Fatal("DELETED task missing")
	}
	if err := raw.Delete(context.Background(), deletedID); err != nil {
		t.Fatalf("delete legacy task: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_list", `{}`); !ok {
		t.Fatal("session list failed")
	}
	raw2, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	tasks, _ := raw2.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	for _, task := range tasks {
		if task.Subject == "DELETED" {
			t.Fatalf("tombstoned task reappeared after migration: %+v", tasks)
		}
	}
	foundVisible := false
	for _, task := range tasks {
		if task.Subject == "VISIBLE" {
			foundVisible = true
		}
	}
	if !foundVisible {
		t.Fatalf("visible task lost after migration: %+v", tasks)
	}
}

// TestTaskBoardMigrationTombstonesDoNotHideNewTasks: legacy tombstone ids share
// the target's task-N space; a raw fallback would hide the first new task.
func TestTaskBoardMigrationTombstonesDoNotHideNewTasks(t *testing.T) {
	dataDir := t.TempDir()
	root, loader := bootTaskBoard(t, dataDir)
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"VISIBLE"}`); !ok {
		t.Fatal("create VISIBLE failed")
	}
	if _, ok := invokeTaskTool(t, root, "team_task_create", `{"subject":"DELETED"}`); !ok {
		t.Fatal("create DELETED failed")
	}
	raw, err := root.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	for _, task := range boardTasks(t, root) {
		if task.Subject == "DELETED" {
			if deleteErr := raw.Delete(context.Background(), task.ID); deleteErr != nil {
				t.Fatalf("delete legacy task: %v", deleteErr)
			}
		}
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	root2, loader2 := bootTaskBoard(t, dataDir)
	defer func() { _ = loader2.Close() }()
	session := contracts.WithAgentSession(context.Background(), "sess-a")
	if _, ok := invokeTaskToolIn(t, root2, session, "team_task_list", `{}`); !ok {
		t.Fatal("session list failed")
	}
	content, ok := invokeTaskToolIn(t, root2, session, "team_task_create", `{"subject":"NEW-TASK"}`)
	if !ok {
		t.Fatalf("post-migration create failed: %s", content)
	}
	board, err := root2.Require(contracts.KeyTaskBoard)
	if err != nil {
		t.Fatalf("require task board: %v", err)
	}
	tasks, _ := board.(contracts.SessionTaskBoard).TaskBoardForSession("sess-a").List(context.Background())
	found := false
	for _, task := range tasks {
		if task.Subject == "NEW-TASK" {
			found = true
		}
	}
	if !found {
		t.Fatalf("new task hidden by a legacy tombstone: %+v", tasks)
	}
}
