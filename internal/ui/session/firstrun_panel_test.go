package session_test

// UD-22 (2026-11 audit): the first-run checklist card uses the t.Panel() preset
// (BgRaised + BorderL2 + RadiusLG), not a hand-picked RadiusMD — one surface
// ramp step, one corner token, like Home's setup card.

import (
	"image/color"
	"math"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// TestFirstRunChecklistCardUsesPanelPreset probes the card's corner: along the
// top-left diagonal the first border pixel sits at about R·(1−1/√2), so the
// panel preset's RadiusLG corner is distinguishable from a RadiusMD one while
// the ladder's own value stays the single source (it was 16, it is 12 now).
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
	// The expected offset comes from the token, not from a literal: a panel
	// corner (RadiusLG) cuts at ~R·(1−1/√2), while the old hand-picked RadiusMD
	// cuts earlier. The frame may round one DIP either way.
	want := int(math.Round(float64(design.RadiusLG) * (1 - 1/math.Sqrt2)))
	if firstBorder < want-1 {
		t.Fatalf("UD-22: card corner border at diagonal d=%d, want ≥%d "+
			"(RadiusLG %.0f panel preset, not the hand-picked RadiusMD)", firstBorder, want-1, design.RadiusLG)
	}
}
