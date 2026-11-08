package uiroot

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestNewDefaults covers the zero-config Root: the product defaults, the five
// services being wired, and an empty registry that fails validation.
func TestNewDefaults(t *testing.T) {
	r := New(Config{})
	if got, want := r.AppName(), "ZeoKeel"; got != want {
		t.Fatalf("AppName() = %q, want %q", got, want)
	}
	if r.Theme().Dark() {
		t.Fatal("a zero-config root must start in the light theme")
	}
	if got, want := r.Locale().Language(), "zh"; got != want {
		t.Fatalf("Language() = %q, want %q", got, want)
	}
	if got := r.Frames(); got != 0 {
		t.Fatalf("Frames() = %d before any frame, want 0", got)
	}
	if r.Runtime() == nil || r.Navigator() == nil || r.Theme() == nil ||
		r.Locale() == nil || r.ScreenLister() == nil {
		t.Fatal("New left one of the host services nil")
	}
	if got := r.ScreenLister().Screens(); len(got) != 0 {
		t.Fatalf("Screens() = %v before registration, want empty", got)
	}
	if got := r.Runtime().Slots(); len(got) != 0 {
		t.Fatalf("Slots() = %v before registration, want empty", got)
	}
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() succeeded with no screen registered, want an error")
	} else {
		requireContains(t, err, "no screen")
	}

	// The explicit configuration wins over the defaults.
	r2 := New(Config{AppName: "Other", Dark: true, Language: "en"})
	if got, want := r2.AppName(), "Other"; got != want {
		t.Fatalf("AppName() = %q, want %q", got, want)
	}
	if !r2.Theme().Dark() {
		t.Fatal("Config.Dark = true did not start the root dark")
	}
	if got, want := r2.Locale().Language(), "en"; got != want {
		t.Fatalf("Language() = %q, want %q", got, want)
	}
	if got, want := r2.Locale().T("nav.settings"), "Settings"; got != want {
		t.Fatalf("T(nav.settings) = %q, want %q", got, want)
	}
}

// TestInterfaceWiring is a compile-time assertion that the Root's services
// satisfy the contracts modules require.
func TestInterfaceWiring(t *testing.T) {
	r := newTestRoot(t)
	var _ contracts.UIRuntime = r.Runtime()
	var _ contracts.Navigator = r.Navigator()
	var _ contracts.ThemeService = r.Theme()
	var _ contracts.LocaleService = r.Locale()
	var _ contracts.ScreenLister = r.ScreenLister()
}

// TestNilInputsAreSafe covers the guard clauses the frame helpers rely on: a
// nil frame context, nil contributions and a nil invalidator must never panic.
func TestNilInputsAreSafe(t *testing.T) {
	r := newTestRoot(t)
	r.View(nil) // the host frame entry tolerates a nil context

	rt := r.Runtime()
	rt.RenderSlot(nil, contracts.SlotSidebarNav)
	if rt.RenderTool(nil, contracts.ToolCall{Name: "shell"}) {
		t.Fatal("RenderTool(nil, ...) = true, want false")
	}
	r.safeBuild(nil, nil)                                                // nil builder guard
	r.safeToolBuild(nil, contracts.ToolRenderer{}, contracts.ToolCall{}) // nil renderer guard
	rt.Post(nil)                                                         // ignored, not queued
	rt.Invalidate()
	if got := r.Frames(); got != 0 {
		t.Fatalf("Frames() = %d after no frame, want 0", got)
	}

	r.SetInvalidator(nil) // clearing a nil invalidator must be safe
	r.SetInvalidator(func() {})
	r.Runtime().Invalidate() // must not panic, must not build a frame by itself
	if got := r.Frames(); got != 0 {
		t.Fatalf("Frames() = %d after Invalidate, want 0 (the host builds frames)", got)
	}
}

// TestScreenRenderingOrderAndDefault covers screen ordering (Order then ID)
// and that the first visible screen is rendered until navigation picks one.
func TestScreenRenderingOrderAndDefault(t *testing.T) {
	r := newTestRoot(t)
	// Registered out of order on purpose.
	mustScreen(t, r, textScreen("beta", "贝塔屏", "body-beta", 20))
	mustScreen(t, r, textScreen("alpha", "阿尔法屏", "body-alpha", 10))
	mustScreen(t, r, textScreen("gamma", "伽马屏", "body-gamma", 10))

	if got, want := r.ScreenLister().Screens(), []string{"alpha", "gamma", "beta"}; !equalStrings(got, want) {
		t.Fatalf("Screens() = %v, want %v (Order then ID)", got, want)
	}
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()

	// The first registered visible screen is the fallback screen; the sidebar
	// destination rows are module contributions, not a host-drawn screen list.
	requireHasText(t, tt, "body-alpha")
	requireNoText(t, tt, "body-gamma")
	requireNoText(t, tt, "body-beta")
}

