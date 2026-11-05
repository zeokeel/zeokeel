package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// probeDump logs the visible labels of one live UI state.
func probeDump(t *testing.T, name string, tt *ui.Tester) {
	t.Helper()
	t.Logf("--- %s", name)
	for _, s := range tt.Texts() {
		if strings.TrimSpace(s) != "" {
			t.Logf("  %q", s)
		}
	}
}

// TestProbeLiveScreens dumps the controls the live shell renders. It is a
// discovery probe; the durable verifier tests are in verifier_probe_test.go.
func TestProbeLiveScreens(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	app := h.app
	app.Screen = Home
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	probeDump(t, "home", tt)
	if err := tt.Click("More"); err != nil {
		t.Logf("more click: %v", err)
	} else {
		tt.Frame()
		probeDump(t, "more menu", tt)
	}

	// Attachment menu.
	if err := clickAddControl(t, tt); err != nil {
		t.Logf("plus click: %v", err)
	} else {
		tt.Frame()
		probeDump(t, "attachment menu", tt)
	}

	// Workspace picker.
	app.attachOpen = false
	if err := tt.Click("Workspace, current: test"); err != nil {
		t.Logf("workspace chip click: %v", err)
	} else {
		tt.Frame()
		probeDump(t, "workspace picker", tt)
	}

	// Plugins.
	app.Screen = Plugins
	tt.Frame()
	probeDump(t, "plugins", tt)
	if err := tt.Click("About plugins"); err == nil {
		tt.Frame()
		probeDump(t, "about plugins", tt)
		tt.Click("Close")
		tt.Frame()
	}
	if err := tt.Click("View Subagent"); err == nil {
		tt.Frame()
		probeDump(t, "plugin detail", tt)
		tt.Click("Cancel")
		tt.Frame()
	}
	if err := tt.Click("View Agent Teams"); err == nil {
		tt.Frame()
		probeDump(t, "experimental detail", tt)
		tt.Click("Close")
		tt.Frame()
	}

	// Automation with a task.
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Probe task", Cron: "@hourly", Enabled: true,
	}); err != nil {
		t.Logf("create automation: %v", err)
	}
	app.Screen = Automation
	tt.Frame()
	probeDump(t, "automation", tt)

	// Settings all tabs.
	app.Screen = Settings
	tt.Frame()
	probeDump(t, "settings general", tt)
	for _, tab := range []string{"Models", "Built-in plugins", "Agent presets", "About"} {
		if err := tt.Click(tab); err != nil {
			t.Logf("settings tab %q: %v", tab, err)
			continue
		}
		tt.Frame()
		probeDump(t, "settings "+tab, tt)
	}
	tt.Click("General")
	tt.Frame()
	if err := tt.Click("Edit shortcuts"); err == nil {
		tt.Frame()
		probeDump(t, "shortcuts dialog", tt)
	}

	// Session with one completed turn.
	app.Screen = Session
	tt.Frame()
	probeDump(t, "session", tt)
	if err := tt.Click("More actions"); err == nil {
		tt.Frame()
		probeDump(t, "session more", tt)
	}
}

// ---------------------------------------------------------------------------
// Durable verifier tests for the controls this change fixed.

// TestVerifierHomeRightSidebarToggle: the Home header's panel control used to
// be an unlabeled Box with no handler (DSH: "Open right sidebar"). It now
// opens and collapses the real right sidebar on Home.
func TestVerifierHomeRightSidebarToggle(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("Home has no Open right sidebar control: %v", err)
	}
	tt.Frame()
	if !h.app.rightOpen {
		t.Fatal("Open right sidebar did not open the panel")
	}
	if !tt.HasText("Files") || !tt.HasText("Terminal") {
		t.Fatalf("right sidebar tabs missing on Home: %v", tt.Texts())
	}
	// Split and Fullscreen must actually change the Home layout, not just
	// light up (rightSidebarSplit/Maximized used to be Session-only).
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("click Split on Home: %v", err)
	}
	tt.Frame()
	if !h.app.rightSidebarSplit() {
		t.Fatal("Split on Home did not put the panel in split mode")
	}
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("toggle Split off: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatalf("click Fullscreen on Home: %v", err)
	}
	tt.Frame()
	if !h.app.rightSidebarMaximized() {
		t.Fatal("Fullscreen on Home did not maximize the panel")
	}
	if err := tt.Click("Exit fullscreen"); err != nil {
		t.Fatalf("exit fullscreen: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Collapse right sidebar"); err != nil {
		t.Fatalf("Collapse right sidebar: %v", err)
	}
	tt.Frame()
	if h.app.rightOpen {
		t.Fatal("Collapse right sidebar did not close the panel")
	}
}

