package toolkit

// Per-session shared task board.
//
// DSH's team task board belongs to the Lead session that created it: another
// conversation must not see or mutate those tasks. The toolkit registers one
// process-wide set of team_task_* tools, so each tool invocation reads the
// run-scoped session ID from its context (contracts.AgentSessionFromContext,
// stamped by the agent module) and dispatches to that session's own rig task
// manager. Every session owns one durable document under team-tasks/, while the
// v1 process-wide document is migrated once into the first session that uses the
// board (so an upgrade keeps the tasks a user already had).
//
// Threading rules:
//   - module activation may read/prune the documents (startup I/O);
//   - a tool call may lazily load/migrate one session's document (run goroutine);
//   - the session UI's TaskBoardForSession is cache-first: a preloaded/cached
//     board never touches the disk, and a cache miss (a new or evicted
//     conversation) performs at most one bounded document read without any
//     migration, quarantine or write.
//
// KeyTaskBoard keeps its process-wide aggregate view for host surfaces that have
// no session context (headless probes, the interactive driver); the session UI
// uses contracts.SessionTaskBoard.TaskBoardForSession.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// taskBoardSessionFileKeep bounds how many per-session documents survive
	// activation; the oldest are pruned.
	taskBoardSessionFileKeep = 128
	// taskBoardSessionFileTTL drops documents of conversations older than this.
	taskBoardSessionFileTTL = 30 * 24 * time.Hour
	// taskBoardSessionCacheMax bounds the in-process board cache.
	taskBoardSessionCacheMax = 192
)

// taskBoardService is the process-wide owner of the per-session boards and of
// the session-aware task tools.
type taskBoardService struct {
	mu            sync.Mutex
	dataDir       string
	logf          func(string, ...any)
	legacy        *taskBoardAdapter
	legacyRetired bool
	sessions      map[string]*taskBoardAdapter
	// sessionTools caches the model-facing tools bound to one session board.
	sessionTools map[string]map[string]rig.Tool
	lastUsed     map[string]time.Time
	// deleted tombstones conversations whose board was removed: a late tool
	// call must never recreate the document (DeleteSession cancels runs
	// asynchronously).
	deleted      map[string]struct{}
	deletedOrder []string
	// migrationBlockedKey is the session whose pending v1 migration failed: its
	// task tools refuse until the migration commits, so a partial migration can
	// never be mistaken for a completed one.
	migrationBlockedKey string
	// migrationTarget is the conversation the in-flight v1 migration belongs to
	// (written to the journal so a restart finalizes instead of duplicating).
	migrationTarget string
	// stores indexes every materialized board store by document path, so a
	// deletion can retire a store whose adapter was already evicted from the
	// cache (a tool resolved before the deletion may still hold it).
	stores sync.Map
}

var (
	_ contracts.TaskBoard        = (*taskBoardService)(nil)
	_ contracts.TaskAssigner     = (*taskBoardService)(nil)
	_ contracts.SessionTaskBoard = (*taskBoardService)(nil)
	_ contracts.TaskBoardCleaner = (*taskBoardService)(nil)
)

// newTaskBoardService returns the board service for dataDir ("" keeps every
// board in memory).
func newTaskBoardService(dataDir string, logf func(string, ...any)) *taskBoardService {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	service := &taskBoardService{
		dataDir:      strings.TrimSpace(dataDir),
		logf:         logf,
		sessions:     make(map[string]*taskBoardAdapter),
		sessionTools: make(map[string]map[string]rig.Tool),
		lastUsed:     make(map[string]time.Time),
		deleted:      make(map[string]struct{}),
	}
	service.loadLegacy()
	service.preloadSessions()
	return service
}

// sessionKey is the cache key of one session board: the sanitized file stem.
// Safe conversation ids are used verbatim; rewritten ids carry a hash suffix.
func sessionKey(sessionID string) string {
	return sanitizeSessionFileName(strings.TrimSpace(sessionID))
}

