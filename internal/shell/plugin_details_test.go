package shell

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
)

// openSettingsLive opens the Settings modal with Cmd/Ctrl+,.
func openSettingsLive(t *testing.T, tt *ui.Tester) {
	t.Helper()
	tt.Key(ui.Cmd, ui.KeyComma)
	if !tt.HasText("Settings") {
		t.Fatalf("settings dialog missing: %q", tt.Texts())
	}
}

// TestPluginsPageHeaderDialogs covers Refresh, About plugins, Add plugin, the
// add split button and the experimental section's More action.
func TestPluginsPageHeaderDialogs(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	if !tt.HasText("Built-in plugins") {
		t.Fatalf("plugins screen missing: %q", tt.Texts())
	}

	// Refresh re-reads the persisted settings and reports through a toast.
	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings()
	h.app.settingsValue.FontSize = session.FontSizeSmall
	if err := h.ctrl.Settings().Save(session.Settings{FontSize: session.FontSizeLarge}); err != nil {
		t.Fatalf("Save font size: %v", err)
	}
	if err := tt.Click("Refresh"); err != nil {
		t.Fatalf("click Refresh: %v", err)
	}
	if !tt.HasText("Plugins refreshed") {
		t.Fatalf("refresh toast missing: %q", tt.Texts())
	}
	if got := h.app.currentSettings().FontSize; got != session.FontSizeLarge {
		t.Fatalf("refresh did not reload settings: font size = %d", got)
	}

	// About plugins is a real dialog with a description and the version.
	if err := tt.Click("About plugins"); err != nil {
		t.Fatalf("click About plugins: %v", err)
	}
	if !tt.HasText("no third-party registry") {
		t.Fatalf("about dialog description missing: %q", tt.Texts())
	}
	if !tt.HasText("version 0.1.0") {
		t.Fatalf("about dialog version missing: %q", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("close about: %v", err)
	}

	// Add plugin explains the built-in-only policy instead of being dead.
	if err := tt.Click("Add plugin"); err != nil {
		t.Fatalf("click Add plugin: %v", err)
	}
	if !tt.HasText("All plugins are built in") {
		t.Fatalf("add plugin dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("close add: %v", err)
	}

	// The split button's "Choose how to add a plugin" opens the same policy
	// explanation.
	if err := tt.Click("Choose how to add a plugin"); err != nil {
		t.Fatalf("click add split button: %v", err)
	}
	if !tt.HasText("All plugins are built in") {
		t.Fatalf("add split dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("close add split: %v", err)
	}

	// Experimental "More" lists the features and their reasons.
	if err := tt.Click("More experimental plugins"); err != nil {
		t.Fatalf("click experimental More: %v", err)
	}
	if !tt.HasText("Agent Teams is experimental") || !tt.HasText("SenseVoice") {
		t.Fatalf("experimental dialog missing reasons: %q", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("close experimental More: %v", err)
	}
}

// TestPluginDetailEditors covers every built-in detail editor: the fields,
// validation, Save persistence, Cancel and the saved toast.
func TestPluginDetailEditors(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}

	cases := []struct {
		name   string
		fields map[string]string
	}{
		{"Subagent", map[string]string{
			"plugin.subagent.depth": "5",
			"plugin.subagent.model": "deepseek-v4.1-flash",
		}},
		{"Web search", map[string]string{
			"plugin.web_search.endpoint":    "https://search.example.com/v1",
			"plugin.web_search.max_results": "5",
		}},
		{"Agent loop", map[string]string{
			"plugin.agent_loop.max_iterations": "25",
		}},
		{"Shell", map[string]string{
			"plugin.shell.timeout_seconds": "180",
			"plugin.shell.output_cap_kb":   "512",
		}},
	}
	for _, tc := range cases {
		// The Plugins page now names the row from builtinPluginDefs ("Shell
		// tools", PD-08) while the detail editor keeps its own "Shell" title.
		rowName := tc.name
		if rowName == "Shell" {
			rowName = "Shell tools"
		}
		if err := tt.Click("View " + rowName); err != nil {
			t.Fatalf("click View %s: %v", rowName, err)
		}
		if !tt.HasText(tc.name + " settings") {
			t.Fatalf("%s detail dialog missing: %q", tc.name, tt.Texts())
		}
		st := h.app.pluginUI()
		for key, value := range tc.fields {
			p, ok := st.detailFields[key]
			if !ok {
				t.Fatalf("%s detail field %q missing", tc.name, key)
			}
			*p = value
		}
		if err := tt.Click("Save"); err != nil {
			t.Fatalf("save %s: %v", tc.name, err)
		}
		if !tt.HasText("Plugin settings saved") {
			t.Fatalf("%s save toast missing: %q", tc.name, tt.Texts())
		}
		for key, want := range tc.fields {
			got, err := h.store.GetSetting(key)
			if err != nil || got != want {
				t.Fatalf("%s: %s = %q, %v; want %q", tc.name, key, got, err, want)
			}
		}
	}

	// Invalid numbers and URLs keep the dialog open and persist nothing.
	st := h.app.pluginUI()
	if err := tt.Click("View Web search"); err != nil {
		t.Fatalf("click View Web search: %v", err)
	}
	*st.detailFields["plugin.web_search.max_results"] = "99"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save invalid max results: %v", err)
	}
	if !tt.HasText("Max results must be a number between 1 and 5") {
		t.Fatalf("range validation toast missing: %q", tt.Texts())
	}
	if !tt.HasText("Web search settings") {
		t.Fatal("invalid save closed the dialog")
	}
	if got, _ := h.store.GetSetting("plugin.web_search.max_results"); got != "5" {
		t.Fatalf("invalid save persisted %q", got)
	}
	*st.detailFields["plugin.web_search.max_results"] = "5"
	*st.detailFields["plugin.web_search.endpoint"] = "not a url"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save invalid endpoint: %v", err)
	}
	if !tt.HasText("must be an http(s) URL") {
		t.Fatalf("URL validation toast missing: %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel invalid edit: %v", err)
	}

	// Cancel discards a draft.
	if err := tt.Click("View Shell tools"); err != nil {
		t.Fatalf("click View Shell tools: %v", err)
	}
	*st.detailFields["plugin.shell.timeout_seconds"] = "301"
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel Shell: %v", err)
	}
	if got, _ := h.store.GetSetting("plugin.shell.timeout_seconds"); got != "180" {
		t.Fatalf("Cancel persisted %q", got)
	}

	// The input's accessible label focuses it, and typing reaches the draft.
	if err := tt.Click("View Agent loop"); err != nil {
		t.Fatalf("click View Agent loop: %v", err)
	}
	*st.detailFields["plugin.agent_loop.max_iterations"] = ""
	if err := tt.Click("Max iterations (0 = unlimited)"); err != nil {
		t.Fatalf("focus Max iterations input: %v", err)
	}
	tt.Type("30")
	if got := *st.detailFields["plugin.agent_loop.max_iterations"]; got != "30" {
		t.Fatalf("typed max iterations = %q, want 30", got)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save typed Agent loop: %v", err)
	}
	if got, _ := h.store.GetSetting("plugin.agent_loop.max_iterations"); got != "30" {
		t.Fatalf("typed max iterations persisted = %q", got)
	}

	// Reopening loads the persisted values into the draft.
	if err := tt.Click("View Agent loop"); err != nil {
		t.Fatalf("click View Agent loop again: %v", err)
	}
	if got := *st.detailFields["plugin.agent_loop.max_iterations"]; got != "30" {
		t.Fatalf("reopened max iterations = %q, want 30", got)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel Agent loop: %v", err)
	}
}

