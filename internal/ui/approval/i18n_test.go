package approval_test

// i18n frame tests for the inline approval card: the default labels follow the
// host locale on every frame while assembly-configured custom labels stay
// verbatim.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/ui/approval"
)

func TestApprovalEnglishCopyAndLanguageSwitch(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	h.frames(tester, 2)

	// Default Chinese copy.
	h.assertTexts(tester, "需要审批", "原始参数", "允许一次", "拒绝", "更多",
		"运行命令", "拒绝本次", "仅此一次")

	h.uiRoot.Locale().SetLanguage("en")
	h.frames(tester, 1)
	h.assertTexts(tester, "Approval required", "Raw arguments",
		"Allow once", "Deny", "More", "Run command", "denies this call", "One call")
	h.assertNoTexts(tester, "需要审批", "拒绝")

	// Switching back restores the Chinese copy on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	h.frames(tester, 1)
	h.assertTexts(tester, "需要审批", "原始参数", "允许一次", "拒绝")
	h.assertNoTexts(tester, "Approval required", "Allow once")
}

func TestApprovalCustomLabelsStayVerbatim(t *testing.T) {
	h := newHarness(t, approval.Config{
		Title:      "确认执行",
		AllowLabel: "执行",
	})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	h.frames(tester, 2)

	h.uiRoot.Locale().SetLanguage("en")
	h.frames(tester, 1)
	// Custom labels are kept; untouched defaults become English.
	h.assertTexts(tester, "确认执行", "执行", "Raw arguments", "Deny", "More")
}

// TestApprovalEnglishToolNamesAndPendingCount covers the localized tool names
// and the banner's pending counter.
func TestApprovalEnglishToolNamesAndPendingCount(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "read_file", "读取配置", `{"path":"x"}`))
	h.approvals.add(pendingRequest("a2", "bash", "运行命令 ls", `{"command":"ls"}`))
	h.frames(tester, 2)

	h.uiRoot.Locale().SetLanguage("en")
	h.frames(tester, 1)
	h.assertTexts(tester, "Approval required", "Read file", "1 more", "One call")
	// The raw protocol id is visible mono copy beside the friendly name.
	h.assertTexts(tester, "read_file")
}
