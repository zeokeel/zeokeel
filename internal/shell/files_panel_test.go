package shell

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// openFilesTab shows the Files tab of the fixture's live session.
func openFilesTab(t *testing.T, h *liveApp) {
	t.Helper()
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
}

// TestFilesBreadcrumbOpensDirectoryInSystemApp covers the Files header's
// directory opener: it hands the absolute path of the directory being browsed
// to the injected OS opener, and a success leaves no panel error behind.
func TestFilesBreadcrumbOpensDirectoryInSystemApp(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "sub/inner.txt", "inner")
	openFilesTab(t, h)
	h.app.filesDir = "sub"
	var opened string
	h.app.filesNativeUI().openPath = func(path string) error {
		opened = path
		return nil
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if _, ok := tt.Find("Open directory in system app"); !ok {
		t.Fatalf("directory opener missing: %v", tt.Texts())
	}
	if err := tt.Click("Open directory in system app"); err != nil {
		t.Fatalf("click directory opener: %v", err)
	}
	tt.Frame()

	if want := filepath.Join(h.dir, "sub"); opened != want {
		t.Fatalf("opener received %q, want the browsed directory %q", opened, want)
	}
	if got := h.app.rightPanelUI().nativeErr; got != "" {
		t.Fatalf("successful open kept the panel error %q", got)
	}
}

// TestFilePreviewOpensAndRevealsInSystemApp covers the preview header's
// native actions: Open hands the file's absolute path to the default-app
// opener and Reveal hands it to the file-manager revealer.
func TestFilePreviewOpensAndRevealsInSystemApp(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	openFilesTab(t, h)
	h.app.openFilePreview("note.txt")
	var opened, revealed string
	h.app.filesNativeUI().openPath = func(path string) error {
		opened = path
		return nil
	}
	h.app.filesNativeUI().revealPath = func(path string) error {
		revealed = path
		return nil
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("hello preview") {
		t.Fatalf("preview body missing: %v", tt.Texts())
	}
	for _, label := range []string{"Open file in system app", "Show in file manager"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("preview action %q missing: %v", label, tt.Texts())
		}
	}
	abs := filepath.Join(h.dir, "note.txt")
	if err := tt.Click("Open file in system app"); err != nil {
		t.Fatalf("click Open: %v", err)
	}
	if opened != abs {
		t.Fatalf("opener received %q, want %q", opened, abs)
	}
	if err := tt.Click("Show in file manager"); err != nil {
		t.Fatalf("click Reveal: %v", err)
	}
	if revealed != abs {
		t.Fatalf("revealer received %q, want %q", revealed, abs)
	}
}

// TestFilesNativeActionFailureShowsNote proves a failed open is reported on
// the panel and by a toast, and that the next success clears the panel note.
func TestFilesNativeActionFailureShowsNote(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	openFilesTab(t, h)
	h.app.openFilePreview("note.txt")
	h.app.filesNativeUI().openPath = func(string) error {
		return errors.New("no handler for that file type")
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Open file in system app"); err != nil {
		t.Fatalf("click Open: %v", err)
	}
	tt.Frame()
	if got := h.app.rightPanelUI().nativeErr; got == "" {
		t.Fatal("failed open left the panel error empty")
	}
	if !tt.HasText("no handler for that file type") {
		t.Fatalf("failed open is not shown: %v", tt.Texts())
	}

	h.app.filesNativeUI().openPath = func(string) error { return nil }
	if err := tt.Click("Open file in system app"); err != nil {
		t.Fatalf("click Open after failure: %v", err)
	}
	if got := h.app.rightPanelUI().nativeErr; got != "" {
		t.Fatalf("successful open kept the earlier panel error %q", got)
	}
}

// TestFilesNativeUnavailableShowsHostNote covers a headless host: with no
// desktop backend the action reports the shared "no desktop" hint (the same
// wording the present card shows) instead of panicking.
func TestFilesNativeUnavailableShowsHostNote(t *testing.T) {
	if mygo.App.IsReady() {
		t.Skip("desktop backend is running: the unavailable branch is not reachable here")
	}
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	openFilesTab(t, h)
	h.app.openFilePreview("note.txt")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Open file in system app"); err != nil {
		t.Fatalf("click Open without a backend: %v", err)
	}
	tt.Frame()
	if got := h.app.rightPanelUI().nativeErr; got == "" {
		t.Fatal("missing backend left the panel error empty")
	}
	if !tt.HasText("no desktop available") {
		t.Fatalf("the no-desktop hint is not shown: %v", tt.Texts())
	}
}

// TestFilePreviewActionsTabReachable keeps the preview's native actions on the
// keyboard path: Tab reaches both, like the rest of the panel.
func TestFilePreviewActionsTabReachable(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	openFilesTab(t, h)
	h.app.openFilePreview("note.txt")
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	assertTabReachable(t, tt, []string{
		"Open file in system app", "Show in file manager",
	})
}

// TestFilesNativeActionsStayInsideNarrowPanel keeps the new header actions in
// the 220 DIP docked panel: neither the preview actions nor the directory
// opener may paint past the panel's edges.
func TestFilesNativeActionsStayInsideNarrowPanel(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	openFilesTab(t, h)
	h.app.openFilePreview("note.txt")
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()

	panel, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatalf("right sidebar missing: %v", tt.Texts())
	}
	for _, label := range []string{
		"Open file in system app", "Show in file manager", "Close preview",
		"Open directory in system app", "Reload",
	} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing from the narrow panel: %v", label, tt.Texts())
		}
		if r.X < panel.X || r.X+r.W > panel.X+panel.W {
			t.Fatalf("%q rect %+v is outside the panel %+v", label, r, panel)
		}
	}

	// A Markdown preview keeps the same panel bounds at the minimum width.
	writeTestFile(t, h.dir, "notes.md", "# 标题\n\n正文\n")
	h.app.openFilePreview("notes.md")
	tt.Frame()
	panel, _ = tt.Find("Right sidebar")
	for _, label := range []string{"Markdown preview", "notes.md", "Close preview"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing from the narrow Markdown preview: %v", label, tt.Texts())
		}
		if r.X < panel.X || r.X+r.W > panel.X+panel.W {
			t.Fatalf("%q rect %+v is outside the panel %+v", label, r, panel)
		}
	}
}

// TestFilesNativeLabelsChinese pins the Chinese labels and the exact
// no-desktop hint the user's reference shows: this Host cannot open files or
// folders externally, but the sidebar preview still works.
func TestFilesNativeLabelsChinese(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello preview")
	h.app.Language = LangZH
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	h.app.openFilePreview("note.txt")

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{
		"在系统中打开文件", "在文件管理器中显示", "在系统中打开目录",
	} {
		if !tt.HasText(want) {
			t.Fatalf("Chinese Files action %q missing: %v", want, tt.Texts())
		}
	}

	h.app.filesNativeUI().openPath = func(string) error { return errPluginNativeUnavailable }
	if err := tt.Click("在系统中打开文件"); err != nil {
		t.Fatalf("click Chinese Open: %v", err)
	}
	tt.Frame()
	if !tt.HasText("此主机没有可用的桌面") {
		t.Fatalf("Chinese no-desktop hint missing: %v", tt.Texts())
	}
}