// legacyPaths returns the four v1 board paths: the live document, the
// in-flight migration claim, the target journal and the done marker.
func (s *taskBoardService) legacyPaths() (legacy, migrating, target, marker string) {
	legacy = taskBoardFilePath(s.dataDir, "")
	if legacy == "" {
		return "", "", "", ""
	}
	return legacy, legacy + ".migrating", legacy + ".migrating.target", legacy + ".migrated"
}

// loadLegacy loads (or retires) the v1 process-wide document at startup. An
// interrupted migration (the journal exists) is recovered, not replayed blindly.
func (s *taskBoardService) loadLegacy() {
	legacyPath, migrating, targetFile, marker := s.legacyPaths()
	if legacyPath == "" {
		s.legacy = s.newAdapter("", false)
		return
	}
	if fileExists(marker) {
		// An interrupted re-migration (marker already present, journal claimed)
		// takes precedence over the marker: recover the claim instead of deleting
		// the only copy of the rollback document.
		if fileExists(migrating) {
			s.legacy = s.newAdapterAt(migrating)
			if s.legacy.store != nil {
				s.legacy.store.retire()
			}
			if data, err := os.ReadFile(targetFile); err == nil {
				s.migrationTarget = strings.TrimSpace(string(data))
			}
			return
		}
		// Migration finished. If a v1 document appeared afterwards (rollback or
		// an old binary), migrate it rather than deleting user tasks silently.
		removeFile(targetFile, s.logf)
		if fileExists(legacyPath) {
			s.legacy = s.newAdapter("", true)
			if !s.legacy.loadOK {
				// Unreadable: keep the document and let the migration block
				// instead of destroying it.
				s.legacyRetired = false
				return
			}
			return
		}
		s.legacy = s.newAdapter("", false)
		if s.legacy.store != nil {
			s.legacy.store.retire()
		}
		s.legacyRetired = true
		return
	}
	if fileExists(migrating) {
		// A previous process claimed the migration but did not finish: keep the
		// claimed document read-only as the pending source and remember the
		// target conversation (if the journal made it that far).
		s.legacy = s.newAdapterAt(migrating)
		if s.legacy.store != nil {
			s.legacy.store.retire()
		}
		if data, err := os.ReadFile(targetFile); err == nil {
			s.migrationTarget = strings.TrimSpace(string(data))
		}
		return
	}
	s.legacy = s.newAdapter("", true)
}

// preloadSessions bounds the session documents and hydrates the kept ones into
// memory, so the first team-overlay open of an existing conversation is a pure
// in-memory read.
func (s *taskBoardService) preloadSessions() {
	if s.dataDir == "" {
		return
	}
	dir := filepath.Join(s.dataDir, "team-tasks")
	kept := pruneTaskBoardDir(dir, taskBoardSessionFileKeep, taskBoardSessionFileTTL, s.logf)
	for _, path := range kept {
		base := filepath.Base(path)
		if strings.Contains(base, ".corrupt-") {
			// A quarantined backup is not a live board: never re-quarantine it.
			continue
		}
		key := strings.TrimSuffix(base, ".json")
		if key == "" {
			continue
		}
		if _, exists := s.sessions[key]; exists {
			continue
		}
		board := s.newAdapterAt(path)
		s.sessions[key] = board
		s.lastUsed[key] = time.Now()
	}
}

// registerStore indexes a materialized document store for later retirement.
func (s *taskBoardService) registerStore(store *taskBoardStore) {
	if store == nil {
		return
	}
	s.stores.Store(store, store)
}

// deleteStores atomically retires and removes every store registered for one
// document path, including superseded stores still held by evicted adapters.
func (s *taskBoardService) deleteStores(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	var firstErr error
	s.stores.Range(func(key, value any) bool {
		store, ok := value.(*taskBoardStore)
		if ok && store.path == path {
			if err := store.deleteDocument(); err != nil && firstErr == nil {
				firstErr = err
			}
			s.stores.Delete(key)
		}
		return true
	})
	return firstErr
}

