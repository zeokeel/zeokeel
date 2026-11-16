// Package automation implements ZeoKeel's automation backend module: the
// persisted scheduled-task registry, the scheduler loop that delivers due tasks
// through the chat service into the task's bound conversation, and the optional
// rig schedule_* tools bound to this durable scheduler. Every run is recorded as
// delivered (the chat service accepted the prompt) and later settles to
// success/failed/interrupted by subscribing to the chat session state.
//
// It is a kernel backend module: it requires the product data directory, the
// host logger and the chat service, and optionally consumes the Rig toolkit
// registry to replace the toolkit's in-memory schedule tools with the durable
// adapter. Tasks are stored as JSON and every mutation is written atomically;
// the ticker goroutine is owned by the module effect and stops on unload.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigschedule "github.com/zeokeel/zeokeel-rig/tools/schedule"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/automation"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DefaultFileName is the registry file name under the product data
	// directory.
	DefaultFileName = "automations.json"
	// DefaultTick is the scheduler poll interval when Config.TickMs is zero.
	DefaultTick = 500 * time.Millisecond
	// fileMode is the registry file permission.
	fileMode = 0o600
	// dirMode is the registry directory permission.
	dirMode = 0o700
)

// ErrTaskNotFound reports an unknown automation task ID.
var ErrTaskNotFound = errors.New("automation: task not found")

// ErrClosed reports a RunNow after Close: the scheduler no longer starts runs
// (BE-07 — the old path raced the teardown WaitGroup and delivered prompts
// after Close returned).
var ErrClosed = errors.New("automation: service is closed")

// Config is the module's typed assembly config.
type Config struct {
	// Path is the registry file: absolute, or relative to the product data
	// directory (default automations.json there).
	Path string `json:"path"`
	// TickMs is the scheduler poll interval (0 = 500ms).
	TickMs int `json:"tickMs"`
}

