package home_test

// Fix-round regression test for SH-01: Home used to be a plain Column, so at
// the product's 900x600 minimum window the recent-session list below the fold
// was clipped with no way to reach it. The page body is a native Scroll now;
// the last recent conversation must become reachable by scrolling.

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// TestHomeRecentSessionsReachableAtMinWindow covers SH-01 at the product's
// minimum window: the recent list overflows 900x600 (the first-run setup card
// is showing), the last row starts below the window edge, and a wheel scroll
// over the page brings it inside the visible area.
func TestHomeRecentSessionsReachableAtMinWindow(t *testing.T) {
	sessions := make([]contracts.ChatSessionInfo, 0, 5)
	for i := 1; i <= 5; i++ {
		sessions = append(sessions, contracts.ChatSessionInfo{
			ID:    fmt.Sprintf("s%d", i),
			Title: fmt.Sprintf("会话%d", i),
		})
	}
	// A model without an API key keeps the first-run card visible, as in the
	// audit's reproduction, so the recent list starts low in the 600 DIP page.
	h := newHarness(t, &fakeChat{
		config:   contracts.ChatConfig{Model: "test-model"},
		sessions: sessions,
	})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 900, 600)
	tester.Frame()

	h.assertTexts(tester, "最近会话", "会话1", "会话5")
	initial, ok := tester.Find("会话5")
	if !ok {
		t.Fatalf("last recent row is not in the frame; texts = %v", tester.Texts())
	}
	if initial.Y+initial.H <= 600 {
		t.Fatalf("last recent row %+v is already inside the 900x600 window; the test needs overflowing content", initial)
	}

	// Scroll the page body with the wheel: a notch glides, so the Tester's
	// clock has to advance for the scroll to land, as a real desktop frame
	// would. Try both directions because the test only needs to prove the
	// content scrolls, not the desktop's natural-scroll direction.
	for _, dy := range []float32{900, -900} {
		tester.Wheel(500, 150, 0, dy)
		tester.Advance(400 * time.Millisecond)
		tester.Frame()
		if after, ok := tester.Find("会话5"); ok && after.H > 0 && after.Y < initial.Y {
			if after.Y < 0 || after.Y+after.H > 600 {
				t.Fatalf("scroll moved the last recent row to %+v, outside the 900x600 window", after)
			}
			return
		}
	}
	after, _ := tester.Find("会话5")
	t.Fatalf("scrolling does not reach the last recent row: before %+v, after %+v", initial, after)
}
