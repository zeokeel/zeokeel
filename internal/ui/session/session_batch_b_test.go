package session_test

// Frame tests for Batch B of the conversation-core work: the tool-input/output
// visibility and the cross-navigation added to the transcript's tool rows and
// the trajectory card. (The old header team chip's tests moved to
// agentsurface_test.go with the DSH team overlay.)

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeTeam is the minimal team read model the tool-card footer and the
// agent surface tests share.
type fakeTeam struct {
	members []contracts.TeamMember
}

func (f *fakeTeam) Members() []contracts.TeamMember {
	return append([]contracts.TeamMember(nil), f.members...)
}
func (f *fakeTeam) Spawn(context.Context, string, string, string) (contracts.TeamMember, error) {
	return contracts.TeamMember{}, nil
}
func (f *fakeTeam) SendAsync(string, string) error { return nil }
func (f *fakeTeam) Send(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeTeam) Wait(context.Context, string) (string, error) { return "", nil }
func (f *fakeTeam) Interrupt(string) error                       { return nil }
func (f *fakeTeam) Close(string) error                           { return nil }

// TestFallbackToolRowShowsInputAndOutput covers S-02/S-03 for tools without a
// registered renderer (MCP-style names): the expanded row shows the pretty JSON
// arguments and the output.
func TestFallbackToolRowShowsInputAndOutput(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "MCP 工具",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "查一下"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好的",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "mcp__srv__lookup", Status: "ok",
					Arguments: `{"query":"needle","limit":5}`,
					Summary:   "found needle in index.go",
				}}},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "mcp__srv__lookup", "输入", "输出", "query", "found needle in index.go")
}

// TestToolCardsHaveNoTeamCrossNavigation covers the removal of the old
// inspector-pane cross-navigation: the team and its task board now live in the
// session header overlay, so the tool cards carry no team/task pane actions.
func TestToolCardsHaveNoTeamCrossNavigation(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "子代理",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "派个子代理"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "subagent", Status: "ok",
						Arguments: `{"prompt":"总结当前工作目录"}`,
						Summary:   "子代理已完成"},
					{ID: "t2", Name: "team_task_create", Status: "ok",
						Arguments: `{"subject":"检查文档"}`,
						Summary:   "已创建"},
				}},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "子代理已完成")
	h.assertNoTexts(tester, "在团队中查看", "打开任务板")
}

// TestTrajectoryCardShowsInputOutput covers the trajectory pane's detailed card:
// it renders the same input/output sections as the transcript row.
func TestTrajectoryCardShowsInputOutput(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "轨迹",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑测试"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "bash", Status: "ok",
					Arguments: `{"command":"go test ./..."}`,
					Summary:   "ok  package one",
				}}},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	h.click(tester, "轨迹")
	tester.Frame()
	h.assertTexts(tester, "运行命令", "命令", "go test ./...", "输出", "ok  package one")
}
