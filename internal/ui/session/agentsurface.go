package session

// DSH-aligned agent surfaces (this file): the session header's 智能体团队 entry,
// 子智能体 chip and background-jobs chip with their native overlay panels, the
// child-agent conversation view the main content area switches to, and the
// generated-file turn-tail cards under a finished answer.
//
// The session shell stays one screen: the child conversation is a content-area
// switch with the parent's title as a breadcrumb, never a sidebar entry or a
// second window. The read models come from internal/contracts/agentsurface.go
// (ChildAgentService/JobService/FilePreviewer); every service is an optional
// context lookup, so a host without one hides the entry instead of failing.
//
// Frames never wait on the backends: overlay opens read the in-memory read
// models synchronously (one bounded snapshot), and the two actions that can
// block — sending a message to a continuable teammate and killing a job (the
// job tool waits a grace period for the process) — run off the UI thread and
// publish their result through UIRuntime.Post.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Header entry metrics. The compact width is where the agent entries drop their
// words (the icon and the count stay) so the conversation title keeps its
// measure; the row action height is the 24 DIP compact target used inside the
// overlay cards and the turn-tail file rows.
const (
	// agentHeaderCompactWidth is the header column width under which the agent
	// entries render in their compact (icon + count) form.
	agentHeaderCompactWidth float32 = design.TranscriptWidth * 2 / 3
	// agentRowActionHeight is the compact inline action height: the control
	// ladder's compact rung (32), never an off-ladder 24 (2026-11 audit UD-07).
	agentRowActionHeight float32 = design.RowCompactHeight
	// maxGeneratedFileRows bounds the turn-tail cards' manual row list (the
	// cards are one transcript row, so an unbounded list would make the row
	// enormous); the header count keeps the full deduplicated total.
	maxGeneratedFileRows = 8
	// agentHeadingWeight is the heading weight of the agent surfaces' names and
	// titles: the type roles carry no heading weight yet (DESIGN.md D6), so the
	// surfaces name the design token once instead of repeating a raw literal
	// (2026-11 audit UD-10).
	agentHeadingWeight = design.WeightSemibold
	// workbenchSectionMaxHeight bounds each team-workbench column: with the
	// modal's header and footer, two columns at DialogBodyMax would exceed the
	// desktop's minimum window height; the lists scroll inside their column
	// instead.
	workbenchSectionMaxHeight float32 = design.Space8 * 10 // 320 DIP
)

// jobsPollInterval is how often a frame with a running background job asks for
// the next one: fine-grained enough to show a completion promptly, slow enough
// to stay cheap while the user is idle.
const jobsPollInterval = 500 * time.Millisecond

const (
	// jobOutputPanelHeight is the expanded output panel's fixed height (256
	// DIP): a chatty job scrolls inside it instead of stretching the overlay.
	jobOutputPanelHeight float32 = design.Space8 * 8
	// jobStopConfirmWindow is how long an armed stop waits for its second
	// press; after it the row resets to 停止 and nothing was stopped.
	jobStopConfirmWindow = 3 * time.Second
)

// agentSurface is the UI-thread state of the agent surfaces: the overlay open
// flags and their scroll/list state, the cached task-board snapshot (loaded
// when the team overlay opens), the child conversation view's state, and the
// in-flight action flags. It lives in the module's state (one instance per
// loaded module) and is only touched on the UI thread.
type agentSurface struct {
	// teamOpen/childOpen/jobsOpen are the three header overlays' open flags:
	// the native popovers close them on Escape, a press outside or a second
	// click on their entry.
	teamOpen  bool
	childOpen bool
	jobsOpen  bool
	// modelMenuOpen backs the composer model chip's menu (PopoverMenuButton's
	// Open), so its open/close is reported to the toast anchor like the overlays.
	modelMenuOpen bool

	// overlayReported tracks the last open/close state reported to the host toast
	// anchor per popover site, so reportOverlay fires once per transition (the
	// host's open/close count stays paired across idle frames).
	overlayReported map[string]bool

	// The overlays' scroll offsets and list state (stable row identity).
	teamScroll     ui.ScrollState
	childScroll    ui.ScrollState
	switcherScroll ui.ScrollState
	jobsScroll     ui.ScrollState
	teamMembers    ui.ListState
	teamHistory    ui.ListState
	teamTasks      ui.ListState
	switcherList   ui.ListState
	jobsList       ui.ListState
	activityList   ui.ListState

	// tasks is the shared task board snapshot cached when the team overlay
	// opens; tasksLoaded separates "not read yet" from "read, empty".
	tasks       []contracts.BoardTask
	tasksErr    string
	tasksLoaded bool

	// Child conversation view. childID names the open child ("" = the parent
	// conversation); childDraft/childSending/childSendErr are the continuable
	// teammate's message input; childReply is the last answer SendChildAgent
	// returned; crumbChosen backs the breadcrumb's native link state.
	childID       string
	childDraft    string
	childInput    ui.Handle
	childFocus    bool
	childSending  bool
	childSendErr  string
	childReply    string
	childReplyFor string
	// childSendSeq identifies the in-flight send: leaving/switching the child
	// view bumps it, so a late answer from the abandoned call is dropped instead
	// of landing on another child's view.
	childSendSeq int
	crumbChosen  int

	// Background-job stop feedback: jobKilling is the id of the job whose kill
	// is in flight (its stop action is disabled), jobErr the last failure.
	jobKilling string
	jobErr     string
	// jobStopArmed is the job whose stop waits for the second press, and
	// jobStopAt is when it was armed. The arm resets after
	// jobStopConfirmWindow (or when the overlay closes or the job ends), so a
	// stray press can never stop a process.
	jobStopArmed string
	jobStopAt    time.Time
	// jobStaleArmed/jobStaleAt arm the bulk stop of the running jobs that still
	// follow an older permission identity (2026-11 audit RG-09): the hint's
	// 「停止这些任务」 waits for a second press within jobStopConfirmWindow, the
	// same two-stage danger semantic as a single job row's stop.
	jobStaleArmed bool
	jobStaleAt    time.Time
	// jobOutputs holds each job row's expanded output panel, keyed by job id:
	// its open flag, the bounded output read's state and the panel's scroll.
	jobOutputs map[string]*jobOutputPanel

	// Team controls: teamStopping/teamClosing are the member ids of the control
	// action in flight (its row action is disabled); teamErr is the last failure
	// (shown inside the team overlay / child view); closeMember is the member
	// whose close awaits the destructive confirmation.
	teamStopping string
	teamClosing  string
	teamErr      string
	closeMember  string

	// workbenchOpen is the persistent team workbench's open flag: a native modal
	// with the members and the conversation's shared task board. Opening it
	// leaves the team popover's own state (open flag, scroll offsets, cached
	// task snapshot) untouched, so closing the workbench returns to the same
	// overlay; navigating away closes both.
	workbenchOpen bool
	// workbenchReturnFocus asks the next frame to hand the keyboard focus back
	// to the 打开工作台 entry after the modal closed (Escape or its close
	// button); the native modal restores the focus too, and this covers the
	// close-button path.
	workbenchReturnFocus bool
	// workbenchTrigger is the 打开工作台 button the focus returns to.
	workbenchTrigger ui.Handle
	// The workbench's own list and scroll state: it is built while the team
	// popover is still open, so it cannot share the popover's handles (one
	// handle bound to two controls in one build panics).
	workbenchMembers       ui.ListState
	workbenchHistory       ui.ListState
	workbenchTasks         ui.ListState
	workbenchMembersScroll ui.ScrollState
	workbenchTasksScroll   ui.ScrollState

	// Execution history: the durable lifecycle records of the active
	// conversation. historySession scopes the cache to the session on screen;
	// the read happens off the UI thread and publishes through UIRuntime.Post.
	history        []contracts.ExecutionRecord
	historyList    ui.ListState
	historySession string

	// lastVisitFrame/lastVisitSet track the frame of the previous conversation
	// screen build. The host rebuilds only the current screen, so a gap means
	// another screen was shown in between; the transient overlays belong to one
	// visit and are closed on the way back.
	lastVisitFrame uint64
	lastVisitSet   bool
}

// jobOutputPanel is one job row's expanded output panel state: whether it is
// open, the bounded read's result (and when it started, for the live poll) and
// the panel's scroll offset, kept across frames and reads.
type jobOutputPanel struct {
	open   bool
	loaded bool
	busy   bool
	seq    uint64
	text   string
	err    string
	at     time.Time
	// running is whether the job ran when the last read started: a job that
	// finished since then (possibly while the overlay was closed) gets one
	// final read for its complete output.
	running bool
	scroll  ui.ScrollState
}

// closeAgentPopovers closes every transient agent overlay (team, child switcher,
// jobs, workbench). It is called when the conversation screen is left/returned
// to, when the conversation switches, and when a child conversation opens or
// closes. An armed job stop belongs to the closed overlay and resets with it.
func (st *state) closeAgentPopovers() {
	st.agents.teamOpen = false
	st.agents.childOpen = false
	st.agents.jobsOpen = false
	st.agents.workbenchOpen = false
	st.agents.jobStopArmed = ""
}

// noteScreenVisit closes the transient overlays when the conversation screen
// was not the one rendered in the previous frame (a screen visit starts clean).
// The host builds only the current screen every frame, so a frame gap is the
// screen-navigation signal available to a screen module.
func (st *state) noteScreenVisit() {
	if st == nil || st.runtime == nil {
		return
	}
	frame := st.runtime.Frame()
	if st.agents.lastVisitSet && frame > st.agents.lastVisitFrame+1 {
		st.closeAgentPopovers()
		st.agents.childSending = false
		st.agents.childSendErr = ""
		st.agents.childSendSeq++
		st.runtime.Invalidate()
	}
	st.agents.lastVisitFrame = frame
	st.agents.lastVisitSet = true
}

// --- optional service lookups (nil-tolerant) --------------------------------

// teamService returns the host's team backend, or nil when the host has none.
func (st *state) teamService() contracts.TeamService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyTeam)
	if !ok || raw == nil {
		return nil
	}
	team, _ := raw.(contracts.TeamService)
	return team
}

// childAgentService returns the host's unified child-agent read model.
func (st *state) childAgentService() contracts.ChildAgentService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyChildAgents)
	if !ok || raw == nil {
		return nil
	}
	children, _ := raw.(contracts.ChildAgentService)
	return children
}

// jobService returns the host's background-job read/control surface.
func (st *state) jobService() contracts.JobService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyJobs)
	if !ok || raw == nil {
		return nil
	}
	jobs, _ := raw.(contracts.JobService)
	return jobs
}

// executionHistoryService returns the host's durable execution-history read
// model, or nil when the host has none (the jobs overlay then shows live rows
// only).
func (st *state) executionHistoryService() contracts.ExecutionHistoryService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyExecutionHistory)
	if !ok || raw == nil {
		return nil
	}
	history, _ := raw.(contracts.ExecutionHistoryService)
	return history
}

// filePreviewer returns the right sidebar's file-preview bridge.
func (st *state) filePreviewer() contracts.FilePreviewer {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyFilePreview)
	if !ok || raw == nil {
		return nil
	}
	previewer, _ := raw.(contracts.FilePreviewer)
	return previewer
}

// taskBoard returns the shared team task board.
func (st *state) taskBoard() contracts.TaskBoard {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyTaskBoard)
	if !ok || raw == nil {
		return nil
	}
	board, _ := raw.(contracts.TaskBoard)
	return board
}

// taskBoardForSession returns the task board scoped to one conversation when the
// backend implements the per-session view (DSH's Lead-session board), else the
// process-wide board.
func (st *state) taskBoardForSession(sessionID string) contracts.TaskBoard {
	board := st.taskBoard()
	if board == nil {
		return nil
	}
	if scoped, ok := board.(contracts.SessionTaskBoard); ok {
		if sessionBoard := scoped.TaskBoardForSession(sessionID); sessionBoard != nil {
			return sessionBoard
		}
	}
	return board
}

// --- header entries ---------------------------------------------------------

// buildAgentRunBand is the header's signature run surface: one quiet band
// after the title that collects the conversation's run state and the
// orchestration entries — 运行中, 智能体团队, 子智能体, 后台任务 and 待审批 — so the
// band reads as one instrument instead of five unrelated badges. Team and
// subagent entries stay visible at zero (key features); the running badge and
// the approval count only appear while there is something to report. The host
// keeps rendering its own utilities (search, inspector toggle) after the
// actions slot.
func buildAgentRunBand(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService) {
	if st == nil {
		return
	}
	activeID := chat.ActiveSession()
	session, hasSession := chat.Session(activeID)
	running := hasSession && session.Running
	team := st.teamService()
	children := st.childAgentService()
	jobs := st.jobService()
	// The restored history is scoped to the conversation on screen; live
	// members are process-wide (the rig manager has no session scope) and stay
	// visible in every conversation, as before.
	var teamMembers []contracts.TeamMember
	teamRunning := 0
	if team != nil {
		teamMembers = visibleTeamMembers(team.Members(), activeID)
		teamRunning = teamRunningCount(teamMembers)
	}
	var jobList []contracts.BackgroundJob
	if jobs != nil {
		jobList = jobs.Jobs()
	}
	// The durable execution history is session-scoped and read off the UI
	// thread: the eager per-session read lets the jobs entry stay hidden when a
	// conversation never ran anything, while still surfacing the records after a
	// restart (when no live manager is left to list).
	st.ensureExecutionHistory(activeID)
	history := st.sessionExecutionHistory(activeID)
	pending := pendingApprovalCount(st)
	// The band exists only when it has something to say: a team/subagent backend
	// (their entries stay discoverable at zero), a live job, a durable execution
	// record, a running turn or a pending approval. Otherwise the title keeps
	// the whole band's width.
	if team == nil && children == nil && len(jobList) == 0 && len(history) == 0 &&
		!running && pending == 0 {
		return
	}
	compact := st.headerWidth > 0 && st.headerWidth < agentHeaderCompactWidth
	// One quiet instrument, not five unrelated chips. The entries are native
	// buttons (mygo keeps their hover/pressed/focus states, the brand only fixes
	// their metrics); the row itself carries no extra container, because a
	// bordered band around already-bordered buttons reads as a control inside a
	// control ("double chrome"). Spacing is the only grouping.
	ui.Row(c).AlignItems(ui.Center).Height(design.ControlCompactHeight).
		Gap(design.Space2).
		Children(func() {
			if running {
				uikit.StatusBadge(c, t, uikit.StatusRunning, st.tr("运行中", "Running"))
			}
			if team != nil {
				buildTeamEntry(c, t, st, teamMembers, teamRunning, activeID, compact)
			}
			buildSubagentEntry(c, t, st, activeID, compact)
			buildJobsEntry(c, t, st, activeID, history, compact)
			if pending > 0 {
				// A pending approval is needs-action, not "queued": the state
				// matrix gives running the accent and warning the states that wait
				// on the user, so the chip carries WarningText on a 12% Warning
				// tint like the approval card's own edge. Painted neutral it read
				// as one more quiet instrument while the run was in fact blocked
				// on a decision (2026-11 audit).
				uikit.StatusBadge(c, t, uikit.StatusNeedsAction,
					fmt.Sprintf(st.tr("待审批 %d", "%d awaiting approval"), pending)).
					Tooltip(st.tr("待审批的请求在上方输入框旁处理",
						"Pending approvals are handled above the composer"))
			}
			// The close confirmation is a screen-level overlay, not a child of
			// the team popover: answering it dismisses the popover (an outside
			// click), and a dialog built inside the panel would unmount with it
			// before its action ran.
			if st.agents.closeMember != "" {
				buildTeamMemberCloseConfirm(c, st, teamMembers)
			}
			// The persistent team workbench is a native modal over the band: it
			// gives the members and the shared board the room the compact popover
			// lacks, without a permanent right-panel page.
			buildTeamWorkbench(c, t, st, teamMembers, teamRunning)
		})
}

