package shell

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
)

// writeTestFile writes name under root, creating parent directories.
func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

// waitForTT polls frames until the tester shows want, so asynchronous output
// has time to arrive without blocking the view itself.
func waitForTT(t *testing.T, tt *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tt.HasText(want) {
			return
		}
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; texts: %q", want, tt.Texts())
}

// TestRightSidebarFilesBrowser checks the directory listing, breadcrumb
// navigation, plain-text preview and the 256 KB truncation note against a
// temporary workspace root.
func TestRightSidebarFilesBrowser(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	writeTestFile(t, h.dir, "sub/inner.txt", "inner text")
	writeTestFile(t, h.dir, "big.txt", strings.Repeat("x", maxPreviewBytes+10))

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	// A freshly opened panel is DSH's Start page, not Files.
	if !h.app.rightOpen || h.app.rightTab != startTab {
		t.Fatalf("right sidebar open=%v tab=%v, want Start", h.app.rightOpen, h.app.rightTab)
	}
	if !tt.HasText("Workspace files") || !tt.HasText("New terminal") || !tt.HasText("Session Log") {
		t.Fatalf("Start page cards missing: %q", tt.Texts())
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files tab: %v", err)
	}

	if !tt.HasText("Files") || !tt.HasText("Terminal") {
		t.Fatalf("right sidebar tabs missing: %q", tt.Texts())
	}
	if !tt.HasText("note.txt") || !tt.HasText("sub/") || !tt.HasText("big.txt") {
		t.Fatalf("root listing missing entries: %q", tt.Texts())
	}

	// Enter a directory through its row.
	if err := tt.Click("sub/"); err != nil {
		t.Fatalf("click sub/: %v", err)
	}
	if !tt.HasText("inner.txt") {
		t.Fatalf("subdirectory listing missing inner.txt: %q", tt.Texts())
	}
	if !tt.HasText("⌂ test") || !tt.HasText("sub") {
		t.Fatalf("breadcrumb missing root/sub: %q", tt.Texts())
	}

	// Preview a text file.
	if err := tt.Click("inner.txt"); err != nil {
		t.Fatalf("click inner.txt: %v", err)
	}
	if !tt.HasText("inner text") {
		t.Fatalf("preview missing file contents: %q", tt.Texts())
	}
	if !tt.HasText("Plain text preview") {
		t.Fatalf("preview header missing: %q", tt.Texts())
	}

	// The breadcrumb segment returns to that directory's listing.
	if err := tt.Click("sub"); err != nil {
		t.Fatalf("click breadcrumb sub: %v", err)
	}
	if !tt.HasText("inner.txt") || tt.HasText("inner text") {
		t.Fatalf("breadcrumb did not leave the preview: %q", tt.Texts())
	}

	// Climb back to the root and preview from there.
	if err := tt.Click("⌂ test"); err != nil {
		t.Fatalf("click breadcrumb root: %v", err)
	}
	if !tt.HasText("note.txt") {
		t.Fatalf("root listing after breadcrumb: %q", tt.Texts())
	}
	if err := tt.Click("note.txt"); err != nil {
		t.Fatalf("click note.txt: %v", err)
	}
	if !tt.HasText("hello preview") {
		t.Fatalf("note preview missing: %q", tt.Texts())
	}
	if err := tt.Click("Close preview"); err != nil {
		t.Fatalf("close preview: %v", err)
	}

	// A file above 256 KB keeps its head and reports truncation.
	if err := tt.Click("big.txt"); err != nil {
		t.Fatalf("click big.txt: %v", err)
	}
	if !tt.HasText("Preview truncated at 256 KB") {
		t.Fatalf("truncation note missing: %q", tt.Texts())
	}
	if !tt.HasText("Plain text preview") {
		t.Fatalf("truncated preview missing: %q", tt.Texts())
	}
}

// TestRightSidebarTerminalRunsEcho checks the simple command runner: output
// is appended, the working directory is the workspace root, and Clear empties
// it. There is no PTY and no persistent shell.
func TestRightSidebarTerminalRunsEcho(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "cwd marker")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	if err := tt.Click("Terminal"); err != nil {
		t.Fatalf("click Terminal tab: %v", err)
	}

	if !tt.HasText("Run") || !tt.HasText("Clear") {
		t.Fatalf("terminal controls missing: %q", tt.Texts())
	}
	if !tt.HasText("no PTY") {
		t.Fatalf("terminal documentation missing: %q", tt.Texts())
	}

	// echo goes through the platform shell and its standard output is shown.
	h.app.termInput = "echo hello-terminal"
	if err := tt.Click("Run"); err != nil {
		t.Fatalf("click Run echo: %v", err)
	}
	waitForTT(t, tt, "hello-terminal")
	if !tt.HasText("$ echo hello-terminal") {
		t.Fatalf("echo command line missing: %q", tt.Texts())
	}

	// cat resolves relative to the workspace root, proving cmd.Dir.
	h.app.termInput = "cat note.txt"
	if err := tt.Click("Run"); err != nil {
		t.Fatalf("click Run cat: %v", err)
	}
	waitForTT(t, tt, "cwd marker")

	// Clear empties the retained output.
	if err := tt.Click("Clear"); err != nil {
		t.Fatalf("click Clear: %v", err)
	}
	if tt.HasText("hello-terminal") || tt.HasText("cwd marker") {
		t.Fatalf("Clear left terminal output: %q", tt.Texts())
	}

	// The collapse control hides the right sidebar.
	if err := tt.Click("Collapse right sidebar"); err != nil {
		t.Fatalf("collapse right sidebar: %v", err)
	}
	if h.app.rightOpen {
		t.Fatal("Collapse right sidebar left the right sidebar open")
	}
}

