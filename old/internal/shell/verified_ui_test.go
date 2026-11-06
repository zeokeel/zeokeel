package shell

import (
	"context"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

func countPixelsIn(img *image.RGBA, r image.Rectangle, want color.RGBA) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.RGBAAt(x, y) == want {
				n++
			}
		}
	}
	return n
}

func rectOf(r ui.Rect) image.Rectangle {
	return image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
}

// TestShortcutsDialogListsMessageInputRows covers the composer input actions
// DSH's shortcut reference lists.
func TestShortcutsDialogListsMessageInputRows(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeySlash)
	tt.Frame()
	for _, want := range []string{"Message input", "Send message", "New line", "Shift+Enter"} {
		if !tt.HasText(want) {
			t.Fatalf("shortcuts dialog missing %q: %v", want, tt.Texts())
		}
	}
}

// TestShortcutToggleSidebarOverrideApplies covers the editable Toggle left
// sidebar binding: the edited chord works and the old chord stops working,
// including while Settings is open.
func TestShortcutToggleSidebarOverrideApplies(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.Language = LangEN
	if ok := h.app.setShortcutOverride("toggle_sidebar", "Cmd+Shift+B"); !ok {
		t.Fatal("could not persist the toggle_sidebar override")
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if h.app.sidebarCollapsed {
		t.Fatal("the old Cmd+B still toggles the sidebar")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyB)
	tt.Frame()
	if !h.app.sidebarCollapsed {
		t.Fatal("the edited Cmd+Shift+B did not toggle the sidebar")
	}

	// While Settings is open the modal's own shortcut registration must use
	// the override too.
	h.app.sidebarCollapsed = false
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Settings did not open: %v", h.app.Screen)
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if h.app.sidebarCollapsed {
		t.Fatal("the old Cmd+B still toggles the sidebar above Settings")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyB)
	tt.Frame()
	if !h.app.sidebarCollapsed {
		t.Fatal("the edited Cmd+Shift+B does not toggle the sidebar above Settings")
	}
}

// TestOverlayShortcutUsesEditedBinding covers the Settings modal's New Session
// shortcut: it must honor the edited chord, not the hard-coded Cmd+N.
func TestOverlayShortcutUsesEditedBinding(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	ctx := context.Background()
	if ok := h.app.setShortcutOverride("new_session", "Cmd+Shift+N"); !ok {
		t.Fatal("could not persist the new_session override")
	}
	before, err := h.ctrl.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Settings did not open: %v", h.app.Screen)
	}

	tt.Key(ui.Cmd, ui.KeyN)
	tt.Frame()
	mid, err := h.ctrl.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(mid) != len(before) {
		t.Fatalf("the old Cmd+N still created a session: %d -> %d", len(before), len(mid))
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyN)
	tt.Frame()
	after, err := h.ctrl.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("the edited Cmd+Shift+N did not create a session: %d -> %d", len(before), len(after))
	}
}

// TestSetShortcutOverrideInitializesMap covers the latent nil-map panic.
func TestSetShortcutOverrideInitializesMap(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if h.app.shortcutKeymap != nil {
		t.Fatal("test needs a fresh override map")
	}
	if ok := h.app.setShortcutOverride("toggle_sidebar", "Cmd+Shift+B"); !ok {
		t.Fatal("setShortcutOverride failed")
	}
	if h.app.shortcutOverride("toggle_sidebar") != "Cmd+Shift+B" {
		t.Fatalf("override not readable: %q", h.app.shortcutOverride("toggle_sidebar"))
	}
}

// TestPluginSwitchRendersOnAndOffState covers the shared switch: on is the
// inverse track/knob pair in both themes, for Plugins and Automation.
// distRGBA is the largest per-channel difference between two colors: small
// when the rendered pixel matches the theme color within antialiasing.
func distRGBA(a, b color.RGBA) int {
	d := 0
	for _, p := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}} {
		diff := int(p[0]) - int(p[1])
		if diff < 0 {
			diff = -diff
		}
		if diff > d {
			d = diff
		}
	}
	return d
}

