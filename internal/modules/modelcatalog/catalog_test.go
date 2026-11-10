package modelcatalog

// Tests for the model-catalog module: descriptor/validation, the empty
// built-in catalog, the assembly-configured baseline, copy semantics and kernel
// activation.

import (
	"fmt"
	"reflect"
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

func TestNoBuiltinCatalog(t *testing.T) {
	// Product decision: the catalog ships no built-in models. An empty assembly
	// config and an unset settings row mean "no models configured", so the
	// composer offers an empty state until the user adds one.
	svc := newService(Config{}, &fakeLogger{})
	if models := svc.Models(); len(models) != 0 {
		t.Fatalf("catalog = %+v, want no built-in presets", models)
	}
}

func TestConfiguredBaseline(t *testing.T) {
	// The assembly config is only a fallback baseline while the user has never
	// saved a model list; it passes the per-model connection fields through.
	cfg := Config{Models: []Model{
		{ID: "m1", Name: "Model One", Note: "fast",
			BaseURL: "https://m1.example/v1/", APIKey: " key-1 "},
		{ID: " m2 ", Name: " ", Note: " deep ",
			BaseURL: " https://m2.example/v1 "},
	}}
	svc := newService(cfg, &fakeLogger{})
	models := svc.Models()
	if len(models) != 2 {
		t.Fatalf("configured models = %+v, want 2", models)
	}
	wantFirst := contracts.ModelInfo{ID: "m1", Name: "Model One", Note: "fast",
		BaseURL: "https://m1.example/v1", APIKey: "key-1"}
	if !reflect.DeepEqual(models[0], wantFirst) {
		t.Fatalf("configured first = %+v, want %+v", models[0], wantFirst)
	}
	wantSecond := contracts.ModelInfo{ID: "m2", Name: "m2", Note: "deep",
		BaseURL: "https://m2.example/v1"}
	if !reflect.DeepEqual(models[1], wantSecond) {
		t.Fatalf("configured second = %+v, want %+v", models[1], wantSecond)
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
			map[string]any{"id": "deepseek-chat", "name": "DeepSeek Chat", "note": "对话",
				"baseURL": "https://chat.example/v1", "apiKey": "catalog-key"},
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
	if models[0].BaseURL != "https://chat.example/v1" || models[0].APIKey != "catalog-key" {
		t.Fatalf("activated connection = %q / %q", models[0].BaseURL, models[0].APIKey)
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

// TestThinkingConfigPassesThrough covers the model thinking metadata: assembly
// config and user settings rows must survive into contracts.ModelInfo.
func TestThinkingConfigPassesThrough(t *testing.T) {
	cfg := Config{Models: []Model{{
		ID: "qwen-x", Family: "qwen", DefaultThinking: "qwen-on",
		ThinkingModes: []contracts.ThinkingMode{{ID: "fast", NameZH: "快", Params: map[string]any{"enable_thinking": false}}},
	}}}
	svc := newService(cfg, &fakeLogger{})
	models := svc.Models()
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	got := models[0]
	if got.Family != "qwen" || got.DefaultThinking != "qwen-on" {
		t.Fatalf("family/default = %+v", got)
	}
	if len(got.ThinkingModes) != 1 || got.ThinkingModes[0].ID != "fast" ||
		got.ThinkingModes[0].Params["enable_thinking"] != false {
		t.Fatalf("custom modes = %+v", got.ThinkingModes)
	}

	store := newSettingsStore(nil)
	store.setModels(`[{"id":"m","family":"deepseek","defaultThinking":"reasoning-high","thinkingModes":[{"id":"x","nameZh":"自定义","params":{"reasoning_effort":"low"}}]}]`)
	svc2, _ := modelService(t, Config{}, store)
	models = svc2.Models()
	if len(models) != 1 || models[0].Family != "deepseek" ||
		models[0].DefaultThinking != "reasoning-high" || len(models[0].ThinkingModes) != 1 {
		t.Fatalf("settings passthrough = %+v", models)
	}
}
