package shell

import (
	"context"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// TestLiveSessionHeaderMatchesStaticMetrics pins the live header to the
// static 76px block: the bottom border sits at y=75 (not 81), the title is
// weight 500, and the mode chip is the static 14px tune icon plus 12px
// "Standard mode" text rather than a 9px chevron with no artwork.
func TestLiveSessionHeaderMatchesStaticMetrics(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	// A title no sidebar row carries, so Find returns the header crumb.
	id, err := h.ctrl.CreateSession(context.Background(), "Header Title Unique")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.OpenSession(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.app.SetSnapshot(h.ctrl.Snapshot())
	// Collapse the sidebar so its session rows cannot shadow the header
	// crumb in Find.
	h.app.sidebarCollapsed = true
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	img := tt.Image()

	tokens := design.Light()
	wantBorder := ui.RGBA(0, 0, 0, 0.12).Over(tokens.BgBase)
	if got := img.RGBAAt(700, 75); got != (color.RGBA{R: wantBorder.R, G: wantBorder.G, B: wantBorder.B, A: 255}) {
		t.Fatalf("live header bottom border at (700,75) = %v, want %v (BorderL3)", got, wantBorder)
	}
	wantBg := tokens.BgBase
	if got := img.RGBAAt(700, 76); got != (color.RGBA{R: wantBg.R, G: wantBg.G, B: wantBg.B, A: 255}) {
		t.Fatalf("content below header at (700,76) = %v, want BgBase", got)
	}

	// The static header's title row is 30px at y=10 and the tab row starts
	// after the 10px gap, at y=50.
	chat, ok := tt.Find("Chat")
	if !ok {
		t.Fatalf("Chat tab missing: %v", tt.Texts())
	}
	if chat.Y < 50 || chat.Y >= 75 {
		t.Fatalf("Chat tab y = %v, want inside the 25px tab row at y=50..75", chat.Y)
	}
	title, ok := tt.Find("Header Title Unique")
	if !ok {
		t.Fatalf("session title missing: %v", tt.Texts())
	}
	if title.Y < 10 || title.Y+title.H > 40 {
		t.Fatalf("title rect %+v is outside the 30px title row at y=10..40", title)
	}

	// The mode chip carries the localized full name and the tune icon: the
	// old chip exposed only "Standard" and a text chevron.
	mode, ok := tt.Find("Standard mode")
	if !ok {
		t.Fatalf("mode chip missing its Standard mode name: %v", tt.Texts())
	}
	if mode.H < 20 || mode.H > 24 {
		t.Fatalf("mode chip height = %v, want the 22px static row", mode.H)
	}
	if tt.HasText("▾") {
		t.Fatalf("live header still paints the 9px text chevron: %v", tt.Texts())
	}
	// The 14x14 tune icon starts the chip: its slot must contain ink.
	ink := 0
	for y := int(mode.Y) + 3; y < int(mode.Y+mode.H)-3; y++ {
		for x := int(mode.X); x < int(mode.X)+16 && x < int(mode.X+mode.W); x++ {
			c := img.RGBAAt(x, y)
			if c.R < 230 && c.G < 230 && c.B < 230 {
				ink++
			}
		}
	}
	if ink < 8 {
		t.Fatalf("mode chip has %d ink pixels in its 16px icon slot, want the tune icon", ink)
	}
}
