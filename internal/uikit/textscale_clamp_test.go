package uikit

// UD-02 (2026-11 audit): ChatMarkdown's heading clamps are absolute DIP bounds
// (H1 17–18, H2 ≤16, H3 ≤15 at the standard body). They must scale with the
// Tokens' TextScale like the body does, otherwise enlarged system text keeps
// document-small headings inside an enlarged reading column.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/design"
)

func TestChatMarkdownHeadingClampsFollowTextScale(t *testing.T) {
	tokens := design.Light()
	tokens.TextScale = 1.5
	body := tokens.Scale(14)
	options := ChatMarkdown(tokens, body)
	h1 := options.Theme.Headings[0].Size * body
	if h1 < tokens.Scale(17)-0.01 || h1 > tokens.Scale(18)+0.01 {
		t.Errorf("UD-02: H1 clamp did not scale with TextScale: got %.2f DIP, want %.1f–%.1f", h1, tokens.Scale(17), tokens.Scale(18))
	}
	h2 := options.Theme.Headings[1].Size * body
	if h2 > tokens.Scale(16)+0.01 {
		t.Errorf("UD-02: H2 clamp did not scale with TextScale: got %.2f DIP, want <= %.1f", h2, tokens.Scale(16))
	}
	// Control: at TextScale=1 the historical bounds hold byte-for-byte.
	plain := design.Light()
	plainOptions := ChatMarkdown(plain, 14)
	plainH1 := plainOptions.Theme.Headings[0].Size * 14
	if plainH1 < 17-0.01 || plainH1 > 18+0.01 {
		t.Fatalf("CONTROL INVALID: unscaled H1 clamp changed: %.2f", plainH1)
	}
}
