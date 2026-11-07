package memory

// Tests for the durable memory module: activation over the kernel service
// graph, persistence across restarts, the store semantics and the optional
// toolkit tool registration.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigmemory "github.com/zeokeel/zeokeel-rig/tools/memory"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// testLogger is a goroutine-safe contracts.Logger test double.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *testLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// harness is one loaded memory module.
type harness struct {
	ctx    cordis.Context
	loader *module.Loader
	store  contracts.MemoryStore
	logger *testLogger

	closeOnce sync.Once
}

func (h *harness) close() {
	h.closeOnce.Do(func() {
		_ = h.loader.Close()
		_ = h.ctx.DisposeAll()
	})
}

// loadMemory loads the memory module on a fresh kernel root. prepare may
// provide additional host services (for example KeyToolkit) before loading.
func loadMemory(t *testing.T, dataDir string, cfg map[string]any, prepare func(ctx cordis.Context)) (*harness, error) {
	t.Helper()
	root := cordis.Root()
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		root.DisposeAll()
		return nil, fmt.Errorf("provide data dir: %w", err)
	}
	logger := &testLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		root.DisposeAll()
		return nil, fmt.Errorf("provide logger: %w", err)
	}
	if prepare != nil {
		prepare(root)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		root.DisposeAll()
		return nil, fmt.Errorf("register memory module: %w", err)
	}
	entry := module.Entry{ID: "memory", Module: ModuleID}
	if cfg != nil {
		entry.Config = cfg
	}
	loader, err := module.Load(root, registry, []module.Entry{entry})
	if err != nil {
		_ = root.DisposeAll()
		return nil, err
	}
	store, ok := root.Get(contracts.KeyMemory)
	if !ok {
		_ = loader.Close()
		_ = root.DisposeAll()
		return nil, fmt.Errorf("contracts.KeyMemory was not provided")
	}
	h := &harness{ctx: root, loader: loader, store: store, logger: logger}
	t.Cleanup(h.close)
	return h, nil
}

func mustLoadMemory(t *testing.T, dataDir string, cfg map[string]any, prepare func(ctx cordis.Context)) *harness {
	t.Helper()
	h, err := loadMemory(t, dataDir, cfg, prepare)
	if err != nil {
		t.Fatalf("load memory module: %v", err)
	}
	return h
}

// memoryConfig converts a typed Config into the loader's JSON-like map.
func memoryConfig(cfg Config) map[string]any {
	out := map[string]any{}
	if cfg.Path != "" {
		out["path"] = cfg.Path
	}
	if cfg.Scope != "" {
		out["scope"] = cfg.Scope
	}
	return out
}

// TestModuleDescriptorAndValidation pins the descriptor and the config hook.
func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/memory" {
		t.Fatalf("module ID = %q, want zeokeel/memory", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyMemory.Name() {
		t.Fatalf("provides = %+v, want KeyMemory", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{contracts.KeyDataDir.Name(), contracts.KeyLogger.Name()} {
		if !required[name] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, name)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	for _, cfg := range []Config{{}, {Scope: "global"}, {Scope: " WORKSPACE "}, {Path: "nested/memory.json"}} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v, want nil", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{Path: "bad\x00path"}); err == nil {
		t.Fatal("Validate accepted a NUL path")
	}
	if err := descriptor.Validate(Config{Scope: "team"}); err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Fatalf("Validate(invalid scope) = %v, want a readable scope error", err)
	}
}

