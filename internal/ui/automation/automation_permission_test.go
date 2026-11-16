package automation_test

// Frame test for US-05 (2026-11 wave D, automation side): the task form's
// permission field discloses the two-layer model — an automation-level
// permission override is APPROVAL-ONLY (仅审批); the OS file sandbox always
// follows the global permission, so "所有权限" here does not lift the OS wall.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/ui/automation"
)

// TestAutomationPermissionApprovalOnlyDisclosure covers the permission field's
// visible two-layer disclosure.
func TestAutomationPermissionApprovalOnlyDisclosure(t *testing.T) {
	service := newFakeAutomation()
	h := newHarness(t, service, automation.Config{})
	tester := h.tester()
	h.click(tester, "新建任务")

	h.assertTexts(tester, "权限", "仅审批", "文件沙箱", "跟随全局")
}
