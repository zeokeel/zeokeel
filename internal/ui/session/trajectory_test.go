package session_test

// Frame tests for the trajectory pane: the tool-call timeline follows the
// streaming end, its count line sits on the transcript's reading measure, and a
// row jumps back to the conversation at the message that made the call.

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// trajectoryJumpChat is a long conversation whose second message made a tool
// call: the early message starts outside the initial viewport, so a jump to its
// row is observable.
func trajectoryJumpChat() *fakeChat {
	chat := newFakeChat()
	messages := []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始检查"},
		{
			ID: "a-early", Role: contracts.ChatRoleAssistant, Text: "早期回复正文",
			Tools: []contracts.ToolCall{{
				ID: "t-early", Name: "read_file", Status: "ok", Summary: "读取了配置文件",
			}},
		},
	}
	for index := 0; index < 24; index++ {
		messages = append(messages, contracts.ChatMessage{
			ID: fmt.Sprintf("u%02d", index), Role: contracts.ChatRoleUser,
			Text: fmt.Sprintf("后续问题 %02d", index),
		})
	}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "轨迹会话", Messages: messages}, true)
	return chat
}

// TestTrajectoryCountAlignedWithTranscriptMeasure covers the reading-column
// alignment: the "N tool calls" line starts at the same x as the transcript
// prose (and the cards it counts).
func TestTrajectoryCountAlignedWithTranscriptMeasure(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "轨迹对齐",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "检查完成",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "read_file", Status: "ok", Summary: "读取配置"},
					{ID: "t2", Name: "bash", Status: "ok", Summary: "go test ./..."},
				}},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	reply, ok := tester.Find("检查完成")
	if !ok {
		t.Fatalf("reply text missing; texts = %v", tester.Texts())
	}

	h.click(tester, "轨迹")
	count, ok := tester.Find("2 个工具调用")
	if !ok {
		t.Fatalf("trajectory count line missing; texts = %v", tester.Texts())
	}
	if delta := math.Abs(float64(count.X - reply.X)); delta > 1 {
		t.Fatalf("count line starts at x=%.1f, transcript prose at x=%.1f (%.1f DIP off the reading measure)",
			count.X, reply.X, delta)
	}
}

// TestTrajectoryRowJumpsToTheMessage covers the row action: it switches back to
// the conversation and scrolls the message that made the call into view.
func TestTrajectoryRowJumpsToTheMessage(t *testing.T) {
	chat := trajectoryJumpChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	// The early message is off-screen at the followed end (and its row is not
	// rendered by the virtualized List either).
	h.assertNoTexts(tester, "早期回复正文")

	h.click(tester, "轨迹")
	if err := tester.Click("在对话中定位 读取文件"); err != nil {
		t.Fatalf("trajectory row has no locate action: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()

	// Back on the conversation tab, scrolled to the calling message.
	h.assertTexts(tester, "早期回复正文")
	h.assertNoTexts(tester, "工具调用轨迹")
}

// TestTrajectoryFollowsNewStreamingCalls covers the streaming follow: a tool
// call appended while the turn runs scrolls into view at the timeline's end.
func TestTrajectoryFollowsNewStreamingCalls(t *testing.T) {
	chat := newFakeChat()
	tools := make([]contracts.ToolCall, 0, 17)
	for index := 0; index < 16; index++ {
		tools = append(tools, contracts.ToolCall{
			ID: fmt.Sprintf("t%02d", index), Name: "read_file", Status: "ok",
			Summary: fmt.Sprintf("文件 %02d", index),
		})
	}
	session := contracts.ChatSession{
		ID: "sess-1", Title: "跟随会话", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在检查…", Streaming: true, Tools: tools},
		},
	}
	chat.addSession(session, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "轨迹")
	tester.Frame()
	// The timeline opens at its end: the first calls are above the viewport.
	h.assertNoTexts(tester, "写入发布清单")

	// A new call arrives while the turn runs.
	session.Messages[1].Tools = append(session.Messages[1].Tools, contracts.ToolCall{
		ID: "t-new", Name: "write_file", Status: "running", Summary: "写入发布清单",
	})
	chat.updateSession(session)
	waitHasText(t, tester, "写入发布清单", "the new streaming tool call to follow")
}

// waitHasText frames until the text shows (the update settles through the
// runtime's posted invalidation).
func waitHasText(t *testing.T, tester *ui.Tester, text, what string) {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		if tester.HasText(text) {
			return
		}
		tester.Frame()
	}
	t.Fatalf("timed out waiting for %s (%q); texts = %v", what, text, tester.Texts())
}

// TestTrajectoryEnterJumpsFromTheKeyboard covers the keyboard path: the list
// takes the focus, Down chooses a row (without leaving the tab) and Enter jumps
// back to the conversation at that message.
func TestTrajectoryEnterJumpsFromTheKeyboard(t *testing.T) {
	chat := trajectoryJumpChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "轨迹")
	h.assertNoTexts(tester, "早期回复正文")

	// Tab from the tab strip moves the keyboard into the timeline's List.
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if !tester.Focused("工具调用轨迹") {
		t.Fatalf("Tab did not reach the trajectory list; texts = %v", tester.Texts())
	}
	// Down only moves the choice: the tab stays.
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	h.assertTexts(tester, "工具调用轨迹")
	h.assertNoTexts(tester, "早期回复正文")
	// Enter submits the chosen row and jumps to its message.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "早期回复正文")
	h.assertNoTexts(tester, "工具调用轨迹")
}
