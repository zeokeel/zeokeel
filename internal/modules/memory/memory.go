// Package memory implements ZeoKeel's durable memory backend module.
//
// It is a kernel backend module like the session store: it requires the product
// data directory and the host logger, loads a JSON memory file (creating an
// empty store when the file does not exist yet), provides the durable
// contracts.KeyMemory store and registers the product memory tool (the Rig tool
// plus the update action) into the shared toolkit registry when that registry is
// already available.
//
// The store implements rig.MemoryStore (contracts.MemoryStore is its product
// alias) over a JSON document {"entries": [...]}: every mutation is written
// atomically (temporary file + fsync + rename + directory fsync) so a crash
// cannot leave a half-written memory file. A malformed or unreadable file is
// quarantined (<path>.corrupt-<ts>) and the module starts with an empty store
// instead of failing the whole product assembly (FR-06).
//
// Entries are workspace aware (FR-25): an entry added for "the current
// workspace" is persisted with the workspace identity in its scope
// ("workspace:<id>"), and List/Search only return global entries plus the
// entries of the current workspace. ListAll exposes every entry to the settings
// page. The configured scope is the single default for callers that omit one.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/memorytools"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/memory"
	// Version is the module's semantic version.
	Version = "1.1.0"
	// DefaultFileName is the memory file name under the product data directory.
	DefaultFileName = "memory.json"
	// DefaultScope is the scope stored for entries that carry none.
	DefaultScope = contracts.MemoryScopeGlobal
	// fileMode is the memory file permission (private to the user).
	fileMode = 0o600
	// dirMode is the memory directory permission.
	dirMode = 0o700
)

// Config is the module's typed assembly config.
type Config struct {
	// Path is the memory file: an absolute path, or a path relative to the
	// product data directory (default memory.json there).
	Path string `json:"path"`
	// Scope is the default scope applied to memories added without one (default
	// "global"). Only "global" and "workspace" are accepted; "workspace" is
	// resolved to the workspace current at write time.
	Scope string `json:"scope"`
}