func TestPluginSwitchRendersOnAndOffState(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		theme := session.ThemeLight
		if dark {
			name = "dark"
			theme = session.ThemeDark
		}
		t.Run(name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			if err := h.store.SetSetting(session.SettingTheme, theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			if err := h.ctrl.SetPluginEnabled("agent_teams", true); err != nil {
				t.Fatalf("enable Agent Teams: %v", err)
			}
			if err := h.ctrl.SetPluginEnabled("auto_review", false); err != nil {
				t.Fatalf("disable Auto review: %v", err)
			}
			h.app.Screen = Plugins
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			img := tt.Image()

			on, ok := tt.Find("Enable Agent Teams")
			if !ok {
				t.Fatalf("enabled switch missing: %v", tt.Texts())
			}
			off, ok := tt.Find("Enable Auto Authorization Review")
			if !ok {
				t.Fatalf("disabled switch missing: %v", tt.Texts())
			}
			// The plugin rows use mygo's native ui.Switch: the ON track is
			// the brand accent, the OFF track the theme's neutral mix, and
			// the knob is white in both states. Assert that state semantics
			// rather than the retired hand-drawn palette pins.
			onTrack := img.RGBAAt(int(on.X)+3, int(on.Y+on.H/2))
			onKnob := img.RGBAAt(int(on.X+on.W)-6, int(on.Y+on.H/2))
			offKnob := img.RGBAAt(int(off.X)+6, int(off.Y+off.H/2))
			offTrack := img.RGBAAt(int(off.X+off.W)-6, int(off.Y+off.H/2))

			accent := rgbaOf(design.Light().Accent)
			if dark {
				accent = rgbaOf(design.Dark().Accent)
			}
			if distRGBA(onTrack, accent) > 32 {
				t.Fatalf("%s ON switch track = %v, want brand accent %v", name, onTrack, accent)
			}
			if onKnob.R < 200 || onKnob.G < 200 || onKnob.B < 200 {
				t.Fatalf("%s ON switch knob = %v, want white", name, onKnob)
			}
			if offKnob.R < 200 || offKnob.G < 200 || offKnob.B < 200 {
				t.Fatalf("%s OFF switch knob = %v, want white", name, offKnob)
			}
			if distRGBA(offTrack, accent) <= 32 {
				t.Fatalf("%s OFF switch track = %v looks like the ON accent", name, offTrack)
			}
			if onTrack == offTrack {
				t.Fatalf("%s ON and OFF tracks are identical: %v", name, onTrack)
			}

			// The Automation screen uses the same control.
			if _, err := h.ctrl.Automations().Create(store.Automation{
				Title: "On task", Cron: "@hourly", Enabled: true,
			}); err != nil {
				t.Fatalf("create enabled automation: %v", err)
			}
			if _, err := h.ctrl.Automations().Create(store.Automation{
				Title: "Off task", Cron: "@daily", Enabled: false,
			}); err != nil {
				t.Fatalf("create disabled automation: %v", err)
			}
			h.app.Screen = Automation
			tt.Frame()
			autoOn, ok := tt.Find("Disable On task")
			if !ok {
				t.Fatalf("enabled automation switch missing: %v", tt.Texts())
			}
			autoTrack := tt.Image().RGBAAt(int(autoOn.X)+3, int(autoOn.Y+autoOn.H/2))
			autoKnob := tt.Image().RGBAAt(int(autoOn.X+autoOn.W)-6, int(autoOn.Y+autoOn.H/2))
			if distRGBA(autoTrack, accent) > 32 {
				t.Fatalf("%s automation ON switch track = %v, want brand accent %v", name, autoTrack, accent)
			}
			if autoKnob.R < 200 || autoKnob.G < 200 || autoKnob.B < 200 {
				t.Fatalf("%s automation ON switch knob = %v, want white", name, autoKnob)
			}
		})
	}
}

