package toolkit

// Per-session shared task board.
//
// DSH's team task board belongs to the Lead session that created it: another
// conversation must not see or mutate those tasks. The toolkit registers one
// process-wide set of team_task_* tools, so each tool invocation reads the
// run-scoped session ID from its context (contracts.AgentSessionFromContext,
// stamped by the agent module) and dispatches to that session's own rig task
// manager. Every session owns one durable document under team-tasks/ via the
// Rig SDK taskstore.Store; the v1 process-wide document is migrated once into
// the first session that uses the board (so an upgrade keeps the tasks a user
// already had). The product keeps only the upgrade journal around that
// migration.
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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/team"
	"github.com/zeokeel/zeokeel-rig/tools/team/taskstore"

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
	// stores indexes every materialized SDK store by pointer, so a deletion can
	// retire a store whose adapter was already evicted from the cache (a tool
	// resolved before the deletion may still hold it).
	stores sync.Map
	// closeMu guards closeObserver, which activation installs while boards may
	// already be created on other goroutines (mu is taken with it, and taking one
	// lock inside the other would deadlock the adapter paths).
	closeMu sync.Mutex
	// closeObserver is the audit sink for tasks closed without a lease review.
	// It is installed by the lease module (contracts.TaskCloseObserverSetter) and
	// copied onto every adapter this service creates.
	closeObserver contracts.TaskCloseAuditor
}

var (
	_ contracts.TaskBoard               = (*taskBoardService)(nil)
	_ contracts.TaskAssigner            = (*taskBoardService)(nil)
	_ contracts.SessionTaskBoard        = (*taskBoardService)(nil)
	_ contracts.TaskBoardRestorer       = (*taskBoardService)(nil)
	_ contracts.TaskBoardCleaner        = (*taskBoardService)(nil)
	_ contracts.TaskCloseObserverSetter = (*taskBoardService)(nil)
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

// sessionKey is the cache key of one session board: the document file stem
// (computed by the SDK's path layout, so it always matches the file on disk).
func (s *taskBoardService) sessionKey(sessionID string) string {
	path := taskBoardFilePath(s.dataDir, strings.TrimSpace(sessionID))
	if path == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(path), ".json")
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
	// A late v1 document parked by a previous finalize (the rollback case) is
	// restored before anything else so it can be migrated again.
	if fileExists(legacyPath+".recreated") && !fileExists(legacyPath) && !fileExists(migrating) {
		if err := os.Rename(legacyPath+".recreated", legacyPath); err != nil {
			s.logf("[toolkit] restore recreated legacy task board: %v", err)
		}
	}
	// An unusable claim cannot be migrated; quarantine it like a board document
	// so it does not block every session tool forever.
	if fileExists(migrating) && !taskBoardDocumentUsable(migrating) {
		s.quarantineJournal(migrating)
	}
	if fileExists(targetFile) {
		if data, err := os.ReadFile(targetFile); err == nil {
			s.migrationTarget = strings.TrimSpace(string(data))
		}
	}
	if fileExists(legacyPath) {
		s.legacy = s.newAdapter("", true)
		return
	}
	if fileExists(migrating) {
		// An interrupted claim: the journal itself is the migration source. The
		// legacy view stays empty (the SDK store owns the live document path).
		s.legacy = memoryTaskBoard()
		return
	}
	if fileExists(marker) {
		// Migration finished and no source reappeared.
		removeFile(targetFile, s.logf)
		s.migrationTarget = ""
		s.legacy = memoryTaskBoard()
		s.legacyRetired = true
		return
	}
	s.legacy = s.newAdapter("", true)
}

// quarantineJournal moves an unusable migration claim out of the way.
func (s *taskBoardService) quarantineJournal(path string) {
	backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UTC().UnixNano())
	if err := os.Rename(path, backup); err != nil {
		s.logf("[toolkit] quarantine task board journal %s: %v", path, err)
		return
	}
	s.logf("[toolkit] task board journal %s is unusable; quarantined as %s", path, backup)
}

// preloadSessions bounds the session documents and hydrates the kept ones into
// memory, so the first team-overlay open of an existing conversation is a pure
// in-memory read.
func (s *taskBoardService) preloadSessions() {
	if s.dataDir == "" {
		return
	}
	dir := filepath.Join(s.dataDir, taskstore.SessionDirName)
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
		board := s.newAdapter(key, true)
		s.sessions[key] = board
		s.lastUsed[key] = time.Now()
	}
}

