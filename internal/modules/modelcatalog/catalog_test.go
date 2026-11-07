package modelcatalog

// Tests for the model-catalog module: descriptor/validation, the built-in and
// configured presets, copy semantics and kernel activation.

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/modelcatalog" {
		t.Fatalf("module ID = %q", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version || Version != "1.0.0" {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyModelCatalog.Name() {
		t.Fatalf("provides = %+v, want KeyModelCatalog", descriptor.Provides)
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyLogger.Name() {
		t.Fatalf("requires = %+v, want KeyLogger", descriptor.Requires)
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validator")
	}
	if err := descriptor.Validate(Config{}); err != nil {
		t.Fatalf("empty config validation = %v, want nil", err)
	}
	if err := descriptor.Validate(Config{Models: []Model{{ID: "a"}, {ID: " b "}}}); err != nil {
		t.Fatalf("valid config validation = %v", err)
	}
	if err := descriptor.Validate(Config{Models: []Model{{ID: "a"}, {ID: " a "}}}); err == nil {
		t.Fatal("duplicate IDs passed validation")
	}
	if err := descriptor.Validate(Config{Models: []Model{{ID: "  "}}}); err == nil {
		t.Fatal("empty ID passed validation")
	}
}

func TestDefaultCatalog(t *testing.T) {
	svc := newService(Config{}, &fakeLogger{})
	models := svc.Models()
	if len(models) != 2 {
		t.Fatalf("default models = %+v, want 2 presets", models)
	}
	if models[0].ID != "deepseek-chat" || models[0].Name != "DeepSeek Chat" || models[0].Note != "通用对话" {
		t.Fatalf("first default = %+v", models[0])
	}
	if models[1].ID != "deepseek-reasoner" || models[1].Name != "DeepSeek Reasoner" || models[1].Note != "深度推理" {
		t.Fatalf("second default = %+v", models[1])
	}
}

func TestConfiguredCatalogOverridesDefaults(t *testing.T) {
	cfg := Config{Models: []Model{
		{ID: "m1", Name: "Model One", Note: "fast"},
		{ID: " m2 ", Name: " ", Note: " deep "},
	}}
	svc := newService(cfg, &fakeLogger{})
	models := svc.Models()
	if len(models) != 2 {
		t.Fatalf("configured models = %+v, want 2", models)
	}
	if models[0] != (contracts.ModelInfo{ID: "m1", Name: "Model One", Note: "fast"}) {
		t.Fatalf("configured first = %+v", models[0])
	}
	if models[1].ID != "m2" || models[1].Name != "m2" || models[1].Note != "deep" {
		t.Fatalf("configured second normalization = %+v", models[1])
	}
}

func TestModelsReturnsACopy(t *testing.T) {
	cfg := Config{Models: []Model{{ID: "one", Name: "One"}}}
	svc := newService(cfg, &fakeLogger{})
	models := svc.Models()
	models[0].ID = "mutated"
	if again := svc.Models(); again[0].ID != "one" {
		t.Fatalf("catalog mutated through Models(): %+v", again)
	}
	// The caller mutating the assembly config after construction must not leak
	// into the service either.
	cfg.Models[0].ID = "changed-config"
	if again := svc.Models(); again[0].ID != "one" {
		t.Fatalf("catalog aliases the config: %+v", again)
	}
}

func TestKernelActivation(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "modelcatalog", Module: ModuleID,
		Config: map[string]any{"models": []any{
			map[string]any{"id": "deepseek-chat", "name": "DeepSeek Chat", "note": "对话"},
		}},
	}})
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	defer func() { _ = loader.Close() }()
	catalog, err := root.Require(contracts.KeyModelCatalog)
	if err != nil {
		t.Fatalf("require KeyModelCatalog: %v", err)
	}
	models := catalog.Models()
	if len(models) != 1 || models[0].ID != "deepseek-chat" || models[0].Name != "DeepSeek Chat" {
		t.Fatalf("activated catalog = %+v", models)
	}
}

// TestActivationRequiresLogger pins the required-service gate.
func TestActivationRequiresLogger(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "modelcatalog", Module: ModuleID}})
	if err == nil {
		_ = loader.Close()
		t.Fatal("activation without a logger succeeded")
	}
	if !strings.Contains(err.Error(), "requires service") {
		t.Fatalf("activation error = %v, want missing-service hint", err)
	}
}