// pendingApprovalCount reports how many approval requests are waiting, for the
// run band's 待审批 count (the cards themselves stay above the composer).
func pendingApprovalCount(st *state) int {
	if st == nil {
		return 0
	}
	raw, ok := st.ctx.Get(contracts.KeyApprovals)
	if !ok || raw == nil {
		return 0
	}
	approvals, ok := raw.(contracts.Approvals)
	if !ok || approvals == nil {
		return 0
	}
	return len(approvals.Pending())
}

// agentEntryButton is one header entry: an icon, its words and the count, on the
// entries' common metrics. The words stay at every width — a narrow band shrinks
// the padding and ellipsizes the label instead of dropping it (VD-21) — and the
// Tooltip carries the exact numbers.
func agentEntryButton(c *ui.Context, t *design.Tokens, label, tooltip string, compact bool) ui.Element {
	padding := design.Space2
	if compact {
		// A narrow band shrinks the entry's padding, never its words: the entry is
		// the feature's one visible name, and an icon alone stopped saying which
		// feature it opens (VD-21 / AGENTS §3.17).
		padding = design.Space1
	}
	// A label-shaped button, not an outlined control: no edge, a neutral fill at
	// rest and the selection tint under the pointer (B-spec §3.1.2). The fill is
	// drawn here because a background override turns off mygo's own button face
	// states, so every state is covered by hand and fades over MotionFast.
	button := ui.Button(c, "").Label(label).Tooltip(tooltip).
		Height(t.Scale(design.ControlCompactHeight)).PaddingX(padding).
		Radius(design.RadiusSM).Shrink(1).Gap(design.Space1).
		Background(t.BgHover)
	switch {
	case button.Pressed():
		button = button.Background(t.BgSelected)
	case button.Hovered() || button.Focused():
		button = button.Background(t.BgSelected)
	}
	return button.Transition(design.TransitionColors(c))
}

// agentEntryText is one entry's words: 12/500 (a label, not metadata), ellipsized
// rather than dropped when the band is narrow.
func agentEntryText(c *ui.Context, t *design.Tokens, label string) ui.Element {
	return design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
		FontWeight(design.WeightMedium).TextColor(t.LabelSecondary).
		MaxLines(1).Ellipsis("…").MaxWidth(design.Space8 * 5)
}

// agentCountText renders the quiet count beside an entry's words.
func agentCountText(c *ui.Context, t *design.Tokens, count int) {
	design.Apply(ui.Text(c, fmt.Sprintf("%d", count)), t.Font(design.TypeXS)).
		TextColor(t.LabelTertiary).NoWrap()
}

// agentStatusFill is the status dot's fill color: the semantic fill token,
// never a status text color used as a fill (DESIGN.md §5.2).
func agentStatusFill(t *design.Tokens, kind uikit.StatusKind) ui.Color {
	switch kind {
	case uikit.StatusRunning, uikit.StatusInProgress:
		// Running is the accent role, not a warning: warning is for the states
		// that need the user (F-06, DESIGN.md §3.7).
		return t.Accent
	case uikit.StatusSuccess:
		return t.Success
	case uikit.StatusError:
		return t.Danger
	default:
		return t.LabelTertiary
	}
}

// toastAnchorService resolves the optional host toast-placement service, caching
// it on the state (the host registers it on the kernel root). A minimal host
// without the service returns nil and reporting is a no-op.
func (st *state) toastAnchorService() contracts.ToastAnchorService {
	if st.toastAnchor == nil && st.ctx != (cordis.Context{}) {
		st.toastAnchor, _ = st.ctx.Get(contracts.KeyToastAnchor)
	}
	return st.toastAnchor
}

// anyOverlayOpen reports whether one of this screen's floating layers is open.
// The window-wide Escape must stand down while one is: a window shortcut takes
// the key before an overlay's own handler, so it would swallow the Escape that
// closes the popover (the team/subagent/jobs panels, the model and permission
// menus, the queue and usage popovers, the suggestion menus).
func (st *state) anyOverlayOpen() bool {
	if st == nil {
		return false
	}
	// The flags themselves, not the reported map: a host without the toast
	// anchor service still opens these popovers, and the Escape contract must not
	// depend on a reporting side channel. The find bar and the suggestion menus
	// are deliberately absent: the session's own Escape arbitration closes them
	// (find first, then the menus), so it has to stay live while they are open.
	return st.queueMenuOpen ||
		st.draftOverwriteOpen || st.agents.teamOpen || st.agents.childOpen ||
		st.agents.jobsOpen || st.agents.modelMenuOpen || st.agents.workbenchOpen ||
		st.reminders.listOpen || st.reminders.createOpen ||
		st.agents.closeMember != ""
}

// reportOverlay reports one module-owned popover's open/close transition to the
// host toast anchor so the stack yields while the overlay is open. It fires once
// per transition (idempotent across idle frames), keeping the host's open/close
// count paired; the host floors the count at zero. Call it after the popover
// builds so the open flag reflects any outside-click close (2026-11 UI review
// #13). No-op when the host provides no service.
func (st *state) reportOverlay(site string, open bool) {
	anchor := st.toastAnchorService()
	if anchor == nil {
		return
	}
	if st.agents.overlayReported == nil {
		st.agents.overlayReported = make(map[string]bool)
	}
	if st.agents.overlayReported[site] == open {
		return
	}
	st.agents.overlayReported[site] = open
	anchor.ReportOverlay(open)
}

// buildTeamEntry is the header's 智能体团队 entry: a users glyph, the words (the
// count alone on a narrow band) and the live member count, opening the native
// team overlay. The entry stays visible whenever the host provides the team
// backend, even at zero: team orchestration is a key feature and the entry is
// its one-step surface (v0.2.20 correction of the v0.2.19 subtraction pass).
func buildTeamEntry(c *ui.Context, t *design.Tokens, st *state, members []contracts.TeamMember,
	running int, activeID string, compact bool) {
	// Team and subagents are the product's key orchestration features: their
	// entries stay visible even at zero so the surfaces are discoverable
	// (user correction to the v0.2.19 subtraction pass).
	label := st.tr("智能体团队", "Agent team")
	tooltip := teamMemberCounts(st, members, running)
	chip := agentEntryButton(c, t, label, tooltip, compact)
	chip.Children(func() {
		ui.Icon(c, icons.Users()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
		agentEntryText(c, t, label)
		// A zero count is not information worth a header glyph: it made an idle
		// band read "0 / 0" even though the entries themselves are the point.
		// The tooltip and the accessible label still carry the exact count.
		if len(members) > 0 {
			agentCountText(c, t, len(members))
		}
		if running > 0 {
			// The quiet warning dot keeps the running state visible when the
			// compact band has no room for the words.
			ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
				Background(t.Warning).Margin(0, 0, 0, design.Space1)
		}
	})
	if chip.Clicked() {
		st.agents.teamOpen = !st.agents.teamOpen
		if st.agents.teamOpen {
			st.loadTeamTasks(activeID)
		}
		st.runtime.Invalidate()
	}
	if !st.agents.teamOpen {
		st.reportOverlay("team", false)
		return
	}
	ui.PopoverBase(c, chip, &st.agents.teamOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthCompact)
		buildTeamPanel(c, t, st, members, running)
	})
	st.reportOverlay("team", st.agents.teamOpen)
}

// visibleTeamMembers scopes the restored history to the active conversation
// while keeping every live member: a restored record belongs to the session it
// ran in, so another conversation must not list it.
func visibleTeamMembers(members []contracts.TeamMember, activeID string) []contracts.TeamMember {
	activeID = strings.TrimSpace(activeID)
	if activeID == "" {
		return members
	}
	out := make([]contracts.TeamMember, 0, len(members))
	for _, member := range members {
		if member.Historical {
			if scope := strings.TrimSpace(member.SessionID); scope != "" && scope != activeID {
				continue
			}
		}
		out = append(out, member)
	}
	return out
}

// teamMemberFinished reports whether a member belongs in the 已结束 section
// rather than the live 成员 roster: a restored historical record, or a live
// member whose run reached a terminal state (done/failed/interrupted). A
// completed-but-not-closed teammate is finished too, so it must not sit under
// 成员 (2026-11 UI review P2: 成员节 = 未结束 roster).
func teamMemberFinished(member contracts.TeamMember) bool {
	if member.Historical {
		return true
	}
	switch uikit.ParseRunState(member.RunState) {
	case uikit.RunStateDone, uikit.RunStateFailed, uikit.RunStateInterrupted:
		return true
	}
	return false
}

// splitTeamMembers separates the unfinished (live) roster from the finished
// members (restored history plus terminal-run live rows).
func splitTeamMembers(members []contracts.TeamMember) (live, history []contracts.TeamMember) {
	for _, member := range members {
		if teamMemberFinished(member) {
			history = append(history, member)
		} else {
			live = append(live, member)
		}
	}
	return live, history
}

// teamRunningCount counts the members with a run in flight.
func teamRunningCount(members []contracts.TeamMember) int {
	running := 0
	for _, member := range members {
		if uikit.ParseRunState(member.RunState) == uikit.RunStateRunning {
			running++
		}
	}
	return running
}

// teamMemberCounts is the overlay's and the tooltip's member summary. The counts
// match the section semantics (2026-11 UI review P2): 名成员 is the unfinished
// roster shown under 成员, 运行中 the runs in flight, and 已结束 the finished
// members in their own section — so a team of only finished members reads
// "0 名成员 · … · N 条已结束", not "N 名成员" over an empty 成员 section.
func teamMemberCounts(st *state, members []contracts.TeamMember, running int) string {
	finished := countFinished(members)
	live := len(members) - finished
	counts := fmt.Sprintf(st.tr("%d 名成员 · %d 个运行中", "%d members · %d running"), live, running)
	if finished > 0 {
		counts += fmt.Sprintf(st.tr(" · %d 条已结束", " · %d finished"), finished)
	}
	return counts
}

// countFinished counts the finished members of a snapshot (restored history plus
// terminal-run live rows) — the 已结束 section's count.
func countFinished(members []contracts.TeamMember) int {
	count := 0
	for _, member := range members {
		if teamMemberFinished(member) {
			count++
		}
	}
	return count
}

// loadTeamTasks snapshots the active conversation's shared task board when the
// team overlay opens. The board is the per-session rig task manager (one bounded
// read), and a read failure stays inside the overlay instead of failing the
// screen.
func (st *state) loadTeamTasks(sessionID string) {
	st.agents.tasks = nil
	st.agents.tasksErr = ""
	st.agents.tasksLoaded = false
	if strings.TrimSpace(sessionID) == "" {
		// No conversation: never fall back to the process-wide legacy board, so
		// a v0.2.31 document cannot surface in an empty session overlay. The
		// snapshot is complete (empty), not pending, so the empty state renders
		// instead of a permanent "reading…" row.
		st.agents.tasksLoaded = true
		return
	}
	board := st.taskBoardForSession(sessionID)
	if board == nil {
		return
	}
	tasks, err := board.List(context.Background())
	if err != nil {
		st.agents.tasksErr = st.tr("任务板读取失败：", "Task board read failed: ") + err.Error()
		return
	}
	st.agents.tasks = tasks
	st.agents.tasksLoaded = true
}

// agentPanelStyle gives an overlay panel the product's floating-surface look
// (the raised panel style plus the overlay elevation token).
func agentPanelStyle(panel ui.Element, t *design.Tokens, width float32) {
	panel = design.Apply(panel, t.Panel(), t.ShadowOverlay())
	panel.Width(width)
	panel.Padding(design.Space3)
	panel.Gap(design.Space2)
}

// agentSectionLabel is the overlays' small section heading.
func agentSectionLabel(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).FontWeight(agentHeadingWeight).
		TextColor(t.LabelTertiary)
}

// agentHint is the overlays' quiet footer hint (how to leave, what a click
// does). It is a sentence, so it uses the 12 metadata step rather than the 11
// micro-label step.
func agentHint(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
}

// agentRowAction is one compact text action inside an overlay card or a file
// row. The visible words stay short; the accessible name is unique per row
// (action + object) so keyboard/AT users and frame tests can tell the rows
// apart.
func agentRowAction(c *ui.Context, t *design.Tokens, text, accessibleName string, onClick func()) {
	button := ui.Button(c, text).Label(accessibleName)
	// Button text is never the 11px micro step (2026-11 audit UD-04): the
	// metadata step (12/18) keeps the short action words readable.
	design.Apply(button, t.Font(design.TypeXXS))
	button.Height(agentRowActionHeight).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0)
	if button.Clicked() && onClick != nil {
		onClick()
	}
}

// agentRowActionTooltip is agentRowAction with the row action's explanation.
func agentRowActionTooltip(c *ui.Context, t *design.Tokens, text, accessibleName, tooltip string,
	onClick func()) {
	button := ui.Button(c, text).Label(accessibleName).Tooltip(tooltip)
	// Button text is never the 11px micro step (2026-11 audit UD-04): the
	// metadata step (12/18) keeps the short action words readable.
	design.Apply(button, t.Font(design.TypeXXS))
	button.Height(agentRowActionHeight).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0)
	if button.Clicked() && onClick != nil {
		onClick()
	}
}

// agentPanelHeader is the overlays' title row: glyph, title and the counts.
func agentPanelHeader(c *ui.Context, t *design.Tokens, icon *ui.SVG, title, counts string) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, title), t.Font(design.TypeS)).FontWeight(agentHeadingWeight).
			TextColor(t.LabelPrimary)
		ui.Spacer(c)
		design.Apply(ui.Text(c, counts), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).NoWrap()
	})
}