// TestVerifierAutomationSwitchToggles: the per-task switch used to be a
// drawing with no handler and no accessible name.
func TestVerifierAutomationSwitchToggles(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	task, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Verifier task", Cron: "@hourly", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.app.Screen = Automation
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Disable Verifier task"); err != nil {
		t.Fatalf("task switch has no accessible control: %v", err)
	}
	tt.Frame()
	got, err := h.ctrl.Automations().Get(task.ID)
	if err != nil || got.Enabled {
		t.Fatalf("switch did not persist disabled: enabled=%v err=%v", got.Enabled, err)
	}
	if err := tt.Click("Enable Verifier task"); err != nil {
		t.Fatalf("switch has no enable label after disable: %v", err)
	}
	tt.Frame()
	got, err = h.ctrl.Automations().Get(task.ID)
	if err != nil || !got.Enabled {
		t.Fatalf("switch did not persist enabled: enabled=%v err=%v", got.Enabled, err)
	}
}

// TestVerifierAttachmentMenuDoesRealWork: both + menu entries used to only
// close the menu (and the Session log entry had no viewer at all).
func TestVerifierAttachmentMenuDoesRealWork(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	if err := os.WriteFile(filepath.Join(h.dir, "verifier-notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	clickAddControl(t, tt)
	tt.Frame()
	if tt.HasText("Session log") {
		t.Fatalf("attachment menu still offers an unsupported session log: %v", tt.Texts())
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	tt.Frame()
	if h.app.draft != "@" {
		t.Fatalf("Files entry draft = %q, want @", h.app.draft)
	}
	if !tt.HasText("verifier-notes.md") {
		t.Logf("draft=%q root=%q entries=%v", h.app.draft, h.ctrl.WorkspaceRoot(), h.app.mentionEntries())
		t.Fatalf("Files entry did not open the mention list: %v", tt.Texts())
	}
	h.app.draft = ""
	tt.Frame()
	clickAddControl(t, tt)
	tt.Frame()
	if err := tt.Click("Commands"); err != nil {
		t.Fatalf("click Commands: %v", err)
	}
	tt.Frame()
	if h.app.draft != "/" {
		t.Fatalf("Commands entry draft = %q, want /", h.app.draft)
	}
	if !tt.HasText("/help") {
		t.Fatalf("Commands entry did not open the command palette: %v", tt.Texts())
	}
}

// TestVerifierRightNewTabMenu: New tab used to silently reset the Files tab.
// It now offers the two tabs this build has, like DSH's picker.
func TestVerifierRightNewTabMenu(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Workspace files") || !tt.HasText("New terminal") {
		t.Fatalf("New tab menu entries missing: %v", tt.Texts())
	}
	if err := tt.Click("New terminal"); err != nil {
		t.Fatalf("click New terminal: %v", err)
	}
	tt.Frame()
	if h.app.rightTab != terminalTab {
		t.Fatalf("rightTab = %v, want Terminal", h.app.rightTab)
	}
	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab again: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Workspace files"); err != nil {
		t.Fatalf("click Workspace files: %v", err)
	}
	tt.Frame()
	if h.app.rightTab != filesTab {
		t.Fatalf("rightTab = %v, want Files", h.app.rightTab)
	}
}

// TestVerifierWorkspacePickerAddWorkspace: DSH's picker ends with an add
// action; ours had none once a workspace existed.
func TestVerifierWorkspacePickerAddWorkspace(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Workspace, current: test"); err != nil {
		t.Fatalf("click workspace chip: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Add workspace…") {
		t.Fatalf("workspace picker has no add action: %v", tt.Texts())
	}
	if err := tt.Click("Add workspace…"); err != nil {
		t.Fatalf("click Add workspace…: %v", err)
	}
	tt.Frame()
	if !h.app.wsDialogOpen {
		t.Fatal("Add workspace… did not open the workspace dialog")
	}
}

// TestVerifierShowCodingViewControlsTrajectory: the setting persisted but was
// never read; the Trajectory tab ignored it.
func TestVerifierShowCodingViewControlsTrajectory(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	h.app.Screen = Session
	tt.Frame()
	if !tt.HasText("Trajectory") {
		t.Fatalf("Trajectory tab missing with the default setting: %v", tt.Texts())
	}
	h.app.Screen = Settings
	tt.Frame()
	if err := tt.Click("Toggle show coding view"); err != nil {
		t.Fatalf("show coding view switch is not reachable: %v", err)
	}
	tt.Frame()
	if h.app.currentSettings().ShowCodingView {
		t.Fatal("Show coding view did not persist off")
	}
	if _, ok := tt.Find("Agent presets"); ok {
		t.Fatalf("Agent presets nav still offered with Show coding view off: %v", tt.Texts())
	}
	h.app.Screen = Session
	tt.Frame()
	if tt.HasText("Trajectory") {
		t.Fatalf("Trajectory tab still rendered with Show coding view off: %v", tt.Texts())
	}
	// Turning it back on restores the tab.
	h.app.Screen = Settings
	tt.Frame()
	if err := tt.Click("Toggle show coding view"); err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	h.app.Screen = Session
	tt.Frame()
	if !tt.HasText("Trajectory") {
		t.Fatalf("Trajectory tab did not return: %v", tt.Texts())
	}
	h.app.Screen = Settings
	tt.Frame()
	if _, ok := tt.Find("Agent presets"); !ok {
		t.Fatalf("Agent presets nav did not return with Show coding view on: %v", tt.Texts())
	}
}

// TestVerifierShortcutsReferenceIsComplete: the reference listed four rows
// and omitted shortcuts the shell already had (Cmd/Ctrl+B) plus the new DSH
// bindings.
func TestVerifierShortcutsReferenceIsComplete(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeySlash)
	tt.Frame()
	if !h.app.shortcutsOpen {
		t.Fatal("Cmd/Ctrl+/ did not open the shortcuts reference")
	}
	for _, want := range []string{
		"Cmd/Ctrl+N", "Cmd/Ctrl+,", "Cmd/Ctrl+K", "Cmd/Ctrl+/",
		"Cmd/Ctrl+B", "Cmd/Ctrl+Alt+B", "Cmd/Ctrl+P", "Ctrl+`", "Esc", "Cmd/Ctrl+O",
		"Cmd/Ctrl+Enter", "Cmd/Ctrl+Alt+G", "Cmd/Ctrl+Shift+A",
	} {
		if !tt.HasText(want) {
			t.Fatalf("shortcuts reference missing %q: %v", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.shortcutsOpen {
		t.Fatal("Esc did not close the shortcuts reference")
	}
}

// TestVerifierRightSidebarShortcuts: Cmd/Ctrl+Alt+B, Cmd/Ctrl+P and Ctrl+`
// are DSH bindings the shell was missing.
func TestVerifierRightSidebarShortcuts(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	if !h.app.rightOpen || h.app.rightTab != filesTab {
		t.Fatalf("Cmd/Ctrl+P: rightOpen=%v tab=%v, want Files", h.app.rightOpen, h.app.rightTab)
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	tt.Frame()
	if h.app.rightOpen {
		t.Fatal("Cmd/Ctrl+Alt+B did not close the right sidebar")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	tt.Frame()
	if !h.app.rightOpen {
		t.Fatal("Cmd/Ctrl+Alt+B did not reopen the right sidebar")
	}
	tt.Key(ui.Ctrl, ui.KeyBackquote)
	tt.Frame()
	if h.app.rightTab != terminalTab {
		t.Fatalf("Ctrl+` tab = %v, want Terminal", h.app.rightTab)
	}
}

// TestVerifierEscapeStopsRunningTurn: DSH's response.stop binding; Esc used
// to close overlays only.
func TestVerifierEscapeStopsRunningTurn(t *testing.T) {
	h := newLiveApp(t, &shellProvider{block: true})
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "hold"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, h.ctrl)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	waitIdle(t, h.ctrl)
}

// TestVerifierFeedbackPersists: the thumbs toasts claimed "recorded" while
// writing nothing.
func TestVerifierFeedbackPersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "say done"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	snap := h.ctrl.Snapshot()
	var msgID string
	for _, m := range snap.Messages {
		if m.Role == state.RoleAssistant && m.Text != "" {
			msgID = m.MsgID
		}
	}
	if msgID == "" {
		t.Fatalf("no assistant message id in snapshot: %+v", snap.Messages)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Good response"); err != nil {
		t.Fatalf("click Good response: %v", err)
	}
	key := feedbackSettingKey(snap.SessionID, msgID)
	value, err := h.ctrl.Store().GetSetting(key)
	if err != nil || value != "good" {
		t.Fatalf("feedback setting %q = %q, %v", key, value, err)
	}
}

// TestVerifierAgentPresetDefaultApplies: Set default persisted agent.preset
// but new sessions ignored it.
func TestVerifierAgentPresetDefaultApplies(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Settings
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Agent presets"); err != nil {
		t.Fatalf("open Agent presets: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Set default PTC mode"); err != nil {
		t.Fatalf("set PTC default: %v", err)
	}
	tt.Frame()
	if got := h.app.agentPresetDefault(); got != "ptc" {
		t.Fatalf("agent.preset = %q, want ptc", got)
	}
	h.app.Screen = Home
	tt.Frame()
	if err := tt.Click("New Session"); err != nil {
		t.Fatalf("click New Session: %v", err)
	}
	if got := h.ctrl.Mode(); got != "ptc" {
		t.Fatalf("new session mode = %q, want ptc", got)
	}
}

// waitCallCount waits until the provider has been called n times.
func waitCallCount(t *testing.T, provider *shellProvider, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if provider.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("provider calls = %d, want at least %d", provider.callCount(), n)
}

// TestVerifierSendBehaviorQueue: with Queue selected, Enter during a running
// turn used to be a silent no-op. It now queues the text and submits it after
// the running turn ends.
func TestVerifierSendBehaviorQueue(t *testing.T) {
	provider := &shellProvider{hang: true}
	h := newLiveApp(t, provider)
	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings()
	if h.app.settingsValue.SendBehavior != session.SendQueue {
		t.Fatalf("default send behavior = %q, want queue", h.app.settingsValue.SendBehavior)
	}
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "first"); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	waitRunning(t, h.ctrl)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("second")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.draft != "" {
		t.Fatalf("queued draft = %q, want cleared", h.app.draft)
	}
	if got := h.app.shellUI().queued[id]; got != "second" {
		snap := h.app.Snapshot()
		phase := state.Phase("?")
		sid := ""
		if snap != nil {
			phase = snap.Phase
			sid = snap.SessionID
		}
		t.Logf("queue=%v phase=%v sid=%q current=%q calls=%d draft=%q", h.app.shellUI().queued, phase, sid, h.ctrl.CurrentSessionID(), provider.callCount(), h.app.draft)
		t.Fatalf("queued = %q, want second", got)
	}
	// The running turn ends; the queued message is submitted.
	h.ctrl.Stop()
	tt.Frame()
	waitCallCount(t, provider, 2)
	if got := h.app.shellUI().queued[id]; got != "" {
		t.Fatalf("queue not consumed: %q", got)
	}
}

// TestVerifierSendBehaviorInterrupt: with Interrupt selected (or Cmd/Ctrl+
// Enter under Queue), the running turn is stopped and the new text is sent.
func TestVerifierSendBehaviorInterrupt(t *testing.T) {
	provider := &shellProvider{hang: true}
	h := newLiveApp(t, provider)
	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings()
	h.app.settingsValue.SendBehavior = session.SendInterrupt
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "first"); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	waitRunning(t, h.ctrl)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("second")
	tt.Key(0, ui.KeyEnter)
	waitCallCount(t, provider, 2)
	if got := h.app.shellUI().queued[id]; got != "" {
		t.Fatalf("interrupt path queued instead of sending: %q", got)
	}
	h.ctrl.Stop()
	waitIdle(t, h.ctrl)
}

// TestVerifierCmdEnterUsesOtherBehavior: Cmd/Ctrl+Enter picks the opposite of
// the configured behavior.
func TestVerifierCmdEnterUsesOtherBehavior(t *testing.T) {
	provider := &shellProvider{hang: true}
	h := newLiveApp(t, provider)
	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings() // Queue
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "first"); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	waitRunning(t, h.ctrl)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("second")
	tt.Key(ui.Cmd, ui.KeyEnter)
	waitCallCount(t, provider, 2)
	if got := h.app.shellUI().queued[id]; got != "" {
		t.Fatalf("Cmd/Ctrl+Enter queued instead of using Interrupt: %q", got)
	}
	h.ctrl.Stop()
	waitIdle(t, h.ctrl)
}

