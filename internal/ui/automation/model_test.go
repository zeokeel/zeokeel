package automation_test

// Tests for the Automation screen's per-task model field: the optional model
// input in the inline create form (trimmed, empty inherits the default) and the
// model badge on the task card, plus the English copy.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

func TestCreateTaskWithModel(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")
	h.assertTexts(tester, "任务名称", "任务提示词", "运行计划", "模型", "保存")

	h.click(tester, "任务名称")
	tester.Type("带模型任务")
	h.click(tester, "任务提示词")
	tester.Type("使用指定模型")
	h.click(tester, "运行计划")
	tester.Type("every 60s")
	// Models are chosen from the catalog menu; the custom entry edits an id.
	h.click(tester, "跟随默认模型")
	h.click(tester, "自定义模型…")
	h.click(tester, "自定义模型 ID")
	tester.Type(" gpt-4o ")
	tester.Frame()
	h.click(tester, "保存")

	created := service.created()
	if len(created) != 1 {
		t.Fatalf("Create calls = %+v, want one", created)
	}
	if created[0].Model != "gpt-4o" {
		t.Fatalf("Create model = %q, want the trimmed gpt-4o", created[0].Model)
	}
}

func TestCreateTaskWithoutModelInheritsDefault(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")
	h.click(tester, "任务名称")
	tester.Type("默认模型任务")
	h.click(tester, "任务提示词")
	tester.Type("p")
	h.click(tester, "运行计划")
	tester.Type("every 60s")
	tester.Frame()
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 {
		t.Fatalf("Create calls = %+v, want one", created)
	}
	if created[0].Model != "" {
		t.Fatalf("Create model = %q, want empty (inherit the default)", created[0].Model)
	}
}

func TestTaskCardShowsModel(t *testing.T) {
	service := newFakeAutomation(
		contracts.AutomationTask{ID: "task-1", Name: "带模型", Prompt: "p", Schedule: "every 60s", Model: "gpt-x"},
		contracts.AutomationTask{ID: "task-2", Name: "默认模型", Prompt: "p", Schedule: "every 60s"},
	)
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "gpt-x", "模型：gpt-x")
	modelBadges := 0
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "模型：") {
			modelBadges++
		}
	}
	if modelBadges != 1 {
		t.Fatalf("model badges = %d, want only the task with a model; texts = %v", modelBadges, tester.Texts())
	}
}

func TestAutomationEnglishModelField(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "Nightly check", Prompt: "p", Schedule: "every 60s", Model: "gpt-en",
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Model: gpt-en")
	h.click(tester, "New task")
	h.assertTexts(tester, "Task name", "Task prompt", "Schedule", "Model", "Save",
		"Use the default model")
}
