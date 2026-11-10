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

	// Default Chinese copy is unchanged.
	h.assertTexts(tester, "需要审批", "原始参数", "允许", "本会话内始终允许", "拒绝",
		"运行命令", "Esc 拒绝")

	h.uiRoot.Locale().SetLanguage("en")
	h.frames(tester, 1)
	h.assertTexts(tester, "Approval required", "Raw arguments",
		"Allow", "Always allow in this session", "Deny", "Run command", "Esc denies")
	h.assertNoTexts(tester, "需要审批", "本次运行内始终允许", "拒绝")

	// Switching back restores the Chinese copy on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	h.frames(tester, 1)
	h.assertTexts(tester, "需要审批", "原始参数", "允许", "本会话内始终允许", "拒绝")
	h.assertNoTexts(tester, "Approval required", "Always allow in this session")
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
	h.assertTexts(tester, "确认执行", "执行", "Raw arguments",
		"Always allow in this session", "Deny")
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
	h.assertTexts(tester, "Approval required", "Read file", "1 more pending approvals",
		"Always allow in this session")
	h.assertNoTexts(tester, "read_file")
}
