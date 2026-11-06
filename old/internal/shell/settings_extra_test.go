package shell

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// openSettingsPage opens Settings and clicks one nav page by label.
func openSettingsPage(t *testing.T, h *liveApp, tt *ui.Tester, label string) {
	t.Helper()
	if h.app.Screen != Settings {
		tt.Key(ui.Cmd, ui.KeyComma)
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("open settings page %q: %v", label, err)
	}
	tt.Frame()
}

// TestWave2SettingsPagesRender pins PD-05: the six new pages exist, are
// reachable from the nav and draw their headings.
func TestWave2SettingsPagesRender(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	pages := []struct{ nav, heading string }{
		{"Notifications", "Notifications"},
		{"Data & privacy", "Data & privacy"},
		{"Diagnostics", "Diagnostics"},
		{"Network", "Network"},
		{"Desktop", "Desktop"},
		{"Updates", "Updates"},
	}
	for _, page := range pages {
		openSettingsPage(t, h, tt, page.nav)
		if !tt.HasText(page.heading) {
			t.Fatalf("page %q missing its heading: %q", page.nav, tt.Texts())
		}
	}
	// The About page shows the build identity the host injected.
	if err := tt.Click("About"); err != nil {
		t.Fatalf("open About: %v", err)
	}
	tt.Frame()
	if !tt.HasText(BuildVersionString()) {
		t.Fatalf("About page missing the build identity %q: %q", BuildVersionString(), tt.Texts())
	}
}

// TestNotificationSettingsPersist pins the two notification switches: the
// page writes the keys the app reads.
func TestNotificationSettingsPersist(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Notifications")

	if err := tt.Click("Enable Turn notifications"); err != nil {
		t.Fatalf("toggle turn notifications: %v", err)
	}
	tt.Frame()
	if got, err := h.store.GetSetting(settingNotificationEnabled); err != nil || got != "false" {
		t.Fatalf("%s = %q, %v; want false", settingNotificationEnabled, got, err)
	}
	if err := tt.Click("Enable Silence while focused"); err != nil {
		t.Fatalf("toggle silence: %v", err)
	}
	tt.Frame()
	if got, err := h.store.GetSetting(settingNotificationSuppressFocused); err != nil || got != "false" {
		t.Fatalf("%s = %q, %v; want false", settingNotificationSuppressFocused, got, err)
	}
}

// TestNetworkProxyPersists pins the proxy row: the input writes network.proxy.
func TestNetworkProxyPersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Network")
	h.app.shellUI().textDrafts[settingNetworkProxy] = "http://127.0.0.1:7890"
	tt.Frame()
	if got, err := h.store.GetSetting(settingNetworkProxy); err != nil || got != "http://127.0.0.1:7890" {
		t.Fatalf("%s = %q, %v", settingNetworkProxy, got, err)
	}
}