// Module is the durable memory backend descriptor. It provides
// contracts.KeyMemory.
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
		// The workspace service is optional: a host without workspaces keeps the
		// legacy flat memory behaviour.
		var workspace contracts.WorkspaceService
		if service, ok := ctx.Get(contracts.KeyWorkspace); ok {
			workspace = service
		}
		// The settings store is optional: it only carries the automatic
		// consolidation policy (maxEntries/maxAgeDays).
		var settings contracts.SettingsService
		if service, ok := ctx.Get(contracts.KeySettings); ok {
			settings = service
		}
		store, err := openStore(dataDir, cfg, logger, workspace, policyFromSettings(settings))
		if err != nil {
			return fmt.Errorf("%s: open memory store: %w", ModuleID, err)
		}

		// The Toolkit module may have registered its process-local memory tool
		// already (it ships rigmemory.Tools(nil)). Registering the durable
		// store here only succeeds when that name is still free; a duplicate is
		// logged and leaves the existing tool untouched, so the module can load
		// no matter which entry order the assembly chooses.
		if registry, ok := ctx.Get(contracts.KeyToolkit); ok && registry != nil {
			registerMemoryTools(ctx, registry, store, logger)
		}

		if _, err := ctx.Provide(contracts.KeyMemory, contracts.MemoryStore(store)); err != nil {
			return fmt.Errorf("%s: provide memory store: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyMemory)).
		AddRequires(
			module.Requires(contracts.KeyDataDir),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel durable memory").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the store could not honour. Relative paths
// are resolved later against the product data directory.
func validateConfig(cfg Config) error {
	if strings.ContainsRune(cfg.Path, 0) {
		return fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	if _, err := normalizeConfiguredScope(cfg.Scope); err != nil {
		return err
	}
	return nil
}

// registerMemoryTools registers the product memory tool (the Rig memory tool
// plus the update action) bound to store into the shared toolkit registry and
// unregisters it again on module disposal. Errors (for example a duplicate name
// owned by the toolkit module) are logged and do not fail activation.
func registerMemoryTools(ctx cordis.Context, registry *rigtools.Registry, store contracts.MemoryStore, logger contracts.Logger) {
	for _, tool := range memorytools.Tools(store) {
		name := tool.Name()
		if err := registry.Register(tool); err != nil {
			logger.Printf("%s: register tool %q: %v", ModuleID, name, err)
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

// normalizeConfiguredScope canonicalizes the assembly-level default scope.
func normalizeConfiguredScope(scope string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(scope)); normalized {
	case "":
		return DefaultScope, nil
	case contracts.MemoryScopeGlobal, contracts.MemoryScopeWorkspace:
		return normalized, nil
	default:
		return "", fmt.Errorf("%s: invalid scope %q (want %q or %q)", ModuleID, scope, contracts.MemoryScopeGlobal, contracts.MemoryScopeWorkspace)
	}
}

// normalizeScope canonicalizes and validates one entry scope against the
// current workspace. An empty scope becomes the configured default, "workspace"
// becomes the bound current-workspace scope, an already bound workspace scope
// keeps its identity, and anything else is rejected (the same rule Add, Update
// and the model tool share).
func (s *Store) normalizeScope(ctx context.Context, scope string) (string, error) {
	raw := strings.TrimSpace(scope)
	if raw == "" {
		raw = s.defaultScope
	}
	switch strings.ToLower(raw) {
	case contracts.MemoryScopeGlobal:
		return contracts.MemoryScopeGlobal, nil
	case contracts.MemoryScopeWorkspace:
		return contracts.MemoryWorkspaceScope(s.currentWorkspaceID(ctx)), nil
	}
	if workspaceID, ok := contracts.MemoryScopeWorkspaceID(raw); ok {
		return contracts.MemoryWorkspaceScope(workspaceID), nil
	}
	return "", fmt.Errorf("%s: invalid scope %q (want %q, %q or \"workspace:<id>\")", ModuleID, scope, contracts.MemoryScopeGlobal, contracts.MemoryScopeWorkspace)
}

// policyFromSettings reads the automatic consolidation policy. Missing or
// malformed rows mean "no policy" (0 = unlimited), matching the settings page.
func policyFromSettings(settings contracts.SettingsService) contracts.MemoryPolicy {
	if settings == nil {
		return contracts.MemoryPolicy{}
	}
	return contracts.MemoryPolicy{
		MaxEntries: parsePolicyInt(settings.Get(contracts.SettingMemoryMaxEntries, "")),
		MaxAge:     time.Duration(parsePolicyInt(settings.Get(contracts.SettingMemoryMaxAgeDays, ""))) * 24 * time.Hour,
	}
}

// parsePolicyInt parses one policy input: empty/invalid/negative means 0
// (unlimited).
func parsePolicyInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// --- store -----------------------------------------------------------------

// fileDocument is the on-disk memory document.
type fileDocument struct {
	Entries []contracts.Memory `json:"entries"`
}

// Store is a JSON-file backed contracts.MemoryStore. All methods are safe for
// concurrent use; each mutation persists the whole (small) document atomically.
type Store struct {
	mu        sync.Mutex
	path      string
	entries   []contracts.Memory
	logger    contracts.Logger
	workspace contracts.WorkspaceService

	defaultScope string
	policy       contracts.MemoryPolicy
	recovery     string
	version      uint64
}

var (
	_ contracts.MemoryStore            = (*Store)(nil)
	_ contracts.MemoryUpdater          = (*Store)(nil)
	_ contracts.MemoryConsolidator     = (*Store)(nil)
	_ contracts.MemoryAllLister        = (*Store)(nil)
	_ contracts.MemoryRecoveryReporter = (*Store)(nil)
	_ contracts.MemoryVersioner        = (*Store)(nil)
)

// newStore resolves the configured path and opens the store without a host
// logger (tests and diagnostics). See openStore for the product entry point.
func newStore(dataDir string, cfg Config) (*Store, error) {
	return openStore(dataDir, cfg, nil, nil, contracts.MemoryPolicy{})
}

// openStore resolves the configured path, loads the existing document and
// applies the automatic consolidation policy once. A missing file starts an
// empty store; a malformed or unreadable file is quarantined and never fails
// activation (FR-06).
func openStore(dataDir string, cfg Config, logger contracts.Logger,
	workspace contracts.WorkspaceService, policy contracts.MemoryPolicy) (*Store, error) {
	path, err := resolvePath(dataDir, cfg.Path)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeConfiguredScope(cfg.Scope)
	if err != nil {
		return nil, err
	}
	store := &Store{
		path:         path,
		defaultScope: scope,
		logger:       logger,
		workspace:    workspace,
		policy:       policy,
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	// The policy is enforced on open (M-10): a user who set a cap or a
	// retention window gets it applied without clicking the settings action.
	store.enforcePolicy(context.Background())
	return store, nil
}

// resolvePath resolves Config.Path against the product data directory.
func resolvePath(dataDir, requested string) (string, error) {
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

// Path returns the resolved memory file path (diagnostics and tests).
func (s *Store) Path() string { return s.path }

// load reads the document, treating a missing file as an empty store. An
// unreadable or malformed file is quarantined and reported through
// RecoveryNotice instead of failing activation (FR-06).
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.noteRecovery(fmt.Sprintf("memory file %q could not be read (%v); starting with an empty memory store", s.path, err))
		return nil
	}
	entries, err := decodeEntries(data)
	if err != nil {
		s.quarantine(fmt.Sprintf("memory file %q is corrupt (%v)", s.path, err))
		return nil
	}
	s.entries = entries
	return nil
}

// decodeEntries accepts the canonical {"entries": [...]} document and, for
// forward/backward compatibility with hand-written files, a bare array.
func decodeEntries(data []byte) ([]contracts.Memory, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var entries []contracts.Memory
		if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
			return nil, err
		}
		return entries, nil
	}
	var document fileDocument
	if err := json.Unmarshal([]byte(trimmed), &document); err != nil {
		return nil, err
	}
	return document.Entries, nil
}

// RecoveryNotice returns the last recovery warning ("" when the store loaded
// healthy). It implements contracts.MemoryRecoveryReporter so the memory page
// can tell the user their previous entries were backed up.
func (s *Store) RecoveryNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recovery
}

// quarantine renames the unusable memory file out of the way and records the
// recovery notice; the original error is kept in the notice so the log is
// actionable. A rename failure only weakens the message: the store still starts
// empty either way.
func (s *Store) quarantine(reason string) {
	backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UTC().UnixNano())
	message := reason + "; starting with an empty memory store"
	if err := os.Rename(s.path, backup); err != nil {
		message += fmt.Sprintf(" (could not quarantine the file: %v)", err)
	} else {
		message += fmt.Sprintf(" (quarantined as %s)", backup)
	}
	s.noteRecovery(message)
}

// noteRecovery records and logs one recovery warning.
func (s *Store) noteRecovery(message string) {
	s.recovery = message
	if s.logger != nil {
		s.logger.Printf("%s: %s", ModuleID, message)
	}
}

// Version returns the successful-mutation counter (contracts.MemoryVersioner):
// the UI compares it per frame to notice entries written out of band.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// currentWorkspaceID returns the id of the workspace current at call time (""
// when no workspace is open or no workspace service is wired).
func (s *Store) currentWorkspaceID(ctx context.Context) string {
	if s.workspace == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workspace, ok, err := s.workspace.Current(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("%s: current workspace for memory scope: %v", ModuleID, err)
		}
		return ""
	}
	if !ok {
		return ""
	}
	return strings.TrimSpace(workspace.ID)
}

// visibleInWorkspace reports whether an entry belongs in the current
// workspace's view. Global entries and legacy/unbound scopes are always visible
// so upgrading never hides existing data; a bound workspace entry is only
// visible in its own workspace.
func visibleInWorkspace(scope, currentWorkspaceID string) bool {
	scope = strings.TrimSpace(scope)
	if workspaceID, ok := contracts.MemoryScopeWorkspaceID(scope); ok {
		return workspaceID == strings.TrimSpace(currentWorkspaceID)
	}
	// Global entries, the legacy bare "workspace" scope and historical custom
	// scopes cannot be attributed to one workspace, so they stay visible.
	return true
}

// List implements contracts.MemoryStore: the current workspace's view in
// insertion order (global + legacy entries + this workspace's entries).
func (s *Store) List(ctx context.Context) ([]contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	currentWorkspaceID := s.currentWorkspaceID(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	visible := make([]contracts.Memory, 0, len(s.entries))
	for _, memory := range s.entries {
		if visibleInWorkspace(memory.Scope, currentWorkspaceID) {
			visible = append(visible, memory)
		}
	}
	return visible, nil
}

// ListAll implements contracts.MemoryAllLister: every entry of every
// workspace, for the settings management page.
func (s *Store) ListAll(ctx context.Context) ([]contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneEntries(s.entries), nil
}

// Add implements contracts.MemoryStore. A missing ID becomes "mem-<unixnano>",
// a missing scope the configured default (a "workspace" default is bound to the
// current workspace), a missing Created now (UTC). An unknown scope is rejected
// with the same rule Update uses (M-04). A persistence failure rolls the
// in-memory list back; the automatic policy runs after a successful add.
func (s *Store) Add(ctx context.Context, memory contracts.Memory) (contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Memory{}, err
	}
	scope, err := s.normalizeScope(ctx, memory.Scope)
	if err != nil {
		return contracts.Memory{}, err
	}
	s.mu.Lock()
	if id := strings.TrimSpace(memory.ID); id == "" {
		memory.ID = s.newIDLocked()
	} else {
		memory.ID = id
	}
	memory.Scope = scope
	if memory.Created.IsZero() {
		memory.Created = time.Now().UTC()
	}

	previous := s.entries
	s.entries = append(previous, memory)
	if err := s.persistLocked(); err != nil {
		s.entries = previous
		s.mu.Unlock()
		return contracts.Memory{}, err
	}
	s.version++
	s.mu.Unlock()
	// Enforce the configured cap/retention on write as well as on open. The
	// new entry is the newest so a cap keeps it; only expired legacy entries
	// can be removed. Policy failures are logged, never fail the add.
	s.enforcePolicy(ctx)
	return memory, nil
}

// newIDLocked generates a unique "mem-<unixnano>" ID.
func (s *Store) newIDLocked() string {
	base := time.Now().UnixNano()
	for offset := int64(0); ; offset++ {
		id := fmt.Sprintf("mem-%d", base+offset)
		if !s.hasIDLocked(id) {
			return id
		}
		if offset > 1024 {
			return fmt.Sprintf("mem-%d-%d", base, len(s.entries))
		}
	}
}

// hasIDLocked reports whether an entry already uses id.
func (s *Store) hasIDLocked(id string) bool {
	for _, memory := range s.entries {
		if memory.ID == id {
			return true
		}
	}
	return false
}

// Update implements contracts.MemoryUpdater: it replaces one entry's text while
// keeping its ID, creation time and (unless a non-empty scope is passed) its
// scope. Unknown IDs, blank text and invalid scopes are reported without
// touching the document; a persistence failure rolls the in-memory entry back.
func (s *Store) Update(ctx context.Context, id, text, scope string) (contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Memory{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return contracts.Memory{}, fmt.Errorf("%s: update: id is required", ModuleID)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return contracts.Memory{}, fmt.Errorf("%s: update: text is required", ModuleID)
	}
	// An omitted scope keeps the entry's current scope; a provided one is
	// normalized exactly like a stored scope.
	normalizedScope := ""
	if strings.TrimSpace(scope) != "" {
		normalized, err := s.normalizeScope(ctx, scope)
		if err != nil {
			return contracts.Memory{}, err
		}
		normalizedScope = normalized
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for position, memory := range s.entries {
		if memory.ID == id {
			index = position
			break
		}
	}
	if index < 0 {
		return contracts.Memory{}, fmt.Errorf("%s: update: no memory with id %q", ModuleID, id)
	}
	updated := s.entries[index]
	updated.Text = text
	if normalizedScope != "" {
		updated.Scope = normalizedScope
	}
	previous := cloneEntries(s.entries)
	s.entries[index] = updated
	if err := s.persistLocked(); err != nil {
		s.entries = previous
		return contracts.Memory{}, err
	}
	s.version++
	return updated, nil
}

// Remove implements contracts.MemoryStore. It reports whether the entry existed
// and keeps the document unchanged when persistence fails.
func (s *Store) Remove(_ context.Context, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for position, memory := range s.entries {
		if memory.ID == id {
			index = position
			break
		}
	}
	if index < 0 {
		return false
	}
	previous := cloneEntries(s.entries)
	s.entries = append(append([]contracts.Memory(nil), s.entries[:index]...), s.entries[index+1:]...)
	if err := s.persistLocked(); err != nil {
		s.entries = previous
		return false
	}
	s.version++
	return true
}

// Consolidate implements contracts.MemoryConsolidator with the current clock.
// It always operates on every entry of every workspace (management semantics).
func (s *Store) Consolidate(ctx context.Context, policy contracts.MemoryPolicy) (contracts.MemoryConsolidationReport, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.MemoryConsolidationReport{}, err
	}
	return s.consolidate(policy, time.Now().UTC())
}

// consolidate runs one deterministic consolidation at now: first the expiry
// window (entries created strictly before now-MaxAge), then the entry cap
// (removing the oldest entries until MaxEntries remain). It persists once (or
// not at all) and returns the counts; a persistence failure leaves the store
// untouched and is reported.
func (s *Store) consolidate(policy contracts.MemoryPolicy, now time.Time) (contracts.MemoryConsolidationReport, error) {
	report := contracts.MemoryConsolidationReport{}

	s.mu.Lock()
	defer s.mu.Unlock()
	if policy.MaxAge <= 0 && policy.MaxEntries <= 0 {
		report.Remaining = len(s.entries)
		return report, nil
	}
	previous := cloneEntries(s.entries)
	keep := make([]bool, len(s.entries))
	for index := range keep {
		keep[index] = true
	}
	if policy.MaxAge > 0 {
		cutoff := now.Add(-policy.MaxAge)
		for index, memory := range s.entries {
			if memory.Created.IsZero() || !memory.Created.Before(cutoff) {
				continue
			}
			keep[index] = false
			report.RemovedExpired++
		}
	}
	if policy.MaxEntries > 0 {
		remaining := 0
		for index := range s.entries {
			if keep[index] {
				remaining++
			}
		}
		if remaining > policy.MaxEntries {
			order := make([]int, 0, remaining)
			for index := range s.entries {
				if keep[index] {
					order = append(order, index)
				}
			}
			sort.SliceStable(order, func(left, right int) bool {
				return s.entries[order[left]].Created.Before(s.entries[order[right]].Created)
			})
			for _, index := range order[:remaining-policy.MaxEntries] {
				keep[index] = false
				report.RemovedOverCap++
			}
		}
	}
	if report.RemovedExpired == 0 && report.RemovedOverCap == 0 {
		report.Remaining = len(s.entries)
		return report, nil
	}
	kept := make([]contracts.Memory, 0, len(s.entries))
	for index, memory := range s.entries {
		if keep[index] {
			kept = append(kept, memory)
		}
	}
	s.entries = kept
	if err := s.persistLocked(); err != nil {
		// Nothing was persisted, so report no removals either: the in-memory
		// store is rolled back and the caller only sees the failure.
		s.entries = previous
		report = contracts.MemoryConsolidationReport{Remaining: len(previous)}
		return report, fmt.Errorf("%s: consolidate: persist: %w", ModuleID, err)
	}
	s.version++
	report.Remaining = len(s.entries)
	return report, nil
}

// enforcePolicy applies the configured automatic policy and logs failures.
// It is a no-op when no policy is configured.
func (s *Store) enforcePolicy(ctx context.Context) {
	present := s.policy.MaxAge > 0 || s.policy.MaxEntries > 0
	if !present {
		return
	}
	report, err := s.Consolidate(ctx, s.policy)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("%s: automatic memory consolidation: %v", ModuleID, err)
		}
		return
	}
	if removed := report.RemovedExpired + report.RemovedOverCap; removed > 0 && s.logger != nil {
		s.logger.Printf("%s: automatic memory consolidation removed %d entr(ies) (expired %d, over cap %d, remaining %d)",
			ModuleID, removed, report.RemovedExpired, report.RemovedOverCap, report.Remaining)
	}
}

