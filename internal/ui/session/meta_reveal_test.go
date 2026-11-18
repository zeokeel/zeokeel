package session_test

// Frame tests for the message meta row (B-spec §3.2.4, VD-11): the clock, the
// usage summary and the ⋯ actions are one fixed 24 DIP line at the bottom of a
// message, revealed while the pointer rests on the message or the keyboard focus
// is inside it. The line is always built — assistive technology always reads it
// and Tab always reaches the ⋯ — so revealing it never moves the transcript.

import (
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// metaChat is one finished turn with a clock and usage.
func metaChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "元信息",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题", CreatedAt: time.Unix(1700000000, 0)},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答",
				CreatedAt: time.Unix(1700000005, 0),
				Usage:     &contracts.MessageUsage{DurationMS: 3200, TotalTokens: 1234},
			},
		},
	}, true)
	return chat
}

// inkIn reports whether the frame shows any pixel unlike the page base inside r.
func inkIn(tester *ui.Tester, r ui.Rect) bool {
	img := tester.Image()
	base := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if img.RGBAAt(x, y) != base {
				return true
			}
		}
	}
	return false
}

// TestMessageMetaRevealsOnHoverOrFocus covers the reveal: the meta line paints
// nothing at rest, paints over the message under the pointer, and its presence
// never moves the reply above it.
func TestMessageMetaRevealsOnHoverOrFocus(t *testing.T) {
	h := newHarness(t, metaChat())
	tester := h.tester()
	tester.Frame()

	// Built (assistive technology and Tab reach it) but not painted.
	meta, ok := tester.Find("用时 3.2s · 1.2K tokens")
	if !ok {
		t.Fatalf("meta line is not built; texts = %v", tester.Texts())
	}
	if inkIn(tester, meta) {
		t.Fatalf("meta line paints at rest (want it revealed on hover/focus only)")
	}
	reply, ok := tester.Find("回答")
	if !ok {
		t.Fatalf("reply missing; texts = %v", tester.Texts())
	}
	beforeY := reply.Y

	// Hovering the message reveals it.
	tester.Move(reply.X+reply.W/2, reply.Y+reply.H/2)
	tester.Advance(design.MotionFast + 20*time.Millisecond)
	tester.Frame()
	tester.Advance(design.MotionFast + 20*time.Millisecond)
	tester.Frame()
	if !inkIn(tester, meta) {
		t.Fatalf("meta line stays hidden while the pointer rests on the message")
	}

	// The row is a fixed line: revealing it did not move the reply.
	after, ok := tester.Find("回答")
	if !ok || after.Y != beforeY {
		t.Fatalf("reply moved on reveal: %.1f → %.1f", beforeY, after.Y)
	}

	// Leaving hides it again.
	tester.Move(0, 0)
	tester.Advance(design.MotionFast + 20*time.Millisecond)
	tester.Frame()
	tester.Advance(design.MotionFast + 20*time.Millisecond)
	tester.Frame()
	if inkIn(tester, meta) {
		t.Fatalf("meta line stays visible after the pointer left the message")
	}
}

// TestMessageMetaShowsForTwoSecondsAfterStreaming covers the one automatic
// reveal: a reply that just finished shows its meta for two seconds on its own,
// then the transcript goes quiet again without another interaction.
func TestMessageMetaShowsForTwoSecondsAfterStreaming(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "流式收尾",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题", CreatedAt: time.Unix(1700000000, 0)},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答", Streaming: true,
				CreatedAt: time.Unix(1700000005, 0),
				Usage:     &contracts.MessageUsage{DurationMS: 900, TotalTokens: 42},
			},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	// A streaming reply has no meta line yet: its clock and usage are not final.
	if tester.HasText("用时 900ms · 42 tokens") {
		t.Fatalf("a streaming reply already shows its meta; texts = %v", tester.Texts())
	}

	// The turn settles: the row shows at once, without the pointer.
	session, _ := chat.Session("sess-1")
	session.Messages[1].Streaming = false
	chat.updateSession(session)
	tester.Frame()
	meta, ok := tester.Find("用时 900ms · 42 tokens")
	if !ok {
		t.Fatalf("settled reply has no meta line; texts = %v", tester.Texts())
	}
	tester.Frame()
	if !inkIn(tester, meta) {
		t.Fatalf("the settle reveal does not paint the meta line")
	}

	// Two seconds later it hides itself again.
	tester.Advance(2*time.Second + 50*time.Millisecond)
	tester.Frame()
	if inkIn(tester, meta) {
		t.Fatalf("the meta line is still painted two seconds after the reply settled")
	}
}

// TestMessageMetaActionsReachableByKeyboard covers the keyboard route: Tab
// reaches the ⋯ of a message the pointer never touched, and focusing it reveals
// the whole line (time together with the actions, never only one of them).
func TestMessageMetaActionsReachableByKeyboard(t *testing.T) {
	h := newHarness(t, metaChat())
	tester := h.tester()
	tester.Frame()

	focused := false
	for i := 0; i < 24 && !focused; i++ {
		tester.Key(0, ui.KeyTab)
		tester.Frame()
		focused = tester.Focused("消息操作")
	}
	if !focused {
		t.Fatalf("Tab never reached the message actions; texts = %v", tester.Texts())
	}
	meta, ok := tester.Find("用时 3.2s · 1.2K tokens")
	if !ok || !inkIn(tester, meta) {
		t.Fatalf("focusing the ⋯ does not reveal the meta line")
	}
}