// TestExperimentalSwitches explains disabled features and persists every
// toggle; built-in switches are labeled and shared with Settings.
func TestExperimentalSwitches(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}

	// The switch is a real labeled control.
	if _, ok := tt.Find("Enable Agent Teams"); !ok {
		t.Fatalf("no labeled Agent Teams switch: %q", tt.Texts())
	}
	if err := tt.Click("Enable Agent Teams"); err != nil {
		t.Fatalf("toggle Agent Teams: %v", err)
	}
	if got, err := h.store.GetSetting("plugin.agent_teams.enabled"); err != nil || got != "true" {
		t.Fatalf("plugin.agent_teams.enabled = %q, %v; want true", got, err)
	}
	if !tt.HasText("Agent Teams is experimental") {
		t.Fatalf("unsupported explanation missing: %q", tt.Texts())
	}
	if err := tt.Click("Enable Agent Teams"); err != nil {
		t.Fatalf("toggle Agent Teams off: %v", err)
	}
	if got, _ := h.store.GetSetting("plugin.agent_teams.enabled"); got != "false" {
		t.Fatalf("plugin.agent_teams.enabled = %q after second toggle", got)
	}
	if !tt.HasText("Plugin disabled") {
		t.Fatalf("disable toast missing: %q", tt.Texts())
	}

	// Voice input explains that its SenseVoice dependency is not shipped.
	if err := tt.Click("Enable Voice input"); err != nil {
		t.Fatalf("toggle Voice input: %v", err)
	}
	if !tt.HasText("SenseVoice") {
		t.Fatalf("voice input explanation missing: %q", tt.Texts())
	}

	// Built-in switches persist the same plugin.<name>.enabled keys.
	if err := tt.Click("Enable Web search"); err != nil {
		t.Fatalf("toggle Web search: %v", err)
	}
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || got {
		t.Fatalf("web search after switch = %v, %v; want false", got, err)
	}
	if err := tt.Click("Enable Agent loop"); err != nil {
		t.Fatalf("toggle Agent loop: %v", err)
	}
	if got, err := h.store.GetSetting("plugin.agent_loop.enabled"); err != nil || got != "false" {
		t.Fatalf("plugin.agent_loop.enabled = %q, %v; want false", got, err)
	}

	// The experimental name opens an explanation dialog too. The page now has
	// six built-in rows (PD-08), so Developer Tools can start below the fold;
	// its reachability by scrolling is covered by
	// TestPluginsScrollsAtMinimumHeight, so this checks the dialog itself.
	h.app.openPluginInstance("Developer Tools")
	tt.Frame()
	if !tt.HasText("inspect tool for raw session data") {
		t.Fatalf("developer tools explanation missing: %q", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("close developer tools info: %v", err)
	}
}

