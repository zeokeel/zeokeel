package assembly_test

// Assembly-root tests. They exercise the embedded products end to end against a
// real kernel root: the products load the backend modules only,
// headless loads the backend-only product on a root that has no UI services and
// no display environment. Everything here is headless-safe: uiroot is pure Go
// state, and the desktop test never creates a mygo window.

import (
	"context"
	"os"

	"errors"
	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"
	"slices"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
)

// --- host service fakes (the process host provides these on the root) ---

type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{values: make(map[string]string)}
}

func (f *fakeSettings) Get(key, def string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.values[key]; ok {
		return value
	}
	return def
}

func (f *fakeSettings) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[key] = value
	return nil
}

func (f *fakeSettings) All() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.values))
	for key, value := range f.values {
		out[key] = value
	}
	return out
}

type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (f *fakeLogger) Printf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, format)
}

// --- helpers ---

func newRoot(t *testing.T) cordis.Context {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() {
		if err := root.DisposeAll(); err != nil {
			t.Errorf("DisposeAll: %v", err)
		}
	})
	return root
}

func provide[T any](t *testing.T, root cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := root.Provide(key, value); err != nil {
		t.Fatalf("provide %q: %v", key.Name(), err)
	}
}

// provideHostServices provides the services every product's host provides:
// data dir, settings and log. The browser transport's own services are the
// gateway's business (internal/gateway), not this package's.
func provideHostServices(t *testing.T, root cordis.Context) {
	t.Helper()
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	var settings contracts.SettingsService = newFakeSettings()
	provide(t, root, contracts.KeySettings, settings)
	var logger contracts.Logger = &fakeLogger{}
	provide(t, root, contracts.KeyLogger, logger)
}

// --- products and registry ---

func TestProductsAndEntries(t *testing.T) {
	// desktop.json is gone with the native UI: the embedded assemblies are the
	// backend-only headless one, and serve reuses its entries.
	want := []string{assembly.ProductHeadless}
	if got := assembly.Products(); !slices.Equal(got, want) {
		t.Fatalf("Products() = %v, want %v", got, want)
	}
	wantAll := []string{assembly.ProductHeadless, assembly.ProductServe}
	if got := assembly.AllProducts(); !slices.Equal(got, wantAll) {
		t.Fatalf("AllProducts() = %v, want %v", got, wantAll)
	}
	for _, product := range []string{"headless", "headless.json"} {
		if _, err := assembly.Entries(product); err != nil {
			t.Fatalf("Entries(%q): %v", product, err)
		}
	}
	if _, err := assembly.Entries("mobile"); err == nil {
		t.Fatal("Entries(unknown product) succeeded")
	} else if !strings.Contains(err.Error(), "unknown product") {
		t.Fatalf("Entries(unknown product) = %v, want an unknown-product error", err)
	}
	// The retired windowed product must not resolve any more.
	if _, err := assembly.Entries("desktop"); err == nil {
		t.Fatal("Entries(desktop) still succeeds after the native UI retirement")
	}

	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	wantHeadless := []string{
		"zeokeel/appinfo", "zeokeel/sessionstore", "zeokeel/goal", "zeokeel/approvals", "zeokeel/workspace",
		"zeokeel/insights",
		"zeokeel/skills", "zeokeel/sandbox", "zeokeel/toolkit", "zeokeel/hosttools", "zeokeel/memorystore",
		"zeokeel/agent", "zeokeel/automation", "zeokeel/update", "zeokeel/importer", "zeokeel/acp",
	}
	if len(headless) != len(wantHeadless) {
		t.Fatalf("headless entries = %d, want %d", len(headless), len(wantHeadless))
	}
	for index, root := range headless {
		if root.Module != wantHeadless[index] {
			t.Fatalf("headless entry %d = %q, want %q", index, root.Module, wantHeadless[index])
		}
	}
}

