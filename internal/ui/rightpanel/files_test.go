package rightpanel_test

// Frame tests for the right sidebar Files page and the opened-file tabs: the
// workspace tree with lazy directory loading, the artifact history section, and
// the read-only text/Markdown/image/unsupported previews plus the diff fold.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// tinyPNG is a valid 1x1 PNG for the image preview test.
var tinyPNG = func() []byte {
	data, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	return data
}()

// setDirectoryPicker replaces the open-folder dialog seam for one test.
func setDirectoryPicker(t *testing.T, fn func(title string) (string, error)) {
	t.Helper()
	previous := rightpanel.DirectoryPicker
	rightpanel.DirectoryPicker = fn
	t.Cleanup(func() { rightpanel.DirectoryPicker = previous })
}

// workspaceHarness builds the sidebar with one current workspace.
func workspaceHarness(t *testing.T, workspace contracts.WorkspaceService) *harness {
	t.Helper()
	return newHarness(t, harnessOptions{Workspace: workspace})
}

// assertPreviewContains checks the official code editor's accessibility value:
// source text is painted on the editor's canvas, so the a11y text field is the
// user-visible content's programmatic equivalent.
func assertPreviewContains(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	found := false
	settled := pumpFrames(tester, func() bool {
		for _, label := range []string{"文件内容", "File content"} {
			if info, ok := tester.AccessByRole(ui.RoleTextField, label); ok {
				found = true
				return strings.Contains(info.Value, want)
			}
		}
		return false
	}, 3*time.Second)
	if settled {
		return
	}
	if !found {
		t.Fatalf("file preview editor is not exposed as a text field; texts = %v", tester.Texts())
	}
	t.Fatalf("file preview does not contain %q; texts = %v", want, tester.Texts())
}

func assertContains(t *testing.T, tester *ui.Tester, fragments ...string) {
	t.Helper()
	settled := pumpFrames(tester, func() bool {
		frame := strings.Join(tester.Texts(), "\n")
		for _, fragment := range fragments {
			if !strings.Contains(frame, fragment) {
				return false
			}
		}
		return true
	}, 3*time.Second)
	if !settled {
		t.Fatalf("frame does not contain %v; texts = %v", fragments, tester.Texts())
	}
}

// --- workspace tree ---------------------------------------------------------

func TestFilesPageWithoutBackend(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "工作区未启用")
}

func TestFilesOpenWorkspaceFlow(t *testing.T) {
	setDirectoryPicker(t, func(title string) (string, error) {
		if title != "选择工作区文件夹" {
			t.Fatalf("dialog title = %q, want 选择工作区文件夹", title)
		}
		return "/work/proj", nil
	})
	workspace := newFakeWorkspace()
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "还没有打开工作区", "打开文件夹")
	h.click(tester, "打开文件夹")
	h.assertTexts(tester, "工作区", "proj")
	if calls := workspace.creates(); len(calls) != 1 || calls[0].root != "/work/proj" {
		t.Fatalf("Create calls = %+v, want one /work/proj", calls)
	}
}

func TestFilesOpenWorkspaceErrors(t *testing.T) {
	setDirectoryPicker(t, func(string) (string, error) {
		return "", errors.New("no picker")
	})
	h := workspaceHarness(t, newFakeWorkspace())
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "打开文件夹")
	h.assertTexts(tester, "无法打开文件夹选择器：no picker")
}

// TestFilesTreeListsRootAndLazilyOpensDirectories: the workspace tree shows the
// root entries; opening a directory loads its children off the UI thread.
func TestFilesTreeListsRootAndLazilyOpensDirectories(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("docs", true), fileEntry("README.md", false))
	workspace.setDir("docs", contracts.FileEntry{Name: "guide.md", Path: "docs/guide.md"})
	workspace.setFile("README.md", []byte("# 根说明\n"))
	workspace.setFile("docs/guide.md", []byte("# 指南\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "proj", "docs", "README.md")

	// Collapsed: the directory's children are not read yet.
	if calls := workspace.listed(); calls != 1 {
		t.Fatalf("Files calls = %d before expanding, want only the root listing", calls)
	}
	h.click(tester, "docs")
	h.assertTexts(tester, "guide.md")
	h.click(tester, "guide.md")
	h.assertTexts(tester, "关闭 guide.md", "指南")
}

func TestFilesTreeErrors(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.filesErr = errors.New("permission denied")
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "列目录失败：permission denied")

	workspace2 := newFakeWorkspace()
	workspace2.currentErr = errors.New("broken registry")
	h2 := workspaceHarness(t, workspace2)
	tester2 := h2.tester()
	h2.openPage(tester2, "文件")
	h2.assertTexts(tester2, "读取工作区失败：broken registry")
}

func TestFilesEmptyWorkspace(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "目录为空")
}

func TestFilesRefreshReloadsTree(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("first.md", false))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "first.md")

	workspace.setDir("", fileEntry("second.md", false))
	h.click(tester, "重新读取目录")
	h.assertTexts(tester, "second.md")
	h.assertNoTexts(tester, "first.md")
}

