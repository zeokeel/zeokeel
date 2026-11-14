package automation_test

// Frame tests for the DSH schedule selectors on the Automation screen: the
// human-readable labels of weekly/cron/at rules, the form's schedule template
// menu and the "every run in a new session" option.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

func TestTaskCardWallClockScheduleLabels(t *testing.T) {
	service := newFakeAutomation(
		contracts.AutomationTask{
			ID: "t-week", Name: "周报", Prompt: "p",
			Schedule: "weekly 09:00:00.000 Asia/Shanghai 1,3", Enabled: true,
		},
		contracts.AutomationTask{
			ID: "t-cron", Name: "工作日", Prompt: "p",
			Schedule: "cron 0 9 * * 1-5 Asia/Shanghai", Enabled: true,
		},
		contracts.AutomationTask{
			ID: "t-at", Name: "一次", Prompt: "p",
			Schedule: "at 2026-12-01T09:30:00.000Z", Enabled: true,
		},
		contracts.AutomationTask{
			ID: "t-utc", Name: "UTC 任务", Prompt: "p",
			Schedule: "cron 0 9 * * * UTC", Enabled: true,
		},
	)
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()

	h.assertTexts(tester,
		"每周一、三 09:00",
		"Cron 0 9 * * 1-5",
		"运行一次",
		"Cron 0 9 * * *（UTC）")
	h.assertNoTexts(tester, "未知计划")
}

func TestTaskCardWallClockScheduleLabelsEnglish(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "t-week", Name: "Weekly", Prompt: "p",
		Schedule: "weekly 09:00:00.000 UTC 1,3", Enabled: true,
	})
	h := newHarness(t, service, automation.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Weekly on Mon, Wed at 09:00 (UTC)")
}

func TestCreateFormScheduleTemplateMenu(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")
	h.click(tester, "计划模板")
	h.assertTexts(tester, "每 N 分钟", "每天", "每周", "自定义 Cron", "一次")
	h.click(tester, "每周")
	tester.Frame()
	// The draft lives in the text field; the preview proves the template parsed.
	h.assertTexts(tester, "下次运行：", "每周一 09:00")

	// The inserted template is a normal editable draft; saving stores it.
	h.click(tester, "任务名称")
	tester.Type("每周任务")
	h.click(tester, "任务提示词")
	tester.Type("p")
	tester.Frame()
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].Schedule != "weekly 09:00:00.000 "+contracts.AutomationDefaultTimeZone+" 1" {
		t.Fatalf("Create = %+v, want the weekly template", created)
	}
}

func TestCreateFormNewSessionPerRunOption(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")
	h.assertTexts(tester, "运行会话", "每次新建会话")
	h.click(tester, "每次新建会话")
	fillCreateForm(t, h, tester, "每次新会话", "p", "every 60s")
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || !created[0].NewSessionPerRun {
		t.Fatalf("Create = %+v, want NewSessionPerRun", created)
	}

}

func TestEditFormNewSessionPerRunOption(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "绑定任务", Prompt: "p", Schedule: "every 60s", Enabled: true,
		NewSessionPerRun: true, SessionID: "sess-bound",
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "编辑")
	tester.Frame()
	h.assertTexts(tester, "每次新建会话")
	h.click(tester, "每次新建会话")
	tester.Frame()
	h.click(tester, "保存")
	updated := service.updated()
	if len(updated) != 1 || updated[0].NewSessionPerRun || updated[0].SessionID != "sess-bound" {
		t.Fatalf("Update = %+v, want the option off and the binding kept", updated)
	}
}