// Module is the automation backend descriptor. It provides
// contracts.KeyAutomation.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data directory: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return fmt.Errorf("%s: require chat service: %w", ModuleID, err)
		}
		// The settings store is optional: it only records the origin of the
		// sessions the runs create, so a host without it still runs tasks (the
		// session read model then reports no provenance).
		settings, _ := ctx.Get(contracts.KeySettings)
		service, err := newService(dataDir, cfg, logger, chat, settings)
		if err != nil {
			return fmt.Errorf("%s: open automation registry: %w", ModuleID, err)
		}
		// The notifier is optional: a host without it (headless/embedded) still
		// settles runs, it only cannot reach the user outside the app.
		if notifier, ok := ctx.Get(contracts.KeyNotifier); ok && notifier != nil {
			service.notifier = notifier
		}
		if _, err := ctx.Provide(contracts.KeyAutomation, contracts.AutomationService(service)); err != nil {
			service.Close()
			return fmt.Errorf("%s: provide automation service: %w", ModuleID, err)
		}
		if registry, ok := ctx.Get(contracts.KeyToolkit); ok {
			registerScheduleTools(ctx, registry, service, logger)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(service.Close)}
		}); err != nil {
			service.Close()
			return fmt.Errorf("%s: own scheduler shutdown: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyAutomation)).
		AddRequires(
			module.Requires(contracts.KeyDataDir),
			module.Requires(contracts.KeyLogger),
			module.Requires(contracts.KeyChat),
		).
		WithName("ZeoKeel automation scheduler").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if cfg.TickMs < 0 {
		return fmt.Errorf("%s: tickMs must not be negative", ModuleID)
	}
	if strings.ContainsRune(cfg.Path, 0) {
		return fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	return nil
}

// --- persistence document --------------------------------------------------

// taskRecord is one task in the JSON registry.
type taskRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Prompt      string `json:"prompt"`
	Schedule    string `json:"schedule"`
	Model       string `json:"model,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Permission  string `json:"permission,omitempty"`
	// SessionID is the conversation subsequent runs are delivered to; the
	// first run creates it and the service persists it here.
	SessionID string `json:"sessionId,omitempty"`
	// NewSessionPerRun starts every run in a fresh conversation.
	NewSessionPerRun bool      `json:"newSessionPerRun,omitempty"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	LastRun          time.Time `json:"lastRun"`
	NextRun          time.Time `json:"nextRun"`
	LastError        string    `json:"lastError"`

	// RunHistory is the persisted recent-run list (newest first).
	RunHistory []contracts.AutomationRunRecord `json:"runHistory,omitempty"`
}

// registryDocument is the on-disk registry.
type registryDocument struct {
	Tasks []taskRecord `json:"tasks"`
}

// --- service ---------------------------------------------------------------

// service is the contracts.AutomationService implementation.
type service struct {
	mu sync.Mutex

	path   string
	logger contracts.Logger
	chat   contracts.ChatService
	// settings is optional: it records the origin of the sessions the runs
	// create, so the sidebar can hide automation-run conversations by explicit
	// provenance instead of guessing from their title (audit F6).
	settings contracts.SettingsService
	// notifier is optional: the host bridge that mirrors a settled automation
	// run outside the app (OS notification + in-app toast).
	notifier contracts.Notifier

	tasks    []contracts.AutomationTask
	inFlight map[string]bool
	nameSeq  int
	runSeq   uint64

	// pending tracks delivered runs whose chat session has not settled yet,
	// keyed by run-record ID. Settlement writes the terminal status back into
	// the task's RunHistory.
	pending map[string]*pendingRun

	// chatUnsub releases the chat-session subscription on Close.
	chatUnsub func()
	// closed stops new settlement goroutines after Close begins.
	closed bool

	subscribers    map[uint64]func()
	nextSubscriber uint64

	interval  time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	// runs tracks in-flight executions so Close can wait for their writes.
	runs sync.WaitGroup
}

var _ contracts.AutomationService = (*service)(nil)

// discardLogger drops automation log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// pendingRun is one delivered run awaiting its chat session's terminal state.
type pendingRun struct {
	taskID      string
	sessionID   string
	startedAt   time.Time
	deliveredAt time.Time
}

// runDelivery is one successful chat delivery of a task prompt.
type runDelivery struct {
	SessionID   string
	Created     bool
	Summary     string
	DeliveredAt time.Time
}

// newService loads the registry and starts the scheduler loop. settings is
// optional (nil for embedded/headless use): it only carries the run-session
// provenance.
func newService(dataDir string, cfg Config, logger contracts.Logger, chat contracts.ChatService,
	settings contracts.SettingsService) (*service, error) {
	path, err := resolveRegistryPath(dataDir, cfg.Path)
	if err != nil {
		return nil, err
	}
	tick := time.Duration(cfg.TickMs) * time.Millisecond
	if cfg.TickMs <= 0 {
		// The module Validate hook rejects negatives; a directly constructed
		// service still must not hand a non-positive interval to NewTicker.
		tick = DefaultTick
	}
	if logger == nil {
		logger = discardLogger{}
	}
	service := &service{
		path:        path,
		logger:      logger,
		chat:        chat,
		settings:    settings,
		inFlight:    map[string]bool{},
		pending:     map[string]*pendingRun{},
		subscribers: map[uint64]func(){},
		interval:    tick,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	// The automation module subscribes to chat-session changes to settle its
	// delivered runs. A host whose chat service cannot subscribe still runs
	// tasks; their records then stay "delivered" (unknown outcome).
	if unsubscribe, err := chat.Subscribe(service.onChatChange); err != nil {
		service.logger.Printf("%s: subscribe chat session states: %v", ModuleID, err)
	} else {
		service.chatUnsub = unsubscribe
	}
	service.reconcileLoadedRuns()
	go service.loop()
	service.logger.Printf("%s: initialized with %d tasks (tick %s)", ModuleID, len(service.tasks), tick)
	return service, nil
}

// resolveRegistryPath resolves Config.Path against the product data directory.
func resolveRegistryPath(dataDir, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = DefaultFileName
	}
	if strings.ContainsRune(requested, 0) {
		return "", fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	if filepath.IsAbs(requested) {
		return filepath.Clean(requested), nil
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("%s: data directory is empty", ModuleID)
	}
	return filepath.Join(dataDir, filepath.FromSlash(requested)), nil
}

// load reads the registry, treating a missing file as empty. An unreadable or
// malformed registry is quarantined (FR-06): the bad file is renamed to
// <path>.corrupt-<ts>, a warning is logged and the scheduler starts with no
// tasks instead of failing activation.
func (s *service) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		// BE-09 (2026-11 audit): a non-NotExist READ error quarantines exactly
		// like a decode error — an empty start with the entry left in place
		// would let the next persist overwrite it and lose the original tasks.
		s.quarantine(fmt.Sprintf("automation registry %q could not be read (%v)", s.path, err))
		return nil
	}
	var document registryDocument
	if err := json.Unmarshal(data, &document); err != nil {
		s.quarantine(fmt.Sprintf("automation registry %q is corrupt (%v)", s.path, err))
		return nil
	}
	tasks := make([]contracts.AutomationTask, 0, len(document.Tasks))
	for _, record := range document.Tasks {
		if strings.TrimSpace(record.ID) == "" {
			continue
		}
		tasks = append(tasks, contracts.AutomationTask{
			ID:               record.ID,
			Name:             record.Name,
			Prompt:           record.Prompt,
			Schedule:         record.Schedule,
			Enabled:          record.Enabled,
			Model:            strings.TrimSpace(record.Model),
			WorkspaceID:      strings.TrimSpace(record.WorkspaceID),
			Permission:       strings.TrimSpace(record.Permission),
			SessionID:        strings.TrimSpace(record.SessionID),
			NewSessionPerRun: record.NewSessionPerRun,
			CreatedAt:        record.CreatedAt.UTC(),
			UpdatedAt:        record.UpdatedAt.UTC(),
			LastRun:          record.LastRun.UTC(),
			NextRun:          record.NextRun.UTC(),
			LastError:        record.LastError,
			RunHistory:       append([]contracts.AutomationRunRecord(nil), record.RunHistory...),
		})
	}
	s.tasks = tasks
	return nil
}

// quarantine renames the unusable registry out of the way, logs a warning and
// lets the scheduler start empty.
func (s *service) quarantine(reason string) {
	backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UTC().UnixNano())
	message := reason + "; starting with no tasks"
	if err := os.Rename(s.path, backup); err != nil {
		message += fmt.Sprintf(" (could not quarantine the file: %v)", err)
	} else {
		message += fmt.Sprintf(" (quarantined as %s)", backup)
	}
	s.logRecovery(message)
}

// logRecovery records a recovery warning through the host logger.
func (s *service) logRecovery(message string) {
	if s.logger != nil {
		s.logger.Printf("%s: %s", ModuleID, message)
	}
}

