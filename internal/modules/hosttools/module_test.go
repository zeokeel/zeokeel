package hosttools

import (
	"context"
	"errors"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	hostcaps "github.com/zeokeel/zeokeel/internal/hosttools"
)

// --- fake toolkit -----------------------------------------------------------

type fakeToolkitConfig struct{}

// fakeToolkitModule provides a caller-supplied Rig registry as KeyToolkit, the
// same service the real toolkit module provides.
func fakeToolkitModule(registry *rigtool.Registry) *module.Descriptor[fakeToolkitConfig] {
	return module.Define("test/fake-toolkit", func(ctx cordis.Context, _ fakeToolkitConfig) error {
		_, err := ctx.Provide(contracts.KeyToolkit, registry)
		return err
	}).AddProvides(module.Provides(contracts.KeyToolkit)).WithName("Fake Rig toolkit")
}

// --- fixtures ---------------------------------------------------------------

type product struct {
	root     cordis.Context
	loader   *module.Loader
	registry *rigtool.Registry
}

// newKernelRegistry builds a kernel registry containing the fake toolkit and
// the host-tools module (with injected host capabilities when host is non-nil).
func newKernelRegistry(t *testing.T, rigRegistry *rigtool.Registry, host *hostcaps.Host) *module.Registry {
	t.Helper()
	registry := module.NewRegistry()
	if err := registry.Register(fakeToolkitModule(rigRegistry)); err != nil {
		t.Fatalf("register fake toolkit: %v", err)
	}
	descriptor := Module()
	if host != nil {
		descriptor = ModuleWithHost(host)
	}
	if err := registry.Register(descriptor); err != nil {
		t.Fatalf("register hosttools: %v", err)
	}
	return registry
}