// registerStore indexes a materialized document store for later retirement.
func (s *taskBoardService) registerStore(store *taskstore.Store) {
	if store == nil {
		return
	}
	s.stores.Store(store, store)
}

// deleteStores retires and removes every store registered for one document
// path, including superseded stores still held by evicted adapters.
func (s *taskBoardService) deleteStores(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	var firstErr error
	s.stores.Range(func(key, value any) bool {
		store, ok := value.(*taskstore.Store)
		if ok && store.Path() == path {
			if err := store.Delete(); err != nil && firstErr == nil {
				firstErr = err
			}
			s.stores.Delete(key)
		}
		return true
	})
	return firstErr
}

// newAdapter builds one board (manager + durable store) for a session scope.
func (s *taskBoardService) newAdapter(sessionID string, load bool) *taskBoardAdapter {
	manager := team.NewTaskManager()
	store := taskstore.New(s.dataDir, sessionID,
		taskstore.WithLogger(s.logf),
		taskstore.WithInProgressOwner("taskboard"),
	)
	s.registerStore(store)
	board := newTaskBoard(manager, store)
	board.logf = s.logf
	// Every adapter the service hands out must carry the audit sink: the lease
	// module installs it once, and a board created afterwards would otherwise
	// close tasks silently.
	board.SetTaskCloseObserver(s.currentCloseObserver())
	if !load || store.Path() == "" {
		return board
	}
	loaded, err := store.Load(context.Background(), manager)
	if err != nil {
		s.logf("[toolkit] load task board %q: %v", store.Path(), err)
		return board
	}
	board.loadOK = loaded.Loaded
	board.setDeleted(loaded.Deleted)
	return board
}

