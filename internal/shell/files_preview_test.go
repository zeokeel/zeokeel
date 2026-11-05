package shell

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// writePreviewPNG writes a tiny valid PNG into the fixture's workspace.
func writePreviewPNG(t *testing.T, h *liveApp, rel string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 220, G: 40, B: 40, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	path := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

// showFilesTab makes the fixture's live app show the Files tab at 1600x1000.
func showFilesTab(t *testing.T, h *liveApp) *ui.Tester {
	t.Helper()
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	return tt
}

// TestFilesMarkdownPreview covers the rendered Markdown preview: a .md file is
// drawn by the shared Markdown view instead of as its source text.
func TestFilesMarkdownPreview(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "notes/readme.md", "# 发布说明\n\n- 第一项\n- 第二项\n")
	h.app.openFilePreview("notes/readme.md")
	tt := showFilesTab(t, h)
	tt.Frame()

	if !tt.HasText("Markdown preview") {
		t.Fatalf("Markdown preview label missing: %v", tt.Texts())
	}
	if !tt.HasText("发布说明") || !tt.HasText("第一项") {
		t.Fatalf("rendered Markdown text missing: %v", tt.Texts())
	}
	if tt.HasText("# 发布说明") {
		t.Fatalf("Markdown source shown instead of the rendered heading: %v", tt.Texts())
	}
}

// TestFilesMarkdownTruncatedStaysText covers a Markdown file over the preview
// cap: rendering a cut document would silently drop the tail, so the plain text
// preview and its truncation note take over.
func TestFilesMarkdownTruncatedStaysText(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "huge.md", strings.Repeat("x", maxPreviewBytes+10))
	h.app.openFilePreview("huge.md")
	tt := showFilesTab(t, h)
	tt.Frame()

	if tt.HasText("Markdown preview") {
		t.Fatalf("truncated Markdown rendered as Markdown: %v", tt.Texts())
	}
	if !tt.HasText("Plain text preview") {
		t.Fatalf("truncated Markdown did not fall back to text: %v", tt.Texts())
	}
	if !tt.HasText("Preview truncated at 256 KB") {
		t.Fatalf("truncation note missing: %v", tt.Texts())
	}
}

// TestFilesImagePreview covers the decoded image preview: a PNG is shown as an
// image, not as the binary-file fallback.
func TestFilesImagePreview(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writePreviewPNG(t, h, "assets/mark.png")
	h.app.openFilePreview("assets/mark.png")
	tt := showFilesTab(t, h)
	tt.Frame()

	if !tt.HasText("Image preview") {
		t.Fatalf("image preview label missing: %v", tt.Texts())
	}
	if tt.HasText("Binary file") {
		t.Fatalf("PNG fell back to the binary preview: %v", tt.Texts())
	}
}

// TestFilesImageDecodeError covers a file with an image extension that is not
// an image: the panel explains that it could not decode it.
func TestFilesImageDecodeError(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "broken.png", "this is not a png")
	h.app.openFilePreview("broken.png")
	tt := showFilesTab(t, h)
	tt.Frame()

	if !tt.HasText("Could not decode the image") {
		t.Fatalf("decode error missing: %v", tt.Texts())
	}
}

// TestFilesImageTooLarge covers the image size cap: the panel refuses to decode
// a file above the cap and says why, leaving the native Open action available.
func TestFilesImageTooLarge(t *testing.T) {
	old := maxPreviewImageBytes
	maxPreviewImageBytes = 64
	t.Cleanup(func() { maxPreviewImageBytes = old })

	h := newLiveApp(t, &shellProvider{})
	writePreviewPNG(t, h, "assets/large.png")
	h.app.openFilePreview("assets/large.png")
	tt := showFilesTab(t, h)
	tt.Frame()

	if !tt.HasText("too large to preview") {
		t.Fatalf("oversize image notice missing: %v", tt.Texts())
	}
	if !tt.HasText("Open file in system app") {
		t.Fatalf("oversize image lost the native Open action: %v", tt.Texts())
	}
}

// TestFilesUnknownExtensionStaysText keeps the plain fallback for formats the
// brand does not render: an unknown .bin with NUL bytes reports the binary
// notice, a normal unknown text extension stays plain text.
func TestFilesUnknownExtensionStaysText(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "notes.txt", "plain notes")
	h.app.openFilePreview("notes.txt")
	tt := showFilesTab(t, h)
	tt.Frame()
	if !tt.HasText("Plain text preview") || !tt.HasText("plain notes") {
		t.Fatalf("plain text preview missing: %v", tt.Texts())
	}

	binPath := filepath.Join(h.dir, "data.bin")
	if err := os.WriteFile(binPath, []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatalf("write data.bin: %v", err)
	}
	h.app.openFilePreview("data.bin")
	tt.Frame()
	if !tt.HasText("Binary file") {
		t.Fatalf("binary fallback missing for an unknown extension: %v", tt.Texts())
	}
}

// TestFilesPreviewLabelsChinese pins the Chinese preview labels: Markdown and
// image previews name their kind in the metadata row and the accessibility
// tree, matching the rest of the panel's bilingual copy.
func TestFilesPreviewLabelsChinese(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "readme.md", "# 标题\n")
	writePreviewPNG(t, h, "mark.png")
	h.app.Language = LangZH
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	h.app.openFilePreview("readme.md")
	tt.Frame()
	if !tt.HasText("Markdown 预览") || !tt.HasText("标题") {
		t.Fatalf("Chinese Markdown preview missing: %v", tt.Texts())
	}
	h.app.openFilePreview("mark.png")
	tt.Frame()
	if !tt.HasText("图片预览") {
		t.Fatalf("Chinese image preview label missing: %v", tt.Texts())
	}
}