// newAdapter builds one board (manager + durable document) for a session scope.
func (s *taskBoardService) newAdapter(sessionID string, load bool) *taskBoardAdapter {
	manager := team.NewTaskManager()
	var store *taskBoardStore
	var deleted map[string]bool
	loadOK := false
	if path := taskBoardFilePath(s.dataDir, sessionID); path != "" {
		store = newTaskBoardStoreAt(path, s.logf)
		s.registerStore(store)
		if load {
			tasks, tombstones, ok := store.load()
			loadOK = ok
			if ok {
				deleted = hydrateTaskBoard(context.Background(), manager, tasks, tombstones, s.logf)
			}
		}
	}
	board := newTaskBoard(manager, store)
	board.loadOK = loadOK
	board.setDeleted(deleted)
	return board
}

// newAdapterReadOnly builds one board for the UI read path: a corrupt document
// is treated as empty without the quarantine rename (a frame never writes).
func (s *taskBoardService) newAdapterReadOnly(sessionID string) *taskBoardAdapter {
	manager := team.NewTaskManager()
	var store *taskBoardStore
	var deleted map[string]bool
	loadOK := false
	if path := taskBoardFilePath(s.dataDir, sessionID); path != "" {
		store = newTaskBoardStoreAt(path, s.logf)
		s.registerStore(store)
		tasks, tombstones, ok := store.loadReadOnly()
		loadOK = ok
		if ok {
			deleted = hydrateTaskBoard(context.Background(), manager, tasks, tombstones, s.logf)
		}
	}
	board := newTaskBoard(manager, store)
	board.loadOK = loadOK
	board.setDeleted(deleted)
	return board
}

// newAdapterAt builds the board for an explicit document path.
func (s *taskBoardService) newAdapterAt(path string) *taskBoardAdapter {
	manager := team.NewTaskManager()
	var deleted map[string]bool
	store := newTaskBoardStoreAt(path, s.logf)
	s.registerStore(store)
	tasks, tombstones, ok := store.load()
	if ok {
		deleted = hydrateTaskBoard(context.Background(), manager, tasks, tombstones, s.logf)
	}
	board := newTaskBoard(manager, store)
	board.loadOK = ok
	board.setDeleted(deleted)
	return board
}

// adapterForTool returns the board of one session for a tool/host mutation,
// loading (and one-time migrating) the document when needed.
func (s *taskBoardService) adapterForTool(sessionID string) *taskBoardAdapter {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.legacy
	}
	key := sessionKey(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adapterForToolLocked(sessionID, key)
}

// adapterForToolLocked is adapterForTool for a caller holding the lock.
func (s *taskBoardService) adapterForToolLocked(sessionID, key string) *taskBoardAdapter {
	if sessionID == "" {
		// No session context: the legacy/process-wide board, never a migration
		// target (the v1 document migrates into a real conversation only).
		return s.legacy
	}
	if _, removed := s.deleted[key]; removed {
		return s.deletedBoardLocked(sessionID, key)
	}
	if board, ok := s.sessions[key]; ok {
		s.lastUsed[key] = time.Now()
		s.runMigrationLocked(sessionID, key, board)
		return board
	}
	board := s.newAdapter(sessionID, true)
	s.cacheLocked(key, board)
	s.runMigrationLocked(sessionID, key, board)
	return board
}

// deletedBoardLocked returns a memory-only, read-only board for a conversation
// whose document was removed, so a late tool call cannot recreate it.
func (s *taskBoardService) deletedBoardLocked(sessionID, key string) *taskBoardAdapter {
	if board, ok := s.sessions[key]; ok {
		return board
	}
	board := s.newAdapter(sessionID, false)
	if board.store != nil {
		board.store.retire()
	}
	s.cacheLocked(key, board)
	return board
}

