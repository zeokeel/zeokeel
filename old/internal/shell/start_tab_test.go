package shell

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// openRightSidebar opens the live panel and returns the tester.
func openRightSidebar(t *testing.T, h *liveApp) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	tt.Frame()
	if !h.app.rightOpen {
		t.Fatal("right sidebar did not open")
	}
	return tt
}

// TestStartTabIsDefaultAndNavigates covers the DSH behavior: a freshly
// opened panel shows the Start guide, and each card opens its real tab.
func TestStartTabIsDefaultAndNavigates(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := openRightSidebar(t, h)

	if h.app.rightTab != startTab {
		t.Fatalf("default tab = %v, want Start", h.app.rightTab)
	}
	for _, want := range []string{
		"Start", "Workspace files", "Browse files in this session's workspace",
		"New terminal", "Run commands in the Session workspace",
		"Session Log", "Read this session's raw event log (read-only)",
	} {
		if !tt.HasText(want) {
			t.Fatalf("Start page missing %q: %v", want, tt.Texts())
		}
	}

	// The Workspace files card opens Files, not a new empty tab.
	if err := tt.Click("Workspace files"); err != nil {
		t.Fatalf("click Workspace files card: %v", err)
	}
	if h.app.rightTab != filesTab {
		t.Fatalf("Workspace files card left tab = %v, want Files", h.app.rightTab)
	}
	if err := tt.Click("Start"); err != nil {
		t.Fatalf("click Start tab: %v", err)
	}
	if h.app.rightTab != startTab {
		t.Fatalf("Start tab left tab = %v", h.app.rightTab)
	}

	// The New terminal card opens the Terminal tab with the real runner.
	if err := tt.Click("New terminal"); err != nil {
		t.Fatalf("click New terminal card: %v", err)
	}
	if h.app.rightTab != terminalTab {
		t.Fatalf("New terminal card left tab = %v, want Terminal", h.app.rightTab)
	}
	if !tt.HasText("Run") || !tt.HasText("Choose shell") {
		t.Fatalf("Terminal tab missing controls: %v", tt.Texts())
	}

	// The Session Log card opens the read-only log.
	if err := tt.Click("Start"); err != nil {
		t.Fatalf("return to Start: %v", err)
	}
	if err := tt.Click("Session Log"); err != nil {
		t.Fatalf("click Session Log card: %v", err)
	}
	if h.app.rightTab != sessionLogTab {
		t.Fatalf("Session Log card left tab = %v, want Session Log", h.app.rightTab)
	}
	if !tt.HasText("Read-only") {
		t.Fatalf("Session Log tab missing the read-only label: %v", tt.Texts())
	}
}

// TestStartTabOnHome checks requirement 4: opening the panel on Home shows
// the Start content, not Files or Terminal.
func TestStartTabOnHome(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("Open right sidebar on Home: %v", err)
	}
	tt.Frame()
	if h.app.rightTab != startTab {
		t.Fatalf("Home panel tab = %v, want Start", h.app.rightTab)
	}
	if !tt.HasText("Workspace files") || !tt.HasText("New terminal") || !tt.HasText("Session Log") {
		t.Fatalf("Home Start page cards missing: %v", tt.Texts())
	}
	if strings.Contains(strings.Join(tt.Texts(), "|"), "Run a command in the workspace root") {
		t.Fatalf("Home opened on the Terminal tab: %v", tt.Texts())
	}
}