// TestScreenConflictsAndUnregister covers rejected screens leaving no residue
// and the unregister function being one-shot (idempotent).
func TestScreenConflictsAndUnregister(t *testing.T) {
	r := newTestRoot(t)
	rt := r.Runtime()

	if _, err := rt.RegisterScreen(contracts.ScreenSpec{ID: "", Build: func(*ui.Context) {}}); err == nil {
		t.Fatal("empty screen ID was accepted")
	} else {
		requireContains(t, err, "empty ID")
	}
	if _, err := rt.RegisterScreen(contracts.ScreenSpec{ID: "no-build"}); err == nil {
		t.Fatal("screen with nil Build was accepted")
	} else {
		requireContains(t, err, "nil Build")
	}

	home := mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	if _, err := rt.RegisterScreen(textScreen("home", "重复首页", "body-dup", 20)); err == nil {
		t.Fatal("duplicate screen ID was accepted")
	} else {
		requireContains(t, err, "duplicate screen ID")
		requireContains(t, err, `"home"`)
	}
	if got, want := r.ScreenLister().Screens(), []string{"home"}; !equalStrings(got, want) {
		t.Fatalf("Screens() = %v after a rejected duplicate, want %v (no residue)", got, want)
	}

	home()
	if got := r.ScreenLister().Screens(); len(got) != 0 {
		t.Fatalf("Screens() = %v after unregister, want empty", got)
	}
	home() // idempotent: a second call neither panics nor changes anything

	// A stale unregister must not remove a fresh registration of the same ID.
	_, err := rt.RegisterScreen(textScreen("home", "新首页", "body-new", 10))
	if err != nil {
		t.Fatalf("re-registering home after unregister: %v", err)
	}
	home()
	if got, want := r.ScreenLister().Screens(), []string{"home"}; !equalStrings(got, want) {
		t.Fatalf("Screens() = %v after a stale unregister call, want %v", got, want)
	}
}