// TestDarkSendControlUsesThemeFill covers the shared brand disabled Send
// fill: live Home and live Session both paint the accent-derived idle tint
// (sendIdleFill) on the enabled wrapper around the disabled native button, so
// both screens share one brand idle look and the dark fill differs from the
// light one.
func TestDarkSendControlUsesThemeFill(t *testing.T) {
	render := func(t *testing.T, screen Screen, dark bool) (*ui.Tester, *image.RGBA) {
		t.Helper()
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		theme := session.ThemeLight
		if dark {
			theme = session.ThemeDark
		}
		if err := h.store.SetSetting(session.SettingTheme, theme); err != nil {
			t.Fatalf("theme setting: %v", err)
		}
		h.app.Screen = screen
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		return tt, tt.Image()
	}
	wantLight := rgbaOf(sendIdleFill(design.Light()))
	wantDark := rgbaOf(sendIdleFill(design.Dark()))
	if wantDark == wantLight {
		t.Fatalf("dark brand idle fill %v matches the light one %v", wantDark, wantLight)
	}
	label := map[Screen]string{Home: "Send message", Session: "Send"}
	for _, screen := range []Screen{Home, Session} {
		darkTT, darkImg := render(t, screen, true)
		r, ok := darkTT.Find(label[screen])
		if !ok {
			t.Fatalf("screen %v: dark Send control missing: %v", screen, darkTT.Texts())
		}
		if got := darkImg.RGBAAt(int(r.X)+5, int(r.Y+r.H/2)); got != wantDark {
			t.Fatalf("screen %v: dark Send fill = %v, want the brand idle fill %v", screen, got, wantDark)
		}

		lightTT, lightImg := render(t, screen, false)
		lr, ok := lightTT.Find(label[screen])
		if !ok {
			t.Fatalf("screen %v: light Send control missing", screen)
		}
		if got := lightImg.RGBAAt(int(lr.X)+5, int(lr.Y+lr.H/2)); got != wantLight {
			t.Fatalf("screen %v: light Send fill = %v, want the brand idle fill %v", screen, got, wantLight)
		}
	}
}

// TestNarrowSessionTranscriptStaysInsideWindow covers the transcript lines that
// used to paint through the window edge at 1200x800 and 900x600: a line long
// enough to overflow the column if it were not wrapped must stay inside the
// transcript column and never ink the window's last columns or the area past
// the column.
func TestNarrowSessionTranscriptStaysInsideWindow(t *testing.T) {
	bg := rgbaOf(design.Light().BgBase)
	// 400 characters is ~7x the widest transcript column at these sizes: it
	// only fits by wrapping, exactly the old NoWrap regression's trigger.
	longLine := strings.Repeat("x", 400)
	snap := state.NewSnapshot()
	snap.Messages = []state.Message{
		{MsgID: "a1", Role: state.RoleAssistant, Text: "```\n" + longLine + "\n```"},
	}
	for _, size := range [][2]int{{1200, 800}, {900, 600}} {
		h := newLiveSnapshotApp(t, Session, snap)
		tt := ui.NewTester(h.app.View, size[0], size[1])
		tt.Frame()
		img := tt.Image()
		col, ok := tt.Find("Session transcript")
		if !ok {
			t.Fatalf("%dx%d: transcript column missing: %v", size[0], size[1], tt.Texts())
		}
		bottom := 520
		if limit := size[1] - 160; bottom > limit {
			bottom = limit
		}
		// The band must actually cross the transcript content, or the scan
		// below would pass on an empty window.
		ink := 0
		for x := int(col.X); x < int(col.X+col.W); x++ {
			for y := 180; y < bottom; y++ {
				if img.RGBAAt(x, y) != bg {
					ink++
				}
			}
		}
		if ink == 0 {
			t.Fatalf("%dx%d: transcript band is empty; nothing to test", size[0], size[1])
		}
		// No glyph may paint in the window's last columns.
		for x := size[0] - 8; x < size[0]; x++ {
			for y := 180; y < bottom; y++ {
				if got := img.RGBAAt(x, y); got != bg {
					t.Fatalf("%dx%d: transcript ink at window edge (%d,%d) = %v", size[0], size[1], x, y, got)
				}
			}
		}
		// Nor beyond the transcript column's right edge.
		right := int(col.X+col.W) + 1
		for x := right; x < size[0]-8; x++ {
			for y := 180; y < bottom; y++ {
				if got := img.RGBAAt(x, y); got != bg {
					t.Fatalf("%dx%d: transcript ink past the column (%d,%d) = %v", size[0], size[1], x, y, got)
				}
			}
		}
	}
}

