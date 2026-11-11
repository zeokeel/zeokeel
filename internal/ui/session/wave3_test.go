package session_test

// Wave 3 UI copy/scope items:
//   - memory rows bound to a workspace show a human-readable scope label, never
//     the raw "workspace:<id>" storage value;
//   - the model chip names the assembly's default model when no model row is
//     configured (FR-26), with the explanation in the chip menu;
//   - the session menu marks permission/conversation mode as conversation-wide
//     settings (PM-14).

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestMemoryBoundWorkspaceScopeLabel covers the scope label of a memory bound
// to the workspace identity ("workspace:<id>"): the row says it is the current
// project's workspace scope instead of printing the storage value.
func TestMemoryBoundWorkspaceScopeLabel(t *testing.T) {
	store := newFakeMemoryStore(contracts.Memory{
		ID: "mem-1", Text: "发布前跑全量测试", Scope: "workspace:ws-a",
		Created: time.Unix(1700000000, 0),
	})
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "绑定工作区"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()

	h.click(tester, "记忆")
	waitFor(t, tester, func() bool { return tester.HasText("发布前跑全量测试") }, "memory rows")
	h.assertTexts(tester, "工作区（本会话项目）")
	h.assertNoTexts(tester, "workspace:ws-a")
}

// TestModelChipNamesAssemblyDefault covers FR-26: with no model row configured
// but an assembly-provided default model, the chip says the model is a default
// without a configured connection and the menu explains where it comes from.
func TestModelChipNamesAssemblyDefault(t *testing.T) {
	chat := modelThinkingChat("gpt-4o-mini") // fake config model, empty catalog
	h := newModelThinkingHarness(t, chat, newPermissionSettings(nil), &thinkingCatalog{})
	tester := h.tester()

	const chip = "gpt-4o-mini（默认模型，尚未配置连接）"
	h.assertTexts(tester, chip)
	h.assertNoExactTexts(tester, "未配置")
	h.clickExact(tester, chip)
	h.assertTexts(tester, "默认模型来自装配，添加模型行后可覆盖")
	tester.Key(0, ui.KeyEscape)
}

// TestSessionMenuMarksConversationWideSettings covers PM-14: the session menu's
// permission and conversation-mode groups say they apply to every conversation
// (persona / remembered tools stay conversation-scoped and unmarked).
func TestSessionMenuMarksConversationWideSettings(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "作用域"}, true)
	h := newPermissionHarness(t, chat, newPermissionSettings(nil))
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式（所有会话）", "对话模式（所有会话）")
	h.assertTexts(tester, "插入预设")
}