// buildTeamPanel is the team overlay's body: member cards (state dot, name,
// model, last activity, 打开) and shared task cards (subject, status, owner,
// blockers, write scopes), scrolling inside the native panel.
func buildTeamPanel(c *ui.Context, t *design.Tokens, st *state, members []contracts.TeamMember,
	running int) {
	agentPanelHeader(c, t, icons.Users(), st.tr("智能体团队", "Agent team"),
		teamMemberCounts(st, members, running))
	if st.agents.teamErr != "" {
		uikit.ErrorLine(c, st.agents.teamErr)
	}
	live, history := splitTeamMembers(members)
	ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
		TrackScroll(&st.agents.teamScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
			agentSectionLabel(c, t, st.tr("成员", "Members"))
			if len(live) > 0 {
				buildTeamMembersList(c, t, st, &st.agents.teamMembers, live)
			} else {
				// 成员 is the unfinished roster; finished members sit in 已结束
				// below. One empty-state wording whether or not finished rows exist
				// (2026-11 UI review P2). Empty-state sentences go through the shared
				// single-line entry (2026-11 audit UD-05/UD-15): never the 11px step.
				uikit.EmptyState(c, st.tr("还没有队友", "No teammates yet"))
			}
			if len(history) > 0 {
				// The restored records stay after the app restarts: the section
				// tells the user they are history, not silently mixed live rows.
				agentSectionLabel(c, t, st.tr("已结束", "Finished"))
				buildTeamMembersList(c, t, st, &st.agents.teamHistory, history)
			}
			agentSectionLabel(c, t, st.tr("共享任务", "Shared tasks")+sharedTasksScrollHint(st))
			buildSharedTasksList(c, t, st)
		})
	})
	// The workbench entry: the persisted read surface for the team, kept out of
	// the right panel (AGENTS.md: team/subagent surfaces stay in the conversation
	// flow). It opens over this popover, which keeps its state underneath.
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Spacer(c)
		open := design.Apply(ui.Button(c, st.tr("打开工作台", "Open workbench")),
			t.SecondaryButton()).Height(design.RowCompactHeight).Shrink(0).
			Bind(&st.agents.workbenchTrigger).
			Tooltip(st.tr("在原生窗口里查看成员与共享任务", "See the members and shared tasks in a dialog"))
		if st.agents.workbenchReturnFocus {
			// The workbench closed: the entry that opened it takes the keyboard
			// back, so Escape never drops the focus on the page behind.
			st.agents.workbenchReturnFocus = false
			st.agents.workbenchTrigger.Focus(c)
		}
		if open.Clicked() {
			// The native modal cannot take pointer input while this popover's
			// overlay is above it (mygo routes the press to the popover first, as
			// the close-member confirmation found), so the popover closes as the
			// modal opens. Its own state — scroll offsets, the cached task
			// snapshot — stays in st.agents and is shown again on close.
			st.agents.teamOpen = false
			st.agents.workbenchOpen = true
			st.runtime.Invalidate()
		}
	})
	agentHint(c, t, st.tr("点击成员在面板中打开会话 · Esc 关闭",
		"Open a member here · Esc closes"))
}

// buildTeamMembersList renders the teammate cards through the official List so
// the overlay can grow without a per-frame card wall.
func buildTeamMembersList(c *ui.Context, t *design.Tokens, st *state, list *ui.ListState,
	members []contracts.TeamMember) {
	list.Key = func(index int) any {
		if index < 0 || index >= len(members) {
			return nil
		}
		return members[index].ID
	}
	list.Label = func(index int) string {
		if index < 0 || index >= len(members) {
			return ""
		}
		return agentDisplayName(members[index].Name, members[index].ID)
	}
	ui.List(c, list, len(members), func(index int) {
		buildTeamMemberCard(c, t, st, members[index])
	}).FillWidth().Gap(design.Space2)
}

// buildTeamMemberCard is one member row: running state dot, name (with the
// 当前 badge when its child view is open), latest activity, model and the 打开
// action (only when the host can render a child conversation).
func buildTeamMemberCard(c *ui.Context, t *design.Tokens, st *state, member contracts.TeamMember) {
	name := agentDisplayName(member.Name, member.ID)
	kind := uikit.ParseRunState(member.RunState).Kind()
	card := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	card.Children(func() {
		ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
			Background(agentStatusFill(t, kind)).Margin(design.Space1, 0, 0, 0)
		ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if !member.Historical && st.agents.childID == member.ID {
					design.Apply(ui.Badge(c, st.tr("当前", "Current")), t.Badge())
				}
				ui.Spacer(c)
				// One status word, not two: a finished member used to carry both a
				// 历史 chip and the run state beside it, which read as two competing
				// states (visual audit). The state phrase absorbs the affordance:
				// 已结束 · 可继续 / 已结束 · 只读.
				status := uikit.RunStateLabel(st.locale, member.RunState)
				if member.Historical {
					status = st.tr("已结束", "Finished")
					if member.Resumable {
						status += st.tr(" · 可继续", " · continuable")
					} else {
						status += st.tr(" · 只读", " · read-only")
					}
				}
				uikit.StatusText(c, t, design.TypeXXXS, kind, status)
			})
			design.Apply(ui.Text(c, agentMemberActivity(st, member)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			if model := strings.TrimSpace(member.Model); model != "" {
				design.Apply(ui.Text(c, model), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			}
		})
		if !member.Historical {
			st.buildTeamMemberControls(c, t, member, name, kind)
		}
		if st.childAgentService() != nil {
			action, actionLabel := st.tr("打开", "Open"), st.tr("打开成员 ", "Open member ")+name
			tooltip := st.tr("打开成员会话", "Open the member conversation")
			if member.Historical && member.Resumable {
				// A restored teammate that the backend can cold-resume gets the
				// interaction that is actually available: open its conversation
				// with the follow-up composer focused.
				action = st.tr("继续", "Continue")
				actionLabel = st.tr("继续此队友 ", "Continue teammate ") + name
				tooltip = st.tr("继续此队友（发送消息会恢复它之前的会话）",
					"Continue this teammate (a message resumes its previous conversation)")
			}
			agentRowActionTooltip(c, t, action, actionLabel, tooltip, func() {
				st.openChildView(member.ID)
			})
		}
	})
}

// agentMemberActivity is the member card's one line of latest activity: the
// newest tracked team activity, else the retained answer, else the empty state.
func agentMemberActivity(st *state, member contracts.TeamMember) string {
	if len(member.Activity) > 0 {
		last := member.Activity[len(member.Activity)-1]
		if text := strings.TrimSpace(last.Text); text != "" {
			// A close entry is the backend's record of the gesture, not a state:
			// "关闭「研究员」" beside a finished member reads as an action still
			// waiting to happen (visual audit). The card states the state instead.
			if last.Kind == contracts.TeamActivityClose ||
				strings.HasPrefix(text, st.tr("关闭", "Closed")) {
				return st.tr("「", "“") + agentDisplayName(member.Name, member.ID) + st.tr("」已结束", "” finished")
			}
			return text
		}
	}
	if text := strings.TrimSpace(member.Last); text != "" {
		return st.tr("上一轮：", "Last: ") + truncateRunes(text, 96)
	}
	return st.tr("还没有活动", "No activity yet")
}

// sharedTasksScrollHint appends the task count and the scroll affordance to the
// 共享任务 section label once the list no longer fits: with more than three
// tasks the panel scrolls, and without the hint the last card just looked cut
// off (R6).
func sharedTasksScrollHint(st *state) string {
	if st == nil || !st.agents.tasksLoaded || len(st.agents.tasks) <= 3 {
		return ""
	}
	return fmt.Sprintf(st.tr(" · %d 项 · 可滚动", " · %d items · scrollable"), len(st.agents.tasks))
}

// buildSharedTasksList renders the task board snapshot: an unread board, a
// read error and the empty state stay explicit instead of rendering a blank
// section.
func buildSharedTasksList(c *ui.Context, t *design.Tokens, st *state) {
	buildSharedTasksListState(c, t, st, &st.agents.teamTasks)
}

// buildSharedTasksListState is buildSharedTasksList with the caller's list
// state: the team popover and the workbench both show the same cached snapshot
// and must not bind one handle to two controls in a build.
func buildSharedTasksListState(c *ui.Context, t *design.Tokens, st *state, list *ui.ListState) {
	board := st.taskBoard()
	if board == nil {
		uikit.EmptyState(c, st.tr("任务板未启用", "Task board is not enabled"))
		return
	}
	if st.agents.tasksErr != "" {
		uikit.ErrorLine(c, st.agents.tasksErr)
		return
	}
	if !st.agents.tasksLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spinner(c).Label(st.tr("读取任务板", "Loading tasks")).
				Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, st.tr("读取任务板…", "Loading tasks…")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		})
		return
	}
	tasks := st.agents.tasks
	if len(tasks) == 0 {
		uikit.EmptyState(c, st.tr("还没有共享任务", "No shared tasks yet"))
		return
	}
	list.Key = func(index int) any {
		if index < 0 || index >= len(tasks) {
			return nil
		}
		return tasks[index].ID
	}
	list.Label = func(index int) string {
		if index < 0 || index >= len(tasks) {
			return ""
		}
		return tasks[index].Subject
	}
	ui.List(c, list, len(tasks), func(index int) {
		buildSharedTaskCard(c, t, st, tasks[index], index)
	}).FillWidth().Gap(design.Space2)
}

// buildSharedTaskCard is one task card: the stable short ID and the subject with
// the truthfully parsed status, then the owner, the blockers and the expected
// write scopes. Two tasks with the same subject stay distinguishable because
// every card names its own ID (R6).
func buildSharedTaskCard(c *ui.Context, t *design.Tokens, st *state, task contracts.BoardTask, index int) {
	status := uikit.ParseTaskStatus(task.Status)
	card := ui.Column(c).FillWidth().Gap(design.Space1).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			// The ID is the card's stable identity: a small mono token before the
			// subject, never the only way to tell two same-subject cards apart by
			// their dependency line.
			design.Apply(ui.Text(c, taskShortID(task, index)), t.MonoText(design.TypeXXXS)).
				TextColor(t.LabelTertiary).NoWrap().
				Tooltip(st.tr("任务 ID：", "Task ID: ") + task.ID)
			design.Apply(ui.Text(c, task.Subject), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
				TextColor(t.LabelPrimary).Grow(1).Shrink(1).MinWidthPercent(0).
				MaxLines(1).Ellipsis("…")
			uikit.StatusBadge(c, t, status.Kind(), uikit.TaskStatusLabel(st.locale, task.Status))
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Wrap().Gap(design.Space2).Children(func() {
			owner := st.tr("未指派", "Unassigned")
			if name := taskOwnerDisplay(st, task.Owner); name != "" {
				owner = st.tr("负责人 ", "Owner ") + name
			}
			agentTaskMeta(c, t, owner)
			if len(task.BlockedBy) > 0 {
				agentTaskMeta(c, t, st.tr("依赖 ", "Blocked by ")+strings.Join(task.BlockedBy, ", "))
			}
			if len(task.WriteScopes) > 0 {
				agentTaskMeta(c, t, st.tr("写入 ", "Writes ")+strings.Join(task.WriteScopes, ", "))
			}
		})
	})
}

// taskShortID is the stable short identity printed on a task card: the board's
// own ID (task-3), or a positional serial only for a record the backend left
// unidentified.
func taskShortID(task contracts.BoardTask, index int) string {
	if id := strings.TrimSpace(task.ID); id != "" {
		return id
	}
	return fmt.Sprintf("#%d", index+1)
}

// taskOwnerDisplay resolves a task owner to a readable name: the board's
// pseudo owner "taskboard" is the lead agent, a team member ID becomes that
// member's display name, and an owner the board already stores as a name stays
// as-is. The raw ID never reaches the card when a display name exists (R6).
func taskOwnerDisplay(st *state, owner string) string {
	trimmed := strings.TrimSpace(owner)
	if trimmed == "" {
		return ""
	}
	if trimmed == contracts.TaskOwnerUnassigned {
		return st.tr("主智能体", "Lead agent")
	}
	if team := st.teamService(); team != nil {
		for _, member := range team.Members() {
			if strings.TrimSpace(member.ID) == trimmed {
				return agentDisplayName(member.Name, member.ID)
			}
		}
	}
	return trimmed
}

// agentTaskMeta is one muted metadata item of a task card. Task metadata is a
// sentence-like line, so it uses the 12 metadata step, not the 11 micro step
// (R6/DESIGN.md §5.2 B).
func agentTaskMeta(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).
		TextColor(t.LabelTertiary).NoWrap()
}

// buildTeamWorkbench renders the persistent team workbench: a native modal, wide
// enough for two columns, with every visible member (state, model, latest
// activity, open and stop while running) and the conversation's shared task
// board (subject, status, owner, blockers, write scopes). It is built from the
// band, not from inside the team popover, so the popover keeps its state
// underneath; the modal itself owns the keyboard (Esc closes it) and hands the
// focus back to the entry that opened it.
func buildTeamWorkbench(c *ui.Context, t *design.Tokens, st *state,
	members []contracts.TeamMember, running int) {
	if st == nil || !st.agents.workbenchOpen {
		return
	}
	el := ui.Modal(c, &st.agents.workbenchOpen, func() {
		design.Apply(ui.Column(c).Width(design.DialogWidthXL).Gap(design.Space3),
			func(el ui.Element) ui.Element { return el.Padding(design.Space4) }).
			Children(func() {
				agentPanelHeader(c, t, icons.Users(), st.tr("团队工作台", "Team workbench"),
					teamMemberCounts(st, members, running))
				if st.agents.teamErr != "" {
					uikit.ErrorLine(c, st.agents.teamErr)
				}
				// Two same-screen sections: the members on the left, the shared
				// task board on the right. Each column scrolls on its own, so a
				// long team never pushes the board out of the dialog.
				ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space4).Children(func() {
					buildWorkbenchMembersSection(c, t, st, members)
					buildWorkbenchTasksSection(c, t, st)
				})
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, st.tr(
						"Esc 关闭工作台；团队浮层保持打开。",
						"Esc closes the workbench; the team overlay stays open.")),
						t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
					closeButton := design.Apply(ui.Button(c, st.tr("关闭", "Close")),
						t.SecondaryButton()).Height(design.RowCompactHeight).Shrink(0).
						Label(st.tr("关闭团队工作台", "Close team workbench"))
					if closeButton.Clicked() {
						st.closeTeamWorkbench()
						st.runtime.Invalidate()
					}
				})
			})
	})
	if el.Dismissed() {
		// Escape or a click outside: reopen the team overlay it was opened from
		// (the scroll offsets and the cached task snapshot never left st.agents)
		// and return the focus to the entry on the next frame.
		st.closeTeamWorkbench()
		st.runtime.Invalidate()
	}
}

// closeTeamWorkbench leaves the workbench and returns to the team overlay: the
// overlay's state is preserved because only the two open flags change.
func (st *state) closeTeamWorkbench() {
	if st == nil {
		return
	}
	st.agents.workbenchOpen = false
	st.agents.workbenchReturnFocus = true
	st.agents.teamOpen = true
}

// buildWorkbenchMembersSection is the workbench's member column: the live
// members and, below them, the restored (finished) records, each row the shared
// member card (state, model, activity, open and stop while running).
func buildWorkbenchMembersSection(c *ui.Context, t *design.Tokens, st *state,
	members []contracts.TeamMember) {
	live, history := splitTeamMembers(members)
	ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Gap(design.Space2).Children(func() {
		// The count matches the section: 成员 is the unfinished roster (finished
		// members are counted in 已结束 below), same口径 as the overlay (P2).
		agentSectionLabel(c, t, fmt.Sprintf(st.tr("成员 · %d", "Members · %d"), len(live)))
		ui.Scroll(c).FillWidth().MaxHeight(workbenchSectionMaxHeight).
			TrackScroll(&st.agents.workbenchMembersScroll).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
				if len(live) > 0 {
					buildTeamMembersList(c, t, st, &st.agents.workbenchMembers, live)
				} else {
					uikit.EmptyState(c, st.tr("还没有队友", "No teammates yet"))
				}
				if len(history) > 0 {
					agentSectionLabel(c, t, st.tr("已结束", "Finished"))
					buildTeamMembersList(c, t, st, &st.agents.workbenchHistory, history)
				}
			})
		})
	})
}