// persistLocked writes the registry atomically (temporary file + Sync +
// rename). The caller holds s.mu.
func (s *service) persistLocked() error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, dirMode); err != nil {
		return fmt.Errorf("%s: create registry directory %q: %w", ModuleID, directory, err)
	}
	document := registryDocument{Tasks: make([]taskRecord, 0, len(s.tasks))}
	for _, task := range s.tasks {
		document.Tasks = append(document.Tasks, taskRecord{
			ID:               task.ID,
			Name:             task.Name,
			Prompt:           task.Prompt,
			Schedule:         task.Schedule,
			Enabled:          task.Enabled,
			Model:            task.Model,
			WorkspaceID:      task.WorkspaceID,
			Permission:       task.Permission,
			SessionID:        task.SessionID,
			NewSessionPerRun: task.NewSessionPerRun,
			CreatedAt:        task.CreatedAt.UTC(),
			UpdatedAt:        task.UpdatedAt.UTC(),
			LastRun:          task.LastRun.UTC(),
			NextRun:          task.NextRun.UTC(),
			LastError:        task.LastError,
			RunHistory:       append([]contracts.AutomationRunRecord(nil), task.RunHistory...),
		})
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: encode registry: %w", ModuleID, err)
	}
	temporary, err := os.CreateTemp(directory, ".automations-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: create temporary registry: %w", ModuleID, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(fileMode); err != nil {
		cleanup()
		return fmt.Errorf("%s: chmod temporary registry: %w", ModuleID, err)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("%s: write temporary registry: %w", ModuleID, err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%s: sync temporary registry: %w", ModuleID, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("%s: close temporary registry: %w", ModuleID, err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("%s: replace registry %q: %w", ModuleID, s.path, err)
	}
	return nil
}

// Close stops the scheduler loop, releases the chat subscription, waits for the
// loop to exit and then waits for any in-flight executions and run settlements
// to finish their registry writes. It is idempotent.
func (s *service) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		unsubscribe := s.chatUnsub
		s.chatUnsub = nil
		s.mu.Unlock()
		if unsubscribe != nil {
			unsubscribe()
		}
		close(s.stop)
		<-s.done
		s.runs.Wait()
		s.logger.Printf("%s: scheduler stopped", ModuleID)
	})
}

// loop is the scheduler ticker goroutine.
func (s *service) loop() {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.tick(now.UTC())
		}
	}
}

// tick launches every enabled task whose NextRun is due. Execution happens on
// goroutines; the in-flight guard prevents overlapping copies of one task.
// Delivered runs whose session ended are settled first (outside the lock and on
// a tracked goroutine: the ticker never waits for a run).
func (s *service) tick(now time.Time) {
	s.reconcilePending()
	s.mu.Lock()
	due := make([]string, 0, len(s.tasks))
	for _, task := range s.tasks {
		if !task.Enabled || task.NextRun.IsZero() || task.NextRun.After(now) || s.inFlight[task.ID] {
			continue
		}
		due = append(due, task.ID)
	}
	s.mu.Unlock()
	for _, id := range due {
		s.startRun(id, false)
	}
}