// TestVerifierRenameAndArchiveShortcuts: DSH's session shortcuts that map to
// controller APIs this build has.
func TestVerifierRenameAndArchiveShortcuts(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd|ui.Alt, ui.KeyG)
	tt.Frame()
	if !h.app.renameOpen {
		t.Fatal("Cmd/Ctrl+Alt+G did not open the rename dialog")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.renameOpen {
		t.Fatal("Esc did not close the rename dialog")
	}
	sess, err := h.ctrl.CurrentSession()
	if err != nil || sess == nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyA)
	tt.Frame()
	got, err := h.store.GetSessionWithArchive(sess.ID)
	if err != nil || got == nil || !got.Archived {
		t.Fatalf("Cmd/Ctrl+Shift+A did not archive the current session: %+v, %v", got, err)
	}
	if h.app.Screen != Home {
		t.Fatalf("archive left screen %v, want Home with no unarchived session", h.app.Screen)
	}
}

// TestVerifierAddWorkspaceShortcut: DSH binds Cmd/Ctrl+O to Add workspace.
func TestVerifierAddWorkspaceShortcut(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyO)
	tt.Frame()
	if !h.app.wsDialogOpen {
		t.Fatal("Cmd/Ctrl+O did not open the Add workspace dialog")
	}
}

// TestVerifierEscapeDeniesPendingApproval: DSH's approval.reject binding; Esc
// used to stop the running turn instead of denying the pending call.
func TestVerifierEscapeDeniesPendingApproval(t *testing.T) {
	h := newLiveApp(t, &shellProvider{callTool: true})
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "use echo"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	req := waitPendingApproval(t, h.ctrl)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	waitNoPending(t, h.ctrl)
	if h.tool.calls.Load() != 0 {
		t.Fatalf("denied tool ran %d times", h.tool.calls.Load())
	}
	_ = req
}

