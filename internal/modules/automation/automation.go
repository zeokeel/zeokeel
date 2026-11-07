// Package automation implements ZeoKeel's automation backend module: the
// persisted scheduled-task registry, the scheduler loop that fires due tasks
// through the chat service, and the optional rig schedule_* tools bound to this
// durable scheduler.
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
		service, err := newService(dataDir, cfg, logger, chat)
		if err != nil {
			return fmt.Errorf("%s: open automation registry: %w", ModuleID, err)
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
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	Schedule  string    `json:"schedule"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	LastRun   time.Time `json:"lastRun"`
	NextRun   time.Time `json:"nextRun"`
	LastError string    `json:"lastError"`

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

	tasks    []contracts.AutomationTask
	inFlight map[string]bool
	nameSeq  int

	subscribers    map[uint64]func()
	nextSubscriber uint64

	interval  time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

var _ contracts.AutomationService = (*service)(nil)

// discardLogger drops automation log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// newService loads the registry and starts the scheduler loop.
func newService(dataDir string, cfg Config, logger contracts.Logger, chat contracts.ChatService) (*service, error) {
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
		inFlight:    map[string]bool{},
		subscribers: map[uint64]func(){},
		interval:    tick,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	if err := service.load(); err != nil {
		return nil, err
	}
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

// load reads the registry, treating a missing file as empty.
func (s *service) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: read %q: %w", ModuleID, s.path, err)
	}
	var document registryDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%s: parse %q: %w", ModuleID, s.path, err)
	}
	tasks := make([]contracts.AutomationTask, 0, len(document.Tasks))
	for _, record := range document.Tasks {
		if strings.TrimSpace(record.ID) == "" {
			continue
		}
		tasks = append(tasks, contracts.AutomationTask{
			ID:         record.ID,
			Name:       record.Name,
			Prompt:     record.Prompt,
			Schedule:   record.Schedule,
			Enabled:    record.Enabled,
			CreatedAt:  record.CreatedAt.UTC(),
			UpdatedAt:  record.UpdatedAt.UTC(),
			LastRun:    record.LastRun.UTC(),
			NextRun:    record.NextRun.UTC(),
			LastError:  record.LastError,
			RunHistory: append([]contracts.AutomationRunRecord(nil), record.RunHistory...),
		})
	}
	s.tasks = tasks
	return nil
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
			ID:         task.ID,
			Name:       task.Name,
			Prompt:     task.Prompt,
			Schedule:   task.Schedule,
			Enabled:    task.Enabled,
			CreatedAt:  task.CreatedAt.UTC(),
			UpdatedAt:  task.UpdatedAt.UTC(),
			LastRun:    task.LastRun.UTC(),
			NextRun:    task.NextRun.UTC(),
			LastError:  task.LastError,
			RunHistory: append([]contracts.AutomationRunRecord(nil), task.RunHistory...),
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

// Close stops the scheduler loop and waits for it to exit. It is idempotent.
func (s *service) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
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
func (s *service) tick(now time.Time) {
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
		go s.execute(id, false)
	}
}

// --- CRUD ------------------------------------------------------------------

// List implements contracts.AutomationService, ordered by creation time.
func (s *service) List(ctx context.Context) ([]contracts.AutomationTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]contracts.AutomationTask(nil), s.tasks...)
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
	schedule := strings.TrimSpace(task.Schedule)
	nextRun := time.Time{}
	if task.Enabled {
		next, err := NextRun(schedule, now)
		if err != nil {
			s.mu.Unlock()
			return contracts.AutomationTask{}, err
		}
		nextRun = next
	}
	created := contracts.AutomationTask{
		ID:        id,
		Name:      name,
		Prompt:    task.Prompt,
		Schedule:  schedule,
		Enabled:   task.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
		NextRun:   nextRun,
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
	return created, nil
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
	schedule := strings.TrimSpace(task.Schedule)
	nextRun := time.Time{}
	if task.Enabled {
		next, err := NextRun(schedule, now)
		if err != nil {
			s.mu.Unlock()
			return contracts.AutomationTask{}, err
		}
		nextRun = next
	}
	updated := existing
	updated.Name = name
	updated.Prompt = task.Prompt
	updated.Schedule = schedule
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
	return updated, nil
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
	go s.execute(id, true)
	return nil
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

// execute runs one task through the chat service and updates its scheduling
// state. force marks a RunNow execution (it runs even for disabled tasks).
func (s *service) execute(taskID string, force bool) {
	s.mu.Lock()
	index := s.indexLocked(taskID)
	if index < 0 || s.inFlight[taskID] {
		s.mu.Unlock()
		return
	}
	task := s.tasks[index]
	if !force && !task.Enabled {
		s.mu.Unlock()
		return
	}
	s.inFlight[taskID] = true
	s.mu.Unlock()

	startedAt := time.Now().UTC()
	summary, runErr := s.runTask(task)
	now := time.Now().UTC()

	s.mu.Lock()
	if index := s.indexLocked(taskID); index >= 0 {
		current := s.tasks[index]
		current.LastRun = now
		current.UpdatedAt = now
		record := contracts.AutomationRunRecord{
			StartedAt:  startedAt,
			FinishedAt: now,
			Status:     contracts.AutomationRunSuccess,
		}
		if runErr != nil {
			current.LastError = runErr.Error()
			record.Status = contracts.AutomationRunFailure
			record.Summary = truncateRunSummary(runErr.Error())
		} else {
			current.LastError = ""
			record.Summary = truncateRunSummary(summary)
		}
		current.RunHistory = append([]contracts.AutomationRunRecord{record}, current.RunHistory...)
		if len(current.RunHistory) > maxRunHistory {
			current.RunHistory = current.RunHistory[:maxRunHistory]
		}
		current.NextRun, current.Enabled = s.rescheduleLocked(current, now)
		s.tasks[index] = current
	}
	delete(s.inFlight, taskID)
	if err := s.persistLocked(); err != nil {
		s.logger.Printf("%s: persist run of %s: %v", ModuleID, taskID, err)
	}
	s.mu.Unlock()
	s.notify()
}

// runTask creates the automation chat session and sends the prompt, returning
// the run summary for the history (the session reference on success).
func (s *service) runTask(task contracts.AutomationTask) (string, error) {
	title := "自动化: " + task.Name
	sessionID, err := s.chat.NewSession(title)
	if err != nil {
		s.logger.Printf("%s: run %s: create chat session: %v", ModuleID, task.ID, err)
		return "", fmt.Errorf("create chat session: %w", err)
	}
	if err := s.chat.Send(sessionID, task.Prompt); err != nil {
		s.logger.Printf("%s: run %s: send prompt: %v", ModuleID, task.ID, err)
		return "", fmt.Errorf("send prompt: %w", err)
	}
	s.logger.Printf("%s: ran task %s in chat session %s", ModuleID, task.ID, sessionID)
	return fmt.Sprintf("ran in chat session %s", sessionID), nil
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
	if schedule.Kind() == KindAfter {
		return time.Time{}, false
	}
	return schedule.Next(from), true
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