// TestDesktopCloseBehaviorAndAutostart pins the Desktop page: the close
// behavior selector and the launch-at-login switch persist their keys.
func TestDesktopCloseBehaviorAndAutostart(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Desktop")
	if err := tt.Click("Closing the window, current: Hide"); err != nil {
		t.Fatalf("open close-behavior selector: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Quit"); err != nil {
		t.Fatalf("choose Quit: %v", err)
	}
	tt.Frame()
	if got, err := h.store.GetSetting(settingCloseBehavior); err != nil || got != "quit" {
		t.Fatalf("%s = %q, %v; want quit", settingCloseBehavior, got, err)
	}
	if err := tt.Click("Enable Launch at login"); err != nil {
		t.Fatalf("toggle autostart: %v", err)
	}
	tt.Frame()
	if got, err := h.store.GetSetting(settingAutostart); err != nil || got != "true" {
		t.Fatalf("%s = %q, %v; want true", settingAutostart, got, err)
	}
}

// TestDiagnosticsCopyAndExportLogs pins the Diagnostics support actions.
func TestDiagnosticsCopyAndExportLogs(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	logDir := filepath.Join(h.dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("create log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "app-20261105.log"), []byte("boot ok\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Diagnostics")
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("copy diagnostics: %v", err)
	}
	tt.Frame()
	if got := tt.Clipboard(); !strings.Contains(got, "ZeoKeel "+BuildVersionString()) {
		t.Fatalf("clipboard diagnostics = %q", got)
	}

	target := filepath.Join(t.TempDir(), "logs.zip")
	h.app.SaveFileDialog = func(string, string) (string, error) { return target, nil }
	if err := tt.Click("Export…"); err != nil {
		t.Fatalf("export logs: %v", err)
	}
	tt.Frame()
	names := zipNames(t, target)
	if len(names) == 0 || !strings.Contains(strings.Join(names, ","), "app-20261105.log") {
		t.Fatalf("log archive entries = %v", names)
	}
}

// TestDataPrivacyExportAll pins the redacted all-data export.
func TestDataPrivacyExportAll(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Data & privacy")
	target := filepath.Join(t.TempDir(), "data.zip")
	h.app.SaveFileDialog = func(string, string) (string, error) { return target, nil }
	if err := tt.Click("Export…"); err != nil {
		t.Fatalf("export all data: %v", err)
	}
	tt.Frame()
	names := zipNames(t, target)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "README.txt") || !strings.Contains(joined, "sessions/") {
		t.Fatalf("data archive entries = %v", names)
	}
	// The archive must not carry the settings database or any key material.
	for _, name := range names {
		if strings.Contains(name, ".sqlite") || strings.Contains(name, "api_key") {
			t.Fatalf("data archive leaked a database/key file: %v", names)
		}
	}
}

// TestDataPrivacyResetAndClear pins the two destructive actions: both ask for
// confirmation, cancel keeps the data, confirm performs the action.
func TestDataPrivacyResetAndClear(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := h.ctrl.Settings().Save(session.Settings{FontSize: session.FontSizeLarge}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Data & privacy")

	// Reset settings: cancel first, then confirm.
	if err := tt.Click("Reset…"); err != nil {
		t.Fatalf("open reset confirmation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Reset settings?") {
		t.Fatalf("reset confirmation missing: %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel reset: %v", err)
	}
	tt.Frame()
	cfg, err := h.ctrl.Settings().Load()
	if err != nil || cfg.FontSize != session.FontSizeLarge {
		t.Fatalf("cancel reset changed settings: %+v, %v", cfg, err)
	}
	if err := tt.Click("Reset…"); err != nil {
		t.Fatalf("reopen reset confirmation: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Reset defaults"); err != nil {
		t.Fatalf("confirm reset: %v", err)
	}
	tt.Frame()
	cfg, err = h.ctrl.Settings().Load()
	if err != nil || cfg.FontSize != session.FontSizeSmall {
		t.Fatalf("confirmed reset kept old settings: %+v, %v", cfg, err)
	}

	// Delete all conversations: cancel keeps them, confirm removes them.
	if err := tt.Click("Delete…"); err != nil {
		t.Fatalf("open delete confirmation: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Delete all conversations?") {
		t.Fatalf("delete confirmation missing: %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel delete: %v", err)
	}
	tt.Frame()
	if sessions, _ := h.ctrl.ListSessions(context.Background()); len(sessions) == 0 {
		t.Fatal("cancel delete removed the sessions")
	}
	if err := tt.Click("Delete…"); err != nil {
		t.Fatalf("reopen delete confirmation: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Delete all"); err != nil {
		t.Fatalf("confirm delete: %v", err)
	}
	tt.Frame()
	if sessions, _ := h.ctrl.ListSessions(context.Background()); len(sessions) != 0 {
		t.Fatalf("sessions after confirmed delete = %d, want 0", len(sessions))
	}
	if h.app.Screen != Home {
		t.Fatalf("confirmed delete left the app on %v, want Home", h.app.Screen)
	}
}

// zipNames lists the entry names of a zip archive.
func zipNames(t *testing.T, path string) []string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %q: %v", path, err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

// TestHomeSetupCardGuidesFirstRun pins the first-run card: it appears only
// while something is missing, and its two CTAs open the matching flows.
func TestHomeSetupCardGuidesFirstRun(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if tt.HasText("Finish setting up ZeoKeel") {
		t.Fatalf("setup card shown on a configured app: %q", tt.Texts())
	}

	// No model: the card explains it and offers the model settings.
	h.ctrl.SetAgent(nil, nil)
	tt.Frame()
	if !tt.HasText("Finish setting up ZeoKeel") {
		t.Fatalf("setup card missing without a model: %q", tt.Texts())
	}
	if !tt.HasText("Configure model") {
		t.Fatalf("setup card missing the model CTA: %q", tt.Texts())
	}
	if err := tt.Click("Configure model"); err != nil {
		t.Fatalf("click Configure model: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings || h.app.shellUI().settingsTab != settingsTabModels {
		t.Fatalf("model CTA opened %v/%v, want Settings/Models",
			h.app.Screen, h.app.shellUI().settingsTab)
	}
	h.app.closeSettings()
	h.app.Screen = Home

	// No workspace: the card offers creating one.
	workspaces, err := h.ctrl.ListWorkspaces(context.Background())
	if err != nil || len(workspaces) == 0 {
		t.Fatalf("workspaces = %+v, %v", workspaces, err)
	}
	if err := h.ctrl.DeleteWorkspace(context.Background(), workspaces[0].ID); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Create workspace") {
		t.Fatalf("setup card missing the workspace CTA: %q", tt.Texts())
	}
	if err := tt.Click("Create workspace"); err != nil {
		t.Fatalf("click Create workspace: %v", err)
	}
	tt.Frame()
	if !h.app.wsDialogOpen {
		t.Fatal("workspace CTA did not open the add-workspace dialog")
	}
}

// TestImportFromDSHDialog pins the PD-04 entry: the Data & privacy page shows
// the CLI instructions and copies both commands.
func TestImportFromDSHDialog(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	openSettingsPage(t, h, tt, "Data & privacy")
	if err := tt.Click("Show command…"); err != nil {
		t.Fatalf("open import dialog: %v", err)
	}
	tt.Frame()
	if !tt.HasText("zeokeel import --dry-run") {
		t.Fatalf("import dialog missing the preview command: %q", tt.Texts())
	}
	if !tt.HasText("zeokeel import") {
		t.Fatalf("import dialog missing the import command: %q", tt.Texts())
	}
	// The first Copy copies the preview command.
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("copy command: %v", err)
	}
	if got := tt.Clipboard(); got != "zeokeel import --dry-run --json" {
		t.Fatalf("clipboard = %q", got)
	}
}