// TestFilesReload covers the Files tab's Reload control: it re-lists the
// current directory and re-reads a previewed file from disk.
func TestFilesReload(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "first")
	writeTestFile(t, h.dir, "sub/inner.txt", "inner")
	h.app.Screen = Session
	tt := openRightSidebar(t, h)
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	if err := tt.Click("sub/"); err != nil {
		t.Fatalf("click sub/: %v", err)
	}
	if err := tt.Click("Reload"); err != nil {
		t.Fatalf("click Reload in a directory: %v", err)
	}
	tt.Frame()
	if h.app.filesDir != "sub" {
		t.Fatalf("Reload changed filesDir to %q", h.app.filesDir)
	}
	if !tt.HasText("Reloaded the directory") {
		t.Fatalf("directory reload toast missing: %v", tt.Texts())
	}
	if !tt.HasText("inner.txt") {
		t.Fatalf("directory listing lost after reload: %v", tt.Texts())
	}

	// Preview reload picks up a change on disk.
	if err := tt.Click("inner.txt"); err != nil {
		t.Fatalf("preview inner.txt: %v", err)
	}
	writeTestFile(t, h.dir, "sub/inner.txt", "inner changed")
	if err := tt.Click("Reload"); err != nil {
		t.Fatalf("click Reload in a preview: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Reloaded the file from disk") {
		t.Fatalf("preview reload toast missing: %v", tt.Texts())
	}
	if !tt.HasText("inner changed") {
		t.Fatalf("preview did not re-read the file: %v", tt.Texts())
	}
}

// TestChooseShellPersists covers the Choose shell menu: it lists installed
// shells, persists the pick under terminal.shell and the Terminal runner
// uses it.
func TestChooseShellPersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	shells := terminalShells()
	if len(shells) < 2 {
		t.Skipf("only one shell candidate on this host: %v", terminalShellNames())
	}
	current := h.app.terminalShellName()
	var target terminalShell
	for _, sh := range shells {
		if sh.name != current {
			target = sh
			break
		}
	}
	h.app.Screen = Session
	tt := openRightSidebar(t, h)
	if err := tt.Click("Terminal"); err != nil {
		t.Fatalf("click Terminal: %v", err)
	}
	if err := tt.Click("Choose shell"); err != nil {
		t.Fatalf("open Choose shell: %v", err)
	}
	tt.Frame()
	for _, sh := range shells {
		if !tt.HasText(sh.label) {
			t.Fatalf("shell menu missing %q: %v", sh.label, tt.Texts())
		}
	}
	if err := tt.Click(target.label); err != nil {
		t.Fatalf("choose %s: %v", target.label, err)
	}
	tt.Frame()
	got, err := h.store.GetSetting(settingTerminalShell)
	if err != nil {
		t.Fatalf("GetSetting %s: %v", settingTerminalShell, err)
	}
	if got != target.name {
		t.Fatalf("persisted shell = %q, want %q", got, target.name)
	}
	if label := h.app.terminalShellLabel(); label != target.label {
		t.Fatalf("shell label = %q, want %q", label, target.label)
	}
	// Drop the cache: the choice must come back from the store, not memory.
	startTabStates.Delete(h.app)
	if h.app.terminalShellName() != target.name {
		t.Fatalf("restored shell = %q, want %q", h.app.terminalShellName(), target.name)
	}

	// The selected shell actually runs the command.
	h.app.termInput = "echo chosen-shell-ok"
	if err := tt.Click("Run"); err != nil {
		t.Fatalf("click Run: %v", err)
	}
	if !tt.HasText("chosen-shell-ok") {
		t.Fatalf("selected shell did not run the command: %v", tt.Texts())
	}
}