// newAdapterReadOnly builds one board for the UI read path without writing to
// disk: a missing/unusable document simply reads as an empty board (a later
// adapter that loads the path may quarantine it).
func (s *taskBoardService) newAdapterReadOnly(sessionID string) *taskBoardAdapter {
	if !taskBoardDocumentUsable(taskBoardFilePath(s.dataDir, sessionID)) {
		return s.newAdapter(sessionID, false)
	}
	return s.newAdapter(sessionID, true)
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
	key := s.sessionKey(sessionID)
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
		return s.deletedBoardLocked(key)
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

// deletedBoardLocked returns a memory-only board for a conversation whose
// document was removed, so a late tool call cannot recreate it.
func (s *taskBoardService) deletedBoardLocked(key string) *taskBoardAdapter {
	if board, ok := s.sessions[key]; ok {
		return board
	}
	board := memoryTaskBoard()
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
	key := s.sessionKey(sessionID)
	if _, removed := s.deleted[key]; removed {
		return s.deletedBoardLocked(key)
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

// reloadLegacySourceLocked re-reads an unreadable legacy source in-process (the
// data dir may have been repaired since activation).
func (s *taskBoardService) reloadLegacySourceLocked() bool {
	legacyPath, _, targetFile, _ := s.legacyPaths()
	if !fileExists(legacyPath) {
		return false
	}
	board := s.newAdapter("", true)
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
// board that a model task tool uses. The SDK store performs the content merge
// (idempotent on retry) and removes the source document; the product journal
// records the claim, the target conversation and the done marker so a crash is
// recovered instead of duplicating tasks. Any failure leaves the journal in
// place and returns an error; the caller blocks that session's task tools so a
// partial migration cannot be finalized as complete.
func (s *taskBoardService) migrateLegacyLocked(sessionID string, board *taskBoardAdapter) error {
	if s.legacyRetired || board == nil || board.store == nil {
		return nil
	}
	if s.migrationTarget != "" && s.migrationTarget != sessionID {
		// A previous process was migrating into a specific conversation; only
		// that conversation may finish the migration (avoids duplicates).
		return nil
	}
	legacyPath, migrating, targetFile, marker := s.legacyPaths()
	switch {
	case fileExists(migrating):
		if !taskBoardDocumentUsable(migrating) {
			return errors.New("legacy task board source is unreadable")
		}
		return s.commitLegacyMigrationLocked(sessionID, board, migrating, targetFile, marker)
	case fileExists(legacyPath):
		if !s.legacy.loadOK && !s.reloadLegacySourceLocked() {
			return errors.New("legacy task board source is unreadable")
		}
		tasks, err := s.legacy.manager.List(context.Background(), rig.TeamTaskFilter{})
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			// A parseable, empty v1 document is retired instead of blocking
			// every session tool forever.
			if err := os.Rename(legacyPath, migrating); err != nil && !os.IsNotExist(err) {
				return err
			}
			return s.finalizeMigrationLocked(migrating, targetFile, marker)
		}
		// Claim the source before writing anything (rename is atomic).
		if err := os.Rename(legacyPath, migrating); err != nil && !os.IsNotExist(err) {
			return err
		}
		return s.commitLegacyMigrationLocked(sessionID, board, migrating, targetFile, marker)
	default:
		// Fresh install: nothing to migrate and no marker to write (a marker
		// would later make a real v1 document look already-migrated).
		return nil
	}
}

// commitLegacyMigrationLocked journals the target, lets the SDK merge the
// source into the session board, and finalizes the journal.
func (s *taskBoardService) commitLegacyMigrationLocked(sessionID string, board *taskBoardAdapter,
	source, targetFile, marker string) error {
	if targetFile != "" {
		// Journal the real session ID (not the sanitized file stem): the target
		// guard and the delete hook compare conversation IDs.
		if err := writeFileAtomic(targetFile, []byte(sessionID)); err != nil {
			return err
		}
		s.migrationTarget = sessionID
	}
	merged, err := board.store.MigrateLegacy(context.Background(), board.manager, source)
	if err != nil {
		return err
	}
	if merged > 0 {
		s.logf("[toolkit] migrated %d legacy shared task(s) into session %q", merged, sessionID)
	}
	return s.finalizeMigrationLocked(source, targetFile, marker)
}

// finalizeMigrationLocked removes the journal, writes the done marker and turns
// the legacy view into an empty read-only board.
func (s *taskBoardService) finalizeMigrationLocked(journalPath, targetFile, marker string) error {
	removeFile(journalPath, s.logf)
	removeFile(targetFile, s.logf)
	if marker != "" {
		if err := writeFileAtomic(marker, []byte("migrated")); err != nil {
			s.logf("[toolkit] write task board migration marker: %v", err)
			return err
		}
	}
	if s.legacy.store != nil {
		// A late v1 document may have reappeared during the migration: park it
		// for the next process instead of letting a retired writer overwrite it.
		if legacyPath, _, _, _ := s.legacyPaths(); fileExists(legacyPath) {
			if err := os.Rename(legacyPath, legacyPath+".recreated"); err != nil {
				s.logf("[toolkit] preserve recreated legacy task board: %v", err)
			}
		}
		_ = s.legacy.store.Delete()
	}
	s.legacyRetired = true
	s.migrationTarget = ""
	s.legacy = memoryTaskBoard()
	delete(s.sessionTools, "")
	s.migrationBlockedKey = ""
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
		key = s.sessionKey(sessionID)
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

// RestoreTask implements contracts.TaskBoardRestorer for the aggregate service:
// the undo of Delete, scoped to the same board the deletion touched.
func (s *taskBoardService) RestoreTask(ctx context.Context, id string) (contracts.BoardTask, error) {
	board, err := s.boardForOperation(ctx)
	if err != nil {
		board = s.adapterForTool(contracts.AgentSessionFromContext(ctx))
	}
	return board.RestoreTask(ctx, id)
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

// SetTaskCloseObserver implements contracts.TaskCloseObserverSetter: the lease
// module installs the review-log writer here at activation.
//
// The observer reaches the legacy board and every cached session board, not only
// boards created later: those are exactly the ones a user can already see and
// close (2026-11-26 audit, 高: a completion that leaves no audit row is the bug).
func (s *taskBoardService) SetTaskCloseObserver(observer contracts.TaskCloseAuditor) {
	s.closeMu.Lock()
	s.closeObserver = observer
	s.closeMu.Unlock()
	s.mu.Lock()
	boards := make([]*taskBoardAdapter, 0, len(s.sessions)+1)
	if s.legacy != nil {
		boards = append(boards, s.legacy)
	}
	for _, board := range s.sessions {
		boards = append(boards, board)
	}
	s.mu.Unlock()
	for _, board := range boards {
		board.SetTaskCloseObserver(observer)
	}
}

// currentCloseObserver reads the installed sink for a board being created.
func (s *taskBoardService) currentCloseObserver() contracts.TaskCloseAuditor {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	return s.closeObserver
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
	key := s.sessionKey(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if board, ok := s.sessions[key]; ok {
		// Retire the cached store first: a tool already bound to it must not
		// persist the deleted document back to disk.
		if board.store != nil {
			_ = board.store.Delete()
		}
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