// TestSettingsPageOrderingAndConflicts covers the settings page registry:
// ordering by Section then Order then ID, conflicts, and render/removal.
func TestSettingsPageOrderingAndConflicts(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	rt := r.Runtime()

	if _, err := rt.RegisterSettingsPage(contracts.SettingsPageSpec{ID: "", Build: func(*ui.Context) {}}); err == nil {
		t.Fatal("empty settings page ID was accepted")
	} else {
		requireContains(t, err, "empty ID")
	}
	if _, err := rt.RegisterSettingsPage(contracts.SettingsPageSpec{ID: "no-build"}); err == nil {
		t.Fatal("settings page with nil Build was accepted")
	} else {
		requireContains(t, err, "nil Build")
	}

	mustPage(t, r, contracts.SettingsPageSpec{ID: "general-2", Section: "general", Title: "通用二", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "general-2-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "general-1", Section: "general", Title: "通用一", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "general-1-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "general-1b", Section: "general", Title: "通用一B", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "general-1b-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "account", Section: "account", Title: "账户", Order: 50,
		Build: func(c *ui.Context) { ui.Text(c, "account-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "zeta", Section: "zeta", Title: "Z页", Order: 1,
		Build: func(c *ui.Context) { ui.Text(c, "zeta-body") }})
	if _, err := rt.RegisterSettingsPage(contracts.SettingsPageSpec{ID: "account", Section: "general", Title: "重复", Order: 1,
		Build: func(*ui.Context) {}}); err == nil {
		t.Fatal("duplicate settings page ID was accepted")
	} else {
		requireContains(t, err, "duplicate settings page ID")
	}

	tt := newTestTester(t, r, 1600, 1000)
	tt.Frame()
	requireNoText(t, tt, "通用一") // settings pages only show when settings is open

	r.Navigator().ToggleSettings()
	tt.Frame()
	// Sections are grouped by rank (general first), then Order, then ID.
	requireTextBefore(t, tt, "通用一", "通用一B", "通用二", "账户", "Z页")
	// The first page of the sorted list is the selected one.
	requireHasText(t, tt, "general-1-body")
	requireNoText(t, tt, "account-body")

	// Unregistering a page removes it from the surface on the next frame.
	unreg := mustPage(t, r, contracts.SettingsPageSpec{ID: "temp", Section: "general", Title: "临时页", Order: 15,
		Build: func(c *ui.Context) { ui.Text(c, "temp-body") }})
	tt.Frame()
	requireHasText(t, tt, "临时页")
	unreg()
	tt.Frame()
	requireNoText(t, tt, "临时页")
	requireHasText(t, tt, "通用一")

	// Idempotent unregister: a stale call must not remove a reused ID.
	if _, err := rt.RegisterSettingsPage(contracts.SettingsPageSpec{ID: "temp", Section: "general", Title: "新临时页", Order: 15,
		Build: func(c *ui.Context) { ui.Text(c, "temp-new-body") }}); err != nil {
		t.Fatalf("re-registering settings page temp: %v", err)
	}
	unreg()
	tt.Frame()
	requireHasText(t, tt, "新临时页")
}

// TestCommandOrderingFilteringAndRun covers command registration: render order,
// Enabled filtering, errors, and the host running a command on click.
func TestCommandOrderingFilteringAndRun(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	rt := r.Runtime()

	if _, err := rt.RegisterCommand(contracts.CommandSpec{ID: "", Run: func() error { return nil }}); err == nil {
		t.Fatal("empty command ID was accepted")
	} else {
		requireContains(t, err, "empty ID")
	}
	if _, err := rt.RegisterCommand(contracts.CommandSpec{ID: "no-run"}); err == nil {
		t.Fatal("command with nil Run was accepted")
	} else {
		requireContains(t, err, "nil Run")
	}

	var runs counter
	mustCommand(t, r, contracts.CommandSpec{ID: "run", Title: "执行命令", Order: 5, Run: func() error { runs.Inc(); return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "b", Title: "命令B", Order: 20, Run: func() error { return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "a", Title: "命令A", Order: 10, Run: func() error { return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "a2", Title: "命令A2", Order: 10, Run: func() error { return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "always", Title: "可用命令", Order: 12, Enabled: func() bool { return true },
		Run: func() error { return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "failing", Title: "失败命令", Order: 40,
		Run: func() error { return errors.New("command failed") }})
	mustCommand(t, r, contracts.CommandSpec{ID: "hidden", Title: "隐藏命令", Order: 15, Enabled: func() bool { return false },
		Run: func() error { return nil }})
	if _, err := rt.RegisterCommand(contracts.CommandSpec{ID: "a", Title: "重复A", Run: func() error { return nil }}); err == nil {
		t.Fatal("duplicate command ID was accepted")
	} else {
		requireContains(t, err, "duplicate command ID")
	}

	// The command registry exposes the sorted list to palettes/composer menus.
	commands := rt.Commands()
	// The host registers its own window commands too (the inspector toggle).
	wantOrder := []string{"run", "a", "a2", "always", "hidden", "b", "failing", "uiroot.toggle-inspector"}
	if len(commands) != len(wantOrder) {
		t.Fatalf("Commands() = %d commands, want %d", len(commands), len(wantOrder))
	}
	for index, want := range wantOrder {
		if commands[index].ID != want {
			t.Fatalf("Commands()[%d] = %q, want %q", index, commands[index].ID, want)
		}
	}
	if commands[4].Enabled == nil || commands[4].Enabled() {
		t.Fatalf("hidden command = %+v, want Enabled() == false", commands[4])
	}
	if commands[0].Enabled != nil && !commands[0].Enabled() {
		t.Fatalf("run command must stay enabled: %+v", commands[0])
	}
	tt := newTestTester(t, r, 1600, 1000)
	tt.Frame()
	requireHasText(t, tt, "body-home")

	// Commands run through their Run function; the counting one increments.
	if err := commands[0].Run(); err != nil {
		t.Fatalf("run command: %v", err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("command Run called %d times, want 1", got)
	}
	// A command whose Run returns an error reports it to the caller; the host
	// contains it like any other command.
	if err := commands[6].Run(); err == nil {
		t.Fatal("failing command returned nil, want an error")
	}
	if err := commands[0].Run(); err != nil {
		t.Fatalf("run command (second): %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("command Run called %d times, want 2", got)
	}

	unreg := mustCommand(t, r, contracts.CommandSpec{ID: "temp", Title: "临时命令", Order: 30, Run: func() error { return nil }})
	if !hasCommand(rt.Commands(), "temp") {
		t.Fatal("registered command temp missing from Commands()")
	}
	unreg()
	if hasCommand(rt.Commands(), "temp") {
		t.Fatal("unregistered command temp still in Commands()")
	}

	// A second call is idempotent, and a stale closure cannot remove a
	// registration that reuses the same ID afterwards.
	if _, err := rt.RegisterCommand(contracts.CommandSpec{ID: "temp", Title: "新临时命令", Order: 30, Run: func() error { return nil }}); err != nil {
		t.Fatalf("re-registering command temp: %v", err)
	}
	unreg()
	if commands := rt.Commands(); !hasCommandPrefix(commands, "temp") {
		t.Fatalf("stale unregister removed the re-registered command: %+v", commands)
	}
}

// hasCommand reports whether the command list carries the exact ID.
func hasCommand(commands []contracts.CommandSpec, id string) bool {
	for _, command := range commands {
		if command.ID == id {
			return true
		}
	}
	return false
}

// hasCommandPrefix reports whether the command list carries an ID prefix.
func hasCommandPrefix(commands []contracts.CommandSpec, prefix string) bool {
	for _, command := range commands {
		if strings.HasPrefix(command.ID, prefix) {
			return true
		}
	}
	return false
}

// TestSlotDiagnosticsConflictAndUnregister covers the slot registry through its
// diagnostic surface: Slots() is sorted and drops emptied slots, duplicate
// identities are rejected without residue, and invalid items are refused.
func TestSlotDiagnosticsConflictAndUnregister(t *testing.T) {
	r := newTestRoot(t)
	rt := r.Runtime()

	if _, err := rt.RegisterSlotItem(contracts.SlotItem{Slot: "", ID: "x", Build: func(*ui.Context) {}}); err == nil {
		t.Fatal("slot item with empty Slot was accepted")
	} else {
		requireContains(t, err, "Slot and ID")
	}
	if _, err := rt.RegisterSlotItem(contracts.SlotItem{Slot: "sidebar.nav", ID: "", Build: func(*ui.Context) {}}); err == nil {
		t.Fatal("slot item with empty ID was accepted")
	} else {
		requireContains(t, err, "Slot and ID")
	}
	if _, err := rt.RegisterSlotItem(contracts.SlotItem{Slot: "sidebar.nav", ID: "no-build"}); err == nil {
		t.Fatal("slot item with nil Build was accepted")
	} else {
		requireContains(t, err, "nil Build")
	}

	navB := mustSlot(t, r, textSlotItem("sidebar.nav", "b", "nav-b", 20))
	navA := mustSlot(t, r, textSlotItem("sidebar.nav", "a", "nav-a", 10))
	turnX := mustSlot(t, r, textSlotItem("session.transcript.turn", "x", "turn-x", 10))
	if _, err := rt.RegisterSlotItem(textSlotItem("sidebar.nav", "a", "dup-a", 30)); err == nil {
		t.Fatal("duplicate slot item ID in the same slot was accepted")
	} else {
		requireContains(t, err, "duplicate slot item")
	}
	// The same ID is allowed in a different slot: identity is (slot, ID).
	treeA := mustSlot(t, r, textSlotItem("sidebar.tree", "a", "tree-a", 10))

	want := []string{"session.transcript.turn", "sidebar.nav", "sidebar.tree"}
	if got := rt.Slots(); !equalStrings(got, want) {
		t.Fatalf("Slots() = %v, want %v (sorted)", got, want)
	}
	// Slots returns a copy: mutating it must not corrupt the registry.
	got := rt.Slots()
	if len(got) > 0 {
		got[0] = "mutated"
	}
	if again := rt.Slots(); !equalStrings(again, want) {
		t.Fatalf("Slots() after mutating the returned slice = %v, want %v", again, want)
	}

	// Unregistering the last item of a slot drops the slot name.
	navB()
	navA()
	if got, want := rt.Slots(), []string{"session.transcript.turn", "sidebar.tree"}; !equalStrings(got, want) {
		t.Fatalf("Slots() = %v after emptying sidebar.nav, want %v", got, want)
	}
	navB() // idempotent
	navA() // idempotent
	if got, want := rt.Slots(), []string{"session.transcript.turn", "sidebar.tree"}; !equalStrings(got, want) {
		t.Fatalf("Slots() = %v after repeated unregister, want %v", got, want)
	}

	// A stale unregister must not remove a fresh registration of the same ID.
	newB, err := rt.RegisterSlotItem(textSlotItem("sidebar.nav", "b", "nav-b-new", 20))
	if err != nil {
		t.Fatalf("re-registering sidebar.nav/b: %v", err)
	}
	navB()
	if got, want := rt.Slots(), []string{"session.transcript.turn", "sidebar.nav", "sidebar.tree"}; !equalStrings(got, want) {
		t.Fatalf("Slots() = %v after a stale unregister, want %v", got, want)
	}

	newB()
	turnX()
	treeA()
	if got := rt.Slots(); len(got) != 0 {
		t.Fatalf("Slots() = %v after all unregisters, want empty", got)
	}
}
