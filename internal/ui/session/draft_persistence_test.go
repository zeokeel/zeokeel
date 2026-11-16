package session_test

// UX-18 (2026-11 audit, lead裁决: implement): per-session composer drafts
// persist through the settings store and survive an app restart; the existing
// in-memory switch save/restore keeps working. Attachments stay in memory (the
// settings store carries the draft text, not file bytes).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// twoSessionChat is two conversations for the switch/restart flows.
func twoSessionChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-a", Title: "会话甲",
		Messages: []contracts.ChatMessage{{ID: "a1", Role: contracts.ChatRoleUser, Text: "甲的问题"}},
	}, true)
	chat.addSession(contracts.ChatSession{
		ID: "sess-b", Title: "会话乙",
		Messages: []contracts.ChatMessage{{ID: "b1", Role: contracts.ChatRoleUser, Text: "乙的问题"}},
	}, false)
	return chat
}

// TestDraftPersistsAcrossRestart is the decisive UX-18 assertion: a draft typed
// in one conversation is restored after the module is disposed and re-activated
// on the same settings store (the app-restart shape).
func TestDraftPersistsAcrossRestart(t *testing.T) {
	settings := newPermissionSettings(nil)
	chat1 := twoSessionChat()
	h1 := newPermissionHarness(t, chat1, settings)
	tester1 := h1.tester()
	tester1.Frame()
	focusComposer(t, tester1)
	tester1.Type("重启前的草稿")
	tester1.Frame()

	// Switching away saves the draft (the existing behavior) and persists it.
	if err := chat1.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("switch session: %v", err)
	}
	tester1.Frame()
	info, ok := tester1.Access("输入消息…")
	if !ok || info.Value != "" {
		t.Fatalf("draft leaked into the other conversation: %+v", info)
	}

	// The restart: dispose the first module tree, boot a fresh one on the same
	// settings store and the same conversation data.
	if err := h1.ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first tree: %v", err)
	}
	chat2 := twoSessionChat()
	h2 := newPermissionHarness(t, chat2, settings)
	tester2 := h2.tester()
	tester2.Frame()
	info, ok = tester2.Access("输入消息…")
	if !ok || info.Value != "重启前的草稿" {
		t.Fatalf("UX-18: draft not restored after restart: %+v found %v", info, ok)
	}
}

// TestSentDraftDoesNotResurrectAfterRestart covers the clear path: a sent draft
// is persisted empty, so a restart does not bring it back.
func TestSentDraftDoesNotResurrectAfterRestart(t *testing.T) {
	settings := newPermissionSettings(nil)
	chat1 := twoSessionChat()
	h1 := newPermissionHarness(t, chat1, settings)
	tester1 := h1.tester()
	tester1.Frame()
	focusComposer(t, tester1)
	tester1.Type("会被发送的草稿")
	tester1.Frame()
	tester1.Key(0, ui.KeyEnter)
	tester1.Frame()
	if sends := chat1.sends(); len(sends) != 1 {
		t.Fatalf("send calls = %v, want 1", sends)
	}

	if err := h1.ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first tree: %v", err)
	}
	chat2 := twoSessionChat()
	h2 := newPermissionHarness(t, chat2, settings)
	tester2 := h2.tester()
	tester2.Frame()
	info, ok := tester2.Access("输入消息…")
	if !ok || info.Value != "" {
		t.Fatalf("UX-18: a sent draft resurrected after restart: %+v", info)
	}
}

// TestDraftSwitchSaveRestoreStillWorks is the regression control: without a
// settings store the in-memory per-session save/restore keeps its behavior.
func TestDraftSwitchSaveRestoreStillWorks(t *testing.T) {
	chat := twoSessionChat()
	h := newHarness(t, chat) // no settings service
	tester := h.tester()
	tester.Frame()
	focusComposer(t, tester)
	tester.Type("甲的草稿")
	tester.Frame()

	if err := chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	tester.Frame()
	info, _ := tester.Access("输入消息…")
	if info.Value != "" {
		t.Fatalf("draft leaked on switch: %+v", info)
	}
	if err := chat.SetActiveSession("sess-a"); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	tester.Frame()
	info, _ = tester.Access("输入消息…")
	if info.Value != "甲的草稿" {
		t.Fatalf("draft not restored on switch back: %+v", info)
	}
}
