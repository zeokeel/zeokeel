package settings_test

// Tests for the Behavior page's send-while-running queue toggle.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestBehaviorPageSendWhileRunningDefaultsOff(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "运行中发送", "运行中允许排队发送",
		"发送的提示会在当前回合结束后依次发出。")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "" {
		t.Fatalf("defaults wrote the send-while-running row: %q", raw)
	}

	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "true" {
		t.Fatalf("enabling wrote %q, want true", raw)
	}
	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "false" {
		t.Fatalf("disabling wrote %q, want false", raw)
	}
}

func TestBehaviorPageSendWhileRunningBadValueIsOff(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingSendWhileRunning: "garbage",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	// A malformed value is rendered as off, so one click enables it.
	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "true" {
		t.Fatalf("toggle from a malformed value wrote %q, want true", raw)
	}
}

func TestBehaviorPageSendWhileRunningEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	expandMoreConversationSettings(t, tester, "More conversation settings")
	h.assertTexts(tester, "Sending while running", "Queue prompts while running",
		"Queued prompts are sent one by one after the current turn finishes.")
	h.click(tester, "Queue prompts while running")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "true" {
		t.Fatalf("English toggle wrote %q, want true", raw)
	}
}
