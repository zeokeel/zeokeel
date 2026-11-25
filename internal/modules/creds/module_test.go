package creds_test

// Module-level tests: the descriptor seam and real activation through the
// kernel, proving the store is opened under the product data directory and that
// a corrupt file never bricks the product.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/creds"
)

func TestDescriptorMetadata(t *testing.T) {
	descriptor := creds.Module()
	if descriptor.ID != creds.ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, creds.ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyCredentials.Name() {
		t.Fatalf("provides = %+v, want only the credentials key", descriptor.Provides)
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

// loadCredsModule activates the descriptor over dataDir and returns the store
// the module provided.
func loadCredsModule(t *testing.T, dataDir string) (contracts.CredentialStore, *testLogger) {
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
	// credential store, not about loading the process host's modules.
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(creds.Module()); err != nil {
		t.Fatalf("register creds module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "creds", Module: creds.ModuleID}})
	if err != nil {
		t.Fatalf("load creds module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	instance, ok := loader.Instance("creds")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("creds instance = (%v, %v), want active", ok, instance.State())
	}
	store, ok := root.Get(contracts.KeyCredentials)
	if !ok {
		t.Fatal("contracts.KeyCredentials was not provided by the module")
	}
	return store, logger
}

func TestModuleProvidesAWorkingStoreUnderTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	store, _ := loadCredsModule(t, dataDir)
	ctx := context.Background()

	if err := store.Set(ctx, contracts.Credential{Kind: "feishu", ID: "default", Fields: map[string]string{"app_secret": "from-module"}}); err != nil {
		t.Fatalf("Set through the provided store: %v", err)
	}
	path := filepath.Join(dataDir, creds.DataSubdir, creds.FileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("credentials file not written to %s: %v", path, err)
	}

	// The store must be the same one the module opened: a second activation over
	// the same data dir sees the credential.
	store2, _ := loadCredsModule(t, dataDir)
	got, found, err := store2.Get(ctx, "feishu", "default")
	if err != nil || !found {
		t.Fatalf("Get after re-activation = (found %v, err %v), want (true, nil)", found, err)
	}
	if got.Fields["app_secret"] != "from-module" {
		t.Fatalf("Get after re-activation = %+v, want the stored secret", got)
	}
}

func TestModuleQuarantinesACorruptStoreAndStaysActive(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, creds.DataSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, creds.FileName)
	corrupt := []byte(`{"version":1,"credentials":`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt store: %v", err)
	}

	store, logger := loadCredsModule(t, dataDir)
	ctx := context.Background()
	if infos, err := store.List(ctx, "feishu"); err != nil || len(infos) != 0 {
		t.Fatalf("List on a quarantined store = (%+v, %v), want empty", infos, err)
	}
	if !strings.Contains(logger.joined(), "corrupt") {
		t.Fatalf("the module did not report the corrupt store: %q", logger.joined())
	}

	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatalf("glob quarantine: %v", err)
	}
	if len(quarantined) != 1 {
		t.Fatalf("quarantined files = %v, want exactly one %s.corrupt-*", quarantined, creds.FileName)
	}
	moved, err := os.ReadFile(quarantined[0])
	if err != nil {
		t.Fatalf("read quarantined file: %v", err)
	}
	if string(moved) != string(corrupt) {
		t.Fatalf("quarantined bytes = %q, want the original %q (evidence must survive)", moved, corrupt)
	}

	// And the fresh store must persist normally.
	if err := store.Set(ctx, contracts.Credential{Kind: "feishu", ID: "default", Fields: map[string]string{"app_secret": "fresh"}}); err != nil {
		t.Fatalf("Set after quarantine: %v", err)
	}
	if got, found, err := store.Get(ctx, "feishu", "default"); err != nil || !found || got.Fields["app_secret"] != "fresh" {
		t.Fatalf("Get after quarantine = (%+v, %v, %v)", got, found, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a new store file was not written: %v", err)
	}
}

func TestModuleFailsWhenTheDataDirIsUnusable(t *testing.T) {
	dataDir := t.TempDir()
	// A regular file where the store directory should be: the module must fail
	// loudly instead of providing a store that silently drops secrets.
	blocked := filepath.Join(dataDir, creds.DataSubdir)
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
	if err := registry.Register(creds.Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := module.Load(root, registry, []module.Entry{{ID: "creds", Module: creds.ModuleID}})
	if err == nil {
		t.Fatal("loading the module over an unusable data dir succeeded, want an error")
	}
	if errors.Is(err, contracts.ErrStoreCorrupt) {
		t.Fatalf("load error = %v, want an I/O error (the store is not corrupt, it is unusable)", err)
	}
}
