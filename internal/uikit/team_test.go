package uikit

// Frame tests for the team surface entry component (AGENT-SURFACES.md S9) and
// the F-1 regression it fixes: every badge of the team pane — the lead's idle
// badge included — paints the one StatusBadge anatomy (a tinted fill), and only
// a running conversation/run paints the warning tint. The pixel probes render a
// real frame, so a regression back to the old plain BgHover badge (the F-1 bug)
// or to an availability-driven "running" badge shows up.
//
// The tests live in package uikit (internal) because the run-state → badge
// mapping (leadBadge / teamTeammateRunBadge) is the contract being pinned; the
// frame assertions still go through the public TeamSurface entry point.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// teamTestState is one module's persistent view state plus the recorded data
// actions, the way rightpanel passes them to uikit.TeamSurface.
type teamTestState struct {
	memberList   ui.ListState
	memberSel    int
	subagentList ui.ListState
	subagentSel  int
	taskList     ui.ListState
	taskSel      int

	selectedMemberID   string
	selectedSubagentID string
	expandedTask       string
	taskDraft          string
	steerDraft         string
	status             string
	err                string

	closeOpen bool
	closeID   string
	closeName string

	deleteOpen    bool
	deleteID      string
	deleteSubject string

	spawnOpen   bool
	spawnName   string
	spawnPrompt string
	spawnModel  string
	spawnErr    string
	spawnBusy   bool

	// Recorded actions.
	openedSpawn  int
	cancelled    int
	spawnSubmits int
	sends        []string
	interrupts   []string
	closes       []string
	assigns      []string
	statusMoves  []string
	deletes      []string
	creates      []string
	retries      int
	openedTasks  []string
	invalidates  int
}

// membersOptions builds the team-pane options around the current state.
func (s *teamTestState) membersOptions(lead TeamLead, members []contracts.TeamMember,
	subagents []contracts.SubagentActivity) TeamSurfaceOptions {
	return TeamSurfaceOptions{
		Pane:      TeamPaneMembers,
		Lead:      lead,
		Members:   members,
		Subagents: subagents,

		MemberList:         &s.memberList,
		MemberSel:          &s.memberSel,
		SubagentList:       &s.subagentList,
		SubagentSel:        &s.subagentSel,
		SelectedMemberID:   &s.selectedMemberID,
		SelectedSubagentID: &s.selectedSubagentID,
		SteerDraft:         &s.steerDraft,
		Status:             &s.status,
		Err:                &s.err,

		CloseOpen: &s.closeOpen,
		CloseID:   &s.closeID,
		CloseName: &s.closeName,

		Spawn: TeamSpawnForm{
			Open:   &s.spawnOpen,
			Name:   &s.spawnName,
			Prompt: &s.spawnPrompt,
			Model:  &s.spawnModel,
			Error:  &s.spawnErr,
			Busy:   &s.spawnBusy,
			OnSubmit: func() {
				s.spawnSubmits++
			},
			OnCancel: func() {
				s.cancelled++
				s.spawnOpen = false
			},
		},

		OnOpenSpawn: func() {
			s.openedSpawn++
			s.spawnOpen = true
		},
		OnSend: func(memberID, message string) {
			s.sends = append(s.sends, memberID+"|"+message)
		},
		OnInterrupt: func(memberID string) {
			s.interrupts = append(s.interrupts, memberID)
		},
		OnClose: func(memberID string) {
			s.closes = append(s.closes, memberID)
		},
		Invalidate: func() { s.invalidates++ },
	}
}

