package session_test

// TDD frame tests for the persistent team workbench (P1-2 semi-loop): the team
// overlay's 打开工作台 entry opens a native modal with the members and the
// conversation's shared task board, keeping the overlay's own state; members
// carry status/model/activity/open/stop (running), tasks carry subject/status/
// owner/blockers/write scopes; Escape closes the workbench and hands the focus
// back; a product without a team backend shows no entry at all.

import (
	"context"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// workbenchTeam is a contracts.TeamService with a scripted member list and
// recorded stop calls (the live rows are the members it returns).
type workbenchTeam struct {
	fakeTeam
	mu          sync.Mutex
	interrupted []string
}

func (f *workbenchTeam) Interrupt(memberID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupted = append(f.interrupted, memberID)
	return nil
}

func (f *workbenchTeam) stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.interrupted...)
}

// workbenchBoard is a contracts.TaskBoard with a scripted task list that counts
// its reads: the workbench must reuse the overlay's cached snapshot instead of
// re-reading the board.
type workbenchBoard struct {
	mu    sync.Mutex
	tasks []contracts.BoardTask
	lists int
}

func (b *workbenchBoard) List(context.Context) ([]contracts.BoardTask, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lists++
	return append([]contracts.BoardTask(nil), b.tasks...), nil
}

func (b *workbenchBoard) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (b *workbenchBoard) SetStatus(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (b *workbenchBoard) Delete(context.Context, string) error { return nil }

func (b *workbenchBoard) reads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lists
}

// workbenchMembers is the scripted team: one running teammate with a model and
// an activity line, one idle teammate.
func workbenchMembers() []contracts.TeamMember {
	return []contracts.TeamMember{
		{
			ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning,
			Model: "gpt-5-codex",
			Activity: []contracts.TeamActivity{{
				ID: "a1", MemberID: "member-1",
				Kind: contracts.TeamActivityFinish, Text: "核对 docs 完成",
			}},
		},
		{ID: "member-2", Name: "测试执行员", RunState: contracts.TeamRunIdle, Model: "deepseek-flash"},
	}
}

// workbenchTasks is the scripted shared board: one owned in-progress task with a
// dependency and write scopes, one unassigned pending task.
func workbenchTasks() []contracts.BoardTask {
	return []contracts.BoardTask{
		{
			ID: "task-1", Subject: "检查 docs 与 README",
			Status: contracts.TaskStatusInProgress, Owner: "member-1",
			BlockedBy: []string{"task-0"}, WriteScopes: []string{"docs/"},
		},
		{
			ID: "task-2", Subject: "运行完整测试并汇报",
			Status: contracts.TaskStatusPending, Owner: contracts.TaskOwnerUnassigned,
		},
	}
}

// newWorkbenchHarness activates the session screen with a team backend, a task
// board and the child-agent read model the member rows need.
func newWorkbenchHarness(t *testing.T, team contracts.TeamService,
	board contracts.TaskBoard) *harness {
	t.Helper()
	return newAgentSurfaceHarness(t, headerChat("工作台会话"), agentServices{
		team:  team,
		board: board,
		children: &fakeChildAgents{agents: []contracts.ChildAgent{
			{ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate},
			{ID: "member-2", Name: "测试执行员", Kind: contracts.ChildAgentTeammate},
		}},
	})
}

// openTeamOverlay clicks the header's team entry and waits for the popover.
func openTeamOverlay(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "智能体团队")
	h.assertTexts(tester, "成员", "共享任务")
}

// openTeamWorkbench opens the overlay's 打开工作台 entry.
func openTeamWorkbench(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	openTeamOverlay(t, h, tester)
	h.assertTexts(tester, "打开工作台")
	h.click(tester, "打开工作台")
	tester.Frame()
	h.assertTexts(tester, "团队工作台")
}

// clickWorkbenchButton clicks a button the workbench owns (the team popover
// closes while the modal shows, so its entries are the only ones in the tree).
func clickWorkbenchButton(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleButton, name)
	if !ok {
		t.Fatalf("the workbench has no button %q; texts = %v", name, tester.Texts())
	}
	tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tester.Frame()
}

