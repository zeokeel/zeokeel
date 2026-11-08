package session_test

// Tests for the ZK-09 fix: the native attachment picker runs off the UI thread
// (busy state, results posted back) and the manual path-entry fallback stages
// absolute paths when the native picker is unavailable.

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

// slowAttachmentPicker returns a picker that blocks until release is closed, so
// a test can observe the composer while the picker is still "open". picks
// counts the calls the seam actually received.
func slowAttachmentPicker(t *testing.T, release <-chan struct{}, paths []string) (picks *int, lock *sync.Mutex) {
	t.Helper()
	picks = new(int)
	lock = new(sync.Mutex)
	setAttachmentPicker(t, func() ([]string, error) {
		lock.Lock()
		*picks++
		lock.Unlock()
		<-release
		return paths, nil
	})
	return picks, lock
}

func TestAttachmentPickerRunsOffUIThreadWithBusyState(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "note.txt", []byte("hello"))
	release := make(chan struct{})
	picks, lock := slowAttachmentPicker(t, release, []string{path})

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachFiles(tester)
	// The picker goroutine is still blocked: the composer must render the busy
	// state instead of freezing the frame (a synchronous picker would never
	// return here).
	h.assertTexts(tester, "选择中…")
	// While busy the attach button itself shows 选择中… and the toolbar offers
	// no second attach entry, so the modal cannot be opened and no second pick
	// starts.
	if got := exactTextCount(tester.Texts(), "附件"); got != 0 {
		t.Fatalf("attach entry still rendered while busy; texts = %v", tester.Texts())
	}
	h.click(tester, "选择中…")
	h.assertNoTexts(tester, "手动添加附件")
	close(release)
	h.waitFor(tester, "note.txt")
	h.assertNoTexts(tester, "选择中…")
	lock.Lock()
	got := *picks
	lock.Unlock()
	if got != 1 {
		t.Fatalf("picker calls = %d, want one while busy", got)
	}
	h.assertTexts(tester, "note.txt")
}

func TestAttachmentManualPathStagesSingleAndMultiple(t *testing.T) {
	dir := t.TempDir()
	first := writeTestFile(t, dir, "first.txt", []byte("one"))
	second := writeTestFile(t, dir, "second.txt", []byte("two"))

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	// Single path.
	h.attachManualPaths(tester)
	h.assertTexts(tester, "手动添加附件", "文件路径")
	h.click(tester, "文件路径")
	tester.Type(first)
	tester.Frame()
	h.click(tester, "添加")
	h.waitFor(tester, "first.txt")
	h.assertNoTexts(tester, "手动添加附件")

	// Two paths on one line, comma separated (the same splitter accepts lines
	// and semicolons; that shape is covered by the uikit unit test).
	h.attachManualPaths(tester)
	h.click(tester, "文件路径")
	tester.Type(first + ", " + second)
	tester.Frame()
	h.click(tester, "添加")
	h.waitFor(tester, "second.txt")
	h.assertNoTexts(tester, "手动添加附件")
}

func TestAttachmentManualPathRejectsRelative(t *testing.T) {
	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachManualPaths(tester)
	h.click(tester, "文件路径")
	tester.Type(filepath.Join("relative", "note.txt"))
	tester.Frame()
	h.click(tester, "添加")
	tester.Frame()

	// The dialog stays open with the inline reason, and nothing was staged.
	h.assertTexts(tester, "手动添加附件", "路径必须是绝对路径：relative/note.txt")
	h.assertNoTexts(tester, "移除附件")
}

func TestAttachmentManualPathRejectsMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.txt")
	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachManualPaths(tester)
	h.click(tester, "文件路径")
	tester.Type(missing)
	tester.Frame()
	h.click(tester, "添加")
	tester.Frame()

	h.assertTexts(tester, "手动添加附件", "文件不存在或不可读："+missing)
}

func TestAttachmentManualPathCancelStagesNothing(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "note.txt", []byte("hello"))
	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachManualPaths(tester)
	h.click(tester, "文件路径")
	tester.Type(path)
	tester.Frame()
	h.click(tester, "取消")
	h.assertNoTexts(tester, "手动添加附件")
	h.settle(tester)
	h.assertNoTexts(tester, "note.txt")
}

func TestAttachmentManualPathStagesValidAndReportsRest(t *testing.T) {
	valid := writeTestFile(t, t.TempDir(), "valid.txt", []byte("ok"))
	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachManualPaths(tester)
	h.click(tester, "文件路径")
	tester.Type(valid + ", relative/other.txt")
	tester.Frame()
	h.click(tester, "添加")
	tester.Frame()

	// The existing file is staged and the typo is reported inline; the dialog
	// stays open for a correction.
	h.assertTexts(tester, "valid.txt", "手动添加附件", "路径必须是绝对路径：relative/other.txt")
}

func TestAttachmentManualPathEmptyShowsError(t *testing.T) {
	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachManualPaths(tester)
	h.click(tester, "文件路径")
	// Enter on the empty field takes the dialog's add action; the inline error
	// explains why nothing was staged and the dialog stays open.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "手动添加附件", "请输入至少一个文件路径")
}