// TestModuleProvidesDurableStore covers the default document, its JSON shape
// and the store semantics through the provided service.
func TestModuleProvidesDurableStore(t *testing.T) {
	dataDir := t.TempDir()
	h := mustLoadMemory(t, dataDir, nil, nil)
	ctx := context.Background()

	if got, err := h.store.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh store = %+v/%v, want empty", got, err)
	}
	added, err := h.store.Add(ctx, contracts.Memory{Text: "Release branch is release/2026"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !strings.HasPrefix(added.ID, "mem-") || added.Scope != DefaultScope || added.Created.IsZero() {
		t.Fatalf("added memory = %+v, want generated id, global scope and created time", added)
	}
	scoped, err := h.store.Add(ctx, contracts.Memory{Text: "Prefer concise answers", Scope: "workspace"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if scoped.Scope != "workspace" {
		t.Fatalf("explicit scope = %q, want workspace", scoped.Scope)
	}

	list, err := h.store.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != added.ID || list[1].ID != scoped.ID {
		t.Fatalf("List = %+v/%v, want insertion order", list, err)
	}
	// Returned slices are copies.
	list[0].Text = "mutated"
	again, _ := h.store.List(ctx)
	if again[0].Text != "Release branch is release/2026" {
		t.Fatalf("List returned aliased entries: %+v", again[0])
	}

	matches, err := h.store.Search(ctx, "RELEASE")
	if err != nil || len(matches) != 1 || matches[0].ID != added.ID {
		t.Fatalf("Search(RELEASE) = %+v/%v", matches, err)
	}
	all, err := h.store.Search(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("Search(empty) = %d/%v, want all", len(all), err)
	}

	if !h.store.Remove(ctx, added.ID) {
		t.Fatal("Remove(existing) = false")
	}
	if h.store.Remove(ctx, added.ID) {
		t.Fatal("second Remove(existing) = true")
	}

	// The file is the documented {"entries": [...]} document under the data
	// directory.
	path := filepath.Join(dataDir, DefaultFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	var document fileDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("memory file is not the documented document: %v\n%s", err, raw)
	}
	if len(document.Entries) != 1 || document.Entries[0].ID != scoped.ID {
		t.Fatalf("file entries = %+v, want the remaining entry", document.Entries)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat memory file: %v", err)
	}
	if info.Mode().Perm() != fileMode {
		t.Fatalf("memory file mode = %o, want %o", info.Mode().Perm(), fileMode)
	}
}

// TestModulePersistenceAcrossRestart covers loading the same data directory
// again after the module unloads.
func TestModulePersistenceAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	first := mustLoadMemory(t, dataDir, nil, nil)
	ctx := context.Background()
	added, err := first.store.Add(ctx, contracts.Memory{Text: "survives restart", Scope: "global"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	first.close()

	second := mustLoadMemory(t, dataDir, nil, nil)
	list, err := second.store.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != added.ID || list[0].Text != "survives restart" {
		t.Fatalf("reloaded store = %+v/%v, want the persisted entry", list, err)
	}
	if !list[0].Created.Equal(added.Created) {
		t.Fatalf("created time = %v, want %v", list[0].Created, added.Created)
	}
}

// TestModuleCorruptFileFailsActivation covers a malformed memory file: the
// module reports an error instead of panicking.
func TestModuleCorruptFileFailsActivation(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, DefaultFileName), []byte(`{"entries": [not json`), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if _, err := loadMemory(t, dataDir, nil, nil); err == nil {
		t.Fatal("module loaded with a corrupt memory file")
	} else if !strings.Contains(err.Error(), "parse") && !strings.Contains(err.Error(), "memory") {
		t.Fatalf("corrupt file error = %v, want a readable parse error", err)
	}
}

// TestModuleCustomPathAndScope covers Config.Path (relative to dataDir) and the
// default scope for new entries.
func TestModuleCustomPathAndScope(t *testing.T) {
	dataDir := t.TempDir()
	h := mustLoadMemory(t, dataDir, memoryConfig(Config{Path: "nested/memories.json", Scope: "workspace"}), nil)
	ctx := context.Background()
	added, err := h.store.Add(ctx, contracts.Memory{Text: "workspace note"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Scope != "workspace" {
		t.Fatalf("scope = %q, want the configured workspace", added.Scope)
	}
	custom, err := h.store.Add(ctx, contracts.Memory{Text: "global note", Scope: "global", ID: "custom-id"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if custom.ID != "custom-id" || custom.Scope != "global" {
		t.Fatalf("explicit fields = %+v, want preserved", custom)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "nested", "memories.json"))
	if err != nil {
		t.Fatalf("read custom memory file: %v", err)
	}
	var document fileDocument
	if err := json.Unmarshal(raw, &document); err != nil || len(document.Entries) != 2 {
		t.Fatalf("custom file entries = %d/%v", len(document.Entries), err)
	}
}

// TestToolkitToolRegistration covers registering the Rig memory tool into the
// shared registry and unregistering it on disposal.
func TestToolkitToolRegistration(t *testing.T) {
	dataDir := t.TempDir()
	registry := rigtools.NewRegistry()
	h := mustLoadMemory(t, dataDir, nil, func(ctx cordis.Context) {
		if _, err := ctx.Provide(contracts.KeyToolkit, registry); err != nil {
			t.Fatalf("provide toolkit: %v", err)
		}
	})
	if registry.Len() != 1 {
		t.Fatalf("toolkit tools = %d, want the registered memory tool", registry.Len())
	}
	if names := toolNames(registry.Tools()); len(names) != 1 || names[0] != rigmemory.ToolName {
		t.Fatalf("toolkit names = %v, want [%s]", names, rigmemory.ToolName)
	}

	result, err := registry.Invoke(context.Background(), rig.ToolCall{
		ID:        "call-1",
		Name:      rigmemory.ToolName,
		Arguments: `{"action":"add","text":"durable through the tool"}`,
	})
	if err != nil {
		t.Fatalf("Invoke memory tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("memory tool error: %s", result.Content)
	}
	list, err := h.store.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Text != "durable through the tool" {
		t.Fatalf("store after tool add = %+v/%v", list, err)
	}

	h.close()
	if registry.Len() != 0 {
		t.Fatalf("toolkit tools after disposal = %d, want the registered tool removed", registry.Len())
	}
}

// TestToolkitDuplicateIsLoggedAndTolerated covers the real product order: the
// toolkit module already registered its process-local memory tool, so the
// durable tool registration is a logged no-op and activation still succeeds.
func TestToolkitDuplicateIsLoggedAndTolerated(t *testing.T) {
	dataDir := t.TempDir()
	existing := rigmemory.Tools(nil)[0]
	registry := rigtools.NewRegistry(existing)
	h, err := loadMemory(t, dataDir, nil, func(ctx cordis.Context) {
		if _, err := ctx.Provide(contracts.KeyToolkit, registry); err != nil {
			t.Fatalf("provide toolkit: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("module activation with a duplicate tool failed: %v", err)
	}
	if registry.Len() != 1 {
		t.Fatalf("toolkit tools = %d, want the pre-existing tool only", registry.Len())
	}
	if !h.logger.contains("duplicate tool name") {
		t.Fatalf("duplicate registration was not logged: %v", h.logger.lines)
	}
	// The module still provides a working durable store.
	if _, err := h.store.Add(context.Background(), contracts.Memory{Text: "still durable"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	h.close()
	if registry.Len() != 1 {
		t.Fatalf("disposal removed a tool it did not register: %d tools", registry.Len())
	}
}

// toolNames extracts registry tool names for assertions.
func toolNames(tools []rig.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

// TestStoreSemanticsDirect covers the store API directly, including context
// cancellation and the bare-array document compatibility.
func TestStoreSemanticsDirect(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "memory.json")
	store, err := newStore(directory, Config{Path: path, Scope: "workspace"})
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	ctx := context.Background()
	added, err := store.Add(ctx, contracts.Memory{Text: "hello"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Scope != "workspace" {
		t.Fatalf("scope = %q", added.Scope)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.List(cancelled); err == nil {
		t.Fatal("List(cancelled ctx) returned nil error")
	}
	if _, err := store.Search(cancelled, ""); err == nil {
		t.Fatal("Search(cancelled ctx) returned nil error")
	}
	if _, err := store.Add(cancelled, contracts.Memory{Text: "x"}); err == nil {
		t.Fatal("Add(cancelled ctx) returned nil error")
	}
	// A nil context is tolerated, like the rig memory store.
	if _, err := store.List(nil); err != nil {
		t.Fatalf("List(nil) = %v", err)
	}

	// A bare array document loads too.
	if err := os.WriteFile(path, []byte(`[{"id":"legacy","text":"old","scope":"global"}]`), 0o600); err != nil {
		t.Fatalf("write bare array: %v", err)
	}
	legacy, err := newStore(directory, Config{Path: path})
	if err != nil {
		t.Fatalf("newStore(bare array): %v", err)
	}
	if list, _ := legacy.List(ctx); len(list) != 1 || list[0].ID != "legacy" {
		t.Fatalf("bare array store = %+v", list)
	}
}

// TestStoreConcurrency covers concurrent mutations and reads under -race.
func TestStoreConcurrency(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(directory, Config{})
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	const workers = 32
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(number int) {
			defer waitGroup.Done()
			if _, err := store.Add(context.Background(), contracts.Memory{
				Text: fmt.Sprintf("note-%d", number),
			}); err != nil {
				errs <- err
				return
			}
			_, _ = store.Search(context.Background(), "note")
			_, _ = store.List(context.Background())
		}(index)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	list, err := store.List(context.Background())
	if err != nil || len(list) != workers {
		t.Fatalf("List = %d/%v, want %d", len(list), err, workers)
	}
	ids := make(map[string]bool, workers)
	for _, memory := range list {
		if ids[memory.ID] {
			t.Fatalf("duplicate generated ID %q", memory.ID)
		}
		ids[memory.ID] = true
	}
	reloaded, err := newStore(directory, Config{})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if entries, _ := reloaded.List(context.Background()); len(entries) != workers {
		t.Fatalf("reloaded entries = %d, want %d", len(entries), workers)
	}
}

// TestStoreWriteFailureRollsBack covers persistence failures.
func TestStoreWriteFailureRollsBack(t *testing.T) {
	directory := t.TempDir()
	store, err := newStore(directory, Config{})
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	ctx := context.Background()
	first, err := store.Add(ctx, contracts.Memory{Text: "first"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Replace the file with a directory: renaming the temporary file over it
	// fails, exercising the rollback path without relying on permissions (the
	// tests may run as root).
	if err := os.Remove(store.Path()); err != nil {
		t.Fatalf("remove memory file: %v", err)
	}
	if err := os.Mkdir(store.Path(), 0o700); err != nil {
		t.Fatalf("replace memory file with directory: %v", err)
	}
	if _, err := store.Add(ctx, contracts.Memory{Text: "second"}); err == nil {
		t.Fatal("Add with an unwritable target returned nil error")
	}
	list, err := store.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("store after failed Add = %+v/%v, want only the first entry", list, err)
	}
	if store.Remove(ctx, first.ID) {
		t.Fatal("Remove with an unwritable target reported success")
	}
	if list, _ := store.List(ctx); len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("store after failed Remove = %+v, want the entry kept", list)
	}
}

// TestStoreInvalidDocument covers malformed JSON and unknown scope configs.
func TestStoreInvalidDocument(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "memory.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("write invalid document: %v", err)
	}
	if _, err := newStore(directory, Config{Path: path}); err == nil {
		t.Fatal("newStore accepted a malformed document")
	}
	if _, err := newStore(directory, Config{Scope: "team"}); err == nil {
		t.Fatal("newStore accepted an invalid scope")
	}
	if _, err := newStore("", Config{Path: "relative.json"}); err == nil {
		t.Fatal("newStore accepted an empty data directory for a relative path")
	}
}
