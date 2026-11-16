package session_test

// UD-22 (2026-11 audit): the first-run checklist card uses the t.Panel() preset
// (BgRaised + BorderL2 + RadiusLG), not a hand-picked RadiusMD — one surface
// ramp step, one corner token, like Home's setup card.

import (
	"image/color"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// TestFirstRunChecklistCardUsesPanelPreset probes the card's corner: at 4 DIP
// along the diagonal a RadiusLG (16) corner has already cut away to the page
// base, while the old RadiusMD (10) corner would still show the raised fill.
func TestFirstRunChecklistCardUsesPanelPreset(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{} // no model/key → the checklist shows
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "首启"}, true)
	h := newWorkspaceHarness(t, chat, &fakeWorkspace{noCurrent: true})
	tester := h.tester()
	tester.Frame()

	title, ok := tester.Find("开始前还需要：")
	if !ok {
		t.Fatalf("first-run checklist missing; texts = %v", tester.Texts())
	}
	// The card pads Space4 horizontally and Space3 vertically around the title.
	// BgRaised and BgBase are both white in the light theme, so the corner is
	// read through the border arc: along the top-left diagonal the first
	// non-white pixel sits at ≈R·(1−1/√2) — ~3 DIP for RadiusMD (10), ~5 for
	// RadiusLG (16). The panel preset's larger corner pushes it past 4.
	cardX := title.X - design.Space4
	cardY := title.Y - design.Space3
	firstBorder := -1
	for d := 0; d <= 8; d++ {
		got := tester.Image().RGBAAt(int(cardX)+d, int(cardY)+d)
		if !nearColor(got, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 3) {
			firstBorder = d
			break
		}
	}
	if firstBorder < 0 {
		t.Fatalf("UD-22: no card border found along the corner diagonal")
	}
	if firstBorder < 4 {
		t.Fatalf("UD-22: card corner border at diagonal d=%d, want ≥4 "+
			"(RadiusLG panel preset, not the hand-picked RadiusMD)", firstBorder)
	}
}