// prepareSchedule canonicalizes a draft schedule and computes its next firing.
//
// Disabled tasks keep an unparseable draft as typed (the UI shows the raw value
// with a Fix action); enabled tasks must parse and have an upcoming occurrence.
// A once-at schedule must also be in the future (DSH rejects past absolute
// targets at creation).
func prepareSchedule(spec string, enabled bool, now time.Time) (string, time.Time, error) {
	canonical := strings.TrimSpace(spec)
	if canonical == "" {
		if !enabled {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, fmt.Errorf("%s: schedule is required for an enabled task", ModuleID)
	}
	parsed, err := Parse(canonical)
	if err != nil {
		if !enabled {
			return canonical, time.Time{}, nil
		}
		return "", time.Time{}, err
	}
	canonical = parsed.String()
	if !enabled {
		return canonical, time.Time{}, nil
	}
	next := parsed.Next(now)
	if next.IsZero() {
		return "", time.Time{}, fmt.Errorf("%s: schedule %q has no upcoming occurrence", ModuleID, canonical)
	}
	if parsed.Kind() == KindAt && !next.After(now) {
		return "", time.Time{}, fmt.Errorf("%s: schedule %q is in the past", ModuleID, canonical)
	}
	return canonical, next, nil
}

// --- CRUD ------------------------------------------------------------------

// List implements contracts.AutomationService, ordered by creation time.
func (s *service) List(ctx context.Context) ([]contracts.AutomationTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.AutomationTask, len(s.tasks))
	for i, task := range s.tasks {
		out[i] = copyTask(task)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Create implements contracts.AutomationService.
func (s *service) Create(ctx context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	if err := ctx.Err(); err != nil {
		return contracts.AutomationTask{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()

	id := strings.TrimSpace(task.ID)
	if id == "" {
		id = s.newTaskIDLocked()
	} else if s.indexLocked(id) >= 0 {
		s.mu.Unlock()
		return contracts.AutomationTask{}, fmt.Errorf("%s: task %q already exists", ModuleID, id)
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		s.nameSeq++
		name = fmt.Sprintf("自动化 %d", s.nameSeq)
	}
	schedule, nextRun, err := prepareSchedule(task.Schedule, task.Enabled, now)
	if err != nil {
		s.mu.Unlock()
		return contracts.AutomationTask{}, err
	}
	created := contracts.AutomationTask{
		ID:               id,
		Name:             name,
		Prompt:           task.Prompt,
		Schedule:         schedule,
		Enabled:          task.Enabled,
		Model:            strings.TrimSpace(task.Model),
		WorkspaceID:      strings.TrimSpace(task.WorkspaceID),
		Permission:       strings.TrimSpace(task.Permission),
		SessionID:        strings.TrimSpace(task.SessionID),
		NewSessionPerRun: task.NewSessionPerRun,
		CreatedAt:        now,
		UpdatedAt:        now,
		NextRun:          nextRun,
	}
	previous := append([]contracts.AutomationTask(nil), s.tasks...)
	s.tasks = append(s.tasks, created)
	if err := s.persistLocked(); err != nil {
		s.tasks = previous
		s.mu.Unlock()
		return contracts.AutomationTask{}, err
	}
	s.mu.Unlock()
	s.notify()
	return copyTask(created), nil
}

// Update implements contracts.AutomationService.
func (s *service) Update(ctx context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	if err := ctx.Err(); err != nil {
		return contracts.AutomationTask{}, err
	}
	id := strings.TrimSpace(task.ID)
	if id == "" {
		return contracts.AutomationTask{}, fmt.Errorf("%s: task id is required", ModuleID)
	}
	now := time.Now().UTC()
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return contracts.AutomationTask{}, fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	}
	existing := s.tasks[index]
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = existing.Name
	}
	schedule, nextRun, err := prepareSchedule(task.Schedule, task.Enabled, now)
	if err != nil {
		s.mu.Unlock()
		return contracts.AutomationTask{}, err
	}
	updated := existing
	updated.Name = name
	updated.Prompt = task.Prompt
	updated.Schedule = schedule
	updated.Model = strings.TrimSpace(task.Model)
	updated.WorkspaceID = strings.TrimSpace(task.WorkspaceID)
	updated.Permission = strings.TrimSpace(task.Permission)
	if sessionID := strings.TrimSpace(task.SessionID); sessionID != "" {
		// Callers that do not manage the binding (the Rig tools) leave it
		// empty; keep whatever conversation the task is already bound to.
		updated.SessionID = sessionID
	}
	updated.NewSessionPerRun = task.NewSessionPerRun
	updated.Enabled = task.Enabled
	updated.NextRun = nextRun
	updated.UpdatedAt = now
	if existing.Schedule != schedule || existing.Enabled != task.Enabled {
		updated.LastError = ""
	}
	previous := append([]contracts.AutomationTask(nil), s.tasks...)
	s.tasks[index] = updated
	if err := s.persistLocked(); err != nil {
		s.tasks = previous
		s.mu.Unlock()
		return contracts.AutomationTask{}, err
	}
	s.mu.Unlock()
	s.notify()
	return copyTask(updated), nil
}

// Delete implements contracts.AutomationService; unknown IDs are a no-op.
func (s *service) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return nil
	}
	previous := append([]contracts.AutomationTask(nil), s.tasks...)
	s.tasks = append(s.tasks[:index], s.tasks[index+1:]...)
	if err := s.persistLocked(); err != nil {
		s.tasks = previous
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

// RunNow implements contracts.AutomationService: it starts the task execution
// immediately (asynchronously, like a scheduled run) and returns unknown-task
// and context errors to the caller. Execution failures land in LastError.
// After Close it returns ErrClosed instead of racing the teardown WaitGroup
// (BE-07).
func (s *service) RunNow(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.indexLocked(id) < 0 {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	}
	s.mu.Unlock()
	if !s.startRun(id, true) {
		return ErrClosed
	}
	return nil
}

// startRun launches one execution and accounts for it so Close waits for the
// registry writes of in-flight runs. The Add happens UNDER s.mu with a closed
// check (BE-07, 2026-11 audit): Close sets closed under the same lock before
// runs.Wait, so an Add can never race a zero-counter Wait — the old
// unconditional Add panicked ("WaitGroup reused before previous Wait has
// returned") and, because the panic lands after the atomic increment, left a
// permanent +1 that hung Close forever. Returns false when the service is
// closed (no goroutine started).
func (s *service) startRun(id string, manual bool) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.runs.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.runs.Done()
		s.execute(id, manual)
	}()
	return true
}

// Subscribe implements contracts.AutomationService.
func (s *service) Subscribe(fn func()) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	s.mu.Lock()
	s.nextSubscriber++
	id := s.nextSubscriber
	s.subscribers[id] = fn
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subscribers, id)
			s.mu.Unlock()
		})
	}, nil
}

// notify calls every subscriber outside the service lock, containing panics.
func (s *service) notify() {
	s.mu.Lock()
	listeners := make([]func(), 0, len(s.subscribers))
	for _, listener := range s.subscribers {
		listeners = append(listeners, listener)
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logger.Printf("%s: subscriber panicked: %v", ModuleID, recovered)
				}
			}()
			listener()
		}()
	}
}

func (s *service) indexLocked(id string) int {
	for index, task := range s.tasks {
		if task.ID == id {
			return index
		}
	}
	return -1
}

func (s *service) newTaskIDLocked() string {
	for {
		id := fmt.Sprintf("auto-%d", time.Now().UnixNano())
		if s.indexLocked(id) < 0 {
			return id
		}
	}
}

// --- execution -------------------------------------------------------------