// adapterForUI returns the cached board of one session for the UI read path.
// Existing documents are preloaded at activation; a cache miss (a new or
// evicted conversation) performs at most one bounded read-only document load
// and never triggers a migration, quarantine or write.
func (s *taskBoardService) adapterForUI(sessionID string) *taskBoardAdapter {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		return s.legacy
	}
	key := sessionKey(sessionID)
	if _, removed := s.deleted[key]; removed {
		return s.deletedBoardLocked(sessionID, key)
	}
	if board, ok := s.sessions[key]; ok {
		s.lastUsed[key] = time.Now()
		return board
	}
	board := s.newAdapterReadOnly(sessionID)
	s.cacheLocked(key, board)
	return board
}

// cacheLocked stores one board and keeps the in-process cache bounded. The
// cache only holds loaded boards: an evicted board is re-read from its document
// by the next access, so it can never shadow persisted state with an empty one.
func (s *taskBoardService) cacheLocked(key string, board *taskBoardAdapter) {
	s.sessions[key] = board
	s.lastUsed[key] = time.Now()
	for len(s.sessions) > taskBoardSessionCacheMax {
		oldest := ""
		oldestUsed := time.Time{}
		for candidate, used := range s.lastUsed {
			if _, ok := s.sessions[candidate]; !ok {
				continue
			}
			if oldest == "" || used.Before(oldestUsed) {
				oldest = candidate
				oldestUsed = used
			}
		}
		if oldest == "" {
			break
		}
		delete(s.sessions, oldest)
		delete(s.sessionTools, oldest)
		delete(s.lastUsed, oldest)
	}
}

// migrationPendingLocked reports whether a v1 migration journal is still
// outstanding. While it is, mutating task surfaces stay blocked so a partial
// migration can never be finalized over legacy tasks.
func (s *taskBoardService) migrationPendingLocked() bool {
	if s.legacyRetired {
		return false
	}
	legacyPath, migrating, _, _ := s.legacyPaths()
	return fileExists(legacyPath) || fileExists(migrating) || s.migrationTarget != ""
}

// migrationPending returns migrationPendingLocked under the service lock.
func (s *taskBoardService) migrationPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migrationPendingLocked()
}

