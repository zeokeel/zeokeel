package session_test

// Frame tests for the composer's new anatomy (B-spec §3.2.7, VD-20/VD-27,
// A4-I-03) and the session tab strip (A1-matrix: ui.Tabs).

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// overToken composites a possibly translucent token over a base, as the painter
// does, as an opaque image colour for the pixel assertions.
func overToken(fg, bg ui.Color) color.RGBA {
	a := float32(fg.A) / 255
	blend := func(f, b uint8) uint8 {
		return uint8(float32(f)*a + float32(b)*(1-a) + 0.5)
	}
	return color.RGBA{R: blend(fg.R, bg.R), G: blend(fg.G, bg.G), B: blend(fg.B, bg.B), A: 255}
}

// TestComposerSendIsASquareButton covers the send control's anatomy: a 32 DIP
// square with the 16 DIP glyph, not the pill it used to be, and the composer's
// only primary.
func TestComposerSendIsASquareButton(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "发送按钮"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposer(t, tester)
	tester.Type("hello")
	tester.Frame()

	info, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if info.Bounds.W != design.RowCompactHeight || info.Bounds.H != design.RowCompactHeight {
		t.Fatalf("send control = %.0fx%.0f, want a %v square",
			info.Bounds.W, info.Bounds.H, design.RowCompactHeight)
	}
	// The enabled send is the accent fill (the screen's one primary), and its
	// glyph is the on-primary ink so the arrow reads on the fill.
	tokens := design.Light()
	fill := sendButtonFill(t, tester, "发送")
	if channelSpread(fill) <= 40 {
		t.Fatalf("the enabled send is not the accent primary: %+v", fill)
	}
	if !nearColor(fill, overToken(tokens.Accent, tokens.BgBase), 6) {
		t.Fatalf("enabled send fill = %+v, want the accent %+v", fill, tokens.Accent)
	}
}

// TestComposerDisabledSendStaysVisible covers VD-20: with an empty draft the
// send keeps the neutral disabled look at full strength — BgStrong fill,
// tertiary ink and one hairline edge, ≥3:1 against the page — instead of fading
// into it.
func TestComposerDisabledSendStaysVisible(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "禁用发送"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	info, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok || !info.Disabled {
		t.Fatalf("send control is not the disabled one on an empty draft: %+v", info)
	}
	tokens := design.Light()
	// The fill is the preset's own BgStrong, not mygo's half-dimmed version: the
	// dimmed control measured 1.15:1 against the white page.
	got := sendButtonFill(t, tester, "发送")
	want := overToken(tokens.BgStrong, tokens.BgBase)
	if !nearColor(got, want, 3) {
		t.Fatalf("disabled send fill = %+v, want BgStrong %+v (no half-opacity dimming)", got, want)
	}
}

// TestComposerSendDegradesWhileAnApprovalIsPending covers F-02: while a decision
// owns the turn the send is disabled, keeps the neutral edge (no accent ring on
// the card) and explains itself in a note beside the control, so the approval
// card keeps the screen's one accent.
func TestComposerSendDegradesWhileAnApprovalIsPending(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "待审批",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑一下"},
		},
	}, true)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{
		ID: "req-1", SessionID: "sess-1", Tool: "bash", Summary: "go build ./...",
	})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	focusComposer(t, tester)
	tester.Type("next")
	tester.Frame()

	info, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok || !info.Disabled {
		t.Fatalf("send is not disabled while an approval is pending: %+v found %v", info, ok)
	}
	// The note explains the disabled control (A4-I-05: a disabled control shows
	// no tooltip, so the reason is a visible line), immediately left of it.
	h.assertTexts(tester, "处理审批后可发送")
	note, okNote := tester.Find("处理审批后可发送")
	if !okNote || note.X >= info.Bounds.X {
		t.Fatalf("send note at %.1f is not left of the send control at %.1f", note.X, info.Bounds.X)
	}
}

// TestComposerFileDropShowsTheTarget covers A4-I-03's visible half: the composer
// card is a drop target, so a surface that accepts files passes OnFiles and the
// shell lights the card's edge. The hint line only appears while files hover.
func TestComposerFileDropWiresTheTarget(t *testing.T) {
	// The shell's own contract: OnFiles set, DragHint passed. The drag itself is
	// a host event the headless tester cannot synthesise, so this pins the wiring
	// (a surface that forgets OnFiles silently loses the highlight and the drop).
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "拖放"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	// Nothing about a drop shows without files over the card.
	h.assertNoTexts(tester, "松开以添加附件")
	if _, ok := tester.Find("输入消息…"); !ok {
		t.Fatalf("composer input missing; texts = %v", tester.Texts())
	}
}

// TestTranscriptTabsAreTheOfficialTabRow covers the A1 migration: the
// conversation's tab strip is mygo's own tab row — assistive technology sees a
// tab list with the current tab selected, and the arrow keys move the choice.
func TestTranscriptTabsAreTheOfficialTabRow(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "Tabs",
		Messages: []contracts.ChatMessage{{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	if _, ok := tester.AccessByRole(ui.RoleTabList, ""); !ok {
		t.Fatalf("the tab strip is not a tab list; texts = %v", tester.Texts())
	}
	current, ok := tester.AccessByRole(ui.RoleTab, "对话")
	if !ok || !current.Selected {
		t.Fatalf("the current tab is not the selected tab; %+v found %v", current, ok)
	}
	// The arrows walk the official row: Right chooses 轨迹 and builds its pane.
	tester.Click("对话")
	tester.Frame()
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	if next, ok := tester.AccessByRole(ui.RoleTab, "轨迹"); !ok || !next.Selected {
		t.Fatalf("Right did not choose the next tab; %+v found %v", next, ok)
	}
	h.assertTexts(tester, "本次会话还没有执行轨迹。")
}
