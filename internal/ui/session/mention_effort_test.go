package session_test

// TDD tests (RED first) for the composer's @session reference group and the
// reasoning-effort selector chip.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// exactTextCount counts exactly equal text nodes (Tester.HasText is substring).
func exactTextCount(texts []string, want string) int {
	count := 0
	for _, text := range texts {
		if text == want {
			count++
		}
	}
	return count
}

// sessionRefChat is a fake chat with one active session and two others.
func sessionRefChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "当前会话"}, true)
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "研究笔记"}, false)
	chat.addSession(contracts.ChatSession{ID: "sess-3", Title: "发布计划"}, false)
	return chat
}

// --- @session mention group -------------------------------------------------

func TestMentionSessionGroupListsOtherSessions(t *testing.T) {
	h := newWorkspaceHarness(t, sessionRefChat(), nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()

	h.assertTexts(tester, "会话", "研究笔记", "发布计划", "sess-2", "sess-3")
	// The active session is not offered as a reference.
	if got := exactTextCount(tester.Texts(), "sess-1"); got != 0 {
		t.Fatalf("active session id appears %d times in the mention menu; texts = %v", got, tester.Texts())
	}
}

func TestMentionSessionGroupFiltersByQuery(t *testing.T) {
	h := newWorkspaceHarness(t, sessionRefChat(), nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@研究")
	tester.Frame()

	h.assertTexts(tester, "研究笔记", "sess-2")
	h.assertNoTexts(tester, "发布计划")
}

func TestMentionSessionGroupHiddenWithoutOtherSessions(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "唯一会话"}, true)
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()

	if got := exactTextCount(tester.Texts(), "会话"); got != 0 {
		t.Fatalf("empty session group is still rendered (%d headers); texts = %v", got, tester.Texts())
	}
}

func TestMentionSessionReferenceIsInsertedAndSent(t *testing.T) {
	chat := sessionRefChat()
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@研究")
	tester.Frame()

	h.click(tester, "研究笔记")
	h.assertNoTexts(tester, "工作区文件", "发布计划")
	h.click(tester, "发送")
	tester.Frame()

	sends := chat.sends()
	want := contracts.FormatSessionReference("sess-2")
	if len(sends) != 1 || !strings.Contains(sends[0].prompt, want) {
		t.Fatalf("Send calls = %+v, want a prompt containing %q", sends, want)
	}
}

// --- reasoning effort -------------------------------------------------------

func TestReasoningEffortChipDefaultsAndWrites(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.assertTexts(tester, "推理：默认")

	h.click(tester, "推理：默认")
	h.assertTexts(tester, "推理强度", "默认", "低", "中", "高")
	h.click(tester, "高")

	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != contracts.SessionEffortHigh {
		t.Fatalf("stored effort = %q, want high", got)
	}
	h.assertTexts(tester, "推理：高")
}

func TestReasoningEffortReflectsStoredValue(t *testing.T) {
	settings := newPermissionSettings(map[string]string{
		contracts.SessionEffortSettingKey("sess-1"): contracts.SessionEffortMedium,
	})
	h := newPermissionHarness(t, permissionChat(), settings)
	h.assertTexts(h.tester(), "推理：中")
}

func TestReasoningEffortHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	if got := exactTextCount(tester.Texts(), "推理：默认"); got != 0 {
		t.Fatalf("reasoning chip rendered without a settings store; texts = %v", tester.Texts())
	}
	// No settings → no chip to open.
	if err := tester.Click("推理：默认"); err == nil {
		t.Fatal("clicking a non-existent reasoning chip succeeded")
	}
}

func TestReasoningEffortEnglishCopy(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Reasoning: Default")

	h.click(tester, "Reasoning: Default")
	h.assertTexts(tester, "Reasoning effort", "Default", "Low", "Medium", "High")
	h.click(tester, "High")

	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != contracts.SessionEffortHigh {
		t.Fatalf("stored effort = %q, want high", got)
	}
	h.assertTexts(tester, "Reasoning: High")
}
