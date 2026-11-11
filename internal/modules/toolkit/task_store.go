package toolkit

// Durable shared task board.
//
// The rig task manager is process-in-memory; without persistence a restart
// empties the team overlay's 共享任务 section even though the session still shows
// the tasks it created. This store writes one bounded JSON document in the
// product data directory after every successful mutation (model tool or UI
// action) and replays it into a fresh rig task manager at activation.
//
// The rig manager numbers tasks itself (task-1, task-2, ...). A replay recreates
// the board in ascending id order through the same create path, which reproduces
// that numbering; if a stored document ever has gaps (should not happen: the
// product tombstones deletions instead of removing tasks), ids are renumbered
// consistently, rewriting dependent references.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/team"
)

const (
	// taskBoardFileName is the board document inside the data directory.
	taskBoardFileName = "team-tasks.json"
	// taskBoardVersion is the document schema version.
	taskBoardVersion = 1
	// taskBoardKeep bounds the retained tasks (the rig manager's own limit is
	// 256; the document mirrors it).
	taskBoardKeep = team.DefaultMaxTasks
)

// taskBoardFile is the persisted board document.
type taskBoardFile struct {
	Version int             `json:"version"`
	Tasks   []taskBoardTask `json:"tasks,omitempty"`
	Deleted []string        `json:"deleted,omitempty"`
}

// taskBoardTask is one persisted task snapshot (the rig type's fields).
type taskBoardTask struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject,omitempty"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	BlockedBy   []string  `json:"blockedBy,omitempty"`
	WriteScopes []string  `json:"writeScopes,omitempty"`
	Created     time.Time `json:"created,omitempty"`
	Updated     time.Time `json:"updated,omitempty"`
}

// taskBoardStore owns the persisted board. It holds its own lock; callers never
// hold the adapter lock while saving.
type taskBoardStore struct {
	mu   sync.Mutex
	path string
	logf func(string, ...any)
}

// newTaskBoardStore returns the store for dataDir (nil when the host has no
// data directory: the board then stays in-memory as before).
func newTaskBoardStore(dataDir string, logf func(string, ...any)) *taskBoardStore {
	dir := strings.TrimSpace(dataDir)
	if dir == "" {
		return nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &taskBoardStore{path: filepath.Join(dir, taskBoardFileName), logf: logf}
}

// load reads the persisted board. It reports ok=false when there is nothing to
// hydrate (missing/corrupt document); a corrupt document is quarantined.
func (s *taskBoardStore) load() (tasks []rig.TeamTask, deleted []string, ok bool) {
	if s == nil {
		return nil, nil, false
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logf("[toolkit] read task board: %v", err)
		}
		return nil, nil, false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil, false
	}
	var document taskBoardFile
	if err := json.Unmarshal(data, &document); err != nil {
		s.quarantine("task board document is not valid JSON: " + err.Error())
		return nil, nil, false
	}
	if document.Version != taskBoardVersion {
		s.quarantine(fmt.Sprintf("task board document has unsupported version %d", document.Version))
		return nil, nil, false
	}
	for _, task := range document.Tasks {
		tasks = append(tasks, rig.TeamTask{
			ID:          task.ID,
			Subject:     task.Subject,
			Description: task.Description,
			Status:      task.Status,
			Owner:       task.Owner,
			BlockedBy:   append([]string(nil), task.BlockedBy...),
			WriteScopes: append([]string(nil), task.WriteScopes...),
			Created:     task.Created,
			Updated:     task.Updated,
		})
	}
	return tasks, document.Deleted, len(tasks) > 0 || len(document.Deleted) > 0
}

// save snapshots the live board and writes the document atomically.
func (s *taskBoardStore) save(tasks []rig.TeamTask, deleted map[string]bool) {
	if s == nil {
		return
	}
	document := taskBoardFile{Version: taskBoardVersion}
	for _, task := range tasks {
		if len(document.Tasks) >= taskBoardKeep {
			break
		}
		document.Tasks = append(document.Tasks, taskBoardTask{
			ID:          task.ID,
			Subject:     task.Subject,
			Description: task.Description,
			Status:      task.Status,
			Owner:       task.Owner,
			BlockedBy:   append([]string(nil), task.BlockedBy...),
			WriteScopes: append([]string(nil), task.WriteScopes...),
			Created:     task.Created,
			Updated:     task.Updated,
		})
	}
	for id := range deleted {
		document.Deleted = append(document.Deleted, id)
	}
	sort.Strings(document.Deleted)
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		s.logf("[toolkit] encode task board: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		s.logf("[toolkit] create task board directory: %v", err)
		return
	}
	temporary, err := os.CreateTemp(directory, ".team-tasks-*.tmp")
	if err != nil {
		s.logf("[toolkit] create temporary task board file: %v", err)
		return
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		s.logf("[toolkit] write temporary task board file: %v", err)
		return
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		s.logf("[toolkit] sync temporary task board file: %v", err)
		return
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		s.logf("[toolkit] close temporary task board file: %v", err)
		return
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		s.logf("[toolkit] replace task board %q: %v", s.path, err)
		return
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
}

// quarantine moves the unusable document out of the way.
func (s *taskBoardStore) quarantine(reason string) {
	backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UTC().UnixNano())
	message := reason
	if err := os.Rename(s.path, backup); err != nil {
		message += fmt.Sprintf(" (could not quarantine the file: %v)", err)
	} else {
		message += " (quarantined as " + backup + ")"
	}
	s.logf("%s; starting with an empty task board", message)
}

