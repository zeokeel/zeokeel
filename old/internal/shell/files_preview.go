package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// filesPreviewKind is how the Files preview draws a file: plain text is the
// fallback, markdown renders through the shared Markdown view, and image
// decodes into a bitmap/SVG element.
type filesPreviewKind int

const (
	filesPreviewText filesPreviewKind = iota
	filesPreviewMarkdown
	filesPreviewImage
)

// maxPreviewImageBytes caps an image preview: 8 MiB. Larger images are not
// decoded; the panel says so and keeps the native Open action instead. A
// variable so the preview test can shrink the limit.
var maxPreviewImageBytes = 8 << 20

// previewKindForPath classifies a preview by its extension. Only the formats
// the brand renders natively are special-cased; every other extension
// (including unknown ones) keeps the plain-text preview.
func previewKindForPath(rel string) filesPreviewKind {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".md", ".markdown":
		return filesPreviewMarkdown
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".svg":
		return filesPreviewImage
	}
	return filesPreviewText
}

// previewMetadataLabel is the preview header's kind-and-size line.
func (a *App) previewMetadataLabel(kind filesPreviewKind, bytes int) string {
	switch kind {
	case filesPreviewMarkdown:
		return fmt.Sprintf(a.T("files.markdown_preview_d_bytes"), bytes)
	case filesPreviewImage:
		return a.T("files.image_preview")
	default:
		return fmt.Sprintf(a.T("files.plain_text_preview_d_bytes"), bytes)
	}
}

// loadPreviewImage decodes a workspace image for the preview panel. The
// returned message is user-facing: unreadable, oversized or undecodable files
// explain themselves instead of drawing a broken element. The extension
// decided the kind; content sniffing stays out so a mislabeled file shows the
// explanation rather than silently falling back.
func (a *App) loadPreviewImage(abs string) (ui.ImageSource, string) {
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Sprintf(a.T("files.could_not_open_the_file_v"), err)
	}
	if info.Size() > int64(maxPreviewImageBytes) {
		return nil, a.T("files.image_too_large_to_preview")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Sprintf(a.T("files.could_not_read_the_file_v"), err)
	}
	src, err := ui.DecodeImage(data)
	if err != nil {
		return nil, a.T("files.could_not_decode_the_image")
	}
	return src, ""
}