// buildWorkbenchTasksSection is the workbench's board column: the same cached
// snapshot the team overlay shows, with the card count in its section label.
func buildWorkbenchTasksSection(c *ui.Context, t *design.Tokens, st *state) {
	label := st.tr("共享任务", "Shared tasks")
	if st.agents.tasksLoaded && st.agents.tasksErr == "" {
		label = fmt.Sprintf(st.tr("共享任务 · %d", "Shared tasks · %d"), len(st.agents.tasks))
	}
	ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Gap(design.Space2).Children(func() {
		agentSectionLabel(c, t, label)
		ui.Scroll(c).FillWidth().MaxHeight(workbenchSectionMaxHeight).
			TrackScroll(&st.agents.workbenchTasksScroll).Children(func() {
			buildSharedTasksListState(c, t, st, &st.agents.workbenchTasks)
		})
	})
}

// buildSubagentEntry is the header's 子智能体 N chip: the count of the active
// session's child agents (subagents plus team members), opening the switcher
// overlay. A zero count renders no chip at all: a permanent "子智能体 0" entry
// only squeezed the conversation title while offering nothing (R15).
func buildSubagentEntry(c *ui.Context, t *design.Tokens, st *state, activeID string, compact bool) {
	// One popover owner per state: while a child conversation is open the
	// breadcrumb's switcher chip owns the child list. Rendering the header chip
	// as well would bind the shared switcher ListState to two popovers and
	// panic the header build ("Handle bound to two controls in one build").
	if strings.TrimSpace(st.agents.childID) != "" {
		return
	}
	children := st.childAgentService()
	if children == nil {
		return
	}
	agents := children.ChildAgents(activeID)
	// Keep the entry at zero: subagents are a key feature and the entry is the
	// only one-step way to the child switcher (user correction).
	running := 0
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.Status), contracts.ChildAgentRunning) {
			running++
		}
	}
	label := fmt.Sprintf(st.tr("子智能体 %d", "Subagents %d"), len(agents))
	tooltip := fmt.Sprintf(st.tr("%d 个会话 · %d 个运行中", "%d conversations · %d running"),
		len(agents), running)
	visual := label
	if len(agents) == 0 {
		// Zero is not worth a header number; the entry stays for discoverability.
		visual = st.tr("子智能体", "Subagents")
	}
	chip := agentEntryButton(c, t, label, tooltip, compact)
	chip.Children(func() {
		ui.Icon(c, icons.Activity()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
		agentEntryText(c, t, visual)
	})
	if chip.Clicked() {
		st.agents.childOpen = !st.agents.childOpen
		st.runtime.Invalidate()
	}
	if !st.agents.childOpen {
		st.reportOverlay("child", false)
		return
	}
	ui.PopoverBase(c, chip, &st.agents.childOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthS)
		buildChildSwitcher(c, t, st, agents)
	})
	st.reportOverlay("child", st.agents.childOpen)
}

// buildChildSwitcher is the child switcher overlay: every child agent with its
// kind, truthful status, model and last activity, and the 进入 action that
// switches the main content area to that child conversation.
func buildChildSwitcher(c *ui.Context, t *design.Tokens, st *state, agents []contracts.ChildAgent) {
	running := 0
	for _, agent := range agents {
		if strings.EqualFold(strings.TrimSpace(agent.Status), contracts.ChildAgentRunning) {
			running++
		}
	}
	agentPanelHeader(c, t, icons.Activity(), st.tr("子智能体会话", "Child conversations"),
		fmt.Sprintf(st.tr("%d 个运行中 · 共 %d 个", "%d running · %d total"), running, len(agents)))
	if len(agents) == 0 {
		design.Apply(ui.Text(c, st.tr("还没有子智能体。子代理/队友启动后会出现在这里。",
			"No child conversations yet. Subagents and teammates appear here once they start.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(3)
		return
	}
	ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
		TrackScroll(&st.agents.switcherScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
			st.agents.switcherList.Key = func(index int) any {
				if index < 0 || index >= len(agents) {
					return nil
				}
				return agents[index].ID
			}
			st.agents.switcherList.Label = func(index int) string {
				if index < 0 || index >= len(agents) {
					return ""
				}
				return agentDisplayName(agents[index].Name, agents[index].ID)
			}
			ui.List(c, &st.agents.switcherList, len(agents), func(index int) {
				buildChildSwitcherRow(c, t, st, agents[index])
			}).FillWidth().Gap(design.Space2)
		})
	})
	agentHint(c, t, st.tr("选择后在当前面板打开 · Esc 关闭",
		"Choosing one opens it here · Esc closes"))
}

// buildChildSwitcherRow is one switcher row: state dot, name, kind/mode/model
// and the latest activity, with the 进入 action.
func buildChildSwitcherRow(c *ui.Context, t *design.Tokens, st *state, agent contracts.ChildAgent) {
	name := agentDisplayName(agent.Name, agent.ID)
	kind := uikit.ParseRunState(agent.Status).Kind()
	card := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	card.Children(func() {
		ui.Box(c).Size(design.Space2, design.Space2).Radius(design.RadiusMax).
			Background(agentStatusFill(t, kind)).Margin(design.Space1, 0, 0, 0)
		ui.Column(c).Grow(1).Shrink(1).MinWidthPercent(0).Gap(design.Space1).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, name), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if agent.Historical {
					design.Apply(ui.Text(c, st.tr("历史", "History")), t.Font(design.TypeXXXS)).
						TextColor(t.LabelTertiary).NoWrap()
				}
				design.Apply(ui.Text(c, childAgentMeta(st, agent)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
				ui.Spacer(c)
				statusKind, statusText := childAgentStatusPhrase(st, agent)
				uikit.StatusText(c, t, design.TypeXXXS, statusKind, statusText)
			})
			design.Apply(ui.Text(c, childAgentActivityLine(st, agent)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		})
		action, actionLabel := st.tr("进入", "Open"), st.tr("进入子智能体会话 ", "Open child conversation ")+name
		tooltip := st.tr("打开这个子智能体的会话", "Open this child conversation")
		if agent.Historical && agent.Resumable {
			action = st.tr("继续", "Continue")
			if agent.Kind == contracts.ChildAgentTeammate {
				actionLabel = st.tr("继续此队友 ", "Continue teammate ") + name
			} else {
				actionLabel = st.tr("继续此子代理 ", "Continue subagent ") + name
			}
			tooltip = st.tr("继续它（发送消息会恢复它的历史会话）",
				"Continue it (a message resumes its finished conversation)")
		}
		agentRowActionTooltip(c, t, action, actionLabel, tooltip, func() {
			st.openChildView(agent.ID)
		})
	})
}

// childAgentStatusPhrase is the child's one status expression: the live run state,
// or — for a restored record — the ended state and what it can still do
// (已结束 · 可继续 / 已结束 · 只读). The switcher row and the child's own header
// both read it, so a child never carries two competing status words (visual
// audit: 已完成 / 已结束记录 / 可继续 stood side by side).
func childAgentStatusPhrase(st *state, agent contracts.ChildAgent) (uikit.StatusKind, string) {
	kind := uikit.ParseRunState(agent.Status).Kind()
	if !agent.Historical {
		return kind, uikit.RunStateLabel(st.locale, agent.Status)
	}
	text := st.tr("已结束", "Finished")
	if agent.Resumable {
		text += st.tr(" · 可继续", " · continuable")
	} else {
		text += st.tr(" · 只读", " · read-only")
	}
	return kind, text
}

// childAgentMeta is the switcher's quiet type line: the kind, the live follow-up
// mode, and the model when the backend names one. A restored record carries no
// mode here: its state and capability are the status phrase beside this line, and
// the durability note says the rest.
func childAgentMeta(st *state, agent contracts.ChildAgent) string {
	parts := []string{childAgentKindLabel(st, agent.Kind)}
	if !agent.Historical {
		parts = append(parts, childAgentModeLabel(st, agent.Mode))
	}
	if model := strings.TrimSpace(agent.Model); model != "" {
		parts = append(parts, model)
	}
	return strings.Join(parts, " · ")
}

// childAgentKindLabel localizes the two child-agent families.
func childAgentKindLabel(st *state, kind string) string {
	if kind == contracts.ChildAgentTeammate {
		return st.tr("队友", "Teammate")
	}
	return st.tr("子智能体", "Subagent")
}

// childAgentModeLabel localizes the follow-up capability.
func childAgentModeLabel(st *state, mode string) string {
	if mode == contracts.ChildAgentContinuable {
		return st.tr("可继续", "Continuable")
	}
	return st.tr("一次性", "One-shot")
}

// childAgentActivityLine is the switcher row's latest-activity line: the newest
// timeline entry, else the latest output, else the prompt, else the empty state.
func childAgentActivityLine(st *state, agent contracts.ChildAgent) string {
	if len(agent.Activity) > 0 {
		if text := strings.TrimSpace(agent.Activity[len(agent.Activity)-1].Text); text != "" {
			return text
		}
	}
	if text := strings.TrimSpace(agent.Output); text != "" {
		return truncateRunes(text, 96)
	}
	if text := strings.TrimSpace(agent.Prompt); text != "" {
		return truncateRunes(text, 96)
	}
	return st.tr("还没有活动", "No activity yet")
}

// openChildView switches the main content area to one child conversation and
// closes the opening overlay.
func (st *state) openChildView(childID string) {
	childID = strings.TrimSpace(childID)
	if childID == "" {
		return
	}
	st.agents.childID = childID
	st.agents.crumbChosen = -1
	st.agents.teamOpen = false
	st.agents.childOpen = false
	st.agents.jobsOpen = false
	st.agents.childReply = ""
	st.agents.childReplyFor = ""
	st.agents.childSending = false
	st.agents.childSendErr = ""
	st.agents.childSendSeq++
	st.agents.childDraft = ""
	st.agents.childFocus = true
	st.agents.childScroll = ui.ScrollState{}
	st.runtime.Invalidate()
}

// exitChildView returns the main content area to the parent conversation. The
// transient overlays and the in-flight send belong to the child visit: leaving
// closes them, so a later re-entry starts clean.
func (st *state) exitChildView() {
	st.agents.childID = ""
	st.agents.crumbChosen = -1
	st.agents.childOpen = false
	st.agents.teamOpen = false
	st.agents.jobsOpen = false
	st.agents.workbenchOpen = false
	st.agents.childSending = false
	st.agents.childSendErr = ""
	st.agents.childSendSeq++
	st.runtime.Invalidate()
}

// --- background jobs --------------------------------------------------------

// buildJobsEntry is the header's background-jobs chip: the retained live-job
// count and the conversation's durable execution history (the records that
// survive a restart) open the native jobs overlay. It renders nothing while the
// session has neither: the chip is a notification of real work, not a permanent
// zero badge that squeezes the title (R15).
func buildJobsEntry(c *ui.Context, t *design.Tokens, st *state, sessionID string,
	history []contracts.ExecutionRecord, compact bool) {
	jobs := st.jobService()
	var list []contracts.BackgroundJob
	if jobs != nil {
		list = jobs.Jobs()
	}
	// The live rows already show a job, so the history group never repeats one.
	visibleHistory := visibleExecutionHistory(history, list)
	if len(list) == 0 && len(visibleHistory) == 0 {
		return
	}
	running := 0
	for _, job := range list {
		if job.Running {
			running++
		}
	}
	if running > 0 {
		// The job service has no change notification: schedule the next frame
		// while any job runs so the chip/overlay pick a completion up without
		// another user interaction (each frame schedules the next poll).
		c.After(jobsPollInterval)
	}
	label := st.tr("后台任务", "Background jobs")
	tooltipParts := make([]string, 0, 2)
	if len(list) > 0 {
		label = fmt.Sprintf(st.tr("%d 个后台任务", "%d background tasks"), len(list))
		tooltipParts = append(tooltipParts, fmt.Sprintf(
			st.tr("%d 个任务 · %d 个运行中", "%d jobs · %d running"), len(list), running))
	} else {
		tooltipParts = append(tooltipParts, st.tr("没有运行中的后台任务",
			"No background job is running"))
	}
	if len(visibleHistory) > 0 {
		tooltipParts = append(tooltipParts, fmt.Sprintf(
			st.tr("历史 %d 条", "%d in history"), len(visibleHistory)))
	}
	chip := agentEntryButton(c, t, label, strings.Join(tooltipParts, " · "), compact)
	chip.Children(func() {
		ui.Icon(c, icons.Clock()).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
		agentEntryText(c, t, label)
	})
	if chip.Clicked() {
		st.agents.jobsOpen = !st.agents.jobsOpen
		if st.agents.jobsOpen {
			st.openExecutionHistory(sessionID)
		} else {
			st.agents.jobStopArmed = ""
		}
		st.runtime.Invalidate()
	}
	if !st.agents.jobsOpen {
		st.reportOverlay("jobs", false)
		return
	}
	ui.PopoverBase(c, chip, &st.agents.jobsOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthCompact)
		buildJobsPanel(c, t, st, jobs, list, running, visibleHistory)
	})
	st.reportOverlay("jobs", st.agents.jobsOpen)
}

// buildJobsPanel is the jobs overlay: status, command, duration and exit code
// per job, with the stop action of a running job, then the conversation's
// durable execution history (lifecycle only; the output is not persisted).
func buildJobsPanel(c *ui.Context, t *design.Tokens, st *state, jobs contracts.JobService,
	list []contracts.BackgroundJob, running int, history []contracts.ExecutionRecord) {
	// An arm older than the confirmation window (or whose job ended) resets
	// before the rows build, so the row shows 停止 again and Enter does nothing.
	st.expireJobStopArm(c, list)
	agentPanelHeader(c, t, icons.Clock(), st.tr("后台任务", "Background jobs"),
		fmt.Sprintf(st.tr("%d 个任务 · %d 个运行中", "%d jobs · %d running"), len(list), running))
	if len(list) == 0 && len(history) == 0 {
		design.Apply(ui.Text(c, st.tr("暂无后台任务。bash run_in_background 启动的后台任务会出现在这里。",
			"No background jobs yet. Background jobs start with bash run_in_background.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(3)
		return
	}
	if st.agents.jobErr != "" {
		uikit.ErrorLine(c, st.agents.jobErr)
	}
	st.buildStalePermissionHint(c, t, jobs)
	ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).
		TrackScroll(&st.agents.jobsScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
			if len(list) > 0 {
				st.agents.jobsList.Key = func(index int) any {
					if index < 0 || index >= len(list) {
						return nil
					}
					return list[index].ID
				}
				st.agents.jobsList.Label = func(index int) string {
					if index < 0 || index >= len(list) {
						return ""
					}
					return backgroundJobCommand(list[index], st)
				}
				ui.List(c, &st.agents.jobsList, len(list), func(index int) {
					buildBackgroundJobRow(c, t, st, jobs, list[index])
				}).FillWidth().Gap(design.Space2)
			} else {
				design.Apply(ui.Text(c, st.tr("当前没有运行中的后台任务。",
					"No background job is running right now.")),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
			}
			buildExecutionHistoryGroup(c, t, st, history)
		})
	})
	// The stop warning is only meaningful while something is running: an idle
	// overlay (history only) keeps just the close hint, not a stop promise about
	// processes that no longer exist (2026-11 UI review P3).
	if running > 0 {
		agentHint(c, t, st.tr("停止会终止任务的进程 · Esc 关闭",
			"Stopping ends the job's process · Esc closes"))
	} else {
		agentHint(c, t, st.tr("Esc 关闭", "Esc closes"))
	}
}