// tasksOptions builds the task-pane options around the current state.
func (s *teamTestState) tasksOptions(tasks []contracts.BoardTask, members []contracts.TeamMember, canAssign bool) TeamSurfaceOptions {
	return TeamSurfaceOptions{
		Pane:         TeamPaneTasks,
		Tasks:        tasks,
		Members:      members,
		CanAssign:    canAssign,
		TaskList:     &s.taskList,
		TaskSel:      &s.taskSel,
		ExpandedTask: &s.expandedTask,
		TaskDraft:    &s.taskDraft,
		Err:          &s.err,

		TaskDeleteOpen:    &s.deleteOpen,
		TaskDeleteID:      &s.deleteID,
		TaskDeleteSubject: &s.deleteSubject,

		OnTaskCreate: func(subject string) {
			s.creates = append(s.creates, subject)
			s.taskDraft = ""
		},
		OnTaskStatus: func(taskID, status string) {
			s.statusMoves = append(s.statusMoves, taskID+"|"+status)
		},
		OnTaskDelete: func(taskID string) {
			s.deletes = append(s.deletes, taskID)
		},
		OnRetryTasks: func() {
			s.retries++
		},
		OnAssign: func(taskID, owner string) {
			s.assigns = append(s.assigns, taskID+"|"+owner)
		},
		OnOpenTask: func(taskID string) {
			s.openedTasks = append(s.openedTasks, taskID)
			if s.expandedTask == taskID {
				s.expandedTask = ""
			} else {
				s.expandedTask = taskID
			}
		},
		Invalidate: func() { s.invalidates++ },
	}
}

// teamTestHeight is tall enough that the native lists build every row (the
// Tester renders a clipped window; a virtualizing List would not build a row
// outside it).
// teamTestWidth is the frame width: wide enough for the 460 DIP destructive
// confirmations (a Modal wider than the window cannot be clicked).
const teamTestWidth = 640

// teamTestHeight is tall enough that the native lists build every row (the
// Tester renders a clipped window; a virtualizing List would not build a row
// outside it).
const teamTestHeight = 1400

// frameMembers renders one team-pane frame around state.
func frameMembers(s *teamTestState, lead TeamLead, members []contracts.TeamMember,
	subagents []contracts.SubagentActivity) *ui.Tester {
	tester := ui.NewTester(func(c *ui.Context) {
		TeamSurface(c, s.membersOptions(lead, members, subagents))
	}, teamTestWidth, teamTestHeight)
	tester.Frame()
	return tester
}

// frameTasks renders one task-pane frame around state.
func frameTasks(s *teamTestState, tasks []contracts.BoardTask, members []contracts.TeamMember,
	canAssign bool) *ui.Tester {
	tester := ui.NewTester(func(c *ui.Context) {
		TeamSurface(c, s.tasksOptions(tasks, members, canAssign))
	}, teamTestWidth, teamTestHeight)
	tester.Frame()
	return tester
}

// teamBadgePixel samples the badge fill behind one status badge label: the label
// box's left padding (the badge's PaddingX is Space2, so 4 DIP left of the text
// is inside the tinted fill, left of the glyphs).
func teamBadgePixel(t *testing.T, tester *ui.Tester, label string) ui.Color {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("badge label %q not found; texts = %v", label, tester.Texts())
	}
	x := int(rect.X) - 4
	y := int(rect.Y + rect.H/2)
	img := tester.Image()
	if x < img.Rect.Min.X || x >= img.Rect.Max.X || y < img.Rect.Min.Y || y >= img.Rect.Max.Y {
		t.Fatalf("badge %q box %+v is outside the frame %+v", label, rect, img.Rect)
	}
	pixel := img.RGBAAt(x, y)
	return ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}
}

// teamTintOver composites fill over bg with alpha, mirroring ui.Color's straight
// alpha blend (the frame's rounded AA edges are avoided by sampling the fill's
// center band).
func teamTintOver(fill, bg ui.Color, alpha float32) ui.Color {
	a := float32(uint8(alpha*255+0.5)) / 255
	mix := func(f, b uint8) uint8 {
		return uint8(float32(f)*a + float32(b)*(1-a) + 0.5)
	}
	return ui.Color{R: mix(fill.R, bg.R), G: mix(fill.G, bg.G), B: mix(fill.B, bg.B), A: 255}
}

// teamBadgeTint is the expected frame fill of one status badge: the status fill
// tinted at the badge alpha over the page background (the light theme; the
// Tester's default).
func teamBadgeTint(kind StatusKind) ui.Color {
	tokens := design.Light()
	return teamTintOver(statusFill(tokens, kind), tokens.BgBase, badgeTintAlpha(kind))
}

