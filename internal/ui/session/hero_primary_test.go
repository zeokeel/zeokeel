package session_test

// 2026-11 UI review P2 (DESIGN rule 10 / AP-20): the empty-state hero action and
// the composer send must never both be filled accent primaries. With no draft the
// send is disabled and the hero keeps the primary; once a draft (or staged
// attachment) makes send the enabled primary, the hero yields to secondary.

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

	// Empty draft: the hero is the one primary; send is the neutral disabled look.
	if !isAccent("开始输入") {
		t.Fatalf("the hero action is not the primary on an empty draft")
	}
	if isAccent("发送") {
		t.Fatalf("the disabled send still paints an accent fill on an empty draft")
	}

	// A staged draft makes send the enabled primary; the hero yields to secondary
	// so the screen keeps exactly one accent primary.
	h.click(tester, "检查当前工作区并给出摘要")
	tester.Frame()
	if isAccent("开始输入") {
		t.Fatalf("the hero stayed primary with a draft: two accent primaries on screen")
	}
	if !isAccent("发送") {
		t.Fatalf("the enabled send is not the accent primary with a draft")
	}
}