// stalePermissionJobs reads the optional JobPermissionReporter half of the jobs
// service (2026-11 audit RG-09): the running jobs still following an older
// permission identity. A backend without the reporter returns nil.
func stalePermissionJobs(jobs contracts.JobService) []contracts.BackgroundJob {
	reporter, ok := jobs.(contracts.JobPermissionReporter)
	if !ok || reporter == nil {
		return nil
	}
	return reporter.StalePermissionJobs()
}

// buildStalePermissionHint shows the one-line permission-downgrade awareness row
// when running jobs still follow an older permission identity: OS confinement is
// applied at spawn time and is not retroactive, so these jobs keep the old wall
// until they finish — stopping them is the way to run under the new permission
// (the KillJob route is fixed, 2026-11 audit RG-01/(c)). The bulk stop uses the
// same two-stage confirmation as a single job row's stop.
func (st *state) buildStalePermissionHint(c *ui.Context, t *design.Tokens, jobs contracts.JobService) {
	stale := stalePermissionJobs(jobs)
	// Reset a bulk arm that timed out or whose jobs all ended, so a stray second
	// press can never stop a later process.
	if st.agents.jobStaleArmed &&
		(c.Now().Sub(st.agents.jobStaleAt) >= jobStopConfirmWindow || len(stale) == 0) {
		st.agents.jobStaleArmed = false
	}
	if len(stale) == 0 {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		uikit.StatusText(c, t, design.TypeXXS, uikit.StatusNeedsAction,
			fmt.Sprintf(st.tr("%d 个运行中任务仍按旧权限运行", "%d running jobs still follow the old permission"), len(stale))).
			Grow(1).MaxLines(2).Tooltip(st.tr(
			"这些任务在权限变更前启动。OS 沙箱约束在启动时施加、不会追溯变更，它们会保持旧权限直到结束；停止后可按新权限重新启动。",
			"These jobs started before the permission change. OS confinement is applied at spawn time and is not retroactive; they keep the old permission until they finish. Stop them to restart under the new permission."))
		stopText := st.tr("停止这些任务", "Stop these jobs")
		stopLabel := st.tr("停止仍按旧权限运行的任务", "Stop the jobs still following the old permission")
		tooltip := st.tr("停止会终止这些任务的进程，无法撤销；再按一次确认",
			"Stopping ends these jobs' processes and cannot be undone; press again to confirm")
		switch {
		case st.agents.jobStaleArmed:
			stopText = st.tr("确认停止", "Confirm stop")
			stopLabel = st.tr("确认停止仍按旧权限运行的任务", "Confirm stop of the jobs still following the old permission")
			tooltip = st.tr("再按一次以终止这些任务（3 秒内有效，超时自动取消）",
				"Press again to end these jobs (valid for 3 seconds, then it resets)")
		case st.agents.jobKilling != "":
			stopText = st.tr("停止中…", "Stopping…")
		}
		stop := ui.Button(c, stopText).Label(stopLabel).Tooltip(tooltip)
		// One width for all three states (停止这些任务 / 确认停止 / 停止中…): the
		// widest label's measure, so arming and settling never move the row's
		// other controls sideways (visual audit: the button grew 44→74 DIP).
		design.Apply(stop, t.DangerButton(), t.Font(design.TypeXXS)).
			Height(agentRowActionHeight).PaddingX(design.Space2).Shrink(0).
			MinWidth(design.Space8 * 3)
		stop.Disabled(st.agents.jobKilling != "")
		if stop.Clicked() {
			if st.agents.jobStaleArmed {
				st.agents.jobStaleArmed = false
				st.killStalePermissionJobs(jobs, stale)
			} else {
				st.agents.jobStaleArmed = true
				st.agents.jobStaleAt = c.Now()
				c.After(jobStopConfirmWindow)
				st.runtime.Invalidate()
			}
		}
	})
}

// killStalePermissionJobs stops every running job that still follows the older
// permission identity in one guarded background pass (the same async shape as a
// single killBackgroundJob), reporting the first failure through the panel's
// error line.
func (st *state) killStalePermissionJobs(jobs contracts.JobService, stale []contracts.BackgroundJob) {
	if jobs == nil || len(stale) == 0 || st.agents.jobKilling != "" {
		return
	}
	ids := make([]string, 0, len(stale))
	for _, job := range stale {
		if id := strings.TrimSpace(job.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	st.agents.jobKilling = ids[0]
	st.agents.jobStopArmed = ""
	st.agents.jobErr = ""
	runtime := st.runtime
	go func() {
		var firstErr error
		for _, id := range ids {
			if err := jobs.KillJob(id); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		runtime.Post(func() {
			st.agents.jobKilling = ""
			if firstErr != nil {
				st.agents.jobErr = st.tr("停止失败：", "Stop failed: ") + firstErr.Error()
			}
			runtime.Invalidate()
		})
	}()
}

// backgroundJobStatus maps the rig job state onto the product's status
// vocabulary: an exited job is a success when its exit code is 0, and every
// other non-running terminal state is a failure (killed jobs are cancelled).
func backgroundJobStatus(st *state, job contracts.BackgroundJob) (uikit.StatusKind, string) {
	switch strings.ToLower(strings.TrimSpace(job.State)) {
	case "running":
		return uikit.StatusRunning, st.tr("运行中", "Running")
	case "killed":
		return uikit.StatusCancelled, st.tr("已取消", "Cancelled")
	case "timed_out":
		return uikit.StatusError, st.tr("超时", "Timed out")
	case "failed":
		return uikit.StatusError, st.tr("失败", "Failed")
	case "exited":
		if job.ExitCode != 0 {
			return uikit.StatusError, st.tr("失败", "Failed")
		}
		return uikit.StatusSuccess, st.tr("完成", "Completed")
	default:
		label := strings.TrimSpace(job.State)
		if label == "" {
			label = st.tr("未知", "Unknown")
		}
		return uikit.StatusUnknown, label
	}
}

// backgroundJobCommand is the row's readable command (the manager's free-text
// command line), falling back to the job id.
func backgroundJobCommand(job contracts.BackgroundJob, st *state) string {
	command := strings.TrimSpace(job.Command)
	if command == "" {
		return job.ID
	}
	return command
}

// backgroundJobExitLabel is the exit-code column: the code of a finished job,
// an honest dash while it still runs.
func backgroundJobExitLabel(job contracts.BackgroundJob) string {
	// A running job has no exit code yet; a killed/timed-out process reports a
	// negative sentinel from the manager, which is also not an exit code.
	if job.Running || job.ExitCode < 0 {
		return "—"
	}
	return fmt.Sprintf("%d", job.ExitCode)
}

// buildBackgroundJobRow is one job row: status badge, command, duration, exit
// code, the output panel toggle and the two-stage stop of a running job. The
// expanded panel reads the job's bounded output through JobService.JobOutput
// (off the UI thread) and refreshes it while the job runs.
func buildBackgroundJobRow(c *ui.Context, t *design.Tokens, st *state,
	jobs contracts.JobService, job contracts.BackgroundJob) {
	kind, label := backgroundJobStatus(st, job)
	panel := st.jobOutputPanelFor(job.ID)
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
			Padding(design.Space2, design.Space3)
		row.Children(func() {
			uikit.StatusBadge(c, t, kind, label)
			command := backgroundJobCommand(job, st)
			design.Apply(ui.Text(c, command), t.MonoText(design.TypeXXS)).
				TextColor(t.LabelSecondary).Grow(1).Shrink(1).MinWidthPercent(0).
				MaxLines(1).Ellipsis("…").Tooltip(command)
			if job.Duration > 0 {
				design.Apply(ui.Text(c, st.tr("用时 ", "Took ")+formatUsageDuration(job.Duration.Milliseconds())),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
			}
			design.Apply(ui.Text(c, st.tr("退出码 ", "Exit ")+backgroundJobExitLabel(job)),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
			outputText := st.tr("输出", "Output")
			outputLabel := st.tr("查看任务输出 ", "Show task output ") + job.ID
			if panel.open {
				outputText = st.tr("收起", "Hide")
				outputLabel = st.tr("收起任务输出 ", "Hide task output ") + job.ID
			}
			agentRowActionTooltip(c, t, outputText, outputLabel, st.tr(
				"展开该任务的输出（等宽文本、可滚动、可复制）",
				"Show this task's output (mono text, scrollable, copyable)"),
				func() {
					panel.open = !panel.open
					st.runtime.Invalidate()
				})
			if job.Running {
				armed := st.agents.jobStopArmed == job.ID
				stopText := st.tr("停止", "Stop")
				stopLabel := st.tr("停止后台任务 ", "Stop background task ") + job.ID
				tooltip := st.tr("停止会终止该任务的进程，无法撤销；再按一次确认",
					"Stopping ends the job's process and cannot be undone; press again to confirm")
				switch {
				case armed:
					stopText = st.tr("确认停止", "Confirm stop")
					stopLabel = st.tr("确认停止后台任务 ", "Confirm stop of background task ") + job.ID
					tooltip = st.tr("再按一次以终止该任务（3 秒内有效，超时自动取消）",
						"Press again to end this task (valid for 3 seconds, then it resets)")
				case st.agents.jobKilling == job.ID:
					stopText = st.tr("停止中…", "Stopping…")
				}
				// Stopping a background job terminates its process: the same danger
				// semantic as the composer's stop, not a neutral secondary (R6/A6).
				stop := ui.Button(c, stopText).Label(stopLabel).Tooltip(tooltip)
				design.Apply(stop, t.DangerButton(), t.Font(design.TypeXXS)).
					Height(agentRowActionHeight).PaddingX(design.Space2).Shrink(0)
				stop.Disabled(st.agents.jobKilling != "")
				if stop.Clicked() {
					if armed {
						// The second press within the window stops the job.
						st.agents.jobStopArmed = ""
						st.killBackgroundJob(jobs, job.ID)
					} else {
						st.armJobStop(c, job.ID)
					}
				}
			}
		})
		if panel.open {
			st.buildJobOutputPanel(c, t, job, panel)
		}
	})
	if panel.open && jobs != nil {
		// Guarded async read: at most one per job in flight, at most one per
		// poll interval while the job runs (a finished job is read once).
		st.ensureJobOutput(c, jobs, job, panel)
	}
}

// jobOutputPanelFor returns (creating on first use) one job row's output panel
// state.
func (st *state) jobOutputPanelFor(jobID string) *jobOutputPanel {
	if st.agents.jobOutputs == nil {
		st.agents.jobOutputs = make(map[string]*jobOutputPanel)
	}
	panel := st.agents.jobOutputs[jobID]
	if panel == nil {
		panel = &jobOutputPanel{}
		st.agents.jobOutputs[jobID] = panel
	}
	return panel
}

// armJobStop starts the two-stage stop: the row shows 确认停止 and this frame
// schedules the reset frame after the confirmation window.
func (st *state) armJobStop(c *ui.Context, jobID string) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return
	}
	st.agents.jobStopArmed = jobID
	st.agents.jobStopAt = c.Now()
	c.After(jobStopConfirmWindow)
	st.runtime.Invalidate()
}

// expireJobStopArm clears an arm that timed out or whose job is no longer
// running, so an old confirmation can never stop a later process.
func (st *state) expireJobStopArm(c *ui.Context, list []contracts.BackgroundJob) {
	armed := st.agents.jobStopArmed
	if armed == "" {
		return
	}
	if c.Now().Sub(st.agents.jobStopAt) >= jobStopConfirmWindow {
		st.agents.jobStopArmed = ""
		return
	}
	for _, job := range list {
		if job.ID == armed && job.Running {
			return
		}
	}
	st.agents.jobStopArmed = ""
}

// ensureJobOutput starts one bounded output read off the UI thread when the
// panel needs it: the first read, then one per poll interval while the job
// runs. The frame never waits for it; the result lands through UIRuntime.Post.
func (st *state) ensureJobOutput(c *ui.Context, jobs contracts.JobService, job contracts.BackgroundJob, panel *jobOutputPanel) {
	if jobs == nil || panel == nil || !panel.open || panel.busy {
		return
	}
	now := c.Now()
	if panel.loaded {
		if !job.Running && !panel.running {
			// A finished job's output no longer changes.
			return
		}
		if job.Running && now.Sub(panel.at) < jobsPollInterval {
			return
		}
	}
	panel.busy = true
	panel.seq++
	sequence := panel.seq
	panel.at = now
	panel.running = job.Running
	runtime := st.runtime
	jobID := job.ID
	go func() {
		text, err := jobs.JobOutput(jobID)
		runtime.Post(func() {
			panel.busy = false
			if panel.seq != sequence {
				// A newer read superseded this one.
				return
			}
			// A failed read counts as loaded: the error shows, and a live job
			// retries at the poll interval instead of every frame.
			panel.loaded = true
			if err != nil {
				panel.err = st.tr("读取输出失败：", "Failed to read the output: ") + st.jobOutputErrorText(err)
			} else {
				panel.err = ""
				panel.text = text
			}
			runtime.Invalidate()
		})
	}()
}

// buildJobOutputPanel draws one expanded output panel: the honest states
// (loading, failure, empty, truncated, stopped), the command/output copy
// actions and the bounded mono body.
func (st *state) buildJobOutputPanel(c *ui.Context, t *design.Tokens,
	job contracts.BackgroundJob, panel *jobOutputPanel) {
	if panel.err != "" {
		uikit.ErrorLine(c, panel.err)
		return
	}
	if !panel.loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Spinner(c).Label(st.tr("读取输出", "Loading output")).
				Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, st.tr("读取输出…", "Loading output…")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		})
		return
	}
	text := panel.text
	truncated := strings.HasPrefix(text, contracts.JobOutputTruncatedMarker)
	if truncated {
		text = strings.TrimPrefix(text, contracts.JobOutputTruncatedMarker)
	}
	if truncated {
		agentHint(c, t, st.tr("输出已截断，仅显示最近部分。",
			"Output truncated: only the recent part is shown."))
	}
	if stoppedJobOutput(job) {
		agentHint(c, t, st.tr("任务已停止，以下为停止前的输出。",
			"The task was stopped; this is its output before the stop."))
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Spacer(c)
		agentRowActionTooltip(c, t, st.tr("复制命令", "Copy command"),
			st.tr("复制任务命令 ", "Copy task command ")+job.ID,
			st.tr("复制该任务的命令行", "Copy this task's command line"),
			func() { c.WriteClipboard(backgroundJobCommand(job, st)) })
		agentRowActionTooltip(c, t, st.tr("复制输出", "Copy output"),
			st.tr("复制任务输出 ", "Copy task output ")+job.ID,
			st.tr("复制当前显示的任务输出", "Copy the task output shown here"),
			func() { c.WriteClipboard(text) })
	})
	if strings.TrimSpace(text) == "" {
		design.Apply(ui.Text(c, st.tr("暂无输出", "No output yet")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		return
	}
	scroll := ui.ScrollBoth(c).Height(jobOutputPanelHeight).FillWidth()
	scroll.TrackScroll(&panel.scroll)
	design.Apply(scroll, func(el ui.Element) ui.Element {
		return el.Background(t.BgBase).Border(1, t.BorderL2).
			Radius(design.RadiusSM).Padding(design.Space2)
	}).Children(func() {
		design.Apply(ui.Text(c, text), t.MonoText(design.TypeXXS)).
			TextColor(t.LabelSecondary).NoWrap().Selectable()
	})
}

// stoppedJobOutput reports whether a job's output is final because the job was
// stopped (killed) or timed out: the panel says so instead of letting the user
// wait for more.
func stoppedJobOutput(job contracts.BackgroundJob) bool {
	if job.Running {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(job.State)) {
	case "killed", "timed_out", "interrupted":
		return true
	}
	return false
}

// jobOutputErrorText localizes the JobService read errors; unknown failures
// keep their diagnostic after the caller's localized prefix.
func (st *state) jobOutputErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, contracts.ErrJobNotFound):
		return st.tr("该后台任务已不存在。", "The background task no longer exists.")
	case errors.Is(err, contracts.ErrJobUnavailable):
		return st.tr("后台任务不可用。", "Background jobs are unavailable.")
	default:
		return err.Error()
	}
}