// TestSafeJoinRejectsEscapes checks the preview path guard.
func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if _, err := safeJoin(root, "../escape.txt"); err == nil {
		t.Fatal("safeJoin allowed a parent escape")
	}
	if _, err := safeJoin(root, "sub/../../escape.txt"); err == nil {
		t.Fatal("safeJoin allowed a nested escape")
	}
	if got, err := safeJoin(root, "sub/../ok.txt"); err != nil || !strings.HasSuffix(filepath.ToSlash(got), "/ok.txt") {
		t.Fatalf("safeJoin in-root = %q, %v", got, err)
	}
}

// TestRightSidebarTerminalRunDoesNotBlock proves Run starts the command off
// the view goroutine: a two-second command returns immediately and the panel
// switches Run to Stop.
func TestRightSidebarTerminalRunDoesNotBlock(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	if err := tt.Click("Terminal"); err != nil {
		t.Fatalf("click Terminal tab: %v", err)
	}
	h.app.termInput = "sleep 2"
	start := time.Now()
	if err := tt.Click("Run"); err != nil {
		t.Fatalf("click Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("Run blocked the view for %v", elapsed)
	}
	tt.Frame()
	if !tt.HasText("$ sleep 2") {
		t.Fatalf("command line not appended immediately: %q", tt.Texts())
	}
	if !tt.HasText("Stop") {
		t.Fatalf("running command did not switch Run to Stop: %q", tt.Texts())
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, running := h.app.terminalState(); !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, running := h.app.terminalState(); running {
		t.Fatal("command never finished")
	}
	tt.Frame()
	if tt.HasText("Stop") {
		t.Fatalf("Stop remained after the command finished: %q", tt.Texts())
	}
}

// TestRightSidebarResizeDrag covers the docked panel's brand width and its
// native resize grip: the default is the brand 560 DIPs on a wide window, a
// pointer drag widens the panel, and the arrow keys resize it while the grip
// has the focus.
func TestRightSidebarResizeDrag(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	tt.Frame()
	panel, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatalf("right sidebar has no label: %q", tt.Texts())
	}
	if panel.W < rightSidebarW-1 || panel.W > rightSidebarW+1 {
		t.Fatalf("default panel width = %v, want the brand %v", panel.W, rightSidebarW)
	}
	grip, ok := tt.Find("Resize right sidebar")
	if !ok {
		t.Fatalf("right sidebar has no resize grip: %q", tt.Texts())
	}

	// Drag the grip left: the right-docked panel widens.
	y := grip.Y + 200
	tt.Press(grip.X+grip.W/2, y)
	tt.Move(grip.X+grip.W/2-120, y)
	tt.Release(grip.X+grip.W/2-120, y)
	tt.Frame()
	wide, _ := tt.Find("Right sidebar")
	if wide.W < panel.W+100 {
		t.Fatalf("dragged panel width = %v, want > %v", wide.W, panel.W+100)
	}

	// The grip keeps the panel resizable with the keyboard: Left grows it by
	// the theme's spacing step.
	if err := tt.Click("Resize right sidebar"); err != nil {
		t.Fatalf("focus resize grip: %v", err)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	grown, _ := tt.Find("Right sidebar")
	if grown.W <= wide.W {
		t.Fatalf("Left on the grip did not widen the panel: %v -> %v", wide.W, grown.W)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	shrunk, _ := tt.Find("Right sidebar")
	if shrunk.W >= grown.W {
		t.Fatalf("Right on the grip did not narrow the panel: %v -> %v", grown.W, shrunk.W)
	}

	// The panel keeps the main column its minimum at a small window.
	h.app.setRightDockWidth(2000)
	small := ui.NewTester(h.app.View, 900, 600)
	small.Frame()
	clamped, _ := small.Find("Right sidebar")
	if clamped.W > 900-sidebarW-minMainW+1 {
		t.Fatalf("clamped panel width = %v, want the main column to keep %v", clamped.W, minMainW)
	}
}

// TestRenderRightSidebarNativePreview renders the branded right panel for
// review: the Start page (its default landing state) and the Files tab, light
// and dark, at the 1600x1000 design size. previewDir keeps ordinary runs in a
// temporary directory; ZEOKEEL_UPDATE_PREVIEWS=1 refreshes docs/design/preview.
func TestRenderRightSidebarNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name  string
		theme string
	}{
		{"light", session.ThemeLight},
		{"dark", session.ThemeDark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			// A workspace with a readable file tree behind the Files tab.
			writeTestFile(t, h.dir, "README.md", "# ZeoKeel\n")
			writeTestFile(t, h.dir, "design/notes.md", "# notes\n")
			writeTestFile(t, h.dir, "internal/main.go", "package main\n")
			h.app.Language = LangZH
			h.app.Screen = Session
			h.app.rightOpen = true
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			h.app.rightTab = startTab
			writePreview(t, filepath.Join(dir, "right-sidebar-native-"+tc.name+".png"), h.app)
			h.app.rightTab = filesTab
			h.app.filesDir = ""
			writePreview(t, filepath.Join(dir, "right-sidebar-files-"+tc.name+".png"), h.app)
		})
	}
}

// writePreview renders the app at the design size and writes the PNG.
func writePreview(t *testing.T, path string, app *App) {
	t.Helper()
	img := ui.Render(app.View, 1600, 1000, 1)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("preview %s is empty: %v", path, err)
	}
	t.Logf("wrote %s", path)
}
