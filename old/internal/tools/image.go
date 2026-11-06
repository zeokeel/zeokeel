package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/runtime"

	// Register the decoders image.DecodeConfig understands. PNG, JPEG and GIF
	// come from the standard library; WebP comes from the pure-Go
	// golang.org/x/image/webp module already required by this module's go.mod.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// read_image decodes an image's metadata and returns a JSON object with a
// capped data URL. The runtime's multimodal injector reads image_data_url from
// tool results and attaches the image to the next model request.

const (
	// DefaultMaxDataURLBytes caps the returned base64 data URL.
	DefaultMaxDataURLBytes = 2 << 20

	// maxImageFileBytes bounds how large a file read_image will inspect.
	maxImageFileBytes = 32 << 20
)

// ImageConfig configures the read_image tool.
type ImageConfig struct {
	// WorkspaceRoot confines file_path. Required.
	WorkspaceRoot string

	// MaxDataURLBytes caps the returned data URL; larger images still report
	// metadata but omit the data URL. Non-positive values use
	// DefaultMaxDataURLBytes.
	MaxDataURLBytes int
}

// NewReadImageTool constructs the read_image tool. The constructor never fails:
// an invalid configuration is reported by Invoke.
func NewReadImageTool(cfg ImageConfig) runtime.Tool {
	t := &readImageTool{maxDataURL: cfg.MaxDataURLBytes}
	if t.maxDataURL <= 0 {
		t.maxDataURL = DefaultMaxDataURLBytes
	}
	ws, err := newWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.initErr = fmt.Errorf("read_image: %w", err)
		return t
	}
	t.ws = ws
	return t
}

type readImageTool struct {
	ws         *workspaceRoot
	maxDataURL int
	initErr    error
}

func (t *readImageTool) Name() string        { return "read_image" }
func (t *readImageTool) Description() string { return readImageDescription }

const readImageDescription = `Read a PNG/JPEG/GIF/WebP file and return its metadata, pixel dimensions and a base64 data: URL.

The path must be workspace-relative; absolute paths, ".." components and symlinks that leave the root are rejected. The result is a JSON object with kind, path, format, mime, width, height, bytes, image_data_url and note. The data URL is capped at 2 MiB and omitted for larger files (metadata is still returned). Files larger than 32 MiB are rejected. The agent runtime attaches the image_data_url to the next model request as an image content part when the active provider supports image input; it is informational otherwise. Do not install image libraries or create thumbnails.

Args JSON schema:
{"type":"object","properties":{"file_path":{"type":"string","description":"Workspace-relative path of the image file."}},"required":["file_path"],"additionalProperties":false}`

// readImageOutput is the JSON object read_image returns. image_data_url is
// omitted when the file does not fit within the configured cap.
type readImageOutput struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Format       string `json:"format"`
	MIME         string `json:"mime"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Bytes        int64  `json:"bytes"`
	ImageDataURL string `json:"image_data_url,omitempty"`
	Note         string `json:"note"`
}

func (t *readImageTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		FilePath string `json:"file_path"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if t.initErr != nil {
		return "", t.initErr
	}
	if strings.TrimSpace(a.FilePath) == "" {
		return "", fmt.Errorf("read_image: file_path is required")
	}
	abs, err := t.ws.resolve(a.FilePath)
	if err != nil {
		return "", fmt.Errorf("read_image: %w", err)
	}
	display := t.ws.display(abs)
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("read_image: %s does not exist", display)
		}
		return "", fmt.Errorf("read_image: stat %s: %w", display, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_image: %s is a directory", display)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_image: %s is not a regular file", display)
	}
	if info.Size() > maxImageFileBytes {
		return "", fmt.Errorf("read_image: %s is %d bytes, larger than the %d byte limit", display, info.Size(), maxImageFileBytes)
	}

	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("read_image: open %s: %w", display, err)
	}
	cfg, format, err := image.DecodeConfig(f)
	closeErr := f.Close()
	if err != nil {
		return "", fmt.Errorf("read_image: cannot decode %s as an image (supported formats: PNG, JPEG, GIF, WebP): %w", display, err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("read_image: read %s: %w", display, closeErr)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", fmt.Errorf("read_image: %s has invalid dimensions %dx%d", display, cfg.Width, cfg.Height)
	}
	mime, ok := imageMIMEType(format)
	if !ok {
		return "", fmt.Errorf("read_image: %s decoded as unsupported format %q (supported formats: PNG, JPEG, GIF, WebP)", display, format)
	}

	prefix := "data:" + mime + ";base64,"
	maxRaw := (t.maxDataURL - len(prefix)) / 4 * 3
	var dataURL string
	switch {
	case maxRaw <= 0:
		dataURL = ""
	case info.Size() > int64(maxRaw):
		dataURL = ""
	default:
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read_image: read %s: %w", display, err)
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		if len(prefix)+len(encoded) > t.maxDataURL {
			dataURL = ""
		} else {
			dataURL = prefix + encoded
		}
	}

	output := readImageOutput{
		Kind:   "image",
		Path:   display,
		Format: format,
		MIME:   mime,
		Width:  cfg.Width,
		Height: cfg.Height,
		Bytes:  info.Size(),
		Note: "the agent runtime attaches this image to the next model request as an " +
			"image content part when the active provider supports image input; otherwise it is informational.",
	}
	if dataURL != "" {
		output.ImageDataURL = dataURL
	} else {
		output.Note = fmt.Sprintf("image_data_url omitted: the %d-byte file does not fit within the %d-byte data URL cap. %s",
			info.Size(), t.maxDataURL, output.Note)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("read_image: encode %s metadata: %w", display, err)
	}
	return string(encoded), nil
}

func imageMIMEType(format string) (string, bool) {
	switch strings.ToLower(format) {
	case "png":
		return "image/png", true
	case "jpeg", "jpg":
		return "image/jpeg", true
	case "gif":
		return "image/gif", true
	case "webp":
		return "image/webp", true
	}
	return "", false
}