// Search implements contracts.MemoryStore: a case-insensitive substring match
// over the text of the current workspace's view; an empty query returns every
// visible entry.
func (s *Store) Search(ctx context.Context, query string) ([]contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	currentWorkspaceID := s.currentWorkspaceID(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	matches := make([]contracts.Memory, 0, len(s.entries))
	for _, memory := range s.entries {
		if !visibleInWorkspace(memory.Scope, currentWorkspaceID) {
			continue
		}
		if query == "" || strings.Contains(strings.ToLower(memory.Text), query) {
			matches = append(matches, memory)
		}
	}
	return matches, nil
}

// persistLocked writes the document atomically: a temporary file in the same
// directory is written, synced, closed and renamed over the target, then the
// directory is synced so the rename itself survives a power loss.
func (s *Store) persistLocked() error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, dirMode); err != nil {
		return fmt.Errorf("%s: create memory directory %q: %w", ModuleID, directory, err)
	}
	data, err := json.MarshalIndent(fileDocument{Entries: s.entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: encode memory document: %w", ModuleID, err)
	}
	temporary, err := os.CreateTemp(directory, ".memory-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: create temporary memory file: %w", ModuleID, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(fileMode); err != nil {
		cleanup()
		return fmt.Errorf("%s: chmod temporary memory file: %w", ModuleID, err)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("%s: write temporary memory file: %w", ModuleID, err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%s: sync temporary memory file: %w", ModuleID, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("%s: close temporary memory file: %w", ModuleID, err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("%s: replace memory file %q: %w", ModuleID, s.path, err)
	}
	syncDirectory(directory)
	return nil
}

// syncDirectory fsyncs a directory so a completed rename survives a power loss
// (best effort: some platforms/filesystems cannot sync directories).
func syncDirectory(directory string) {
	handle, err := os.Open(directory)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}

// cloneEntries returns a shallow copy of the entry slice (entries contain no
// references, so a slice copy is a deep copy for callers).
func cloneEntries(entries []contracts.Memory) []contracts.Memory {
	return append([]contracts.Memory(nil), entries...)
}

// contextErr returns ctx.Err(), tolerating a nil context.
func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