// killBackgroundJob stops one job off the UI thread (the job tool waits a
// grace period for the process to be reaped) and publishes the result at the
// next frame.
func (st *state) killBackgroundJob(jobs contracts.JobService, jobID string) {
	if jobs == nil || st.agents.jobKilling != "" {
		return
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return
	}
	st.agents.jobKilling = jobID
	st.agents.jobStopArmed = ""
	st.agents.jobErr = ""
	runtime := st.runtime
	go func() {
		err := jobs.KillJob(jobID)
		runtime.Post(func() {
			st.agents.jobKilling = ""
			if err != nil {
				st.agents.jobErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
			}
			runtime.Invalidate()
		})
	}()
}

// --- child conversation view ------------------------------------------------

// buildChildView renders the main content area's child conversation: the prompt,
// the run state, the output and the activity of one subagent or teammate. A
// continuable teammate gets the message input that calls SendChildAgent; a
// one-shot subagent stays read-only. The header shows the parent > child
// breadcrumb instead of the title.
func buildChildView(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime, st *state, cfg Config) {
	children := st.childAgentService()
	var child contracts.ChildAgent
	found := false
	if children != nil && strings.TrimSpace(st.agents.childID) != "" {
		child, found = children.ChildAgent(st.agents.childID)
	}
	if !found {
		buildMissingChildView(c, t, st)
		return
	}
	switch st.tab {
	case tabTrajectory:
		buildChildActivityPane(c, t, st, child)
	case tabMemory:
		buildChildMemoryPane(c, t, st, child)
	default:
		buildChildConversationPane(c, t, runtime, st, cfg, child)
	}
}

// buildMissingChildView is the honest empty state of a child that disappeared
// under the view (a closed teammate, a backend restart): the breadcrumb and a
// labeled return action stay.
func buildMissingChildView(c *ui.Context, t *design.Tokens, st *state) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Row(c).FillWidth().Grow(1).Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
				Gap(design.Space3).PaddingY(design.Space8).AlignItems(ui.Start).Children(func() {
				uikit.EmptyState(c, st.tr("子智能体已不存在", "This child agent no longer exists"))
				if button := design.Apply(ui.Button(c, st.tr("返回主会话", "Back to the conversation")),
					t.SecondaryButton()); button.Clicked() {
					st.exitChildView()
				}
			})
		})
	})
}

// buildChildConversationPane is the child's 对话 tab: the prompt, the run state,
// the output (or the failure), the activity timeline, and — for a continuable
// teammate — the send box.
func buildChildConversationPane(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime,
	st *state, cfg Config, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&st.agents.childScroll).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
					Gap(design.Space4).PaddingY(design.Space5).Children(func() {
					buildChildHeader(c, t, st, child)
					if prompt := strings.TrimSpace(child.Prompt); prompt != "" {
						buildChildTextSection(c, t, st.tr("任务", "Prompt"), prompt)
					}
					buildChildOutput(c, t, st, child)
					buildChildActivitySection(c, t, st, child)
					switch {
					case child.Historical && child.Resumable:
						agentHint(c, t, st.tr("历史记录（应用重启后仍保留）：可继续；发送后会接着已保存的会话继续。",
							"Finished record (kept after a restart): continuable; sending picks the saved conversation back up."))
					case child.Historical:
						agentHint(c, t, st.tr("已结束的记录（应用重启后仍保留）：只读，不能再继续对话。",
							"Finished record (kept after a restart): read-only, it cannot be continued."))
					case child.Mode != contracts.ChildAgentContinuable:
						agentHint(c, t, st.tr("一次性子智能体：只读会话，不能继续对话。",
							"One-shot subagent: read-only, it cannot be continued."))
					}
				})
			})
		})
		// A live continuable teammate and a resumable historical record both take
		// a follow-up message through the same ChildAgentService call.
		if (child.Mode == contracts.ChildAgentContinuable && !child.Historical) ||
			(child.Historical && child.Resumable) {
			buildChildComposer(c, t, runtime, st, child)
		}
	})
}

// buildChildHeader is the child view's status line: the truthful run state, the
// agent family, the model, the usage and the elapsed time.
func buildChildHeader(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	// The raw child ID (member-1) is an internal key, not user copy: it moves
	// into the quiet line's tooltip as the session ID instead of occupying its
	// own row under the status (R6).
	sessionID := ""
	if strings.TrimSpace(child.ID) != "" {
		sessionID = st.tr("会话 ID：", "Session ID: ") + strings.TrimSpace(child.ID)
	}
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			statusKind, statusText := childAgentStatusPhrase(st, child)
			uikit.StatusBadge(c, t, statusKind, statusText)
			meta := design.Apply(ui.Text(c, childAgentMeta(st, child)), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
			if sessionID != "" {
				meta.Tooltip(sessionID)
			}
			ui.Spacer(c)
			if summary := childAgentUsageSummary(st, child, c.Now()); summary != "" {
				design.Apply(ui.Text(c, summary), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
			st.buildChildStopAction(c, t, child)
		})
		if st.agents.teamErr != "" {
			uikit.ErrorLine(c, st.agents.teamErr)
		}
	})
}

// childAgentUsageSummary is the child header's compact accounting: the elapsed
// run (or the finished duration), the iterations and the accumulated tokens.
func childAgentUsageSummary(st *state, child contracts.ChildAgent, now time.Time) string {
	var parts []string
	if !child.Started.IsZero() {
		end := child.Finished
		if end.IsZero() {
			end = now
		}
		if duration := end.Sub(child.Started); duration > 0 {
			parts = append(parts, st.tr("用时 ", "Took ")+formatUsageDuration(duration.Milliseconds()))
		}
	}
	if child.Iterations > 0 {
		parts = append(parts, fmt.Sprintf(st.tr("%d 步", "%d iterations"), child.Iterations))
	}
	if child.UsageTokens > 0 {
		parts = append(parts, uikit.FormatCompactCount(child.UsageTokens)+" tokens")
	}
	return strings.Join(parts, " · ")
}

// buildChildTextSection renders one titled plain-text block (the prompt and the
// latest reply).
func buildChildTextSection(c *ui.Context, t *design.Tokens, title, body string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		agentSectionLabel(c, t, title)
		design.Apply(ui.Text(c, body), t.Font(design.TypeBody)).
			TextColor(t.LabelPrimary).Selectable()
	})
}

// buildChildOutput renders the child's latest answer as Markdown (the models
// answer in Markdown), the failure as the shared error line, or the honest
// running/empty state.
func buildChildOutput(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	if strings.TrimSpace(child.Error) != "" {
		agentSectionLabel(c, t, st.tr("错误", "Error"))
		uikit.ErrorLine(c, child.Error)
		return
	}
	output := strings.TrimSpace(child.Output)
	if output == "" {
		message := st.tr("还没有输出", "No output yet")
		if strings.EqualFold(strings.TrimSpace(child.Status), contracts.ChildAgentRunning) {
			message = st.tr("正在运行…", "Running…")
		}
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			agentSectionLabel(c, t, st.tr("输出", "Output"))
			uikit.EmptyState(c, message)
		})
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		agentSectionLabel(c, t, st.tr("输出", "Output"))
		uikit.MarkdownReply(c, output, st.markdownOptions(t))
	})
	if st.agents.childReplyFor == child.ID && strings.TrimSpace(st.agents.childReply) != "" {
		buildChildTextSection(c, t, st.tr("最新回复", "Latest reply"), st.agents.childReply)
	}
}

// buildChildActivitySection is the child's bounded activity timeline. A
// one-shot subagent's registry records no timeline, and that stays explicit
// instead of inventing entries.
func buildChildActivitySection(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		agentSectionLabel(c, t, st.tr("活动", "Activity"))
		if len(child.Activity) == 0 {
			uikit.EmptyState(c, st.tr("还没有活动记录", "No activity recorded yet"))
			return
		}
		st.agents.activityList.Key = func(index int) any {
			if index < 0 || index >= len(child.Activity) {
				return nil
			}
			entry := child.Activity[index]
			// Restored activity rows carry no ID: fall back to the row position
			// and its clock so two entries never share one element state.
			if id := strings.TrimSpace(entry.ID); id != "" {
				return id
			}
			return fmt.Sprintf("%d-%d", index, entry.Time.UnixNano())
		}
		st.agents.activityList.Label = func(index int) string {
			if index < 0 || index >= len(child.Activity) {
				return ""
			}
			return child.Activity[index].Text
		}
		activity := child.Activity
		ui.List(c, &st.agents.activityList, len(activity), func(index int) {
			buildChildActivityRow(c, t, st, activity[index])
		}).FillWidth()
	})
}

// buildChildActivityRow is one timeline row: the kind, the text and the clock.
// A kind word the entry's own text already opens with is dropped instead of
// duplicating it: a finish entry reads "「研究员」已完成：…" rather than
// "完成 「研究员」已完成：…" (R6), and a close entry reads 关闭「队友」 rather
// than 关闭 关闭「队友」. The body uses the 12 metadata step because it is a
// sentence, not a micro label.
func buildChildActivityRow(c *ui.Context, t *design.Tokens, st *state, entry contracts.ChildAgentActivity) {
	color := t.LabelSecondary
	if entry.Error {
		color = t.DangerText
	}
	showKind := true
	kindLabel := childActivityKindLabel(st, entry.Kind)
	switch {
	case entry.Kind == contracts.TeamActivityFinish && strings.Contains(entry.Text, "已完成"):
		showKind = false
	case strings.HasPrefix(strings.TrimSpace(entry.Text), kindLabel):
		// The backend's text already opens with the kind word (a close entry
		// reads 关闭「队友」), so printing the kind again would read
		// 关闭 关闭「队友」.
		showKind = false
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space1).Children(func() {
		design.Apply(ui.Text(c, messageClock(st.locale, entry.Time)),
			t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		if showKind {
			design.Apply(ui.Text(c, kindLabel),
				t.Font(design.TypeXXS)).FontWeight(agentHeadingWeight).TextColor(color).NoWrap()
		}
		design.Apply(ui.Text(c, entry.Text), t.Font(design.TypeXXS)).
			TextColor(color).Grow(1).Shrink(1).MinWidthPercent(0).MaxLines(2).
			Tooltip(entry.Text)
	})
}

// childActivityKindLabel localizes the team activity kinds shown in the child
// timeline.
func childActivityKindLabel(st *state, kind string) string {
	switch kind {
	case contracts.TeamActivitySpawn:
		return st.tr("派发", "Spawn")
	case contracts.TeamActivitySend:
		return st.tr("发送", "Send")
	case contracts.TeamActivityWait:
		return st.tr("等待", "Wait")
	case contracts.TeamActivityFinish:
		return st.tr("完成", "Finish")
	case contracts.TeamActivityError:
		return st.tr("失败", "Error")
	case contracts.TeamActivityInterrupt:
		return st.tr("中断", "Interrupt")
	case contracts.TeamActivityClose:
		return st.tr("关闭", "Close")
	default:
		return st.tr("工具", "Tool")
	}
}

// buildChildActivityPane is the child's 轨迹 tab: the activity timeline alone.
func buildChildActivityPane(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&st.agents.childScroll).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
					Gap(design.Space3).PaddingY(design.Space5).Children(func() {
					buildChildHeader(c, t, st, child)
					buildChildActivitySection(c, t, st, child)
				})
			})
		})
	})
}

// buildChildMemoryPane is the child's 记忆 tab: a child conversation has no
// memory surface, and saying so is honest.
func buildChildMemoryPane(c *ui.Context, t *design.Tokens, st *state, child contracts.ChildAgent) {
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).
			Gap(design.Space2).PaddingY(design.Space8).Children(func() {
			agentSectionLabel(c, t, st.tr("记忆", "Memory"))
			uikit.EmptyState(c, st.tr("子智能体会话没有记忆面板。",
				"Child conversations have no memory panel."))
		})
	})
}

// buildChildComposer is the continuable teammate's message box. It is the
// conversation's own composer shell — the same card, the same 32 DIP send control
// and the same toolbar slot — with only what a child needs: its draft, its
// placeholder and the answer note. A bare text field with a small button read as
// a different product from the parent composer right above it (p2-team-child
// audit).
func buildChildComposer(c *ui.Context, t *design.Tokens, runtime uinative.UIRuntime,
	st *state, child contracts.ChildAgent) {
	children := st.childAgentService()
	if children == nil {
		return
	}
	name := agentDisplayName(child.Name, child.ID)
	placeholder := st.tr("发消息给该队友…", "Message this teammate…")
	if child.Historical {
		if child.Kind == contracts.ChildAgentTeammate {
			placeholder = st.tr("继续该队友…", "Continue this teammate…")
		} else {
			placeholder = st.tr("继续该子代理…", "Continue this subagent…")
		}
	}
	sending := st.agents.childSending
	var errs []string
	if st.agents.childSendErr != "" {
		errs = []string{st.agents.childSendErr}
	}
	uikit.ComposerShell(c, uikit.ComposerShellOptions{
		Key:         "session.child.composer",
		Draft:       &st.agents.childDraft,
		Placeholder: placeholder,
		Locale:      st.locale,
		Focus:       &st.agents.childFocus,
		CardLabel:   st.tr("发送给 ", "Send to ") + name,
		// The input's name is the card's own, unique per child, and it does not
		// move when the placeholder switches between 继续该队友… and 继续该子代理…:
		// the placeholder stays visual copy (QA: two child names collided because
		// the accessible name was the placeholder).
		InputLabel:      st.tr("发送给 ", "Send to ") + name,
		SendLabel:       st.tr("发送", "Send"),
		SendEnabled:     strings.TrimSpace(st.agents.childDraft) != "" && !sending,
		SendDescription: st.tr("发送消息给 ", "Send message to ") + name,
		Send: func() bool {
			if sending {
				return false
			}
			st.sendChildMessage(children, child.ID)
			return true
		},
		Errors: errs,
		Hint: func(*ui.Context) string {
			if sending {
				return st.tr("已发送，等待该队友的回复。", "Sent; waiting for the teammate's reply.")
			}
			if child.Historical {
				return st.tr("发送后会恢复这条历史记录的会话，并等待它的回答。",
					"Sending resumes this record's conversation and waits for its answer.")
			}
			return st.tr("发送后等待该队友的回复。", "The teammate answers when its next run finishes.")
		},
	})
}

