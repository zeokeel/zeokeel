package toasts

// US-14 (2026-11 audit): the toast body and its action label localize at RENDER
// time, like the level title already does — switching the language while a
// toast is alive must not leave a mixed-language card (zh body under an en
// title). Known product copy translates both ways; unknown copy passes through.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestToastBodyLocalizesOnLanguageSwitch is the decisive US-14 assertion: a
// toast created in Chinese renders fully in English after the switch (body,
// action label and level title), and back again.
func TestToastBodyLocalizesOnLanguageSwitch(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifySuccess, Text: "已删除记忆", ActionLabel: "撤销",
	})
	tester.Frame()
	h.assertTexts(tester, "已删除记忆", "成功", "撤销")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Memory deleted", "Success", "Undo")
	h.assertNoTexts(tester, "已删除记忆", "撤销")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "已删除记忆", "成功", "撤销")
	h.assertNoTexts(tester, "Memory deleted", "Undo")
}

// TestToastEnglishBodyLocalizesToChinese covers the reverse direction: an
// English-created toast (a backend that localized at creation) renders in
// Chinese after the switch.
func TestToastEnglishBodyLocalizesToChinese(t *testing.T) {
	h := newHarness(t, "en")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyInfo, Text: "Stopped",
	})
	tester.Frame()
	h.assertTexts(tester, "Stopped", "Info")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "已停止", "提示")
	h.assertNoTexts(tester, "Stopped")
}

// TestToastDynamicPrefixLocalizes covers dynamic copy: the approval-waiting
// prefix translates while the payload (a raw tool id) stays verbatim.
func TestToastDynamicPrefixLocalizes(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyWarning, Text: "审批等待：bash",
	})
	tester.Frame()
	h.assertTexts(tester, "审批等待：bash")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Approval waiting: bash", "Warning")
	h.assertNoTexts(tester, "审批等待")
}

// TestToastUnknownCopyPassesThrough keeps the layer honest: copy the phrasebook
// does not know is shown as created (no invented translation).
func TestToastUnknownCopyPassesThrough(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyInfo, Text: "一条自定义通知",
	})
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "一条自定义通知")
}