// execute delivers one task run through the chat service and updates its
// scheduling state. force marks a RunNow execution (it runs even for disabled
// tasks). Delivery happens on the caller's goroutine; completion is settled
// later from the chat subscription (never from the ticker).
func (s *service) execute(taskID string, force bool) {
	task, ok := s.claimRun(taskID, force)
	if !ok {
		return
	}
	defer s.releaseRun(taskID)

	// A trigger while the previous delivered run of this task is still running
	// is folded (existing in-flight behavior). When that run already ended but
	// its notification was missed, settle it first and deliver this trigger.
	if s.hasPendingTask(taskID) {
		s.settleTask(taskID)
		if s.hasPendingTask(taskID) {
			s.logger.Printf("%s: task %s still has a run in flight; folding this trigger", ModuleID, taskID)
			return
		}
	}

	startedAt := time.Now().UTC()
	delivery, deliveryErr := s.runTask(task)
	recordStatus, recordError, settled := "", "", false
	if deliveryErr == nil {
		recordStatus, recordError, settled = s.runOutcome(delivery.SessionID)
	}
	now := time.Now().UTC()
	notifySettled := false

	s.mu.Lock()
	recordID := s.newRunRecordIDLocked()
	if index := s.indexLocked(taskID); index >= 0 {
		current := s.tasks[index]
		current.LastRun = now
		current.UpdatedAt = now
		if delivery.Created && !task.NewSessionPerRun {
			// The first run remembers the conversation subsequent runs use.
			current.SessionID = delivery.SessionID
		}
		record := contracts.AutomationRunRecord{
			ID:        recordID,
			StartedAt: startedAt,
			SessionID: delivery.SessionID,
			Summary:   truncateRunSummary(delivery.Summary),
		}
		switch {
		case deliveryErr != nil:
			record.Status = contracts.AutomationRunFailure
			record.CompletedAt = now
			record.Error = truncateRunSummary(deliveryErr.Error())
			record.Summary = record.Error
			record.DurationMS = now.Sub(startedAt).Milliseconds()
			current.LastError = record.Error
		case settled:
			record.DeliveredAt = delivery.DeliveredAt
			record.Status = recordStatus
			record.Error = truncateRunSummary(recordError)
			record.CompletedAt = now
			record.DurationMS = runDuration(now, delivery.DeliveredAt, startedAt)
			notifySettled = true
			if recordStatus == contracts.AutomationRunFailure {
				current.LastError = record.Error
			} else {
				current.LastError = ""
			}
		default:
			record.DeliveredAt = delivery.DeliveredAt
			record.Status = contracts.AutomationRunDelivered
			current.LastError = ""
			s.pending[recordID] = &pendingRun{
				taskID: taskID, sessionID: delivery.SessionID,
				startedAt: startedAt, deliveredAt: delivery.DeliveredAt,
			}
		}
		current.RunHistory = append([]contracts.AutomationRunRecord{record}, current.RunHistory...)
		if len(current.RunHistory) > maxRunHistory {
			current.RunHistory = current.RunHistory[:maxRunHistory]
		}
		current.NextRun, current.Enabled = s.rescheduleLocked(current, now)
		s.tasks[index] = current
	}
	if err := s.persistLocked(); err != nil {
		s.logger.Printf("%s: persist run of %s: %v", ModuleID, taskID, err)
	}
	s.mu.Unlock()
	s.notify()
	if notifySettled {
		s.notifyRunSettled(task.Name, delivery.SessionID, recordStatus, recordError)
	}
	if deliveryErr == nil && !settled {
		// The session may have settled between the read above and the pending
		// registration; its notification then found nothing to settle.
		s.settleRecord(recordID)
	}
}

// copyTask returns a task whose nested slices are independent of the store, so
// a caller reading a List/Update/claim snapshot cannot race the background
// settlement that rewrites RunHistory in place.
func copyTask(task contracts.AutomationTask) contracts.AutomationTask {
	task.RunHistory = append([]contracts.AutomationRunRecord(nil), task.RunHistory...)
	return task
}

// claimRun takes the in-flight guard for one execution and snapshots the task.
// No closed check here: an execution ACCEPTED before Close (startRun's guarded
// Add) must still run and flush its record — Close's runs.Wait waits for it
// (the regression contract "Close waits for in-flight RunNow"). Post-close
// dispatch is prevented at the entry point instead: startRun refuses to Add
// after closed, and Close only returns after every accepted run settled
// (BE-07, 2026-11 audit).
func (s *service) claimRun(taskID string, force bool) (contracts.AutomationTask, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.indexLocked(taskID)
	if index < 0 || s.inFlight[taskID] {
		return contracts.AutomationTask{}, false
	}
	task := s.tasks[index]
	if !force && !task.Enabled {
		return contracts.AutomationTask{}, false
	}
	s.inFlight[taskID] = true
	return copyTask(task), true
}

// releaseRun clears the in-flight guard of one execution.
func (s *service) releaseRun(taskID string) {
	s.mu.Lock()
	delete(s.inFlight, taskID)
	s.mu.Unlock()
}

// newRunRecordIDLocked allocates a stable record id for in-flight settlement.
func (s *service) newRunRecordIDLocked() string {
	s.runSeq++
	return fmt.Sprintf("run-%d-%d", time.Now().UTC().UnixNano(), s.runSeq)
}

// runDuration returns the delivery-to-completion duration in milliseconds, or
// the attempt duration when no delivery instant was recorded.
func runDuration(completedAt, deliveredAt, startedAt time.Time) int64 {
	if !deliveredAt.IsZero() {
		return completedAt.Sub(deliveredAt).Milliseconds()
	}
	if !startedAt.IsZero() {
		return completedAt.Sub(startedAt).Milliseconds()
	}
	return 0
}