// TestVerifierSettingsCloseControl: the header X was a clickable glyph with no
// accessible name; it now has one and closes the modal.
func TestVerifierSettingsCloseControl(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Settings
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Close settings"); err != nil {
		t.Fatalf("settings X has no accessible control: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("screen = %v, want Home", h.app.Screen)
	}
}

// TestVerifierTypedSlashCommandExecutes: the palette listed commands, but
// typing one and pressing Enter used to send it to the model as prose.
func TestVerifierTypedSlashCommandExecutes(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("/clear")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if h.app.draft != "" {
		t.Fatalf("typed /clear left draft %q", h.app.draft)
	}
	if provider.callCount() != 0 {
		t.Fatalf("/clear was sent to the model (%d calls)", provider.callCount())
	}
	if tt.HasText("/help") {
		t.Fatalf("command palette still visible after /clear cleared the draft: %v", tt.Texts())
	}
}

// TestVerifierHomeComposerEnterSends: Enter in the Home composer used to do
// nothing (only the Send button worked).
func TestVerifierHomeComposerEnterSends(t *testing.T) {
	for _, mods := range []ui.Modifiers{0, ui.Cmd} {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Home
		before, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		if err := tt.Click("Composer input"); err != nil {
			t.Fatalf("click Home composer: %v", err)
		}
		tt.Type("from home")
		tt.Key(mods, ui.KeyEnter)
		tt.Frame()
		after, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if len(after) != len(before)+1 {
			t.Fatalf("mods=%v: sessions after Enter = %d, want %d", mods, len(after), len(before)+1)
		}
		if h.app.Screen != Session {
			t.Fatalf("mods=%v: screen = %v, want Session", mods, h.app.Screen)
		}
		if h.app.draft != "" {
			t.Fatalf("mods=%v: draft = %q, want cleared", mods, h.app.draft)
		}
	}
}