func loadProduct(t *testing.T, host *hostcaps.Host, interactive bool) *product {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	rigRegistry := rigtool.NewRegistry()
	registry := newKernelRegistry(t, rigRegistry, host)
	loader, err := module.Load(root, registry, []module.Entry{
		{ID: "toolkit", Module: "test/fake-toolkit"},
		{ID: "hosttools", Module: ModuleID, Config: map[string]any{"interactive": interactive}},
	})
	if err != nil {
		t.Fatalf("load hosttools product: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	})
	return &product{root: root, loader: loader, registry: rigRegistry}
}

func (p *product) call(t *testing.T, name, arguments string) (rig.ToolResult, error) {
	t.Helper()
	return p.registry.Invoke(context.Background(), rig.ToolCall{ID: "call-1", Name: name, Arguments: arguments})
}

func (p *product) callOK(t *testing.T, name, arguments string) string {
	t.Helper()
	result, err := p.call(t, name, arguments)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("%s returned a model-facing error: %s", name, result.Content)
	}
	return result.Content
}

func (p *product) callError(t *testing.T, name, arguments string) string {
	t.Helper()
	result, err := p.call(t, name, arguments)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !result.IsError {
		t.Fatalf("%s did not return an error result: %q", name, result.Content)
	}
	return result.Content
}

// --- registration -----------------------------------------------------------

func TestModuleRegistersHostTools(t *testing.T) {
	product := loadProduct(t, nil, true)

	names := make([]string, 0, 5)
	for _, spec := range product.registry.Specs() {
		names = append(names, spec.Name)
		if strings.TrimSpace(spec.Description) == "" {
			t.Fatalf("tool %q has an empty description", spec.Name)
		}
		if spec.Parameters["type"] != "object" {
			t.Fatalf("tool %q schema type = %v, want object", spec.Name, spec.Parameters["type"])
		}
	}
	want := []string{
		ToolAskUser, ToolExitPlanMode, ToolGoal, ToolPlanReview, ToolPresent,
	}
	if len(names) != len(want) {
		t.Fatalf("registered tools = %v, want %v", names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("registered tools = %v, want %v", names, want)
		}
	}

	instance, ok := product.loader.Instance("hosttools")
	if !ok || instance.ModuleID() != ModuleID {
		t.Fatalf("hosttools instance = %+v (found=%v)", instance, ok)
	}
	if state := instance.State().String(); state != "active" {
		t.Fatalf("hosttools state = %s, want active", state)
	}
}

// TestNonInteractiveProductOmitsPlanTools pins the headless/ACP product: with
// interactive=false no user can ever approve a plan, so the plan-review tools
// must not be registered at all (a submitted plan would stall the run); the
// other host tools stay available.
func TestNonInteractiveProductOmitsPlanTools(t *testing.T) {
	product := loadProduct(t, nil, false)

	names := make([]string, 0, 3)
	for _, spec := range product.registry.Specs() {
		names = append(names, spec.Name)
	}
	want := []string{ToolAskUser, ToolGoal, ToolPresent}
	if len(names) != len(want) {
		t.Fatalf("non-interactive registered tools = %v, want %v", names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("non-interactive registered tools = %v, want %v", names, want)
		}
	}

	// The plan tools are gone from the registry, not merely disabled: a call
	// is an ordinary unknown-tool error the model can recover from.
	for _, name := range []string{ToolPlanReview, ToolExitPlanMode} {
		if _, err := product.call(t, name, `{"plan":"# p","action":"status"}`); !errors.Is(err, rig.ErrToolNotFound) {
			t.Fatalf("%s in a non-interactive product = %v, want rig.ErrToolNotFound", name, err)
		}
	}
	// The remaining tools keep working in a headless product.
	if content := product.callOK(t, ToolGoal, `{"action":"get"}`); content != "no goal is set" {
		t.Fatalf("headless goal get = %q", content)
	}
	if content := product.callOK(t, ToolPresent, `{"title":"t","content":"c"}`); !strings.Contains(content, "artifact-1") {
		t.Fatalf("headless present = %q", content)
	}
}

func TestDescriptorMetadata(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID {
		t.Fatalf("module ID = %q, want %q", descriptor.ID, ModuleID)
	}
	if descriptor.Name == "" {
		t.Fatal("module has no display name")
	}
	if len(descriptor.Requires) != 1 || descriptor.Requires[0].Name != contracts.KeyToolkit.Name() {
		t.Fatalf("requires = %+v, want the toolkit key", descriptor.Requires)
	}
	if len(descriptor.Provides) != 0 {
		t.Fatalf("provides = %+v, want none", descriptor.Provides)
	}
}

// --- tool behaviour ---------------------------------------------------------

func TestAskUserWithoutInteractiveUI(t *testing.T) {
	product := loadProduct(t, nil, false)
	content := product.callError(t, ToolAskUser, `{"question":"Which database?"}`)
	if !strings.Contains(content, "no interactive UI") {
		t.Fatalf("ask_user non-interactive content = %q, want the no-interactive message", content)
	}
	// A blank question is rejected before the capability is consulted.
	if content := product.callError(t, ToolAskUser, `{}`); !strings.Contains(content, "question is required") {
		t.Fatalf("ask_user missing question = %q", content)
	}
}

func TestAskUserWithInjectedAsker(t *testing.T) {
	host := hostcaps.NewMemoryHost()
	asker := hostcaps.NewMemoryAsker("postgres")
	host.Asker = asker
	product := loadProduct(t, host, true)

	content := product.callOK(t, ToolAskUser, `{"question":"Which database?","options":["sqlite","postgres"]}`)
	if !strings.Contains(content, "postgres") {
		t.Fatalf("ask_user content = %q, want the injected answer", content)
	}
	asked := asker.Asked()
	if len(asked) != 1 || asked[0].Question != "Which database?" || len(asked[0].Options) != 2 {
		t.Fatalf("asked = %+v, want the recorded question", asked)
	}
}

func TestPresentTool(t *testing.T) {
	host := hostcaps.NewMemoryHost()
	product := loadProduct(t, host, true)

	content := product.callOK(t, ToolPresent, `{"title":"Test report","content":"# everything passed"}`)
	if !strings.Contains(content, "Test report") || !strings.Contains(content, "artifact-1") {
		t.Fatalf("present content = %q", content)
	}
	artifacts := host.Presenter.(*hostcaps.MemoryPresenter).Artifacts()
	if len(artifacts) != 1 || artifacts[0].Title != "Test report" || artifacts[0].Body != "# everything passed" {
		t.Fatalf("presented artifacts = %+v", artifacts)
	}
	if content := product.callError(t, ToolPresent, `{"title":"","content":"x"}`); !strings.Contains(content, "title is required") {
		t.Fatalf("present missing title = %q", content)
	}
}

func TestPlanReviewAndExitPlanMode(t *testing.T) {
	product := loadProduct(t, nil, true)

	// Nothing is under review yet: approving is a readable error.
	if content := product.callError(t, ToolExitPlanMode, `{"action":"approve"}`); !strings.Contains(content, "no plan is under review") {
		t.Fatalf("approve without plan = %q", content)
	}
	content := product.callOK(t, ToolPlanReview, `{"plan":"# Release plan\n1. test\n2. ship"}`)
	if !strings.Contains(content, "reviewing") {
		t.Fatalf("plan_review content = %q, want reviewing", content)
	}
	// Reviewing is not approval: status reports reviewing, then approve applies.
	content = product.callOK(t, ToolExitPlanMode, `{"action":"status"}`)
	if content != "plan mode: reviewing" {
		t.Fatalf("status content = %q, want plan mode: reviewing", content)
	}
	content = product.callOK(t, ToolExitPlanMode, `{"action":"approve"}`)
	if content != "plan mode: approved" {
		t.Fatalf("approve content = %q, want plan mode: approved", content)
	}
	if content := product.callError(t, ToolExitPlanMode, `{"action":"cancel"}`); !strings.Contains(content, "no plan is under review") {
		t.Fatalf("cancel after approval = %q", content)
	}
	if content := product.callError(t, ToolExitPlanMode, `{"action":"bogus"}`); !strings.Contains(content, "unknown plan action") {
		t.Fatalf("bogus action = %q", content)
	}
	// Attempting to approve/exit plan mode with no plan controller connected is
	// a tool error, not an activation failure.
	empty := loadProduct(t, &hostcaps.Host{}, true)
	if content := empty.callError(t, ToolPlanReview, `{"plan":"# x"}`); !strings.Contains(content, "no plan controller") {
		t.Fatalf("plan_review without controller = %q", content)
	}
}

func TestGoalTool(t *testing.T) {
	product := loadProduct(t, nil, true)

	if content := product.callOK(t, ToolGoal, `{"action":"get"}`); content != "no goal is set" {
		t.Fatalf("goal get on empty = %q", content)
	}
	content := product.callOK(t, ToolGoal, `{"action":"set","text":"Ship v0.4"}`)
	if !strings.Contains(content, "goal-1") || !strings.Contains(content, "Ship v0.4") {
		t.Fatalf("goal set content = %q", content)
	}
	// State persists between tool calls (the same in-memory store).
	content = product.callOK(t, ToolGoal, `{"action":"get"}`)
	if content != "goal goal-1 [active]: Ship v0.4" {
		t.Fatalf("goal get content = %q", content)
	}
	content = product.callOK(t, ToolGoal, `{"action":"status"}`)
	if !strings.Contains(content, "Ship v0.4") {
		t.Fatalf("goal status content = %q", content)
	}
	if content := product.callError(t, ToolGoal, `{"action":"set"}`); !strings.Contains(content, "text is required") {
		t.Fatalf("goal set without text = %q", content)
	}
	if content := product.callOK(t, ToolGoal, `{"action":"clear"}`); content != "goal cleared" {
		t.Fatalf("goal clear = %q", content)
	}
	if content := product.callOK(t, ToolGoal, `{"action":"get"}`); content != "no goal is set" {
		t.Fatalf("goal get after clear = %q", content)
	}
	if content := product.callError(t, ToolGoal, `{"action":"bogus"}`); !strings.Contains(content, "action must be one of") {
		t.Fatalf("goal bogus action = %q", content)
	}
}

func TestMissingCapabilitiesAreToolErrors(t *testing.T) {
	product := loadProduct(t, &hostcaps.Host{}, true)
	cases := map[string]string{
		ToolAskUser:      `{"question":"q"}`,
		ToolPresent:      `{"title":"t","content":"c"}`,
		ToolPlanReview:   `{"plan":"# p"}`,
		ToolExitPlanMode: `{"action":"status"}`,
		ToolGoal:         `{"action":"get"}`,
	}
	for name, arguments := range cases {
		content := product.callError(t, name, arguments)
		if !strings.Contains(content, "no ") || !strings.Contains(content, "available") {
			t.Fatalf("%s missing capability = %q, want a readable unavailable message", name, content)
		}
	}
}

// --- unregistration and rollback -------------------------------------------

func TestModuleCloseUnregistersTools(t *testing.T) {
	product := loadProduct(t, nil, false)
	if got := len(product.registry.Specs()); got != 3 {
		t.Fatalf("tools after load = %d, want 3 (non-interactive product without plan tools)", got)
	}
	if err := product.loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if got := len(product.registry.Specs()); got != 0 {
		names := make([]string, 0)
		for _, spec := range product.registry.Specs() {
			names = append(names, spec.Name)
		}
		t.Fatalf("tools after module close = %v, want none", names)
	}
	if _, ok := product.root.Get(contracts.KeyToolkit); ok {
		t.Fatal("toolkit service survived loader.Close")
	}
}

func TestDuplicateRegistrationRollsBack(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })

	rigRegistry := rigtool.NewRegistry()
	if err := rigRegistry.Register(dummyTool{name: ToolGoal}); err != nil {
		t.Fatalf("pre-register dummy: %v", err)
	}
	registry := newKernelRegistry(t, rigRegistry, nil)
	_, err := module.Load(root, registry, []module.Entry{
		{ID: "toolkit", Module: "test/fake-toolkit"},
		{ID: "hosttools", Module: ModuleID, Config: map[string]any{"interactive": true}},
	})
	if err == nil {
		t.Fatal("load succeeded with a duplicate tool name")
	}
	if !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("load error = %v, want the duplicate-tool error", err)
	}
	// The tools registered before the duplicate failed must be rolled back;
	// only the pre-existing dummy remains.
	specs := rigRegistry.Specs()
	if len(specs) != 1 || specs[0].Name != ToolGoal {
		names := make([]string, 0, len(specs))
		for _, spec := range specs {
			names = append(names, spec.Name)
		}
		t.Fatalf("registry after failed activation = %v, want only the pre-existing tool", names)
	}
}

func TestToolContextCancellation(t *testing.T) {
	product := loadProduct(t, nil, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := findTool(t, product.registry, ToolGoal)
	if _, err := tool.Invoke(ctx, rig.ToolCall{Name: ToolGoal, Arguments: `{"action":"get"}`}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Invoke(cancelled ctx) = %v, want context.Canceled", err)
	}
}

// dummyTool is a minimal rig.Tool used to occupy a tool name.
type dummyTool struct {
	name string
}

func (d dummyTool) Name() string           { return d.name }
func (d dummyTool) Description() string    { return "dummy" }
func (d dummyTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (d dummyTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "dummy"}, nil
}

func findTool(t *testing.T, registry *rigtool.Registry, name string) rig.Tool {
	t.Helper()
	for _, tool := range registry.Tools() {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