// teamAssertBadgeTint pins one badge's rendered fill to its status tint.
func teamAssertBadgeTint(t *testing.T, tester *ui.Tester, label string, kind StatusKind) {
	t.Helper()
	got := teamBadgePixel(t, tester, label)
	want := teamBadgeTint(kind)
	diff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	if diff(got.R, want.R) > 1 || diff(got.G, want.G) > 1 || diff(got.B, want.B) > 1 || got.A != want.A {
		t.Fatalf("badge %q fill = %+v, want the status tint %+v", label, got, want)
	}
}

// exactTextIndex returns the index of the first text node equal to want.
func exactTextIndex(texts []string, want string) int {
	for index, text := range texts {
		if text == want {
			return index
		}
	}
	return -1
}

// --- F-1: the lead badge -----------------------------------------------------

// TestLeadBadgeUsesUnifiedStatusBadge is the F-1 regression: the lead's idle
// badge must be the pane's one tinted StatusBadge anatomy (the neutral idle
// tint), never the old plain BgHover badge, and only a running conversation
// paints the warning tint.
func TestLeadBadgeUsesUnifiedStatusBadge(t *testing.T) {
	tokens := design.Light()

	idle := TeamSurfaceOptions{}
	idleLabel, idleKind := leadBadge(&idle)
	if idleLabel != "空闲" || idleKind != StatusIdle {
		t.Fatalf("idle lead badge = %q/%v, want 空闲/%v", idleLabel, idleKind, StatusIdle)
	}
	running := TeamSurfaceOptions{Lead: TeamLead{Running: true}}
	runningLabel, runningKind := leadBadge(&running)
	if runningLabel != "运行中" || runningKind != StatusRunning {
		t.Fatalf("running lead badge = %q/%v, want 运行中/%v", runningLabel, runningKind, StatusRunning)
	}

	members := []contracts.TeamMember{{
		ID: "m1", Name: "研究员", Status: "active",
		RunState: contracts.TeamRunDone, Last: "已整理三份资料",
	}}

	// Idle conversation: the lead badge paints the same neutral idle tint as a
	// teammate badge and not the old BgHover fill.
	s := &teamTestState{}
	idleTester := frameMembers(s, TeamLead{Model: "deepseek-chat"}, members, nil)
	teamAssertBadgeTint(t, idleTester, "空闲", StatusIdle)
	oldBadge := teamTintOver(tokens.BgHover, tokens.BgBase, float32(tokens.BgHover.A)/255)
	if got := teamBadgePixel(t, idleTester, "空闲"); got == oldBadge {
		t.Fatalf("lead idle badge fill = %+v, the pre-F-1 plain BgHover badge; want the StatusBadge idle tint %+v",
			got, teamBadgeTint(StatusIdle))
	}
	if exactTextIndex(idleTester.Texts(), "运行中") >= 0 {
		t.Fatalf("an idle conversation draws a running badge; texts = %v", idleTester.Texts())
	}

	// Running conversation: only then the warning tint appears.
	runState := &teamTestState{}
	runningTester := frameMembers(runState, TeamLead{Running: true, Model: "deepseek-chat"}, members, nil)
	teamAssertBadgeTint(t, runningTester, "运行中", StatusRunning)
	if exactTextIndex(runningTester.Texts(), "空闲") >= 0 {
		t.Fatalf("a running conversation still draws an idle badge; texts = %v", runningTester.Texts())
	}
}

// TestLeadAvailabilityIsNotRunning pins the availability rule for the whole
// pane: a member the manager reports as "active" with no tracked run state is
// idle, never running (the old bug drew availability as running).
func TestLeadAvailabilityIsNotRunning(t *testing.T) {
	cases := []struct {
		name   string
		member contracts.TeamMember
		badge  string
		kind   StatusKind
	}{
		{
			name:   "active without run state",
			member: contracts.TeamMember{ID: "m1", Name: "旧后端", Status: "active"},
			badge:  "空闲",
			kind:   StatusIdle,
		},
		{
			name:   "availability word as run state",
			member: contracts.TeamMember{ID: "m1", Name: "旧后端", Status: "active", RunState: "active"},
			badge:  "空闲",
			kind:   StatusIdle,
		},
		{
			name:   "interrupted availability",
			member: contracts.TeamMember{ID: "m1", Name: "被中断", Status: "interrupted"},
			badge:  "已中断",
			kind:   StatusInterrupted,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := &teamTestState{}
			tester := frameMembers(s, TeamLead{}, []contracts.TeamMember{tc.member}, nil)
			if !tester.HasText(tc.badge) {
				t.Fatalf("badge %q missing; texts = %v", tc.badge, tester.Texts())
			}
			if exactTextIndex(tester.Texts(), "运行中") >= 0 {
				t.Fatalf("availability draws a running badge; texts = %v", tester.Texts())
			}
			teamAssertBadgeTint(t, tester, tc.badge, tc.kind)
		})
	}
}