// TestVerifierPermissionChipOpensMenuNotSettings guards the specific wrong
// target the brief calls out: the access-mode chip must open its own menu, not
// the Settings screen.
func TestVerifierPermissionChipOpensMenuNotSettings(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Access mode, current: " + h.app.composerPermissionLabel(h.app.currentSettings().Permission)); err != nil {
		t.Fatalf("click Access mode: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Access mode navigated to screen %v, want Home", h.app.Screen)
	}
	if !h.app.liveMenus().permissionOpen {
		t.Fatal("Access mode did not open its permission menu")
	}
	for _, want := range []string{"Read Only", "Workspace Write", "Full access"} {
		if !tt.HasText(want) {
			t.Fatalf("permission menu missing %q: %v", want, tt.Texts())
		}
	}
	if _, ok := tt.Find("Open configuration file"); ok {
		t.Fatal("Access mode opened the Settings panel")
	}
}

// TestVerifierViewOptionsPersist: the sidebar's grouping, sort and archive
// filter used to reset on every launch.
func TestVerifierViewOptionsPersist(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("View options"); err != nil {
		t.Fatalf("click View options: %v", err)
	}
	tt.Frame()
	if err := tt.Click("In one list"); err != nil {
		t.Fatalf("click In one list: %v", err)
	}
	tt.Frame()
	// Choosing an option dismisses the popover (DSH behavior), so reopen it
	// before each selection.
	if err := tt.Click("View options"); err != nil {
		t.Fatalf("reopen View options: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Manual"); err != nil {
		t.Fatalf("click Manual: %v", err)
	}
	tt.Frame()
	if err := tt.Click("View options"); err != nil {
		t.Fatalf("reopen View options: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Archived only"); err != nil {
		t.Fatalf("click Archived only: %v", err)
	}
	tt.Frame()
	if raw, err := h.ctrl.Store().GetSetting(settingSessionsManualSort); err != nil || raw != "true" {
		t.Fatalf("%s = %q, %v", settingSessionsManualSort, raw, err)
	}
	if raw, err := h.ctrl.Store().GetSetting(settingSessionsGroupBy); err != nil || raw != groupByList {
		t.Fatalf("%s = %q, %v", settingSessionsGroupBy, raw, err)
	}
	if raw, err := h.ctrl.Store().GetSetting(settingSessionsArchiveFilter); err != nil || raw != archiveArchived {
		t.Fatalf("%s = %q, %v", settingSessionsArchiveFilter, raw, err)
	}
	// A fresh App on the same store restores the choices.
	app2 := &App{Ctrl: h.ctrl, Language: LangEN, Screen: Home}
	tt2 := ui.NewTester(app2.View, 1600, 1000)
	tt2.Frame()
	if !app2.manualSort || app2.groupBy != groupByList || app2.archiveFilter != archiveArchived {
		t.Fatalf("restored sort=%v group=%q filter=%q, want true/list/archived",
			app2.manualSort, app2.groupBy, app2.archiveFilter)
	}
}

func clickAddControl(t *testing.T, tt *ui.Tester) error {
	t.Helper()
	for _, label := range []string{"Add attachments", "Add files or run commands", "+"} {
		if err := tt.Click(label); err == nil {
			return nil
		}
	}
	return fmt.Errorf("add control not found")
}
