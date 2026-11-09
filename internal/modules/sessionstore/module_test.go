package sessionstore

// Tests for the kernel module: descriptor metadata, activation over the kernel
// service graph, config handling and persistence across restarts.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"

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

// loadStoreModule loads the sessionstore module on a fresh kernel root and
// returns the provided store.
func loadStoreModule(t *testing.T, dataDir string, config map[string]any) (cordis.Context, *module.Loader, contracts.Store, *testLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	logger := &testLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	// The process host provides the data dir and logger at the kernel root;
	// declaring them as builtins lets pre-flight validation see them.
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register sessionstore module: %v", err)
	}
	entry := module.Entry{ID: "store", Module: ModuleID}
	if config != nil {
		entry.Config = config
	}
	loader, err := module.Load(root, registry, []module.Entry{entry})
	if err != nil {
		t.Fatalf("load sessionstore module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	service, ok := root.Get(contracts.KeyStore)
	if !ok {
		t.Fatal("contracts.KeyStore was not provided by the module")
	}
	return root, loader, service, logger
}

func TestModuleDescriptor(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if descriptor.Name == "" {
		t.Fatal("module has no display name")
	}
	if descriptor.Version == "" {
		t.Fatal("module has no version")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyStore.Name() {
		t.Fatalf("provides = %+v, want the store key", descriptor.Provides)
	}
	required := map[string]bool{}
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, want := range []string{contracts.KeyDataDir.Name(), contracts.KeyLogger.Name()} {
		if !required[want] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, want)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
}

func TestModuleProvidesWorkingStore(t *testing.T) {
	dataDir := t.TempDir()
	_, loader, service, logger := loadStoreModule(t, dataDir, nil)
	ctx := context.Background()

	instance, ok := loader.Instance("store")
	if !ok {
		t.Fatal("store instance not found in loader")
	}
	if instance.ModuleID() != ModuleID {
		t.Fatalf("instance module ID = %q", instance.ModuleID())
	}
	if state := instance.State().String(); state != "active" {
		t.Fatalf("instance state = %q, want active", state)
	}

	if err := service.CreateSession(ctx, "s1", "Module session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := service.History().Append(ctx, "s1", rig.NewMessage(rig.RoleUser, "persisted message")); err != nil {
		t.Fatalf("History.Append: %v", err)
	}
	meta, found, err := service.GetSessionMeta(ctx, "s1")
	if err != nil || !found || meta.MessageCount != 1 {
		t.Fatalf("GetSessionMeta = %+v found=%v err=%v", meta, found, err)
	}
	messages, err := service.History().Load(ctx, "s1")
	if err != nil || len(messages) != 1 {
		t.Fatalf("History.Load = %d messages, %v", len(messages), err)
	}
	if !strings.Contains(strings.Join(logger.lines, "\n"), "opened database") {
		t.Fatalf("logger did not record the open: %v", logger.lines)
	}
	if _, err := os.Stat(filepath.Join(dataDir, DBFileName)); err != nil {
		t.Fatalf("database file missing: %v", err)
	}
}

func TestModulePathOverride(t *testing.T) {
	dataDir := t.TempDir()
	_, _, service, _ := loadStoreModule(t, dataDir, map[string]any{
		"path":          "custom/store.db",
		"busyTimeoutMs": 250,
	})
	if _, err := os.Stat(filepath.Join(dataDir, "custom", "store.db")); err != nil {
		t.Fatalf("custom database path was not used: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, DBFileName)); !os.IsNotExist(err) {
		t.Fatalf("default database should not exist when path is overridden: %v", err)
	}
	if err := service.CreateSession(context.Background(), "s1", "custom"); err != nil {
		t.Fatalf("CreateSession on custom database: %v", err)
	}
}

func TestModuleInvalidConfig(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeyDataDir, t.TempDir()); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(&testLogger{})); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := module.Load(root, registry, []module.Entry{{
		ID: "store", Module: ModuleID,
		Config: map[string]any{"busyTimeoutMs": -1},
	}})
	if err == nil || !strings.Contains(err.Error(), "busyTimeoutMs") {
		t.Fatalf("invalid config load error = %v, want a busyTimeoutMs validation error", err)
	}
}

func TestModuleMissingDataDirFails(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(&testLogger{})); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "store", Module: ModuleID}})
	if loader != nil {
		_ = loader.Close()
	}
	if err == nil {
		t.Fatal("module loaded without the data directory service")
	}
	if !strings.Contains(err.Error(), contracts.KeyDataDir.Name()) &&
		!strings.Contains(err.Error(), "data dir") {
		t.Fatalf("missing data dir error = %v, want it to mention the data directory", err)
	}
}

func TestModuleClosesDatabaseOnUnload(t *testing.T) {
	dataDir := t.TempDir()
	root, loader, service, _ := loadStoreModule(t, dataDir, nil)
	ctx := context.Background()
	if err := service.CreateSession(ctx, "s1", "before close"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	// The service value is stale after disposal: the database was closed, so
	// calls must fail instead of silently using a closed handle.
	if err := service.CreateSession(ctx, "s2", "after close"); err == nil {
		t.Fatal("store still works after module unload; the database was not closed")
	}
	if _, ok := root.Get(contracts.KeyStore); ok {
		t.Fatal("store service should be removed when the module unloads")
	}
}

func TestModulePersistenceAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()

	_, firstLoader, first, _ := loadStoreModule(t, dataDir, nil)
	if err := first.CreateSession(ctx, "s1", "persisted session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := first.History().Append(ctx, "s1", rig.NewMessage(rig.RoleUser, "persisted message")); err != nil {
		t.Fatalf("History.Append: %v", err)
	}
	if err := first.AppendEvent(ctx, rig.SessionEvent{SessionID: "s1", Type: "user", Payload: []byte(`{"kind":"module-test"}`)}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := firstLoader.Close(); err != nil {
		t.Fatalf("close first loader: %v", err)
	}

	_, secondLoader, second, _ := loadStoreModule(t, dataDir, nil)
	_ = secondLoader
	meta, found, err := second.GetSessionMeta(ctx, "s1")
	if err != nil || !found || meta.Title != "persisted session" || meta.MessageCount != 1 {
		t.Fatalf("reopened meta = %+v found=%v err=%v", meta, found, err)
	}
	messages, err := second.History().Load(ctx, "s1")
	if err != nil || len(messages) != 1 || messages[0].Text() != "persisted message" {
		t.Fatalf("reopened history = %+v, %v", messages, err)
	}
	events, err := second.Events(ctx, "s1", 0, 0)
	if err != nil || len(events) != 1 || events[0].Type != "user" {
		t.Fatalf("reopened events = %+v, %v", events, err)
	}
}