// TestWorkspaceTreeNestsChildWorkspaces covers View options > Workspace Tree:
// a child workspace renders indented under its ancestor, before the ancestor's
// sessions.
func TestWorkspaceTreeNestsChildWorkspaces(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	parentRoot := t.TempDir()
	childRoot := parentRoot + "/child"
	parent, err := h.ctrl.CreateWorkspace(context.Background(), "parent-ws", parentRoot)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := h.ctrl.CreateWorkspace(context.Background(), "child-ws", childRoot)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := h.ctrl.SelectWorkspace(context.Background(), parent.ID); err != nil {
		t.Fatalf("select parent: %v", err)
	}
	if _, err := h.ctrl.CreateSessionInWorkspace(context.Background(), child.ID, "child conversation"); err != nil {
		t.Fatalf("child session: %v", err)
	}
	if _, err := h.ctrl.CreateSessionInWorkspace(context.Background(), parent.ID, "parent conversation"); err != nil {
		t.Fatalf("parent session: %v", err)
	}
	if err := h.store.SetSetting(settingSessionsGroupBy, groupByTree); err != nil {
		t.Fatalf("group setting: %v", err)
	}
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	pr, ok := tt.Find("parent-ws")
	if !ok {
		t.Fatalf("parent workspace missing: %v", tt.Texts())
	}
	cr, ok := tt.Find("child-ws")
	if !ok {
		t.Fatalf("child workspace missing: %v", tt.Texts())
	}
	if cr.X <= pr.X {
		t.Fatalf("child workspace x=%v is not indented past the parent x=%v", cr.X, pr.X)
	}
	childSession, ok := tt.Find("child conversation")
	if !ok {
		t.Fatalf("child session missing: %v", tt.Texts())
	}
	parentSession, ok := tt.Find("parent conversation")
	if !ok {
		t.Fatalf("parent session missing: %v", tt.Texts())
	}
	if childSession.Y >= parentSession.Y {
		t.Fatalf("child session y=%v is not before the parent's sessions y=%v", childSession.Y, parentSession.Y)
	}
}

