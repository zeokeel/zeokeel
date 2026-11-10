package rightpanel_test

// Frame tests for the contracts.FilePreviewer the right sidebar provides: the
// session cards' "preview in sidebar" / "view changes" / "open natively"
// entry points.

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/lib/openpath"
)

// withOpenNative replaces the OS launcher seam for one test.
func withOpenNative(t *testing.T, fn func(path string) error) *[]string {
	t.Helper()
	var opened []string
	previous := openpath.Open
	openpath.Open = func(path string) error {
		opened = append(opened, path)
		if fn != nil {
			return fn(path)
		}
		return nil
	}
	t.Cleanup(func() { openpath.Open = previous })
	return &opened
}

// TestPreviewFileOpensTheSidebarAndATab: the session cards' preview action
// opens a file tab and the host's panel even while it was closed.
func TestPreviewFileOpensTheSidebarAndATab(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setFile("docs/guide.md", []byte("# 指南\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	h.uiRoot.SetRightPanelOpen(false)
	tester.Frame()
	if h.uiRoot.RightPanelOpen() {
		t.Fatal("precondition: the panel should start closed")
	}

	if err := h.previewer().PreviewFile("docs/guide.md"); err != nil {
		t.Fatalf("PreviewFile: %v", err)
	}
	if !h.uiRoot.RightPanelOpen() {
		t.Fatal("PreviewFile did not open the host panel")
	}
	tester.Frame()
	h.assertTexts(tester, "关闭 guide.md", "指南")
}

// TestPreviewFileFocusesAnOpenTab: a second preview of the same file focuses
// the existing tab instead of duplicating it.
func TestPreviewFileFocusesAnOpenTab(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setFile("a.md", []byte("# 甲\n"))
	workspace.setFile("b.md", []byte("# 乙\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	previewer := h.previewer()

	for _, path := range []string{"a.md", "b.md", "a.md"} {
		if err := previewer.PreviewFile(path); err != nil {
			t.Fatalf("PreviewFile(%q): %v", path, err)
		}
	}
	tester.Frame()
	texts := tester.Texts()
	if count := countOf(texts, "关闭 a.md"); count != 1 {
		t.Fatalf("a.md has %d tabs, want 1; texts = %v", count, tester.Texts())
	}
	h.assertTexts(tester, "甲", "关闭 b.md")
}

// TestOpenFilesAndOpenTerminalFocusPages: the two page-focus entry points.
func TestOpenFilesAndOpenTerminalFocusPages(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	previewer := h.previewer()

	if err := previewer.OpenFiles(); err != nil {
		t.Fatalf("OpenFiles: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "工作区", "proj")

	if err := previewer.OpenTerminal(); err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "命令未启用")
}

// TestOpenNativeResolvesWorkspaceRelativePath: the native launch action takes
// a workspace-relative file card path and launches its absolute path.
func TestOpenNativeResolvesWorkspaceRelativePath(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := newHarness(t, harnessOptions{Workspace: workspace})
	opened := withOpenNative(t, nil)
	if err := h.previewer().OpenNative("docs/release.md"); err != nil {
		t.Fatalf("OpenNative: %v", err)
	}
	want := filepath.Join("/work/proj", "docs/release.md")
	if len(*opened) != 1 || (*opened)[0] != want {
		t.Fatalf("launched paths = %v, want [%s]", *opened, want)
	}
}

// TestOpenNativeAllowsWorkspaceContainedPaths: a workspace-relative path
// resolves under the live root, and an absolute path inside that root is
// accepted and launched at its absolute form.
func TestOpenNativeAllowsWorkspaceContainedPaths(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := newHarness(t, harnessOptions{Workspace: workspace})
	opened := withOpenNative(t, nil)

	if err := h.previewer().OpenNative("docs/release.md"); err != nil {
		t.Fatalf("OpenNative(relative): %v", err)
	}
	inside := filepath.Join("/work/proj", "README.md")
	if err := h.previewer().OpenNative(inside); err != nil {
		t.Fatalf("OpenNative(absolute inside): %v", err)
	}
	if len(*opened) != 2 ||
		(*opened)[0] != filepath.Join("/work/proj", "docs/release.md") ||
		(*opened)[1] != inside {
		t.Fatalf("launched paths = %v, want the workspace-resolved relative path and the absolute path", *opened)
	}
}

// TestOpenNativeRejectsEscapesAndOutsidePaths pins the workspace containment of
// the native-launch action: it is the same policy as PreviewFile, so a card
// path can never launch a file outside the open workspace.
func TestOpenNativeRejectsEscapesAndOutsidePaths(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := newHarness(t, harnessOptions{Workspace: workspace})
	opened := withOpenNative(t, nil)
	service := h.previewer()

	for _, path := range []string{
		"../secret.txt",
		"docs/../../secret.txt",
		"/etc/passwd",
		"/tmp/zeokeel-out/report.pdf",
	} {
		if err := service.OpenNative(path); err == nil {
			t.Fatalf("OpenNative(%q) launched a path outside the workspace; opened = %v", path, *opened)
		}
	}
	if len(*opened) != 0 {
		t.Fatalf("escaped paths reached the OS launcher: %v", *opened)
	}
}

// TestOpenNativeErrors: an empty path and a failing launcher are reported, not
// swallowed, so the session card can show the fallback.
func TestOpenNativeErrors(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	if err := h.previewer().OpenNative("  "); err == nil {
		t.Fatal("OpenNative accepted an empty path")
	}
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	inside := newHarness(t, harnessOptions{Workspace: workspace})
	withOpenNative(t, func(string) error { return errors.New("no launcher") })
	if err := inside.previewer().OpenNative("notes/x.md"); err == nil {
		t.Fatal("OpenNative swallowed the launcher error")
	}
}

// TestOpenNativeWithoutWorkspace: without a workspace an absolute path and a
// parent-directory escape are rejected; a contained relative path still opens
// (there is no root to resolve it against, so it is launched as given).
func TestOpenNativeWithoutWorkspace(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	opened := withOpenNative(t, nil)
	service := h.previewer()

	for _, path := range []string{"/etc/passwd", "../secret.txt", "./../secret.txt"} {
		if err := service.OpenNative(path); err == nil {
			t.Fatalf("OpenNative(%q) launched without a workspace; opened = %v", path, *opened)
		}
	}
	if len(*opened) != 0 {
		t.Fatalf("rejected paths reached the OS launcher: %v", *opened)
	}
	if err := service.OpenNative("relative/note.md"); err != nil {
		t.Fatalf("OpenNative(contained relative): %v", err)
	}
	if len(*opened) != 1 || (*opened)[0] != "relative/note.md" {
		t.Fatalf("launched paths = %v, want the given path", *opened)
	}
}

// TestPreviewFileWithoutWorkspaceShowsAnHonestErrorTab: a backend-less host
// still opens the tab; the tab explains why it cannot read the file.
func TestPreviewFileWithoutWorkspaceShowsAnHonestErrorTab(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	if err := h.previewer().PreviewFile("notes.md"); err != nil {
		t.Fatalf("PreviewFile: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "关闭 notes.md", "读取文件失败")
}
