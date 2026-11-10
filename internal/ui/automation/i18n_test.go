package automation_test

// i18n frame tests: the Automation screen copy follows the host locale on every
// frame (English tabs/actions/forms now, Chinese after switching back) while zh
// stays the default.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

func TestAutomationEnglishCopyAndLanguageSwitch(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID:       "task-1",
		Name:     "Nightly check",
		Prompt:   "check deps",
		Schedule: "every 60s",
		Enabled:  true,
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	// Default Chinese copy is unchanged.
	h.assertTexts(tester, "自动化", "新建任务", "立即运行", "任务操作",
		"上次运行：—", "下次运行：—")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Automation", "New task", "Run now", "Task actions",
		"Last run: —", "Next run: —", "Nightly check", "Enabled")
	h.assertNoTexts(tester, "自动化", "新建任务", "立即运行", "上次运行：")

	// The inline create form is translated as well.
	h.click(tester, "New task")
	h.assertTexts(tester, "New automation task", "Task name", "Task prompt",
		"Schedule", "Enabled", "Cancel", "Save",
		"Supports */N * * * * (every N minutes)")
	h.assertNoTexts(tester, "新建自动化任务", "任务名称", "任务提示词", "运行计划", "保存")

	// Switching back restores the Chinese copy on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "自动化", "取消", "立即运行", "新建自动化任务", "保存")
	h.assertNoTexts(tester, "New task", "New automation task")
}

func TestAutomationEnglishEmptyBackend(t *testing.T) {
	h := newHarness(t, nil, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "自动化后端未启用")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Automation backend not enabled")
	h.assertNoTexts(tester, "自动化后端未启用")
}
