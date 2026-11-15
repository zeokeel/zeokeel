package session_test

// Tests for the ZK-09 / P2-12 attachment paths: the native attachment picker
// runs off the UI thread (busy state, results posted back), the attach control
// opens that picker directly with no menu and no manual path entry, a picker
// that is unavailable explains itself and names the drag/paste alternatives, and
// the staged chip is the quiet BgStrong fill of the composer redesign.

import (
	"errors"
	"image/color"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
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

// TestAttachmentPickerRunsOffUIThreadWithBusyState covers the off-thread picker:
// the frame stays live and shows the busy state while the dialog is open, a
// second click starts no second pick, and there is no manual path entry to fall
// back to.
func TestAttachmentPickerRunsOffUIThreadWithBusyState(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "note.txt", []byte("hello"))
	release := make(chan struct{})
	picks, lock := slowAttachmentPicker(t, release, []string{path})

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	// One click on the attach control starts the native picker directly.
	h.attachFiles(tester)
	// The picker goroutine is still blocked: the composer must render the busy
	// state instead of freezing the frame (a synchronous picker would never
	// return here). The state is the official Spinner (a named progress
	// indicator), not a static icon.
	h.assertTexts(tester, "选择中…")
	if info, ok := tester.AccessByRole(ui.RoleProgress, "选择中…"); !ok || info.Role != ui.RoleProgress {
		t.Fatalf("attachment picker busy state is not a progress indicator: %+v found %v", info, ok)
	}
	// While busy the attach button itself shows 选择中…, so a second click cannot
	// open a second picker and the removed manual path entry cannot appear.
	if got := exactTextCount(tester.Texts(), "附件"); got != 0 {
		t.Fatalf("attach entry still rendered while busy; texts = %v", tester.Texts())
	}
	h.click(tester, "选择中…")
	h.assertNoTexts(tester, "手动添加附件", "手动输入路径…", "选择文件…", "文件路径")
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

// TestAttachmentButtonOpensPickerDirectly covers the P2-12 entry: clicking 附件
// opens the picker itself, with no 选择文件…/手动输入路径… menu in between, and
// stages the picked file on the posted frame.
func TestAttachmentButtonOpensPickerDirectly(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "direct.txt", []byte("hi"))
	picks := 0
	setAttachmentPicker(t, func() ([]string, error) {
		picks++
		return []string{path}, nil
	})

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "附件")
	// The old attach menu and its manual path dialog are gone from every frame.
	h.assertNoTexts(tester, "选择文件…", "手动输入路径…", "手动添加附件", "文件路径")

	h.attachFiles(tester)
	h.waitFor(tester, "direct.txt")
	if picks != 1 {
		t.Fatalf("picker calls = %d, want 1", picks)
	}
	// No menu was opened on the way: the attach control is not a menu button.
	h.assertNoTexts(tester, "选择文件…", "手动输入路径…", "手动添加附件")
}

// TestAttachmentPickerUnavailableExplainsAlternatives covers a picker that
// cannot open (a headless/portal-less environment): the composer reports the
// reason and the drag/paste paths in its error row instead of asking for a
// typed path, and the attach control stays usable for a retry.
func TestAttachmentPickerUnavailableExplainsAlternatives(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "retry.txt", []byte("ok"))
	setAttachmentPicker(t, func() ([]string, error) { return nil, errors.New("no file dialog") })

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()

	h.attachFiles(tester)
	h.waitFor(tester, "无法打开文件选择器：no file dialog", "请拖入文件或粘贴图片")
	h.assertNoTexts(tester, "手动输入路径…", "手动添加附件", "文件路径")
	h.assertTexts(tester, "附件")

	// The failed pick does not leave the button busy: the seam is retried.
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })
	h.attachFiles(tester)
	h.waitFor(tester, "retry.txt")
	h.assertNoTexts(tester, "无法打开文件选择器")
}

// TestAttachmentChipIsQuietNoBorder covers the P2-12 chip look: the staged chip
// paints the quiet BgStrong fill with no hairline border, keeps the 28 DIP
// height and the remove action's 24 DIP hit target.
func TestAttachmentChipIsQuietNoBorder(t *testing.T) {
	path := writeTestFile(t, t.TempDir(), "chip.txt", []byte("quiet chip"))
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()
	h.attachFiles(tester)
	h.waitFor(tester, "chip.txt")

	// The chip row carries the attachment's name as its accessible name, so its
	// bounds are the chip itself (the text below it is a separate RoleText).
	chip, ok := tester.Access("chip.txt")
	if !ok {
		t.Fatalf("staged chip has no accessible entry; texts = %v", tester.Texts())
	}
	if diff := chip.Bounds.H - design.ControlCompactHeight; diff < -1 || diff > 1 {
		t.Fatalf("chip height = %.1f, want %.0f", chip.Bounds.H, design.ControlCompactHeight)
	}
	light := design.Light()
	want := color.RGBA{R: light.BgStrong.R, G: light.BgStrong.G, B: light.BgStrong.B, A: light.BgStrong.A}
	x, y := int(chip.Bounds.X), int(chip.Bounds.Y+chip.Bounds.H/2)
	// Inside the padding the chip paints BgStrong.
	if got := tester.Image().RGBAAt(x+3, y); got != want {
		t.Fatalf("chip fill pixel = %v, want the quiet BgStrong %v", got, want)
	}
	// The left edge is chip fill too: a 1 DIP border would paint BorderL2 there.
	border := light.BorderL2
	if got := tester.Image().RGBAAt(x, y); got == (color.RGBA{R: border.R, G: border.G, B: border.B, A: border.A}) {
		t.Fatalf("chip left edge paints the BorderL2 hairline %v, want a borderless fill", got)
	}

	remove, ok := tester.AccessByRole(ui.RoleButton, "移除附件 chip.txt")
	if !ok {
		t.Fatalf("chip lost its remove action; texts = %v", tester.Texts())
	}
	if remove.Bounds.W < 24 || remove.Bounds.H < 24 {
		t.Fatalf("remove target = %.0fx%.0f, want >= 24x24", remove.Bounds.W, remove.Bounds.H)
	}
}
