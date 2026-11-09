package design_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// TestTokensFollowTextScale covers the desktop text-size preference wiring:
// the brand type scale is applied through design.Of, and a custom theme does
// not receive mygo's automatic scaling, so the tokens must carry it.
func TestTokensFollowTextScale(t *testing.T) {
	var got *design.Tokens
	view := func(c *ui.Context) {
		got = design.Of(c)
		ui.Text(c, "probe")
	}
	tt := ui.NewTesterWith(view, 320, 200, ui.TesterOptions{
		Preferences: ui.Preferences{TextScale: 1.5, ReduceMotion: true},
	})
	tt.Frame()
	if got == nil {
		t.Fatal("design.Of was not called")
	}
	if got.TextScale != 1.5 {
		t.Fatalf("TextScale = %v, want 1.5 from Preferences", got.TextScale)
	}
	size, line, _ := got.Metrics(design.TypeBody)
	if size != 21 || line != 33 {
		t.Fatalf("body metrics = %v/%v, want 21/33 at 1.5x", size, line)
	}

	// The default tester has the desktop's usual scale: 1.
	defaultTokens := func() *design.Tokens {
		var tokens *design.Tokens
		tt := ui.NewTesterWith(func(c *ui.Context) { tokens = design.Of(c) }, 320, 200, ui.TesterOptions{})
		tt.Frame()
		return tokens
	}()
	if defaultTokens.TextScale != 1 {
		t.Fatalf("default TextScale = %v, want 1", defaultTokens.TextScale)
	}
}