// runTask delivers one run: it reuses the task's bound conversation (creating
// and remembering one on the first run) or starts a fresh conversation when the
// task asks for it, applies the task's workspace/permission/model overrides, and
// sends the prompt. Persisting a remembered session id is the caller's job.
func (s *service) runTask(task contracts.AutomationTask) (runDelivery, error) {
	sessionID := strings.TrimSpace(task.SessionID)
	created := false
	if task.NewSessionPerRun || sessionID == "" || !s.chatSessionExists(sessionID) {
		newSessionID, err := s.chat.NewSession("自动化: " + task.Name)
		if err != nil {
			s.logger.Printf("%s: run %s: create chat session: %v", ModuleID, task.ID, err)
			return runDelivery{}, fmt.Errorf("create chat session: %w", err)
		}
		sessionID = newSessionID
		created = true
		// Record the run session's provenance right after creation, before
		// anything can fail: the sidebar hides the session by this explicit
		// origin, not by its title (audit F6). A failed write only costs the
		// filter.
		s.recordSessionOrigin(task.ID, sessionID)
	}
	if workspaceID := strings.TrimSpace(task.WorkspaceID); workspaceID != "" {
		if switcher, ok := s.chat.(contracts.SessionWorkspaceSwitcher); ok {
			if err := switcher.SetSessionWorkspace(sessionID, workspaceID); err != nil {
				// The workspace binding is best effort: warn and still run.
				s.logger.Printf("%s: run %s: set session workspace %q: %v", ModuleID, task.ID, workspaceID, err)
			}
		} else {
			s.logger.Printf("%s: run %s: chat service lacks session workspace switching; using the current workspace", ModuleID, task.ID)
		}
	}
	if permission := strings.TrimSpace(task.Permission); permission != "" {
		if switcher, ok := s.chat.(contracts.SessionPermissionSwitcher); ok {
			if err := switcher.SetSessionPermission(sessionID, permission); err != nil {
				s.logger.Printf("%s: run %s: set session permission %q: %v", ModuleID, task.ID, permission, err)
			}
		} else {
			s.logger.Printf("%s: run %s: chat service lacks session permission switching; using the global permission", ModuleID, task.ID)
		}
	}
	if model := strings.TrimSpace(task.Model); model != "" {
		if switcher, ok := s.chat.(contracts.SessionModelSwitcher); ok {
			if err := switcher.SetSessionModel(sessionID, model); err != nil {
				// The model override is best effort: warn and still run the task
				// with the default model.
				s.logger.Printf("%s: run %s: set session model %q: %v", ModuleID, task.ID, model, err)
			}
		} else {
			s.logger.Printf("%s: run %s: chat service lacks per-session model switching; using the default model", ModuleID, task.ID)
		}
	}
	if err := s.chat.Send(sessionID, task.Prompt); err != nil {
		s.logger.Printf("%s: run %s: send prompt: %v", ModuleID, task.ID, err)
		return runDelivery{SessionID: sessionID, Created: created}, fmt.Errorf("send prompt: %w", err)
	}
	s.logger.Printf("%s: ran task %s in chat session %s", ModuleID, task.ID, sessionID)
	return runDelivery{
		SessionID:   sessionID,
		Created:     created,
		Summary:     fmt.Sprintf("ran in chat session %s", sessionID),
		DeliveredAt: time.Now().UTC(),
	}, nil
}

// chatSessionExists reports whether the chat service still knows a session.
func (s *service) chatSessionExists(sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	_, ok := s.chat.Session(sessionID)
	return ok
}

// --- run settlement --------------------------------------------------------

// onChatChange is the ChatService subscription callback: when a session changed,
// settle any automation run delivered to it. It only dispatches a tracked
// goroutine, so the agent's notification goroutine is never blocked by a
// registry write.
func (s *service) onChatChange(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	recordIDs := make([]string, 0, 1)
	for recordID, run := range s.pending {
		if run.sessionID == sessionID {
			recordIDs = append(recordIDs, recordID)
		}
	}
	s.mu.Unlock()
	if len(recordIDs) == 0 {
		return
	}
	// The agent notifies on every streaming event; the cheap session list probe
	// keeps a long run from copying the whole read model on each one.
	if running, exists := s.chatSessionRunning(sessionID); exists && running {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.runs.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.runs.Done()
		for _, recordID := range recordIDs {
			s.settleRecord(recordID)
		}
	}()
}

// chatSessionRunning probes the chat read model for one session's run state
// without materializing its message list.
func (s *service) chatSessionRunning(sessionID string) (running, exists bool) {
	for _, info := range s.chat.Sessions() {
		if info.ID == sessionID {
			return info.Running, true
		}
	}
	return false, false
}

// reconcilePending settles every delivered run that can be settled now. The
// ticker calls it so a missed terminal notification can never stall a task; the
// work runs on a tracked goroutine so the ticker never waits for a run.
func (s *service) reconcilePending() {
	s.mu.Lock()
	if s.closed || len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	candidates := make([]string, 0, len(s.pending))
	for recordID := range s.pending {
		candidates = append(candidates, recordID)
	}
	s.mu.Unlock()
	recordIDs := make([]string, 0, len(candidates))
	for _, recordID := range candidates {
		s.mu.Lock()
		run := s.pending[recordID]
		s.mu.Unlock()
		if run == nil {
			continue
		}
		if running, exists := s.chatSessionRunning(run.sessionID); exists && running {
			continue
		}
		recordIDs = append(recordIDs, recordID)
	}
	if len(recordIDs) == 0 {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.runs.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.runs.Done()
		for _, recordID := range recordIDs {
			s.settleRecord(recordID)
		}
	}()
}

