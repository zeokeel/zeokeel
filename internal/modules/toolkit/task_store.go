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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	mu      sync.Mutex
	path    string
	logf    func(string, ...any)
	retired bool
}

// retire makes the store read-only/terminal: later saves are no-ops. The v0.2.31
// process-wide document is retired after its one-time migration so a stale
// writer can never recreate it and get re-migrated into duplicate tasks.
func (s *taskBoardStore) retire() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.retired = true
	s.mu.Unlock()
}

// taskBoardFilePath returns the document path for one board scope. The legacy
// (process-wide) board keeps the v1 path; each session owns one file under
// team-tasks/ so concurrent sessions never share task ids or state.
func taskBoardFilePath(dataDir, sessionID string) string {
	dir := strings.TrimSpace(dataDir)
	if dir == "" {
		return ""
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return filepath.Join(dir, taskBoardFileName)
	}
	return filepath.Join(dir, "team-tasks", sanitizeSessionFileName(sessionID)+".json")
}

// sanitizeSessionFileName makes a session id safe as a file name while keeping
// it recognisable; ids that need rewriting get a short hash suffix so two
// distinct sessions can never collide.
func sanitizeSessionFileName(sessionID string) string {
	var builder strings.Builder
	clean := true
	for _, char := range sessionID {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9', char == '-', char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
			clean = false
		}
	}
	name := builder.String()
	if name == "" {
		name = "session"
		clean = false
	}
	if !clean {
		name += "-" + shortHash(sessionID)
	}
	return name
}

// shortHash returns the first 8 hex characters of the session id's SHA-256.
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

// newTaskBoardStoreAt returns the store for one explicit document path.
func newTaskBoardStoreAt(path string, logf func(string, ...any)) *taskBoardStore {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &taskBoardStore{path: path, logf: logf}
}

// deleteDocument retires the store and removes its document atomically with
// respect to save: a save already in flight finishes under the lock before the
// file is removed, and every later save is a no-op.
func (s *taskBoardStore) deleteDocument() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retired = true
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) && !isNotDirOrMissing(err) {
		return err
	}
	return nil
}

// isNotDirOrMissing treats ENOTDIR like a missing path (a parent turned into a
// file during teardown); the document cannot exist in that state.
func isNotDirOrMissing(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}

// load reads the persisted board. It reports ok=true when the document exists
// and parsed (a valid-but-empty board included); a corrupt document is
// quarantined and reports ok=false.
func (s *taskBoardStore) load() (tasks []rig.TeamTask, deleted []string, ok bool) {
	return s.loadDocument(true)
}

// loadReadOnly is load without the corrupt-document quarantine rename. The
// session UI uses it on its one bounded cache-miss read so a frame callback can
// never write to disk; a corrupt document simply reads as an empty board (a
// later adapter that loads the path may quarantine it).
func (s *taskBoardStore) loadReadOnly() (tasks []rig.TeamTask, deleted []string, ok bool) {
	return s.loadDocument(false)
}

// loadDocument is the shared parser; quarantine controls the corrupt recovery.
func (s *taskBoardStore) loadDocument(quarantine bool) (tasks []rig.TeamTask, deleted []string, ok bool) {
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
		if quarantine {
			s.quarantine("task board document is not valid JSON: " + err.Error())
		}
		return nil, nil, false
	}
	if document.Version != taskBoardVersion {
		if quarantine {
			s.quarantine(fmt.Sprintf("task board document has unsupported version %d", document.Version))
		}
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
	return tasks, document.Deleted, true
}

// save snapshots the live board and writes the document atomically. It returns
// the write error so callers that must not lose state (the one-time migration)
// can defer their commit.
func (s *taskBoardStore) save(tasks []rig.TeamTask, deleted map[string]bool) error {
	if s == nil {
		return nil
	}
	// Hold the lock across the retired check AND the write: deleteDocument uses
	// the same lock, so an in-flight save can never recreate a removed document
	// after the deletion.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired {
		return nil
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
		return err
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		s.logf("[toolkit] create task board directory: %v", err)
		return err
	}
	temporary, err := os.CreateTemp(directory, ".team-tasks-*.tmp")
	if err != nil {
		s.logf("[toolkit] create temporary task board file: %v", err)
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		s.logf("[toolkit] write temporary task board file: %v", err)
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		s.logf("[toolkit] sync temporary task board file: %v", err)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		s.logf("[toolkit] close temporary task board file: %v", err)
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		s.logf("[toolkit] replace task board %q: %v", s.path, err)
		return err
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
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

// Invoke implements rig.Tool. Only the mutating task tools persist: a read-only
// list/get must not rewrite the document (that previously recreated a retired
// legacy document after migration).
func (t *persistingTaskTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	result, err := t.inner.Invoke(ctx, call)
	if err == nil && !result.IsError && taskToolMutates(t.inner.Name()) {
		t.board.persist()
	}
	return result, err
}

// taskToolMutates reports whether a task tool changes the board.
func taskToolMutates(name string) bool {
	switch name {
	case "team_task_create", "team_task_update":
		return true
	default:
		return false
	}
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

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// removeFile removes path, logging (not failing) unexpected errors.
func removeFile(path string, logf func(string, ...any)) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) && logf != nil {
		logf("[toolkit] remove %s: %v", path, err)
	}
}

// writeFileAtomic writes one small file through a temp file + rename so a crash
// never leaves a partial journal/marker.
func writeFileAtomic(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// taskBoardDocumentName recognizes a board document and its quarantined
// backups (`<name>.json`, `<name>.json.corrupt-<ts>`).
func taskBoardDocumentName(name string) bool {
	return strings.HasSuffix(name, ".json") || strings.Contains(name, ".json.corrupt-")
}

// pruneTaskBoardDir bounds the per-session documents: it keeps at most keep
// files, drops documents older than ttl and returns the retained paths (newest
// first). It runs at module activation, never on a UI frame.
func pruneTaskBoardDir(dir string, keep int, ttl time.Duration, logf func(string, ...any)) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type document struct {
		path    string
		modTime time.Time
	}
	documents := make([]document, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !taskBoardDocumentName(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		documents = append(documents, document{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].modTime.After(documents[j].modTime) })
	cutoff := time.Now().Add(-ttl)
	kept := make([]string, 0, len(documents))
	for index, document := range documents {
		if index >= keep || document.modTime.Before(cutoff) {
			if removeErr := os.Remove(document.path); removeErr != nil && !os.IsNotExist(removeErr) {
				logf("[toolkit] prune task board %s: %v", document.path, removeErr)
			}
			continue
		}
		kept = append(kept, document.path)
	}
	return kept
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