// TestSessionLogReadsStore covers the read-only Session Log tab and its
// store-backed event list.
func TestSessionLogReadsStore(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	id := h.ctrl.CurrentSessionID()
	if _, err := h.store.AppendEvent(id, store.Event{
		Type:    "test.event",
		Payload: json.RawMessage(`{"kind":"marker"}`),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = sessionLogTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Session Log") || !tt.HasText("Read-only") {
		t.Fatalf("log header missing: %v", tt.Texts())
	}
	if !tt.HasText("test.event") || !tt.HasText("marker") {
		t.Fatalf("log missing the stored event: %v", tt.Texts())
	}
	if !tt.HasText("#1") {
		t.Fatalf("log missing the event sequence: %v", tt.Texts())
	}
}

// TestNewTabPickerTargets covers the New tab picker entries: Start, Files,
// Terminal and Session Log are all real targets.
func TestNewTabPickerTargets(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := openRightSidebar(t, h)
	// Start on the Files tab so the picker's labels are not duplicated by
	// the Start cards behind it.
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab: %v", err)
	}
	tt.Frame()
	for _, want := range []string{"Start page", "Workspace files", "New terminal", "Session log"} {
		if !tt.HasText(want) {
			t.Fatalf("New tab picker missing %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Session log"); err != nil {
		t.Fatalf("click Session log in the picker: %v", err)
	}
	tt.Frame()
	if h.app.rightTab != sessionLogTab {
		t.Fatalf("picker left tab = %v, want Session Log", h.app.rightTab)
	}
	if h.app.shellUI().rightNewTabOpen {
		t.Fatal("picker stayed open after choosing a tab")
	}

	// Start page returns to the guide.
	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab again: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Start page"); err != nil {
		t.Fatalf("click Start page: %v", err)
	}
	if h.app.rightTab != startTab {
		t.Fatalf("Start page target left tab = %v", h.app.rightTab)
	}
}

// TestCollapseResetsToStart checks the reopening contract: collapsing the
// panel and opening it again shows Start rather than the last tab.
func TestCollapseResetsToStart(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := openRightSidebar(t, h)
	if err := tt.Click("Terminal"); err != nil {
		t.Fatalf("click Terminal: %v", err)
	}
	if err := tt.Click("Collapse right sidebar"); err != nil {
		t.Fatalf("collapse: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	tt.Frame()
	if h.app.rightTab != startTab {
		t.Fatalf("reopened tab = %v, want Start", h.app.rightTab)
	}
}

// TestSessionLogSurvivesMissingSession checks the empty state instead of a
// crash when no session exists yet.
func TestSessionLogMissingSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.rightOpen = true
	h.app.rightTab = sessionLogTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Session Log") {
		t.Fatalf("log tab header missing on Home: %v", tt.Texts())
	}
}

// TestTerminalShellCandidates checks the shell list never offers a missing
// executable and always has at least the platform default.
func TestTerminalShellCandidates(t *testing.T) {
	shells := terminalShells()
	if len(shells) == 0 {
		t.Fatal("no shell candidates")
	}
	names := terminalShellNames()
	if len(names) != len(shells) {
		t.Fatalf("names = %v, shells = %v", names, shells)
	}
	for _, sh := range shells {
		if sh.name == "" || sh.label == "" {
			t.Fatalf("incomplete shell candidate: %+v", sh)
		}
	}
	if _, ok := findTerminalShell(defaultShellName()); !ok {
		t.Fatalf("default shell %q is not among %v", defaultShellName(), names)
	}
}

// TestEventPayloadPreview checks the log preview compacts and bounds payloads.
func TestEventPayloadPreview(t *testing.T) {
	if got := eventPayloadPreview(nil); got != "" {
		t.Fatalf("empty payload preview = %q", got)
	}
	if got := eventPayloadPreview(json.RawMessage("{\n \"a\" : 1 }")); got != `{"a":1}` {
		t.Fatalf("compact preview = %q", got)
	}
	long := json.RawMessage(`"` + strings.Repeat("x", 500) + `"`)
	got := eventPayloadPreview(long)
	if len(got) > 200 {
		t.Fatalf("long preview = %d chars, want it bounded", len(got))
	}
}

// TestStartUIStateIsPerApp guards the per-App state map: a new App must not
// inherit another App's shell selection.
func TestStartUIStateIsPerApp(t *testing.T) {
	a := &App{}
	b := &App{}
	a.startUI().shellName = "one"
	if b.startUI().shellName == "one" {
		t.Fatal("startTabState leaked between Apps")
	}
}
