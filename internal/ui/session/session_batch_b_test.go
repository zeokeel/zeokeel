package session_test

// Frame tests for Batch B of the conversation-core work: the session header's
// team chip (S-11 team entry), and the tool-input/output visibility +
// cross-navigation added to the transcript's tool rows and the trajectory card.

import (
	"context"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeTeam is the minimal team read model the header chip needs.
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

// teamChipLabels reports whether any frame text looks like the header chip
// (distinct from a session title that merely contains the word).
func teamChipLabels(texts []string) bool {
	for _, text := range texts {
		if strings.HasPrefix(text, "团队 ") {
			return true
		}
	}
	return false
}

// newBatchBHarness is newHarness with an optional team backend.
func newBatchBHarness(t *testing.T, chat *fakeChat, team contracts.TeamService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if team != nil {
		provide[contracts.TeamService](t, ctx, contracts.KeyTeam, team)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// registerTeamCommands registers the inspector pane commands the header chip and
// the tool-card footers run, and returns the counters they increment.
func registerTeamCommands(t *testing.T, h *harness) (teamRuns, taskRuns *int) {
	t.Helper()
	teamCount, taskCount := 0, 0
	if _, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
		ID: "rightpanel.team.open", Title: "打开团队面板",
		Run: func() error { teamCount++; return nil },
	}); err != nil {
		t.Fatalf("register team command: %v", err)
	}
	if _, err := h.ui.Runtime().RegisterCommand(contracts.CommandSpec{
		ID: "rightpanel.tasks.open", Title: "打开任务板",
		Run: func() error { taskCount++; return nil },
	}); err != nil {
		t.Fatalf("register tasks command: %v", err)
	}
	return &teamCount, &taskCount
}

func TestTeamChipHiddenWithoutTeam(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无后端"}, true)
	h := newBatchBHarness(t, chat, nil)
	tester := h.tester()
	tester.Frame()
	if teamChipLabels(tester.Texts()) {
		t.Fatalf("team chip shown without a team backend; texts = %v", tester.Texts())
	}
}

func TestTeamChipHiddenWhenTeamEmpty(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "空列表"}, true)
	h := newBatchBHarness(t, chat, &fakeTeam{})
	tester := h.tester()
	tester.Frame()
	if teamChipLabels(tester.Texts()) {
		t.Fatalf("team chip shown for an empty team; texts = %v", tester.Texts())
	}
}

func TestTeamChipShowsCountAndRunningAndOpensTeam(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "团队会话"}, true)
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m1", Name: "研究员", RunState: contracts.TeamRunIdle},
		// Status "active" is availability, not a run: it must not count as 运行中.
		{ID: "m2", Name: "写手", Status: "active"},
		{ID: "m3", Name: "审查员", RunState: contracts.TeamRunRunning},
	}}
	h := newBatchBHarness(t, chat, team)
	teamRuns, taskRuns := registerTeamCommands(t, h)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "团队 3 · 1 运行中")

	h.click(tester, "团队 3 · 1 运行中")
	h.click(tester, "打开团队面板")
	if *teamRuns != 1 || *taskRuns != 0 {
		t.Fatalf("team runs=%d tasks=%d, want the team command run once", *teamRuns, *taskRuns)
	}

	// The task-board entry is the second menu item.
	h.click(tester, "团队 3 · 1 运行中")
	h.click(tester, "打开任务板")
	if *taskRuns != 1 {
		t.Fatalf("task runs=%d, want the task command run once", *taskRuns)
	}
}

func TestTeamChipNarrowKeepsCountOnly(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "窄团队"}, true)
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m1", RunState: contracts.TeamRunRunning},
		{ID: "m2", RunState: contracts.TeamRunIdle},
	}}
	h := newBatchBHarness(t, chat, team)
	tester := h.tester()
	tester.SetSize(800, 700)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "团队 2")
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "团队 2 ·") {
			t.Fatalf("narrow header still shows the running words: %q; texts = %v", text, tester.Texts())
		}
	}
}

func TestTeamChipMenuItemsDisabledWithoutCommands(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无命令"}, true)
	team := &fakeTeam{members: []contracts.TeamMember{{ID: "m1", RunState: contracts.TeamRunIdle}}}
	h := newBatchBHarness(t, chat, team)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "团队 1")
	h.assertTexts(tester, "打开团队面板", "打开任务板")
	if info, ok := tester.Access("打开团队面板"); ok && !info.Disabled {
		t.Fatalf("team menu item is enabled without the inspector command: %+v", info)
	}
	// Clicking a disabled item must not panic or close/act.
	h.click(tester, "打开团队面板")
}

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

// TestToolCardCrossNavigation covers the team cross-navigation footer on the
// session's fallback tool row: a subagent call offers the team pane action and
// runs the stable inspector command.
func TestToolCardCrossNavigation(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "子代理",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "派个子代理"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "subagent", Status: "ok",
					Arguments: `{"prompt":"总结当前工作目录"}`,
					Summary:   "子代理已完成",
				}}},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	teamRuns, _ := registerTeamCommands(t, h)
	tester := h.tester()
	h.assertTexts(tester, "在团队中查看")
	h.click(tester, "在团队中查看")
	if *teamRuns != 1 {
		t.Fatalf("team command runs = %d, want 1", *teamRuns)
	}

	// A tool unrelated to the team gets no footer action.
	plain := newFakeChat()
	plain.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "普通工具",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "读文件"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "read_file", Status: "ok",
					Arguments: `{"path":"main.go"}`, Summary: "package main",
				}}},
		},
	}, true)
	ph := newHarness(t, plain)
	registerTeamCommands(t, ph)
	if ph.tester().HasText("在团队中查看") || ph.tester().HasText("打开任务板") {
		t.Fatal("team cross-navigation shown for an unrelated tool")
	}
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