// TestManualSessionOrderPersistsAndRenders covers View options > Manual: a
// persisted session order is applied to the sidebar and reordering rewrites
// it.
func TestManualSessionOrderPersistsAndRenders(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	ws := h.ctrl.CurrentWorkspaceID()
	first, err := h.ctrl.CreateSessionInWorkspace(ctx, ws, "First session")
	if err != nil {
		t.Fatalf("first session: %v", err)
	}
	second, err := h.ctrl.CreateSessionInWorkspace(ctx, ws, "Second session")
	if err != nil {
		t.Fatalf("second session: %v", err)
	}
	if err := h.store.SetSetting(settingSessionsManualSort, "true"); err != nil {
		t.Fatalf("manual setting: %v", err)
	}
	if err := h.store.SetSetting(settingSessionOrder, second+","+first); err != nil {
		t.Fatalf("order setting: %v", err)
	}
	h.app.Screen = Home
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	fr, ok := tt.Find("First session")
	if !ok {
		t.Fatalf("first session row missing: %v", tt.Texts())
	}
	sr, ok := tt.Find("Second session")
	if !ok {
		t.Fatalf("second session row missing: %v", tt.Texts())
	}
	if sr.Y >= fr.Y {
		t.Fatalf("manual order not applied: Second y=%v, First y=%v", sr.Y, fr.Y)
	}

	// A reorder writes the manual sequence and the next frame follows it.
	h.app.reorderSession(first, second, true)
	tt.Frame()
	fr, _ = tt.Find("First session")
	sr, _ = tt.Find("Second session")
	if fr.Y >= sr.Y {
		t.Fatalf("reorder not applied: First y=%v, Second y=%v", fr.Y, sr.Y)
	}
	if got, err := h.store.GetSetting(settingSessionOrder); err != nil || !strings.HasPrefix(got, first) {
		t.Fatalf("persisted order = %q, %v", got, err)
	}
}

// TestTabGroupsArrowNavigationAndSingleSelection covers the three custom tab
// groups: arrows move focus and the selected state follows.
func TestTabGroupsArrowNavigationAndSingleSelection(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Session
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		accent := rgbaOf(design.Light().Accent)
		chat, _ := tt.Find("Chat")
		traj, _ := tt.Find("Trajectory")
		chatBar := image.Rect(int(chat.X), int(chat.Y+chat.H)-3, int(chat.X+chat.W), int(chat.Y+chat.H))
		trajBar := image.Rect(int(traj.X), int(traj.Y+traj.H)-3, int(traj.X+traj.W), int(traj.Y+traj.H))
		if countPixelsIn(tt.Image(), chatBar, accent) == 0 || countPixelsIn(tt.Image(), trajBar, accent) != 0 {
			t.Fatal("initial selected tab is not exactly Chat")
		}
		for i := 0; i < 80 && !tt.Focused("Chat"); i++ {
			tt.Key(0, ui.KeyTab)
		}
		tt.Key(0, ui.KeyRight)
		tt.Frame()
		if h.app.uiState().tab != int(liveTrajectory) || !tt.Focused("Trajectory") {
			t.Fatalf("Right did not select/focus Trajectory: tab=%v", h.app.uiState().tab)
		}
		if countPixelsIn(tt.Image(), trajBar, accent) == 0 || countPixelsIn(tt.Image(), chatBar, accent) != 0 {
			t.Fatal("selected tab is not exactly Trajectory after Right")
		}
	})

	t.Run("right-sidebar", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Session
		h.app.Language = LangEN
		h.app.rightOpen = true
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		for i := 0; i < 80 && !tt.Focused("Start"); i++ {
			tt.Key(0, ui.KeyTab)
		}
		if !tt.Focused("Start") {
			t.Fatalf("right sidebar tab group never took focus: %v", tt.Texts())
		}
		tt.Key(0, ui.KeyRight)
		tt.Frame()
		if h.app.rightTab != filesTab || !tt.Focused("Files") {
			t.Fatalf("Right did not select Files: tab=%v", h.app.rightTab)
		}
		tt.Key(0, ui.KeyEnd)
		tt.Frame()
		if h.app.rightTab != sessionLogTab || !tt.Focused("Log") {
			t.Fatalf("End did not select Log: tab=%v", h.app.rightTab)
		}
	})

	t.Run("automation", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Automation
		h.app.Language = LangEN
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		for i := 0; i < 40 && !tt.Focused("All"); i++ {
			tt.Key(0, ui.KeyTab)
		}
		tt.Key(0, ui.KeyRight)
		tt.Frame()
		if got := h.app.shellUI().automationFilter; got != "enabled" {
			t.Fatalf("automation filter after Right = %q, want enabled", got)
		}
		tt.Key(0, ui.KeyRight)
		tt.Frame()
		if got := h.app.shellUI().automationFilter; got != "inactive" {
			t.Fatalf("automation filter after two Rights = %q, want inactive", got)
		}
	})
}

