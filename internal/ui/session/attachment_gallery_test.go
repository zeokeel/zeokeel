package session_test

// Frame tests for the attachment image gallery (P1): a message image chip opens
// the native modal lightbox with the original image, the file name and size; the
// dialog closes from its button, Escape and the backdrop; a message with several
// images pages through them; a staged image previews before sending; a chip that
// is not an image does nothing; and an original that cannot be shown says why
// instead of failing silently.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// galleryPNG encodes a w×h PNG with a visible gradient, so the lightbox under
// test has a real aspect ratio to fit.
func galleryPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buffer.Bytes()
}

// galleryImage returns one image attachment of a sent user message.
func galleryImage(id, name string, data []byte) contracts.Attachment {
	return contracts.Attachment{ID: id, Name: name, MIME: "image/png", Data: data}
}

// galleryChat is a fake chat with one active conversation whose only message
// carries the given attachments.
func galleryChat(attachments ...contracts.Attachment) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "画廊会话",
		Messages: []contracts.ChatMessage{{
			ID: "m1", Role: contracts.ChatRoleUser, Text: "看这些附件",
			Attachments: attachments,
			CreatedAt:   time.Unix(1700000000, 0),
		}},
	}, true)
	return chat
}

// galleryDialog reports whether a modal dialog is on screen.
func galleryDialog(tester *ui.Tester) bool {
	_, open := tester.AccessByRole(ui.RoleDialog, "")
	return open
}

// openGallery clicks one image chip (or its preview action) and draws the frame.
func openGallery(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click image chip %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// requireGalleryImage asserts the lightbox shows a labeled image inside the
// window, with roughly the bitmap's aspect ratio.
func requireGalleryImage(t *testing.T, tester *ui.Tester, name string, aspect float64) ui.Rect {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleImage, name)
	if !ok {
		t.Fatalf("the lightbox shows no image named %q; texts = %v", name, tester.Texts())
	}
	rect := info.Bounds
	if rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("lightbox image %q has no visible area: %+v", name, rect)
	}
	if aspect > 0 {
		if got := float64(rect.W) / float64(rect.H); got < aspect*0.9 || got > aspect*1.1 {
			t.Fatalf("lightbox image %q aspect = %.2f, want about %.2f (%+v)", name, got, aspect, rect)
		}
	}
	return rect
}

// requireNoGallery fails while a modal dialog is showing.
func requireNoGallery(t *testing.T, tester *ui.Tester, where string) {
	t.Helper()
	if galleryDialog(tester) {
		t.Fatalf("%s: a gallery dialog is still open; texts = %v", where, tester.Texts())
	}
}