// mergeLegacyTasksLocked reconciles the v1 source tasks into the target board by
// content. It is a multiset merge: duplicate-content source tasks each claim one
// distinct existing id (or create one), so a legal duplicate is never collapsed;
// tombstoned ids are remapped onto the target board's tombstone set so deleted
// tasks stay deleted. A retry after a failed persist or a crash is idempotent.
func (s *taskBoardService) mergeLegacyTasksLocked(board *taskBoardAdapter, tasks []rig.TeamTask) (int, error) {
	existing, err := board.manager.List(context.Background(), rig.TeamTaskFilter{})
	if err != nil {
		return 0, err
	}
	contentKey := func(task rig.TeamTask) string {
		return strings.Join([]string{
			strings.TrimSpace(task.Subject),
			strings.TrimSpace(task.Description),
			strings.ToLower(strings.TrimSpace(task.Status)),
			strings.TrimSpace(task.Owner),
			strings.Join(task.WriteScopes, "\x00"),
		}, "\x01")
	}
	// A queue per content key: each source occurrence consumes one distinct id.
	available := make(map[string][]string, len(existing))
	for _, task := range existing {
		key := contentKey(rig.TeamTask{
			Subject: task.Subject, Description: task.Description, Status: task.Status,
			Owner: task.Owner, WriteScopes: task.WriteScopes,
		})
		available[key] = append(available[key], task.ID)
	}
	deletedIDs := s.legacy.deletedIDs()
	tombstoned := make(map[string]bool, len(deletedIDs))
	for _, id := range deletedIDs {
		tombstoned[id] = true
	}
	remap := make(map[string]string, len(tasks))
	created := 0
	for _, task := range tasks {
		if tombstoned[task.ID] {
			continue
		}
		key := contentKey(task)
		if ids := available[key]; len(ids) > 0 {
			remap[task.ID] = ids[0]
			available[key] = ids[1:]
			continue
		}
		owner := strings.TrimSpace(task.Owner)
		status := strings.ToLower(strings.TrimSpace(task.Status))
		if status == team.TaskStatusInProgress && owner == "" {
			owner = "taskboard"
		}
		replayed, createErr := board.manager.Create(context.Background(), rig.TeamTask{
			Subject:     task.Subject,
			Description: task.Description,
			Status:      status,
			Owner:       owner,
			WriteScopes: task.WriteScopes,
			Created:     task.Created,
		})
		if createErr != nil {
			return created, createErr
		}
		remap[task.ID] = replayed.ID
		created++
	}
	// Restore dependencies after every mapped id exists.
	for _, task := range tasks {
		if tombstoned[task.ID] || len(task.BlockedBy) == 0 {
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
		if _, updateErr := board.manager.Update(context.Background(), remap[task.ID], rig.TeamTaskUpdate{AddBlocked: mapped}); updateErr != nil {
			s.logf("[toolkit] merge legacy task %q dependencies: %v", task.ID, updateErr)
		}
	}
	if len(deletedIDs) > 0 {
		// Only tombstones that actually mapped onto a target task are carried
		// over. A raw fallback would collide with the target board's own task-N
		// numbering and hide live/future tasks (tombstoned source tasks are never
		// created, so they need no tombstone at all).
		remappedDeleted := make(map[string]bool, len(deletedIDs))
		for _, id := range deletedIDs {
			if mapped, ok := remap[id]; ok {
				remappedDeleted[mapped] = true
			}
		}
		if len(remappedDeleted) > 0 {
			board.setDeleted(remappedDeleted)
		}
	}
	return created, nil
}

// reloadLegacySourceLocked re-reads an unreadable legacy source in-process (the
// data dir may have been repaired since activation).
func (s *taskBoardService) reloadLegacySourceLocked() bool {
	legacyPath, migrating, targetFile, _ := s.legacyPaths()
	path := ""
	if fileExists(migrating) {
		path = migrating
	} else if fileExists(legacyPath) {
		path = legacyPath
	}
	if path == "" {
		return false
	}
	board := s.newAdapterAt(path)
	if board.store != nil {
		board.store.retire()
	}
	if !board.loadOK {
		return false
	}
	s.legacy = board
	if data, err := os.ReadFile(targetFile); err == nil {
		s.migrationTarget = strings.TrimSpace(string(data))
	}
	return true
}

// runMigrationLocked drives the one-time migration for one session and records
// a failure so that session's task tools stay blocked until it commits.
func (s *taskBoardService) runMigrationLocked(sessionID, key string, board *taskBoardAdapter) {
	if s.legacyRetired {
		if s.migrationBlockedKey == key {
			s.migrationBlockedKey = ""
		}
		return
	}
	if err := s.migrateLegacyLocked(sessionID, board); err != nil {
		s.logf("[toolkit] legacy task board migration pending for %q: %v", sessionID, err)
		s.migrationBlockedKey = key
		return
	}
	if s.migrationBlockedKey == key {
		s.migrationBlockedKey = ""
	}
}

// migrateLegacyLocked replays the v1 process-wide board into the first session
// board that a model task tool uses. It is crash-safe: the source document is
// renamed to a journal (".migrating"), the target conversation and the
// persisted board are recorded before the done marker, so a restart either
// retries the migration once or finalizes an already-persisted one. Any failure
// leaves the journal in place and returns an error; the caller blocks that
// session's task tools so a partial migration cannot be finalized as complete.
func (s *taskBoardService) migrateLegacyLocked(sessionID string, board *taskBoardAdapter) error {
	if s.legacyRetired || board == nil {
		return nil
	}
	if s.migrationTarget != "" && s.migrationTarget != sessionID {
		// A previous process was migrating into a specific conversation; only
		// that conversation may finish the migration (avoids duplicates).
		return nil
	}
	legacyPath, migrating, targetFile, marker := s.legacyPaths()
	tasks, err := s.legacy.manager.List(context.Background(), rig.TeamTaskFilter{})
	if err != nil {
		return err
	}
	if len(tasks) == 0 && !s.legacy.loadOK && (fileExists(legacyPath) || fileExists(migrating)) {
		if !s.reloadLegacySourceLocked() {
			return errors.New("legacy task board source is unreadable")
		}
		tasks, err = s.legacy.manager.List(context.Background(), rig.TeamTaskFilter{})
		if err != nil {
			return err
		}
	}
	if len(tasks) == 0 {
		if fileExists(legacyPath) || fileExists(migrating) {
			if s.legacy.loadOK {
				// A valid-but-empty v1 document: claim it first so finalize
				// removes the source instead of leaving it next to the marker.
				if fileExists(legacyPath) {
					if renameErr := os.Rename(legacyPath, migrating); renameErr != nil && !os.IsNotExist(renameErr) {
						return renameErr
					}
				}
				return s.finalizeMigrationLocked(board, legacyPath, migrating, targetFile, marker)
			}
			// An existing source document that could not be read is not
			// genuinely empty: never retire it, or the pre-upgrade tasks would
			// be lost.
			return errors.New("legacy task board source is unreadable")
		}
		// Fresh install: nothing to migrate and no marker to write (a marker
		// would later make a real v1 document look already-migrated).
		return nil
	}
	// Claim the source document before persisting anything (rename is atomic).
	if fileExists(legacyPath) {
		if renameErr := os.Rename(legacyPath, migrating); renameErr != nil && !os.IsNotExist(renameErr) {
			return renameErr
		}
	}
	if legacyPath != "" {
		// Journal the real session ID (not the sanitized file stem): the target
		// guard and the delete hook compare conversation IDs.
		if writeErr := writeFileAtomic(targetFile, []byte(sessionID)); writeErr != nil {
			return writeErr
		}
		s.migrationTarget = sessionID
	}
	// Merge the source tasks into the target board by content: a retry (after a
	// failed persist or a crash), a late rollback document and an already
	// non-empty conversation must neither duplicate nor drop tasks.
	merged, mergeErr := s.mergeLegacyTasksLocked(board, tasks)
	if mergeErr != nil {
		return mergeErr
	}
	if merged > 0 {
		s.logf("[toolkit] migrated %d legacy shared task(s) into session %q", merged, sessionID)
	}
	// Persist on every attempt (idempotent): if an earlier attempt merged in
	// memory but its write failed, the tasks must reach the document before the
	// journal is retired.
	if persistErr := board.persist(); persistErr != nil {
		return persistErr
	}
	return s.finalizeMigrationLocked(board, legacyPath, migrating, targetFile, marker)
}

// finalizeMigrationLocked removes the journal, writes the done marker and turns
// the legacy view into an empty read-only board.
func (s *taskBoardService) finalizeMigrationLocked(board *taskBoardAdapter, legacyPath, migrating, targetFile, marker string) error {
	removeFile(migrating, s.logf)
	removeFile(targetFile, s.logf)
	if marker != "" {
		if err := writeFileAtomic(marker, []byte("migrated")); err != nil {
			s.logf("[toolkit] write task board migration marker: %v", err)
			return err
		}
	}
	if s.legacy.store != nil {
		s.legacy.store.retire()
	}
	s.legacyRetired = true
	s.migrationTarget = ""
	empty := s.newAdapter("", false)
	if empty.store != nil {
		empty.store.retire()
	}
	s.legacy = empty
	delete(s.sessionTools, "")
	s.migrationBlockedKey = ""
	_ = board
	return nil
}

// tools returns the model-facing task tools: one wrapper per task tool name that
// dispatches to the calling session's board.
func (s *taskBoardService) tools() []rig.Tool {
	base := team.TaskTools(team.NewTaskManager())
	tools := make([]rig.Tool, 0, len(base))
	for _, tool := range base {
		if tool == nil {
			continue
		}
		tools = append(tools, &sessionTaskTool{name: tool.Name(), base: tool, service: s})
	}
	return tools
}

// toolFor returns the task tool bound to one session's board, creating the bound
// tool set on first use.
func (s *taskBoardService) toolFor(sessionID, name string) rig.Tool {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ""
	if sessionID != "" {
		key = sessionKey(sessionID)
	}
	if tools := s.sessionTools[key]; tools != nil {
		s.lastUsed[key] = time.Now()
		if !s.legacyRetired {
			// A tool set may be cached while a failed migration left the journal
			// pending: retry from the cached board before serving the call.
			if board, ok := s.sessions[key]; ok {
				s.runMigrationLocked(sessionID, key, board)
			}
		}
		if key != "" && s.migrationBlockedKey == key {
			return nil
		}
		if key != "" && s.migrationPendingLocked() && taskToolMutates(name) {
			return nil
		}
		return tools[name]
	}
	board := s.adapterForToolLocked(sessionID, key)
	if key != "" && s.migrationBlockedKey == key {
		return nil
	}
	if key != "" && s.migrationPendingLocked() && taskToolMutates(name) {
		return nil
	}
	bound := wrapPersistingTaskTools(team.TaskTools(board.manager), board)
	tools := make(map[string]rig.Tool, len(bound))
	for _, tool := range bound {
		if tool != nil {
			tools[tool.Name()] = tool
		}
	}
	s.sessionTools[key] = tools
	return tools[name]
}

// errTaskBoardMigrationPending rejects mutations while a v1 migration journal
// is outstanding.
var errTaskBoardMigrationPending = errors.New("toolkit: task board migration pending")

// boardForOperation routes one board mutation to the run's session board (the
// legacy board when the call has no session context). Mutations are refused
// while a migration journal is pending.
func (s *taskBoardService) boardForOperation(ctx context.Context) (*taskBoardAdapter, error) {
	sessionID := contracts.AgentSessionFromContext(ctx)
	board := s.adapterForTool(sessionID)
	if sessionID != "" && s.migrationPending() {
		// The process-wide legacy board (no session context) stays usable: the
		// migration only replaces it with per-session boards.
		return nil, errTaskBoardMigrationPending
	}
	return board, nil
}

// List implements contracts.TaskBoard: the aggregate view across the legacy
// board and every cached session board, oldest first. Host surfaces with no
// session context use it; the session UI filters through
// TaskBoardForSession.
func (s *taskBoardService) List(ctx context.Context) ([]contracts.BoardTask, error) {
	s.mu.Lock()
	boards := make([]*taskBoardAdapter, 0, len(s.sessions)+1)
	boards = append(boards, s.legacy)
	for _, board := range s.sessions {
		boards = append(boards, board)
	}
	s.mu.Unlock()
	var tasks []contracts.BoardTask
	for _, board := range boards {
		rows, err := board.List(ctx)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, rows...)
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Created.Before(tasks[j].Created) })
	return tasks, nil
}