// TestTeamRunStateIsTruthful pins the truthful tracked RunState badges as
// tinted status badges: running/done/failed/interrupted, and an unknown run
// state falling back to idle.
func TestTeamRunStateIsTruthful(t *testing.T) {
	members := []contracts.TeamMember{
		{ID: "m-run", Name: "跑者", Status: "active", RunState: contracts.TeamRunRunning},
		{ID: "m-done", Name: "完成者", Status: "active", RunState: contracts.TeamRunDone, Last: "结论通过"},
		{ID: "m-fail", Name: "失败者", Status: "active", RunState: contracts.TeamRunFailed, Error: "provider exploded"},
		{ID: "m-stop", Name: "被中断", Status: "interrupted", RunState: contracts.TeamRunInterrupted},
		{ID: "m-old", Name: "旧后端", Status: "active"},
	}
	s := &teamTestState{}
	tester := frameMembers(s, TeamLead{}, members, nil)

	for _, want := range []struct {
		badge string
		kind  StatusKind
	}{
		{"运行中", StatusRunning},
		{"已完成", StatusSuccess},
		{"失败", StatusError},
		{"已中断", StatusInterrupted},
		{"空闲", StatusIdle},
	} {
		teamAssertBadgeTint(t, tester, want.badge, want.kind)
	}
	// The failure stays the danger red; a user/runtime interruption is neutral.
	if got := teamBadgePixel(t, tester, "失败"); got != teamBadgeTint(StatusError) {
		t.Fatalf("failed badge is not the danger tint: %+v", got)
	}
	if got := teamBadgePixel(t, tester, "已中断"); got == teamBadgeTint(StatusError) {
		t.Fatalf("interrupted badge paints the failure tint: %+v", got)
	}
}

// TestTeamSubagentBadgesAreTinted pins the read-only delegation group's badges:
// the "子代理" type marker and every tool-status badge share the tinted
// StatusBadge anatomy (the type marker used to be the pane's only plain badge).
func TestTeamSubagentBadgesAreTinted(t *testing.T) {
	subagents := []contracts.SubagentActivity{
		{ID: "sa-run", Name: "资料助手", Status: contracts.SubagentStatusRunning, Prompt: "查找资料"},
		{ID: "sa-done", Name: "代码助手", Status: contracts.SubagentStatusDone, Detail: "结论：通过"},
		{ID: "sa-err", Name: "失败助手", Status: contracts.SubagentStatusError, Detail: "provider exploded"},
		{ID: "sa-stop", Name: "取消助手", Status: contracts.SubagentStatusCancelled},
	}
	s := &teamTestState{}
	tester := frameMembers(s, TeamLead{}, nil, subagents)

	for _, want := range []struct {
		label string
		kind  StatusKind
	}{
		{"子代理", StatusUnknown},
		{"运行中", StatusRunning},
		{"完成", StatusSuccess},
		{"失败", StatusError},
		{"已取消", StatusCancelled},
	} {
		teamAssertBadgeTint(t, tester, want.label, want.kind)
	}
}

// --- team pane interactions --------------------------------------------------

