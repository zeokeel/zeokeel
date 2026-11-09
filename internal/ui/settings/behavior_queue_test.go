package settings_test

// Tests for the Behavior page's send-while-running queue toggle: queueing is
// the default (missing/empty/malformed rows mean on), only an explicit "false"
// disables it.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestBehaviorPageSendWhileRunningDefaultsOn(t *testing.T) {
	store := newFakeSettings(nil) // no row written: the default applies
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "运行中发送", "运行中允许排队发送",
		"默认开启。运行中发送的提示会排队，在本轮结束后依次发出；关闭后回复生成期间 Enter 不可用，草稿会保留到本轮结束。")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "" {
		t.Fatalf("defaults wrote the send-while-running row: %q", raw)
	}

	// The toggle starts on: one click opts out and writes the explicit "false".
	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "false" {
		t.Fatalf("disabling wrote %q, want false", raw)
	}
	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "true" {
		t.Fatalf("enabling wrote %q, want true", raw)
	}
}

func TestBehaviorPageSendWhileRunningBadValueDefaultsOn(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingSendWhileRunning: "garbage",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	// A malformed value keeps the default (on), so one click disables it.
	h.click(tester, "运行中允许排队发送")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "false" {
		t.Fatalf("toggle from a malformed value wrote %q, want false", raw)
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
		"On by default. Prompts sent while running are queued and sent after the current turn; when off, Enter is unavailable while a reply streams and the draft is kept until the turn ends.")
	h.click(tester, "Queue prompts while running")
	if raw := store.get(contracts.SettingSendWhileRunning); raw != "false" {
		t.Fatalf("English toggle wrote %q, want false", raw)
	}
}
