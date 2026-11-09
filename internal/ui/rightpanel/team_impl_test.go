package rightpanel_test

// Frame tests for the team-visibility implementation: the grouped Agents pane,
// the truthful run-state badges, the run meta + activity timeline, the copy
// output action, the empty state and the native spawn form, the completion
// toast (with dedupe and its View action), the close confirmation that has no
// default action, and the stable open-pane palette commands.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// fakeNotification records one toast raised by the inspector.
type fakeNotification struct {
	level       string
	text        string
	actionLabel string
	action      func()
}

// fakeNotifier implements contracts.Notifier and contracts.ActionNotifier; it
// records the toasts so the tests can assert the completion notification and
// run its View action.
type fakeNotifier struct {
	mu     sync.Mutex
	nextID int
	items  []fakeNotification
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{} }

func (f *fakeNotifier) SetHandler(func(contracts.Notification)) {}

func (f *fakeNotifier) Notify(level, text string) contracts.Notification {
	return f.record(level, text, "", nil)
}

func (f *fakeNotifier) Dismiss(string) {}

func (f *fakeNotifier) NotifyAction(level, text, actionLabel string, action func()) contracts.Notification {
	return f.record(level, text, actionLabel, action)
}

func (f *fakeNotifier) record(level, text, actionLabel string, action func()) contracts.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.items = append(f.items, fakeNotification{level: level, text: text, actionLabel: actionLabel, action: action})
	return contracts.Notification{ID: fmt.Sprintf("toast-%d", f.nextID), Level: level, Text: text,
		ActionLabel: actionLabel, Action: action}
}

func (f *fakeNotifier) notifications() []fakeNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeNotification(nil), f.items...)
}

// seedTeamSeen baselines the completion-toast observer with the current roster:
// the first observation of a member must never toast its history.
func seedTeamSeen(h *harness, tester *ui.Tester) {
	h.chat.notify("")
	tester.Frame()
}

// TestAgentsSectionsGroupLeadTeammatesAndSubagents pins the three sections the
// pane shows: the lead context row, the teammate list and the read-only
// delegation group.
func TestAgentsSectionsGroupLeadTeammatesAndSubagents(t *testing.T) {
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{},
		newFakeTeam(), newFakeSubagents(agentsActivities()...), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")

	h.assertTexts(tester, "主代理", "ZeoKeel 主代理",
		"队友 · 2", "同步子代理 · 4（只读）", "新建队友")
	// The lead row answers "is the conversation running": it is idle here.
	h.assertTexts(tester, "空闲")
}

// TestAgentsRunStateBadgesAreTruthful pins the core semantic fix: the badge
// comes from the tracked run state, never from the manager's availability, so a
// finished member is not drawn as running.
func TestAgentsRunStateBadgesAreTruthful(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		member     contracts.TeamMember
		wantBadge  string
		wantAbsent string
	}{
		{
			name: "running",
			member: contracts.TeamMember{ID: "m", Name: "跑者", Status: "active",
				RunState: contracts.TeamRunRunning, Started: now},
			wantBadge:  "运行中",
			wantAbsent: "已完成",
		},
		{
			name: "done",
			member: contracts.TeamMember{ID: "m", Name: "完成者", Status: "active",
				RunState: contracts.TeamRunDone, Last: "结论通过", Started: now.Add(-time.Minute), Finished: now},
			wantBadge:  "已完成",
			wantAbsent: "运行中",
		},
		{
			name: "failed",
			member: contracts.TeamMember{ID: "m", Name: "失败者", Status: "active",
				RunState: contracts.TeamRunFailed, Error: "provider exploded",
				Started: now.Add(-time.Minute), Finished: now},
			wantBadge:  "失败",
			wantAbsent: "运行中",
		},
		{
			name: "interrupted",
			member: contracts.TeamMember{ID: "m", Name: "被中断", Status: "interrupted",
				RunState: contracts.TeamRunInterrupted, Started: now.Add(-time.Minute)},
			wantBadge:  "已中断",
			wantAbsent: "运行中",
		},
		{
			// An older backend reports only availability: the pane must fall
			// back to 空闲 instead of inventing 运行中.
			name:       "unknown run state",
			member:     contracts.TeamMember{ID: "m", Name: "旧后端", Status: "active"},
			wantBadge:  "空闲",
			wantAbsent: "运行中",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			team := &fakeTeam{members: []contracts.TeamMember{tc.member}}
			h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
			tester := h.tester()
			tester.Frame()
			h.selectPane(tester, "团队")
			h.assertTexts(tester, tc.member.Name, tc.wantBadge)
			if hasTextNode(tester.Texts(), tc.wantAbsent) {
				t.Fatalf("member %s shows the stale badge %q; texts = %v",
					tc.member.Name, tc.wantAbsent, tester.Texts())
			}
		})
	}
}

