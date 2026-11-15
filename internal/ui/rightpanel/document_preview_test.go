package rightpanel_test

// Frame tests for the document-preview external-open fallback: PDF/Office/HTML
// documents and oversized images cannot be rendered in the panel (the build
// ships no embedded PDF/Office/HTML renderer), so the preview explains that and
// offers the platform's default application as its primary action. A build
// without the launcher seam hides the action; text, Markdown and image previews
// stay untouched.

import (
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/lib/openpath"
)

// withOpenNativeNil replaces the OS launcher seam with "not available".
func withOpenNativeNil(t *testing.T) {
	t.Helper()
	previous := openpath.Open
	openpath.Open = nil
	t.Cleanup(func() { openpath.Open = previous })
}

// TestDocumentPreviewOffersSystemOpen covers the unsupported document types:
// the fallback names the missing renderer, keeps the metadata rows, and the
// primary action (body button and header icon) hands the file to the platform.
func TestDocumentPreviewOffersSystemOpen(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("report.pdf", false))
	workspace.setFile("report.pdf", []byte("%PDF-1.7\n"))
	opened := withOpenNative(t, nil)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "report.pdf")

	h.assertTexts(tester, "关闭 report.pdf", "暂不支持预览",
		"本机没有内嵌 PDF、Office 或 HTML 渲染器，无法在侧边栏显示该文件。",
		"PDF 文档需要系统应用打开。", "用系统应用打开")
	// The header carries the same action as an icon.
	h.assertTexts(tester, "用系统应用打开 report.pdf")

	h.click(tester, "用系统应用打开")
	if len(*opened) != 1 || !strings.HasSuffix((*opened)[0], "report.pdf") {
		t.Fatalf("opened paths = %v, want one path ending in report.pdf", *opened)
	}
	h.assertNoTexts(tester, "打开文件失败")
}

// TestDocumentPreviewOfficeAndHTML covers the other document families: the
// explanation names the family and HTML is never rendered as source.
func TestDocumentPreviewOfficeAndHTML(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("brief.docx", false), fileEntry("index.html", false))
	workspace.setFile("brief.docx", []byte{0x50, 0x4b, 0x03, 0x04})
	workspace.setFile("index.html", []byte("<h1>hello</h1>"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")

	h.click(tester, "brief.docx")
	h.assertTexts(tester, "暂不支持预览", "Office 文档需要系统应用打开。", "用系统应用打开")
	h.closeFileTab(tester, "brief.docx")

	h.click(tester, "index.html")
	h.assertTexts(tester, "暂不支持预览", "HTML 页面需要浏览器打开。", "用系统应用打开")
	// The raw HTML source must not be rendered by the text preview.
	h.assertNoTexts(tester, "<h1>hello</h1>")
}

// TestDocumentPreviewOpenErrorIsLocalized covers the launcher failure: the
// error is shown inline with the product's localized prefix.
func TestDocumentPreviewOpenErrorIsLocalized(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("report.pdf", false))
	workspace.setFile("report.pdf", []byte("%PDF-1.7\n"))
	withOpenNative(t, func(string) error { return errors.New("no handler registered") })
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "report.pdf")

	h.assertTexts(tester, "用系统应用打开")
	h.click(tester, "用系统应用打开")
	h.assertTexts(tester, "打开文件失败：no handler registered")
}

// TestDocumentPreviewHidesSystemOpenWithoutLauncher covers a build without the
// launcher seam: the action and its header icon are hidden, and the explanation
// still tells the user why the file is not shown.
func TestDocumentPreviewHidesSystemOpenWithoutLauncher(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("report.pdf", false))
	workspace.setFile("report.pdf", []byte("%PDF-1.7\n"))
	withOpenNativeNil(t)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "report.pdf")

	h.assertTexts(tester, "暂不支持预览",
		"本机没有内嵌 PDF、Office 或 HTML 渲染器，无法在侧边栏显示该文件。")
	h.assertNoTexts(tester, "用系统应用打开")
}

// TestDocumentPreviewOversizedImageFallsBackExternally covers the oversized
// image: instead of a bare read error the panel explains the embedded limit and
// offers the system application.
func TestDocumentPreviewOversizedImageFallsBackExternally(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("huge.png", false))
	// Over the 16 MiB image read bound: the preview never reaches the decoder.
	workspace.setFile("huge.png", make([]byte, (16<<20)+1))
	opened := withOpenNative(t, nil)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "huge.png")

	h.assertTexts(tester, "暂不支持预览", "图片超过内嵌预览上限，请用系统应用查看。", "用系统应用打开")
	h.click(tester, "用系统应用打开")
	if len(*opened) != 1 || !strings.HasSuffix((*opened)[0], "huge.png") {
		t.Fatalf("opened paths = %v, want one path ending in huge.png", *opened)
	}
}

// TestDocumentPreviewKeepsTextAndImagePreviews is the regression guard: the
// supported previews do not grow the external-open action.
func TestDocumentPreviewKeepsTextAndImagePreviews(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("main.go", false), fileEntry("notes.md", false), fileEntry("pic.png", false))
	workspace.setFile("main.go", []byte("package main\n\nfunc main() {}\n"))
	workspace.setFile("notes.md", []byte("# 标题\n\n正文\n"))
	workspace.setFile("pic.png", tinyPNG)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")

	h.click(tester, "main.go")
	assertPreviewContains(t, tester, "func main()")
	h.assertNoTexts(tester, "用系统应用打开", "暂不支持预览", "本机没有内嵌")
	h.closeFileTab(tester, "main.go")

	h.click(tester, "notes.md")
	h.assertTexts(tester, "标题", "正文")
	h.assertNoTexts(tester, "用系统应用打开", "暂不支持预览")
	h.closeFileTab(tester, "notes.md")

	h.click(tester, "pic.png")
	h.assertTexts(tester, "关闭 pic.png")
	h.assertNoTexts(tester, "用系统应用打开", "暂不支持预览")
}
