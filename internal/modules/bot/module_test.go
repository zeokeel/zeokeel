package bot_test

// Module-level tests: the descriptor seam plus real activation through the
// kernel, proving the store is opened under the product data directory, that a
// bot survives a re-activation, and that a corrupt document is quarantined
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
	"github.com/zeokeel/zeokeel/internal/modules/bot"
)

func TestDescriptorMetadata(t *testing.T) {
	descriptor := bot.Module()
	if descriptor.ID != bot.ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, bot.ModuleID)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyBots.Name() {
		t.Fatalf("provides = %+v, want only the bots key", descriptor.Provides)
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

// loadBotModule activates the descriptor over dataDir and returns the store the
// module provided.
func loadBotModule(t *testing.T, dataDir string) (contracts.BotService, *testLogger) {
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
	// The host provides the data dir and the logger; this test is about the bot
	// store, not about loading the process host's modules.
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(bot.Module()); err != nil {
		t.Fatalf("register bot module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "bots", Module: bot.ModuleID}})
	if err != nil {
		t.Fatalf("load bot module: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	instance, ok := loader.Instance("bots")
	if !ok || instance.State() != cordis.Active {
		t.Fatalf("bots instance = (%v, %v), want active", ok, instance.State())
	}
	store, ok := root.Get(contracts.KeyBots)
	if !ok {
		t.Fatal("contracts.KeyBots was not provided by the module")
	}
	return store, logger
}

func TestModuleProvidesAWorkingStoreUnderTheDataDir(t *testing.T) {
	dataDir := t.TempDir()
	store, _ := loadBotModule(t, dataDir)
	ctx := context.Background()

	created, err := store.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: bot.LeadTemplateID, Owner: "alice"})
	if err != nil {
		t.Fatalf("Create through the provided store: %v", err)
	}
	if created.Status != contracts.BotStatusActive {
		t.Fatalf("status = %q, want active", created.Status)
	}
	path := filepath.Join(dataDir, bot.DataSubdir, bot.FileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bot store not written to %s: %v", path, err)
	}

	// A second activation over the same data dir must see the bot: that is what
	// "restarts still there" means.
	store2, _ := loadBotModule(t, dataDir)
	got, err := store2.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get after re-activation: %v", err)
	}
	if got.Owner != "alice" || got.TemplateID != bot.LeadTemplateID {
		t.Fatalf("Get after re-activation = %+v, want the stored bot", got)
	}
	templates, err := store2.Templates(ctx)
	if err != nil {
		t.Fatalf("Templates after re-activation: %v", err)
	}
	if len(templates) != 1 || templates[0].ID != bot.LeadTemplateID {
		t.Fatalf("Templates after re-activation = %+v, want the built-in lead", templates)
	}
}

func TestModuleQuarantinesACorruptStoreAndStaysActive(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, bot.DataSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, bot.FileName)
	corrupt := []byte(`{"version":1,"templates":[`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatalf("seed corrupt store: %v", err)
	}

	store, logger := loadBotModule(t, dataDir)
	ctx := context.Background()
	if instances, err := store.Instances(ctx); err != nil || len(instances) != 0 {
		t.Fatalf("Instances on a quarantined store = (%+v, %v), want empty", instances, err)
	}
	if !strings.Contains(logger.joined(), "corrupt") {
		t.Fatalf("the module did not report the corrupt store: %q", logger.joined())
	}

	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatalf("glob quarantine: %v", err)
	}
	if len(quarantined) != 1 {
		t.Fatalf("quarantined files = %v, want exactly one %s.corrupt-*", quarantined, bot.FileName)
	}
	moved, err := os.ReadFile(quarantined[0])
	if err != nil {
		t.Fatalf("read quarantined file: %v", err)
	}
	if string(moved) != string(corrupt) {
		t.Fatalf("quarantined bytes = %q, want the original %q (evidence must survive)", moved, corrupt)
	}

	// The built-in template is still there, so the product can create a bot again.
	templates, err := store.Templates(ctx)
	if err != nil {
		t.Fatalf("Templates after quarantine: %v", err)
	}
	if len(templates) != 1 || templates[0].ID != bot.LeadTemplateID {
		t.Fatalf("Templates after quarantine = %+v, want the built-in lead", templates)
	}
	if _, err := store.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: bot.LeadTemplateID}); err != nil {
		t.Fatalf("Create after quarantine: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a new store file was not written: %v", err)
	}
}

func TestModuleFailsWhenTheDataDirIsUnusable(t *testing.T) {
	dataDir := t.TempDir()
	// A regular file where the store directory should be: the module must fail
	// loudly instead of providing a store that silently drops bot records.
	blocked := filepath.Join(dataDir, bot.DataSubdir)
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
	if err := registry.Register(bot.Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := module.Load(root, registry, []module.Entry{{ID: "bots", Module: bot.ModuleID}}); err == nil {
		t.Fatal("module.Load succeeded with an unusable data dir")
	}
	if _, ok := root.Get(contracts.KeyBots); ok {
		t.Fatal("the failed module still provided contracts.KeyBots")
	}
}