// sendChildMessage delivers one follow-up message to a continuable child off
// the UI thread (the backend waits for the teammate's answer) and posts the
// result back for the frame.
func (st *state) sendChildMessage(children contracts.ChildAgentService, childID string) {
	message := strings.TrimSpace(st.agents.childDraft)
	if children == nil || message == "" || st.agents.childSending {
		return
	}
	st.agents.childSending = true
	st.agents.childSendErr = ""
	st.agents.childDraft = ""
	st.agents.childSendSeq++
	sequence := st.agents.childSendSeq
	runtime := st.runtime
	go func() {
		answer, err := children.SendChildAgent(context.Background(), childID, message)
		runtime.Post(func() {
			// A send from a visit the user already left must not touch the
			// current view (it may be another child or the same one reopened).
			if sequence != st.agents.childSendSeq {
				return
			}
			st.agents.childSending = false
			if err != nil {
				st.agents.childSendErr = st.tr("发送失败：", "Send failed: ") + st.childSendErrorText(err)
			} else {
				st.agents.childReply = strings.TrimSpace(answer)
				st.agents.childReplyFor = childID
			}
			st.agents.childFocus = true
			runtime.Invalidate()
		})
	}()
}

// childSendErrorText localizes the backend's cold-resume failures; unknown
// errors keep their message (the caller adds the localized prefix).
func (st *state) childSendErrorText(err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "already running"):
		return st.tr("该队友正在运行，请等本次运行结束后再发送。",
			"This teammate is already running; wait for the current run to finish.")
	case strings.Contains(lower, "cannot be resumed"), strings.Contains(lower, "no durable session id"):
		return st.tr("这条历史记录无法继续：缺少可恢复的会话数据。",
			"This finished record cannot be continued: no resumable session data.")
	case strings.Contains(lower, "empty answer"):
		return st.tr("该队友本轮没有返回答案，请重试。",
			"This teammate returned no answer this time; try again.")
	default:
		return raw
	}
}

// --- child breadcrumb -------------------------------------------------------

// buildChildBreadcrumb is the header's title slot in the child view: the parent
// conversation title as a native breadcrumb link, the child's name as the
// current page, and a switcher entry that reopens the child list. Clicking the
// parent crumb returns to the conversation (the DSH lineage contract).
func buildChildBreadcrumb(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService, cfg Config) {
	parent := sessionTitle(st, chat, cfg)
	childName := childAgentDisplayName(st, st.agents.childID)
	crumbs := ui.Breadcrumbs(c, []string{parent, childName}, &st.agents.crumbChosen).
		Label(st.tr("会话路径", "Conversation path")).Shrink(1)
	design.Apply(crumbs, t.Font(design.TypeS))
	if crumbs.Changed() && st.agents.crumbChosen == 0 {
		st.exitChildView()
	}
	switchChip := uikit.IconButton(c, uikit.IconButtonOptions{
		Label:    st.tr("切换子智能体会话", "Switch child conversation"),
		Tooltip:  st.tr("切换子智能体会话", "Switch child conversation"),
		Icon:     icons.ChevronDown(),
		Size:     design.Space3,
		Variant:  uikit.IconButtonNative,
		InkColor: t.LabelTertiary,
		OnClick: func() {
			st.agents.childOpen = !st.agents.childOpen
			st.runtime.Invalidate()
		},
	})
	children := st.childAgentService()
	if !st.agents.childOpen || children == nil {
		return
	}
	ui.PopoverBase(c, switchChip, &st.agents.childOpen, func(panel ui.Element) {
		agentPanelStyle(panel, t, design.DialogWidthS)
		buildChildSwitcher(c, t, st, children.ChildAgents(chat.ActiveSession()))
	})
}

// childAgentDisplayName resolves the child's display name for the breadcrumb
// and the switcher: the service name when available, else the stable ID.
func childAgentDisplayName(st *state, childID string) string {
	childID = strings.TrimSpace(childID)
	if childID == "" {
		return ""
	}
	if children := st.childAgentService(); children != nil {
		if child, ok := children.ChildAgent(childID); ok {
			return agentDisplayName(child.Name, child.ID)
		}
	}
	return childID
}

// agentDisplayName falls back to the stable ID when the backend has no display
// name (an unnamed delegation).
func agentDisplayName(name, id string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(id)
}

// --- generated-file turn-tail cards ----------------------------------------

// generatedFile is one file a turn's tool calls produced: the path the cards
// open, the display name and the directory part (both derived from the path).
type generatedFile struct {
	Path string
	Name string
	Dir  string
	// Deleted marks a file the turn removed (apply_patch Delete File): the row
	// stays honest about it and offers no preview actions.
	Deleted bool
}

// generatedFiles is one finished turn's file output, split into the files the
// agent delivered (written) and the files it edited.
type generatedFiles struct {
	delivered []generatedFile
	edited    []generatedFile
}

// newGeneratedFile splits one path into the display name and the directory.
func newGeneratedFile(path string) generatedFile {
	path = strings.TrimSpace(path)
	file := generatedFile{Path: path, Name: path}
	if index := strings.LastIndex(path, "/"); index >= 0 {
		file.Dir = path[:index+1]
		file.Name = path[index+1:]
	}
	return file
}

// tailTurnMessages returns the assistant messages of the current (last) turn:
// the messages after the last user message.
func tailTurnMessages(messages []contracts.ChatMessage) []contracts.ChatMessage {
	start := 0
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == contracts.ChatRoleUser {
			start = index + 1
			break
		}
	}
	if start >= len(messages) {
		return nil
	}
	return messages[start:]
}

// generatedFileEntry is one file the turn produced, with the card family it
// belongs to (delivered vs edited). One call can produce several entries
// (apply_patch writes, deletes and moves multiple files).
type generatedFileEntry struct {
	path    string
	kind    string
	deleted bool
}

// removeGeneratedFile drops one path from a card list (write supersedes edit).
func removeGeneratedFile(files []generatedFile, path string) []generatedFile {
	kept := files[:0]
	for _, file := range files {
		if file.Path != path {
			kept = append(kept, file)
		}
	}
	return kept
}

// turnGeneratedFiles collects the current turn's file-producing calls:
// write_file paths are the delivered files, edit_file/str_replace_editor paths
// the edited files. A path appears once; a file both written and edited counts
// as delivered. Present-tool artifacts carry no filesystem path and are not
// part of these cards.
func turnGeneratedFiles(messages []contracts.ChatMessage) generatedFiles {
	var files generatedFiles
	delivered := make(map[string]bool)
	edited := make(map[string]bool)
	for _, message := range messages {
		if message.Role != contracts.ChatRoleAssistant {
			continue
		}
		for _, call := range message.Tools {
			// Only a successful call produced a file: a failed, cancelled,
			// denied or still-running write must never be presented as
			// delivered or edited (the cards offer actions on the path).
			if uikit.ToolCallStatusKind(call) != uikit.StatusSuccess {
				continue
			}
			for _, entry := range generatedFileEntries(call) {
				switch entry.kind {
				case generatedToolWrite:
					if delivered[entry.path] {
						continue
					}
					delivered[entry.path] = true
					// A file written after it was edited counts as delivered
					// only (drop the earlier edited row).
					files.edited = removeGeneratedFile(files.edited, entry.path)
					delete(edited, entry.path)
					files.delivered = append(files.delivered, newGeneratedFile(entry.path))
				case generatedToolEdit:
					if delivered[entry.path] || edited[entry.path] {
						continue
					}
					edited[entry.path] = true
					file := newGeneratedFile(entry.path)
					file.Deleted = entry.deleted
					files.edited = append(files.edited, file)
				}
			}
		}
	}
	return files
}

// The file-producing tool families the cards know: the rig file tools.
const (
	generatedToolWrite = "write_file"
	generatedToolEdit  = "edit_file"
	generatedToolNone  = ""
)

// generatedFileTool classifies one tool call name into the card's write/edit
// families (an unknown tool produces no card).
func generatedFileTool(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case generatedToolWrite:
		return generatedToolWrite
	case "edit_file", "str_replace_editor":
		return generatedToolEdit
	default:
		return generatedToolNone
	}
}

// generatedFileEntries resolves the files one successful call produced. The
// single-file tools name their path in the arguments; apply_patch names every
// file in its patch body, so one call can fill several rows (added files count
// as delivered, updated/deleted/moved files as edited).
func generatedFileEntries(call contracts.ToolCall) []generatedFileEntry {
	// The runtime records the file list of a multi-file call from the full
	// arguments (the bounded patch body is not valid JSON). Older transcripts
	// fall back to parsing the arguments below.
	if len(call.Generated) > 0 {
		entries := make([]generatedFileEntry, 0, len(call.Generated))
		for _, file := range call.Generated {
			kind := generatedToolEdit
			if file.Added && !file.Deleted {
				kind = generatedToolWrite
			}
			entries = append(entries, generatedFileEntry{path: file.Path, kind: kind, deleted: file.Deleted})
		}
		return entries
	}
	if strings.EqualFold(strings.TrimSpace(call.Name), "apply_patch") {
		return generatedPatchEntries(call)
	}
	path := generatedFileFromToolCall(call)
	kind := generatedFileTool(call.Name)
	if path == "" || kind == generatedToolNone {
		return nil
	}
	return []generatedFileEntry{{path: path, kind: kind}}
}

// maxGeneratedPatchFiles mirrors the runtime parser's cap (the SDK accepts up
// to 200 files per patch); the card itself shows the first rows and a "more"
// line.
const maxGeneratedPatchFiles = 200

// generatedPatchEntries parses the file lines of an apply_patch body
// (*** Add File / Update File / Delete File / Move to). A body that does not
// parse yields no rows: the card never invents a path.
func generatedPatchEntries(call contracts.ToolCall) []generatedFileEntry {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		return nil
	}
	var args struct {
		Patch  string `json:"patch"`
		DryRun bool   `json:"dry_run"`
	}
	if json.Unmarshal([]byte(raw), &args) != nil || args.DryRun {
		return nil
	}
	entries := make([]generatedFileEntry, 0, 4)
	seen := make(map[string]bool, 4)
	for _, line := range strings.Split(args.Patch, "\n") {
		// Directives are matched at column 0, exactly like the SDK: a context
		// line that merely mentions one must not invent a file.
		kind := generatedToolNone
		path := ""
		deleted := false
		switch {
		case strings.HasPrefix(line, "*** Add File:"):
			kind, path = generatedToolWrite, strings.TrimPrefix(line, "*** Add File:")
		case strings.HasPrefix(line, "*** Update File:"):
			kind, path = generatedToolEdit, strings.TrimPrefix(line, "*** Update File:")
		case strings.HasPrefix(line, "*** Delete File:"):
			kind, path, deleted = generatedToolEdit, strings.TrimPrefix(line, "*** Delete File:"), true
		case strings.HasPrefix(line, "*** Move to:"):
			kind, path = generatedToolEdit, strings.TrimPrefix(line, "*** Move to:")
		default:
			continue
		}
		path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		entries = append(entries, generatedFileEntry{path: path, kind: kind, deleted: deleted})
		if len(entries) >= maxGeneratedPatchFiles {
			break
		}
	}
	return entries
}

// generatedFileFromToolCall reads the file path of one file-producing call: the
// structured ToolCall.Path first (captured from the full arguments before the
// transcript bounds them), then the JSON arguments for older records. A call
// without a readable path is skipped instead of inventing one.
func generatedFileFromToolCall(call contracts.ToolCall) string {
	if path := strings.TrimSpace(call.Path); path != "" {
		return strings.ReplaceAll(path, "\\", "/")
	}
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filePath"} {
		value, ok := values[key].(string)
		if !ok {
			continue
		}
		path := strings.TrimSpace(value)
		if path == "" {
			continue
		}
		return strings.ReplaceAll(path, "\\", "/")
	}
	return ""
}

// buildGeneratedFileCards renders the turn-tail cards under a finished answer:
// 交付文件 (written files) and 已编辑 N 个文件 (edited files), each row offering
// the sidebar preview, the change view (the same file tab until the previewer
// grows a diff entry point) and the native open action.
func buildGeneratedFileCards(c *ui.Context, t *design.Tokens, st *state, files generatedFiles) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		if len(files.delivered) > 0 {
			buildGeneratedFileCard(c, t, st, icons.Check(), st.tr("交付文件", "Delivered files"),
				fmt.Sprintf(st.tr("%d 个", "%d files"), len(files.delivered)),
				files.delivered)
		}
		if len(files.edited) > 0 {
			buildGeneratedFileCard(c, t, st, icons.Pencil(),
				fmt.Sprintf(st.tr("已编辑 %d 个文件", "Edited %d files"), len(files.edited)),
				"", files.edited)
		}
	})
}

// buildGeneratedFileCard is one card: a titled panel with one action row per
// file. The rows stay even when the host has no previewer (the file list is
// conversation data); the sidebar actions are then left out instead of shown
// dead.
func buildGeneratedFileCard(c *ui.Context, t *design.Tokens, st *state, icon *ui.SVG,
	title, count string, files []generatedFile) {
	card := design.Apply(ui.Column(c), t.Panel()).FillWidth().
		Padding(design.Space2, design.Space3).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			PaddingX(design.Space1).Children(func() {
			ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).FontWeight(agentHeadingWeight).
				TextColor(t.LabelPrimary)
			if count != "" {
				design.Apply(ui.Text(c, count), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary)
			}
		})
		shown := files
		if len(shown) > maxGeneratedFileRows {
			shown = shown[:maxGeneratedFileRows]
		}
		for _, file := range shown {
			buildGeneratedFileRow(c, t, st, file)
		}
		if hidden := len(files) - len(shown); hidden > 0 {
			design.Apply(ui.Text(c, fmt.Sprintf(st.tr("还有 %d 个文件", "%d more files"), hidden)),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
	})
}