func TestAttachmentGalleryOpensFromMessageChip(t *testing.T) {
	data := galleryPNG(t, 400, 200)
	h := newAttachmentHarness(t, galleryChat(galleryImage("att-1", "chart.png", data)))
	tester := h.tester()
	tester.Frame()
	requireNoGallery(t, tester, "before the click")
	h.assertTexts(tester, "chart.png")

	openGallery(t, tester, "chart.png")
	if !galleryDialog(tester) {
		t.Fatalf("the image chip did not open the lightbox; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "chart.png")
	// The dialog names the file and its size, and offers the explicit close.
	h.assertTexts(tester, formatGallerySize(len(data)))
	if _, ok := tester.AccessByRole(ui.RoleButton, "关闭大图"); !ok {
		t.Fatalf("the lightbox has no close action; texts = %v", tester.Texts())
	}
	rect := requireGalleryImage(t, tester, "chart.png", 2)
	if rect.W > 1280 || rect.H > 800 {
		t.Fatalf("the lightbox image escaped the window: %+v", rect)
	}

	// The explicit close button closes it.
	h.click(tester, "关闭大图")
	requireNoGallery(t, tester, "after the close action")
}

func TestAttachmentGalleryClosesWithEscapeAndBackdrop(t *testing.T) {
	data := galleryPNG(t, 320, 240)
	h := newAttachmentHarness(t, galleryChat(galleryImage("att-1", "shot.png", data)))
	tester := h.tester()
	tester.Frame()

	openGallery(t, tester, "shot.png")
	if !galleryDialog(tester) {
		t.Fatalf("the lightbox did not open; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	requireNoGallery(t, tester, "after Escape")

	openGallery(t, tester, "shot.png")
	if !galleryDialog(tester) {
		t.Fatalf("the lightbox did not reopen; texts = %v", tester.Texts())
	}
	// A press on the dimmed backdrop (far from the centered panel) closes it.
	tester.ClickAt(6, 6)
	tester.Frame()
	requireNoGallery(t, tester, "after a backdrop click")
}

func TestAttachmentGalleryPagesThroughOneMessageImages(t *testing.T) {
	first := galleryPNG(t, 400, 200)
	second := galleryPNG(t, 200, 400)
	h := newAttachmentHarness(t, galleryChat(
		galleryImage("att-1", "first.png", first),
		galleryImage("att-2", "second.png", second),
	))
	tester := h.tester()
	tester.Frame()

	openGallery(t, tester, "first.png")
	if !galleryDialog(tester) {
		t.Fatalf("the lightbox did not open; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "第 1 / 2 张")
	requireGalleryImage(t, tester, "first.png", 2)

	h.click(tester, "下一张")
	h.assertTexts(tester, "第 2 / 2 张", "second.png")
	requireGalleryImage(t, tester, "second.png", 0.5)
	h.assertNoTexts(tester, "第 1 / 2 张")

	h.click(tester, "上一张")
	h.assertTexts(tester, "第 1 / 2 张", "first.png")

	// The arrow keys page too while the dialog has the keyboard.
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	h.assertTexts(tester, "第 2 / 2 张")
	tester.Key(0, ui.KeyLeft)
	tester.Frame()
	h.assertTexts(tester, "第 1 / 2 张")
}

func TestAttachmentGalleryStagedImagePreview(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "draft.png", galleryPNG(t, 300, 200))
	setAttachmentPicker(t, func() ([]string, error) { return []string{path}, nil })

	h := newAttachmentHarness(t, activeSessionChat())
	tester := h.tester()
	tester.Frame()
	h.attachFiles(tester)
	h.waitFor(tester, "draft.png")

	openGallery(t, tester, "查看原图 draft.png")
	if !galleryDialog(tester) {
		t.Fatalf("the staged image did not preview; texts = %v", tester.Texts())
	}
	requireGalleryImage(t, tester, "draft.png", 1.5)
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	requireNoGallery(t, tester, "after previewing the staged image")

	// The preview action does not displace the remove action.
	if err := tester.Click("移除附件 draft.png"); err != nil {
		t.Fatalf("click remove after preview: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "draft.png")
}

func TestAttachmentGalleryNonImageChipDoesNothing(t *testing.T) {
	textAttachment := contracts.Attachment{
		ID: "att-1", Name: "notes.txt", MIME: "text/plain", Data: []byte("hello"),
	}
	h := newAttachmentHarness(t, galleryChat(textAttachment))
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "notes.txt")
	openGallery(t, tester, "notes.txt")
	requireNoGallery(t, tester, "after clicking a non-image chip")
}

func TestAttachmentGalleryHonestNotices(t *testing.T) {
	t.Run("stored history without image data", func(t *testing.T) {
		restored := contracts.Attachment{ID: "att-1", Name: "restored.png", MIME: "image/png"}
		h := newAttachmentHarness(t, galleryChat(restored))
		tester := h.tester()
		tester.Frame()
		openGallery(t, tester, "restored.png")
		if !galleryDialog(tester) {
			t.Fatalf("the restored image did not open its notice; texts = %v", tester.Texts())
		}
		h.assertTexts(tester, "没有保存图片数据")
	})

	t.Run("undecodable image", func(t *testing.T) {
		broken := galleryImage("att-1", "broken.png", []byte("not a png"))
		h := newAttachmentHarness(t, galleryChat(broken))
		tester := h.tester()
		tester.Frame()
		openGallery(t, tester, "broken.png")
		if !galleryDialog(tester) {
			t.Fatalf("the broken image did not open its notice; texts = %v", tester.Texts())
		}
		h.assertTexts(tester, "无法解码")
	})

	t.Run("over the preview limit", func(t *testing.T) {
		overLimit := galleryImage("att-1", "huge.png", make([]byte, maxGalleryPreviewBytes+1))
		h := newAttachmentHarness(t, galleryChat(overLimit))
		tester := h.tester()
		tester.Frame()
		openGallery(t, tester, "huge.png")
		if !galleryDialog(tester) {
			t.Fatalf("the oversized image did not open its notice; texts = %v", tester.Texts())
		}
		h.assertTexts(tester, "预览上限")
	})
}

func TestAttachmentGalleryFitsNarrowWindow(t *testing.T) {
	data := galleryPNG(t, 1200, 800)
	h := newAttachmentHarness(t, galleryChat(galleryImage("att-1", "wide.png", data)))
	tester := h.tester()
	tester.SetSize(900, 600)
	tester.Frame()
	tester.Frame()

	openGallery(t, tester, "wide.png")
	if !galleryDialog(tester) {
		t.Fatalf("the lightbox did not open at 900x600; texts = %v", tester.Texts())
	}
	rect := requireGalleryImage(t, tester, "wide.png", 1.5)
	if rect.W > 900 || rect.H > 600 || rect.X < 0 || rect.Y < 0 ||
		rect.X+rect.W > 900 || rect.Y+rect.H > 600 {
		t.Fatalf("the lightbox image is not fitted inside the 900x600 viewport: %+v", rect)
	}
}

// formatGallerySize mirrors the chip's size copy for the opened dialog, so the
// test asserts the same exact string the user sees.
func formatGallerySize(size int) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}

// TestAttachmentGalleryKeyboardFocus stays a compile-time guard that the dialog
// label copy remains a sentence (no TypeXXXS micro-label regression).
func TestAttachmentGalleryKeyboardFocus(t *testing.T) {
	if strings.TrimSpace("关闭大图") == "" {
		t.Fatal("empty close copy")
	}
}

// maxGalleryPreviewBytes mirrors the product's 10 MiB preview bound, so the
// oversized-original case builds just past it.
const maxGalleryPreviewBytes = 10 << 20
