package users_test

// Module-level tests: the descriptor seam plus real activation through the
// kernel, proving the roster is opened under the product data directory, that a
// member survives a re-activation, and that a corrupt document is quarantined
// instead of bricking the product.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/users"
)

func TestDescriptorMetadata(t *testing.T) {
	descriptor := users.Module()
	if descriptor.ID != users.ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, users.ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyUsers.Name() {
		t.Fatalf("provides = %+v, want only the users key", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
		if requirement.Optional {
			t.Fatalf("requirement %q is optional, want required", requirement.Name)
		}
	}
	if len(required) != 1 || !required[contracts.KeyDataDir.Name()] {
		t.Fatalf("requires = %+v, want only the data dir", descriptor.Requires)
	}
	if descriptor.Name == "" {
		t.Fatal("descriptor has no display name")
	}
}

type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, format)
	l.mu.Unlock()
}

func (l *testLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// loadUsersModule activates the descriptor over dataDir and returns the roster
// the module provided.
func loadUsersModule(t *testing.T, dataDir string) (contracts.UserService, *testLogger) {
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
	// The host provides the data dir and the logger; this test is about the
	// roster, not about loading the process host's modules.
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(users.Module()); err != nil {
		t.Fatalf("register users module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "users", Module: users.ModuleID}})
	if err != nil {
		t.Fatalf("load users module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	instance, ok := loader.Instance("users")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("users instance = (%v, %v), want active", ok, instance.State())
	}
	store, ok := root.Get(contracts.KeyUsers)
	if !ok {
		t.Fatal("contracts.KeyUsers was not provided by the module")
	}
	return store, logger
}

func TestModuleProvidesAWorkingStoreUnderTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	store, _ := loadUsersModule(t, dataDir)
	ctx := context.Background()

	// A fresh product knows its account holder before anyone edits the roster.
	members, err := store.Users(ctx)
	if err != nil {
		t.Fatalf("Users on a fresh store: %v", err)
	}
	if len(members) != 1 || members[0].ID != users.SeedUserID || members[0].Role != contracts.UserRoleOwner {
		t.Fatalf("Users on a fresh store = %+v, want just the seeded owner", members)
	}

	if _, err := store.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleManager}); err != nil {
		t.Fatalf("Put through the provided store: %v", err)
	}
	path := filepath.Join(dataDir, users.DataSubdir, users.FileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("user store not written to %s: %v", path, err)
	}

	// A second activation over the same data dir must see the member: that is
	// what "restarts still know who is who" means.
	store2, _ := loadUsersModule(t, dataDir)
	got, err := store2.Get(ctx, "alice")
	if err != nil {
		t.Fatalf("Get after re-activation: %v", err)
	}
	if got.Name != "Alice" || got.Role != contracts.UserRoleManager {
		t.Fatalf("Get after re-activation = %+v, want the stored member", got)
	}
	owner, err := store2.Get(ctx, users.SeedUserID)
	if err != nil {
		t.Fatalf("seeded owner after re-activation: %v", err)
	}
	if owner.Role != contracts.UserRoleOwner {
		t.Fatalf("seeded owner role after re-activation = %q, want owner", owner.Role)
	}
}

func TestModuleQuarantinesACorruptStoreAndStaysActive(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, users.DataSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, users.FileName)
	corrupt := []byte(`{"version":1,"users":[`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt store: %v", err)
	}

	store, logger := loadUsersModule(t, dataDir)
	ctx := context.Background()
	members, err := store.Users(ctx)
	if err != nil {
		t.Fatalf("Users on a quarantined store: %v", err)
	}
	if len(members) != 1 || members[0].ID != users.SeedUserID {
		t.Fatalf("Users on a quarantined store = %+v, want just the seeded owner", members)
	}
	if !strings.Contains(logger.joined(), "corrupt") {
		t.Fatalf("the module did not report the corrupt store: %q", logger.joined())
	}

	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatalf("glob quarantine: %v", err)
	}
	if len(quarantined) != 1 {
		t.Fatalf("quarantined files = %v, want exactly one %s.corrupt-*", quarantined, users.FileName)
	}
	moved, err := os.ReadFile(quarantined[0])
	if err != nil {
		t.Fatalf("read quarantined file: %v", err)
	}
	if string(moved) != string(corrupt) {
		t.Fatalf("quarantined bytes = %q, want the original %q (evidence must survive)", moved, corrupt)
	}

	// The seeded owner is still there, so the product can be administered again.
	if _, err := store.Put(ctx, contracts.User{ID: "alice", Name: "Alice", Role: contracts.UserRoleCollaborator}); err != nil {
		t.Fatalf("Put after quarantine: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a new store file was not written: %v", err)
	}
}

func TestModuleFailsWhenTheDataDirIsUnusable(t *testing.T) {
	dataDir := t.TempDir()
	// A regular file where the store directory should be: the module must fail
	// loudly instead of providing a roster that silently drops members.
	blocked := filepath.Join(dataDir, users.DataSubdir)
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}

	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	if _, err := root.Provide(contracts.KeyDataDir, dataDir); err != nil {
		t.Fatalf("provide data dir: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir)
	if err := registry.Register(users.Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := module.Load(root, registry, []module.Entry{{ID: "users", Module: users.ModuleID}}); err == nil {
		t.Fatal("module.Load succeeded with an unusable data dir")
	}
	if _, ok := root.Get(contracts.KeyUsers); ok {
		t.Fatal("the failed module still provided contracts.KeyUsers")
	}
}
