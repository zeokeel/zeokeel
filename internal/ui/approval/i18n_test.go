package approval_test

// i18n frame tests for the approval dialog: the default labels follow the host
// locale on every frame while assembly-configured custom labels stay verbatim.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/ui/approval"
)

func TestApprovalEnglishCopyAndLanguageSwitch(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	tester.Frame()

	// Default Chinese copy is unchanged.
	h.assertTexts(tester, "需要审批", "工具", "参数", "允许", "本会话内始终允许", "拒绝")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Approval required", "Tool", "Arguments",
		"Allow", "Always allow in this session", "Deny")
	h.assertNoTexts(tester, "需要审批", "本次运行内始终允许", "拒绝")

	// Switching back restores the Chinese copy on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "需要审批", "工具", "参数", "允许", "本会话内始终允许", "拒绝")
	h.assertNoTexts(tester, "Approval required", "Always allow in this session")
}

func TestApprovalCustomLabelsStayVerbatim(t *testing.T) {
	h := newHarness(t, approval.Config{
		Title:      "确认执行",
		AllowLabel: "执行",
	})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "bash", "运行命令 ls", `{"command":"ls"}`))
	tester.Frame()

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	// Custom labels are kept; untouched defaults become English.
	h.assertTexts(tester, "确认执行", "执行", "Tool", "Arguments",
		"Always allow in this session", "Deny")
}

// TestApprovalEnglishToolNamesAndPendingCount covers the localized tool names
// and the quiet queued-request counter.
func TestApprovalEnglishToolNamesAndPendingCount(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()
	h.approvals.add(pendingRequest("a1", "read_file", "读取配置", `{"path":"x"}`))
	h.approvals.add(pendingRequest("a2", "bash", "运行命令 ls", `{"command":"ls"}`))
	tester.Frame()

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Approval required", "Read file", "1 more approval requests waiting",
		"Always allow in this session")
	h.assertNoTexts(tester, "read_file")
}
