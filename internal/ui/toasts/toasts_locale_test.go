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
// prefix AND its tool-id payload translate at render time (the payload is the
// stable id, resolved through uikit.ToolDisplayName in the active language).
func TestToastDynamicPrefixLocalizes(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyWarning, Text: "审批等待：bash",
	})
	tester.Frame()
	h.assertTexts(tester, "审批等待：运行命令")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Approval waiting: Run command", "Warning")
	h.assertNoTexts(tester, "审批等待")
}

// TestToastApprovalToolNameFollowsLanguageSwitch is the decisive P3 assertion
// (2026-11-17 UI review): one approval toast created in Chinese shows the
// friendly tool name in Chinese, then in English after the framework language
// switch — the same live toast, not a re-created one — and back again. The
// origin suffix and the timeout sibling translate with it; an unknown tool id
// falls back to the original string instead of inventing a translation.
func TestToastApprovalToolNameFollowsLanguageSwitch(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyWarning, Text: "审批等待：team_task_create（自动化）",
	})
	h.notifier.trigger(contracts.Notification{
		ID: "n2", Level: contracts.NotifyWarning, Text: "审批超时已自动拒绝：bash（自动化）",
	})
	tester.Frame()
	h.assertTexts(tester, "审批等待：团队任务（自动化）", "审批超时已自动拒绝：运行命令（自动化）")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester,
		"Approval waiting: Team task (Automation)",
		"Approval timed out and was auto-denied: Run command (automation)")
	h.assertNoTexts(tester, "团队任务", "审批等待")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "审批等待：团队任务（自动化）", "审批超时已自动拒绝：运行命令（自动化）")
	h.assertNoTexts(tester, "Team task", "Approval waiting")
}

// TestToastApprovalUnknownToolFallsBack covers the lead ruling on unknown ids:
// the payload passes through as the original string in both languages, and a
// legacy display-name payload (a toast created before the id switch) is not
// mistranslated either.
func TestToastApprovalUnknownToolFallsBack(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyWarning, Text: "审批等待：acme_ping",
	})
	h.notifier.trigger(contracts.Notification{
		ID: "n2", Level: contracts.NotifyWarning, Text: "审批等待：团队任务",
	})
	tester.Frame()
	h.assertTexts(tester, "审批等待：acme_ping", "审批等待：团队任务")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Approval waiting: acme_ping", "Approval waiting: 团队任务")
}

// TestToastApprovalEnglishCreatedLocalizesToChinese covers the reverse
// direction: an English-created approval notice (prefix, id payload and origin
// suffix) renders fully in Chinese after the switch.
func TestToastApprovalEnglishCreatedLocalizesToChinese(t *testing.T) {
	h := newHarness(t, "en")
	tester := h.tester()
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifyWarning, Text: "Approval waiting: bash (Subagent)",
	})
	tester.Frame()
	h.assertTexts(tester, "Approval waiting: Run command (Subagent)")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "审批等待：运行命令（子代理）")
	h.assertNoTexts(tester, "Approval waiting")
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
