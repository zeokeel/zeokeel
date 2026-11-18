package session_test

// 2026-11 UI review P2 (DESIGN rule 10 / AP-20) after the copy audit: the empty
// conversation's hero no longer carries a filled accent action at all (a button
// that only moved the caret was removed, A5 F-13), so the composer's send is the
// screen's one primary — and it only paints the accent fill once it can send.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestHeroAndComposerKeepSinglePrimary(t *testing.T) {
	chat := newFakeChat() // configured model + API key by default
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "空会话"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	isAccent := func(label string) bool {
		return channelSpread(sendButtonFill(t, tester, label)) > 40
	}

	// Empty draft: no accent fill anywhere — the disabled send keeps the visible
	// neutral look (BgStrong + tertiary ink, VD-20) and the hero has no action.
	if isAccent("发送") {
		t.Fatalf("the disabled send paints an accent fill on an empty draft")
	}
	h.assertNoTexts(tester, "开始输入", "开始新对话")

	// A staged draft makes send the screen's one enabled primary.
	h.click(tester, "检查当前工作区并给出摘要")
	tester.Frame()
	if !isAccent("发送") {
		t.Fatalf("the enabled send is not the accent primary with a draft")
	}
}
