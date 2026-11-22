// Package automation implements ZeoKeel's automation backend module: the
// persisted scheduled-task registry, the scheduler loop that delivers due tasks
// through the chat service into the task's bound conversation, and the optional
// rig schedule_* tools bound to this durable scheduler. Every run is recorded as
// delivered (the chat service accepted the prompt) and later settles to
// success/failed/interrupted by reading the chat session state.
//
// It is a kernel backend module: it requires the product data directory, the
// host logger and the chat service, and optionally consumes the Rig toolkit
// registry to replace the toolkit's in-memory schedule tools with the durable
// adapter.
//
// The durable registry and the claim/deliver/settle state machine live in the
// host-agnostic github.com/zeokeel/zeokeel-rig/session/automation package. This
// module supplies the host half: the schedule parser (product contracts), chat
// delivery, model/workspace/permission overrides, run notifications, session
// provenance and the contracts.AutomationService read model. Product-only task
// fields are carried through the registry via the SDK's opaque task host fields,
// so an existing automations.json keeps its model/workspace/session/permission
// values and remains readable.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigautomation "github.com/zeokeel/zeokeel-rig/session/automation"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigschedule "github.com/zeokeel/zeokeel-rig/tools/schedule"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
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

// ErrClosed reports a RunNow after Close: the scheduler no longer starts runs.
var ErrClosed = errors.New("automation: service is closed")