// clickTextField focuses one labeled text field of a native dialog through its
// accessible box (the placeholders are not text nodes the Tester can Find).
func clickTextField(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleTextField, label)
	if !ok {
		t.Fatalf("text field %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tester.Frame()
}

// hasTextNode reports whether texts contains want as an exact node.
func hasTextNode(texts []string, want string) bool {
	for _, text := range texts {
		if text == want {
			return true
		}
	}
	return false
}

// TestAgentsMemberMetaAndActivityTimeline pins the run meta (model, elapsed,
// steps, tokens) and the detail's bounded activity timeline.
func TestAgentsMemberMetaAndActivityTimeline(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")

	h.assertTexts(tester, "deepseek-chat", "3 步", "1.2K tokens",
		"活动时间线", "创建队友「研究员」", "最新产出", "已整理三份资料")
}

// TestAgentsCopyOutput copies the selected teammate's latest output.
func TestAgentsCopyOutput(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")

	h.click(tester, "复制")
	if got := tester.Clipboard(); got != "已整理三份资料" {
		t.Fatalf("clipboard = %q, want the latest output", got)
	}
	h.assertTexts(tester, "已复制产出")
}

// TestAgentsEmptyStateExplainsTeamAndOpensSpawnForm pins the empty state copy
// and the native spawn form: name/task/model fields, Enter never submits, and
// Create calls TeamService.Spawn off the UI thread.
func TestAgentsEmptyStateExplainsTeamAndOpensSpawnForm(t *testing.T) {
	team := &fakeTeam{}
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")

	h.assertTexts(tester, "还没有队友", "spawn_teammate", "新建队友")
	h.click(tester, "新建队友")
	h.assertTexts(tester, "名称", "任务", "模型（可选）", "创建", "取消")

	// The Name field takes the focus when the form opens; Enter cannot spawn
	// because the form has no default action.
	tester.Type("研究员")
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if spawns := team.spawnCalls(); len(spawns) != 0 {
		t.Fatalf("Enter spawned a teammate: %+v", spawns)
	}

	// Fill the task and press Create.
	clickTextField(t, tester, "任务")
	tester.Type("核对发布清单")
	tester.Frame()
	h.click(tester, "创建")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(team.spawnCalls()) == 0 {
		tester.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	spawns := team.spawnCalls()
	if len(spawns) != 1 || spawns[0].name != "研究员" || spawns[0].prompt != "核对发布清单" {
		t.Fatalf("Spawn calls = %+v, want 研究员/核对发布清单", spawns)
	}
	// The dialog closes and the created teammate is selected.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tester.HasText("模型（可选）") {
		tester.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	if tester.HasText("模型（可选）") {
		t.Fatalf("spawn dialog did not close; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "已创建队友")
}

// TestAgentsCompletionToastOnceAndViewAction pins the completion notification:
// one toast per finished run (deduped by the finished stamp), with a View action
// that opens the team pane on the finished member.
func TestAgentsCompletionToastOnceAndViewAction(t *testing.T) {
	team := newFakeTeam()
	team.setRunState("member-1", contracts.TeamRunRunning)
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	seedTeamSeen(h, tester)
	if got := len(h.notifier.notifications()); got != 0 {
		t.Fatalf("tosted the member history on open: %+v", h.notifier.notifications())
	}

	finished := time.Now()
	team.finishes("member-1", "已整理三份资料", finished)
	h.chat.notify("")
	tester.Frame()
	items := h.notifier.notifications()
	if len(items) != 1 {
		t.Fatalf("notifications = %+v, want one completion toast", items)
	}
	if items[0].level != contracts.NotifySuccess || !strings.Contains(items[0].text, "已完成") ||
		!strings.Contains(items[0].text, "研究员") || items[0].actionLabel != "查看" || items[0].action == nil {
		t.Fatalf("completion toast = %+v, want success/已完成/研究员/查看 action", items[0])
	}

	// The same finished run notifies again (a stream event): no second toast.
	h.chat.notify("")
	tester.Frame()
	if got := len(h.notifier.notifications()); got != 1 {
		t.Fatalf("notifications after a repeat notify = %d, want 1 (deduped)", got)
	}

	// The toast's View action selects the member and shows the pane detail.
	items[0].action()
	tester.Frame()
	h.assertTexts(tester, "研究员", "最新产出", "已整理三份资料")
}

// TestAgentsCloseConfirmationHasNoDefaultAction pins the destructive teammate
// close: the native confirmation has no default button, so Enter does nothing
// and only the explicit action closes the member.
func TestAgentsCloseConfirmationHasNoDefaultAction(t *testing.T) {
	team := newFakeTeam()
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")

	h.click(tester, "关闭")
	h.assertTexts(tester, "关闭队友", "确认关闭", "取消")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := team.closed(); len(got) != 0 {
		t.Fatalf("Enter closed a teammate: %v", got)
	}
	// The safe action is the focused one, so Enter dismissed the dialog instead
	// of confirming: reopen and use the explicit action.
	if !tester.HasText("确认关闭") {
		h.click(tester, "关闭")
	}
	h.assertTexts(tester, "确认关闭")
	h.click(tester, "确认关闭")
	if got := team.closed(); len(got) != 1 || got[0] != "member-1" {
		t.Fatalf("Close calls = %v, want [member-1]", got)
	}
}

// TestAgentsSpawnErrorIsShown pins the spawn form's error line.
func TestAgentsSpawnErrorIsShown(t *testing.T) {
	team := &fakeTeam{spawnErr: errors.New("team is full")}
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.click(tester, "新建队友")
	tester.Type("研究员")
	tester.Frame()
	clickTextField(t, tester, "任务")
	tester.Type("任务")
	tester.Frame()
	h.click(tester, "创建")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !tester.HasText("创建队友失败") {
		tester.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	h.assertTexts(tester, "创建队友失败：team is full")
}

// TestOpenPaneCommandsStableIDs pins the palette entry points the session header
// chip looks up: stable IDs, keyword-carrying copy, and a one-call open that
// also selects the pane and refreshes the task board.
func TestOpenPaneCommandsStableIDs(t *testing.T) {
	board := newFakeTaskBoard(contracts.BoardTask{
		ID: "t1", Subject: "看板任务", Status: contracts.TaskStatusPending, Created: time.Now().UTC(),
	})
	h := newAgentsHarnessWithBoard(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), board, "zh")
	tester := h.tester()
	tester.Frame()
	// Open the Tasks pane through its palette command: the pane alone did not
	// load the board before.
	commands := h.uiRoot.Runtime().Commands()
	byID := map[string]contracts.CommandSpec{}
	for _, command := range commands {
		byID[command.ID] = command
	}
	teamCommand, ok := byID["rightpanel.team.open"]
	if !ok {
		t.Fatalf("commands = %v, want rightpanel.team.open", byID)
	}
	tasksCommand, ok := byID["rightpanel.tasks.open"]
	if !ok {
		t.Fatalf("commands = %v, want rightpanel.tasks.open", byID)
	}
	if !strings.Contains(teamCommand.Title, "团队") || !strings.Contains(teamCommand.Hint, "团队") {
		t.Fatalf("team command copy = %+v, want the 团队 keyword in title and hint", teamCommand)
	}
	if !strings.Contains(tasksCommand.Title, "任务") || !strings.Contains(tasksCommand.Hint, "任务") {
		t.Fatalf("tasks command copy = %+v, want the 任务 keyword in title and hint", tasksCommand)
	}
	if tasksCommand.Run == nil || teamCommand.Run == nil {
		t.Fatalf("open-pane commands have no Run")
	}
	if err := tasksCommand.Run(); err != nil {
		t.Fatalf("tasks command: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "看板任务", "新任务")
	if got := board.listCount(); got == 0 {
		t.Fatal("the tasks command did not read the board")
	}
	if err := teamCommand.Run(); err != nil {
		t.Fatalf("team command: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "队友 · 2", "主代理")
}

// TestOpenPaneCommandsFollowLanguage re-registers the two commands with the
// English copy after a language switch (the command palette reads them by ID).
func TestOpenPaneCommandsFollowLanguage(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	tester.Frame()
	byID := map[string]contracts.CommandSpec{}
	for _, command := range h.uiRoot.Runtime().Commands() {
		byID[command.ID] = command
	}
	if command := byID["rightpanel.team.open"]; command.ID != "" {
		if !strings.Contains(strings.ToLower(command.Title), "team") ||
			!strings.Contains(strings.ToLower(command.Hint), "team") {
			t.Fatalf("team command after the language switch = %+v, want the English copy", command)
		}
	} else {
		t.Fatalf("rightpanel.team.open missing after the language switch: %v", byID)
	}
	if command := byID["rightpanel.tasks.open"]; command.ID != "" {
		if !strings.Contains(strings.ToLower(command.Title), "task") {
			t.Fatalf("tasks command after the language switch = %+v, want the English copy", command)
		}
	}
}

// newAgentsHarnessWithBoard extends the agents harness with the task board, so a
// test can exercise both open-pane commands on one frame.
func newAgentsHarnessWithBoard(t *testing.T, chat *fakeChat, cfg rightpanel.Config,
	team contracts.TeamService, board contracts.TaskBoard, language string) *harness {
	t.Helper()
	h := newAgentsHarness(t, chat, cfg, team, language)
	if board != nil {
		// The harness already built the module without the board; provide the
		// optional service before it is read by a frame.
		if _, err := h.ctx.Provide(contracts.KeyTaskBoard, contracts.TaskBoard(board)); err != nil {
			t.Fatalf("provide task board: %v", err)
		}
	}
	return h
}

// TestAgentsCompletionToastStripsMarkdown pins the toast preview cleaning: the
// final answer is markdown, but the completion toast must be one readable line
// without heading markers, fences or the fenced code content.
func TestAgentsCompletionToastStripsMarkdown(t *testing.T) {
	team := newFakeTeam()
	team.setRunState("member-1", contracts.TeamRunRunning)
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	seedTeamSeen(h, tester)

	answer := "# 脚本回复\n\n- 工作目录已确认\n- 可以继续下一步了\n\n```text\n/data/mygo\n```"
	team.finishes("member-1", answer, time.Now())
	h.chat.notify("")
	tester.Frame()
	items := h.notifier.notifications()
	if len(items) != 1 {
		t.Fatalf("notifications = %+v, want one completion toast", items)
	}
	text := items[0].text
	for _, want := range []string{"已完成", "研究员", "脚本回复", "工作目录已确认"} {
		if !strings.Contains(text, want) {
			t.Fatalf("toast = %q, want %q", text, want)
		}
	}
	for _, unwanted := range []string{"#", "```", "/data/mygo", "\n"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("toast = %q still leaks %q", text, unwanted)
		}
	}
	// The preview is bounded: the toast stays a one-line notification.
	if runes := []rune(text); len(runes) > 140 {
		t.Fatalf("toast = %q (%d runes), want a bounded preview", text, len(runes))
	}
}
