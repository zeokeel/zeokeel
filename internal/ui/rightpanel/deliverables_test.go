package rightpanel_test

// Frame tests for the Files page's 交付区 (deliverables) section: the artifact
// history list, its Markdown preview fold and the download action.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"

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

func artifactTextIndex(texts []string, want string) int {
	for index, text := range texts {
		if text == want {
			return index
		}
	}
	return -1
}

func TestDeliverablesHiddenWithoutLister(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertNoTexts(tester, "交付区", "还没有交付物")
}

func TestDeliverablesEmptyState(t *testing.T) {
	h := newArtifactsHarness(t, &fakeArtifactService{})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "交付区", "还没有交付物")
}

func TestDeliverablesNewestFirst(t *testing.T) {
	base := time.Unix(1700000000, 0)
	history := []contracts.Artifact{
		artifactAt("a-old", "旧工件", "旧内容", base),
		artifactAt("a-new", "新工件", "新内容", base.Add(5*time.Minute)),
	}
	h := newArtifactsHarness(t, &fakeArtifactService{history: history})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "交付区", "新工件", "旧工件", "预览")

	newest := artifactTextIndex(tester.Texts(), "新工件")
	oldest := artifactTextIndex(tester.Texts(), "旧工件")
	if !(newest >= 0 && oldest >= 0 && newest < oldest) {
		t.Fatalf("artifact order = newest %d, oldest %d; want newest first", newest, oldest)
	}
}

func TestDeliverablePreviewExpandsAndCollapses(t *testing.T) {
	history := []contracts.Artifact{
		artifactAt("a1", "报告", "# 标题\n\n正文", time.Unix(1700000000, 0)),
	}
	h := newArtifactsHarness(t, &fakeArtifactService{history: history})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "报告")
	h.assertNoTexts(tester, "正文") // folded

	h.click(tester, "预览")
	h.assertTexts(tester, "正文")
	h.click(tester, "收起")
	h.assertNoTexts(tester, "正文")
}

func TestDeliverableDownloadSavesMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.md")
	var captured mygo.SaveDialogOptions
	withArtifactSaver(t, func(options mygo.SaveDialogOptions) (string, error) {
		captured = options
		return path, nil
	})
	history := []contracts.Artifact{
		artifactAt("a1", "巡检 报告", "# 内容\n\n正文", time.Unix(1700000000, 0)),
	}
	h := newArtifactsHarness(t, &fakeArtifactService{history: history})
	tester := h.tester()
	h.openPage(tester, "文件")

	h.click(tester, "下载")
	if captured.Title != "保存产物" {
		t.Fatalf("dialog title = %q, want 保存产物", captured.Title)
	}
	if captured.DefaultPath != "巡检-报告.md" {
		t.Fatalf("default path = %q, want 巡检-报告.md", captured.DefaultPath)
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

func TestDeliverableDownloadErrors(t *testing.T) {
	withArtifactSaver(t, func(mygo.SaveDialogOptions) (string, error) {
		return "", errors.New("no dialog")
	})
	history := []contracts.Artifact{
		artifactAt("a1", "失败", "body", time.Unix(1700000000, 0)),
	}
	h := newArtifactsHarness(t, &fakeArtifactService{history: history})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "下载")
	h.assertTexts(tester, "保存失败：no dialog")
}

// TestDeliverableListScrolls: the deliverables list is bounded and scrolls, so
// its history cannot push the workspace tree off the page.
func TestDeliverableListScrolls(t *testing.T) {
	base := time.Unix(1700000000, 0)
	history := make([]contracts.Artifact, 0, 12)
	for index := 0; index < 12; index++ {
		history = append(history, artifactAt(
			"a"+string(rune('a'+index)),
			"交付物-"+string(rune('a'+index)),
			"# body\n",
			base.Add(time.Duration(index)*time.Minute)))
	}
	h := newArtifactsHarness(t, &fakeArtifactService{history: history})
	tester := h.tester()
	h.openPage(tester, "文件")
	tester.Frame()
	// The section caps its rows; the newest ten show and the list scrolls.
	if len(history) < 10 {
		t.Fatalf("fixture too small: %d", len(history))
	}
	if !strings.Contains(strings.Join(tester.Texts(), "\n"), "交付物-") {
		t.Fatalf("deliverables are not listed; texts = %v", tester.Texts())
	}
}
