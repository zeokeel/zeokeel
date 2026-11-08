package automation_test

// Tests for the automation task workspace/permission selectors and badges.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

// fakeWorkspace is a minimal contracts.WorkspaceService for the selector.
type fakeWorkspace struct {
	workspaces []contracts.Workspace
	current    contracts.Workspace
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return append([]contracts.Workspace(nil), f.workspaces...), nil
}
func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return f.current, f.current.ID != "", nil
}
func (f *fakeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("not implemented")
}
func (f *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("not implemented")
}
func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }
func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }
func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func workspaceAutomationHarness(t *testing.T, service contracts.AutomationService) (*harness, *fakeWorkspace) {
	t.Helper()
	h := newHarness(t, service, automation.Config{})
	workspace := &fakeWorkspace{
		current: contracts.Workspace{ID: "w1", Name: "项目一"},
		workspaces: []contracts.Workspace{
			{ID: "w1", Name: "项目一"},
			{ID: "w2", Name: "项目二"},
		},
	}
	provide[contracts.WorkspaceService](t, h.ctx, contracts.KeyWorkspace, workspace)
	return h, workspace
}

func TestCreateFormWorkspaceAndPermissionSelectors(t *testing.T) {
	service := newFakeAutomation()
	h, _ := workspaceAutomationHarness(t, service)
	tester := h.tester()
	h.click(tester, "新建任务")
	h.assertTexts(tester, "工作区：当前工作区", "权限：默认全局")

	h.click(tester, "工作区：当前工作区")
	h.assertTexts(tester, "当前工作区", "项目一", "项目二")
	h.click(tester, "项目一")
	h.assertTexts(tester, "工作区：项目一")

	h.click(tester, "权限：默认全局")
	h.assertTexts(tester, "询问", "工作区", "全部", "从不")
	h.click(tester, "全部")
	h.assertTexts(tester, "权限：全部")

	h.click(tester, "任务名称")
	tester.Type("巡检")
	h.click(tester, "任务提示词")
	tester.Type("检查依赖")
	h.click(tester, "运行计划")
	tester.Type("every 60s")
	tester.Frame()
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].WorkspaceID != "w1" || created[0].Permission != "full" {
		t.Fatalf("Create args = %+v", created)
	}
}

func TestCreateFormEmptySelectorsStayEmpty(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")
	h.click(tester, "任务名称")
	tester.Type("任务")
	h.click(tester, "任务提示词")
	tester.Type("提示")
	h.click(tester, "运行计划")
	tester.Type("every 60s")
	tester.Frame()
	h.click(tester, "保存")
	created := service.created()
	if len(created) != 1 || created[0].WorkspaceID != "" || created[0].Permission != "" {
		t.Fatalf("empty selectors wrote fields: %+v", created)
	}
}

func TestTaskCardWorkspacePermissionBadges(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "带字段", Prompt: "p", Schedule: "every 60s", Enabled: true,
		WorkspaceID: "w1", Permission: "never",
	})
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.assertTexts(tester, "工作区：w1", "权限：从不")
}

func TestAutomationFieldsEnglish(t *testing.T) {
	service := newFakeAutomation(contracts.AutomationTask{
		ID: "task-1", Name: "task", Prompt: "p", Schedule: "every 60s",
		WorkspaceID: "w1", Permission: "full",
	})
	h := newHarness(t, service, automation.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Workspace: w1", "Permission: Full access")
	h.click(tester, "New task")
	h.assertTexts(tester, "Workspace: Current workspace", "Permission: Global default")
	h.click(tester, "Permission: Global default")
	h.assertTexts(tester, "Ask", "Workspace", "Full access", "Never")
}