// clickConfigAction presses the Settings header's data-folder action. The
// visible label is owned by settings_ui.go/i18n.go, so accept either the new
// "Open data folder" or the old "Open configuration file" spelling while
// those files are updated separately.
func clickConfigAction(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for _, label := range []string{"Open data folder", "Open configuration file"} {
		if err := tt.Click(label); err == nil {
			return
		}
	}
	t.Fatalf("no data-folder action in the Settings header: %q", tt.Texts())
}

// TestSettingsGeneralNewControls covers the explicit Close button, the
// data-folder dialog with its copy and open actions, and the font controls.
func TestSettingsGeneralNewControls(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	openSettingsLive(t, tt)

	// The explicit Close button dismisses the modal (the X still works too).
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("click Close: %v", err)
	}
	if h.app.Screen != Session {
		t.Fatalf("Close left screen %v, want the Session it was opened from", h.app.Screen)
	}
	openSettingsLive(t, tt)

	// The action targets the data directory, so it always opens the dialog
	// that names the folder and highlights index.sqlite inside it.
	wantPath := filepath.Join(h.store.Dir(), "index.sqlite")
	clickConfigAction(t, tt)
	if !tt.HasText("Configuration and database") || !tt.HasText("Data folder") ||
		!tt.HasText(h.store.Dir()) || !tt.HasText(wantPath) {
		t.Fatalf("data-folder dialog missing folder/database: %q", tt.Texts())
	}

	// The headless tester has no desktop backend, so Open folder reports
	// unavailable in the dialog rather than blocking.
	if err := tt.Click("Open folder"); err != nil {
		t.Fatalf("click Open folder: %v", err)
	}
	if !tt.HasText("Could not open the system file manager") {
		t.Fatalf("default config fallback missing: %q", tt.Texts())
	}

	// Copy database path copies the highlighted database path, not the
	// directory, and its accessible name contains its visible text.
	if _, ok := tt.Find("Copy database path"); !ok {
		t.Fatalf("config copy button has no unique Copy database path label: %q", tt.Texts())
	}
	if err := tt.Click("Copy database path"); err != nil {
		t.Fatalf("click Copy database path: %v", err)
	}
	if got := tt.Clipboard(); got != wantPath {
		t.Fatalf("clipboard = %q, want %q", got, wantPath)
	}
	if !tt.HasText("Database path copied") {
		t.Fatalf("copy toast missing: %q", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("close default config dialog: %v", err)
	}

	// An injected opener that fails also reports in the dialog and receives
	// the data directory (not the database file).
	called := ""
	h.app.pluginUI().openPath = func(path string) error {
		called = path
		return errors.New("no file manager in this session")
	}
	clickConfigAction(t, tt)
	if err := tt.Click("Open folder"); err != nil {
		t.Fatalf("click Open folder with failing opener: %v", err)
	}
	if called != h.store.Dir() {
		t.Fatalf("opener path = %q, want %q", called, h.store.Dir())
	}
	if !tt.HasText("Could not open the system file manager") || !tt.HasText(wantPath) {
		t.Fatalf("config dialog missing path: %q", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("close config dialog: %v", err)
	}

	// A working opener opens the data folder and reports through a toast.
	h.app.pluginUI().openPath = func(string) error { return nil }
	clickConfigAction(t, tt)
	if err := tt.Click("Open folder"); err != nil {
		t.Fatalf("click Open folder with working opener: %v", err)
	}
	if !tt.HasText("Opened data folder") {
		t.Fatalf("opened-folder toast missing: %q", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("close config dialog: %v", err)
	}

	// Increase/Decrease step the persisted conversation font size.
	if err := tt.Click("Increase font size"); err != nil {
		t.Fatalf("click Increase font size: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FontSize != session.FontSizeLarge {
		t.Fatalf("font size after Increase = %d, want 16", cfg.FontSize)
	}
	if err := tt.Click("Decrease font size"); err != nil {
		t.Fatalf("click Decrease font size: %v", err)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil || cfg.FontSize != session.FontSizeSmall {
		t.Fatalf("font size after Decrease = %d, %v; want 14", cfg.FontSize, err)
	}

	// More font settings opens a popover that keeps the 14/16 choices.
	if err := tt.Click("More font settings"); err != nil {
		t.Fatalf("click More font settings: %v", err)
	}
	if !tt.HasText("Conversation text supports 14 or 16 px") {
		t.Fatalf("more font settings popover missing: %q", tt.Texts())
	}
	if err := tt.Click("16 px"); err != nil {
		t.Fatalf("click 16 px in more font settings: %v", err)
	}
	cfg, err = h.ctrl.Settings().Load()
	if err != nil || cfg.FontSize != session.FontSizeLarge {
		t.Fatalf("font size after More = %d, %v; want 16", cfg.FontSize, err)
	}
	if got := h.app.ConversationFontSize(); got != 16 {
		t.Fatalf("ConversationFontSize = %v, want 16", got)
	}
}

// TestSettingsStaticPages covers Built-in plugins (shared data with the
// Plugins page), Agent presets (persisted agent.preset), About (version,
// build info, links/copy) and that Models still opens.
func TestSettingsStaticPages(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	openSettingsLive(t, tt)

	// Built-in plugins: same defs/keys as the Plugins page, with a Configure
	// action for the four configurable plugins.
	if err := tt.Click("Built-in plugins"); err != nil {
		t.Fatalf("click Built-in plugins: %v", err)
	}
	for _, label := range []string{"Subagent", "Web search", "Agent loop", "Shell tools", "Skills", "MCP servers"} {
		if !tt.HasText(label) {
			t.Fatalf("built-in page missing %q: %q", label, tt.Texts())
		}
	}
	if err := tt.Click("Configure Web search"); err != nil {
		t.Fatalf("click Configure Web search: %v", err)
	}
	if !tt.HasText("Web search settings") {
		t.Fatalf("configure dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel configure: %v", err)
	}
	if err := tt.Click("Enable Agent loop"); err != nil {
		t.Fatalf("toggle Agent loop in settings: %v", err)
	}
	if got, err := h.store.GetSetting("plugin.agent_loop.enabled"); err != nil || got != "false" {
		t.Fatalf("plugin.agent_loop.enabled = %q, %v; want false", got, err)
	}

	// Agent presets: every preset with its description and Set default.
	if err := tt.Click("Agent presets"); err != nil {
		t.Fatalf("click Agent presets: %v", err)
	}
	for _, label := range []string{"Standard mode", "PTC mode", "Minimal mode", "Creator mode"} {
		if !tt.HasText(label) {
			t.Fatalf("agent presets page missing %q: %q", label, tt.Texts())
		}
	}
	if !tt.HasText("Better suited to tasks that call tools in batches") {
		t.Fatalf("preset description missing: %q", tt.Texts())
	}
	if err := tt.Click("Set default PTC mode"); err != nil {
		t.Fatalf("click Set default PTC mode: %v", err)
	}
	if got, err := h.store.GetSetting("agent.preset"); err != nil || got != "ptc" {
		t.Fatalf("agent.preset = %q, %v; want ptc", got, err)
	}
	if !tt.HasText("Default preset: PTC mode") {
		t.Fatalf("set default toast missing: %q", tt.Texts())
	}
	if !tt.HasText("Default") {
		t.Fatalf("default badge missing: %q", tt.Texts())
	}

	// About: version, build info, links and copy actions.
	opened := ""
	h.app.pluginUI().openExternal = func(url string) error {
		opened = url
		return nil
	}
	if err := tt.Click("About"); err != nil {
		t.Fatalf("click About: %v", err)
	}
	if !tt.HasText("version 0.1.0") || !tt.HasText("Go go") || !tt.HasText("github.com/zeokeel/zeokeel") {
		t.Fatalf("about page missing version/build info: %q", tt.Texts())
	}
	if err := tt.Click("Open project repository"); err != nil {
		t.Fatalf("click project link: %v", err)
	}
	if opened != "https://github.com/zeokeel/zeokeel" {
		t.Fatalf("opened URL = %q", opened)
	}
	if err := tt.Click("Copy version"); err != nil {
		t.Fatalf("click Copy version: %v", err)
	}
	if got := tt.Clipboard(); got != "0.1.0" {
		t.Fatalf("version clipboard = %q", got)
	}
	if err := tt.Click("Copy build info"); err != nil {
		t.Fatalf("click Copy build info: %v", err)
	}
	if got := tt.Clipboard(); !strings.Contains(got, "ZeoKeel 0.1.0") || !strings.Contains(got, "Go ") {
		t.Fatalf("build clipboard = %q", got)
	}

	// Models still opens and its Add model dialog is reachable.
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("click Models: %v", err)
	}
	if !tt.HasText("Models") {
		t.Fatalf("models page missing: %q", tt.Texts())
	}
	if err := tt.Click("Add model"); err != nil {
		t.Fatalf("click Add model: %v", err)
	}
	if !tt.HasText("Base URL") {
		t.Fatalf("model dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel model dialog: %v", err)
	}
}

// TestSettingsNavBuiltinMatchesPluginsPage checks that the two surfaces share
// the persisted plugin keys: a toggle in Settings changes the Plugins page.
func TestSettingsNavBuiltinMatchesPluginsPage(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	openSettingsLive(t, tt)
	if err := tt.Click("Built-in plugins"); err != nil {
		t.Fatalf("click Built-in plugins: %v", err)
	}
	if err := tt.Click("Enable Web search"); err != nil {
		t.Fatalf("toggle Web search in settings: %v", err)
	}
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || got {
		t.Fatalf("web_search after settings toggle = %v, %v", got, err)
	}
	tt.Key(0, ui.KeyEscape)
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	if _, ok := tt.Find("Enable Web search"); !ok {
		t.Fatalf("plugins page lost the shared switch: %q", tt.Texts())
	}
	if err := tt.Click("Enable Web search"); err != nil {
		t.Fatalf("toggle back from Plugins: %v", err)
	}
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || !got {
		t.Fatalf("web_search after Plugins toggle = %v, %v", got, err)
	}
}

// TestWebSearchDetailRequiresAdapterAndAPIKey covers the setup regression:
// the editor no longer defaults to the DeepSeek chat API, exposes an API-key
// field persisted under the key the runtime reads, accepts an empty endpoint
// (the runtime reads it as unconfigured), and refuses a non-URL endpoint.
func TestWebSearchDetailRequiresAdapterAndAPIKey(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	if err := tt.Click("View Web search"); err != nil {
		t.Fatalf("click View Web search: %v", err)
	}
	if !tt.HasText("Web search settings") {
		t.Fatalf("web search detail dialog missing: %q", tt.Texts())
	}

	// No chat-API default: an unconfigured install must show an empty field
	// and register no web_search tool rather than one that always 401s.
	st := h.app.pluginUI()
	endpoint, ok := st.detailFields["plugin.web_search.endpoint"]
	if !ok {
		t.Fatal("endpoint field missing")
	}
	if *endpoint != "" {
		t.Fatalf("endpoint default = %q, want empty (no chat API default)", *endpoint)
	}
	if got := h.app.pluginSetting("plugin.web_search.endpoint", ""); got != "" {
		t.Fatalf("persisted default endpoint = %q, want empty", got)
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "api.deepseek.com") {
			t.Fatalf("web search editor still defaults to the chat API: %q", text)
		}
	}
	keyField, ok := st.detailFields["websearch.api_key"]
	if !ok {
		t.Fatal("API key field under websearch.api_key missing")
	}
	if !tt.HasText("adapter") || !tt.HasText("Bearer token") {
		t.Fatalf("web search setup text missing: %q", tt.Texts())
	}

	// A blank endpoint saves: the runtime already treats "" as
	// "web_search not configured", and the first Save of an install must
	// not fail just because the optional adapter is unset.
	*endpoint = ""
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save blank endpoint: %v", err)
	}
	if !tt.HasText("Plugin settings saved") {
		t.Fatalf("blank endpoint save toast missing: %q", tt.Texts())
	}
	if _, stillOpen := tt.Find("Web search settings"); stillOpen {
		t.Fatal("blank endpoint save left the editor open")
	}
	if got, err := h.store.GetSetting("plugin.web_search.endpoint"); err != nil || got != "" {
		t.Fatalf("blank endpoint persisted %q, %v; want empty", got, err)
	}

	// A real adapter URL and key persist under the runtime keys.
	if err := tt.Click("View Web search"); err != nil {
		t.Fatalf("reopen web search: %v", err)
	}
	st = h.app.pluginUI()
	endpoint = st.detailFields["plugin.web_search.endpoint"]
	keyField = st.detailFields["websearch.api_key"]
	*endpoint = "http://127.0.0.1:9/search"
	*keyField = "secret-token"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save configured web search: %v", err)
	}
	if !tt.HasText("Plugin settings saved") {
		t.Fatalf("save toast missing: %q", tt.Texts())
	}
	for key, want := range map[string]string{
		"plugin.web_search.endpoint": "http://127.0.0.1:9/search",
		"websearch.api_key":          "secret-token",
	} {
		if got, err := h.store.GetSetting(key); err != nil || got != want {
			t.Fatalf("%s = %q, %v; want %q", key, got, err, want)
		}
	}

	// Clearing the configured endpoint through the UI stops web search
	// without disabling the whole plugin.
	if err := tt.Click("View Web search"); err != nil {
		t.Fatalf("reopen configured web search: %v", err)
	}
	st = h.app.pluginUI()
	*st.detailFields["plugin.web_search.endpoint"] = ""
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("clear configured endpoint: %v", err)
	}
	if got, err := h.store.GetSetting("plugin.web_search.endpoint"); err != nil || got != "" {
		t.Fatalf("cleared endpoint = %q, %v; want empty", got, err)
	}
}