// reconcileLoadedRuns settles runs that were delivered but never completed
// before the previous shutdown: the in-memory pending set is gone after a
// restart, so the persisted "delivered" records are the only trace. Records
// whose session is still running (or unknown) stay delivered (outcome unknown).
func (s *service) reconcileLoadedRuns() {
	s.mu.Lock()
	recordIDs := make([]string, 0)
	for _, task := range s.tasks {
		for _, record := range task.RunHistory {
			if record.Status != contracts.AutomationRunDelivered || record.ID == "" {
				continue
			}
			sessionID := strings.TrimSpace(record.SessionID)
			if sessionID == "" {
				sessionID = contracts.AutomationRunSessionID(record.Summary)
			}
			if sessionID == "" {
				continue
			}
			s.pending[record.ID] = &pendingRun{
				taskID: task.ID, sessionID: sessionID,
				startedAt: record.StartedAt, deliveredAt: record.DeliveredAt,
			}
			recordIDs = append(recordIDs, record.ID)
		}
	}
	s.mu.Unlock()
	for _, recordID := range recordIDs {
		s.settleRecord(recordID)
	}
}

// hasPendingTask reports whether the task has a delivered run awaiting its
// session's terminal state.
func (s *service) hasPendingTask(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.pending {
		if run.taskID == taskID {
			return true
		}
	}
	return false
}

// settleTask attempts to settle every pending run of one task.
func (s *service) settleTask(taskID string) {
	s.mu.Lock()
	recordIDs := make([]string, 0, 1)
	for recordID, run := range s.pending {
		if run.taskID == taskID {
			recordIDs = append(recordIDs, recordID)
		}
	}
	s.mu.Unlock()
	for _, recordID := range recordIDs {
		s.settleRecord(recordID)
	}
}

// runOutcome maps a chat session's read model onto a terminal run status,
// reporting settled=false while the session is still running.
func (s *service) runOutcome(sessionID string) (status, errText string, settled bool) {
	state, exists := s.chat.Session(sessionID)
	if !exists {
		return contracts.AutomationRunInterrupted, "the chat session is no longer available", true
	}
	if state.Running {
		return "", "", false
	}
	var lastAssistant *contracts.ChatMessage
	for index := len(state.Messages) - 1; index >= 0; index-- {
		if state.Messages[index].Role == contracts.ChatRoleAssistant {
			lastAssistant = &state.Messages[index]
			break
		}
	}
	if lastAssistant != nil {
		if lastAssistant.Streaming {
			return contracts.AutomationRunInterrupted, "the run was interrupted", true
		}
		if messageError := strings.TrimSpace(lastAssistant.Error); messageError != "" {
			if lastAssistant.ErrorKind == contracts.MessageErrorKindCancelled {
				return contracts.AutomationRunInterrupted, messageError, true
			}
			return contracts.AutomationRunFailure, messageError, true
		}
	}
	if sessionError := strings.TrimSpace(state.Error); sessionError != "" {
		return contracts.AutomationRunFailure, sessionError, true
	}
	return contracts.AutomationRunSuccess, "", true
}

// settleRecord writes one delivered run's terminal state back into its task's
// run history. It is idempotent: an already-settled record is left alone.
func (s *service) settleRecord(recordID string) {
	if recordID == "" {
		return
	}
	s.mu.Lock()
	run := s.pending[recordID]
	s.mu.Unlock()
	if run == nil {
		return
	}
	status, errText, settled := s.runOutcome(run.sessionID)
	if !settled {
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	if s.pending[recordID] == nil {
		s.mu.Unlock()
		return
	}
	delete(s.pending, recordID)
	taskName := run.taskID
	settledNow := false
	if index := s.indexLocked(run.taskID); index >= 0 {
		task := &s.tasks[index]
		taskName = task.Name
		for historyIndex := range task.RunHistory {
			record := &task.RunHistory[historyIndex]
			if record.ID != recordID || record.Status != contracts.AutomationRunDelivered {
				continue
			}
			record.Status = status
			record.Error = truncateRunSummary(errText)
			record.CompletedAt = now
			record.DurationMS = runDuration(now, record.DeliveredAt, record.StartedAt)
			settledNow = true
			if historyIndex == 0 {
				if status == contracts.AutomationRunFailure {
					task.LastError = record.Error
				} else {
					task.LastError = ""
				}
			}
			task.UpdatedAt = now
			break
		}
	}
	if err := s.persistLocked(); err != nil {
		s.logger.Printf("%s: persist settled run %s: %v", ModuleID, recordID, err)
	}
	s.mu.Unlock()
	s.notify()
	if settledNow {
		s.notifyRunSettled(taskName, run.sessionID, status, errText)
	}
}

// notifyRunSettled reports one settled automation run to the optional host
// notifier (a no-op without one). A delivered run never notifies: the prompt was
// accepted but nothing finished. Interrupted runs stay silent too: the user
// stopped them and the shared copy is success/failure only, so a neutral
// interruption must not be reported as either.
//
// The run's conversation travels as the notification's session tag (not as
// visible copy): a click on the OS notification opens that session, and the raw
// session id never leaks into the toast/notification text.
func (s *service) notifyRunSettled(taskName, sessionID, status, errText string) {
	if s.notifier == nil {
		return
	}
	if status != contracts.AutomationRunSuccess && status != contracts.AutomationRunFailure {
		return
	}
	name := strings.TrimSpace(taskName)
	if name == "" {
		name = "自动化任务"
	}
	failed := status == contracts.AutomationRunFailure
	text := "自动化「" + name + "」已完成"
	if failed {
		text = "自动化「" + name + "」失败"
		if cause := truncateRunSummary(strings.TrimSpace(errText)); cause != "" {
			text += "：" + cause
		}
	}
	contracts.NotifyRunFinishedForSession(s.notifier, text, failed, strings.TrimSpace(sessionID))
}

// recordSessionOrigin persists the origin of one run session (best effort): the
// settings store is the session-provenance transport shared with the chat read
// model and the sidebar, so no session-store schema change is needed. A host
// without a settings service, or a failing write, only costs the sidebar's
// filter; the run itself must never be blocked by it.
func (s *service) recordSessionOrigin(taskID, sessionID string) {
	if s.settings == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if err := s.settings.Set(contracts.SessionOriginSettingKey(sessionID), contracts.SessionOriginAutomation); err != nil {
		s.logger.Printf("%s: run %s: record session origin of %s: %v", ModuleID, taskID, sessionID, err)
	}
}

// maxRunHistory bounds the per-task run history.
const maxRunHistory = 10

// truncateRunSummary bounds one history summary to 200 runes.
func truncateRunSummary(summary string) string {
	const limit = 200
	runes := []rune(summary)
	if len(runes) <= limit {
		return summary
	}
	return string(runes[:limit-1]) + "…"
}

// rescheduleLocked computes the next firing after one run: one-shot tasks
// disable themselves, other enabled tasks get the next schedule instant.
func (s *service) rescheduleLocked(task contracts.AutomationTask, from time.Time) (time.Time, bool) {
	if !task.Enabled {
		return time.Time{}, false
	}
	schedule, err := Parse(task.Schedule)
	if err != nil {
		s.logger.Printf("%s: task %s has an invalid schedule %q: %v", ModuleID, task.ID, task.Schedule, err)
		return time.Time{}, false
	}
	if schedule.Kind() == KindAfter || schedule.Kind() == KindAt {
		return time.Time{}, false
	}
	next := schedule.Next(from)
	if next.IsZero() {
		// A repeating rule with no future occurrence (an impossible calendar
		// combination) ends the task instead of leaving it enabled forever.
		return time.Time{}, false
	}
	return next, true
}

// --- optional Rig schedule tools -------------------------------------------

// schedulerAdapter implements rig.Scheduler on top of the automation service.
type schedulerAdapter struct {
	service *service
}

var _ rig.Scheduler = (*schedulerAdapter)(nil)

// List implements rig.Scheduler.
func (a *schedulerAdapter) List(ctx context.Context) ([]rig.ScheduledTask, error) {
	tasks, err := a.service.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]rig.ScheduledTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, rig.ScheduledTask{
			ID:       task.ID,
			Name:     task.Name,
			Schedule: task.Schedule,
			Prompt:   task.Prompt,
			Enabled:  task.Enabled,
			NextRun:  task.NextRun,
			Created:  task.CreatedAt,
		})
	}
	return out, nil
}