// TestHeadlessPinsPermissionSensitiveModules pins the shared-store contract
// (2026-11 audit): headless/ACP must not follow a desktop permission preset
// left in a shared settings store — BOTH the approval gate (approvals) and the
// OS sandbox mode (toolkit) pin their assembly defaults via
// ignorePermissionSetting. Desktop keeps following the live preset.
func TestHeadlessPinsPermissionSensitiveModules(t *testing.T) {
	configOf := func(entries []module.Entry, id string) map[string]any {
		t.Helper()
		for _, entry := range entries {
			if entry.ID == id {
				config, _ := entry.Config.(map[string]any)
				return config
			}
		}
		t.Fatalf("entry %q not found", id)
		return nil
	}
	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	for _, id := range []string{"approvals", "toolkit"} {
		if pinned, _ := configOf(headless, id)["ignorePermissionSetting"].(bool); !pinned {
			t.Errorf("headless %s must set ignorePermissionSetting=true", id)
		}
	}
	// serve reuses the headless entries with its own overrides; its approvals
	// override is checked in serve_test.go.
}

func TestRegistryIsExplicit(t *testing.T) {
	registry := assembly.Registry()
	for _, id := range []string{
		"zeokeel/appinfo",
		"zeokeel/toolkit",
		"zeokeel/agent",
		"zeokeel/hosttools",
		"zeokeel/automation",
		"zeokeel/update",
		"zeokeel/importer",
		"zeokeel/acp",
		"zeokeel/modelcatalog",
		"zeokeel/insights",
		"zeokeel/sessionstore",
		"zeokeel/goal",
		"zeokeel/workspace",
		"zeokeel/skills",
		"zeokeel/approvals",
		"zeokeel/sandbox",
	} {
		if !registry.Has(id) {
			t.Fatalf("Registry() lacks module %q", id)
		}
	}
	// The native UI's twelve modules are not part of any product any more.
	for _, id := range []string{"zeokeel-ui/home", "zeokeel-ui/session", "zeokeel-ui/settings"} {
		if registry.Has(id) {
			t.Fatalf("Registry() still registers the retired UI module %q", id)
		}
	}
	if got := len(registry.Modules()); got != 18 {
		t.Fatalf("Registry() modules = %d, want 20", got)
	}
}

func TestValidateEmbeddedProducts(t *testing.T) {
	for _, product := range assembly.Products() {
		if err := assembly.Validate(product); err != nil {
			t.Errorf("Validate(%s): %v", product, err)
		}
	}
}

// --- headless product: backend modules load, close unwinds ---

