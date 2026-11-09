package session_test

// Frame tests for Batch C: message timestamps from CreatedAt (S-10), the user
// message's copy/edit actions (S-21), and the Memory pane's debounced search
// and Enter-to-add path (S-17).

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMessageTimestampsInActionRows(t *testing.T) {
	userAt := time.Date(2026, 11, 9, 17, 5, 0, 0, time.Local)
	assistantAt := time.Date(2026, 11, 9, 18, 41, 0, 0, time.Local)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "时间戳",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题", CreatedAt: userAt},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答", CreatedAt: assistantAt},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	// The action rows show the time of day (HH:MM) for both sides.
	h.assertTexts(tester, "17:05", "18:41")
	// The tooltip carries the full date and time.
	if info, ok := tester.Access("17:05"); !ok || info.Description != "2026-11-09 17:05" {
		t.Fatalf("user time tooltip = %+v found %v, want the full timestamp", info, ok)
	}
	if info, ok := tester.Access("18:41"); !ok || info.Description != "2026-11-09 18:41" {
		t.Fatalf("assistant time tooltip = %+v found %v, want the full timestamp", info, ok)
	}
}

func TestUserMessageActionsCopyAndEdit(t *testing.T) {
	const original = "原始问题：检查登录流程"
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "用户动作",
		Messages: []contracts.ChatMessage{
			message("u1", contracts.ChatRoleUser, original),
			message("a1", contracts.ChatRoleAssistant, "回答"),
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "复制原文", "编辑消息")

	// Copy the original text with a 32px target and the transient feedback.
	rect, ok := tester.Find("复制原文")
	if !ok || rect.W < 32 || rect.H < 32 {
		t.Fatalf("copy-original target = %+v found %v, want at least 32x32", rect, ok)
	}
	h.click(tester, "复制原文")
	if got := tester.Clipboard(); got != original {
		t.Fatalf("clipboard = %q, want the original user text", got)
	}
	if info, ok := tester.Access("复制原文"); !ok || info.Description != "已复制" {
		t.Fatalf("copy feedback = %+v found %v, want 已复制", info, ok)
	}
	tester.Advance(2 * time.Second)
	tester.Frame()
	if info, ok := tester.Access("复制原文"); !ok || info.Description == "已复制" {
		t.Fatalf("copy feedback did not reset: %+v found %v", info, ok)
	}

	// Edit puts the original text back in the composer and focuses it, without
	// sending anything.
	h.click(tester, "编辑消息")
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("edit auto-sent a message: %v", sends)
	}
	if !tester.Focused("输入消息…") {
		t.Fatalf("composer was not focused after edit; texts = %v", tester.Texts())
	}
	info, ok := tester.Access("输入消息…")
	if !ok || info.Value != original {
		t.Fatalf("composer draft = %+v found %v, want the original text", info, ok)
	}
}

// TestUserMessageMetaOnlyForText keeps the row honest: an attachment-only user
// message gets its time but no copy/edit actions.
func TestUserMessageMetaOnlyForText(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "附件消息",
		Messages: []contracts.ChatMessage{{
			ID: "u1", Role: contracts.ChatRoleUser, CreatedAt: time.Date(2026, 11, 9, 9, 30, 0, 0, time.Local),
			Attachments: []contracts.Attachment{{ID: "att-1", Name: "notes.md", MIME: "text/plain"}},
		}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "09:30")
	for _, text := range tester.Texts() {
		if strings.Contains(text, "复制原文") || strings.Contains(text, "编辑消息") {
			t.Fatalf("text actions on an attachment-only message: %v", tester.Texts())
		}
	}
}