// Create implements contracts.TaskBoard.
func (s *taskBoardService) Create(ctx context.Context, subject, description string) (contracts.BoardTask, error) {
	board, err := s.boardForOperation(ctx)
	if err != nil {
		return contracts.BoardTask{}, err
	}
	return board.Create(ctx, subject, description)
}

// SetStatus implements contracts.TaskBoard.
func (s *taskBoardService) SetStatus(ctx context.Context, id, status string) (contracts.BoardTask, error) {
	board, err := s.boardForOperation(ctx)
	if err != nil {
		return contracts.BoardTask{}, err
	}
	return board.SetStatus(ctx, id, status)
}

// Delete implements contracts.TaskBoard.
func (s *taskBoardService) Delete(ctx context.Context, id string) error {
	board, err := s.boardForOperation(ctx)
	if err != nil {
		// Deletion stays available while a migration is pending (a full target
		// board must remain recoverable).
		board = s.adapterForTool(contracts.AgentSessionFromContext(ctx))
	}
	return board.Delete(ctx, id)
}

// Assign implements contracts.TaskAssigner. A session-scoped call only touches
// that session's board: task ids repeat across conversations, so a cross-board
// fallback would silently mutate another conversation's task.
func (s *taskBoardService) Assign(ctx context.Context, id, owner string) (contracts.BoardTask, error) {
	board, err := s.boardForOperation(ctx)
	if err != nil {
		return contracts.BoardTask{}, err
	}
	return board.Assign(ctx, id, owner)
}

