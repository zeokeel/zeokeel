package session_test

// US-03 + UX-19 (2026-11 audit): the standard-mode live work-steps row tells the
// truth about the current phase — a thinking state while reasoning streams
// (instead of a finished tool's progressive word), the right tense once a tool
// finished — and the running turn is a RoleStatus live region. A settling reply
// and an arriving approval announce themselves (S1/S2/S7/S8 a11y).

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// liveActivityChat builds a running standard-mode conversation whose streaming
// assistant message carries the given reasoning segments, tools and prose.
func liveActivityChat(segments []contracts.ReasoningSegment, tools []contracts.ToolCall, text string) *fakeChat {
	chat := newFakeChat()
	flat := ""
	for _, segment := range segments {
		flat += segment.Text
	}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "活动行", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题", CreatedAt: time.Unix(1700000000, 0)},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: text,
				Reasoning: flat, ReasoningSegments: segments,
				Tools: tools, ToolTimelineKnown: len(tools) > 0,
				Streaming: true, CreatedAt: time.Unix(1700000001, 0),
			},
		},
	}, true)
	return chat
}

// TestStandardLiveRowThinkingWhileReasoningStreams covers US-03 window 1: no
// tool yet, reasoning streaming — the row says 正在思考, not 正在分析请求.
func TestStandardLiveRowThinkingWhileReasoningStreams(t *testing.T) {
	chat := liveActivityChat(
		[]contracts.ReasoningSegment{{Text: "先看认证模块", Iteration: 1, TextOffset: 0}},
		nil, "")
	h := newHarness(t, chat) // standard mode default
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "正在思考")
	h.assertNoTexts(tester, "正在分析请求")
}

// TestStandardLiveRowThinkingAfterFinishedTool covers US-03 window 2: the tool
// finished and iteration 2 is thinking — the row must not keep the finished
// tool's progressive word.
func TestStandardLiveRowThinkingAfterFinishedTool(t *testing.T) {
	chat := liveActivityChat(
		[]contracts.ReasoningSegment{
			{Text: "第一轮思考", Iteration: 1, TextOffset: 0},
			{Text: "第二轮思考", Iteration: 2, TextOffset: 0},
		},
		[]contracts.ToolCall{{ID: "t1", Name: "read_file", Status: "ok", Summary: "auth.go", TextOffset: 0}},
		"")
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "正在思考")
	h.assertNoTexts(tester, "正在读取文件")
}

// TestStandardLiveRowRunningToolKeepsProgressive is the control: a tool still
// running keeps its progressive activity word.
func TestStandardLiveRowRunningToolKeepsProgressive(t *testing.T) {
	chat := liveActivityChat(
		[]contracts.ReasoningSegment{{Text: "先读文件", Iteration: 1, TextOffset: 0}},
		[]contracts.ToolCall{{ID: "t1", Name: "read_file", Status: "running", Summary: "auth.go", TextOffset: 0}},
		"")
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "正在读取文件")
}

// TestStandardLiveRowWritingWhenProseStreams covers the tense after the last
// thinking block: prose arrived after it, so the row says the reply is being
// written instead of claiming the model still thinks.
func TestStandardLiveRowWritingWhenProseStreams(t *testing.T) {
	chat := liveActivityChat(
		[]contracts.ReasoningSegment{{Text: "想好了", Iteration: 1, TextOffset: 0}},
		nil, "正在写出的回答")
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "正在回复")
	h.assertNoTexts(tester, "正在思考")
}

// TestStandardLiveRowIsRoleStatus covers UX-19's default-mode live region: the
// activity row is reachable as RoleStatus with the activity word as its name.
func TestStandardLiveRowIsRoleStatus(t *testing.T) {
	chat := liveActivityChat(nil, nil, "")
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	if _, ok := tester.AccessByRole(ui.RoleStatus, "正在分析请求"); !ok {
		t.Fatalf("standard live row is no RoleStatus live region; texts = %v", tester.Texts())
	}
}

// TestSettleAnnouncesCompletion covers UX-19's settle announcement: the
// running→idle transition announces the finished reply.
func TestSettleAnnouncesCompletion(t *testing.T) {
	chat := liveActivityChat(nil, nil, "回答正文")
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	tester.Frame()

	session, _ := chat.Session("sess-1")
	session.Running = false
	session.Messages[1].Streaming = false
	chat.updateSession(session)
	tester.Frame()
	tester.Frame()

	announced := false
	for _, announcement := range tester.Announcements() {
		if strings.Contains(announcement, "完成") || strings.Contains(announcement, "回答") {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("settle did not announce the finished reply; announcements = %v", tester.Announcements())
	}
}

// TestApprovalArrivalAnnounces covers UX-19's approval arrival: a new pending
// request announces itself while the conversation is on screen.
func TestApprovalArrivalAnnounces(t *testing.T) {
	chat := liveActivityChat(nil, nil, "")
	approvals := &fakeApprovals{}
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	tester.Frame()
	tester.Frame()

	approvals.setPending(contracts.ApprovalRequest{
		ID: "ap-arrive", Tool: "bash", SessionID: "sess-1", Summary: "rm -rf build",
	})
	tester.Frame()
	tester.Frame()

	announced := false
	for _, announcement := range tester.Announcements() {
		if strings.Contains(announcement, "审批") {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("approval arrival did not announce; announcements = %v", tester.Announcements())
	}
}
