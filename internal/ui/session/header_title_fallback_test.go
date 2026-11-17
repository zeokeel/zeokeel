package session_test

// 2026-11 UI review B5: an open conversation with no title yet is named the same
// in the header title slot and the sidebar row ("未命名会话"/"Untitled session"),
// not the screen's generic "对话" fallback. A titled conversation keeps its title.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestEmptySessionTitleUsesSidebarFallback(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: ""}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	// The header title slot reads the sidebar's untitled fallback.
	if !tester.HasText("未命名会话") {
		t.Fatalf("the untitled session header does not use the sidebar fallback; texts = %v", tester.Texts())
	}
}

func TestTitledSessionKeepsItsTitle(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "发布检查清单"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "发布检查清单")
	h.assertNoTexts(tester, "未命名会话")
}