// persistingTaskTool wraps one model-facing task tool: after a successful
// invocation it writes the board document, so a task created/updated through the
// model survives the next restart exactly like one changed from the UI.
type persistingTaskTool struct {
	inner rig.Tool
	board *taskBoardAdapter
}

var _ rig.Tool = (*persistingTaskTool)(nil)

// Name implements rig.Tool.
func (t *persistingTaskTool) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t *persistingTaskTool) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t *persistingTaskTool) Schema() map[string]any { return t.inner.Schema() }

// Invoke implements rig.Tool.
func (t *persistingTaskTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	result, err := t.inner.Invoke(ctx, call)
	if err == nil && !result.IsError {
		t.board.persist()
	}
	return result, err
}

// wrapPersistingTaskTools wraps the task-board tools of one manager.
func wrapPersistingTaskTools(tools []rig.Tool, board *taskBoardAdapter) []rig.Tool {
	if board == nil || board.store == nil {
		return tools
	}
	wrapped := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		wrapped = append(wrapped, &persistingTaskTool{inner: tool, board: board})
	}
	return wrapped
}

// taskNumericID returns the numeric suffix of a "task-<n>" id.
func taskNumericID(id string) (int, bool) {
	trimmed := strings.TrimSpace(id)
	if !strings.HasPrefix(trimmed, "task-") {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimPrefix(trimmed, "task-"))
	if err != nil || value < 1 {
		return 0, false
	}
	return value, true
}

// hydrateTaskBoard replays the persisted board into a fresh rig task manager
// and returns the tombstoned ids. It is best effort: failures are logged and the
// board stays partial instead of failing module activation.
func hydrateTaskBoard(ctx context.Context, manager *team.TaskManager, tasks []rig.TeamTask,
	deleted []string, logf func(string, ...any)) map[string]bool {
	deletedSet := make(map[string]bool, len(deleted))
	for _, id := range deleted {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			deletedSet[trimmed] = true
		}
	}
	if len(tasks) == 0 {
		return deletedSet
	}
	sorted := append([]rig.TeamTask(nil), tasks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, leftOK := taskNumericID(sorted[i].ID)
		right, rightOK := taskNumericID(sorted[j].ID)
		if leftOK && rightOK {
			return left < right
		}
		return sorted[i].ID < sorted[j].ID
	})
	// Replay in ascending order through the auto-numbered create path, which
	// reproduces the manager's own task-1..task-N numbering. A document with a
	// gap (never produced by this product) is renumbered consistently and the
	// references are rewritten through remap.
	remap := make(map[string]string, len(sorted))
	contiguous := true
	for index, task := range sorted {
		if id, ok := taskNumericID(task.ID); !ok || id != index+1 {
			contiguous = false
			break
		}
	}
	if !contiguous {
		logf("[toolkit] task board ids were not contiguous; renumbering %d tasks", len(sorted))
	}
	for _, task := range sorted {
		owner := strings.TrimSpace(task.Owner)
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if status == team.TaskStatusInProgress && owner == "" {
			owner = "taskboard"
		}
		// Create with an empty id: the manager's own sequence advances (an
		// explicit id does not), so the next live task continues after the
		// restored ones instead of overwriting task-1.
		replayed, err := manager.Create(ctx, rig.TeamTask{
			Subject:     task.Subject,
			Description: task.Description,
			Status:      status,
			Owner:       owner,
			WriteScopes: task.WriteScopes,
			Created:     task.Created,
		})
		if err != nil {
			logf("[toolkit] replay task %q: %v", task.ID, err)
			continue
		}
		remap[task.ID] = replayed.ID
	}
	// Dependencies after every task exists (Create validates their presence).
	for _, task := range sorted {
		if len(task.BlockedBy) == 0 {
			continue
		}
		mapped := make([]string, 0, len(task.BlockedBy))
		for _, dependency := range task.BlockedBy {
			if remapped, ok := remap[dependency]; ok {
				mapped = append(mapped, remapped)
			}
		}
		if len(mapped) == 0 {
			continue
		}
		if _, err := manager.Update(ctx, remap[task.ID], rig.TeamTaskUpdate{AddBlocked: mapped}); err != nil {
			logf("[toolkit] replay task %q dependencies: %v", task.ID, err)
		}
	}
	if len(remap) > 0 {
		remappedDeleted := make(map[string]bool, len(deletedSet))
		for id := range deletedSet {
			if mapped, ok := remap[id]; ok {
				remappedDeleted[mapped] = true
			} else {
				remappedDeleted[id] = true
			}
		}
		return remappedDeleted
	}
	return deletedSet
}