// TestHeadlessAssemblyLoadsAndUnwinds is the load/unload contract of the one
// remaining embedded assembly: every backend module settles, its services are
// reachable, and Close withdraws them and unregisters every tool the module
// contributed. (The windowed product's UI group is gone with the native UI.)
func TestHeadlessAssemblyLoadsAndUnwinds(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{
		Version:   "9.9.9",
		Commit:    "deadbeef",
		BuildTime: "2026-11-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless): %v", err)
	}

	instances := loader.Instances()
	wantIDs := []string{
		"appinfo", "store", "goal", "approvals", "workspace", "insights", "skills", "sandbox", "toolkit", "hosttools", "memorystore",
		"agent", "automation", "update", "importer", "acp",
	}
	gotIDs := make([]string, len(instances))
	for index, instance := range instances {
		gotIDs[index] = instance.ID()
		if state := instance.State(); state != cordis.Active {
			t.Fatalf("instance %q state = %s, want Active", instance.ID(), state)
		}
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("headless instance IDs = %v, want %v", gotIDs, wantIDs)
	}
	if warnings := loader.Warnings(); len(warnings) != 0 {
		t.Fatalf("headless warnings = %v, want none", warnings)
	}

	// The toolkit registry is the product's shared tool catalog: the hosttools
	// module registered its host tools into it during activation.
	toolkitRegistry, ok := root.Get(hosttools.KeyRegistry)
	if !ok {
		t.Fatal("tool registry service is not available on the root")
	}
	// headless.json sets hosttools interactive=false, so the plan-review pair is
	// intentionally absent: nothing could approve a plan. serve flips this (see
	// serve_test.go).
	hostToolNames := []string{"ask_user", "create_goal", "get_goal", "present", "update_goal"}
	for _, name := range hostToolNames {
		found := false
		for _, spec := range toolkitRegistry.Specs() {
			if spec.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("toolkit registry lacks host tool %q", name)
		}
	}

	// Goal v2: the durable per-session goal backend is assembled and the DSH
	// goal trio reaches it through the toolkit registry.
	goalService, ok := root.Get(contracts.KeyGoal)
	if !ok || goalService == nil {
		t.Fatal("KeyGoal service is not available on the root")
	}
	store, ok := root.Get(contracts.KeyStore)
	if !ok || store == nil {
		t.Fatal("KeyStore service is not available on the root")
	}
	if err := store.CreateSession(context.Background(), "sess-goal-assembly", "goal test"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	created, err := goalService.CreateGoal(context.Background(), "sess-goal-assembly", "ship the release", 0)
	if err != nil {
		t.Fatalf("CreateGoal on the assembled backend: %v", err)
	}
	if created.Revision != 1 || created.MaxGoalRounds != contracts.DefaultMaxGoalRounds ||
		created.Activation != contracts.GoalActivationArmed {
		t.Fatalf("assembled CreateGoal = %+v, want revision 1 / 256 rounds / armed", created)
	}
	goalCtx := contracts.WithAgentSession(context.Background(), "sess-goal-assembly")
	result, err := toolkitRegistry.Invoke(goalCtx, rig.ToolCall{ID: "goal-call", Name: "get_goal", Arguments: "{}"})
	if err != nil || result.IsError {
		t.Fatalf("get_goal through the assembled registry = %q / %v", result.Content, err)
	}
	if !strings.Contains(result.Content, created.ID) {
		t.Fatalf("get_goal content = %q, want the created goal %q", result.Content, created.ID)
	}
	// Deleting the session must not let a later goal round resurrect it.
	if err := store.DeleteSession(context.Background(), "sess-goal-assembly"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := goalService.AdmitRound(context.Background(), "sess-goal-assembly"); !errors.Is(err, contracts.ErrGoalNotFound) {
		t.Fatalf("AdmitRound on a deleted session = %v, want ErrGoalNotFound", err)
	}
	sessions, err := store.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	for _, session := range sessions {
		if session.ID == "sess-goal-assembly" {
			t.Fatal("a goal event resurrected the deleted session row")
		}
	}

	// The agent provides the context inspector the browser's Context tab reads.
	if inspector, ok := root.Get(contracts.KeyContext); !ok || inspector == nil {
		t.Fatal("KeyContext service is not available on the root")
	}

	// Build identity from the host was overlaid onto the appinfo module config.
	info, ok := root.Get(contracts.KeyAppInfo)
	if !ok {
		t.Fatal("KeyAppInfo service is not available on the root")
	}
	if info.Version != "9.9.9" || info.Commit != "deadbeef" || info.BuildTime != "2026-11-06T12:00:00Z" {
		t.Fatalf("appinfo = %+v, want the injected build identity", info)
	}

	// Closing the loader disposes the assembly: every service is withdrawn and
	// every contributed tool is unregistered through its kernel effect.
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if _, ok := root.Get(contracts.KeyAppInfo); ok {
		t.Fatal("appinfo service survived loader.Close")
	}
	for _, spec := range toolkitRegistry.Specs() {
		if slices.Contains(hostToolNames, spec.Name) {
			t.Fatalf("host tool %q survived loader.Close", spec.Name)
		}
	}
	if got := len(toolkitRegistry.Specs()); got == 0 {
		t.Fatal("toolkit module's own tools disappeared with the host tools")
	}
}

// --- headless product: loads without any UI service or display environment ---

// TestHeadlessAssemblyHasNoStubTeamTool is the FR-14 regression: the legacy
// hosttools in-memory "team" stub is gone, while the real rig team tools that
// the agent module registers (against contracts.KeyTeam) stay available.
func TestHeadlessAssemblyHasNoStubTeamTool(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{Version: "test"})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless): %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	registry, ok := root.Get(hosttools.KeyRegistry)
	if !ok || registry == nil {
		t.Fatal("tool registry service is not available on the root")
	}
	names := make(map[string]bool, 128)
	for _, spec := range registry.Specs() {
		names[spec.Name] = true
	}
	if names["team"] {
		t.Fatal("the legacy hosttools stub team tool is still registered")
	}
	// The real team surface (agent module + rig team manager) is still there.
	for _, name := range []string{
		"spawn_teammate", "list_agents", "wait_agent", "send_message", "interrupt_agent", "team_task_create",
	} {
		if !names[name] {
			t.Fatalf("real team tool %q missing after removing the stub (have %v)", name, names)
		}
	}
	// The remaining host tools are unchanged.
	for _, name := range []string{"ask_user", "present", "get_goal", "create_goal", "update_goal"} {
		if !names[name] {
			t.Fatalf("host tool %q missing", name)
		}
	}
}

