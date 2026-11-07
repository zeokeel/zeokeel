package assembly_test

// Assembly-root tests. They exercise the embedded products end to end against a
// real kernel root: desktop loads the UI group (and needs uiroot's services),
// headless loads the backend-only product on a root that has no UI services and
// no display environment. Everything here is headless-safe: uiroot is pure Go
// state, and the desktop test never creates a mygo window.

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
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

// provideHostServices provides the services every product's host provides, plus
// the uiroot services when withUI is true (desktop).
func provideHostServices(t *testing.T, root cordis.Context, withUI bool) *uiroot.Root {
	t.Helper()
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	var settings contracts.SettingsService = newFakeSettings()
	provide(t, root, contracts.KeySettings, settings)
	var logger contracts.Logger = &fakeLogger{}
	provide(t, root, contracts.KeyLogger, logger)
	if !withUI {
		return nil
	}
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel test"})
	provide(t, root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, root, contracts.KeyTheme, uiRoot.Theme())
	provide(t, root, contracts.KeyLocale, uiRoot.Locale())
	provide(t, root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	return uiRoot
}

// --- products and registry ---

func TestProductsAndEntries(t *testing.T) {
	want := []string{assembly.ProductDesktop, assembly.ProductHeadless}
	if got := assembly.Products(); !slices.Equal(got, want) {
		t.Fatalf("Products() = %v, want %v", got, want)
	}
	for _, product := range []string{"desktop", "desktop.json"} {
		if _, err := assembly.Entries(product); err != nil {
			t.Fatalf("Entries(%q): %v", product, err)
		}
	}
	if _, err := assembly.Entries("mobile"); err == nil {
		t.Fatal("Entries(unknown product) succeeded")
	} else if !strings.Contains(err.Error(), "unknown product") {
		t.Fatalf("Entries(unknown product) = %v, want an unknown-product error", err)
	}

	desktop, err := assembly.Entries(assembly.ProductDesktop)
	if err != nil {
		t.Fatalf("Entries(desktop): %v", err)
	}
	if len(desktop) != 15 {
		t.Fatalf("desktop roots = %d, want 15 entries", len(desktop))
	}
	_ = desktop
	wantRoots := []string{
		"zeokeel/appinfo", "zeokeel/sessionstore", "zeokeel/approvals", "zeokeel/workspace",
		"zeokeel/terminal", "zeokeel/skills", "zeokeel/memory", "zeokeel/toolkit", "zeokeel/hosttools",
		"zeokeel/agent", "zeokeel/automation", "zeokeel/update", "zeokeel/importer",
		"zeokeel/modelcatalog", assembly.GroupModuleID,
	}
	for index, root := range desktop {
		if root.Module != wantRoots[index] {
			t.Fatalf("desktop root %d = %q, want %q", index, root.Module, wantRoots[index])
		}
	}
	if len(desktop[14].Children) != 12 {
		t.Fatalf("UI group children = %d, want 10 UI modules", len(desktop[11].Children))
	}
	wantChildren := []string{
		"zeokeel-ui/home", "zeokeel-ui/toolcards", "zeokeel-ui/about",
		"zeokeel-ui/sidebar", "zeokeel-ui/session", "zeokeel-ui/settings", "zeokeel-ui/approval",
		"zeokeel-ui/rightpanel", "zeokeel-ui/automation", "zeokeel-ui/plugins",
		"zeokeel-ui/interaction", "zeokeel-ui/toasts",
	}
	for index, child := range desktop[14].Children {
		if child.Module != wantChildren[index] {
			t.Fatalf("UI group child %d = %q, want %q", index, child.Module, wantChildren[index])
		}
	}

	headless, err := assembly.Entries(assembly.ProductHeadless)
	if err != nil {
		t.Fatalf("Entries(headless): %v", err)
	}
	wantHeadless := []string{
		"zeokeel/appinfo", "zeokeel/sessionstore", "zeokeel/approvals", "zeokeel/workspace",
		"zeokeel/terminal", "zeokeel/skills", "zeokeel/memory", "zeokeel/toolkit", "zeokeel/hosttools",
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

func TestRegistryIsExplicit(t *testing.T) {
	registry := assembly.Registry()
	for _, id := range []string{
		"zeokeel/appinfo",
		"zeokeel-ui/home",
		"zeokeel-ui/about",
		"zeokeel-ui/welcome",
		"zeokeel-ui/toolcards",
		"zeokeel/toolkit",
		"zeokeel/agent",
		"zeokeel/hosttools",
		"zeokeel/automation",
		"zeokeel/update",
		"zeokeel/importer",
		"zeokeel/acp",
		"zeokeel/modelcatalog",
		"zeokeel-ui/interaction",
		"zeokeel/sessionstore",
		"zeokeel/workspace",
		"zeokeel/terminal",
		"zeokeel/skills",
		"zeokeel/memory",
		"zeokeel/approvals",
		"zeokeel-ui/sidebar",
		"zeokeel-ui/session",
		"zeokeel-ui/settings",
		"zeokeel-ui/approval",
		"zeokeel-ui/rightpanel",
		"zeokeel-ui/automation",
		"zeokeel-ui/plugins",
		assembly.GroupModuleID,
	} {
		if !registry.Has(id) {
			t.Fatalf("Registry() lacks module %q", id)
		}
	}
	if got := len(registry.Modules()); got != 29 {
		t.Fatalf("Registry() modules = %d, want 29", got)
	}
}

func TestValidateEmbeddedProducts(t *testing.T) {
	for _, product := range assembly.Products() {
		if err := assembly.Validate(product); err != nil {
			t.Errorf("Validate(%s): %v", product, err)
		}
	}
}

// --- desktop product: UI group loads, contributions register, close unwinds ---

func TestDesktopAssemblyLoadsAndUnwinds(t *testing.T) {
	root := newRoot(t)
	uiRoot := provideHostServices(t, root, true)

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{
		Version:   "9.9.9",
		Commit:    "deadbeef",
		BuildTime: "2026-11-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(desktop): %v", err)
	}

	// Backend modules and the UI group with its eight UI modules are all active
	// after the synchronous load: the store provider, the toolkit provider, the
	// approvals provider, the agent consumer and the UI modules settle in
	// dependency order.
	instances := loader.Instances()
	if len(instances) != 27 {
		t.Fatalf("desktop instances = %d, want 27", len(instances))
	}
	wantIDs := []string{
		"appinfo", "store", "approvals", "workspace", "terminal", "skills", "memory", "toolkit", "hosttools",
		"agent", "automation", "update", "importer", "modelcatalog", "ui",
		"home", "toolcards", "about", "sidebar", "session", "settings", "approval", "rightpanel",
		"automation-ui", "plugins-ui", "interaction", "toasts",
	}
	gotIDs := make([]string, len(instances))
	for index, instance := range instances {
		gotIDs[index] = instance.ID()
		if state := instance.State(); state != cordis.Active {
			t.Fatalf("instance %q state = %s, want Active", instance.ID(), state)
		}
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("desktop instance IDs = %v, want %v", gotIDs, wantIDs)
	}
	group, ok := loader.Instance("ui")
	if !ok || group.ModuleID() != assembly.GroupModuleID {
		t.Fatalf("ui entry = %+v (found=%v), want a %q instance", group, ok, assembly.GroupModuleID)
	}
	if warnings := loader.Warnings(); len(warnings) != 0 {
		t.Fatalf("desktop warnings = %v, want none", warnings)
	}

	// The UI modules registered their contributions during activation: the UI
	// root has one screen and validates.
	if err := uiRoot.Validate(); err != nil {
		t.Fatalf("uiroot Validate after load: %v", err)
	}
	if screens := uiRoot.ScreenLister().Screens(); !slices.Equal(screens, []string{"home", "automation", "session", "plugins"}) {
		t.Fatalf("screens after load = %v, want [home automation session plugins]", screens)
	}
	// The modules contributed to the host's reserved slots (ui/welcome) and to
	// the settings surface (ui/about) without the host knowing about them.
	slots := uiRoot.Runtime().Slots()
	for _, want := range []string{
		contracts.SlotSidebarPrimary,
		contracts.SlotSidebarNav,
		contracts.SlotSidebarFooter,
		contracts.SlotSettingsHeader,
		contracts.SlotOverlayDialogs,
	} {
		if !slices.Contains(slots, want) {
			t.Fatalf("registered slots = %v, want it to contain %q", slots, want)
		}
	}

	// The toolkit registry is the product's shared tool catalog: the hosttools
	// module registered its six tools into it during activation.
	toolkitRegistry, ok := root.Get(contracts.KeyToolkit)
	if !ok {
		t.Fatal("KeyToolkit service is not available on the root")
	}
	hostToolNames := []string{"ask_user", "exit_plan_mode", "goal", "plan_review", "present", "team"}
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

	// Build identity from the host was overlaid onto the appinfo module config.
	info, ok := root.Get(contracts.KeyAppInfo)
	if !ok {
		t.Fatal("KeyAppInfo service is not available on the root")
	}
	if info.Version != "9.9.9" || info.Commit != "deadbeef" || info.BuildTime != "2026-11-06T12:00:00Z" {
		t.Fatalf("appinfo = %+v, want the injected build identity", info)
	}

	// Closing the loader disposes the assembly: the appinfo service is
	// withdrawn, the UI group cascades, and every contribution is unregistered.
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
	if _, ok := root.Get(contracts.KeyAppInfo); ok {
		t.Fatal("appinfo service survived loader.Close")
	}
	if screens := uiRoot.ScreenLister().Screens(); len(screens) != 0 {
		t.Fatalf("screens after close = %v, want none", screens)
	}
	if err := uiRoot.Validate(); err == nil {
		t.Fatal("uiroot Validate after close succeeded, want no-screen error")
	}
	// Module disposal unregistered every host tool through the kernel effect,
	// leaving only the toolkit module's own tools in the shared registry.
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

func TestHeadlessAssemblyNeedsNoUIServices(t *testing.T) {
	root := newRoot(t)
	// Deliberately no uiroot: the headless host provides settings and log only.
	provideHostServices(t, root, false)

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
	if len(instances) != 14 {
		t.Fatalf("headless instances = %d, want 14", len(instances))
	}
	instance := instances[0]
	if instance.ID() != "appinfo" || instance.ModuleID() != "zeokeel/appinfo" {
		t.Fatalf("headless instance = %s/%s, want appinfo/zeokeel/appinfo", instance.ID(), instance.ModuleID())
	}
	if state := instance.State(); state != cordis.Active {
		t.Fatalf("appinfo state = %s, want Active", state)
	}
	hostInstance := instances[8]
	if hostInstance.ID() != "hosttools" || hostInstance.ModuleID() != "zeokeel/hosttools" {
		t.Fatalf("headless instance[8] = %s/%s, want hosttools/zeokeel/hosttools",
			hostInstance.ID(), hostInstance.ModuleID())
	}
	if state := hostInstance.State(); state != cordis.Active {
		t.Fatalf("hosttools state = %s, want Active", state)
	}
	// Host tools are registered even in a non-interactive product: the catalog
	// is stable, only the ask_user capability is unavailable.
	toolkitRegistry, ok := root.Get(contracts.KeyToolkit)
	if !ok {
		t.Fatal("KeyToolkit service is not available on the headless root")
	}
	registered := make(map[string]bool, len(toolkitRegistry.Specs()))
	for _, spec := range toolkitRegistry.Specs() {
		registered[spec.Name] = true
	}
	for _, name := range []string{"ask_user", "exit_plan_mode", "goal", "plan_review", "present", "team"} {
		if !registered[name] {
			t.Fatalf("headless toolkit registry lacks host tool %q", name)
		}
	}
	if warnings := loader.Warnings(); len(warnings) != 0 {
		t.Fatalf("headless warnings = %v, want none", warnings)
	}
	// The product genuinely has no UI: no UI service was provided and the
	// headless assembly never asks for one.
	if _, ok := root.Get(contracts.KeyUIRuntime); ok {
		t.Fatal("headless root unexpectedly has a UI runtime service")
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
	homeEntry := module.Entry{ID: "home", Module: "zeokeel-ui/home"}

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
			entries: []module.Entry{homeEntry},
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
	provideHostServices(t, root, true)

	if err := assembly.ValidateEntries([]module.Entry{{ID: "home", Module: "zeokeel-ui/home"}}); err == nil {
		t.Fatal("invalid entry tree passed validation")
	}
	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(desktop) after a failed preflight: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("loader.Close: %v", err)
	}
}
