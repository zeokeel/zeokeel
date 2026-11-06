package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 40), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return buf.Bytes()
}

// imageOutput is the JSON object read_image returns.
type imageOutput struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Format       string `json:"format"`
	MIME         string `json:"mime"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Bytes        int64  `json:"bytes"`
	ImageDataURL string `json:"image_data_url"`
	Note         string `json:"note"`
}

func decodeImageOutput(t *testing.T, out string) imageOutput {
	t.Helper()
	var decoded imageOutput
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("read_image output is not a JSON object: %v\n%s", err, out)
	}
	return decoded
}

func TestReadImage(t *testing.T) {
	root := t.TempDir()
	tool := NewReadImageTool(ImageConfig{WorkspaceRoot: root})
	assertSchema(t, tool)
	if tool.Name() != "read_image" {
		t.Fatalf("name = %q", tool.Name())
	}

	pngBytes := writePNG(t, filepath.Join(root, "pic.png"), 3, 2)
	out := mustInvoke(t, tool, map[string]any{"file_path": "pic.png"})
	got := decodeImageOutput(t, out)
	if got.Kind != "image" || got.Path != "pic.png" || got.Format != "png" || got.MIME != "image/png" {
		t.Fatalf("png output = %+v", got)
	}
	if got.Width != 3 || got.Height != 2 || got.Bytes != int64(len(pngBytes)) {
		t.Fatalf("png metadata = %+v, want 3x2 and %d bytes", got, len(pngBytes))
	}
	if !strings.HasPrefix(got.ImageDataURL, "data:image/png;base64,") {
		t.Fatalf("png data URL = %q", got.ImageDataURL)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got.ImageDataURL, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("decode data URL: %v", err)
	}
	if !bytes.Equal(decoded, pngBytes) {
		t.Fatal("data URL does not contain the original file bytes")
	}
	if got.Note == "" || !strings.Contains(got.Note, "provider supports") {
		t.Fatalf("note = %q", got.Note)
	}

	gifImg := image.NewPaletted(image.Rect(0, 0, 2, 4), color.Palette{color.Black, color.White})
	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, gifImg, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "anim.gif"), gifBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got = decodeImageOutput(t, mustInvoke(t, tool, map[string]any{"file_path": "anim.gif"}))
	if got.MIME != "image/gif" || got.Width != 2 || got.Height != 4 || got.Format != "gif" {
		t.Fatalf("gif output = %+v", got)
	}

	jpegImg := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, jpegImg, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "photo.jpeg"), jpegBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	got = decodeImageOutput(t, mustInvoke(t, tool, map[string]any{"file_path": "photo.jpeg"}))
	if got.MIME != "image/jpeg" || got.Width != 4 || got.Height != 3 {
		t.Fatalf("jpeg output = %+v", got)
	}

	// An extensionless file is sniffed from its content.
	if err := os.WriteFile(filepath.Join(root, "noext"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	got = decodeImageOutput(t, mustInvoke(t, tool, map[string]any{"file_path": "noext"}))
	if got.MIME != "image/png" {
		t.Fatalf("extensionless output = %+v", got)
	}
}

func TestReadImageCapAndTraversal(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "pic.png"), 3, 2)
	smallCap := NewReadImageTool(ImageConfig{WorkspaceRoot: root, MaxDataURLBytes: 64})
	out := mustInvoke(t, smallCap, map[string]any{"file_path": "pic.png"})
	capped := decodeImageOutput(t, out)
	if capped.ImageDataURL != "" || strings.Contains(out, "data:image/") {
		t.Fatalf("capped output = %q", out)
	}
	if !strings.Contains(capped.Note, "omitted") || !strings.Contains(capped.Note, "64-byte") {
		t.Fatalf("capped note = %q", capped.Note)
	}

	tool := NewReadImageTool(ImageConfig{WorkspaceRoot: root})
	for _, bad := range []string{"../pic.png", "/tmp/pic.png", "a/../pic.png"} {
		if _, err := invokeTool(t, tool, map[string]any{"file_path": bad}); err == nil {
			t.Fatalf("read_image accepted traversal path %q", bad)
		}
	}
	if _, err := invokeTool(t, tool, map[string]any{"file_path": "missing.png"}); err == nil {
		t.Fatal("missing image should fail")
	}
	writeTestFile(t, filepath.Join(root, "not-image.txt"), "plain text")
	if _, err := invokeTool(t, tool, map[string]any{"file_path": "not-image.txt"}); err == nil || !strings.Contains(err.Error(), "cannot decode") {
		t.Fatalf("non-image error = %v", err)
	}
	if _, err := invokeTool(t, tool, map[string]any{"file_path": "."}); err == nil {
		t.Fatal("directory should fail")
	}

	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "secret.png"), 1, 1)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	} else if _, err := invokeTool(t, tool, map[string]any{"file_path": "escape/secret.png"}); err == nil {
		t.Fatal("read_image followed an escaping symlink")
	}

	bad := NewReadImageTool(ImageConfig{})
	if _, err := invokeTool(t, bad, map[string]any{"file_path": "pic.png"}); err == nil || !strings.Contains(err.Error(), "WorkspaceRoot") {
		t.Fatalf("missing root error = %v", err)
	}
}