func TestHeadlessAssemblyNeedsNoUIServices(t *testing.T) {
	root := newRoot(t)
	// Deliberately no uiroot: the headless host provides settings and log only.
	provideHostServices(t, root)

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless): %v", err)
	}
	defer func() {
		if err := loader.Close(); err != nil {
			t.Errorf("loader.Close: %v", err)
		}
	}()

	instances := loader.Instances()
	if len(instances) != 16 {
		t.Fatalf("headless instances = %d, want 16", len(instances))
	}
	instance := instances[0]
	if instance.ID() != "appinfo" || instance.ModuleID() != "zeokeel/appinfo" {
		t.Fatalf("headless instance = %s/%s, want appinfo/zeokeel/appinfo", instance.ID(), instance.ModuleID())
	}
	if state := instance.State(); state != cordis.Active {
		t.Fatalf("appinfo state = %s, want Active", state)
	}
	// insights follows workspace, so hosttools shifts to index 9.
	hostInstance := instances[9]
	if hostInstance.ID() != "hosttools" || hostInstance.ModuleID() != "zeokeel/hosttools" {
		t.Fatalf("headless instance[9] = %s/%s, want hosttools/zeokeel/hosttools",
			hostInstance.ID(), hostInstance.ModuleID())
	}
	if state := hostInstance.State(); state != cordis.Active {
		t.Fatalf("hosttools state = %s, want Active", state)
	}
	// The non-interactive product keeps the host tools that can still act
	// without a UI (ask_user degrades to a readable message; present/goal/team
	// keep their in-process behavior) but hides the plan-review pair: a plan
	// submitted for review could never be approved in a headless session, so
	// exposing plan_review/exit_plan_mode only dead-ends the agent.
	toolkitRegistry, ok := root.Get(hosttools.KeyRegistry)
	if !ok {
		t.Fatal("tool registry service is not available on the headless root")
	}
	registered := make(map[string]bool, len(toolkitRegistry.Specs()))
	for _, spec := range toolkitRegistry.Specs() {
		registered[spec.Name] = true
	}
	for _, name := range []string{"ask_user", "get_goal", "create_goal", "update_goal", "present"} {
		if !registered[name] {
			t.Fatalf("headless toolkit registry lacks host tool %q", name)
		}
	}
	if registered["goal"] {
		t.Fatal("headless toolkit registry still exposes the retired Goal v1 `goal` tool")
	}
	for _, name := range []string{"plan_review", "exit_plan_mode"} {
		if registered[name] {
			t.Fatalf("headless toolkit registry exposes plan tool %q, want it hidden", name)
		}
	}
	if warnings := loader.Warnings(); len(warnings) != 0 {
		t.Fatalf("headless warnings = %v, want none", warnings)
	}
	// The product genuinely has no UI: no UI service was provided and the
	// headless assembly never asks for one.
	// No window-owned service is provided at all any more: the browser
	// transport provides its own (gateway.Provide), and the process host
	// contributes only data dir/settings/log.
	if _, ok := root.Get(contracts.KeyNavigator); ok {
		t.Fatal("the headless root unexpectedly has a navigator service")
	}
	if _, ok := root.Get(contracts.KeyTheme); ok {
		t.Fatal("the headless root unexpectedly has a theme service")
	}
	info, ok := root.Get(contracts.KeyAppInfo)
	if !ok {
		t.Fatal("appinfo service is not available")
	}
	if info.Version != "0.1.0" {
		t.Fatalf("headless appinfo version = %q, want the injected 0.1.0", info.Version)
	}
}