// Create implements rig.Scheduler.
func (a *schedulerAdapter) Create(ctx context.Context, task rig.ScheduledTask) (rig.ScheduledTask, error) {
	created, err := a.service.Create(ctx, contracts.AutomationTask{
		ID:       task.ID,
		Name:     task.Name,
		Prompt:   task.Prompt,
		Schedule: task.Schedule,
		Enabled:  task.Enabled,
	})
	if err != nil {
		return rig.ScheduledTask{}, err
	}
	return a.mapTask(created), nil
}

// Update implements rig.Scheduler.
func (a *schedulerAdapter) Update(ctx context.Context, task rig.ScheduledTask) (rig.ScheduledTask, error) {
	updated, err := a.service.Update(ctx, contracts.AutomationTask{
		ID:       task.ID,
		Name:     task.Name,
		Prompt:   task.Prompt,
		Schedule: task.Schedule,
		Enabled:  task.Enabled,
	})
	if err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return rig.ScheduledTask{}, fmt.Errorf("%w: %v", rigschedule.ErrTaskNotFound, err)
		}
		return rig.ScheduledTask{}, err
	}
	return a.mapTask(updated), nil
}

// Delete implements rig.Scheduler. The automation contract treats unknown
// deletes as a no-op; the Rig tool wants a not-found error, so existence is
// checked first.
func (a *schedulerAdapter) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	tasks, err := a.service.List(ctx)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.ID == id {
			return a.service.Delete(ctx, id)
		}
	}
	return fmt.Errorf("%w: %q", rigschedule.ErrTaskNotFound, id)
}

func (a *schedulerAdapter) mapTask(task contracts.AutomationTask) rig.ScheduledTask {
	return rig.ScheduledTask{
		ID:       task.ID,
		Name:     task.Name,
		Schedule: task.Schedule,
		Prompt:   task.Prompt,
		Enabled:  task.Enabled,
		NextRun:  task.NextRun,
		Created:  task.CreatedAt,
	}
}

// registerScheduleTools replaces the toolkit's in-memory schedule_* tools with
// the durable adapter. The toolkit module still registers schedule.Tools(nil)
// for its own tests, so a duplicate name is unregistered and retried; the same
// names are unregistered when the module unloads.
func registerScheduleTools(ctx cordis.Context, registry *rigtools.Registry, service *service, logger contracts.Logger) {
	adapter := &schedulerAdapter{service: service}
	for _, tool := range rigschedule.Tools(adapter) {
		name := tool.Name()
		registerErr := registry.Register(tool)
		if registerErr != nil && strings.Contains(registerErr.Error(), "duplicate tool name") {
			registry.Unregister(name)
			registerErr = registry.Register(tool)
		}
		if registerErr != nil {
			logger.Printf("%s: register tool %q: %v", ModuleID, name, registerErr)
			continue
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				registry.Unregister(name)
			})}
		}); err != nil {
			registry.Unregister(name)
			logger.Printf("%s: own tool %q cleanup: %v", ModuleID, name, err)
		}
	}
}