// TestAgentLoopStepCeilingBounds pins the Agent loop plugin editor after the
// unlimited-by-default change: the field defaults to 0 (no fixed ceiling, the
// model decides when to end), 0 is a valid persisted choice meaning unlimited,
// a positive safety cap up to the editor limit is accepted, and negative or
// too-large values are rejected without persisting.
func TestAgentLoopStepCeilingBounds(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	openEditor := func() {
		t.Helper()
		if err := tt.Click("View Agent loop"); err != nil {
			t.Fatalf("view Agent loop: %v", err)
		}
	}
	openEditor()
	st := h.app.pluginUI()
	field := "plugin.agent_loop.max_iterations"
	if runtime.DefaultMaxIterations != 0 {
		t.Fatalf("runtime.DefaultMaxIterations = %d, want 0 (DSH-aligned unlimited default)", runtime.DefaultMaxIterations)
	}
	if got, want := *st.detailFields[field], strconv.Itoa(runtime.DefaultMaxIterations); got != want {
		t.Fatalf("default max iterations = %q, want the runtime default %q", got, want)
	}
	boundsToast := fmt.Sprintf("Max iterations (0 = unlimited) must be a number between 0 and %d", runtime.MaxIterationsLimit)

	// Explicit 0 (= unlimited) is valid and persisted as the user's choice.
	*st.detailFields[field] = "0"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save zero: %v", err)
	}
	if got, _ := h.store.GetSetting(field); got != "0" {
		t.Fatalf("zero persisted = %q, want \"0\" (explicit unlimited)", got)
	}

	// Negative values are outside the editor's 0 lower bound and never persist.
	openEditor()
	*st.detailFields[field] = "-5"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save negative: %v", err)
	}
	if !tt.HasText(boundsToast) {
		t.Fatalf("negative range toast missing: %q", tt.Texts())
	}
	if got, _ := h.store.GetSetting(field); got != "0" {
		t.Fatalf("negative save changed setting to %q", got)
	}

	// Values above the editor limit are rejected the same way.
	*st.detailFields[field] = strconv.Itoa(runtime.MaxIterationsLimit + 1)
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save out-of-range: %v", err)
	}
	if !tt.HasText(boundsToast) {
		t.Fatalf("range toast missing: %q", tt.Texts())
	}
	if got, _ := h.store.GetSetting(field); got != "0" {
		t.Fatalf("out-of-range save changed setting to %q", got)
	}

	// The upper bound itself is accepted and persisted as an explicit cap.
	*st.detailFields[field] = strconv.Itoa(runtime.MaxIterationsLimit)
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save upper bound: %v", err)
	}
	if got, _ := h.store.GetSetting(field); got != strconv.Itoa(runtime.MaxIterationsLimit) {
		t.Fatalf("upper bound persisted = %q, want %d", got, runtime.MaxIterationsLimit)
	}
}