// buildGeneratedFileRow is one file row: name, directory and the three
// actions.
func buildGeneratedFileRow(c *ui.Context, t *design.Tokens, st *state, file generatedFile) {
	previewer := st.filePreviewer()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingX(design.Space1).Children(func() {
		ui.Icon(c, icons.File()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, file.Name), t.Font(design.TypeXXXS)).FontWeight(agentHeadingWeight).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Tooltip(file.Path)
		if file.Dir != "" {
			design.Apply(ui.Text(c, file.Dir), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		if file.Deleted {
			design.Apply(ui.Text(c, st.tr("· 已删除", "· deleted")), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary)
		}
		ui.Spacer(c)
		// A deleted file has nothing to preview or open: showing the actions
		// would open a missing path and read as "the delete did not happen".
		if previewer == nil || file.Deleted {
			return
		}
		agentRowAction(c, t, st.tr("在侧边栏预览", "Preview"),
			st.tr("在侧边栏预览 ", "Preview in the sidebar ")+file.Path, func() {
				st.previewGeneratedFile(previewer, file.Path, false)
			})
		// The previewer has no diff entry point yet: the same file tab shows
		// the working-tree diff where the right sidebar supports it, so the
		// change action opens the same file.
		agentRowAction(c, t, st.tr("查看改动", "View changes"),
			st.tr("查看改动 ", "View changes ")+file.Path, func() {
				st.previewGeneratedFile(previewer, file.Path, false)
			})
		agentRowAction(c, t, st.tr("用系统应用打开", "Open natively"),
			st.tr("用系统应用打开 ", "Open natively ")+file.Path, func() {
				st.previewGeneratedFile(previewer, file.Path, true)
			})
	})
}

// previewGeneratedFile asks the right sidebar to open (or focus) one file, or
// hands it to the platform; a failure surfaces as an error toast instead of a
// silent no-op.
func (st *state) previewGeneratedFile(previewer contracts.FilePreviewer, path string, native bool) {
	if previewer == nil {
		return
	}
	var err error
	if native {
		err = previewer.OpenNative(path)
	} else {
		err = previewer.PreviewFile(path)
	}
	if err != nil {
		st.toast(contracts.NotifyError, st.tr("打开文件失败：", "Open file failed: ")+err.Error())
	}
}

// --- shared header title ----------------------------------------------------

// sessionTitle resolves the active conversation's title (the configured default
// when the session has none); the header, the breadcrumb and the parent crumb
// all read the same name.
func sessionTitle(st *state, chat contracts.ChatService, cfg Config) string {
	if chat != nil {
		if session, ok := chat.Session(chat.ActiveSession()); ok {
			if title := strings.TrimSpace(session.Title); title != "" {
				return title
			}
			// An open conversation with no title yet reads the same as its sidebar
			// row ("未命名会话"/"Untitled session"), not the screen's generic "对话"
			// fallback, so the header and the sidebar never name one session two
			// ways (2026-11 UI review B5). The backend auto-titles it from the first
			// prompt.
			return st.tr("未命名会话", "Untitled session")
		}
	}
	return st.localized(cfg.Title, "对话", "Chat")
}

// --- team controls (P1-2): stop and close a teammate -----------------------

// buildTeamMemberControls draws the running member's stop action and the row's
// member-actions menu (close, through the destructive confirmation). Historic
// rows are records, not live teammates, so they keep only their open/continue
// action.
func (st *state) buildTeamMemberControls(c *ui.Context, t *design.Tokens,
	member contracts.TeamMember, name string, kind uikit.StatusKind) {
	team := st.teamService()
	if team == nil {
		return
	}
	if kind == uikit.StatusRunning {
		stopText := st.tr("停止", "Stop")
		if st.agents.teamStopping == member.ID {
			stopText = st.tr("停止中…", "Stopping…")
		}
		// Interrupting a teammate cancels its current run; the teammate stays and
		// can continue, so the action is secondary, not a danger verb.
		stop := design.Apply(ui.Button(c, stopText), t.SecondaryButton()).
			Label(st.tr("停止成员 ", "Stop member ") + name).
			Tooltip(st.tr("停止会取消该队友当前的运行；队友会保留，可随时继续",
				"Stopping cancels the teammate's current run; the teammate stays and can continue")).
			Height(agentRowActionHeight).PaddingX(design.Space2).Shrink(0)
		stop.Disabled(st.agents.teamStopping != "")
		if stop.Clicked() {
			st.stopTeamMember(team, member.ID)
		}
	}
	menu := uikit.IconButton(c, uikit.IconButtonOptions{
		Key:     "session.team.member.menu." + member.ID,
		Label:   st.tr("成员操作 ", "Member actions ") + name,
		Tooltip: st.tr("成员操作", "Member actions"),
		Icon:    icons.More(),
		Size:    design.IconSizeS,
	})
	menu.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(st.tr("关闭成员", "Close member")).Danger().Chosen() {
			// The confirmation is a screen-level modal: close the overlay first so
			// the dialog owns the keyboard and its buttons are the only targets.
			st.agents.closeMember = member.ID
			st.agents.teamOpen = false
			st.runtime.Invalidate()
		}
	})
}

// buildTeamMemberCloseConfirm renders the pending close through the shared
// destructive confirmation: the safe action (取消) carries the default focus and
// Enter, and the danger action is explicit (DESIGN.md AP-12).
func buildTeamMemberCloseConfirm(c *ui.Context, st *state, members []contracts.TeamMember) {
	id := strings.TrimSpace(st.agents.closeMember)
	if id == "" {
		return
	}
	team := st.teamService()
	if team == nil {
		st.agents.closeMember = ""
		return
	}
	name := id
	for _, member := range members {
		if member.ID == id {
			name = agentDisplayName(member.Name, member.ID)
			break
		}
	}
	open := true
	uikit.ConfirmDanger(c, &open, uikit.ConfirmDangerOptions{
		Key:   "session.team.close." + id,
		Title: st.tr("关闭成员", "Close member"),
		Message: fmt.Sprintf(st.tr(
			"关闭「%s」会停止并移除该队友，进行中的运行会中断，且无法撤销。",
			"Closing %q stops and removes the teammate; a run in flight is interrupted and this cannot be undone."),
			name),
		Cancel:  st.tr("取消", "Cancel"),
		Confirm: st.tr("确认关闭", "Close member"),
		OnConfirm: func() {
			st.agents.closeMember = ""
			st.closeTeamMember(team, id)
		},
	})
	if !open {
		st.agents.closeMember = ""
	}
}

// stopTeamMember interrupts one teammate's current run off the UI thread (the
// rig manager waits for the run to settle) and publishes the outcome at the
// next frame.
func (st *state) stopTeamMember(team contracts.TeamService, memberID string) {
	if team == nil || st.agents.teamStopping != "" {
		return
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return
	}
	st.agents.teamStopping = memberID
	st.agents.teamErr = ""
	runtime := st.runtime
	go func() {
		err := team.Interrupt(memberID)
		runtime.Post(func() {
			if st.agents.teamStopping == memberID {
				st.agents.teamStopping = ""
			}
			if err != nil {
				st.agents.teamErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
			}
			runtime.Invalidate()
		})
	}()
}

// closeTeamMember stops and removes one teammate off the UI thread; the row
// disappears on the next backend snapshot.
func (st *state) closeTeamMember(team contracts.TeamService, memberID string) {
	if team == nil || st.agents.teamClosing != "" {
		return
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return
	}
	st.agents.teamClosing = memberID
	st.agents.teamErr = ""
	runtime := st.runtime
	go func() {
		err := team.Close(memberID)
		runtime.Post(func() {
			if st.agents.teamClosing == memberID {
				st.agents.teamClosing = ""
			}
			if err != nil {
				st.agents.teamErr = st.tr("关闭失败：", "Close failed: ") + err.Error()
			}
			runtime.Invalidate()
		})
	}()
}

// childRunningTeammate reports whether the child conversation is a live
// teammate whose run is in flight — the only child kind the interrupt API can
// stop (a one-shot subagent is synchronous and has no interrupt surface).
func childRunningTeammate(child contracts.ChildAgent) bool {
	return child.Kind == contracts.ChildAgentTeammate && !child.Historical &&
		strings.EqualFold(strings.TrimSpace(child.Status), contracts.ChildAgentRunning)
}

// buildChildStopAction draws the child header's stop action for a running
// teammate, so the user can interrupt it from the conversation they are reading
// instead of returning to the team overlay.
func (st *state) buildChildStopAction(c *ui.Context, t *design.Tokens, child contracts.ChildAgent) {
	if st.teamService() == nil || !childRunningTeammate(child) {
		return
	}
	stopText := st.tr("停止", "Stop")
	if st.agents.teamStopping == child.ID {
		stopText = st.tr("停止中…", "Stopping…")
	}
	name := agentDisplayName(child.Name, child.ID)
	stop := design.Apply(ui.Button(c, stopText), t.SecondaryButton()).
		Label(st.tr("停止成员 ", "Stop member ") + name).
		Tooltip(st.tr("停止会取消该队友当前的运行；队友会保留，可随时继续",
			"Stopping cancels the teammate's current run; the teammate stays and can continue")).
		Height(agentRowActionHeight).PaddingX(design.Space2).Shrink(0)
	stop.Disabled(st.agents.teamStopping != "")
	if stop.Clicked() {
		st.stopTeamMember(st.teamService(), child.ID)
	}
}

// --- execution history (P1-3) ----------------------------------------------

// ensureExecutionHistory starts the one bounded read of the active
// conversation's durable execution records. The read runs off the UI thread and
// publishes through UIRuntime.Post; the per-session guard keeps stable frames
// from re-reading the store every frame, and a session switch resets the cache
// before starting the new read.
func (st *state) ensureExecutionHistory(sessionID string) {
	if st == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if st.agents.historySession == sessionID {
		return
	}
	st.agents.historySession = sessionID
	st.agents.history = nil
	st.agents.historyList = ui.ListState{}
	service := st.executionHistoryService()
	if service == nil || sessionID == "" {
		return
	}
	st.loadExecutionHistory(service, sessionID)
}

// openExecutionHistory re-reads the records when the jobs overlay opens, so a
// panel opened after new executions shows them. The previous records stay on
// screen until the new read lands (no flicker).
func (st *state) openExecutionHistory(sessionID string) {
	if st == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if st.agents.historySession != sessionID {
		st.ensureExecutionHistory(sessionID)
		return
	}
	service := st.executionHistoryService()
	if service == nil || sessionID == "" {
		return
	}
	st.loadExecutionHistory(service, sessionID)
}

// loadExecutionHistory reads the bounded records off the UI thread and publishes
// them at the next frame; a late answer for a session the user already left is
// dropped.
func (st *state) loadExecutionHistory(service contracts.ExecutionHistoryService, sessionID string) {
	runtime := st.runtime
	go func() {
		records := service.ExecutionHistory(sessionID, 0)
		runtime.Post(func() {
			if st.agents.historySession != sessionID {
				return
			}
			st.agents.history = records
			runtime.Invalidate()
		})
	}()
}

// sessionExecutionHistory returns the cached records of one conversation (nil
// while that conversation's read is still in flight, or for another session).
func (st *state) sessionExecutionHistory(sessionID string) []contracts.ExecutionRecord {
	if st == nil || st.agents.historySession != strings.TrimSpace(sessionID) {
		return nil
	}
	return st.agents.history
}

// visibleExecutionHistory drops the records whose resource is already listed as
// a live background job: the live row is the current truth, and the same job
// must not appear twice in one overlay.
func visibleExecutionHistory(records []contracts.ExecutionRecord,
	live []contracts.BackgroundJob) []contracts.ExecutionRecord {
	if len(records) == 0 {
		return nil
	}
	liveIDs := make(map[string]bool, len(live))
	for _, job := range live {
		liveIDs[job.ID] = true
	}
	out := make([]contracts.ExecutionRecord, 0, len(records))
	for _, record := range records {
		if record.Kind == contracts.ExecutionKindJob && liveIDs[record.ID] {
			continue
		}
		out = append(out, record)
	}
	return out
}

// buildExecutionHistoryGroup draws the durable execution records under the live
// jobs: the lifecycle only, with the honest no-output note and the interrupted
// state as a neutral one. An empty set renders nothing (no empty section).
func buildExecutionHistoryGroup(c *ui.Context, t *design.Tokens, st *state,
	records []contracts.ExecutionRecord) {
	if len(records) == 0 {
		return
	}
	ui.Divider(c)
	agentSectionLabel(c, t, st.tr("历史", "History"))
	agentHint(c, t, st.tr(
		"持久记录，重启后仍可查看；只记录开始/结束/中断，输出不会持久化。",
		"Persisted records survive a restart; they cover the lifecycle (start/end/interrupt) only — output is not persisted."))
	st.agents.historyList.Key = func(index int) any {
		if index < 0 || index >= len(records) {
			return nil
		}
		return records[index].Kind + "/" + records[index].ID
	}
	st.agents.historyList.Label = func(index int) string {
		if index < 0 || index >= len(records) {
			return ""
		}
		return executionRecordLabel(st, records[index])
	}
	ui.List(c, &st.agents.historyList, len(records), func(index int) {
		buildExecutionHistoryRow(c, t, st, records[index])
	}).FillWidth().Gap(design.Space2)
}

// buildExecutionHistoryRow is one durable record: state badge, kind glyph, the
// command/cwd, the start clock and the observed duration.
func buildExecutionHistoryRow(c *ui.Context, t *design.Tokens, st *state,
	record contracts.ExecutionRecord) {
	kind, label := executionRecordStatus(st, record)
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Background(t.BgBase).Border(1, t.BorderL2).Radius(design.RadiusMD).
		Padding(design.Space2, design.Space3)
	row.Children(func() {
		uikit.StatusBadge(c, t, kind, label).Tooltip(executionRecordStateTooltip(st, record))
		glyph := icons.Clock()
		if record.Kind == contracts.ExecutionKindTerminal {
			glyph = icons.Terminal()
		}
		ui.Icon(c, glyph).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
		summary := executionRecordSummary(record)
		design.Apply(ui.Text(c, summary), t.MonoText(design.TypeXXS)).
			TextColor(t.LabelSecondary).Grow(1).Shrink(1).MinWidthPercent(0).
			MaxLines(1).Ellipsis("…").Tooltip(summary)
		if !record.Started.IsZero() {
			design.Apply(ui.Text(c, messageClock(st.locale, record.Started)),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		}
		if duration := record.Duration(); duration > 0 {
			design.Apply(ui.Text(c, formatUsageDuration(duration.Milliseconds())),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).NoWrap()
		}
	})
}

// executionRecordStatus maps a folded record onto the product's status
// vocabulary. An interrupted record is neutral (not a failure); a finished job
// keeps its backend outcome word, and a terminal's close is a success.
func executionRecordStatus(st *state, record contracts.ExecutionRecord) (uikit.StatusKind, string) {
	switch record.State {
	case contracts.ExecutionStateRunning:
		return uikit.StatusRunning, st.tr("运行中", "Running")
	case contracts.ExecutionStateInterrupted:
		return uikit.StatusInterrupted, st.tr("已中断", "Interrupted")
	case contracts.ExecutionStateFinished:
		switch strings.ToLower(strings.TrimSpace(record.Outcome)) {
		case "killed":
			return uikit.StatusCancelled, st.tr("已取消", "Cancelled")
		case "failed":
			return uikit.StatusError, st.tr("失败", "Failed")
		case "timed_out":
			return uikit.StatusError, st.tr("超时", "Timed out")
		default:
			return uikit.StatusSuccess, st.tr("已完成", "Finished")
		}
	default:
		label := strings.TrimSpace(record.State)
		if label == "" {
			label = st.tr("未知", "Unknown")
		}
		return uikit.StatusUnknown, label
	}
}

// executionRecordSummary is the record row's readable line: the job command, or
// the terminal's working directory (its transport when no cwd was recorded),
// falling back to the id.
func executionRecordSummary(record contracts.ExecutionRecord) string {
	if command := strings.TrimSpace(record.Command); command != "" {
		return command
	}
	if cwd := strings.TrimSpace(record.CWD); cwd != "" {
		return cwd
	}
	if transport := strings.TrimSpace(record.Transport); transport != "" {
		return transport
	}
	return record.ID
}

// executionRecordLabel is the record row's accessible name.
func executionRecordLabel(st *state, record contracts.ExecutionRecord) string {
	kind := st.tr("后台任务 ", "job ")
	if record.Kind == contracts.ExecutionKindTerminal {
		kind = st.tr("终端 ", "terminal ")
	}
	return kind + record.ID
}

// executionRecordStateTooltip explains the state words that need it: an
// interrupted record's end was never observed, and a finished job's exit code is
// not part of the durable lifecycle.
func executionRecordStateTooltip(st *state, record contracts.ExecutionRecord) string {
	switch record.State {
	case contracts.ExecutionStateInterrupted:
		return st.tr("结束未记录：应用退出或进程结束时没有收尾（不代表仍在运行）。",
			"The end was not recorded (the app or process exited first); this does not mean it is still running.")
	case contracts.ExecutionStateFinished:
		return st.tr("只记录了生命周期，退出码与输出不会持久化。",
			"Only the lifecycle is recorded; the exit code and output are not persisted.")
	default:
		return ""
	}
}
