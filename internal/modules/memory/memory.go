// Package memory implements ZeoKeel's durable memory backend module.
//
// It is a kernel backend module like the session store: it requires the product
// data directory and the host logger, loads a JSON memory file (creating an
// empty store when the file does not exist yet), provides the durable
// contracts.KeyMemory store and registers the Rig memory tool into the shared
// toolkit registry when that registry is already available.
//
// The store implements rig.MemoryStore (contracts.MemoryStore is its product
// alias) over a JSON document {"entries": [...]}: every mutation is written
// atomically (temporary file + rename in the same directory) so a crash cannot
// leave a half-written memory file. The module's activation owns the file
// handle-free store; persistence errors are returned to the caller.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigmemory "github.com/zeokeel/zeokeel-rig/tools/memory"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/memory"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DefaultFileName is the memory file name under the product data directory.
	DefaultFileName = "memory.json"
	// DefaultScope is the scope stored for entries that carry none.
	DefaultScope = "global"
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
	// Scope is the scope applied to memories added without one (default
	// "global"). Only "global" and "workspace" are accepted.
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
		store, err := newStore(dataDir, cfg)
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
	if _, err := normalizeScope(cfg.Scope); err != nil {
		return err
	}
	return nil
}

// registerMemoryTools registers the Rig memory tool bound to store into the
// shared toolkit registry and unregisters it again on module disposal. Errors
// (for example a duplicate name owned by the toolkit module) are logged and do
// not fail activation.
func registerMemoryTools(ctx cordis.Context, registry *rigtools.Registry, store contracts.MemoryStore, logger contracts.Logger) {
	for _, tool := range rigmemory.Tools(store) {
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

// normalizeScope canonicalizes and validates one scope.
func normalizeScope(scope string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(scope))
	if normalized == "" {
		return DefaultScope, nil
	}
	switch normalized {
	case DefaultScope, "workspace":
		return normalized, nil
	default:
		return "", fmt.Errorf("%s: invalid scope %q (want %q or %q)", ModuleID, scope, DefaultScope, "workspace")
	}
}

// --- store -----------------------------------------------------------------

// fileDocument is the on-disk memory document.
type fileDocument struct {
	Entries []contracts.Memory `json:"entries"`
}

// Store is a JSON-file backed contracts.MemoryStore. All methods are safe for
// concurrent use; each mutation persists the whole (small) document atomically.
type Store struct {
	mu           sync.Mutex
	path         string
	defaultScope string
	entries      []contracts.Memory
}

var _ contracts.MemoryStore = (*Store)(nil)

// newStore resolves the configured path and loads the existing document. A
// missing file starts an empty store; an unreadable or malformed file is a
// startup error (wrapped, never a panic).
func newStore(dataDir string, cfg Config) (*Store, error) {
	path, err := resolvePath(dataDir, cfg.Path)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeScope(cfg.Scope)
	if err != nil {
		return nil, err
	}
	store := &Store{path: path, defaultScope: scope}
	if err := store.load(); err != nil {
		return nil, err
	}
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

// load reads the document, treating a missing file as an empty store.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: read %q: %w", ModuleID, s.path, err)
	}
	entries, err := decodeEntries(data)
	if err != nil {
		return fmt.Errorf("%s: parse %q: %w", ModuleID, s.path, err)
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

// List implements contracts.MemoryStore in insertion order.
func (s *Store) List(ctx context.Context) ([]contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneEntries(s.entries), nil
}

// Add implements contracts.MemoryStore. A missing ID becomes
// "mem-<unixnano>", a missing scope the configured default and a missing
// Created now (UTC). A persistence failure rolls the in-memory list back and is
// returned to the caller.
func (s *Store) Add(ctx context.Context, memory contracts.Memory) (contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Memory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if id := strings.TrimSpace(memory.ID); id == "" {
		memory.ID = s.newIDLocked()
	} else {
		memory.ID = id
	}
	if scope := strings.TrimSpace(memory.Scope); scope == "" {
		memory.Scope = s.defaultScope
	} else {
		memory.Scope = scope
	}
	if memory.Created.IsZero() {
		memory.Created = time.Now().UTC()
	}

	previous := s.entries
	s.entries = append(previous, memory)
	if err := s.persistLocked(); err != nil {
		s.entries = previous
		return contracts.Memory{}, err
	}
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
	return true
}

// Search implements contracts.MemoryStore: a case-insensitive substring match
// over the text; an empty query returns every entry.
func (s *Store) Search(ctx context.Context, query string) ([]contracts.Memory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	s.mu.Lock()
	defer s.mu.Unlock()
	if query == "" {
		return cloneEntries(s.entries), nil
	}
	matches := make([]contracts.Memory, 0, len(s.entries))
	for _, memory := range s.entries {
		if strings.Contains(strings.ToLower(memory.Text), query) {
			matches = append(matches, memory)
		}
	}
	return matches, nil
}

// persistLocked writes the document atomically: a temporary file in the same
// directory is written, synced, closed and renamed over the target.
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
	return nil
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