// TestTeamSelectShowsDetailBelowList pins the inspector anatomy: choosing a
// teammate row opens its detail BELOW the list (a design.Panel), not inside the
// selected row.
func TestTeamSelectShowsDetailBelowList(t *testing.T) {
	now := time.Now()
	members := []contracts.TeamMember{{
		ID: "m1", Name: "研究员", Status: "active",
		RunState: contracts.TeamRunDone, Model: "deepseek-chat", Last: "已整理三份资料",
		Activity: []contracts.TeamActivity{{
			ID: "a1", MemberID: "m1", Kind: contracts.TeamActivityFinish,
			Text: "「研究员」已完成", Time: now,
		}},
	}}
	s := &teamTestState{}
	tester := frameMembers(s, TeamLead{}, members, nil)
	if exactTextIndex(tester.Texts(), "最新产出") >= 0 {
		t.Fatalf("the detail is drawn before a row is selected; texts = %v", tester.Texts())
	}

	if err := tester.Click("研究员"); err != nil {
		t.Fatalf("select teammate: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if s.selectedMemberID != "m1" {
		t.Fatalf("SelectedMemberID = %q, want m1", s.selectedMemberID)
	}
	texts := tester.Texts()
	listIndex := exactTextIndex(texts, "研究员")
	detailIndex := exactTextIndex(texts, "最新产出")
	if listIndex < 0 || detailIndex < 0 || detailIndex < listIndex {
		t.Fatalf("the selected detail does not render below the list: %v", texts)
	}
	for _, want := range []string{"发送消息", "发送", "中断", "关闭", "活动时间线"} {
		if exactTextIndex(texts, want) < 0 && !tester.HasText(want) {
			t.Fatalf("teammate detail is missing %q; texts = %v", want, texts)
		}
	}
}

// TestTeamCloseConfirmationHasNoDefaultAction pins the destructive teammate
// close: the confirmation uses the product's one dialog action bar
// (uikit.DialogFooter — the danger action first, the safe Cancel rightmost),
// with the safe action as the default focus, so a stray Enter cancels and can
// never close the teammate.
func TestTeamCloseConfirmationHasNoDefaultAction(t *testing.T) {
	members := []contracts.TeamMember{{
		ID: "m1", Name: "研究员", Status: "active", RunState: contracts.TeamRunDone,
	}}
	s := &teamTestState{}
	tester := frameMembers(s, TeamLead{}, members, nil)
	if err := tester.Click("研究员"); err != nil {
		t.Fatalf("select teammate: %v", err)
	}
	tester.Frame()

	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("open close confirmation: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"关闭队友", "确认关闭", "取消"} {
		if !tester.HasText(want) {
			t.Fatalf("close confirmation is missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The footer order is the product's: danger first (left), the safe action
	// rightmost. The Modal takes the keyboard on its first focusable control,
	// so the safe Cancel is the default focus and the danger action never is
	// (AP-12: the destructive action must not be the default).
	confirmRect, ok := tester.Find("确认关闭")
	if !ok {
		t.Fatalf("confirm action missing; texts = %v", tester.Texts())
	}
	cancelRect, ok := tester.Find("取消")
	if !ok {
		t.Fatalf("cancel action missing; texts = %v", tester.Texts())
	}
	if !(confirmRect.X < cancelRect.X) {
		t.Fatalf("footer order x = confirm %.1f, cancel %.1f; want the danger action before the safe one",
			confirmRect.X, cancelRect.X)
	}
	if tester.Focused("确认关闭") {
		t.Fatalf("the close confirmation put the default focus on the destructive action; texts = %v",
			tester.Texts())
	}
	if !tester.Focused("取消") {
		t.Fatalf("the close confirmation does not focus the safe action; texts = %v", tester.Texts())
	}
	// A stray Enter cancels (it never closes the member).
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(s.closes) != 0 {
		t.Fatalf("Enter closed a teammate: %v", s.closes)
	}
	if tester.HasText("确认关闭") {
		t.Fatalf("the safe action did not dismiss the close confirmation; texts = %v", tester.Texts())
	}
	// Only the explicit danger action closes the member.
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("reopen close confirmation: %v", err)
	}
	tester.Frame()
	if err := tester.Click("确认关闭"); err != nil {
		t.Fatalf("confirm close: %v", err)
	}
	tester.Frame()
	if len(s.closes) != 1 || s.closes[0] != "m1" {
		t.Fatalf("Close calls = %v, want [m1]", s.closes)
	}
	if tester.HasText("确认关闭") {
		t.Fatalf("the close confirmation stayed open after the explicit action")
	}
}

// TestTeamEmptyStateExplainsTeamAndOpensSpawn covers the empty state: it
// explains the team capability and opens the native spawn form.
func TestTeamEmptyStateExplainsTeamAndOpensSpawn(t *testing.T) {
	s := &teamTestState{}
	tester := frameMembers(s, TeamLead{}, nil, nil)
	if !tester.HasText("还没有队友") || !tester.HasText("spawn_teammate") {
		t.Fatalf("empty state does not explain the team; texts = %v", tester.Texts())
	}
	if !tester.HasText("新建队友") {
		t.Fatalf("empty state has no spawn entry; texts = %v", tester.Texts())
	}
	if err := tester.Click("新建队友"); err != nil {
		t.Fatalf("open spawn form: %v", err)
	}
	if s.openedSpawn != 1 || !s.spawnOpen {
		t.Fatalf("OnOpenSpawn calls = %d, spawnOpen = %v", s.openedSpawn, s.spawnOpen)
	}
	tester.Frame()
	for _, want := range []string{"名称", "任务", "模型（可选）", "创建", "取消"} {
		if !tester.HasText(want) {
			t.Fatalf("spawn form is missing %q; texts = %v", want, tester.Texts())
		}
	}
}

// TestTeamSpawnFormHasNoDefaultAction pins the spawn form's safety contract:
// Enter never spawns (no default action), Create stays disabled while the name
// or task is empty, and the explicit Create action reaches the module.
func TestTeamSpawnFormHasNoDefaultAction(t *testing.T) {
	s := &teamTestState{spawnOpen: true}
	tester := frameMembers(s, TeamLead{}, nil, nil)

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if s.spawnSubmits != 0 {
		t.Fatalf("Enter submitted the spawn form: %d submissions", s.spawnSubmits)
	}
	if err := tester.Click("创建"); err != nil {
		t.Fatalf("click disabled Create: %v", err)
	}
	if s.spawnSubmits != 0 {
		t.Fatalf("the disabled Create action spawned a teammate")
	}

	// The name field is auto-focused; the task field takes a click.
	tester.Type("研究员")
	tester.Frame()
	info, ok := tester.AccessByRole(ui.RoleTextField, "任务")
	if !ok {
		t.Fatalf("task field not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tester.Frame()
	tester.Type("核对发布清单")
	tester.Frame()
	if err := tester.Click("创建"); err != nil {
		t.Fatalf("click Create: %v", err)
	}
	if s.spawnSubmits != 1 {
		t.Fatalf("Create submissions = %d, want 1 (name=%q task=%q)", s.spawnSubmits, s.spawnName, s.spawnPrompt)
	}
}

// --- task board --------------------------------------------------------------

// TestTeamTaskGroupsAndCounts pins the List Header grouping and the counts:
// action-first order (in progress → pending → completed → unknown) with one
// header count and the summary line.
func TestTeamTaskGroupsAndCounts(t *testing.T) {
	tasks := []contracts.BoardTask{
		{ID: "c", Subject: "完成的任务", Status: contracts.TaskStatusCompleted},
		{ID: "p", Subject: "待处理的任务", Status: contracts.TaskStatusPending},
		{ID: "i1", Subject: "进行中的一", Status: contracts.TaskStatusInProgress},
		{ID: "i2", Subject: "进行中的二", Status: contracts.TaskStatusInProgress},
		{ID: "u", Subject: "未知状态的任务", Status: "blocked"},
	}
	s := &teamTestState{}
	tester := frameTasks(s, tasks, nil, false)

	for _, want := range []string{
		"进行中 · 2", "待处理 · 1", "已完成 · 1", "其他 · 1",
		"5 个任务 · 进行中 2 · 待处理 1 · 已完成 1",
	} {
		if !tester.HasText(want) {
			t.Fatalf("task board is missing %q; texts = %v", want, tester.Texts())
		}
	}
	texts := tester.Texts()
	order := []string{"进行中的一", "进行中的二", "待处理的任务", "完成的任务", "未知状态的任务"}
	previous := -1
	for _, subject := range order {
		index := exactTextIndex(texts, subject)
		if index < 0 {
			t.Fatalf("subject %q not found; texts = %v", subject, texts)
		}
		if index <= previous {
			t.Fatalf("tasks are not status/created ordered: %v", texts)
		}
		previous = index
	}
}

// TestTeamAssignViaMenu pins the inline owner control: the optional
// assigner turns the owner line into a menu of 未认领 / 主代理 / live teammates,
// and a board without the assigner keeps a read-only line (no dead menu).
func TestTeamAssignViaMenu(t *testing.T) {
	tasks := []contracts.BoardTask{{ID: "t1", Subject: "待认领任务", Status: contracts.TaskStatusPending}}
	members := []contracts.TeamMember{{ID: "m1", Name: "研究员", Status: "active", RunState: contracts.TeamRunIdle}}

	s := &teamTestState{}
	tester := frameTasks(s, tasks, members, true)
	if !tester.HasText("负责人：未认领") {
		t.Fatalf("owner line missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("负责人：未认领"); err != nil {
		t.Fatalf("open owner menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"未认领", "主代理", "研究员"} {
		if !tester.HasText(want) {
			t.Fatalf("owner menu is missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("研究员"); err != nil {
		t.Fatalf("assign to teammate: %v", err)
	}
	if len(s.assigns) != 1 || s.assigns[0] != "t1|研究员" {
		t.Fatalf("Assign calls = %v, want [t1|研究员]", s.assigns)
	}

	// A read-only host (no TaskAssigner) draws a plain owner line.
	readOnly := &teamTestState{}
	readOnlyTester := frameTasks(readOnly, tasks, members, false)
	if err := readOnlyTester.Click("负责人：未认领"); err == nil {
		tester.Frame()
		if readOnlyTester.HasText("主代理") {
			t.Fatalf("a board without TaskAssigner opened an owner menu; texts = %v", readOnlyTester.Texts())
		}
	}
	if len(readOnly.assigns) != 0 {
		t.Fatalf("a read-only owner control assigned: %v", readOnly.assigns)
	}
}

// TestTeamTaskDependencyLine pins the dependency/blocker line: a waiting
// dependency is a warning, a ready pending task says 可开始.
func TestTeamTaskDependencyLine(t *testing.T) {
	tasks := []contracts.BoardTask{
		{ID: "dep", Subject: "前置任务", Status: contracts.TaskStatusInProgress},
		{ID: "blocked", Subject: "被阻塞的任务", Status: contracts.TaskStatusPending, BlockedBy: []string{"dep"}},
		{ID: "ready", Subject: "可开始的任务", Status: contracts.TaskStatusPending},
	}
	s := &teamTestState{}
	tester := frameTasks(s, tasks, nil, false)
	if !tester.HasText("等待 dep「前置任务」完成") {
		t.Fatalf("blocked dependency line missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("可开始") {
		t.Fatalf("ready line missing; texts = %v", tester.Texts())
	}
}

// TestTeamTaskEnterExpandsDetail pins the keyboard path: the native list chooses
// a row, Enter asks the module to toggle the detail panel below the list, and
// the detail renders description/dependencies/write scopes and the transitions.
func TestTeamTaskEnterExpandsDetail(t *testing.T) {
	tasks := []contracts.BoardTask{{
		ID: "dep", Subject: "前置任务", Status: contracts.TaskStatusCompleted,
	}, {
		ID: "t1", Subject: "展开我的任务", Status: contracts.TaskStatusPending,
		Description: "任务细节描述", BlockedBy: []string{"dep"}, WriteScopes: []string{"docs/a.md"},
	}}
	s := &teamTestState{}
	tester := frameTasks(s, tasks, nil, false)
	if err := tester.Click("展开我的任务"); err != nil {
		t.Fatalf("select task row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(s.openedTasks) != 1 || s.openedTasks[0] != "t1" {
		t.Fatalf("OnOpenTask calls = %v, want [t1]", s.openedTasks)
	}
	for _, want := range []string{"任务细节描述", "dep「前置任务」· 已完成", "写入范围：docs/a.md", "开始任务", "删除"} {
		if !tester.HasText(want) {
			t.Fatalf("expanded detail is missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("收起"); err != nil {
		t.Fatalf("collapse detail: %v", err)
	}
	tester.Frame()
	if len(s.openedTasks) != 2 || s.expandedTask != "" {
		t.Fatalf("Collapse calls = %v, expandedTask = %q", s.openedTasks, s.expandedTask)
	}
	if tester.HasText("任务细节描述") {
		t.Fatalf("the detail stayed open after Collapse; texts = %v", tester.Texts())
	}
}

// TestTeamTaskDeleteRequiresConfirm pins the destructive task delete: the row's
// ⋯ menu opens a native confirmation built with the product's one action bar
// (uikit.DialogFooter — danger first, safe Cancel rightmost), with the safe
// action as the default focus, so a stray Enter cancels and only the explicit
// Delete removes the task.
func TestTeamTaskDeleteRequiresConfirm(t *testing.T) {
	tasks := []contracts.BoardTask{{ID: "t1", Subject: "临时任务", Status: contracts.TaskStatusPending}}
	s := &teamTestState{}
	tester := frameTasks(s, tasks, nil, false)

	openMenu := func() {
		t.Helper()
		if err := tester.Click("任务操作：临时任务"); err != nil {
			t.Fatalf("open task menu: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
		if err := tester.Click("删除"); err != nil {
			t.Fatalf("choose Delete: %v", err)
		}
		tester.Frame()
	}
	openMenu()
	for _, want := range []string{"删除任务", "取消", "删除"} {
		if !tester.HasText(want) {
			t.Fatalf("delete confirmation is missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The footer is the product's: the destructive Delete first (left), the
	// safe Cancel rightmost. The Modal takes the keyboard on its first
	// focusable control, so the safe Cancel is the default focus and the
	// danger action never is (AP-12: the destructive action must not be the
	// default).
	deleteRect, ok := tester.Find("删除")
	if !ok {
		t.Fatalf("delete action missing; texts = %v", tester.Texts())
	}
	cancelRect, ok := tester.Find("取消")
	if !ok {
		t.Fatalf("cancel action missing; texts = %v", tester.Texts())
	}
	if !(deleteRect.X < cancelRect.X) {
		t.Fatalf("footer order x = delete %.1f, cancel %.1f; want the danger action before the safe one",
			deleteRect.X, cancelRect.X)
	}
	if tester.Focused("删除") {
		t.Fatalf("the delete confirmation put the default focus on the destructive action; texts = %v",
			tester.Texts())
	}
	if !tester.Focused("取消") {
		t.Fatalf("the delete confirmation does not focus the safe action; texts = %v", tester.Texts())
	}
	// A stray Enter cancels (it never deletes the task).
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if len(s.deletes) != 0 {
		t.Fatalf("Enter deleted a task: %v", s.deletes)
	}
	if tester.HasText("删除任务") {
		t.Fatalf("the safe action did not dismiss the delete confirmation; texts = %v", tester.Texts())
	}
	// Only the explicit danger action deletes.
	openMenu()
	if err := tester.Click("删除"); err != nil {
		t.Fatalf("confirm Delete: %v", err)
	}
	tester.Frame()
	if len(s.deletes) != 1 || s.deletes[0] != "t1" {
		t.Fatalf("Delete calls = %v, want [t1]", s.deletes)
	}
	if tester.HasText("删除任务") {
		t.Fatalf("the delete confirmation stayed open after the explicit action")
	}
}

// TestTeamTaskEmptyStateAndAdd covers the empty board and the add row: the
// explicit empty copy plus a disabled Add until the draft has a subject.
func TestTeamTaskEmptyStateAndAdd(t *testing.T) {
	s := &teamTestState{}
	tester := frameTasks(s, nil, nil, false)
	if !tester.HasText("还没有任务") || !tester.HasText("team_task_create") {
		t.Fatalf("empty board copy missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("click Add with an empty draft: %v", err)
	}
	if len(s.creates) != 0 {
		t.Fatalf("an empty draft created a task: %v", s.creates)
	}
	rect, ok := tester.Find("新任务")
	if !ok {
		t.Fatalf("add row input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	tester.Type("整理发布说明")
	tester.Frame()
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("click Add: %v", err)
	}
	if len(s.creates) != 1 || s.creates[0] != "整理发布说明" {
		t.Fatalf("Create calls = %v, want [整理发布说明]", s.creates)
	}
}