// --- pre-flight failures happen before any fiber is created ---

func TestPreflightValidationFailures(t *testing.T) {
	appinfoEntry := module.Entry{ID: "appinfo", Module: "zeokeel/appinfo"}
	// The agent requires the session store, the approvals service and the shared
	// tool registry (the serve host provides the last one): with only the agent
	// in the tree nothing provides them, which is the missing-provider case.
	agentEntry := module.Entry{ID: "agent", Module: "zeokeel/agent"}

	tests := []struct {
		name    string
		entries []module.Entry
		want    string
	}{
		{
			name:    "unknown module",
			entries: []module.Entry{{ID: "ghost", Module: "zeokeel/missing"}},
			want:    "unknown module",
		},
		{
			name: "duplicate instance id",
			entries: []module.Entry{
				appinfoEntry,
				{ID: "appinfo", Module: "zeokeel/appinfo"},
			},
			want: "duplicate instance ID",
		},
		{
			name:    "missing provider",
			entries: []module.Entry{agentEntry},
			want:    "no enabled entry in the assembly provides",
		},
		{
			name: "rejected config",
			entries: []module.Entry{{
				ID:     "appinfo",
				Module: "zeokeel/appinfo",
				Config: map[string]any{"version": 42},
			}},
			want: "decode config",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := assembly.ValidateEntries(test.entries)
			if err == nil {
				t.Fatalf("ValidateEntries(%s) succeeded, want a pre-flight error", test.name)
			}
			var validation *module.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error type = %T, want *module.ValidationError", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

// A pre-flight failure must not leave a partially loaded tree or any residue:
// the same root can load the valid product afterwards.
func TestPreflightFailureLeavesRootUsable(t *testing.T) {
	root := newRoot(t)
	provideHostServices(t, root)

	if err := assembly.ValidateEntries([]module.Entry{{ID: "ghost", Module: "zeokeel-ui/ghost"}}); err == nil {
		t.Fatal("invalid entry tree passed validation")
	}
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductHeadless, assembly.BuildInfo{})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(headless) after a failed preflight: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
}

// TestMain lets the OS-sandbox helper re-exec this test binary: the assembly
// loads the always-on sandbox module, and on hosts without bwrap its Landlock
// fallback spawns this executable as a hidden helper (otherwise the whole test
// suite would recursively re-run inside the helper).
func TestMain(m *testing.M) {
	if osrunner.MaybeRunHelper(os.Args) {
		return
	}
	os.Exit(m.Run())
}

// A module can be registered and never composed. That is exactly how the Insights
// screen died: `zeokeel/insights` was in Registry() but in no product's entries, so
// `KeyInsights` was never provided at runtime and the screen's endpoint answered
// with zeros. This test makes the gap impossible to leave silently.
func TestEveryRegisteredModuleIsComposedIntoAProduct(t *testing.T) {
	entries, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	composed := make(map[string]bool, len(entries))
	for _, entry := range entries {
		composed[entry.Module] = true
	}
	// Registered on purpose, composed on purpose never: nothing consumes
	// KeyModelCatalog (the agent reads the modelcatalog.models settings row
	// directly), so composing it would add a settings migration with no reader.
	intentionallyUncomposed := map[string]string{
		"zeokeel/modelcatalog": "no consumer of KeyModelCatalog: the agent reads the modelcatalog.models settings row directly",
		"zeokeel/host/group":   "a no-op container that only carries an Entry.Children subtree; no product nests one since the UI group retired",
	}
	for _, info := range assembly.Registry().Modules() {
		if composed[info.ID] {
			continue
		}
		if reason, ok := intentionallyUncomposed[info.ID]; ok && reason != "" {
			continue
		}
		t.Fatalf("module %q is registered but composed into no product; either add it to "+
			"assemblies/headless.json or record why it is intentionally uncomposed", info.ID)
	}
}