// ErrSchedulerDisabled reports that this instance does not own the automation
// registry lock, so scheduled and manual runs are disabled here while another
// instance delivers them. CRUD and the read model keep working.
var ErrSchedulerDisabled = errors.New("automation: scheduler is disabled (the registry is owned by another instance)")

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
		// sessions the runs create, so a host without it still runs tasks.
		settings, _ := ctx.Get(contracts.KeySettings)
		service, err := newService(dataDir, cfg, logger, chat, settings)
		if err != nil {
			return fmt.Errorf("%s: open automation registry: %w", ModuleID, err)
		}
		// The notifier is optional: a host without it still settles runs, it
		// only cannot reach the user outside the app.
		if notifier, ok := ctx.Get(contracts.KeyNotifier); ok && notifier != nil {
			service.notifier = notifier
		}
		if _, err := ctx.Provide(contracts.KeyAutomation, contracts.AutomationService(service)); err != nil {
			service.Close()
			return fmt.Errorf("%s: provide automation service: %w", ModuleID, err)
		}
		if registry, ok := ctx.Get(hosttools.KeyRegistry); ok {
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

// --- service ---------------------------------------------------------------

// discardLogger drops automation log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// runDelivery is one successful chat delivery of a task prompt.
type runDelivery struct {
	SessionID   string
	Created     bool
	Summary     string
	DeliveredAt time.Time
}

// service is the contracts.AutomationService implementation.
type service struct {
	mu sync.Mutex

	path     string
	logger   contracts.Logger
	chat     contracts.ChatService
	settings contracts.SettingsService
	notifier contracts.Notifier

	// store is the durable registry; scheduler owns claim/deliver/settle.
	store     *rigautomation.Store
	scheduler *rigautomation.Scheduler
	// interval is the configured scheduler tick, reused when Retry builds a
	// replacement scheduler after a lock takeover.
	interval time.Duration

	// nameSeq numbers generated "自动化 N" names.
	nameSeq int
	// notified dedupes completion notifications per delivery.
	notified map[string]bool

	// chatUnsub releases the chat-session subscription on Close.
	chatUnsub func()
	// closed stops new runs after Close begins.
	closed bool
	// degradedErr is set when another instance owns the registry lock: this
	// service stays usable for CRUD/read but never starts the scheduler.
	degradedErr error

	subscribers    map[uint64]func()
	nextSubscriber uint64

	closeOnce sync.Once
	// runs tracks in-flight RunNow/onChatChange work so Close waits for it.
	runs sync.WaitGroup
}

var _ contracts.AutomationService = (*service)(nil)
var _ contracts.AutomationStatusProvider = (*service)(nil)
var _ contracts.AutomationRecovery = (*service)(nil)

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
		notified:    make(map[string]bool),
		subscribers: make(map[uint64]func()),
	}
	// A corrupt or unreadable registry is quarantined by the SDK store; log the
	// recovery with the product's wording (the SDK has no logger).
	service.logRegistryRecovery()
	// The registry is process-owned: the first instance acquires the directory
	// lock and runs the scheduler. A second instance (another app window, a CLI
	// next to the desktop app) must not fail startup and must never deliver the
	// same task twice, so it degrades to a scheduler-less service: the read
	// model and CRUD stay available, scheduled runs do not.
	store, err := rigautomation.OpenFileLocked(path)
	degraded := false
	if errors.Is(err, rigautomation.ErrLocked) {
		degraded = true
		service.degradedErr = err
		service.logger.Printf("%s: automation registry %q is locked by another running instance; scheduled runs are disabled in this window (only the owning instance delivers automations)", ModuleID, path)
		store, err = rigautomation.OpenFile(path)
	}
	if err != nil {
		return nil, err
	}
	service.store = store
	service.interval = tick
	service.scheduler = &rigautomation.Scheduler{
		Store:    store,
		Now:      func() time.Time { return time.Now().UTC() },
		Deliver:  service.deliver,
		Settle:   service.settle,
		Next:     service.nextRun,
		Interval: tick,
		Logger: func(format string, args ...any) {
			service.logger.Printf(format, args...)
		},
	}
	if !degraded {
		// Settle runs that were delivered but never completed before the
		// previous shutdown (the SDK rebuilds its pending set from the durable
		// records).
		service.adoptLegacyDeliveredRuns(store)
		if err := service.scheduler.SettlePending(context.Background()); err != nil && !errors.Is(err, rigautomation.ErrSchedulerClosed) {
			service.logger.Printf("%s: initialize scheduler: %v", ModuleID, err)
		}
		// The automation module subscribes to chat-session changes to settle its
		// delivered runs promptly. A host whose chat service cannot subscribe
		// still runs tasks; settlement then happens on the ticker.
		if unsubscribe, err := chat.Subscribe(service.onChatChange); err != nil {
			service.logger.Printf("%s: subscribe chat session states: %v", ModuleID, err)
		} else {
			service.chatUnsub = unsubscribe
		}
		if err := service.scheduler.Start(); err != nil {
			service.Close()
			return nil, err
		}
	}
	tasks, _ := store.List()
	if degraded {
		service.logger.Printf("%s: initialized in degraded mode with %d tasks (scheduler disabled)", ModuleID, len(tasks))
	} else {
		service.logger.Printf("%s: initialized with %d tasks (tick %s)", ModuleID, len(tasks), tick)
	}
	return service, nil
}

// Status implements contracts.AutomationStatusProvider: the service is
// degraded (unavailable) when another instance owns the registry lock. The read
// is cheap and lock-state based; it never probes the other process.
func (s *service) Status(ctx context.Context) contracts.AutomationStatus {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return contracts.AutomationStatus{Available: false, Reason: err.Error()}
		}
	}
	if s == nil {
		return contracts.AutomationStatus{Available: false, Reason: "automation service is unavailable"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.degradedErr != nil {
		if errors.Is(s.degradedErr, rigautomation.ErrLocked) {
			lockedBy := ""
			if s.path != "" {
				lockedBy = filepath.Join(filepath.Dir(s.path), rigautomation.LockFileName)
			}
			return contracts.AutomationStatus{
				Available: false,
				Reason:    "another instance owns the automation registry",
				LockedBy:  lockedBy,
			}
		}
		return contracts.AutomationStatus{Available: false, Reason: s.degradedErr.Error()}
	}
	if s.closed {
		return contracts.AutomationStatus{Available: false, Reason: "automation service is closed"}
	}
	return contracts.AutomationStatus{Available: true}
}

// registry returns the current durable store (it changes after a successful
// Retry lock takeover).
func (s *service) registry() *rigautomation.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store
}

