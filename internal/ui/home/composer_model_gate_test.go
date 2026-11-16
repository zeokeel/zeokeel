package home_test

// Frame tests for UX-09 (2026-11 wave D): the Home composer must gate sending
// on a configured model with the SAME semantics as the conversation composer
// (S7) — without a model, Send is disabled, a localized notice explains why and
// offers the "打开模型设置" deep link, and the keyboard path (Enter) neither
// creates an empty session nor fires a request that can only fail. Before the
// fix Home sent anyway, creating an empty session and surfacing the raw
// backend error ("rig: no model configured").

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// TestHomeComposerNoModelDisablesSend covers the gate: no model → disabled
// Send, the localized notice and the deep link, and Enter creates nothing.
func TestHomeComposerNoModelDisablesSend(t *testing.T) {
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{}}} // no model
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	// The composer says why sending is disabled and offers the fix (FR-31).
	h.assertTexts(tester, "尚未配置模型：发送已禁用", "打开模型设置")
	send, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if !send.Disabled {
		t.Fatal("UX-09: Send is enabled without a configured model")
	}

	// The keyboard path respects the same gate: a typed draft + Enter creates
	// no session and sends nothing.
	clickHome(t, tester, "问点什么…")
	tester.Type("hello without a model")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	news, sends, _ := chat.snapshot()
	if len(news) != 0 || len(sends) != 0 {
		t.Fatalf("UX-09: Enter without a model created/sent: news=%v sends=%v", news, sends)
	}

	// The deep link opens the settings surface (the model page when registered).
	clickHome(t, tester, "打开模型设置")
	tester.Frame()
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("UX-09: the 打开模型设置 action did not open the settings surface")
	}
}

// TestHomeComposerWithModelEnablesSend is the contrast: a configured model
// hides the no-model notice and enables Send (the gate is model-specific, not
// a blanket disable).
func TestHomeComposerWithModelEnablesSend(t *testing.T) {
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "m"}}}
	h := newComposerHarness(t, chat, nil, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	h.assertNoTexts(tester, "尚未配置模型：发送已禁用")
	clickHome(t, tester, "问点什么…")
	tester.Type("hello with a model")
	tester.Frame()
	send, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if send.Disabled {
		t.Fatal("Send is disabled with a configured model and a non-empty draft")
	}
	clickHome(t, tester, "发送")
	_, sends, _ := chat.snapshot()
	if len(sends) != 1 || sends[0].prompt != "hello with a model" {
		t.Fatalf("sends = %v, want the typed draft", sends)
	}
}