// TaskBoardForSession implements contracts.SessionTaskBoard: the session UI
// reads only the active conversation's tasks, from memory.
func (s *taskBoardService) TaskBoardForSession(sessionID string) contracts.TaskBoard {
	return s.adapterForUI(sessionID)
}

// RemoveTaskBoardForSession implements contracts.TaskBoardCleaner: deleting a
// conversation removes its board document (even when the board was evicted from
// the cache) and tombstones the ID so a late tool call cannot recreate it.
func (s *taskBoardService) RemoveTaskBoardForSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	key := sessionKey(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if board, ok := s.sessions[key]; ok {
		// Retire the adapter first: a tool already bound to it must not persist
		// the deleted document back to disk.
		board.retire()
	}
	delete(s.sessions, key)
	delete(s.sessionTools, key)
	delete(s.lastUsed, key)
	s.deleted[key] = struct{}{}
	s.deletedOrder = append(s.deletedOrder, key)
	for len(s.deletedOrder) > 4096 {
		oldest := s.deletedOrder[0]
		s.deletedOrder = s.deletedOrder[1:]
		delete(s.deleted, oldest)
	}
	if s.migrationTarget == sessionID {
		// The journal's target conversation is gone: release the journal so any
		// later conversation can finish the migration instead of stranding it.
		// The idempotent content merge makes that retry safe even into a board
		// that already has tasks.
		s.migrationTarget = ""
		_, _, targetFile, _ := s.legacyPaths()
		removeFile(targetFile, s.logf)
	}

	path := taskBoardFilePath(s.dataDir, sessionID)
	if err := s.deleteStores(path); err != nil {
		// Deleting a conversation must not fail because its board file is
		// already unreachable (missing parent/ENOTDIR); attempt the fallback and
		// log real errors.
		s.logf("[toolkit] remove task board stores for %q: %v", sessionID, err)
	}
	// Fallback for documents no registered store knows about (external writer,
	// second service instance): delete by path as a best effort.
	removeFile(path, s.logf)
	if s.migrationBlockedKey == key {
		s.migrationBlockedKey = ""
	}
	return nil
}

// sessionTaskTool is one model-facing task tool that dispatches to the board of
// the run's session.
type sessionTaskTool struct {
	name    string
	base    rig.Tool
	service *taskBoardService
}

var _ rig.Tool = (*sessionTaskTool)(nil)

// Name implements rig.Tool.
func (t *sessionTaskTool) Name() string { return t.name }

// Description implements rig.Tool.
func (t *sessionTaskTool) Description() string { return t.base.Description() }

// Schema implements rig.Tool.
func (t *sessionTaskTool) Schema() map[string]any { return t.base.Schema() }

// Invoke implements rig.Tool.
func (t *sessionTaskTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	tool := t.service.toolFor(contracts.AgentSessionFromContext(ctx), t.name)
	if tool == nil {
		return rig.ToolResult{
			Content: "task board is unavailable for this session",
			IsError: true,
		}, nil
	}
	return tool.Invoke(ctx, call)
}