// Retry implements contracts.AutomationRecovery: while degraded it tries to
// take over the registry lock again. On success it swaps in a freshly opened
// locked store and a new scheduler, settles runs delivered by the previous
// owner, subscribes to chat changes and starts the ticker. On lock contention
// it stays degraded and reports contracts.ErrAutomationLocked.
func (s *service) Retry(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.degradedErr == nil {
		// Already available: Retry is a no-op.
		s.mu.Unlock()
		return nil
	}
	path := s.path
	interval := s.interval
	s.mu.Unlock()

	store, err := rigautomation.OpenFileLocked(path)
	if err != nil {
		if errors.Is(err, rigautomation.ErrLocked) {
			s.mu.Lock()
			if s.degradedErr != nil {
				s.degradedErr = err
			}
			s.mu.Unlock()
			return fmt.Errorf("%w: %v", contracts.ErrAutomationLocked, err)
		}
		s.mu.Lock()
		if s.degradedErr != nil {
			s.degradedErr = err
		}
		s.mu.Unlock()
		return err
	}
	scheduler := &rigautomation.Scheduler{
		Store:    store,
		Now:      func() time.Time { return time.Now().UTC() },
		Deliver:  s.deliver,
		Settle:   s.settle,
		Next:     s.nextRun,
		Interval: interval,
		Logger: func(format string, args ...any) {
			s.logger.Printf(format, args...)
		},
	}
	// Settle whatever the previous owner delivered while this instance was
	// degraded (legacy records need their delivery id adopted first).
	s.adoptLegacyDeliveredRuns(store)
	if err := scheduler.SettlePending(ctx); err != nil && !errors.Is(err, rigautomation.ErrSchedulerClosed) {
		s.logger.Printf("%s: retry: settle pending runs: %v", ModuleID, err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = store.Close()
		return ErrClosed
	}
	oldStore := s.store
	s.store = store
	s.scheduler = scheduler
	s.degradedErr = nil
	needSubscribe := s.chatUnsub == nil
	s.mu.Unlock()
	if oldStore != nil {
		// The degraded store was opened without the lock; closing it only
		// releases a lock if one was lazily acquired (normally none).
		_ = oldStore.Close()
	}
	if needSubscribe {
		if unsubscribe, err := s.chat.Subscribe(s.onChatChange); err != nil {
			s.logger.Printf("%s: retry: subscribe chat session states: %v", ModuleID, err)
		} else {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				unsubscribe()
			} else {
				s.chatUnsub = unsubscribe
				s.mu.Unlock()
			}
		}
	}
	if err := scheduler.Start(); err != nil {
		s.mu.Lock()
		s.degradedErr = err
		s.mu.Unlock()
		// Do not sit degraded while holding the registry lock: release it so
		// another instance (or the next Retry) can take over.
		_ = store.Close()
		s.logger.Printf("%s: retry: start scheduler: %v", ModuleID, err)
		s.notify()
		return err
	}
	s.logger.Printf("%s: automation registry recovered; scheduler started", ModuleID)
	s.notify()
	return nil
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

// logRegistryRecovery reports a corrupt/unreadable registry before the SDK
// quarantines it, keeping the product's recovery log lines and wording.
func (s *service) logRegistryRecovery() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Printf("%s: automation registry %q could not be read (%v); quarantine it and start with no tasks", ModuleID, s.path, err)
		}
		return
	}
	var document struct {
		Tasks []rigautomation.Task `json:"tasks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		s.logger.Printf("%s: automation registry %q is corrupt (%v); quarantine it and start with no tasks", ModuleID, s.path, err)
	}
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
		scheduler := s.scheduler
		store := s.store
		s.mu.Unlock()
		if unsubscribe != nil {
			unsubscribe()
		}
		if scheduler != nil {
			scheduler.Stop()
		}
		s.runs.Wait()
		if store != nil {
			// Release the cross-process registry lock so the next instance can
			// take ownership (tests and host restarts rely on this).
			if err := store.Close(); err != nil {
				s.logger.Printf("%s: release automation registry lock: %v", ModuleID, err)
			}
		}
		s.logger.Printf("%s: scheduler stopped", ModuleID)
	})
}

// --- CRUD ------------------------------------------------------------------

// List implements contracts.AutomationService, ordered by creation time.
func (s *service) List(ctx context.Context) ([]contracts.AutomationTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cores, err := s.registry().List()
	if err != nil {
		return nil, err
	}
	out := make([]contracts.AutomationTask, len(cores))
	for index, core := range cores {
		out[index] = s.contractTask(core)
	}
	return out, nil
}

// Create implements contracts.AutomationService.
func (s *service) Create(ctx context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	if err := ctx.Err(); err != nil {
		return contracts.AutomationTask{}, err
	}
	now := time.Now().UTC()
	store := s.registry()
	id := strings.TrimSpace(task.ID)
	if id == "" {
		id = s.newTaskID()
	} else if _, ok := store.Get(id); ok {
		return contracts.AutomationTask{}, fmt.Errorf("%s: task %q already exists", ModuleID, id)
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		s.mu.Lock()
		s.nameSeq++
		name = fmt.Sprintf("自动化 %d", s.nameSeq)
		s.mu.Unlock()
	}
	schedule, nextRun, err := prepareSchedule(task.Schedule, task.Enabled, now)
	if err != nil {
		return contracts.AutomationTask{}, err
	}
	core := rigautomation.Task{
		ID:        id,
		Name:      name,
		Prompt:    task.Prompt,
		Schedule:  rigautomation.Schedule(schedule),
		Enabled:   task.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
		NextRun:   nextRun,
	}
	applyHostFields(&core, task)
	created, err := store.Create(core)
	if err != nil {
		return contracts.AutomationTask{}, err
	}
	s.notify()
	return s.contractTask(created), nil
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
	store := s.registry()
	core, ok := store.Get(id)
	if !ok {
		return contracts.AutomationTask{}, fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	}
	existing := s.contractTask(core)
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = existing.Name
	}
	now := time.Now().UTC()
	schedule, nextRun, err := prepareSchedule(task.Schedule, task.Enabled, now)
	if err != nil {
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
	core.Name = updated.Name
	core.Prompt = updated.Prompt
	core.Schedule = rigautomation.Schedule(updated.Schedule)
	core.Enabled = updated.Enabled
	core.NextRun = updated.NextRun
	core.UpdatedAt = updated.UpdatedAt
	core.LastError = updated.LastError
	applyHostFields(&core, updated)
	stored, err := store.Update(core)
	if err != nil {
		if errors.Is(err, rigautomation.ErrTaskNotFound) {
			return contracts.AutomationTask{}, fmt.Errorf("%w: %q", ErrTaskNotFound, id)
		}
		return contracts.AutomationTask{}, err
	}
	s.notify()
	return s.contractTask(stored), nil
}

// Delete implements contracts.AutomationService; unknown IDs are a no-op.
func (s *service) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	store := s.registry()
	if _, ok := store.Get(id); !ok {
		return nil
	}
	if err := store.Delete(id); err != nil {
		return err
	}
	s.notify()
	return nil
}

// RunNow implements contracts.AutomationService: it starts the task execution
// immediately (even for a disabled task) and returns unknown-task/context/closed
// errors. Execution failures land in LastError. After Close it returns ErrClosed.
func (s *service) RunNow(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	if s.degradedErr != nil {
		degradedErr := s.degradedErr
		s.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrSchedulerDisabled, degradedErr)
	}
	if _, ok := s.store.Get(id); !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	}
	scheduler := s.scheduler
	// Track accepted runs so Close waits for their registry writes (BE-07).
	s.runs.Add(1)
	s.mu.Unlock()
	defer s.runs.Done()
	if scheduler == nil {
		return fmt.Errorf("%w: scheduler is unavailable", ErrSchedulerDisabled)
	}

	err := scheduler.RunNow(ctx, id)
	switch {
	case errors.Is(err, rigautomation.ErrTaskNotFound):
		return fmt.Errorf("%w: %q", ErrTaskNotFound, id)
	case errors.Is(err, rigautomation.ErrSchedulerClosed):
		return ErrClosed
	default:
		return err
	}
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

// newTaskID allocates a free durable task ID.
func (s *service) newTaskID() string {
	for {
		id := fmt.Sprintf("auto-%d", time.Now().UnixNano())
		if _, ok := s.store.Get(id); !ok {
			return id
		}
	}
}

// --- host field mapping ----------------------------------------------------

// Host-field keys carried through the SDK's opaque task fields.
const (
	hostKeyModel            = "model"
	hostKeyWorkspaceID      = "workspaceId"
	hostKeyPermission       = "permission"
	hostKeySessionID        = "sessionId"
	hostKeyNewSessionPerRun = "newSessionPerRun"
)

// contractTask maps one durable SDK task onto the product read model.
func (s *service) contractTask(core rigautomation.Task) contracts.AutomationTask {
	task := contracts.AutomationTask{
		ID:               core.ID,
		Name:             core.Name,
		Prompt:           core.Prompt,
		Schedule:         string(core.Schedule),
		Enabled:          core.Enabled,
		CreatedAt:        core.CreatedAt,
		UpdatedAt:        core.UpdatedAt,
		LastRun:          core.LastRun,
		NextRun:          core.NextRun,
		LastError:        core.LastError,
		Model:            hostString(core.Host, hostKeyModel),
		WorkspaceID:      hostString(core.Host, hostKeyWorkspaceID),
		Permission:       hostString(core.Host, hostKeyPermission),
		SessionID:        hostString(core.Host, hostKeySessionID),
		NewSessionPerRun: hostBool(core.Host, hostKeyNewSessionPerRun),
	}
	if len(core.RunHistory) > 0 {
		task.RunHistory = make([]contracts.AutomationRunRecord, len(core.RunHistory))
		for index, run := range core.RunHistory {
			task.RunHistory[index] = contractRun(run)
		}
	}
	return task
}

// contractRun maps one SDK run onto the product run record. New runs carry the
// conversation as the delivery id; legacy records carry it in the preserved
// host fields ("sessionId"/"summary", and "finishedAt" for the old completion
// spelling).
func contractRun(run rigautomation.Run) contracts.AutomationRunRecord {
	record := contracts.AutomationRunRecord{
		ID:          run.ID,
		StartedAt:   run.StartedAt,
		DeliveredAt: run.DeliveredAt,
		CompletedAt: run.CompletedAt,
		Status:      run.Status,
		Error:       run.Error,
		DurationMS:  run.DurationMS,
	}
	record.SessionID = hostString(run.Host, "sessionId")
	if record.SessionID == "" {
		record.SessionID = strings.TrimSpace(run.DeliveryID)
	}
	if summary := hostString(run.Host, "summary"); summary != "" {
		record.Summary = summary
	} else if record.SessionID != "" {
		record.Summary = "ran in chat session " + record.SessionID
	} else {
		record.Summary = run.Error
	}
	if record.CompletedAt.IsZero() {
		if legacy := hostString(run.Host, "finishedAt"); legacy != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, legacy); err == nil {
				record.CompletedAt = parsed
			}
		}
	}
	return record
}

// adoptLegacyDeliveredRuns migrates pre-SDK registries: a delivered run there
// identifies its conversation by "sessionId" (or the legacy summary) but has no
// "deliveryId", which the SDK needs to settle it. Derive the delivery id once so
// the startup settlement pass picks the run up.
func (s *service) adoptLegacyDeliveredRuns(store *rigautomation.Store) {
	cores, err := store.List()
	if err != nil {
		return
	}
	for _, core := range cores {
		changed := false
		for index := range core.RunHistory {
			run := &core.RunHistory[index]
			if run.Status != rigautomation.StatusDelivered || strings.TrimSpace(run.DeliveryID) != "" {
				continue
			}
			sessionID := hostString(run.Host, "sessionId")
			if sessionID == "" {
				sessionID = contracts.AutomationRunSessionID(hostString(run.Host, "summary"))
			}
			if sessionID == "" {
				sessionID = hostString(core.Host, hostKeySessionID)
			}
			if sessionID == "" {
				continue
			}
			run.DeliveryID = sessionID
			changed = true
		}
		if !changed {
			continue
		}
		if _, err := store.Update(core); err != nil {
			s.logger.Printf("%s: adopt legacy delivered run of %s: %v", ModuleID, core.ID, err)
		}
	}
}

// applyHostFields writes the product-only task fields into the SDK task's
// opaque host map (empty values remove the key, so a cleared override stays
// cleared in the registry).
func applyHostFields(core *rigautomation.Task, task contracts.AutomationTask) {
	setHostString(core, hostKeyModel, strings.TrimSpace(task.Model))
	setHostString(core, hostKeyWorkspaceID, strings.TrimSpace(task.WorkspaceID))
	setHostString(core, hostKeyPermission, strings.TrimSpace(task.Permission))
	setHostString(core, hostKeySessionID, strings.TrimSpace(task.SessionID))
	setHostBool(core, hostKeyNewSessionPerRun, task.NewSessionPerRun)
}

// hostString reads one string host field ("" when absent or malformed).
func hostString(host map[string]json.RawMessage, key string) string {
	raw, ok := host[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// hostBool reads one bool host field (false when absent or malformed).
func hostBool(host map[string]json.RawMessage, key string) bool {
	raw, ok := host[key]
	if !ok {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}

// setHostString writes (or removes) one string host field.
func setHostString(core *rigautomation.Task, key, value string) {
	if value == "" {
		delete(core.Host, key)
		return
	}
	if core.Host == nil {
		core.Host = make(map[string]json.RawMessage, 5)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	core.Host[key] = encoded
}

// setHostBool writes a bool host field (false removes it, matching omitempty).
func setHostBool(core *rigautomation.Task, key string, value bool) {
	if !value {
		delete(core.Host, key)
		return
	}
	if core.Host == nil {
		core.Host = make(map[string]json.RawMessage, 5)
	}
	core.Host[key] = json.RawMessage(`true`)
}

// --- schedule --------------------------------------------------------------

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

// nextRun computes the SDK task's next firing after one run: one-shot schedules
// end the task, a repeating schedule advances. It owns no schedule parsing; the
// product contracts parser does.
func (s *service) nextRun(core rigautomation.Task, after time.Time) (time.Time, bool) {
	if !core.Enabled {
		return time.Time{}, false
	}
	schedule, err := Parse(string(core.Schedule))
	if err != nil {
		s.logger.Printf("%s: task %s has an invalid schedule %q: %v", ModuleID, core.ID, core.Schedule, err)
		return time.Time{}, false
	}
	if schedule.Kind() == KindAfter || schedule.Kind() == KindAt {
		return time.Time{}, false
	}
	next := schedule.Next(after)
	if next.IsZero() {
		// A repeating rule with no future occurrence ends the task instead of
		// leaving it enabled forever.
		return time.Time{}, false
	}
	return next, true
}

// --- delivery --------------------------------------------------------------

// deliver is the SDK Deliver callback: it resolves the product task, sends the
// prompt through the chat service (reusing or creating the bound conversation,
// applying model/workspace/permission overrides) and returns the conversation id
// as the delivery id (the same id settles the run later).
func (s *service) deliver(ctx context.Context, core rigautomation.Task) (string, error) {
	task := s.contractTask(core)
	delivery, err := s.runTask(task)
	// The first successful session creation binds future runs to that
	// conversation, even when the send itself failed.
	if delivery.Created && !task.NewSessionPerRun {
		s.rememberSession(core.ID, delivery.SessionID)
	}
	if err != nil {
		return "", err
	}
	return delivery.SessionID, nil
}

// rememberSession persists the conversation subsequent runs deliver to.
func (s *service) rememberSession(taskID, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	store := s.registry()
	current, ok := store.Get(taskID)
	if !ok {
		return
	}
	setHostString(&current, hostKeySessionID, sessionID)
	current.UpdatedAt = time.Now().UTC()
	if _, err := store.Update(current); err != nil {
		s.logger.Printf("%s: run %s: remember session %s: %v", ModuleID, taskID, sessionID, err)
	}
}

// settle is the SDK Settle callback: the chat read model decides the run
// outcome. Completion notifications fire once per delivery here (never on the
// delivered state, never for a neutral interruption).
func (s *service) settle(_ context.Context, core rigautomation.Task, deliveryID string) (string, string, bool) {
	sessionID := strings.TrimSpace(deliveryID)
	if sessionID == "" {
		sessionID = hostString(core.Host, hostKeySessionID)
	}
	status, detail, settled := s.runOutcome(sessionID)
	if settled && (status == contracts.AutomationRunSuccess || status == contracts.AutomationRunFailure) {
		s.notifySettledOnce(deliveryID, core.Name, sessionID, status, detail)
	}
	return status, detail, settled
}

// notifySettledOnce reports one terminal run to the optional host notifier.
func (s *service) notifySettledOnce(deliveryID, taskName, sessionID, status, detail string) {
	key := strings.TrimSpace(deliveryID)
	if key == "" {
		key = strings.TrimSpace(sessionID)
	}
	if key == "" {
		return
	}
	s.mu.Lock()
	if s.notified[key] {
		s.mu.Unlock()
		return
	}
	s.notified[key] = true
	s.mu.Unlock()
	s.notifyRunSettled(taskName, sessionID, status, detail)
}

// onChatChange is the ChatService subscription callback: when a session changed,
// settle its automation runs by ticking the SDK scheduler (which retries every
// delivered run). It only dispatches a tracked goroutine.
func (s *service) onChatChange(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	// The agent notifies on every streaming event; the cheap session list probe
	// keeps a long run from ticking on each one.
	if running, exists := s.chatSessionRunning(sessionID); exists && running {
		return
	}
	s.mu.Lock()
	if s.closed || s.degradedErr != nil {
		s.mu.Unlock()
		return
	}
	scheduler := s.scheduler
	s.runs.Add(1)
	s.mu.Unlock()
	if scheduler == nil {
		s.runs.Done()
		return
	}
	go func() {
		defer s.runs.Done()
		if err := scheduler.SettlePending(context.Background()); err != nil && !errors.Is(err, rigautomation.ErrSchedulerClosed) {
			s.logger.Printf("%s: settle on chat change: %v", ModuleID, err)
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

// runTask delivers one run through the chat service. The product handlers below
// are unchanged from the pre-SDK implementation; the scheduler owns recording.
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
		// origin, not by its title (audit F6).
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

// --- run outcome -----------------------------------------------------------

// runOutcome maps a chat session's read model onto a terminal run status,
// reporting settled=false while the session is still running.
func (s *service) runOutcome(sessionID string) (status, errText string, settled bool) {
	if strings.TrimSpace(sessionID) == "" {
		return contracts.AutomationRunInterrupted, "the chat session is no longer available", true
	}
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

// notifyRunSettled reports one settled automation run to the optional host
// notifier (a no-op without one). A delivered run never notifies; interrupted
// runs stay silent too.
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

// recordSessionOrigin persists the origin of one run session (best effort).
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

// truncateRunSummary bounds one history summary to 200 runes.
func truncateRunSummary(summary string) string {
	const limit = 200
	runes := []rune(summary)
	if len(runes) <= limit {
		return summary
	}
	return string(runes[:limit-1]) + "…"
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
// the durable adapter.
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
