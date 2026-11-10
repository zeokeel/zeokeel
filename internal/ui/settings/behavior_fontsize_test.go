package settings_test

// Tests for the Behavior page's conversation font-size selector.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestBehaviorPageChatFontSizeDefaultsStandard(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "消息字号", "标准")
	if raw := store.get(contracts.SettingChatFontSize); raw != "" {
		t.Fatalf("defaults wrote the chat-font-size row: %q", raw)
	}
	h.click(tester, "消息字号：标准")
	h.assertTexts(tester, "小", "大")
	h.click(tester, "大")
	if raw := store.get(contracts.SettingChatFontSize); raw != "large" {
		t.Fatalf("selecting large wrote %q", raw)
	}
	h.assertTexts(tester, "已保存", "大")

	h.click(tester, "消息字号：大")
	h.click(tester, "小")
	if raw := store.get(contracts.SettingChatFontSize); raw != "small" {
		t.Fatalf("selecting small wrote %q", raw)
	}
}

func TestBehaviorPageChatFontSizeBadValueNormalized(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingChatFontSize: "garbage",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "标准")
	h.click(tester, "消息字号：标准")
	h.click(tester, "小")
	if raw := store.get(contracts.SettingChatFontSize); raw != "small" {
		t.Fatalf("selecting small from a malformed value wrote %q", raw)
	}
}

func TestBehaviorPageChatFontSizeEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	expandMoreConversationSettings(t, tester, "More conversation settings")
	h.assertTexts(tester, "Message text size", "Standard")
	h.click(tester, "Text size: Standard")
	h.click(tester, "Large")
	if raw := store.get(contracts.SettingChatFontSize); raw != "large" {
		t.Fatalf("English selection wrote %q, want large", raw)
	}
}
