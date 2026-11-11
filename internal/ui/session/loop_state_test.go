package session_test

// Wave 2 loop-state mappings: the message-level states introduced with the
// agent loop hardening must be visible and localized in the conversation.
//
//   - ChatMessage.Truncated: the provider cut the answer at the output-token
//     limit; the last assistant reply says so (foldable explanation).
//   - MessageErrorKindIterations: the turn hit chat.maxIterations; the localized
//     heading points at the limit instead of dumping rig's English diagnostic.
//   - ErrChatCompacting / ErrCompactConflict: a send (or /compact) while the
//     history is being compacted is explained in Chinese.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// loopStateMessage builds an assistant message with the new state fields.
func loopStateMessage(id string, tools []contracts.ToolCall) contracts.ChatMessage {
	return contracts.ChatMessage{
		ID: id, Role: contracts.ChatRoleAssistant, Text: "回答内容",
		Tools: tools, CreatedAt: time.Unix(1700000200, 0),
	}
}

// TestTruncatedAnswerHintOnLastReply covers the truncated-answer hint: only the
// transcript's final assistant reply says the answer was cut, and the hint
// expands to explain what happened.
func TestTruncatedAnswerHintOnLastReply(t *testing.T) {
	chat := newFakeChat()
	last := loopStateMessage("m2", nil)
	last.Truncated = true
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "截断回答",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "写一篇长文"),
			last,
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	if tester.HasText("达到输出上限") {
		t.Fatalf("the truncated explanation starts expanded; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "回答被截断")
	h.click(tester, "回答被截断")
	tester.Advance(300 * time.Millisecond)
	h.assertTexts(tester, "模型在输出上限处停止", "提高最大输出 tokens")
}

// TestTruncatedHintOnlyOnLastReply pins the placement: an older truncated reply
// does not add the hint (the final answer is the actionable one).
func TestTruncatedHintOnlyOnLastReply(t *testing.T) {
	chat := newFakeChat()
	first := loopStateMessage("m2", nil)
	first.Truncated = true
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "旧截断",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "第一问"),
			first,
			message("m3", contracts.ChatRoleUser, "第二问"),
			loopStateMessage("m4", nil),
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertNoTexts(tester, "回答被截断")
}

// TestIterationsErrorIsLocalized covers MessageErrorKindIterations: the failed
// turn explains the iteration budget with a way forward, and rig's English
// diagnostic is not the visible headline.
func TestIterationsErrorIsLocalized(t *testing.T) {
	chat := newFakeChat()
	failed := loopStateMessage("m2", nil)
	failed.Text = ""
	failed.Error = "agent: max iterations reached without a final answer"
	failed.ErrorKind = contracts.MessageErrorKindIterations
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "迭代上限",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "跑一个很长的任务"),
			failed,
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	h.assertTexts(tester, "已达最大迭代次数", "提高最大迭代次数", "打开模型设置")
	h.assertNoTexts(tester, "发送失败：agent: max iterations")
	h.clickExact(tester, "打开模型设置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("the iterations suggestion did not open the model settings")
	}
}

// TestSendWhileCompactingIsLocalized covers ErrChatCompacting on the send path:
// the composer explains that the history is being compacted instead of showing
// the raw Go error.
func TestSendWhileCompactingIsLocalized(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "压缩中发送",
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "你好")},
	}, true)
	chat.sendErr = contracts.ErrChatCompacting
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposer(t, tester)
	tester.Type("继续")
	h.click(tester, "发送")
	tester.Frame()

	h.assertTexts(tester, "会话正在压缩，请稍后再试", "being compacted")
	h.assertNoTexts(tester, "发送失败：session context is being compacted")
}

// TestSendDuringCompactConflictIsLocalized covers ErrCompactConflict on the
// send path: the history changed under the compaction, so the send was
// cancelled and the user is told so.
func TestSendDuringCompactConflictIsLocalized(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "压缩冲突",
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "你好")},
	}, true)
	chat.sendErr = contracts.ErrCompactConflict
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposer(t, tester)
	tester.Type("继续")
	h.click(tester, "发送")
	tester.Frame()

	h.assertTexts(tester, "压缩期间历史被修改，已取消", "history changed during compaction")
	h.assertNoTexts(tester, "发送失败：session history changed during compaction")
}

// TestCompactCommandConflictAndCompactingMessages covers /compact's own error
// copy for the two new stable errors.
func TestCompactCommandConflictAndCompactingMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"conflict", contracts.ErrCompactConflict, "压缩期间历史被修改，已取消"},
		{"compacting", contracts.ErrChatCompacting, "会话正在压缩，请稍后再试"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chat := compactSessionChat()
			chat.compactErr = test.err
			h := newModelHarness(t, chat, nil)
			tester := h.tester()
			if err := findCompactCommand(t, h).Run(); err != nil {
				t.Fatalf("compact Run: %v", err)
			}
			waitCompactNotice(t, h, tester)
			h.assertTexts(tester, test.want)
		})
	}
}