// TestChinesePluginAccessibleNames covers the hard-coded English accessible
// names on the Chinese live default.
func TestChinesePluginAccessibleNames(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{
		"查看子代理", "启用子代理", "添加插件", "选择添加插件的方式",
		"关于插件", "刷新", "更多实验性插件",
	} {
		if !tt.HasText(want) {
			t.Fatalf("Chinese plugin page missing accessible name %q: %v", want, tt.Texts())
		}
	}
	for _, gone := range []string{"View Subagent", "Enable Subagent", "About plugins", "More experimental plugins"} {
		if tt.HasText(gone) {
			t.Fatalf("Chinese plugin page still exposes %q", gone)
		}
	}

	h.app.Screen = Settings
	tt.Frame()
	if err := tt.Click("内置插件"); err != nil {
		t.Fatalf("open built-in plugins: %v", err)
	}
	tt.Frame()
	if !tt.HasText("配置子代理") || !tt.HasText("启用子代理") {
		t.Fatalf("localized Configure/Enable labels missing: %v", tt.Texts())
	}
	if tt.HasText("Configure Subagent") || tt.HasText("Enable Subagent") {
		t.Fatalf("settings still exposes English accessible names: %v", tt.Texts())
	}
}

// TestConfigurationDialogAccessibleLabels covers the duplicate/mismatched
// names: the database button and the copy action are unique and contain their
// visible text.
func TestConfigurationDialogAccessibleLabels(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	var opened string
	h.app.pluginUI().openPath = func(path string) error {
		opened = path
		return nil
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	clickConfigAction(t, tt)
	if !tt.HasText("Configuration and database") {
		t.Fatalf("dialog title not distinct: %v", tt.Texts())
	}
	if _, ok := tt.Find("Open database file"); !ok {
		t.Fatalf("Open database file label missing: %v", tt.Texts())
	}
	if _, ok := tt.Find("Copy database path"); !ok {
		t.Fatalf("Copy database path label missing: %v", tt.Texts())
	}
	// Opening the database must not dismiss the dialog (the duplicate name
	// used to make it look like the dialog's close action).
	if err := tt.Click("Open database file"); err != nil {
		t.Fatalf("click Open database file: %v", err)
	}
	if !h.app.pluginUI().configOpen {
		t.Fatal("Open database file closed the dialog")
	}
	want := h.app.pluginConfigPath()
	if opened != want {
		t.Fatalf("opened %q, want %q", opened, want)
	}
}

// TestSetDefaultOnlyOnNonDefaultRows covers Settings > Models: the default
// row offers Edit only, and a non-default row offers Set default.
func TestSetDefaultOnlyOnNonDefaultRows(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	first, err := h.ctrl.Models().Create(store.Model{Name: "Only Model", Model: "only"})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	h.app.Screen = Settings
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("open Models: %v", err)
	}
	tt.Frame()
	if tt.HasText("Set default") {
		t.Fatalf("default-only model still offers Set default: %v", tt.Texts())
	}
	second, err := h.ctrl.Models().Create(store.Model{Name: "Second", Model: "second"})
	if err != nil {
		t.Fatalf("create second model: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Set default") {
		t.Fatalf("non-default model row has no Set default: %v", tt.Texts())
	}
	if got, err := h.ctrl.Models().DefaultID(); err != nil || got != first.ID {
		t.Fatalf("default before the click = %q, %v; want %q", got, err, first.ID)
	}
	if err := tt.Click("Set default"); err != nil {
		t.Fatalf("click Set default: %v", err)
	}
	if got, err := h.ctrl.Models().DefaultID(); err != nil || got != second.ID {
		t.Fatalf("default model = %q, %v; want %q", got, err, second.ID)
	}
}