func TestFilesTreeScrolls(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	entries := make([]contracts.FileEntry, 0, 40)
	for index := 0; index < 40; index++ {
		entries = append(entries, fileEntry(fmt.Sprintf("file-%02d.md", index), false))
	}
	workspace.setDir("", entries...)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	tester.SetSize(1100, 420)
	h.openPage(tester, "文件")
	tester.Frame()
	// The page body is a native scroll: a wheel over the tree must not panic and
	// the content stays findable.
	tester.Wheel(1050, 260, 0, 1)
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("proj") {
		t.Fatalf("the workspace header disappeared after scrolling; texts = %v", tester.Texts())
	}
}

// --- file tab previews ------------------------------------------------------

func TestFileTabMarkdownPreview(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	workspace.setFile("README.md", []byte("# 标题\n\n正文段落\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "README.md")
	h.assertTexts(tester, "关闭 README.md", "标题", "正文段落")
}

func TestFileTabTextPreview(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("main.go", false))
	workspace.setFile("main.go", []byte("package main\n\nfunc main() {}\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "main.go")
	assertPreviewContains(t, tester, "func main()")
}

func TestFileTabImagePreview(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("pic.png", false))
	workspace.setFile("pic.png", tinyPNG)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "pic.png")
	tester.Frame()
	h.assertTexts(tester, "关闭 pic.png")
	h.assertNoTexts(tester, "读取文件失败", "暂不支持预览")
}

func TestFileTabUnsupportedPreview(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("data.bin", false))
	workspace.setFile("data.bin", []byte{0x00, 0x01, 0x02})
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "data.bin")
	h.assertTexts(tester, "关闭 data.bin", "暂不支持预览", "大小", "类型", "bin")
}

func TestFileTabReadError(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("hidden.md", false))
	workspace.readErrs["hidden.md"] = errors.New("permission denied")
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "hidden.md")
	h.assertTexts(tester, "读取文件失败：permission denied")
}

// TestFileTabDiffFold: an opened file that the workspace differ reports as
// changed gets the folded unified diff (the Changes pane folded into the file
// tab, so the session's "view changes" action lands here).
func TestFileTabDiffFold(t *testing.T) {
	mainDiff := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,2 +1,2 @@",
		"-old line",
		"+new line",
		" context line",
	}, "\n")
	workspace := newDifferWorkspace(contracts.FileChange{Path: "main.go", Status: "M", Diff: mainDiff})
	workspace.setDir("", fileEntry("main.go", false))
	workspace.setFile("main.go", []byte("package main\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()

	h.openPage(tester, "文件")
	h.click(tester, "main.go")
	h.assertTexts(tester, "关闭 main.go", "查看改动")
	h.assertNoTexts(tester, "@@ -1,2 +1,2 @@") // folded by default

	if err := tester.Click("查看改动"); err != nil {
		t.Fatalf("expand diff: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	assertContains(t, tester, "@@ -1,2 +1,2 @@")
	newSide, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the diff viewer's new side is not exposed; texts = %v", tester.Texts())
	}
	if !strings.Contains(newSide.Value, "new line") {
		t.Fatalf("diff viewer's new side value = %q, want the added line", newSide.Value)
	}
}

func TestFileTabDiffAbsentForCleanFile(t *testing.T) {
	workspace := newDifferWorkspace()
	workspace.setDir("", fileEntry("clean.go", false))
	workspace.setFile("clean.go", []byte("package clean\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "clean.go")
	tester.Frame()
	h.assertNoTexts(tester, "查看改动")
}

// TestFileTabRefreshReloads: the tab header reads the file again on demand.
func TestFileTabRefreshReloads(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("live.md", false))
	workspace.setFile("live.md", []byte("# 第一版\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "live.md")
	h.assertTexts(tester, "第一版")

	workspace.setFile("live.md", []byte("# 第二版\n"))
	h.click(tester, "重新读取文件")
	h.assertTexts(tester, "第二版")
}

func TestFileTabCopyPath(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("copied.md", false))
	workspace.setFile("copied.md", []byte("# x\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "copied.md")
	h.click(tester, "复制路径")
	if got := tester.Clipboard(); got != "copied.md" {
		t.Fatalf("clipboard = %q, want copied.md", got)
	}
}

// --- nested path normalization ----------------------------------------------

func TestPreviewFileWithAbsoluteWorkspacePath(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setFile("docs/guide.md", []byte("# 指南\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	previewer := h.previewer()
	if err := previewer.PreviewFile("/work/proj/docs/guide.md"); err != nil {
		t.Fatalf("PreviewFile: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "关闭 guide.md", "指南")
}

func TestPreviewFileRejectsOutsidePath(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := workspaceHarness(t, workspace)
	if err := h.previewer().PreviewFile("/etc/passwd"); err == nil {
		t.Fatal("PreviewFile accepted a path outside the workspace root")
	}
	if err := h.previewer().PreviewFile(""); err == nil {
		t.Fatal("PreviewFile accepted an empty path")
	}
}
