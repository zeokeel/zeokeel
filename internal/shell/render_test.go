package shell

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRenderShell is the pixel pipeline's first link: render the shell
// headlessly at the baseline size and check the two anchor colors.
func TestRenderShell(t *testing.T) {
	img := ui.Render((&App{}).View, 1600, 1000, 1)
	if img == nil {
		t.Fatal("render returned nil")
	}
	if got := img.Bounds(); got.Dx() != 1600 || got.Dy() != 1000 {
		t.Fatalf("size: %v", got)
	}
	// (10,900) is inside the 280px sidebar and below the workspace/session
	// list, clear of any row glyph: baseline #F9FAFB.
	if r, g, b, _ := img.RGBAAt(10, 900).RGBA(); r>>8 != 0xF9 || g>>8 != 0xFA || b>>8 != 0xFB {
		t.Fatalf("sidebar pixel: %02x %02x %02x", r>>8, g>>8, b>>8)
	}
	// (1500,900) is the white main area, clear of the composer.
	if r, g, b, _ := img.RGBAAt(1500, 900).RGBA(); r>>8 != 0xFF || g>>8 != 0xFF || b>>8 != 0xFF {
		t.Fatalf("main pixel: %02x %02x %02x", r>>8, g>>8, b>>8)
	}

	dir := os.Getenv("ARTIFACTS")
	if dir == "" {
		dir = "../../artifacts"
	}
	if err := os.MkdirAll(filepath.Join(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "m0-shell.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