// TestTeamWorkbenchOpensWithMembersAndTasks covers the open path and the two
// sections: the header counts, every member's state/model/activity/open action
// and the shared board's subjects/status/owner/blockers/write scopes.
func TestTeamWorkbenchOpensWithMembersAndTasks(t *testing.T) {
	team := &workbenchTeam{fakeTeam: fakeTeam{members: workbenchMembers()}}
	board := &workbenchBoard{tasks: workbenchTasks()}
	h := newWorkbenchHarness(t, team, board)
	tester := h.tester()
	tester.Frame()

	openTeamOverlay(t, h, tester)
	if _, ok := tester.Find("打开工作台"); !ok {
		t.Fatalf("the team overlay has no workbench entry; texts = %v", tester.Texts())
	}
	h.click(tester, "打开工作台")
	tester.Frame()

	h.assertTexts(tester,
		"团队工作台", "2 名成员 · 1 个运行中",
		// Members: status, model and latest activity.
		"文档审查员", "运行中", "gpt-5-codex", "核对 docs 完成",
		"测试执行员", "空闲", "deepseek-flash",
		// The shared board, with its count and every card field.
		"共享任务 · 2", "检查 docs 与 README", "进行中",
		"负责人 文档审查员", "依赖 task-0", "写入 docs/",
		"运行完整测试并汇报", "待处理", "负责人 主智能体",
	)
	// The running member has its stop action; every member can be opened.
	if _, ok := tester.AccessByRole(ui.RoleButton, "停止成员 文档审查员"); !ok {
		t.Fatalf("the workbench's running member has no stop action; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "打开成员 文档审查员"); !ok {
		t.Fatalf("the workbench has no open action; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "关闭团队工作台"); !ok {
		t.Fatalf("the workbench has no close action; texts = %v", tester.Texts())
	}
}

// TestTeamWorkbenchStopsRunningMember covers the running control: stopping from
// the workbench calls TeamService.Interrupt for that member.
func TestTeamWorkbenchStopsRunningMember(t *testing.T) {
	team := &workbenchTeam{fakeTeam: fakeTeam{members: workbenchMembers()}}
	board := &workbenchBoard{tasks: workbenchTasks()}
	h := newWorkbenchHarness(t, team, board)
	tester := h.tester()
	tester.Frame()

	openTeamWorkbench(t, h, tester)
	clickWorkbenchButton(t, tester, "停止成员 文档审查员")

	waitFor(t, tester, func() bool { return len(team.stopped()) == 1 }, "TeamService.Interrupt call")
	if got := team.stopped()[0]; got != "member-1" {
		t.Fatalf("Interrupt(%q), want member-1", got)
	}
}

// TestTeamWorkbenchEscapeClosesAndKeepsOverlayState covers the close contract:
// Escape closes the modal, returns the focus to the entry that opened it, and
// leaves the team overlay (and its cached task snapshot) untouched.
func TestTeamWorkbenchEscapeClosesAndKeepsOverlayState(t *testing.T) {
	team := &workbenchTeam{fakeTeam: fakeTeam{members: workbenchMembers()}}
	board := &workbenchBoard{tasks: workbenchTasks()}
	h := newWorkbenchHarness(t, team, board)
	tester := h.tester()
	tester.Frame()

	openTeamWorkbench(t, h, tester)
	readsAfterOpen := board.reads()

	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	h.assertNoTexts(tester, "团队工作台")
	// The overlay is still open with its members and its cached task snapshot:
	// closing the workbench did not reset it or re-read the board.
	h.assertTexts(tester, "成员", "共享任务", "文档审查员", "检查 docs 与 README", "打开工作台")
	if got := board.reads(); got != readsAfterOpen {
		t.Fatalf("closing the workbench re-read the task board (%d reads, want %d)", got, readsAfterOpen)
	}
	// The focus comes back to the entry that opened the workbench.
	if !tester.Focused("打开工作台") && !tester.Focused("智能体团队") {
		t.Fatalf("the closed workbench did not return the focus; texts = %v", tester.Texts())
	}

	// The close button takes the same path back to the overlay.
	h.click(tester, "打开工作台")
	tester.Frame()
	h.assertTexts(tester, "团队工作台")
	clickWorkbenchButton(t, tester, "关闭团队工作台")
	h.assertNoTexts(tester, "团队工作台")
	h.assertTexts(tester, "成员", "共享任务", "文档审查员", "打开工作台")
	if got := board.reads(); got != readsAfterOpen {
		t.Fatalf("closing the workbench with its button re-read the task board (%d reads, want %d)",
			got, readsAfterOpen)
	}
}

// TestTeamWorkbenchEntryHiddenWithoutBackend covers the boundary: a product
// without a team backend shows neither the team entry nor a workbench entry.
func TestTeamWorkbenchEntryHiddenWithoutBackend(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("无团队后端"), agentServices{})
	tester := h.tester()
	tester.Frame()

	h.assertNoTexts(tester, "智能体团队", "打开工作台", "团队工作台")
}
