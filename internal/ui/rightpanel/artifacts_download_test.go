package rightpanel_test

// TDD tests (RED first) for the artifact download action in the Files pane.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// withArtifactSaver replaces the save-dialog seam for one test.
func withArtifactSaver(t *testing.T, saver func(mygo.SaveDialogOptions) (string, error)) {
	t.Helper()
	previous := rightpanel.ExportArtifactSaver
	rightpanel.ExportArtifactSaver = saver
	t.Cleanup(func() { rightpanel.ExportArtifactSaver = previous })
}

func downloadHarness(t *testing.T, artifact contracts.Artifact, language string) *harness {
	t.Helper()
	return newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil,
		&fakeArtifactService{history: []contracts.Artifact{artifact}}, language)
}

func openFilesTab(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "文件")
}

// artifactForDownload builds one artifact with a fixed body.
func artifactForDownload(title, body string) contracts.Artifact {
	return contracts.Artifact{
		ID: "a1", Title: title, Body: body, CreatedAt: time.Unix(1_700_000_000, 0),
	}
}

// --- tests ------------------------------------------------------------------

func TestArtifactDownloadSavesMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.md")
	var captured mygo.SaveDialogOptions
	withArtifactSaver(t, func(options mygo.SaveDialogOptions) (string, error) {
		captured = options
		return path, nil
	})
	h := downloadHarness(t, artifactForDownload("巡检 报告", "# 内容\n\n正文"), "zh")
	tester := h.tester()
	tester.Frame()
	openFilesTab(t, h, tester)

	h.click(tester, "下载")
	if captured.Title != "保存产物" {
		t.Fatalf("dialog title = %q, want 保存产物", captured.Title)
	}
	if captured.DefaultPath != "巡检-报告.md" {
		t.Fatalf("default path = %q, want 巡检-报告.md", captured.DefaultPath)
	}
	if len(captured.Filters) != 1 || len(captured.Filters[0].Extensions) != 1 ||
		captured.Filters[0].Extensions[0] != "md" {
		t.Fatalf("filters = %+v, want markdown", captured.Filters)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("saved artifact: %v", err)
	}
	if string(data) != "# 内容\n\n正文" {
		t.Fatalf("saved content = %q", string(data))
	}
	h.assertTexts(tester, "已保存", filepath.Base(path))
}

func TestArtifactDownloadCancelled(t *testing.T) {
	withArtifactSaver(t, func(mygo.SaveDialogOptions) (string, error) { return "", nil })
	h := downloadHarness(t, artifactForDownload("取消", "body"), "zh")
	tester := h.tester()
	tester.Frame()
	openFilesTab(t, h, tester)

	h.click(tester, "下载")
	h.assertNoTexts(tester, "已保存", "保存失败")
}

func TestArtifactDownloadDialogError(t *testing.T) {
	withArtifactSaver(t, func(mygo.SaveDialogOptions) (string, error) {
		return "", errors.New("dialog boom")
	})
	h := downloadHarness(t, artifactForDownload("错误", "body"), "zh")
	tester := h.tester()
	tester.Frame()
	openFilesTab(t, h, tester)
	h.click(tester, "下载")
	h.assertTexts(tester, "保存失败：dialog boom")
}

func TestArtifactDownloadWriteError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "artifact.md")
	withArtifactSaver(t, func(mygo.SaveDialogOptions) (string, error) { return path, nil })
	h := downloadHarness(t, artifactForDownload("写入失败", "body"), "zh")
	tester := h.tester()
	tester.Frame()
	openFilesTab(t, h, tester)
	h.click(tester, "下载")
	h.assertTexts(tester, "保存失败：")
}

func TestArtifactDownloadSlug(t *testing.T) {
	cases := []struct{ title, want string }{
		{"Hello World", "Hello-World.md"},
		{"  中文 标题 ", "中文-标题.md"},
		{"a/b:c", "a-b-c.md"},
		{"!!!", "artifact.md"},
		{strings.Repeat("x", 70), strings.Repeat("x", 60) + ".md"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			var captured mygo.SaveDialogOptions
			withArtifactSaver(t, func(options mygo.SaveDialogOptions) (string, error) {
				captured = options
				return filepath.Join(t.TempDir(), "out.md"), nil
			})
			h := downloadHarness(t, artifactForDownload(tc.title, "body"), "zh")
			tester := h.tester()
			tester.Frame()
			openFilesTab(t, h, tester)
			h.click(tester, "下载")
			if captured.DefaultPath != tc.want {
				t.Fatalf("title %q default path = %q, want %q", tc.title, captured.DefaultPath, tc.want)
			}
		})
	}
}

func TestArtifactDownloadEnglishCopy(t *testing.T) {
	var captured mygo.SaveDialogOptions
	withArtifactSaver(t, func(options mygo.SaveDialogOptions) (string, error) {
		captured = options
		return filepath.Join(t.TempDir(), "out.md"), nil
	})
	h := downloadHarness(t, artifactForDownload("Report", "body"), "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Files")
	h.click(tester, "Download")
	if captured.Title != "Save artifact" {
		t.Fatalf("dialog title = %q, want Save artifact", captured.Title)
	}
	h.assertTexts(tester, "Saved")
}
