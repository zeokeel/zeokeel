package session

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Limits of the composer's suggestion menus.
const (
	maxSlashCommands = 8
	maxMentionFiles  = 8
)

// Tab indexes used by the native ui.Tabs control; they match the state field.
const (
	tabChat = iota
	tabTrajectory
	tabMemory
)

// buildScreen renders one frame of the branded conversation screen. All look
// comes from internal/design (tokens + component styles) and the native mygo
// widgets; the screen draws nothing itself.
func buildScreen(
	c *ui.Context,
	runtime contracts.UIRuntime,
	navigator contracts.Navigator,
	chat contracts.ChatService,
	cfg Config,
	st *state,
) {
	t := design.Of(c)
	// Keep the slash commands' registry titles in the host language (the
	// command palette reads them directly). Only an actual language change
	// rebuilds the registry; a stable frame never does.
	if lang := uikit.Language(st.locale); st.syncCommandLocale != nil && lang != st.commandLocale {
		st.commandLocale = lang
		st.syncCommandLocale()
	}
	activeID := chat.ActiveSession()
	// The composer draft and staged attachments belong to one conversation:
	// save/restore them on the conversation switch before anything builds.
	// Opening or switching a conversation asks for the keyboard once, so the
	// user can start typing immediately.
	switchedSession := st.draftSession != activeID
	st.syncComposerDraft(activeID)
	if switchedSession && activeID != "" && st.tab == tabChat {
		st.focusComposer = true
	}
	chatSession, hasSession := chat.Session(activeID)
	settled := observeRunSettled(chat, st)
	if settled {
		// The reply is done: hand the keyboard back to the composer (only on the
		// conversation tab, never while some other input owns it).
		if st.tab == tabChat {
			st.focusComposer = true
		}
		runtime.Post(func() {
			handleRunSettled(chat, st, runtime)
			runtime.Invalidate()
		})
	}
	// Entering the Memory tab re-reads the store once (the cached read model is
	// what avoids render-time I/O in every other frame).
	if st.tab != st.lastTab {
		if st.tab == tabMemory {
			requestMemoryRefresh(st)
		}
		st.lastTab = st.tab
	}
	// Switching conversations starts the new transcript at its end: the offset
	// of the previous conversation is meaningless in the new one (each tab
	// keeps its own ScrollState otherwise). Only the transcript follows the
	// streaming end; the trajectory and memory panes open at their top.
	if st.scrollSession != activeID {
		st.scrollSession = activeID
		// Fold/usage/expanded state is keyed by message ID; the maps would grow
		// with every conversation the user ever opens. Keep only the rows of the
		// conversation that shows (per-session transcript windows are the one
		// exception: they are small and keep the user's place).
		pruneMessageState(st, chatSession.Messages)
		// The transcript List starts at the new conversation's end; the fold
		// state, sticky follow flag and laid-out gate are reset with it.
		st.chatList = ui.ListState{}
		st.chatFollowEnd = true
		st.queueMenuOpen = false
		st.chatListSeen = false
		st.trajList = ui.ListState{}
		st.memoryList = ui.ListState{}
		// Session-level errors (export/regenerate/command/auto-continue
		// failures) belongs to the conversation that raised it: a switch must
		// not show another conversation's stale error row.
		resetSessionErrors(st)
	}
	// Frame-time fallback for hosts that update the session state without an
	// extra notification: retry the queue once the run has settled. When this
	// frame already detected the settle, handleRunSettled owns the flush, so
	// posting the fallback too would send two queued prompts in one frame.
	// A delivery failure pauses the queue and leaves its error set: the fallback
	// must then stay quiet instead of re-sending the same prompt every frame
	// (the explicit 重试 action clears both and retries).
	if !settled && len(st.queuedFor(activeID)) > 0 && !chatSession.Running &&
		!st.queuePaused[activeID] && st.queueErrorFor(activeID) == "" {
		runtime.Post(func() {
			processQueuedPrompts(chat, st, activeID)
			runtime.Invalidate()
		})
	}
	// Keyboard interrupt of the running turn: Esc / Cmd+. stop generation the
	// way the 停止 control does. The composer's suggestion menus consume Esc
	// first (composerTextKey), and this scope refuses to interrupt while one is
	// open, so closing a menu never stops the turn by accident.
	registerInterruptShortcuts(c, chat, st, runtime, activeID)

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		// The title, actions and tabs live in the host's B3 top band (registered
		// screen slots); the body keeps only its status/error lines.
		if st.exportErr != "" {
			buildErrorLine(c, t, st.tr("导出失败：", "Export failed: ")+st.exportErr)
		}
		if st.autoContinueErr != "" {
			buildErrorLine(c, t, st.autoContinueErr)
		}
		if st.goalActionErr != "" {
			buildErrorLine(c, t, st.goalActionErr)
		}
		switch st.tab {
		case tabTrajectory:
			buildTrajectoryPane(c, t, chatSession, hasSession, st)
		case tabMemory:
			buildMemoryPane(c, t, runtime, st)
		default:
			buildChatPane(c, t, runtime, navigator, chat, cfg, st, chatSession, hasSession)
		}
		if hasSession {
			// The goal strip and the approval banner dock right above the
			// composer, on the transcript's reading measure.
			buildComposerDock(c, t, runtime, st)
			buildComposer(c, t, runtime, navigator, chat, cfg, st, activeID, chatSession)
		}
		// Command-family dialogs (/goal, /plan, /memory): native Modals above
		// the screen; their buttons close them and request the next frame.
		buildGoalDialog(c, t, runtime, st)
		buildPlanDialog(c, t, runtime, st)
		buildMemoryDialog(c, t, runtime, st)
		buildHelpDialog(c, t, runtime, st)
		// The attachment path fallback modal (ZK-09) is not command-family but
		// also lives above the screen, next to the composer it stages into.
		buildAttachmentPathDialog(c, t, runtime, st)
		// The goal actions' destructive confirmation is the native AlertDialog:
		// it lives above the whole screen (the goal menu itself is in the dock).
		buildGoalMenuActions(c, t, runtime, st)
	})
}

// pruneMessageState drops per-message UI state that no longer belongs to the
// conversation on screen: the fold state (thinking blocks, tool rows), the usage
// fold and the unfolded action row are keyed by message ID and only meaningful
// for the active conversation, so a long session never keeps another session's
// entries (mygo-maintenance: keep app state bounded).
func pruneMessageState(st *state, messages []contracts.ChatMessage) {
	keep := make(map[string]bool, len(messages))
	for _, message := range messages {
		keep[message.ID] = true
	}
	keepKey := func(key string) bool {
		if keep[key] {
			return true
		}
		// Tool rows are keyed "session.tool.<messageID>.<toolID>".
		if rest, ok := strings.CutPrefix(key, "session.tool."); ok {
			for id := range keep {
				if strings.HasPrefix(rest, id+".") {
					return true
				}
			}
		}
		return false
	}
	prune := func(m map[string]bool) {
		for key := range m {
			if !keepKey(key) {
				delete(m, key)
			}
		}
	}
	prune(st.thinkOpen)
	prune(st.toolOpen)
	prune(st.processOpen)
}

// --- session-scoped state helpers -------------------------------------------

// resetSessionErrors clears the UI feedback that belongs to the conversation
// currently shown, so switching conversations never shows another one's stale
// export/regenerate/rating/command/auto-continue failure (or a copy check).
func resetSessionErrors(st *state) {
	if st == nil {
		return
	}
	st.exportErr = ""
	st.regenerateErr = ""
	st.commandErr = ""
	st.autoContinueErr = ""
}

// cancelRunningTurn stops the active run the way the 停止 control does: pause
// the queued prompts (a stop must not start another run automatically),
// suppress goal auto-continue, toast the outcome and report a failure inline.
// It is shared by the stop button, Esc/Cmd+. and the screen shortcut so every
// interrupt path has identical semantics.
func cancelRunningTurn(chat contracts.ChatService, st *state, runtime contracts.UIRuntime, sessionID string) {
	if chat == nil || st == nil || sessionID == "" {
		return
	}
	st.autoContinueSuppressed = true
	pauseQueue(st, sessionID)
	if err := chat.Cancel(sessionID); err != nil {
		st.commandErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
	} else {
		st.commandErr = ""
		st.toast(contracts.NotifyInfo, st.tr("已停止", "Stopped"))
	}
	runtime.Invalidate()
}

// suggestionMenuOpen reports whether a slash or @ suggestion menu owns Esc right
// now: the interrupt shortcut must leave that key to the menu (menu-first).
func suggestionMenuOpen(st *state) bool {
	if st == nil {
		return false
	}
	if _, active := slashQuery(st.draft); active && st.slashDismissed != st.draft {
		return true
	}
	if _, _, _, active := mentionQueryAt(st.draft, st.composerCaret); active && st.mentionDismissed != st.draft {
		return true
	}
	return false
}

// sessionDialogOpen reports whether a modal dialog of the conversation screen
// owns Esc right now (command dialogs, the attachment path fallback, memory
// confirm/edit). Esc must close those first, never interrupt a run behind them.
func sessionDialogOpen(st *state) bool {
	if st == nil {
		return false
	}
	return st.goalOpen || st.planOpen || st.memOpen ||
		st.helpOpen || st.attachPathsOpen || st.memoryEditOpen || st.memoryClearOpen
}

// denyPendingApproval answers the oldest pending approval as denied, the way the
// approval card's deny action does. Esc on the screen must dismiss an approval
// before it interrupts the turn: the card is the thing waiting for the user.
// It reports whether one was pending, so the interrupt paths leave the run alive.
func denyPendingApproval(st *state, runtime contracts.UIRuntime) bool {
	if st == nil {
		return false
	}
	raw, ok := st.ctx.Get(contracts.KeyApprovals)
	if !ok || raw == nil {
		return false
	}
	approvals, ok := raw.(contracts.Approvals)
	if !ok || approvals == nil {
		return false
	}
	pending := approvals.Pending()
	if len(pending) == 0 {
		return false
	}
	// Oldest first: Pending() is documented as oldest first, so the card the
	// user sees at the top is the one Esc answers.
	if err := approvals.Resolve(pending[0].ID, contracts.ApprovalDeny); err != nil {
		st.commandErr = st.tr("拒绝审批失败：", "Could not deny the request: ") + err.Error()
	} else {
		st.commandErr = ""
	}
	runtime.Invalidate()
	return true
}

// registerInterruptShortcuts wires the screen-level Esc / Cmd+. interrupt while
// a turn runs. The composer path (composerTextKey) also handles Esc menu-first
// when the key reaches the text hook; this scope covers the rest of the screen.
// Esc is strictly ordered: a modal dialog first, then an open suggestion menu
// (dismiss it), and only then the interrupt. Cmd+. never belongs to a menu and
// interrupts directly.
func registerInterruptShortcuts(c *ui.Context, chat contracts.ChatService, st *state, runtime contracts.UIRuntime, activeID string) {
	if activeID == "" {
		return
	}
	session, ok := chat.Session(activeID)
	if !ok || !session.Running {
		return
	}
	interruptEsc := func() {
		// A pending approval owns Esc first: deny the oldest request and keep the
		// turn running (the user is answering the agent, not stopping it).
		if denyPendingApproval(st, runtime) {
			return
		}
		if sessionDialogOpen(st) {
			// The dialog's own overlay shortcut closes it; leave Esc to that.
			return
		}
		if suggestionMenuOpen(st) {
			// Menu-first: the first Esc only dismisses the open suggestion menu.
			st.slashDismissed = st.draft
			st.mentionDismissed = st.draft
			runtime.Invalidate()
			return
		}
		cancelRunningTurn(chat, st, runtime, activeID)
	}
	c.OnShortcut(0, ui.KeyEscape, interruptEsc)
	c.OnShortcut(ui.Cmd, ui.KeyPeriod, func() {
		if sessionDialogOpen(st) {
			return
		}
		cancelRunningTurn(chat, st, runtime, activeID)
	})
}

// --- screen chrome (host B3 top band slots) ---------------------------------

// buildScreenHeaderSlot is the conversation's SlotScreenHeader contribution: the
// conversation title (the session title, else the configured default) with the
// workspace name as quiet context when the band is wide enough. The measured row
// width goes back into st.headerWidth, which the actions slot uses on the next
// frame to compact the team chip.
func buildScreenHeaderSlot(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService, cfg Config) {
	title := st.localized(cfg.Title, "对话", "Chat")
	activeID := chat.ActiveSession()
	if session, ok := chat.Session(activeID); ok && strings.TrimSpace(session.Title) != "" {
		title = session.Title
	}
	// The workspace label is quiet context: it stays while the title keeps its
	// room and drops out on a narrow band (the Overview pane still names it).
	showWorkspace := st.headerWidth == 0 || st.headerWidth >= 460
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2)
	row.Children(func() {
		// The title sits in window chrome (B3 band), not in a page body: the
		// TypeM step keeps it in proportion with the 13 DIP tabs, 12 DIP badges
		// and 16 DIP band icons; TypeL made the band read top-heavy.
		titleElement := design.Apply(ui.Text(c, title), t.Font(design.TypeM)).
			FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MinWidth(0).
			MaxLines(1).Ellipsis("…").Tooltip(title)
		if st.headerWidth > 0 && st.headerWidth < 560 {
			titleElement.MinWidth(120)
		}
		if showWorkspace {
			buildHeaderWorkspace(c, t, st)
		}
	})
	st.headerWidth = row.Bounds().W
}

// buildScreenActionsSlot is the conversation's SlotScreenActions contribution:
// the running badge, the team entry and the conversation-actions ⋯ menu.
func buildScreenActionsSlot(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, chat contracts.ChatService, cfg Config) {
	activeID := chat.ActiveSession()
	session, hasSession := chat.Session(activeID)
	if hasSession && session.Running {
		design.Apply(ui.Badge(c, st.tr("运行中", "Running")), t.Badge()).TextColor(t.WarningText)
	}
	// The team chip drops its running words on a crowded band: the count alone
	// still answers "is there a team".
	compactTeam := st.headerWidth > 0 && st.headerWidth < 520
	buildTeamChip(c, t, runtime, st, compactTeam)
	if hasSession && activeID != "" {
		buildConversationActionsButton(c, t, st, runtime, cfg, session)
	}
}

// buildConversationActionsButton is the band's ⋯ entry: a 24 DIP More icon (no
// visible words) whose menu holds the conversation clipboard, export and archive
// actions. The actions themselves keep the old overflow menu's order.
func buildConversationActionsButton(c *ui.Context, t *design.Tokens, st *state,
	runtime contracts.UIRuntime, cfg Config, chatSession contracts.ChatSession) {
	label := st.tr("会话操作", "Conversation actions")
	button := ui.Button(c, "").Label(label).Tooltip(label).
		Size(t.Scale(design.Space6), t.Scale(design.Space6)).
		PaddingX(0).Radius(design.RadiusSM)
	button.Children(func() {
		ui.Icon(c, icons.More()).Size(t.Scale(design.Space4), t.Scale(design.Space4)).
			TextColor(t.LabelSecondary)
	})
	button.PopoverMenuButton(func(m *ui.Menu) {
		buildConversationActionsMenu(m, c, st, runtime, cfg, chatSession)
	})
}

// buildConversationActionsMenu fills the ⋯ menu: the clipboard and export
// commands of the conversation (the /export command remains the keyboard entry)
// and the archive action. Actions are grouped: clipboard first, the export
// formats next, archiving last.
func buildConversationActionsMenu(m *ui.Menu, c *ui.Context, st *state,
	runtime contracts.UIRuntime, cfg Config, chatSession contracts.ChatSession) {
	if m.Item(st.tr("复制会话 ID", "Copy session ID")).Chosen() {
		c.WriteClipboard(chatSession.ID)
	}
	if m.Item(st.tr("复制对话", "Copy conversation")).Chosen() {
		c.WriteClipboard(transcriptText(chatSession, st.english()))
	}
	m.Separator()
	if m.Item(st.tr("导出 Markdown", "Export Markdown")).Chosen() {
		exportSession(st, chatSession, cfg)
	}
	if m.Item(st.tr("导出 JSONL", "Export JSONL")).Chosen() {
		exportSessionJSONL(st, chatSession, cfg)
	}
	// Archiving moved to the settings data page, but the conversation keeps its
	// own action; success is a toast with Undo (restores the flag).
	chat, _ := st.ctx.Get(contracts.KeyChat)
	archiver, canArchive := chat.(contracts.SessionArchiver)
	if canArchive && chatSession.ID != "" {
		m.Separator()
		if m.Item(st.tr("归档会话", "Archive conversation")).Chosen() {
			if err := archiver.SetSessionArchived(chatSession.ID, true); err != nil {
				st.toast(contracts.NotifyError, st.tr("归档失败：", "Archive failed: ")+err.Error())
			} else {
				sessionID := chatSession.ID
				st.notifyUndo(st.tr("会话已归档", "Conversation archived"), st.tr("撤销", "Undo"), func() {
					_ = archiver.SetSessionArchived(sessionID, false)
					st.runtime.Invalidate()
				})
			}
		}
	}
}

// buildScreenTabsSlot is the conversation's SlotScreenTabs contribution: the
// Chat / Trajectory / Memory strip, sized to the host band's 32 DIP tab row.
func buildScreenTabsSlot(c *ui.Context, t *design.Tokens, cfg Config, st *state) {
	buildTabs(c, t, cfg, st)
}

// buildHeaderWorkspace renders the conversation's workspace name beside the
// conversation title, muted so the title stays primary; the tooltip carries the
// full folder path. The workspace is the context a user otherwise has to
// reconstruct from the sidebar group or the Files inspector.
func buildHeaderWorkspace(c *ui.Context, t *design.Tokens, st *state) {
	if st == nil {
		return
	}
	var workspace contracts.WorkspaceService
	if raw, ok := st.ctx.Get(contracts.KeyWorkspace); ok {
		workspace, _ = raw.(contracts.WorkspaceService)
	}
	if workspace == nil {
		return
	}
	current, ok, err := workspace.Current(context.Background())
	if err != nil || !ok {
		return
	}
	name := strings.TrimSpace(current.Name)
	if name == "" {
		name = strings.TrimSpace(current.Root)
	}
	if name == "" {
		return
	}
	// One shrink-proof chip: without the Row wrapper the icon and the name were
	// separate flex children and a long conversation title could squeeze them to
	// zero width, silently hiding the workspace from the header.
	ui.Row(c).AlignItems(ui.Center).Gap(design.Space1).Shrink(0).Children(func() {
		ui.Icon(c, icons.Folder()).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
		nameElement := design.Apply(ui.Text(c, name), t.TextMuted(design.TypeXS)).
			MaxLines(1).Ellipsis("…").MaxWidth(140).NoWrap()
		if root := strings.TrimSpace(current.Root); root != "" {
			nameElement.Tooltip(root)
		}
	})
}

// goalStatus resolves the localized chip label, the muted/danger color and the
// terminal state of the active goal.
func goalStatus(st *state, t *design.Tokens) (label string, color ui.Color, paused, terminal bool) {
	status := strings.ToLower(strings.TrimSpace(st.goal.Status))
	switch status {
	case contracts.GoalStatusPaused:
		return st.tr("目标已暂停", "Goal paused"), t.WarningText, true, false
	case contracts.GoalStatusCompleted:
		return st.tr("目标已完成", "Goal completed"), t.LabelTertiary, false, true
	case contracts.GoalStatusAbandoned:
		return st.tr("目标已放弃", "Goal abandoned"), t.LabelTertiary, false, true
	default:
		return st.tr("目标", "Goal"), t.Accent, false, false
	}
}

// buildComposerDock is the strip above the composer on the transcript's reading
// measure: the active goal (badge, text, ⋯ menu) first, then the host slot the
// approval module draws its inline card into. The transcript itself stays above.
func buildComposerDock(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
		ui.Column(c).Width(design.TranscriptWidth).MaxWidthPercent(92).Gap(design.Space2).Children(func() {
			buildGoalDock(c, t, runtime, st)
			runtime.RenderSlot(c, contracts.SlotSessionBanner)
		})
	})
}

// buildGoalDock is the active goal's one compact line above the composer: the
// status badge, the goal text (ellipsized, full text in the tooltip) and the ⋯
// goal menu (pause/resume, complete, end/delete) carried over from the header.
func buildGoalDock(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st.goal == nil || strings.TrimSpace(st.goal.Text) == "" {
		return
	}
	th := t.Theme()
	label, color, _, _ := goalStatus(st, t)
	ui.Row(c).FillWidth().Height(t.Scale(th.Space(7))).AlignItems(ui.Center).
		Gap(design.Space2).Children(func() {
		design.Apply(ui.Badge(c, label), t.Badge()).TextColor(color)
		design.Apply(ui.Text(c, st.goal.Text), t.Font(design.TypeXXS)).
			TextColor(t.LabelSecondary).Grow(1).Shrink(1).MaxLines(1).
			Ellipsis("…").Tooltip(st.goal.Text)
		buildGoalDockMenuButton(c, t, runtime, st)
	})
}

// buildGoalDockMenuButton is the goal strip's ⋯ entry (24 DIP More icon).
func buildGoalDockMenuButton(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	label := st.tr("目标操作", "Goal actions")
	button := ui.Button(c, "").Label(label).Tooltip(label).
		Size(t.Scale(design.Space6), t.Scale(design.Space6)).
		PaddingX(0).Radius(design.RadiusSM)
	button.Children(func() {
		ui.Icon(c, icons.More()).Size(t.Scale(design.Space4), t.Scale(design.Space4)).
			TextColor(t.LabelTertiary)
	})
	button.PopoverMenuButton(func(m *ui.Menu) { buildGoalMenu(m, t, runtime, st) })
}

// buildTeamChip is the header's team entry: it shows the live teammate count and
// (on a wide header) how many are running, and opens the inspector's team pane
// or shared task board through the stable rightpanel command IDs. It renders
// nothing when the host has no team backend or the team is empty; a narrow
// header keeps only the count so the title stays readable. RunState decides the
// running count (Member.Status is availability, not a run: it must never be
// painted as 运行中).
func buildTeamChip(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, compact bool) {
	if st == nil {
		return
	}
	raw, ok := st.ctx.Get(contracts.KeyTeam)
	if !ok || raw == nil {
		return
	}
	team, ok := raw.(contracts.TeamService)
	if !ok || team == nil {
		return
	}
	members := team.Members()
	if len(members) == 0 {
		return
	}
	running := 0
	for _, member := range members {
		if strings.EqualFold(strings.TrimSpace(member.RunState), contracts.TeamRunRunning) {
			running++
		}
	}
	th := t.Theme()
	label := fmt.Sprintf(st.tr("团队 %d", "Team %d"), len(members))
	if running > 0 && !compact {
		label = fmt.Sprintf(st.tr("团队 %d · %d 运行中", "Team %d · %d running"), len(members), running)
	}
	tooltip := fmt.Sprintf(st.tr("智能体团队：%d 名成员，%d 个运行中", "Agent team: %d members, %d running"),
		len(members), running)
	chip := ui.Button(c, "").
		Label(tooltip).
		Tooltip(tooltip).
		Height(t.Scale(th.Space(7))).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0).Gap(design.Space1)
	chip.Children(func() {
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).FontWeight(600).
			TextColor(t.LabelSecondary).NoWrap()
		if running > 0 && compact {
			// The compact header has no room for the running words: one quiet
			// warning dot keeps the state visible without the text.
			ui.Box(c).Size(design.Space1, design.Space1).Radius(design.Space1).
				Background(t.Warning).Margin(0, 0, 0, design.Space1)
		}
	})
	chip.PopoverMenuButton(func(m *ui.Menu) {
		buildTeamChipMenu(m, runtime, st)
	})
}

// buildTeamChipMenu fills the team chip's menu with the two inspector entries.
// The commands are optional (a host without the right inspector registers no
// such command): a missing command keeps its item visible but disabled, so the
// entry never lies about an action it cannot run.
func buildTeamChipMenu(m *ui.Menu, runtime contracts.UIRuntime, st *state) {
	teamCommand, hasTeam := findRuntimeCommand(runtime, teamOpenCommandID)
	item := m.Item(st.tr("打开团队面板", "Open team panel"))
	item.ID(teamOpenCommandID)
	item.Disabled(!hasTeam)
	if item.Chosen() && hasTeam {
		runRuntimeCommand(teamCommand)
	}
	m.Separator()
	tasksCommand, hasTasks := findRuntimeCommand(runtime, tasksOpenCommandID)
	tasksItem := m.Item(st.tr("打开任务板", "Open task board"))
	tasksItem.ID(tasksOpenCommandID)
	tasksItem.Disabled(!hasTasks)
	if tasksItem.Chosen() && hasTasks {
		runRuntimeCommand(tasksCommand)
	}
}

// Stable inspector pane-opening command IDs (registered by internal/ui/rightpanel;
// repeated here because UI modules do not import each other).
const (
	teamOpenCommandID  = "rightpanel.team.open"
	tasksOpenCommandID = "rightpanel.tasks.open"
)

// findRuntimeCommand returns one registered command by its stable ID.
func findRuntimeCommand(runtime contracts.UIRuntime, id string) (contracts.CommandSpec, bool) {
	if runtime == nil {
		return contracts.CommandSpec{}, false
	}
	for _, command := range runtime.Commands() {
		if command.ID == id {
			return command, true
		}
	}
	return contracts.CommandSpec{}, false
}

// runRuntimeCommand runs one command, honoring its Enabled guard.
func runRuntimeCommand(command contracts.CommandSpec) {
	if command.Enabled != nil && !command.Enabled() {
		return
	}
	if command.Run != nil {
		_ = command.Run()
	}
}

// buildGoalMenu fills the goal chip's menu: pause/resume and complete first,
// then the destructive end/delete actions in the danger color, separated.
func buildGoalMenu(m *ui.Menu, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st.goal == nil {
		return
	}
	_, _, paused, terminal := goalStatus(st, t)
	if !terminal {
		if pauser, ok := st.goalTracker.(contracts.GoalPauser); ok && pauser != nil {
			label := st.tr("暂停目标", "Pause goal")
			if paused {
				label = st.tr("继续目标", "Resume goal")
			}
			if m.Item(label).ID("goal.pause").Chosen() {
				ok := pauser.Pause()
				if paused {
					ok = pauser.Resume()
				}
				st.goalActionErr = ""
				if !ok {
					if paused {
						st.goalActionErr = st.tr("继续目标失败：", "Resume goal failed: ") + st.tr("目标不存在", "no active goal")
					} else {
						st.goalActionErr = st.tr("暂停目标失败：", "Pause goal failed: ") + st.tr("目标不存在", "no active goal")
					}
				}
				runtime.Invalidate()
			}
		}
		if _, ok := goalLifecycle(st); ok {
			if m.Item(st.tr("完成目标", "Complete goal")).ID("goal.complete").Chosen() {
				lifecycle, ok := goalLifecycle(st)
				if !ok {
					return
				}
				if lifecycle.Complete() {
					st.goalActionErr = ""
					st.toast(contracts.NotifySuccess, st.tr("目标已完成", "Goal completed"))
				} else {
					st.goalActionErr = st.tr("完成目标失败：", "Complete goal failed: ") + st.tr("目标不存在", "no active goal")
				}
				runtime.Invalidate()
			}
			m.Separator()
			// "End" keeps the record; only "Delete" removes it — the two danger
			// actions are no longer adjacent siblings with unclear semantics.
			if m.Item(st.tr("结束目标（保留记录）", "End goal (keep record)")).ID("goal.end").Chosen() {
				st.goalConfirm = "abandon"
				runtime.Invalidate()
			}
		} else if _, ok := st.goalTracker.(contracts.GoalPauser); ok {
			m.Separator()
		}
	}
	if m.Item(st.tr("删除目标记录", "Delete goal record")).ID("goal.delete").Danger().Chosen() {
		st.goalConfirm = "clear"
		runtime.Invalidate()
	}
}

// buildGoalMenuActions renders the confirmation of a destructive goal action.
// Abandon and clear are irreversible enough to ask through the native
// AlertDialog (cancel left, confirm right) instead of running on a menu click.
func buildGoalMenuActions(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st.goalConfirm == "" {
		return
	}
	open := true
	title, message, confirm := "", "", ""
	switch st.goalConfirm {
	case "abandon":
		title = st.tr("结束目标", "End goal")
		message = st.tr("结束当前目标？目标记录与历史会保留，自动推进停止，之后仍可删除记录。",
			"End the current goal? Its record and history stay, automatic progress stops, and you can delete the record later.")
		confirm = st.tr("确认结束", "End")
	default:
		title = st.tr("删除目标记录", "Delete goal record")
		message = st.tr("删除当前目标及其记录？与「结束目标」不同，历史记录也会被移除，无法撤销。",
			"Delete the current goal and its record? Unlike ending it, the record is removed and this cannot be undone.")
		confirm = st.tr("确认删除", "Delete")
	}
	action := st.goalConfirm
	switch ui.AlertDialog(c, &open, title, message, st.tr("取消", "Cancel"), confirm) {
	case 1:
		if action == "abandon" {
			lifecycle, ok := goalLifecycle(st)
			if ok && lifecycle.Abandon() {
				st.goalActionErr = ""
				st.toast(contracts.NotifySuccess, st.tr("目标已放弃", "Goal abandoned"))
			} else if ok {
				st.goalActionErr = st.tr("放弃目标失败：", "Abandon goal failed: ") + st.tr("目标不存在", "no active goal")
			}
		} else {
			if st.goalTracker != nil {
				st.goalTracker.Clear()
			}
			st.goalActionErr = ""
			st.toast(contracts.NotifySuccess, st.tr("目标已清除", "Goal cleared"))
		}
		runtime.Invalidate()
	}
	if !open {
		st.goalConfirm = ""
	}
}

// exportSessionJSONL writes the conversation as JSON Lines through its seam.
func exportSessionJSONL(st *state, chatSession contracts.ChatSession, cfg Config) {
	title := firstNonEmpty(chatSession.Title, cfg.Title, st.tr("会话", "Conversation"))
	content, err := exportJSONLTranscript(chatSession)
	if err != nil {
		st.exportErr = err.Error()
		return
	}
	path, err := ExportJSONLSaver(st.tr("导出会话", "Export conversation"),
		sanitizeExportFilename(title)+".jsonl", content)
	switch {
	case err != nil:
		// Store the bare error: the header renders the localized prefix once,
		// exactly like the Markdown path (AUDIT-P3-4).
		st.exportErr = err.Error()
		notifyExport(st, contracts.NotifyError, st.tr("导出失败：", "Export failed: ")+err.Error())
	case strings.TrimSpace(path) != "":
		st.exportErr = ""
		notifyExport(st, contracts.NotifySuccess, st.tr("对话已导出", "Conversation exported"))
	}
}

// exportSession writes the conversation as Markdown through the export seam
// and records the result for the header status line.
func exportSession(st *state, chatSession contracts.ChatSession, cfg Config) {
	title := firstNonEmpty(chatSession.Title, cfg.Title, st.tr("会话", "Conversation"))
	path, err := ExportSaver(st.tr("导出会话", "Export conversation"),
		sanitizeExportFilename(title)+".md",
		exportTranscript(chatSession, title, st.english(), st.locale))
	switch {
	case err != nil:
		st.exportErr = err.Error()
		notifyExport(st, contracts.NotifyError, st.tr("导出失败：", "Export failed: ")+err.Error())
	case strings.TrimSpace(path) != "":
		st.exportErr = ""
		notifyExport(st, contracts.NotifySuccess, st.tr("对话已导出", "Conversation exported"))
	}
}

// notifyExport raises the optional desktop toast for one export outcome; the
// transient toast is the only success channel (the header keeps errors only).
func notifyExport(st *state, level, text string) {
	if st.notifier == nil {
		return
	}
	st.notifier.Notify(level, text)
}

// transcriptText formats the session read model for the clipboard: one block
// per message, tool calls summarised inline (the old shell's copy action).
func transcriptText(chatSession contracts.ChatSession, english bool) string {
	var builder strings.Builder
	for _, message := range chatSession.Messages {
		if strings.TrimSpace(message.Text) == "" && len(message.Tools) == 0 {
			continue
		}
		role := message.Role
		switch role {
		case "user":
			role = "用户"
			if english {
				role = "User"
			}
		case "assistant":
			role = "助手"
			if english {
				role = "Assistant"
			}
		}
		fmt.Fprintf(&builder, "%s: %s\n", role, strings.TrimSpace(message.Text))
		for _, call := range message.Tools {
			toolLabel := "工具"
			if english {
				toolLabel = "Tool"
			}
			fmt.Fprintf(&builder, "[%s] %s (%s)\n", toolLabel, call.Name, call.Status)
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// buildTabs draws the Chat/Trajectory/Memory strip with mygo's native tabs
// (TabsBase) for selection and keyboard navigation, painted as the brand's
// quiet label + accent underline.
func buildTabs(c *ui.Context, t *design.Tokens, cfg Config, st *state) {
	th := t.Theme()
	labels := []string{
		st.localized(cfg.TabChat, "对话", "Chat"),
		st.localized(cfg.TabTrajectory, "轨迹", "Trajectory"),
		st.localized(cfg.TabMemory, "记忆", "Memory"),
	}
	tabs := ui.TabsBase(c, &st.tab, len(labels))
	tabs.List.FillWidth().Height(design.Space8).Margin(0).
		Gap(design.Space4).AlignItems(ui.Stretch).Children(func() {
		for index, label := range labels {
			index, label := index, label
			active := st.tab == index
			tab := tabs.Tab(index).Height(design.Space8).PaddingX(design.Space1).
				Gap(0).Label(label)
			tab.Children(func() {
				ui.Column(c).Height(design.Space8).Justify(ui.SpaceBetween).Children(func() {
					color, weight := t.LabelSecondary, 500
					if active {
						color, weight = t.LabelPrimary, 600
					}
					design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
						FontWeight(weight).NoWrap().TextColor(color).
						Margin(design.Space2, 0, 0, 0)
					underline := ui.Box(c).Height(th.Space(0.5)).Radius(th.Space(0.25))
					if active {
						underline.Background(t.Accent)
					}
				})
			})
		}
	})
}

// --- chat -------------------------------------------------------------------

// buildChatPane is the conversation transcript: the shared SessionThread entry
// owns the official List (virtualization, message-ID keys, the streaming
// follow), the empty hero and the floating "back to latest" action. This module
// keeps the message bodies in the Row slot: prose, reasoning, tool cards,
// per-turn actions and the usage footer.
func buildChatPane(
	c *ui.Context,
	t *design.Tokens,
	runtime contracts.UIRuntime,
	navigator contracts.Navigator,
	chat contracts.ChatService,
	cfg Config,
	st *state,
	chatSession contracts.ChatSession,
	hasSession bool,
) {
	messages := chatSession.Messages
	lastID := ""
	if len(messages) > 0 {
		lastID = messages[len(messages)-1].ID
	}
	visibleError := ""
	if hasSession && sessionErrorVisible(messages, chatSession.Error) {
		visibleError = chatSession.Error
	}
	// Regenerate is a message-level action: it belongs to the last assistant
	// turn of an idle conversation (the failed turn keeps a labeled 重试
	// instead). The Row slot knows the row's message ID, so the last-row check
	// runs here once per frame.
	_, hasRegenerator := chat.(contracts.SessionRegenerator)
	// The work-steps policy also decides the completed turn's default fold: with
	// collapse=next-input the latest turn (every assistant message after the last
	// user message) opens by default and folds once a newer user message arrives.
	// A reader's explicit toggle in st.processOpen always wins over this default.
	policy := st.workStepsPolicyFor()
	lastUserIndex := -1
	indexByID := make(map[string]int, len(messages))
	for index, message := range messages {
		indexByID[message.ID] = index
		if message.Role == contracts.ChatRoleUser {
			lastUserIndex = index
		}
	}
	defaultOpen := func(messageID string) bool {
		if policy.collapse != contracts.WorkStepsCollapseNextInput {
			return false
		}
		index, ok := indexByID[messageID]
		return ok && index > lastUserIndex
	}
	// verbose keeps the whole timeline and never bundles steps; the other modes
	// fold a multi-step turn into one process row.
	var sameTurn func(prev, next contracts.ChatMessage) bool
	if policy.mode != contracts.WorkStepsVerbose {
		sameTurn = assistantTurnContinues
	}
	uikit.SessionThread(c, uikit.SessionThreadOptions{
		Messages:  messages,
		Running:   chatSession.Running,
		State:     &st.chatList,
		Locale:    st.locale,
		HoldPlace: &st.chatHoldPlace,
		FollowEnd: &st.chatFollowEnd,
		LaidOut:   &st.chatListSeen,
		Error:     visibleError,
		Empty: func(ec *ui.Context) {
			buildHero(ec, t, cfg, navigator, chat, st)
		},
		Row: func(rc *ui.Context, message contracts.ChatMessage) {
			last := message.ID == lastID && message.Role == contracts.ChatRoleAssistant
			canRegenerate := hasRegenerator && !chatSession.Running && last
			buildMessage(rc, t, runtime, message, st, canRegenerate,
				last && canCopyReply(message), defaultOpen(message.ID))
		},
		SameTurn: sameTurn,
		GroupRow: func(rc *ui.Context, group []contracts.ChatMessage) {
			last := group[len(group)-1].ID == lastID
			canRegenerate := hasRegenerator && !chatSession.Running && last
			buildAssistantTurnGroup(rc, t, runtime, st, group, canRegenerate,
				last && canCopyReply(group[len(group)-1]),
				defaultOpen(group[len(group)-1].ID))
		},
	})
}

// buildHero is the branded first-run empty state: icon badge, title, hint and
// the native new-conversation action.
func buildHero(c *ui.Context, t *design.Tokens, cfg Config, navigator contracts.Navigator, chat contracts.ChatService, st *state) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		PaddingY(design.Space8).Children(func() {
		ui.Icon(c, icons.NewChat()).Size(th.Space(6), th.Space(6)).
			TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, st.tr("开始新对话", "Start a new conversation")),
			t.Font(design.TypeM)).FontWeight(600).TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, st.localized(cfg.EmptyText,
			"还没有消息，开始一段新对话吧。",
			"No messages yet. Start a new conversation.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		button := design.Apply(ui.PrimaryButton(c, st.tr("新对话", "New conversation")), t.PrimaryButton())
		if button.Clicked() {
			startNewSession(st, chat, navigator)
		}
	})
}

// buildMessage renders one transcript entry. User turns are right-aligned
// raised bubbles; assistant turns are plain brand body text with optional
// reasoning, tool cards and errors.
func buildMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state, canRegenerate, canCopy, defaultOpen bool) {
	switch message.Role {
	case contracts.ChatRoleUser:
		ui.Column(c.Key("session.message." + message.ID)).FillWidth().AlignItems(ui.End).Gap(design.Space1).Children(func() {
			if strings.TrimSpace(message.Text) != "" {
				design.Apply(ui.Column(c), t.Panel()).
					MaxWidthPercent(70).Padding(design.Space3, design.Space4).Children(func() {
					design.Apply(ui.Text(c, message.Text), t.Font(design.TypeBody)).
						FontSize(st.messageFontSize()).TextColor(t.LabelPrimary)
				})
			}
			buildMessageAttachments(c, t, st, message.Attachments)
			// Quiet metadata/actions under the bubble: time, copy the original
			// text, and edit it back into the composer (never auto-sent).
			buildUserMessageMeta(c, t, runtime, st, message)
		})
	case contracts.ChatRoleTool, contracts.ChatRoleSystem:
		// The role badge is localized copy, never the raw protocol value
		// ("tool"/"system") that used to leak into the English UI.
		role := st.tr("工具", "Tool")
		if message.Role == contracts.ChatRoleSystem {
			role = st.tr("系统", "System")
		}
		ui.Row(c.Key("session.message." + message.ID)).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Badge(c, role), t.Badge())
			design.Apply(ui.Text(c, message.Text), t.TextMuted(design.TypeXS))
		})
	default:
		buildAssistantMessage(c, t, runtime, message, st, canRegenerate, canCopy, defaultOpen)
	}
}

// buildAssistantMessage renders one assistant turn under the work-steps policy.
// While it streams, compact/standard show one live activity line plus the reply
// body and decision cards, while detailed/verbose keep the whole timeline; once
// finished, compact/standard/detailed fold the process into one summary row (open
// by default only for the latest turn under collapse=next-input), while verbose
// always shows the full timeline under its meta row. A failed turn never folds.
func buildAssistantMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state, canRegenerate, canCopy, defaultOpen bool) {
	policy := st.workStepsPolicyFor()
	foldable := assistantProcessFoldable(message, policy.mode)
	open, explicit := st.processOpen[message.ID]
	if !explicit {
		open = defaultOpen
	}
	open = open && foldable
	outer := ui.Column(c.Key("session.message." + message.ID)).FillWidth().Gap(design.Space2)
	outer.Children(func() {
		// The finished turn's meta (time/usage/⋯) is a header: on a folded turn
		// it shares the 过程 summary row, otherwise it is the row above the reply.
		meta := func(c *ui.Context) {
			buildAssistantMetaContent(c, t, runtime, st, message, canRegenerate)
		}
		switch {
		case message.Streaming:
			buildAssistantStreaming(c, t, runtime, st, message, policy)
		case policy.mode == contracts.WorkStepsVerbose:
			// verbose: the full timeline, always open, under the meta row, with no
			// fold summary row.
			buildAssistantMetaRow(c, t, runtime, st, message, canRegenerate)
			buildAssistantTimeline(c, t, runtime, st, message)
		case foldable:
			open = buildProcessSummary(c, t, runtime, st, message, open, meta)
			if open {
				buildAssistantTimeline(c, t, runtime, st, message)
			} else {
				buildAssistantReply(c, t, runtime, st, message)
			}
		default:
			// A pure-text turn has no process to fold: meta row, then the reply.
			buildAssistantMetaRow(c, t, runtime, st, message, canRegenerate)
			buildAssistantTimeline(c, t, runtime, st, message)
		}
		if message.Error != "" {
			buildMessageError(c, t, st, message)
			// A failed turn keeps one obvious way out: the labeled 重试 action
			// under the error, enabled even though the turn has no text to copy.
			if canRegenerate {
				buildRetryAction(c, t, runtime, st)
			}
		}
		if st.regenerateErr != "" && canRegenerate && strings.TrimSpace(message.Error) == "" {
			buildErrorLine(c, t, st.regenerateErr)
		}
		if canCopy {
			buildReplyCopyButton(c, t, st, message)
		}
	})
}

// buildAssistantStreaming draws a running turn under the work-steps policy:
// compact/standard replace the live timeline with one activity line (then the
// streaming body and any decision card), while detailed/verbose keep the
// interleaved timeline and the generating indicator.
func buildAssistantStreaming(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage, policy workStepsPolicy) {
	switch policy.mode {
	case contracts.WorkStepsCompact:
		buildLiveWorkSteps(c, t, st, message, false)
	case contracts.WorkStepsStandard:
		buildLiveWorkSteps(c, t, st, message, true)
	default:
		buildAssistantTimeline(c, t, runtime, st, message)
		// The generating indicator is the shared run-status row (S8): the
		// official spinner with its status copy, never a fake progress bar.
		uikit.RunStatus(c, uikit.RunStatusOptions{
			Running:     true,
			StatusLabel: st.tr("生成中", "Generating"),
			Locale:      st.locale,
		})
		return
	}
	// The single activity line replaces the timeline, not the answer: the
	// streaming prose and any decision the turn waits on stay visible.
	renderAssistantText(c, t, st, message)
	buildProminentDecisions(c, t, runtime, st, message)
}

// assistantProcessFoldable reports whether a finished turn folds its process row:
// any successful turn with a tool call or some reasoning folds under
// compact/standard/detailed. verbose never folds; streaming and failed turns
// render inline (their own rows carry the story).
func assistantProcessFoldable(message contracts.ChatMessage, mode string) bool {
	if message.Streaming || strings.TrimSpace(message.Error) != "" ||
		mode == contracts.WorkStepsVerbose {
		return false
	}
	return len(message.Tools) > 0 || strings.TrimSpace(message.Reasoning) != ""
}

// buildProcessSummary draws the folded process line as a bare disclosure (the
// product has no public component for it): one muted summary with the chevron
// right after the text, and the turn's meta (time/usage/⋯) at the trailing edge
// of the same row, so a folded turn spends one row instead of two. It returns
// the resulting fold state; a click stores it in the transcript state and asks
// for HoldPlace so the row keeps its place.
func buildProcessSummary(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state,
	message contracts.ChatMessage, open bool, meta func(c *ui.Context)) bool {
	return buildProcessFoldRow(c, t, runtime, st, message.ID, open,
		processSummaryParts(st, message.Reasoning, len(message.Tools)), meta)
}

// buildProcessFoldRow is the one disclosure row a folded process renders: the
// chevron, the summary and the turn's meta (time/usage/⋯) on one row. The fold
// key is the message ID for a single turn and the first message ID for a grouped
// multi-step turn, so both remember their state the same way.
func buildProcessFoldRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state,
	key string, open bool, parts []string, meta func(c *ui.Context)) bool {
	summary := strings.Join(parts, " · ")
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		// The disclosure is created inside the row so its trigger is a row
		// child; the meta parts drawn after it share the same row.
		end := ui.CollapsibleBase(c.Key("session.process."+key), &open)
		trigger := end.Trigger.Grow(1).AlignItems(ui.Center).Gap(design.Space2).
			PaddingY(design.Space1).Radius(design.RadiusSM).Label(summary)
		if end.Trigger.Hovered() || end.Trigger.Focused() {
			trigger = trigger.Background(t.BgHover)
		}
		trigger.Children(func() {
			// The fold header carries the same fixed 16 DIP marker column as the
			// activity rows, with the disclosure chevron as its marker.
			ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
				ui.Icon(c, icons.ChevronRight()).Size(design.Space4, design.Space4).
					TextColor(t.LabelTertiary).Rotate(90 * end.Progress())
			})
			design.Apply(ui.Text(c, summary), t.Font(design.TypeXXS)).TextColor(t.LabelSecondary)
		})
		// The meta parts are siblings of the trigger, right-aligned by the
		// growing trigger; the ⋯ click never falls through to the fold toggle.
		if meta != nil {
			meta(c)
		}
		if end.Trigger.Changed() {
			st.processOpen[key] = open
			st.chatHoldPlace = true
			runtime.Invalidate()
		}
	})
	return open
}

// processSummaryParts is the fold summary copy: 过程, 思考 (when the turn
// reasoned) and the tool count. Duration/tokens live in the meta half of the
// same row, never twice.
func processSummaryParts(st *state, reasoning string, tools int) []string {
	parts := []string{st.tr("过程", "Process")}
	if strings.TrimSpace(reasoning) != "" {
		parts = append(parts, st.tr("思考", "Reasoning"))
	}
	if tools > 0 {
		if tools == 1 {
			parts = append(parts, st.tr("1 个工具调用", "1 tool call"))
		} else {
			parts = append(parts, fmt.Sprintf(st.tr("%d 个工具调用", "%d tool calls"), tools))
		}
	}
	return parts
}

// assistantTurnContinues reports whether next is another step of the same
// assistant turn: a finished process step (tools and no error) followed by the
// next finished assistant message. Such runs render as one transcript row: the
// whole process folds once and only the final result stays visible.
func assistantTurnContinues(prev, next contracts.ChatMessage) bool {
	if prev.Role != contracts.ChatRoleAssistant || next.Role != contracts.ChatRoleAssistant {
		return false
	}
	if prev.Streaming || next.Streaming || prev.Error != "" || next.Error != "" {
		return false
	}
	return len(prev.Tools) > 0
}

// buildAssistantTurnGroup renders a multi-step assistant turn as one folded
// process row: the steps' tool calls and reasoning are summarized once, the
// intermediate replies stay behind the fold, and only the final result (plus any
// decision card that is waiting on the user) is shown under it. Expanding the
// fold restores every step's full timeline in order.
func buildAssistantTurnGroup(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state,
	group []contracts.ChatMessage, canRegenerate, canCopy, defaultOpen bool) {
	key := group[0].ID
	open, explicit := st.processOpen[key]
	if !explicit {
		open = defaultOpen
	}
	tools := 0
	var reasoning strings.Builder
	for _, message := range group {
		tools += len(message.Tools)
		reasoning.WriteString(message.Reasoning)
	}
	// The group's meta reports the whole turn: summed duration/tokens on the
	// last message's clock and actions (copy/regenerate apply to that result).
	metaMessage := group[len(group)-1]
	metaMessage.Usage = groupedMessageUsage(group)
	ui.Column(c.Key("session.turn." + key)).FillWidth().Gap(design.Space2).Children(func() {
		open = buildProcessFoldRow(c, t, runtime, st, key, open,
			processSummaryParts(st, reasoning.String(), tools),
			func(c *ui.Context) {
				buildAssistantMetaContent(c, t, runtime, st, metaMessage, canRegenerate)
			})
		for index, message := range group {
			if open {
				buildAssistantTimeline(c, t, runtime, st, message)
				continue
			}
			if index == len(group)-1 {
				buildAssistantReply(c, t, runtime, st, message)
				continue
			}
			buildProminentDecisions(c, t, runtime, st, message)
		}
		if canCopy {
			buildReplyCopyButton(c, t, st, group[len(group)-1])
		}
	})
}

// canCopyReply reports whether a finished assistant reply has text worth a
// bottom copy button: the latest result, not a streaming turn or a failed one.
func canCopyReply(message contracts.ChatMessage) bool {
	return !message.Streaming && strings.TrimSpace(message.Error) == "" &&
		strings.TrimSpace(message.Text) != ""
}

// buildReplyCopyButton is the latest reply's bottom copy affordance: one quiet
// labeled button under the answer. It copies the same text as the ⋯ menu's
// 复制消息，so the two entries never disagree.
func buildReplyCopyButton(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	label := st.tr("复制", "Copy")
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		button := design.Apply(ui.Button(c, ""), t.SecondaryButton()).
			Height(design.Space8).PaddingX(design.Space3).
			Label(st.tr("复制回复", "Copy reply")).
			Tooltip(st.tr("复制这条回复", "Copy this reply"))
		button.Children(func() {
			ui.Icon(c, icons.Copy()).Size(design.Space3, design.Space3).
				TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
		})
		if button.Clicked() {
			c.WriteClipboard(message.Text)
			st.toast(contracts.NotifySuccess, st.tr("已复制", "Copied"))
		}
	})
}

// groupedMessageUsage sums a multi-step turn's usage: duration, billing tokens
// and iterations add up, while the context size is the size of the last request
// (the same rule as one message's ContextTokens).
func groupedMessageUsage(group []contracts.ChatMessage) *contracts.MessageUsage {
	var total contracts.MessageUsage
	any := false
	for _, message := range group {
		if message.Usage == nil {
			continue
		}
		any = true
		total.DurationMS += message.Usage.DurationMS
		total.PromptTokens += message.Usage.PromptTokens
		total.CompletionTokens += message.Usage.CompletionTokens
		total.TotalTokens += message.Usage.TotalTokens
		total.CachedTokens += message.Usage.CachedTokens
		total.Iterations += message.Usage.Iterations
		if message.Usage.ContextTokens > 0 {
			total.ContextTokens = message.Usage.ContextTokens
		}
	}
	if !any {
		return nil
	}
	return &total
}

// buildAssistantReply draws the folded turn's visible reply: the Markdown body
// and the prominent decision cards (ask_user / plan_review / exit_plan_mode) in
// call order, while the timeline rows stay folded behind the summary.
func buildAssistantReply(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, message contracts.ChatMessage) {
	renderAssistantText(c, t, st, message)
	buildProminentDecisions(c, t, runtime, st, message)
}

// buildProminentDecisions draws the decision cards (ask_user / plan_review /
// exit_plan_mode) of one step: a folded process must never hide a decision the
// turn is waiting on, so these stay visible while the rest of the step folds.
func buildProminentDecisions(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, message contracts.ChatMessage) {
	for _, tool := range message.Tools {
		if prominentTool(tool.Name) {
			buildAssistantToolRow(c, t, runtime, st, message.ID, tool, false)
		}
	}
}

// renderAssistantText draws the reply prose: the official Markdown view for a
// finished turn, and the streaming view while the model writes.
func renderAssistantText(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	if message.Text == "" {
		return
	}
	if message.Streaming {
		markdown.Stream(c, message.Text, st.markdownOptions(t))
		return
	}
	markdown.View(c, message.Text, st.markdownOptions(t))
}

// buildAssistantTimeline draws the expanded process: the activity band
// (reasoning and compact tool rows, indented Space4, Space1 row spacing, inline
// chevrons) interleaved with the reply prose in the recorded order.
func buildAssistantTimeline(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, message contracts.ChatMessage) {
	timeline := ui.Column(c).FillWidth().Gap(design.Space1)
	timeline.Children(func() {
		if strings.TrimSpace(message.Reasoning) != "" {
			ui.Column(c).FillWidth().PaddingStart(design.Space4).Children(func() {
				buildReasoningDisclosure(c, t, st, message)
			})
		}
		renderAssistantBody(c, t, runtime, st, message)
	})
}

// renderAssistantBody draws one assistant reply in event order. Live turns
// record where in the reply each tool call arrived (ToolCall.TextOffset, gated
// by ChatMessage.ToolTimelineKnown): the transcript reads text before the
// call, the call's row, then the text that followed it. An all-zero offset set
// on a live message is valid order — every call happened before any prose —
// and renders the cards above the reply (the reported screenshot bug). Messages
// whose timeline was never recorded keep the old whole-text-then-cards
// fallback; tool-only replies are unchanged.
func renderAssistantBody(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage) {
	renderText := func(text string) {
		if text == "" {
			return
		}
		if message.Streaming {
			markdown.Stream(c, text, st.markdownOptions(t))
		} else {
			markdown.View(c, text, st.markdownOptions(t))
		}
	}
	renderTool := func(tool contracts.ToolCall) {
		buildAssistantToolRow(c, t, runtime, st, message.ID, tool, true)
	}

	text := message.Text
	if len(message.Tools) == 0 {
		renderText(text)
		return
	}
	if text == "" {
		for _, tool := range message.Tools {
			renderTool(tool)
		}
		return
	}
	// Only the explicit per-message flag certifies the offsets. A legacy or
	// not-yet-hydrated message may have no offsets at all (all zero), which is
	// indistinguishable from a live turn whose calls all came first, so the
	// heuristic "any offset > 0" cannot decide: the flag does.
	if !message.ToolTimelineKnown {
		renderText(text)
		for _, tool := range message.Tools {
			renderTool(tool)
		}
		return
	}
	// Chronological segments: [prev:offset] text, the call, then the next
	// segment. A tool whose offset went backwards (or past the end) is clamped,
	// so calls that arrive together render consecutively.
	pos := 0
	for _, tool := range message.Tools {
		offset := tool.TextOffset
		if offset < pos {
			offset = pos
		}
		if offset > len(text) {
			offset = len(text)
		}
		renderText(text[pos:offset])
		renderTool(tool)
		pos = offset
	}
	renderText(text[pos:])
}

// prominentTool reports whether a tool call is a decision/interaction rather
// than background tool noise: those keep the full card in the transcript.
func prominentTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "plan_review", "exit_plan_mode", "ask_user":
		return true
	default:
		return false
	}
}

// buildAssistantToolRow draws one transcript tool call: a registered renderer
// wins (its own card layout), decision tools keep the prominent card, and every
// other call uses the shared compact disclosure row, placed inside the activity
// band when inBand is set.
func buildAssistantToolRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, messageID string, tool contracts.ToolCall, inBand bool) {
	call := contracts.ToolCall{
		ID: tool.ID, Name: tool.Name, Status: tool.Status,
		Summary: tool.Summary, Arguments: tool.Arguments, TextOffset: tool.TextOffset,
	}
	key := "session.tool." + messageID + "." + tool.ID
	if runtime.RenderTool(c, call) {
		return
	}
	if prominentTool(tool.Name) {
		buildToolCard(c, st, call, key, 0, uikit.ToolCallCardDecision)
		return
	}
	summary := strings.TrimSpace(tool.Summary)
	open, seen := st.toolOpen[key]
	if !seen {
		// H15: a failure or a very short result is readable without a click;
		// long successful results stay folded.
		open = uikit.SummaryDefaultOpen(tool.Status, summary)
	}
	statusKind := uikit.ParseToolStatus(tool.Status).Kind()
	toggled := false
	// The expanded panel shows the call's input (arguments) and its output:
	// the same sections the registered tool cards use, so an MCP tool with no
	// renderer is as auditable as bash. Team-related calls also get the
	// cross-navigation footer to the inspector's team pane / task board.
	row := uikit.ToolRowOptions{
		Key:           key,
		Icon:          uikit.ToolIcon(tool.Name),
		Name:          uikit.ToolDisplayName(st.locale, tool.Name),
		RawName:       tool.Name,
		Status:        uikit.ToolStatusLabel(st.locale, tool.Status),
		StatusColor:   uikit.StatusColor(t, statusKind),
		Running:       statusKind == uikit.StatusRunning,
		Preview:       uikit.ToolPreview(summary, 96),
		Open:          open,
		InlineChevron: inBand,
		MarkerColumn:  inBand,
		Toggle: func(next bool) {
			toggled = true
			st.toolOpen[key] = next
			// The row grows/shrinks: keep the transcript where it is for this
			// layout so the clicked line does not jump (list.md HoldPlace).
			st.chatHoldPlace = true
			runtime.Invalidate()
		},
		Sections: toolcards.PanelSections(st.locale, call),
		Footer:   toolcards.TeamToolFooter(runtime, st.locale, tool.Name),
	}
	draw := func() { uikit.ToolRow(c, row) }
	if inBand {
		ui.Column(c).FillWidth().PaddingStart(design.Space4).Children(func() {
			draw()
		})
	} else {
		draw()
	}
	if !seen && !toggled {
		// Remember the H15 default as the reader's state only when they did not
		// just toggle it in this frame.
		st.toolOpen[key] = open
	}
}

// buildReasoningDisclosure draws the reply's thinking through the shared
// ReasoningTrace entry: open with the official spinner while the model thinks,
// folded to one quiet row (glue glyph, 思考, line count) once the turn ends, the
// muted reasoning Markdown in the panel. The activity band wants the chevron
// right after the label, so InlineChevron is on.
func buildReasoningDisclosure(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	if message.Reasoning == "" {
		return
	}
	open, chosen := st.thinkOpen[message.ID]
	if !chosen {
		open = message.Streaming
	}
	messageID := message.ID
	uikit.ReasoningTrace(c, uikit.ReasoningTraceOptions{
		Key:           "session.reasoning." + messageID,
		Reasoning:     message.Reasoning,
		Streaming:     message.Streaming,
		Open:          &open,
		InlineChevron: true,
		MarkerColumn:  true,
		OnToggle: func(next bool) {
			st.thinkOpen[messageID] = next
		},
		HoldPlace: &st.chatHoldPlace,
		Locale:    st.locale,
		Markdown:  st.reasoningMarkdownOptions(t),
	})
}

// buildAssistantMetaContent draws the finished turn's compact meta parts:
// time, the usage summary and the ⋯ message-actions menu. It draws no row of its
// own so the folded process row can host it at its trailing edge, next to the
// 过程 summary, instead of spending a separate row under the turn.
func buildAssistantMetaContent(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage, canRegenerate bool) bool {
	chat, hasText := messageActionTarget(st, message)
	regenerate := canRegenerate && strings.TrimSpace(message.Error) == ""
	hasUsage := message.Usage != nil
	// A failed turn keeps its own error row and the labeled retry; its meta row
	// only reports time/usage (no actions that would judge an unfinished answer).
	hasMenu := strings.TrimSpace(message.Error) == "" && (hasText || regenerate || hasUsage)
	hasTime := !message.CreatedAt.IsZero()
	if !hasTime && !hasMenu {
		return false
	}
	summary := usageSummaryText(st, message.Usage)
	if hasTime {
		buildMessageTime(c, t, st.locale, message.CreatedAt)
	}
	if summary != "" {
		if hasTime {
			metaSeparator(c, t)
		}
		design.Apply(ui.Text(c, summary), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).NoWrap()
	}
	if hasMenu {
		if hasTime || summary != "" {
			metaSeparator(c, t)
		}
		buildMessageMoreButton(c, t, "session.actions."+message.ID,
			st.tr("消息操作", "Message actions"), func(m *ui.Menu) {
				buildAssistantActionsMenu(m, c, runtime, st, chat, message,
					canRegenerate, hasText)
			})
	}
	return true
}

// buildAssistantMetaRow wraps the meta content in a right-aligned row. It is the
// header line of a turn without a folded process summary, so a finished turn
// never ends with an isolated ⋯ row.
func buildAssistantMetaRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage, canRegenerate bool) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1).Children(func() {
		ui.Spacer(c).Grow(1)
		buildAssistantMetaContent(c, t, runtime, st, message, canRegenerate)
	})
}

// buildAssistantActionsMenu fills the ⋯ menu of a finished turn: copy, regenerate
// and the compact usage-detail submenu.
func buildAssistantActionsMenu(m *ui.Menu, c *ui.Context, runtime contracts.UIRuntime,
	st *state, chat contracts.ChatService, message contracts.ChatMessage,
	canRegenerate, hasText bool) {
	if hasText {
		if m.Item(st.tr("复制消息", "Copy message")).Chosen() {
			c.WriteClipboard(message.Text)
			st.toast(contracts.NotifySuccess, st.tr("已复制", "Copied"))
		}
	}
	if canRegenerate && strings.TrimSpace(message.Error) == "" {
		if m.Item(st.tr("重新生成", "Regenerate")).Chosen() {
			runRegenerate(st, runtime)
		}
	}
	if message.Usage != nil {
		m.Separator()
		m.Submenu(st.tr("用量明细", "Usage details"), func(sub *ui.Menu) {
			info := func(label, value string) {
				sub.Item(label).Trailing(value).Disabled(true)
			}
			info(st.tr("时长", "Duration"), formatUsageDuration(message.Usage.DurationMS))
			info(st.tr("输入 tokens", "Prompt tokens"),
				uikit.FormatCompactCount(message.Usage.PromptTokens))
			info(st.tr("输出 tokens", "Completion tokens"),
				uikit.FormatCompactCount(message.Usage.CompletionTokens))
			if message.Usage.CachedTokens > 0 {
				info(st.tr("缓存 tokens", "Cached tokens"),
					uikit.FormatCompactCount(message.Usage.CachedTokens))
			}
			if window := st.usageContextWindowSize(); window != "" {
				info(st.tr("上下文预算", "Context budget"), window)
			}
		})
	}
}

// buildMessageMoreButton is the compact ⋯ entry of one message's meta row: a
// 24 DIP icon button (16 DIP glyph) opening the drawn actions menu, named and
// tooltipped for the pointer and assistive technology.
func buildMessageMoreButton(c *ui.Context, t *design.Tokens, key, label string, build func(m *ui.Menu)) {
	button := ui.Button(c.Key(key), "").Label(label).Tooltip(label).
		Size(t.Scale(design.Space6), t.Scale(design.Space6)).
		PaddingX(0).Radius(design.RadiusSM)
	button.Children(func() {
		ui.Icon(c, icons.More()).Size(t.Scale(design.Space4), t.Scale(design.Space4)).
			TextColor(t.LabelTertiary)
	})
	button.PopoverMenuButton(build)
}

// metaSeparator is the muted middle dot between the parts of a compact meta row
// (the row gap supplies the spacing around it).
func metaSeparator(c *ui.Context, t *design.Tokens) {
	design.Apply(ui.Text(c, "·"), t.Font(design.TypeXXXS)).
		TextColor(t.LabelTertiary).NoWrap()
}

// usageSummaryText is the one-line usage summary of a turn, in the product's
// compact units: "用时 3.2s · 1.2K tokens".
func usageSummaryText(st *state, usage *contracts.MessageUsage) string {
	if usage == nil {
		return ""
	}
	return st.tr("用时 ", "Took ") + formatUsageDuration(usage.DurationMS) +
		" · " + uikit.FormatCompactCount(usageTotalTokens(usage)) + " tokens"
}

// buildUserMessageMeta is the compact meta row under a user bubble: the message
// time and one ⋯ menu with copy (original text) and edit. Edit fills the draft
// and asks for the keyboard, but never sends: the user reviews and submits the
// edited message themselves. A message with only attachments keeps its time.
func buildUserMessageMeta(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage) {
	hasText := strings.TrimSpace(message.Text) != ""
	hasTime := !message.CreatedAt.IsZero()
	if !hasText && !hasTime {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1).Children(func() {
		ui.Spacer(c).Grow(1)
		if hasTime {
			buildMessageTime(c, t, st.locale, message.CreatedAt)
		}
		if !hasText {
			return
		}
		if hasTime {
			metaSeparator(c, t)
		}
		buildMessageMoreButton(c, t, "session.actions."+message.ID,
			// A distinct name from the assistant meta row's 消息操作: assistive
			// technology (and frame tests) can tell the two ⋯ entries apart when
			// a turn has both a user and an assistant row.
			st.tr("用户消息操作", "User message actions"), func(m *ui.Menu) {
				if m.Item(st.tr("复制原文", "Copy original")).Chosen() {
					c.WriteClipboard(message.Text)
					st.toast(contracts.NotifySuccess, st.tr("已复制", "Copied"))
				}
				if m.Item(st.tr("编辑消息", "Edit message")).Chosen() {
					editUserMessageDraft(st, runtime, message)
				}
			})
	})
}

// editUserMessageDraft puts one user message back into the composer without
// sending it: the draft replaces the composer text, any suggestion menu the text
// would open is suppressed, and the keyboard goes to the composer.
func editUserMessageDraft(st *state, runtime contracts.UIRuntime, message contracts.ChatMessage) {
	st.draft = message.Text
	st.slashDismissed = st.draft
	st.mentionDismissed = st.draft
	st.focusComposer = true
	runtime.Invalidate()
}

// runRegenerate re-runs the last exchange of the active conversation through
// the optional SessionRegenerator. A failure stays on the message as an error
// line instead of a toast: the message is where the action was.
func runRegenerate(st *state, runtime contracts.UIRuntime) {
	chat, _ := st.ctx.Get(contracts.KeyChat)
	regenerator, ok := chat.(contracts.SessionRegenerator)
	if !ok || chat == nil {
		return
	}
	sessionID := chat.ActiveSession()
	if sessionID == "" {
		return
	}
	if err := regenerator.Regenerate(sessionID); err != nil {
		st.regenerateErr = st.tr("重新生成失败：", "Regenerate failed: ") + err.Error()
	} else {
		st.regenerateErr = ""
	}
	runtime.Invalidate()
}

// buildRetryAction is the failed turn's one obvious retry: a labeled secondary
// button under the error row, enabled even when the turn has no text to copy.
func buildRetryAction(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	_ = t
	uikit.RunStatus(c, uikit.RunStatusOptions{
		Retryable: true,
		OnRetry: func() {
			runRegenerate(st, runtime)
		},
		Locale: st.locale,
	})
	if st.regenerateErr != "" {
		buildErrorLine(c, t, st.regenerateErr)
	}
}

// messageClock formats one message's time of day (HH:MM; English uses the
// locale's 12-hour form) for the quiet meta row. The full date and time stay in
// the tooltip.
func messageClock(locale contracts.LocaleService, instant time.Time) string {
	if instant.IsZero() {
		return ""
	}
	instant = instant.Local()
	if uikit.Language(locale) == "en" {
		return instant.Format("3:04 PM")
	}
	return instant.Format("15:04")
}

// buildMessageTime renders the quiet time-of-day of one message in its meta row,
// with the full timestamp in the tooltip. An unset CreatedAt renders nothing
// (imported/legacy messages without time metadata stay clean).
func buildMessageTime(c *ui.Context, t *design.Tokens, locale contracts.LocaleService, instant time.Time) {
	clock := messageClock(locale, instant)
	if clock == "" {
		return
	}
	design.Apply(ui.Text(c, clock), t.Font(design.TypeXXXS)).
		TextColor(t.LabelTertiary).NoWrap().
		Tooltip(uikit.FormatDateTime(locale, instant))
}

// messageActionTarget resolves the action affordances of one assistant turn:
// the optional chat backend (for regenerate) and whether the turn has text to
// copy.
func messageActionTarget(st *state, message contracts.ChatMessage) (chat contracts.ChatService, hasText bool) {
	hasText = strings.TrimSpace(message.Text) != ""
	chat, _ = st.ctx.Get(contracts.KeyChat)
	return chat, hasText
}

// buildMessageError renders a failed assistant turn: a localized kind heading
// with actionable guidance, plus the original backend text as muted diagnostic
// copy so no information is lost. Unknown/other kinds keep the raw line.
func buildMessageError(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	kind := strings.ToLower(strings.TrimSpace(message.ErrorKind))
	heading, reason := "", ""
	switch kind {
	case contracts.MessageErrorKindAuth:
		heading, reason = st.tr("鉴权失败", "Authentication failed"), st.tr("请检查 API Key", "check the API key")
	case contracts.MessageErrorKindRateLimit:
		heading, reason = st.tr("请求过于频繁", "Rate limited"), st.tr("请稍后重试", "try again later")
	case contracts.MessageErrorKindNetwork:
		heading = st.tr("网络连接失败", "Network connection failed")
	case contracts.MessageErrorKindTimeout:
		heading = st.tr("请求超时", "Request timed out")
	case contracts.MessageErrorKindContext:
		heading, reason = st.tr("上下文超限", "Context limit reached"), st.tr("请压缩或新建会话", "compact or start a new conversation")
	case contracts.MessageErrorKindCancelled:
		heading = st.tr("已取消", "Cancelled")
	}
	if heading == "" {
		buildErrorLine(c, t, message.Error)
		return
	}
	// A cancelled turn is not a failure: keep it one quiet, neutral line and
	// never repeat the raw backend spelling ("cancelled", "context canceled")
	// under it. Real errors keep the localized heading plus the diagnostic copy.
	if kind == contracts.MessageErrorKindCancelled {
		design.Apply(ui.Text(c, st.tr("已取消", "Cancelled")), t.TextMuted(design.TypeXS))
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		uikit.ErrorLine(c, heading+st.localizedReasonSuffix(reason))
		design.Apply(ui.Text(c, message.Error), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(3)
	})
}

// localizedReasonSuffix joins a guidance reason to the heading separator.
func (st *state) localizedReasonSuffix(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return st.tr("：", ": ") + reason
}

// sessionErrorVisible reports whether the session-level error row should be
// rendered. When a failed turn already carries its own (localized) error row in
// the transcript, adding the session-level raw error below it renders the same
// failure twice (verification ZK-05); the session-level row is only the fallback
// for errors that are not attached to a message.
func sessionErrorVisible(messages []contracts.ChatMessage, sessionErr string) bool {
	if strings.TrimSpace(sessionErr) == "" {
		return false
	}
	for _, message := range messages {
		if strings.TrimSpace(message.Error) != "" {
			return false
		}
	}
	return true
}

// buildErrorLine is the brand danger row (icon + WCAG-AA danger text).
func buildErrorLine(c *ui.Context, t *design.Tokens, text string) {
	uikit.ErrorLine(c, text)
}

// buildToolCard routes one tool call through the shared complete card
// (uikit.ToolCallCard): the same trigger head as the compact row (category icon,
// friendly localized name, WCAG-AA status with the official spinner while
// running), the optional trajectory step, and the official detail disclosure
// with the copyable input/output sections and the team footer. The transcript
// uses the decision variant for prominent decision tools (plan review, exit
// plan, ask user), so their card stays visible even without a detail body; the
// trajectory timeline shows every call as the normal variant.
func buildToolCard(c *ui.Context, st *state, call contracts.ToolCall, key string, step int,
	variant uikit.ToolCallCardVariant) {
	summary := strings.TrimSpace(call.Summary)
	open, seen := st.toolOpen[key]
	if !seen {
		// H15: a failure or a very short result is readable without a click;
		// long successful results stay folded.
		open = uikit.SummaryDefaultOpen(call.Status, summary)
	}
	toggled := false
	uikit.ToolCallCard(c, uikit.ToolCallCardOptions{
		Key:      key,
		Call:     call,
		Locale:   st.locale,
		Sections: toolcards.PanelSections(st.locale, call),
		Footer:   toolcards.TeamToolFooter(st.runtime, st.locale, call.Name),
		Open:     &open,
		OnToggle: func(next bool) {
			toggled = true
			st.toolOpen[key] = next
		},
		Steps:   step,
		Variant: variant,
	})
	if !seen && !toggled {
		st.toolOpen[key] = open
	}
}

// --- trajectory / memory ----------------------------------------------------

// trajectoryItem is one tool call of the tool timeline.
type trajectoryItem struct {
	key  string
	tool contracts.ToolCall
}

// trajectoryItems flattens the session's tool calls in message order.
func trajectoryItems(chatSession contracts.ChatSession) []trajectoryItem {
	items := make([]trajectoryItem, 0, 8)
	for messageIndex, message := range chatSession.Messages {
		for toolIndex, tool := range message.Tools {
			items = append(items, trajectoryItem{
				key:  fmt.Sprintf("%d-%d-%s", messageIndex, toolIndex, tool.ID),
				tool: tool,
			})
		}
	}
	return items
}

// buildTrajectoryPane is the tool timeline: the same card anatomy as the
// transcript's tool cards on the shared reading column, following the end while
// the stream runs, with the floating "back to latest" action. The tab already
// says 轨迹, so the pane carries the count as a muted line instead of repeating
// the title and a badge.
func buildTrajectoryPane(c *ui.Context, t *design.Tokens, chatSession contracts.ChatSession, hasSession bool, st *state) {
	items := trajectoryItems(chatSession)
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		if len(items) > 0 {
			design.Apply(ui.Text(c, fmt.Sprintf(st.tr("%d 个工具调用", "%d tool calls"), len(items))),
				t.TextMuted(design.TypeXXS)).PaddingX(design.Space5)
		}
		switch {
		case !hasSession:
			uikit.EmptyStateBlock(c,
				st.tr("还没有会话，暂无可查看的轨迹。", "No session yet; nothing to inspect."),
				st.tr("开始一段对话后，工具调用会出现在这里。", "Start a conversation and its tool calls show here."), nil)
		case len(items) == 0:
			uikit.EmptyStateBlock(c,
				st.tr("本次会话还没有工具调用。", "No tool calls in this conversation yet."),
				st.tr("它的调用步骤会按时间顺序列在这里。", "Its steps are listed here in order."), nil)
		default:
			// The timeline is a collection: the official List keys, virtualizes
			// and scrolls its rows instead of a Scroll plus a tool-card loop.
			st.trajList.Key = func(index int) any {
				if index < 0 || index >= len(items) {
					return nil
				}
				return items[index].key
			}
			st.trajList.Label = func(index int) string {
				if index < 0 || index >= len(items) {
					return ""
				}
				return firstNonEmpty(uikit.ToolDisplayName(st.locale, items[index].tool.Name),
					st.tr("工具调用", "Tool call"))
			}
			ui.List(c, &st.trajList, len(items)).
				Label(st.tr("工具调用轨迹", "Tool call trajectory")).
				Gap(design.Space3).
				PaddingY(design.Space5).
				Grow(1).
				Rows(func(row ui.ListRow) {
					item := items[row.Index]
					ui.Row(row.Context).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
						design.Apply(ui.Column(row.Context), t.Gap(design.Space3)).
							Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
							buildToolCard(row.Context, st, item.tool, "session.trajectory."+item.key, row.Index+1,
								uikit.ToolCallCardNormal)
						})
					})
				})
		}
	})
}

// markdownCache keeps the renderer options for one appearance so the Markdown
// views do not rebuild their configuration (and reparse) every frame.
type markdownCache struct {
	options *markdown.Options
	dark    bool
	size    float32
}

// markdownOptions returns the brand Markdown configuration: body text at the
// design body size (14) with its 22 DIP line box, the default extension set and
// no reveal pacing (the reply is already complete when it is drawn; streaming
// uses the library's own healing parser).
func (st *state) markdownOptions(t *design.Tokens) *markdown.Options {
	size := st.messageFontSize()
	if st.md.options != nil && st.md.dark == t.Dark && st.md.size == size {
		return st.md.options
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = size
	theme.LineHeight = 22.0 / 14
	// The transcript's headings follow the app type scale instead of Markdown's
	// defaults: H1 17–18, H2 16, H3 15 at weight 600, all below the 20/600 page
	// title, so message content can never outrank the screen's own hierarchy.
	// The sizes are multipliers of the body size, so the small/large chat
	// font settings keep the same relation.
	heading := func(target float32) float32 {
		if size <= 0 {
			return 1
		}
		return target / size
	}
	h1 := size + 3.5
	if h1 < 17 {
		h1 = 17
	}
	if h1 > 18 {
		h1 = 18
	}
	theme.Headings[0].Size = heading(h1)
	theme.Headings[1].Size = heading(min(size+2, 16))
	theme.Headings[2].Size = heading(min(size+1, 15))
	theme.Headings[3].Size = 1
	theme.Headings[4].Size = 0.95
	theme.Headings[5].Size = 0.9
	for index := range theme.Headings {
		theme.Headings[index].Weight = 600
	}
	theme.Headings[5].Color = theme.Muted
	st.md.options = &markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
	}
	st.md.dark = t.Dark
	st.md.size = size
	return st.md.options
}

// reasoningMarkdownOptions returns the muted, slightly smaller Markdown
// configuration of the thinking panel (the shared ReasoningTrace theme): the
// reply's body size minus one DIP in the secondary color, kept subordinate to
// the reply's own headings. It follows the light/dark theme and the
// ui.chatFontSize setting like the prose; the theme itself is built by uikit and
// cached here per theme/dark/size so a frame does not rebuild it.
func (st *state) reasoningMarkdownOptions(t *design.Tokens) *markdown.Options {
	size := st.messageFontSize() - 1
	if size < 11 {
		size = 11
	}
	if st.mdReasoning.options != nil && st.mdReasoning.dark == t.Dark && st.mdReasoning.size == size {
		return st.mdReasoning.options
	}
	st.mdReasoning.options = uikit.ReasoningMarkdown(t, size)
	st.mdReasoning.dark = t.Dark
	st.mdReasoning.size = size
	return st.mdReasoning.options
}

// messageFontSize is the transcript body size from ui.chatFontSize:
// 12.5 (small), 14 (standard, the default) or 16 (large). Missing and
// malformed values fall back to standard.
func (st *state) messageFontSize() float32 {
	if st == nil || st.settings == nil {
		return 14
	}
	switch strings.ToLower(strings.TrimSpace(st.settings.Get(contracts.SettingChatFontSize, ""))) {
	case "small":
		return 12.5
	case "large":
		return 16
	default:
		return 14
	}
}

// memorySearchDebounce is how long the Memory tab waits after the last
// keystroke before reading the store: a typed word costs one read, not one
// synchronous query per character.
const memorySearchDebounce = 180 * time.Millisecond

// readMemoryNow refreshes the cached read model once (pane open or a mutation
// asked for it). Search typing never comes through here: query changes are
// debounced through scheduleMemorySearch instead.
func readMemoryNow(store contracts.MemoryStore, st *state, query string) {
	st.memorySeq++ // drop any search result still in flight
	st.memoryPendingQuery = ""
	st.memoryReading = false
	if query != "" {
		st.memories, st.memoriesErr = store.Search(context.Background(), query)
	} else {
		st.memories, st.memoriesErr = store.List(context.Background())
	}
	// A new result set starts at its top, not at the previous list offset.
	st.memoryList = ui.ListState{}
	st.memoriesQuery = query
	st.memoriesFresh = true
	st.memoryRefresh = false
}

// scheduleMemorySearch debounces one changed search query: the read starts on
// its own goroutine after the user stops typing (memorySearchDebounce), so a
// typed word costs one read instead of one synchronous SQLite query per
// keystroke. The frame clock drives the delay (c.After wakes the starting
// frame), so headless tests stay deterministic with Tester.Advance.
func scheduleMemorySearch(c *ui.Context, runtime contracts.UIRuntime, store contracts.MemoryStore, st *state, query string) {
	if st.memoryPendingQuery != query {
		st.memoryPendingQuery = query
		st.memorySearchDue = c.Now().Add(memorySearchDebounce)
		c.After(memorySearchDebounce)
		return
	}
	if !c.Now().Before(st.memorySearchDue) {
		startMemoryRead(runtime, store, st, query)
	}
}

// startMemoryRead reads one query on its own goroutine and publishes the result
// to the UI thread. A newer read (memorySeq) drops this one, so a result for an
// abandoned query never replaces the visible list.
func startMemoryRead(runtime contracts.UIRuntime, store contracts.MemoryStore, st *state, query string) {
	st.memoryRefresh = false
	st.memoryPendingQuery = ""
	st.memoryReading = true
	st.memorySeq++
	sequence := st.memorySeq
	go func() {
		var memories []contracts.Memory
		var err error
		if query != "" {
			memories, err = store.Search(context.Background(), query)
		} else {
			memories, err = store.List(context.Background())
		}
		runtime.Post(func() {
			if sequence != st.memorySeq {
				return
			}
			st.memories, st.memoriesErr = memories, err
			st.memoriesQuery = query
			st.memoriesFresh = true
			st.memoryReading = false
			// A new result set starts at its top, not at the previous offset.
			st.memoryList = ui.ListState{}
			runtime.Invalidate()
		})
	}()
}

// buildMemoryPane is the Memory tab's real panel: the workspace memory store's
// entries (or search results), divider rows with a row-end delete icon, and the
// add row. The read model is cached in state; a query change schedules one
// debounced background read (scheduleMemoryRead) and a mutation asks for an
// immediate refresh, so the frame builder never reads the store. Without a
// memory backend (contracts.KeyMemory is not provided) it shows the disabled
// empty state instead of failing.
func buildMemoryPane(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	store, hasStore := st.ctx.Get(contracts.KeyMemory)
	query := strings.TrimSpace(st.memoryQuery)
	if hasStore {
		// The cached read model is refreshed at most once per pane open or
		// mutation. Search typing never reads the store inside the frame: a
		// changed query schedules one debounced background read whose result
		// arrives through UIRuntime.Post (S-17).
		switch {
		case !st.memoriesFresh || st.memoryRefresh:
			readMemoryNow(store, st, query)
		case query == st.memoriesQuery:
			st.memoryPendingQuery = ""
		default:
			scheduleMemorySearch(c, runtime, store, st, query)
		}
	}
	memories := st.memories
	listErr := st.memoriesErr

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		centered := func(children func(*ui.Context)) {
			ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				design.Apply(ui.Column(c), t.Gap(design.Space4)).
					Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
					children(c)
				})
			})
		}
		if !hasStore {
			// The action row must BUILD a button: calling the deep link from the
			// callback itself opened the settings surface on every frame the tab
			// was visible.
			uikit.EmptyStateBlock(c,
				st.tr("记忆后端未启用", "Memory backend not enabled"),
				st.tr("在设置中启用记忆后，这里会显示已保存的记忆。",
					"Enable memory in settings and saved entries show here."), func() {
					open := design.Apply(ui.Button(c, st.tr("打开设置", "Open settings")),
						t.SecondaryButton())
					if open.Clicked() {
						openSettingsPage(st.navigator, settingsPageMemory)
					}
				})
			return
		}
		// Header: the 记忆 tab already names the page, so the search field and
		// the management row follow the tab, with no second page title.
		centered(func(c *ui.Context) {
			ui.SearchField(c.Key("session.memory.search"), &st.memoryQuery).
				Label(st.tr("搜索记忆", "Search memory")).Placeholder(st.tr("搜索记忆", "Search memory")).FillWidth()
			if st.memoryReading || st.memoryPendingQuery != "" {
				// The read happens off the UI thread; say so instead of leaving
				// the previous results looking current during the debounce.
				ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Spinner(c).Label(st.tr("搜索中…", "Searching…")).
						Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
					design.Apply(ui.Text(c, st.tr("搜索中…", "Searching…")),
						t.TextMuted(design.TypeXXXS))
				})
			}
			buildMemoryTools(c, t, runtime, store, st, len(memories))
			if listErr != nil {
				uikit.ErrorLine(c, st.tr("读取记忆失败：", "Failed to read memory: ")+listErr.Error())
			}
		})
		switch {
		case listErr != nil:
			// The error row above is the whole content.
		case st.memoryReading && len(memories) == 0:
			// The loading line above is the content; never flash the empty state
			// before the first read lands.
		case len(memories) == 0 && query != "":
			uikit.EmptyStateBlock(c, st.tr("没有匹配的记忆", "No matching memories"),
				st.tr("换一个关键词试试。", "Try another keyword."), nil)
		case len(memories) == 0:
			uikit.EmptyStateBlock(c, st.tr("还没有记忆", "No memories yet"),
				st.tr("写入一条记忆，后续会话都可以使用。", "Save a memory and later conversations can use it."), nil)
		default:
			// The entries are a collection: the official List virtualizes and
			// keys them, while the search/tools/add rows stay fixed above/below.
			st.memoryList.Key = func(index int) any {
				if index < 0 || index >= len(memories) {
					return nil
				}
				return memories[index].ID
			}
			st.memoryList.Label = func(index int) string {
				if index < 0 || index >= len(memories) {
					return ""
				}
				return memories[index].Text
			}
			ui.List(c, &st.memoryList, len(memories)).
				Label(st.tr("记忆列表", "Memories")).
				Grow(1).
				Rows(func(row ui.ListRow) {
					memory := memories[row.Index]
					ui.Row(row.Context).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
						design.Apply(ui.Column(row.Context), t.Gap(design.Space4)).
							Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
							memoryRow(row.Context, t, runtime, store, st, memory)
						})
					})
				})
		}
		// The add row stays fixed at the bottom; only the entries scroll.
		centered(func(c *ui.Context) {
			buildMemoryAdd(c, t, runtime, store, st)
		})
		buildMemoryEditDialog(c, t, runtime, store, st)
	})
}

// buildMemoryAdd is the native add row at the bottom of the Memory tab: a
// labeled input and the primary add action, no card wrapper. Enter submits the
// same action (uikit.OnTextKey-style hook) and an empty draft keeps the action
// disabled, so the keyboard path and the pointer path agree.
func buildMemoryAdd(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, store contracts.MemoryStore, st *state) {
	draft := strings.TrimSpace(st.memoryDraft)
	ui.Column(c.Key("session.memory.add")).FillWidth().Gap(design.Space2).
		PaddingY(design.Space3).Children(func() {
		ui.Divider(c)
		ui.TextInput(c, &st.memoryDraft).Label(st.tr("新建记忆", "New memory")).
			Placeholder(st.tr("输入要记住的内容", "Enter something to remember")).FillWidth().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key != ui.KeyEnter || k.Mods != 0 {
					return false
				}
				// Consume Enter either way: an empty draft is a no-op, not a
				// stray submit through some outer form.
				submitMemoryDraft(store, st, runtime)
				return true
			})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Spacer(c).Grow(1)
			add := design.Apply(ui.PrimaryButton(c, st.tr("添加记忆", "Add memory")), t.PrimaryButton()).
				Disabled(draft == "")
			if add.Clicked() && draft != "" {
				submitMemoryDraft(store, st, runtime)
			}
		})
		if st.memoryError != "" {
			uikit.ErrorLine(c, st.memoryError)
		}
	})
}

// submitMemoryDraft adds the trimmed composer draft to the workspace memory
// store and clears it on success; failures stay inline. The caller requests the
// refresh only after the store accepted the entry.
func submitMemoryDraft(store contracts.MemoryStore, st *state, runtime contracts.UIRuntime) {
	draft := strings.TrimSpace(st.memoryDraft)
	if draft == "" {
		return
	}
	if _, err := store.Add(context.Background(), contracts.Memory{
		Text:  draft,
		Scope: memoryScopeWorkspace,
	}); err != nil {
		st.memoryError = st.tr("添加记忆失败：", "Add failed: ") + err.Error()
	} else {
		st.memoryDraft = ""
		st.memoryError = ""
		st.toast(contracts.NotifySuccess, st.tr("已添加记忆", "Memory added"))
		requestMemoryRefresh(st)
	}
	runtime.Invalidate()
}

// formatTimestamp renders a display timestamp for the current language via
// uikit.FormatDateTime, so the session, inspector and memory rows can never use
// different date layouts; "—" when unset.
func formatTimestamp(locale contracts.LocaleService, value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return uikit.FormatDateTime(locale, value)
}

// memoryScopeWorkspace is the scope the session panel writes; the store owns
// the normalized value, this is the display fallback.
const memoryScopeWorkspace = "workspace"

// memoryScopeLabel renders a memory scope for the metadata line.
func memoryScopeLabel(st *state, scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "workspace", "工作区":
		return st.tr("工作区", "Workspace")
	case "global", "全局":
		return st.tr("全局", "Global")
	case "":
		return ""
	default:
		return scope
	}
}

// --- composer ---------------------------------------------------------------

// buildComposer is the conversation composer: the shared ComposerShell owns the
// card, the input, the widened-width policy, the send/queue/stop position and
// the default key semantics; this module keeps its own business — the slash/@
// suggestion menus, the queue state and actions, the session toolbar menus and
// the usage strip under the card.
func buildComposer(
	c *ui.Context,
	t *design.Tokens,
	runtime contracts.UIRuntime,
	navigator contracts.Navigator,
	chat contracts.ChatService,
	cfg Config,
	st *state,
	activeID string,
	chatSession contracts.ChatSession,
) {
	running := chatSession.Running
	queueAllowed := running && sendWhileRunningEnabled(st.settings)
	// Whether the slash command popover is the layer owning Enter/Esc right
	// now. It only exists while no turn runs, and an Esc-dismissed draft keeps
	// the menu closed until the draft changes. The same condition drives the
	// quiet hint text and the highlight reset below.
	slashMenuActive := false
	if _, active := slashQuery(st.draft); active && !running && st.slashDismissed != st.draft {
		slashMenuActive = true
	} else {
		// Reopening the menu starts on the first row again instead of resuming
		// a stale highlight from a previous open (C-21).
		st.slashSel = 0
	}
	workspace, _ := st.ctx.Get(contracts.KeyWorkspace)
	placeholder := st.localized(cfg.ComposerPlaceholder, "输入消息…", "Ask ZeoKeel…")
	sendLabel := st.localized(cfg.SendLabel, "发送", "Send")
	stopLabel := st.localized(cfg.StopLabel, "停止", "Stop")
	// The send tooltip carries the full key hint on narrow composers (C-23) and
	// on wide ones the visible line shows only the short form: the English copy
	// is long enough that the single-line hint beside the toolbar truncated at
	// the default 1280 window ("Shift+Enter fo…"). The complete key set
	// (Cmd/Ctrl+Enter included) stays in the send button's tooltip.
	const defaultHintZh = "Enter 发送 · Shift+Enter 换行"
	sendHint := st.localized(cfg.ComposerHint, defaultHintZh, "Enter to send · Shift+Enter newline")
	if trimmed := strings.TrimSpace(cfg.ComposerHint); trimmed == "" || trimmed == defaultHintZh ||
		trimmed == "Enter to send · Shift+Enter for newline" || trimmed == "Enter to send · Shift+Enter newline" {
		sendHint = composerHintText(st, running, slashMenuActive)
	}
	hasContent := strings.TrimSpace(st.draft) != "" || len(st.pendingAttachments) > 0
	hasDraft := strings.TrimSpace(st.draft) != ""
	queueTip := st.tr("当前回复尚未结束：加入排队，本轮结束后自动发送",
		"Still generating: queue it and send when this turn ends")
	if len(st.pendingAttachments) > 0 {
		queueTip = st.tr("本轮只发送文字；暂存的附件会保留在输入框",
			"Queue sends text only; staged attachments stay in the composer")
	}
	slashDraft := false
	if _, ok := slashQuery(st.draft); ok {
		slashDraft = true
	}
	var composerWidth float32
	compactControls := func() bool { return composerWidth > 0 && composerWidth < 800 }
	veryCompact := func() bool { return composerWidth > 0 && composerWidth < 480 }
	var queueSend func() bool
	if queueAllowed {
		queueSend = func() bool { return queueDraft(st, activeID) }
	}
	composerCard, composerInput := uikit.ComposerShell(c, uikit.ComposerShellOptions{
		Key:         "session.composer",
		Draft:       &st.draft,
		Placeholder: placeholder,
		Running:     running,
		CardLabel:   st.tr("会话输入框", "Session composer"),
		Locale:      st.locale,
		InputBind:   &st.composerInput,
		Focus:       &st.focusComposer,
		OnWidth:     func(width float32) { composerWidth = width },
		InputReady: func(ic *ui.Context, input ui.Element) {
			// The @ menu follows the caret (C-15): remember where the selection
			// ends from this frame's input so mentionQueryAt can find the token
			// around it. Accepting a suggestion (which replaces the text through
			// the key hook) leaves the editor's caret behind, so that frame moves
			// it after the reference instead of reading the stale offset.
			if st.composerCaretPending {
				st.composerCaretPending = false
				input.SetTextSelection(st.composerCaret, st.composerCaret)
			} else if _, caret := input.TextSelection(); caret >= 0 {
				st.composerCaret = caret
			}
		},
		Attachments: func(ic *ui.Context) {
			buildPendingAttachments(ic, t, runtime, st)
		},
		Notices: func(ic *ui.Context) {
			if running && !queueAllowed {
				// Queueing is off: the composer is editable but Enter does not
				// send. Say where the text goes instead of leaving the user
				// wondering why Enter inserted a line break.
				design.Apply(ui.Text(ic, st.tr("回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送",
					"Generating: Enter is unavailable; your draft is kept for when this turn ends")),
					t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(1)
			}
			if running && queueAllowed && len(st.pendingAttachments) > 0 {
				// The queue carries text only: say so instead of silently leaving
				// the staged files behind.
				design.Apply(ui.Text(ic, st.tr("本轮只发送文字；附件会保留在输入框，随下一条消息发送",
					"Queue sends text only; attachments stay in the composer for your next message")),
					t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(1)
			}
		},
		Queue:       st.queuedFor(activeID),
		QueueOpen:   &st.queueMenuOpen,
		QueueList:   &st.queueList,
		QueuePaused: st.queuePaused[activeID],
		QueueError:  st.queueErrorFor(activeID),
		OnQueueCancel: func(index int) {
			cancelQueuedPrompt(st, activeID, index)
			runtime.Invalidate()
		},
		OnQueueClear: func() {
			clearQueuedPrompts(st, activeID)
			runtime.Invalidate()
		},
		OnQueueResume: func() {
			resumeQueue(chat, st, activeID)
			runtime.Invalidate()
		},
		OnRetryQueue: func() {
			// The explicit retry is also the resume: clear the failure pause so
			// processQueuedPrompts runs again (a second failure re-pauses it).
			st.setQueueError(activeID, "")
			delete(st.queuePaused, activeID)
			processQueuedPrompts(chat, st, activeID)
			runtime.Invalidate()
		},
		Toolbar: func(ic *ui.Context) {
			buildComposerToolbar(ic, t, runtime, navigator, chat, st, running,
				compactControls(), veryCompact())
		},
		Hint: func(*ui.Context) string {
			// A quiet key hint on wide composers only (C-23): Enter sends,
			// Shift+Enter breaks the line, and a slash draft explains what Enter
			// will do (C-05/C-22). Narrow composers keep the full copy in the
			// send button's tooltip. While a turn runs only the command-draft
			// state gets a hint: the stop/queue controls speak for themselves.
			if compactControls() || (running && !slashDraft) {
				return ""
			}
			return composerVisibleHint(st, running, slashMenuActive)
		},
		SendEnabled: hasContent,
		SendLabel:   sendLabel,
		SendTooltip: sendHint,
		Send: func() bool {
			return submitDraft(chat, activeID, st)
		},
		QueueSend:    queueSend,
		QueueEnabled: hasDraft,
		QueueLabel:   st.tr("排队", "Queue"),
		QueueTooltip: queueTip,
		Stop: func() {
			cancelRunningTurn(chat, st, st.runtime, activeID)
		},
		StopLabel:   stopLabel,
		StopTooltip: stopLabel,
		OnKey:       composerTextKey(runtime, chat, st, activeID, running, queueAllowed),
		Usage: func(ic *ui.Context) {
			// The DSH-style usage strip sits directly under the composer card:
			// one muted line of session statistics (rounds, steps, speed, tokens,
			// cache/context) with the exact numbers in its tooltip.
			buildSessionStatusStrip(ic, t, chat, st, chatSession)
		},
	})
	// An empty draft forgets an earlier Esc dismissal: typing the same "/" or
	// "@..." again after clearing the input must reopen the menu instead of
	// silently staying dismissed for the rest of the session.
	if strings.TrimSpace(st.draft) == "" {
		st.slashDismissed = ""
		st.mentionDismissed = ""
	}
	// The suggestion menus are anchored above the composer (H8): they are an
	// overlay, so they never push the transcript around. The keyboard handling
	// stays in the composer's OnKey (Up/Down/Enter/Tab/Esc).
	mentionActive := false
	var suggestionRow ui.Element
	if query, tokenStart, tokenEnd, active := mentionQueryAt(st.draft, st.composerCaret); active && st.mentionDismissed != st.draft {
		mentionActive = true
		if composerCard.Valid() {
			suggestionRow = buildMentionPopover(c, t, runtime, workspace, chat, st, query, tokenStart, tokenEnd, composerCard)
		}
	}
	if !mentionActive {
		// The next @ opens on a fresh workspace listing.
		st.mentionFresh = false
	}
	if slashMenuActive {
		if query, active := slashQuery(st.draft); active && composerCard.Valid() {
			suggestionRow = buildSlashPopover(c, t, runtime, st, query, composerCard)
		}
	}
	if suggestionRow.Valid() && composerInput.Valid() {
		// The composer keeps the keyboard focus while a suggestion menu is open;
		// the highlighted option is its ActiveDescendant, so assistive technology
		// reads the option the arrows are on (mygo accessibility.md: a list box a
		// text area drives).
		composerInput.ActiveDescendant(suggestionRow)
	}
}

// composerTextKey is the composer's native key hook: the suggestion menus take
// the arrow keys, Enter/Tab and Esc while they are open. Enter (or Cmd/Ctrl+
// Enter) sends when idle and queues while a turn runs with queueing on; while a
// turn runs with queueing explicitly off, Enter inserts a newline and the draft
// is kept. Esc closes an open suggestion menu first and only then interrupts a
// running turn.
//
// Slash drafts: the open menu executes the highlighted command on Enter. A
// slash draft is never QUEUED as chat text while a turn runs (C-05: the menu is
// hidden then, and queueing "/help" would send it to the model). Once the menu
// is dismissed with Esc, or when nothing matches, Enter sends the draft
// literally (C-22), so a message that begins with "/" stays sendable; the
// composer hint says which of the two Enter will do.
func composerTextKey(runtime contracts.UIRuntime, chat contracts.ChatService, st *state, activeID string, running, queueAllowed bool) func(ui.TextKey) bool {
	return func(k ui.TextKey) bool {
		// Esc closes an open suggestion menu and leaves the draft untouched
		// (the dismissal is remembered until the draft changes again).
		if k.Key == ui.KeyEscape && k.Mods == 0 {
			if _, active := slashQuery(st.draft); active && st.slashDismissed != st.draft {
				st.slashDismissed = st.draft
				runtime.Invalidate()
				return true
			}
			if _, _, _, active := mentionQueryAt(st.draft, st.composerCaret); active && st.mentionDismissed != st.draft {
				st.mentionDismissed = st.draft
				runtime.Invalidate()
				return true
			}
			// No menu owns this Esc: fall through to the running-turn interrupt
			// below (menu-first, interrupt second).
		}
		// The slash menu only exists while no turn runs (its popover is not
		// built while running): never execute an invisible command from Enter.
		if query, active := slashQuery(st.draft); active && !running && st.slashDismissed != st.draft {
			if handleSlashMenuKey(k, runtime, st, query) {
				return true
			}
		}
		if query, tokenStart, tokenEnd, active := mentionQueryAt(st.draft, st.composerCaret); active && st.mentionDismissed != st.draft {
			if handleMentionMenuKey(k, runtime, chat, st, query, tokenStart, tokenEnd) {
				return true
			}
		}
		// Menu-first interrupt: with no suggestion menu open, Esc and Cmd+. stop
		// the running turn exactly like the 停止 control (a menu Esc already
		// returned above, so closing a menu never stops the turn by accident).
		if running && activeID != "" {
			switch {
			case k.Key == ui.KeyEscape && k.Mods == 0:
				// A pending approval owns Esc first: it answers "deny" instead of
				// stopping the turn the card belongs to.
				if denyPendingApproval(st, runtime) {
					return true
				}
				cancelRunningTurn(chat, st, runtime, activeID)
				return true
			case k.Key == ui.KeyPeriod && k.Mods == ui.Cmd:
				cancelRunningTurn(chat, st, runtime, activeID)
				return true
			}
		}
		// C-25: Cmd/Ctrl+Enter sends too, so the DSH/Codex habit works; Shift+
		// or Alt+Enter still break the line.
		if k.Key != ui.KeyEnter || (k.Mods != 0 && k.Mods != ui.Cmd) {
			return false
		}
		if running {
			// A running turn never calls chat.Send through Enter: queue the
			// draft when queueing is on, otherwise let Enter insert a newline
			// (the draft stays in the composer and is sent once the reply ends).
			if !queueAllowed {
				return false
			}
			// C-05: a slash draft is a command, not chat text. Queueing it would
			// send "/help" to the model after this turn; keep the draft for the
			// next idle moment and let the composer hint explain why.
			if _, slashDraft := slashQuery(st.draft); slashDraft {
				runtime.Invalidate()
				return true
			}
			queueDraft(st, activeID)
			runtime.Invalidate()
			return true
		}
		if submitDraft(chat, activeID, st) {
			k.SetText("")
		}
		return true
	}
}

// composerHintText is the composer's full key/state hint: it lives in the send
// button's tooltip and carries the complete key set (Enter / Cmd+Enter and
// Shift+Enter, C-23/C-25) plus the explanations of the slash-draft states (a
// running turn keeps a command draft for later, C-05; an Esc-dismissed or
// match-less slash draft is sent literally, C-22).
func composerHintText(st *state, running, slashMenuActive bool) string {
	if _, slashDraft := slashQuery(st.draft); slashDraft {
		switch {
		case running:
			return st.tr("命令在回合结束后可用；草稿已保留，Enter 暂不生效",
				"Commands are available after this turn; your draft is kept, Enter does nothing yet")
		case slashMenuActive:
			return st.tr("↑↓ 选择 · Enter 执行 · Esc 关闭列表",
				"Up/Down to choose · Enter runs · Esc closes the list")
		default:
			return st.tr("Enter 会把 /… 作为普通消息发出；清空后输入 / 可重新打开建议",
				"Enter sends /… as a normal message; clear it and type / to reopen the suggestions")
		}
	}
	return st.tr("Enter 或 Cmd/Ctrl+Enter 发送 · Shift+Enter 换行",
		"Enter or Cmd/Ctrl+Enter to send · Shift+Enter for a newline")
}

// composerVisibleHint is the short copy the wide composer's single line can
// always show in both languages without an ellipsis: the idle state is just
// "Enter 发送 · Shift+Enter 换行" / "Enter to send · Shift+Enter newline" (the
// full chord list stays in the send tooltip), while the slash-draft states keep
// their explanatory copy because it explains what Enter will do.
func composerVisibleHint(st *state, running, slashMenuActive bool) string {
	if _, slashDraft := slashQuery(st.draft); slashDraft {
		return composerHintText(st, running, slashMenuActive)
	}
	return st.tr("Enter 发送 · Shift+Enter 换行", "Enter to send · Shift+Enter newline")
}

// buildComposerToolbar is the composer's action row: one attach menu, the
// model chip and one session menu, followed by the fixed send/stop control.//
// The row used to expose model, permission, agent mode, persona and 会话设置 as
// separate chips plus standalone "/" and "路径" buttons: eight equally weighted
// controls under one input read as visual noise and gave the security/model
// state too much chrome. The current layout keeps at most four controls and
// folds the low-frequency choices into the session menu. Non-default security
// or mode states stay visible on the menu button itself (e.g. 所有权限, 计划),
// so hiding the chips never hides a state the user must notice.
func buildComposerToolbar(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state, running, compactControls, veryCompact bool) {
	activeID := chat.ActiveSession()
	// The attach menu stays available while a turn runs: with queue-while-
	// running the next message may be typed already, and its files must be
	// stageable too (only the picker itself being busy disables the action).
	buildAttachmentMenu(c, t, runtime, st, running)
	if !veryCompact {
		buildModelChip(c, t, runtime, navigator, chat, st, compactControls, veryCompact)
	}
	buildSessionMenu(c, t, runtime, navigator, chat, st, activeID, veryCompact)
}

// buildAttachmentMenu is the composer's single attach control: choosing files
// through the native picker or entering absolute paths manually are two items
// of one menu instead of two competing toolbar buttons. The picker itself runs
// off the UI thread (startAttachmentPick), so this button never blocks.
func buildAttachmentMenu(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, running bool) {
	label := st.tr("附件", "Attach")
	tip := st.tr("添加文件或图片，或手动输入路径", "Add files or images, or enter a path")
	if running {
		tip = st.tr("添加文件或图片，或手动输入路径；本轮结束后仍会保留",
			"Add files or images, or enter a path; they stay staged for your next message")
	}
	if st.attachBusy {
		label = st.tr("选择中…", "Choosing…")
		tip = st.tr("文件选择器打开中…", "The file picker is open…")
	}
	button := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if m.Item(st.tr("选择文件…", "Choose files…")).Disabled(st.attachBusy).Chosen() {
			startAttachmentPick(runtime, st)
		}
		if m.Item(st.tr("手动输入路径…", "Enter a path manually…")).
			Disabled(st.attachBusy).Chosen() {
			openAttachmentPathDialog(st, runtime)
		}
	}).Tooltip(tip)
	design.Apply(button, t.TextMuted(design.TypeXS))
}

// buildSessionMenu groups the low-frequency session choices (permission, agent
// mode, persona and prompt presets) behind one button. Its label keeps a
// non-default security/mode state visible.
func buildSessionMenu(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state, activeID string, veryCompact bool) {
	label, warn := st.sessionMenuLabel()
	button := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if veryCompact {
			model := strings.TrimSpace(chat.Config().Model)
			if model == "" {
				model = st.tr("未配置", "Not set")
			}
			m.Item(st.tr("模型：", "Model: ") + model).Disabled(true)
			// The model chip is folded away at this width, so its 推理等级
			// entry moves here; both use the same submenu builder.
			if thinking, ok := resolveSessionThinking(st, chat, activeID); ok {
				buildSessionThinkingRow(m, runtime, st, thinking, activeID)
			}
			if m.Item(st.tr("打开模型设置…", "Open model settings…")).Chosen() {
				openSettingsPage(navigator, settingsPageModel)
			}
			m.Separator()
		}
		buildSessionPermissionMenuItems(m, runtime, st)
		m.Separator()
		if buildSessionClearAllowedToolsItem(m, runtime, chat, st, activeID) {
			m.Separator()
		}
		buildSessionModeMenuItems(m, runtime, st)
		// The thinking strength moved into the model chip's compact 推理等级
		// submenu (a wide composer); repeating it here stacked the same choice
		// twice. Persona and prompt presets are lists that can grow with user
		// data: nested submenus keep the outer menu short enough to fit a
		// 704x720 window (the tail items stay reachable without scrolling).
		if setter, ok := chat.(contracts.SessionPresetSetter); ok && activeID != "" {
			if presets := effectiveAgentPresets(st); len(presets) > 0 {
				m.Separator()
				m.Submenu(st.tr("人设", "Persona"), func(sub *ui.Menu) {
					buildAgentPresetMenuItems(sub, st, runtime, activeID, setter)
				})
			}
		}
		m.Separator()
		m.Submenu(st.tr("插入预设", "Insert preset"), func(sub *ui.Menu) {
			buildSessionPresetSubmenuItems(sub, runtime, st)
		})
		m.Separator()
		if m.Item(st.tr("打开行为设置…", "Open behavior settings…")).Chosen() {
			openSettingsPage(navigator, settingsPageBehavior)
		}
	}).Tooltip(st.tr(
		"权限、对话模式、人设与提示词预设；模型与推理等级在输入框旁的模型菜单。权限模式：只读只自动允许只读/信息类工具；工作区可写（默认）另自动允许工作区内的文件读写，shell/网络仍会询问；所有权限不再弹窗。弹窗中的「本会话内始终允许」只对本会话生效。",
		"Permission, mode, persona and prompt presets; the model and reasoning level live in the composer's model menu. Permission: read-only auto-allows read-only/informational tools; workspace-write (default) also auto-allows file reads/writes in the workspace, shell/network still ask; all permissions stops every prompt. The dialog's Always allow in this session only covers this conversation."))
	if warn {
		button.TextColor(t.WarningText)
	} else {
		design.Apply(button, t.TextMuted(design.TypeXS))
	}
}

// sessionMenuLabel names the session menu button: a non-default security or
// agent-mode state owns the label (and warns), everything else is the quiet
// default label. The full state is repeated in the tooltip/menu.
func (st *state) sessionMenuLabel() (label string, warn bool) {
	if st.settings == nil {
		return st.tr("会话设置", "Session settings"), false
	}
	if strings.EqualFold(strings.TrimSpace(st.settings.Get(contracts.SettingAgentMode, "")), contracts.AgentModePlan) {
		return st.tr("计划模式", "Plan mode"), true
	}
	// The all-permissions state is the one warning-level security state; it
	// owns the label first. A non-default chat mode then owns it (so choosing
	// 精简/创作 stays visible) before the read-only/workspace permission label,
	// which is always shown so the active permission mode stays inspectable.
	switch permissionMode(st.settings) {
	case contracts.PermissionFull:
		return st.tr("所有权限", "All permissions"), true
	}
	switch currentChatModeOption(st.settings).key {
	case contracts.ChatModeMinimal:
		return st.tr("精简", "Minimal"), false
	case contracts.ChatModeCreator:
		return st.tr("创作", "Creator"), false
	}
	switch permissionMode(st.settings) {
	case contracts.PermissionReadOnly:
		return st.tr("只读", "Read-only"), false
	default:
		return st.tr("工作区可写", "Workspace-write"), false
	}
}

// buildSessionPermissionMenuItems adds the permission group header FIRST and
// then exactly the three selectable presets (只读 / 工作区可写 / 所有权限), one
// Radio item each. The disabled header row keeps the group name above its
// choices (the previous order put the header after the items) and carries the
// explanation in its tooltip, so the menu does not grow another row (a taller
// menu would overflow the 704x720 window).
func buildSessionPermissionMenuItems(m *ui.Menu, runtime contracts.UIRuntime, st *state) {
	if st.settings == nil {
		return
	}
	current := permissionMode(st.settings)
	m.Item(st.tr("权限模式", "Permission mode")).Disabled(true).Tooltip(st.tr(
		"只读：只自动允许只读/信息类工具，其他操作会询问。工作区可写（默认）：工作区内的文件读写自动允许，shell/网络仍会询问。所有权限：全部自动允许，不再弹窗。弹窗中选择「本会话内始终允许」可停止重复询问。",
		"Read-only: only read-only/informational tools are auto-allowed; other actions ask. Workspace-write (default): workspace file reads/writes are auto-allowed; shell/network still ask. All permissions: everything is auto-allowed without asking. Choose \"Always allow in this session\" in the dialog to stop repeats."))
	for _, mode := range []string{contracts.PermissionReadOnly, contracts.PermissionWorkspace, contracts.PermissionFull} {
		mode := mode
		item := m.Item(st.permissionLabel(mode))
		// Radio marks a single-choice item: assistive technology reads it as a
		// radio item, not a check box (mygo context-menu.md: MenuItem.Radio).
		item.Radio(mode == current)
		if item.Chosen() && mode != current {
			_ = st.settings.Set(contracts.SettingAgentPermission, mode)
			runtime.Invalidate()
		}
	}
}

// buildSessionClearAllowedToolsItem adds the session-scoped "clear remembered
// tools" action once the backend has remembered any tool for this conversation,
// and reports whether the row was added so the caller can close the section with
// a separator. The approval dialog's 本会话内始终允许 remembers tools for one
// session; this is the one place to see that it is active and to forget it.
func buildSessionClearAllowedToolsItem(m *ui.Menu, runtime contracts.UIRuntime,
	chat contracts.ChatService, st *state, activeID string) bool {
	permissions, ok := chat.(contracts.SessionToolPermissions)
	if !ok || activeID == "" {
		return false
	}
	allowed := permissions.AllowedTools(activeID)
	if len(allowed) == 0 {
		return false
	}
	label := fmt.Sprintf(st.tr("清除自动允许的工具（%d）", "Clear remembered tools (%d)"), len(allowed))
	if m.Item(label).Chosen() {
		if err := permissions.ClearAllowedTools(activeID); err != nil {
			st.commandErr = st.tr("清除失败：", "Clear failed: ") + err.Error()
		} else {
			st.commandErr = ""
			st.toast(contracts.NotifySuccess, st.tr("已清除自动允许的工具", "Remembered tools cleared"))
		}
		runtime.Invalidate()
	}
	return true
}

// buildSessionModeMenuItems adds the agent/chat mode choices.
func buildSessionModeMenuItems(m *ui.Menu, runtime contracts.UIRuntime, st *state) {
	if st.settings == nil {
		return
	}
	current := currentChatModeOption(st.settings)
	m.Item(st.tr("对话模式", "Conversation mode")).Disabled(true)
	for _, option := range chatModeOptions {
		option := option
		item := m.Item(st.tr(option.menuZh, option.menuEn))
		item.Radio(option.key == current.key)
		if item.Chosen() {
			applyChatMode(st.settings, option)
			runtime.Invalidate()
		}
	}
}

// sessionThinkingModelInfo resolves the conversation's effective model and its
// catalog entry: the per-session model override first, then the configured
// default. The model catalog is the optional backend module; without one (or
// for an ID it does not know) the entry is synthesized with an inferred family,
// so custom model names keep the generic thinking modes.
func sessionThinkingModelInfo(st *state, chat contracts.ChatService, activeID string) contracts.ModelInfo {
	model := ""
	if session, ok := chat.Session(activeID); ok {
		model = strings.TrimSpace(session.Model)
	}
	if model == "" {
		model = strings.TrimSpace(chat.Config().Model)
	}
	var models []contracts.ModelInfo
	if catalog, ok := st.ctx.Get(contracts.KeyModelCatalog); ok && catalog != nil {
		models = catalog.Models()
	}
	return contracts.ResolveModelInfo(models, model)
}

// sessionThinkingState is one frame's resolved thinking state for the
// conversation's effective model: the model entry (the catalog row or an
// inferred fallback), its selectable modes and the mode the next run will use.
type sessionThinkingState struct {
	info    contracts.ModelInfo
	modes   []contracts.ThinkingMode
	current contracts.ThinkingMode
	// explicit is the conversation's own override for this model (the per-model
	// row, or the legacy conversation-wide fallback); "" means follow the model
	// default.
	explicit string
}

// resolveSessionThinking resolves the conversation's thinking state for its
// effective model (the per-session model override, then the configured
// default). ok is false without a settings store, without a model, or when the
// model offers no selectable mode: the 推理等级 entry then stays hidden.
func resolveSessionThinking(st *state, chat contracts.ChatService, activeID string) (sessionThinkingState, bool) {
	if st.settings == nil || activeID == "" {
		return sessionThinkingState{}, false
	}
	info := sessionThinkingModelInfo(st, chat, activeID)
	modelID := strings.TrimSpace(info.ID)
	if modelID == "" {
		return sessionThinkingState{}, false
	}
	modes := contracts.ThinkingModesForModel(info)
	if len(modes) == 0 {
		return sessionThinkingState{}, false
	}
	override := contracts.SessionThinkingOverride(st.settings, activeID, modelID)
	current, _ := contracts.ResolveThinkingMode(info, override,
		st.settings.Get(contracts.SessionEffortSettingKey(activeID), ""))
	return sessionThinkingState{
		info:    info,
		modes:   modes,
		current: current,
		explicit: strings.TrimSpace(st.settings.Get(
			contracts.SessionModelThinkingSettingKey(activeID, modelID), "")),
	}, true
}

// buildSessionThinkingRow adds the compact 推理等级 submenu row whose trailing
// value is the effective level. The model chip's menu and the very-compact
// session-menu fallback share it, so both entry points behave identically.
func buildSessionThinkingRow(m *ui.Menu, runtime contracts.UIRuntime, st *state,
	thinking sessionThinkingState, activeID string) {
	row := m.Submenu(st.tr("推理等级", "Reasoning level"), func(sub *ui.Menu) {
		buildSessionThinkingChoices(sub, runtime, st, thinking, activeID)
	})
	row.Trailing(thinking.current.DisplayName(st.english()))
}

// buildSessionThinkingChoices fills the 推理等级 submenu: the follow-the-model-
// default radio (checked when the conversation has no explicit override), then
// the model's modes with the radio on the per-(conversation, model) override.
// Selecting a mode stores it on that (conversation, model) row and clears the
// legacy conversation-wide rows; following the default clears every override
// row, so switching models never inherits another model's strength.
func buildSessionThinkingChoices(m *ui.Menu, runtime contracts.UIRuntime, st *state,
	thinking sessionThinkingState, activeID string) {
	modelID := strings.TrimSpace(thinking.info.ID)
	label := modelThinkingDefaultLabel(st, thinking.info)
	follow := m.Item(st.tr("跟随模型默认（"+label+"）", "Follow the model default ("+label+")"))
	follow.Radio(thinking.explicit == "")
	if follow.Chosen() {
		clearSessionThinkingOverride(st.settings, activeID, modelID)
		runtime.Invalidate()
	}
	modelKey := contracts.SessionModelThinkingSettingKey(activeID, modelID)
	for _, mode := range thinking.modes {
		mode := mode
		item := m.Item(mode.DisplayName(st.english()))
		item.Radio(strings.EqualFold(thinking.explicit, mode.ID))
		if item.Chosen() {
			_ = st.settings.Set(modelKey, mode.ID)
			clearLegacySessionThinking(st.settings, activeID)
			runtime.Invalidate()
		}
	}
}

// clearSessionThinkingOverride writes every thinking-override row of one
// (conversation, model) pair empty (the per-model row, the legacy
// conversation-wide thinking row and the legacy reasoning-effort row).
func clearSessionThinkingOverride(settings contracts.SettingsService, sessionID, modelID string) {
	if settings == nil {
		return
	}
	for _, key := range contracts.SessionThinkingOverrideKeys(sessionID, modelID) {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(settings.Get(key, "")) == "" {
			continue
		}
		_ = settings.Set(key, "")
	}
}

// clearLegacySessionThinking empties the two pre-per-model conversation-wide
// thinking rows: the legacy thinking mode and the legacy reasoning effort.
func clearLegacySessionThinking(settings contracts.SettingsService, sessionID string) {
	if settings == nil {
		return
	}
	_ = settings.Set(contracts.SessionThinkingSettingKey(sessionID), "")
	_ = settings.Set(contracts.SessionEffortSettingKey(sessionID), "")
}

// modelThinkingDefaultLabel names one model's configured default thinking mode:
// the mode's display label, "系列默认" when the row follows the family default
// (empty DefaultThinking), or the raw ID when the row refers to a mode its
// effective mode list no longer defines.
func modelThinkingDefaultLabel(st *state, info contracts.ModelInfo) string {
	id := strings.TrimSpace(info.DefaultThinking)
	if id == "" {
		return st.tr("系列默认", "Family default")
	}
	if mode, ok := contracts.ThinkingModeByID(contracts.ThinkingModesForModel(info), id); ok {
		return mode.DisplayName(st.english())
	}
	return id
}

// composerProviderGroup names the provider group of one model row in the 模型
// submenu: the preset matching the row's own BaseURL, or the custom-service
// label. Rows keep the catalog order; a group header is added whenever the
// provider changes.
func composerProviderGroup(st *state, info contracts.ModelInfo) string {
	if preset, ok := contracts.MatchProviderPreset(info.BaseURL); ok {
		return preset.DisplayName(st.english())
	}
	return st.tr("自定义服务", "Custom service")
}

// sessionChipModelLabel names the effective model for the compact menu: the
// catalog display name when the model is configured, else its id, else the
// not-configured copy.
func sessionChipModelLabel(st *state, models []contracts.ModelInfo, effectiveModel string) string {
	if info, ok := contracts.FindModelInfo(models, effectiveModel); ok {
		return modelMenuLabel(info)
	}
	if model := strings.TrimSpace(effectiveModel); model != "" {
		return model
	}
	return st.tr("未配置", "Not set")
}

// buildSessionModelChoices fills the compact menu's 模型 submenu: the models
// grouped by provider (a disabled header per provider group, in catalog order),
// each row radio-marking the conversation's current model and switching it when
// chosen. An empty catalog says so instead of rendering an empty menu.
func buildSessionModelChoices(m *ui.Menu, runtime contracts.UIRuntime, st *state,
	switcher contracts.SessionModelSwitcher, activeID, effectiveModel string,
	models []contracts.ModelInfo) {
	if len(models) == 0 {
		m.Item(st.tr("还没有模型", "No models yet")).Disabled(true)
		return
	}
	group := ""
	for _, info := range models {
		info := info
		if provider := composerProviderGroup(st, info); provider != group {
			m.Item(provider).Disabled(true)
			group = provider
		}
		item := m.Item(modelMenuLabel(info))
		item.ID(info.ID)
		if note := strings.TrimSpace(info.Note); note != "" {
			item.Subtitle(note)
		}
		if info.ContextWindow > 0 {
			item.Trailing(formatContextWindow(info.ContextWindow))
		}
		item.Radio(strings.EqualFold(strings.TrimSpace(info.ID),
			strings.TrimSpace(effectiveModel)))
		if item.Chosen() {
			// Model-switch failures stay quiet: the chip keeps showing the
			// effective model of the next frame.
			if err := switcher.SetSessionModel(activeID, info.ID); err == nil {
				runtime.Invalidate()
			}
		}
	}
}

// buildSessionPresetSubmenuItems fills the 插入预设 submenu with the prompt
// presets (the submenu's own label is the group header).
func buildSessionPresetSubmenuItems(m *ui.Menu, runtime contracts.UIRuntime, st *state) {
	presets := effectivePromptPresets(st)
	if len(presets) == 0 {
		m.Item(st.tr("还没有预设", "No presets yet")).Disabled(true)
		return
	}
	for _, preset := range presets {
		preset := preset
		if m.Item(strings.TrimSpace(preset.Name)).Chosen() {
			insertPromptPreset(st, preset.Prompt)
			runtime.Invalidate()
		}
	}
}

// truncateRunes shortens one-line UI labels to at most max runes, marking the
// cut with an ellipsis; the untruncated value stays in the control's tooltip.
func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

// buildModelChip shows the session's effective model (session override, then
// the configured model) and opens the model switcher or the model settings.
func buildModelChip(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state, compactControls, veryCompact bool) {
	activeID := chat.ActiveSession()
	activeSession, hasSession := chat.Session(activeID)
	effectiveModel := ""
	if hasSession {
		effectiveModel = strings.TrimSpace(activeSession.Model)
	}
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(chat.Config().Model)
	}
	model := effectiveModel
	if model == "" {
		model = st.tr("未配置", "Not set")
	}
	// A narrow composer gives the model chip a bounded measure so the
	// low-frequency controls (or the labeled 更多工具 menu) always fit beside
	// the send control; the full name stays in the tooltip.
	switch {
	case veryCompact:
		model = truncateRunes(model, 8)
	case compactControls:
		model = truncateRunes(model, 14)
	}
	switcher, canSwitch := chat.(contracts.SessionModelSwitcher)
	catalog, hasCatalog := st.ctx.Get(contracts.KeyModelCatalog)
	if canSwitch && hasSession && activeID != "" {
		// The thinking state is resolved once for the 推理等级 row's trailing
		// value and the chip tooltip.
		thinking, hasThinking := resolveSessionThinking(st, chat, activeID)
		chipModel := strings.TrimSpace(effectiveModel)
		if chipModel == "" {
			chipModel = st.tr("未配置", "Not set")
		}
		tooltip := st.tr("切换本会话模型与推理等级：", "Switch this session's model and reasoning level: ") + chipModel
		if hasThinking {
			tooltip += " · " + thinking.current.DisplayName(st.english())
		}
		ui.PopoverMenuButton(c, model, func(m *ui.Menu) {
			// The catalogue is read when the menu opens, not on every frame: its
			// backend service re-reads the modelcatalog.models settings row.
			var models []contracts.ModelInfo
			if hasCatalog && catalog != nil {
				models = catalog.Models()
			}
			// Two compact entries open the real choices one level down; keeping
			// the model list and the thinking strengths out of the top menu
			// avoids one long stacked menu (mygo v0.8.3 draws a submenu's
			// Trailing value before its disclosure chevron).
			m.MaxWidth(design.OverlayWidthS)
			modelRow := m.Submenu(st.tr("模型", "Model"), func(sub *ui.Menu) {
				buildSessionModelChoices(sub, runtime, st, switcher, activeID, effectiveModel, models)
			})
			modelRow.Trailing(sessionChipModelLabel(st, models, effectiveModel))
			if hasThinking {
				buildSessionThinkingRow(m, runtime, st, thinking, activeID)
			}
			m.Separator()
			if m.Item(st.tr("打开模型设置…", "Open model settings…")).Chosen() {
				openSettingsPage(navigator, settingsPageModel)
			}
		}).Tooltip(tooltip)
	} else {
		modelButton := ui.Button(c, model).Tooltip(st.tr("打开模型设置", "Open model settings"))
		if modelButton.Clicked() {
			openSettingsPage(navigator, settingsPageModel)
		}
	}
}

// openAttachmentPathDialog opens the manual path-entry fallback (ZK-09): the
// native picker still runs off the UI thread, but when it is unavailable the
// user can stage absolute paths directly from a native Modal.
func openAttachmentPathDialog(st *state, runtime contracts.UIRuntime) {
	st.attachPathsOpen = true
	st.attachPathErr = ""
	runtime.Invalidate()
}

// buildAgentPresetMenuItems adds the persona picker rows to the 会话设置 menu,
// for the narrow composer where the standalone persona chip is folded away.
func buildAgentPresetMenuItems(m *ui.Menu, st *state, runtime contracts.UIRuntime,
	activeID string, setter contracts.SessionPresetSetter) {
	presets := effectiveAgentPresets(st)
	currentID := strings.TrimSpace(setter.SessionPreset(activeID))
	none := m.Item(st.tr("无", "None"))
	none.Radio(currentID == "")
	if none.Chosen() && currentID != "" {
		applySessionPreset(setter, st, runtime, activeID, "")
	}
	for _, preset := range presets {
		preset := preset
		item := m.Item(preset.Name)
		item.Radio(preset.ID == currentID)
		if item.Chosen() && preset.ID != currentID {
			applySessionPreset(setter, st, runtime, activeID, preset.ID)
		}
	}
}

// modelMenuLabel is one model menu row's primary label: the display name only.
// The catalogue note is rendered once, as the item's Subtitle (mygo
// context-menu.md), never inlined here: appending it to the label rendered the
// same sentence twice in one row.
func modelMenuLabel(info contracts.ModelInfo) string {
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = info.ID
	}
	return name
}

// agentMode reads the stored agent mode (normal when unset/unknown or without
// a settings store).
func agentMode(settings contracts.SettingsService) string {
	if settings == nil {
		return contracts.AgentModeNormal
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingAgentMode, ""))) {
	case contracts.AgentModePlan:
		return contracts.AgentModePlan
	default:
		return contracts.AgentModeNormal
	}
}

// usageTotalTokens returns the accounting total, falling back to the prompt+
// completion sum when the provider only reported the parts.
func usageTotalTokens(usage *contracts.MessageUsage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}

// usageContextWindowSize renders the model context-window size from the
// optional model catalogue (empty when there is no catalogue, the model is
// unknown, or its ContextWindow is unset). It is the bare value the usage
// detail's 上下文预算 row shows; the label already names the budget.
func (st *state) usageContextWindowSize() string {
	catalog, ok := st.ctx.Get(contracts.KeyModelCatalog)
	if !ok || catalog == nil {
		return ""
	}
	chat, ok := st.ctx.Get(contracts.KeyChat)
	if !ok || chat == nil {
		return ""
	}
	sessionID := chat.ActiveSession()
	model := ""
	if session, ok := chat.Session(sessionID); ok {
		model = strings.TrimSpace(session.Model)
	}
	if model == "" {
		model = strings.TrimSpace(chat.Config().Model)
	}
	if model == "" {
		return ""
	}
	for _, info := range catalog.Models() {
		if info.ID == model && info.ContextWindow > 0 {
			return formatContextWindow(info.ContextWindow)
		}
	}
	return ""
}

// formatContextWindow renders a token count for the usage footer: exact
// thousands as "128k", other positive values with one decimal ("32.8k").
func formatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens%1000 == 0 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%.1fk", float64(tokens)/1000)
}

// formatUsageDuration renders a turn duration: milliseconds under one second,
// otherwise one decimal of seconds.
func formatUsageDuration(durationMS int64) string {
	if durationMS < 1000 {
		return fmt.Sprintf("%dms", durationMS)
	}
	return fmt.Sprintf("%.1fs", float64(durationMS)/1000)
}

// minStatusSpeedDurationMS is the shortest turn wall time that makes an
// average output-speed number meaningful; shorter turns omit the item.
const minStatusSpeedDurationMS = 250

// sessionUsageStats is one frame's read of the conversation's usage read model:
// rounds, tool steps, output/billing token sums, wall time, the newest request's
// context size and the cache accounting. Every field except rounds/steps comes
// from MessageUsage; unknown values stay 0 and are omitted from the strip
// instead of being rendered as a wrong number.
type sessionUsageStats struct {
	rounds     int
	steps      int
	completion int
	prompt     int
	cached     int
	durationMS int64
	// contextTokens is the newest assistant usage's ContextTokens: the prompt
	// size of the last model request of that turn (the real context usage).
	// MessageUsage.PromptTokens is a billing total across every model call and
	// must never be compared with the context window.
	contextTokens int
	hasUsage      bool
}

// collectSessionUsage sums the session read model in one pass. The strip only
// shows up once the session has real token accounting, so a fresh or
// non-accounted conversation stays quiet.
func collectSessionUsage(session contracts.ChatSession) sessionUsageStats {
	var stats sessionUsageStats
	for _, message := range session.Messages {
		switch message.Role {
		case contracts.ChatRoleUser:
			stats.rounds++
		case contracts.ChatRoleAssistant:
			stats.steps += len(message.Tools)
		}
		usage := message.Usage
		if usage == nil {
			continue
		}
		stats.hasUsage = true
		stats.completion += usage.CompletionTokens
		stats.prompt += usage.PromptTokens
		stats.cached += usage.CachedTokens
		stats.durationMS += usage.DurationMS
		if message.Role == contracts.ChatRoleAssistant && usage.ContextTokens > 0 {
			stats.contextTokens = usage.ContextTokens
		}
	}
	return stats
}

// sessionContextWindow resolves the model context window for the active session
// from the optional model catalogue (per-session model override first, then the
// configured model). 0 means unknown: the strip then omits the context and
// remaining-context items rather than falling back to a config budget that is
// not a context window (ChatConfig.ContextBudget bounds the compactor).
func (st *state) sessionContextWindow(chat contracts.ChatService, session contracts.ChatSession) int {
	if chat == nil {
		return 0
	}
	catalog, ok := st.ctx.Get(contracts.KeyModelCatalog)
	if !ok || catalog == nil {
		return 0
	}
	model := strings.TrimSpace(session.Model)
	if model == "" {
		model = strings.TrimSpace(chat.Config().Model)
	}
	if model == "" {
		return 0
	}
	if info, found := contracts.FindModelInfo(catalog.Models(), model); found && info.ContextWindow > 0 {
		return info.ContextWindow
	}
	return 0
}

// buildSessionStatusStrip renders the DSH-style single muted status line right
// under the composer: rounds · steps · output speed · context/window · cache hit ·
// remaining context. It is informational only (no card, icon or badge); the
// exact uncompacted numbers live in its tooltip, and the whole strip stays
// hidden until the session has token accounting to report.
func buildSessionStatusStrip(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	st *state, session contracts.ChatSession) {
	line, exact, ok := sessionStatusLine(st, chat, session)
	if !ok {
		return
	}
	ui.Row(c).FillWidth().Justify(ui.Center).Margin(design.Space1, 0, 0, 0).Children(func() {
		uikit.RunStatus(c, uikit.RunStatusOptions{
			Usage:  &uikit.Usage{Summary: line, Exact: exact},
			Locale: st.locale,
		})
	})
}

// sessionStatusLine builds the quiet status line and the exact-number tooltip
// for one session; ok is false when there is nothing to show (no token usage
// yet). Items whose data is unknown are left out: the speed needs output tokens
// and wall time, the context ratio needs both the newest ContextTokens and the
// catalogue window, and the cache hit needs prompt tokens.
func sessionStatusLine(st *state, chat contracts.ChatService, session contracts.ChatSession) (line, exact string, ok bool) {
	stats := collectSessionUsage(session)
	if !stats.hasUsage {
		return "", "", false
	}

	parts := []string{
		sessionCountLabel(st, stats.rounds, "轮", "round", "rounds"),
		sessionCountLabel(st, stats.steps, "步", "step", "steps"),
	}
	// Average output speed over the run, tool time included: generated
	// completion tokens divided by the turn's wall time. A turn shorter than
	// minStatusSpeedDurationMS says nothing about throughput (scripted turns,
	// cancellations), so the item is omitted instead of printing a silly
	// five-digit number.
	speed := 0.0
	hasSpeed := stats.durationMS >= minStatusSpeedDurationMS && stats.completion > 0
	if hasSpeed {
		speed = float64(stats.completion) / (float64(stats.durationMS) / 1000)
		parts = append(parts, fmt.Sprintf("%d tok/s", int64(math.Round(speed))))
	}
	window := st.sessionContextWindow(chat, session)
	remaining := 0
	hasContext := stats.contextTokens > 0 && window > 0
	if hasContext {
		parts = append(parts, uikit.FormatCompactCount(stats.contextTokens)+"/"+
			uikit.FormatCompactCount(window)+" tok")
		remaining = 100 - int(math.Round(float64(stats.contextTokens)*100/float64(window)))
		if remaining < 0 {
			remaining = 0
		}
		if remaining > 100 {
			remaining = 100
		}
	}
	cacheHit := 0
	hasCache := stats.prompt > 0
	if hasCache {
		cacheHit = int(math.Round(float64(stats.cached) * 100 / float64(stats.prompt)))
		if cacheHit < 0 {
			cacheHit = 0
		}
		if cacheHit > 100 {
			cacheHit = 100
		}
		parts = append(parts, fmt.Sprintf(st.tr("缓存命中 %d%%", "Cache hit %d%%"), cacheHit))
	}
	if hasContext {
		parts = append(parts, fmt.Sprintf(st.tr("剩余 %d%%", "%d%% left"), remaining))
	}
	return strings.Join(parts, " · "), sessionExactStatusTooltip(st, stats, window, speed, cacheHit, remaining, hasSpeed, hasCache, hasContext), true
}

// sessionCountLabel renders one count with its unit: the Chinese unit is
// uninflected, English pluralizes ("1 round"/"2 rounds").
func sessionCountLabel(st *state, count int, zh, singular, plural string) string {
	if st.english() && count != 1 {
		return fmt.Sprintf("%d %s", count, plural)
	}
	if st.english() {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, zh)
}

// sessionExactStatusTooltip is the uncompacted version of the status line: the
// exact token sums, the average-speed arithmetic and the context window
// accounting. It also documents what the speed means.
func sessionExactStatusTooltip(st *state, stats sessionUsageStats, window int, speed float64,
	cacheHit, remaining int, hasSpeed, hasCache, hasContext bool) string {
	parts := []string{
		st.tr("轮次 ", "Rounds ") + strconv.Itoa(stats.rounds),
		st.tr("步骤 ", "Steps ") + strconv.Itoa(stats.steps),
	}
	if hasSpeed {
		parts = append(parts, fmt.Sprintf("%.1f tok/s", speed)+st.tr("（平均输出速度，含工具耗时）",
			" (average output speed over the run, tool time included)"))
	}
	if hasCache {
		parts = append(parts, st.tr("输出 ", "Completion ")+strconv.Itoa(stats.completion)+
			st.tr(" · 缓存命中 ", " · cache hit ")+strconv.Itoa(cacheHit)+"%")
	}
	if hasContext {
		parts = append(parts, st.tr("上下文 ", "Context ")+strconv.Itoa(stats.contextTokens)+
			st.tr(" / 窗口 ", " / window ")+strconv.Itoa(window)+
			st.tr("，剩余 ", ", ")+strconv.Itoa(remaining)+"%")
	}
	return strings.Join(parts, " · ")
}

// submitDraft sends the trimmed draft and any staged attachments (a message
// may consist of attachments only) and clears both on success. A backend
// without contracts.AttachmentSender reports the readable unsupported error
// instead of silently dropping the files.
func submitDraft(chat contracts.ChatService, activeID string, st *state) bool {
	prompt := strings.TrimSpace(st.draft)
	attachments := append([]contracts.Attachment(nil), st.pendingAttachments...)
	if (prompt == "" && len(attachments) == 0) || activeID == "" {
		return false
	}
	if len(attachments) > 0 {
		sender, ok := chat.(contracts.AttachmentSender)
		if !ok {
			st.attachErr = st.tr("当前后端不支持附件", "This backend does not support attachments")
			return false
		}
		if err := sender.SendWithAttachments(activeID, prompt, attachments); err != nil {
			st.attachErr = st.tr("发送附件失败：", "Failed to send attachments: ") + err.Error()
			return false
		}
	} else if err := chat.Send(activeID, prompt); err != nil {
		// C-04: a failed send must not be silent. The composer's error line
		// already renders commandErr as an alert, and the draft is kept so the
		// user can retry.
		st.commandErr = st.tr("发送失败：", "Send failed: ") + err.Error()
		return false
	}
	// A delivered message clears an older composer error (e.g. a stop failure).
	st.commandErr = ""
	// The attachments are in the conversation now; their decoded previews are
	// no longer needed and would otherwise stay in memory for the session's
	// lifetime (the map only grows as images are staged).
	for _, attachment := range attachments {
		delete(st.thumbnails, attachment.ID)
	}
	st.draft = ""
	st.composerCaret = 0
	st.pendingAttachments = nil
	st.attachErr = ""
	return true
}

// slashQuery reports whether draft starts with "/" (after whitespace) and
// returns the query typed after it.
func slashQuery(draft string) (string, bool) {
	trimmed := strings.TrimSpace(draft)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "/")), true
}

// matchingCommands filters the command surface for the slash query: commands
// with Enabled()==false are always hidden, and a non-empty query matches the
// title, ID or hint case-insensitively. The result is capped for the menu.
func matchingCommands(runtime contracts.UIRuntime, query string) []contracts.CommandSpec {
	needle := strings.ToLower(strings.TrimSpace(query))
	matches := make([]contracts.CommandSpec, 0, maxSlashCommands)
	// Two modules can register the same visible action (the old shell's
	// "打开设置" and the home screen's "设置"): the slash menu, like the command
	// palette, shows each title once.
	seen := make(map[string]bool, maxSlashCommands)
	for _, cmd := range runtime.Commands() {
		if cmd.Enabled != nil && !cmd.Enabled() {
			continue
		}
		if needle != "" {
			haystack := strings.ToLower(cmd.Title + "\x00" + cmd.ID + "\x00" + cmd.Hint)
			if !strings.Contains(haystack, needle) {
				continue
			}
		}
		if seen[cmd.Title] {
			continue
		}
		seen[cmd.Title] = true
		matches = append(matches, cmd)
		if len(matches) == maxSlashCommands {
			break
		}
	}
	return matches
}

// stepSelection moves a menu highlight by delta with wrap-around.
func stepSelection(sel, delta, n int) int {
	if n <= 0 {
		return 0
	}
	sel = (sel + delta) % n
	if sel < 0 {
		sel += n
	}
	return sel
}

// clampSelection keeps a menu highlight inside [0,n).
func clampSelection(sel, n int) int {
	if n <= 0 || sel < 0 {
		return 0
	}
	if sel >= n {
		return n - 1
	}
	return sel
}

// handleSlashMenuKey drives the slash menu from the keyboard: Up/Down move the
// highlight, Enter (or Cmd/Ctrl+Enter) and Tab run the highlighted command, Esc
// is handled by the caller (it closes the menu without clearing the draft). When
// nothing matches the key keeps its normal meaning: the draft is sent literally
// (C-22 — a message that begins with "/" must stay sendable, and the composer
// hint says so after the menu is dismissed).
func handleSlashMenuKey(k ui.TextKey, runtime contracts.UIRuntime, st *state, query string) bool {
	matches := matchingCommands(runtime, query)
	switch k.Key {
	case ui.KeyUp:
		st.slashSel = stepSelection(clampSelection(st.slashSel, len(matches)), -1, len(matches))
		runtime.Invalidate()
		return len(matches) > 0
	case ui.KeyDown:
		st.slashSel = stepSelection(clampSelection(st.slashSel, len(matches)), 1, len(matches))
		runtime.Invalidate()
		return len(matches) > 0
	case ui.KeyTab, ui.KeyEnter:
		if k.Mods != 0 && !(k.Key == ui.KeyEnter && k.Mods == ui.Cmd) {
			return false
		}
		if len(matches) == 0 {
			// No highlight to run: Enter keeps its normal send meaning (a
			// literal "/note" message must stay sendable); Tab moves focus.
			return false
		}
		cmd := matches[clampSelection(st.slashSel, len(matches))]
		k.SetText("")
		executeSlashCommand(runtime, st, cmd)
		return true
	}
	return false
}

// buildSlashPopover is the slash command menu, anchored above the composer card
// as an overlay (it never joins the layout flow, so the transcript does not
// move). The keyboard path stays in composerTextKey: Up/Down move the highlight,
// Enter/Tab run the highlighted command, Esc closes the menu and keeps the
// draft. Clicking outside closes it too.
func buildSlashPopover(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, query string, anchor ui.Element) ui.Element {
	matches := matchingCommands(runtime, query)
	sel := clampSelection(st.slashSel, len(matches))
	panelWidth := anchor.Bounds().W
	if panelWidth <= 0 {
		panelWidth = design.TranscriptWidth
	}
	open := true
	var highlighted ui.Element
	ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
		// The suggestion popup is a list box of options; the composer keeps the
		// keyboard focus and points at the highlighted option with
		// ActiveDescendant (mygo accessibility.md: suggestions at a text area's
		// caret).
		design.Apply(panel.Width(panelWidth).MaxWidthPercent(92).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, design.Space2, 0).
			Padding(design.Space2).Gap(design.Space1).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusLG),
			t.ShadowOverlay()).
			Role(ui.RoleListBox).Label(st.tr("命令菜单", "Command menu"))
		if len(matches) == 0 {
			design.Apply(ui.Text(c, st.tr("没有匹配的命令", "No matching commands")), t.TextMuted(design.TypeXS)).
				PaddingX(design.Space2).PaddingY(design.Space1)
			return
		}
		for index, cmd := range matches {
			index, cmd := index, cmd
			row := ui.ButtonBase(c.Key("session.slash."+cmd.ID)).FillWidth().
				Justify(ui.Start).AlignItems(ui.Center).
				Gap(design.Space2).Padding(design.Space1, design.Space2).
				Radius(design.RadiusSM).Label(firstNonEmpty(cmd.Title, cmd.ID)).
				Role(ui.RoleOption).Checked(index == sel).
				PositionInSet(index+1, len(matches))
			switch {
			case row.Pressed() || index == sel:
				row.Background(t.BgSelected)
			case row.Hovered():
				row.Background(t.BgHover)
			}
			row.Children(func() {
				title := firstNonEmpty(cmd.Title, cmd.ID)
				design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).FontWeight(600).
					TextColor(t.LabelPrimary)
				if hint := strings.TrimSpace(cmd.Hint); hint != "" {
					design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXXXS))
				}
			})
			if index == sel {
				highlighted = row
			}
			if row.Clicked() {
				st.slashSel = index
				executeSlashCommand(runtime, st, cmd)
				st.composerInput.Focus(c)
			}
		}
	})
	if !open && st.slashDismissed != st.draft {
		// Clicking outside closes the menu; the draft is kept.
		st.slashDismissed = st.draft
		runtime.Invalidate()
	}
	return highlighted
}

// mentionQuery reports whether the composer's current token is an "@" mention
// and returns the query after "@" plus the token's start offset in the draft.
// mentionQueryAt reports whether the draft token ending at the caret is an "@"
// mention, and returns the query after "@" plus the token's byte range in the
// draft. It follows the caret (C-15): an @ typed in the middle of the message
// opens the suggestions for the token around it, not only at the draft's end.
// The parenthesised byte range keeps the replacement exact (the text after the
// token is preserved instead of being dropped).
func mentionQueryAt(draft string, caret int) (query string, tokenStart, tokenEnd int, ok bool) {
	tokenEnd = len(draft)
	if caret >= 0 {
		if runes := []rune(draft); caret < len(runes) {
			tokenEnd = len(string(runes[:caret]))
		}
	}
	before := draft[:tokenEnd]
	tokenStart = strings.LastIndexAny(before, " \t\n") + 1
	token := before[tokenStart:]
	if !strings.HasPrefix(token, "@") {
		return "", 0, 0, false
	}
	query = token[1:]
	if strings.ContainsAny(query, " \t\n") {
		return "", 0, 0, false
	}
	return query, tokenStart, tokenEnd, true
}

// mentionSessionRows lists the other conversations the @ menu offers: the
// active session is excluded and the candidates are ranked by
// contracts.RankSessionReferenceCandidates (exact ID, ID prefix, title contains,
// then recency) and capped like the file suggestions.
func mentionSessionRows(chat contracts.ChatService, query string) []contracts.ChatSessionInfo {
	active := chat.ActiveSession()
	byID := make(map[string]contracts.ChatSessionInfo)
	candidates := make([]contracts.SessionReferenceCandidate, 0, maxMentionFiles)
	for _, info := range chat.Sessions() {
		if info.ID == active {
			continue
		}
		byID[info.ID] = info
		candidates = append(candidates, contracts.SessionReferenceCandidate{
			SessionID: info.ID,
			Title:     info.Title,
			UpdatedAt: info.UpdatedAt,
		})
	}
	ranked := contracts.RankSessionReferenceCandidates(candidates, query, maxMentionFiles)
	rows := make([]contracts.ChatSessionInfo, 0, len(ranked))
	for _, candidate := range ranked {
		if info, ok := byID[candidate.SessionID]; ok {
			rows = append(rows, info)
		}
	}
	return rows
}

// sessionReferenceIDPrefix renders the short session-ID hint shown next to a
// mention candidate (the full ID stays in the inserted token).
func sessionReferenceIDPrefix(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	const limit = 8
	runes := []rune(id)
	if len(runes) <= limit {
		return id
	}
	return string(runes[:limit]) + "…"
}

// mentionRowKind distinguishes the two @ menu groups.
type mentionRowKind int

const (
	mentionFile mentionRowKind = iota
	mentionSession
)

// mentionRow is one selectable row of the @ menu, in visual order: the
// workspace files first, then the other conversations.
type mentionRow struct {
	kind    mentionRowKind
	file    contracts.FileEntry
	session contracts.ChatSessionInfo
}

// ensureMentionCache refreshes the @ menu's file read model only when the query
// changed (or the menu opened): the frame builder never lists or searches the
// workspace on every frame, and the cache doubles as the keyboard menu's data.
func ensureMentionCache(st *state, workspace contracts.WorkspaceService, query string) {
	if workspace == nil {
		st.mentionFiles = nil
		st.mentionErr = nil
		st.mentionQuery = query
		st.mentionFresh = true
		return
	}
	if st.mentionFresh && st.mentionQuery == query {
		return
	}
	st.mentionQuery = query
	st.mentionFresh = true
	ctx := context.Background()
	var (
		entries []contracts.FileEntry
		loadErr error
	)
	if strings.TrimSpace(query) == "" {
		entries, loadErr = workspace.Files(ctx, "")
	} else {
		entries, loadErr = workspace.Search(ctx, query, maxMentionFiles)
	}
	if len(entries) > maxMentionFiles {
		entries = entries[:maxMentionFiles]
	}
	st.mentionFiles, st.mentionErr = entries, loadErr
}

// mentionRows returns the @ menu's rows in visual (and keyboard) order.
func mentionRows(st *state, chat contracts.ChatService, query string) []mentionRow {
	rows := make([]mentionRow, 0, len(st.mentionFiles)+maxMentionFiles)
	for _, entry := range st.mentionFiles {
		rows = append(rows, mentionRow{kind: mentionFile, file: entry})
	}
	for _, info := range mentionSessionRows(chat, query) {
		rows = append(rows, mentionRow{kind: mentionSession, session: info})
	}
	return rows
}

// applyMention replaces the @ token (tokenStart..tokenEnd) with the chosen
// reference, keeping any text that follows the caret.
func applyMention(st *state, draft string, tokenStart, tokenEnd int, row mentionRow) string {
	var replacement string
	switch row.kind {
	case mentionFile:
		replacement = "@" + row.file.Path + " "
	default:
		replacement = " " + contracts.FormatSessionReference(row.session.ID) + " "
	}
	if tokenStart < 0 || tokenStart > len(draft) {
		tokenStart = len(draft)
	}
	if tokenEnd < tokenStart || tokenEnd > len(draft) {
		tokenEnd = len(draft)
	}
	next := draft[:tokenStart] + replacement + draft[tokenEnd:]
	st.draft = next
	// The accepted reference ends at tokenStart+len(replacement): place the
	// caret there on the next frame. The editor keeps its old offset after the
	// key hook replaces the text, which would both re-open the menu for the old
	// token and put the next typed character inside the reference.
	st.composerCaret = len([]rune(next[:tokenStart+len(replacement)]))
	st.composerCaretPending = true
	return next
}

// handleMentionMenuKey drives the @ menu from the keyboard: Up/Down move the
// highlight, Enter (or Cmd/Ctrl+Enter) and Tab insert the highlighted
// reference, Esc is handled by the caller. With no rows the keys keep their
// normal editing meaning.
func handleMentionMenuKey(k ui.TextKey, runtime contracts.UIRuntime, chat contracts.ChatService,
	st *state, query string, tokenStart, tokenEnd int) bool {
	workspace, _ := st.ctx.Get(contracts.KeyWorkspace)
	ensureMentionCache(st, workspace, query)
	rows := mentionRows(st, chat, query)
	switch k.Key {
	case ui.KeyUp:
		st.mentionSel = stepSelection(clampSelection(st.mentionSel, len(rows)), -1, len(rows))
		runtime.Invalidate()
		return len(rows) > 0
	case ui.KeyDown:
		st.mentionSel = stepSelection(clampSelection(st.mentionSel, len(rows)), 1, len(rows))
		runtime.Invalidate()
		return len(rows) > 0
	case ui.KeyTab, ui.KeyEnter:
		if (k.Mods != 0 && !(k.Key == ui.KeyEnter && k.Mods == ui.Cmd)) || len(rows) == 0 {
			return false
		}
		row := rows[clampSelection(st.mentionSel, len(rows))]
		next := applyMention(st, st.draft, tokenStart, tokenEnd, row)
		k.SetText(next)
		st.mentionSel = 0
		runtime.Invalidate()
		return true
	}
	return false
}

// buildMentionPopover is the composer's @ suggestion menu, anchored above the
// composer card as an overlay (H8): the transcript does not move when it opens.
// It lists the workspace files the cached query matched and, when other
// conversations exist, the 会话 group referencing them. Choosing a file inserts
// "@path " into the draft; choosing a chat inserts "@session:<id>".
func buildMentionPopover(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	workspace contracts.WorkspaceService, chat contracts.ChatService, st *state,
	query string, tokenStart, tokenEnd int, anchor ui.Element) ui.Element {
	ensureMentionCache(st, workspace, query)
	rows := mentionRows(st, chat, query)
	if workspace == nil && len(rows) == 0 {
		return ui.Element{}
	}
	sel := clampSelection(st.mentionSel, len(rows))
	panelWidth := anchor.Bounds().W
	if panelWidth <= 0 {
		panelWidth = design.TranscriptWidth
	}
	open := true
	var highlighted ui.Element
	ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
		// A list box of options driven by the text area, whose focus stays in the
		// composer; the highlighted option is the input's ActiveDescendant
		// (mygo accessibility.md: suggestions at a text area's caret).
		design.Apply(panel.Width(panelWidth).MaxWidthPercent(92).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, design.Space2, 0).
			Padding(design.Space2).Gap(design.Space1).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusLG),
			t.ShadowOverlay()).
			Role(ui.RoleListBox).Label(st.tr("@ 引用菜单", "@ reference menu"))
		fileRows := 0
		for _, row := range rows {
			if row.kind == mentionFile {
				fileRows++
			}
		}
		if workspace != nil {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Icon(c, icons.Search()).Size(design.Space3, design.Space3).
					TextColor(t.LabelTertiary)
				design.Apply(ui.Text(c, st.tr("工作区文件", "Workspace files")), t.TextMuted(design.TypeXXS)).Grow(1)
				design.Apply(ui.Text(c, st.tr("@ 引用文件", "@ reference a file")), t.TextMuted(design.TypeXXXS))
			})
			switch {
			case st.mentionErr != nil:
				uikit.ErrorLine(c, st.tr("读取工作区失败：", "Failed to read the workspace: ")+st.mentionErr.Error())
			case fileRows == 0:
				design.Apply(ui.Text(c, st.tr("没有匹配的文件", "No matching files")), t.TextMuted(design.TypeXS)).
					PaddingX(design.Space2).PaddingY(design.Space1)
			}
		}
		for index, row := range rows {
			index, row := index, row
			if row.kind == mentionSession && (index == 0 || rows[index-1].kind != mentionSession) {
				if workspace != nil {
					ui.Divider(c)
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.Message()).Size(design.Space3, design.Space3).
						TextColor(t.LabelTertiary)
					design.Apply(ui.Text(c, st.tr("会话", "Chats")), t.TextMuted(design.TypeXXS)).Grow(1)
					design.Apply(ui.Text(c, st.tr("@ 引用会话", "@ reference a chat")),
						t.TextMuted(design.TypeXXXS))
				})
			}
			option := buildMentionRow(c, t, runtime, st, index, sel, len(rows), row, tokenStart, tokenEnd)
			if index == sel {
				highlighted = option
			}
		}
	})
	if !open && st.mentionDismissed != st.draft {
		// A click outside closes the menu and keeps the draft.
		st.mentionDismissed = st.draft
		runtime.Invalidate()
	}
	return highlighted
}

// buildMentionRow is one selectable @ menu option. It reports the option the
// composer's ActiveDescendant points at when it is the highlighted one.
func buildMentionRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state,
	index, sel, total int, row mentionRow, tokenStart, tokenEnd int) ui.Element {
	label, detail, icon := "", "", icons.File()
	key := ""
	switch row.kind {
	case mentionFile:
		icon = icons.File()
		if row.file.Dir {
			icon = icons.Folder()
		}
		label = "/" + row.file.Path
		key = "session.mention.file." + firstNonEmpty(row.file.Path, label)
		if !row.file.Dir {
			detail = formatAttachmentSize(int(row.file.Size))
		}
	default:
		icon = icons.Message()
		label = firstNonEmpty(row.session.Title, row.session.ID)
		key = "session.mention.session." + firstNonEmpty(row.session.ID, label)
		detail = sessionReferenceIDPrefix(row.session.ID)
	}
	button := ui.ButtonBase(c.Key(key)).FillWidth().Justify(ui.Start).
		AlignItems(ui.Center).Gap(design.Space2).Padding(design.Space1, design.Space2).
		Radius(design.RadiusSM).Label(label).
		Role(ui.RoleOption).Checked(index == sel).PositionInSet(index+1, total)
	switch {
	case button.Pressed() || index == sel:
		button.Background(t.BgSelected)
	case button.Hovered():
		button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icon).Size(design.Space3, design.Space3).TextColor(t.LabelSecondary)
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
			TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		if detail != "" {
			design.Apply(ui.Text(c, detail), t.TextMuted(design.TypeXXXS)).NoWrap()
		}
	})
	if button.Clicked() {
		st.mentionSel = index
		st.draft = applyMention(st, st.draft, tokenStart, tokenEnd, row)
		st.composerInput.Focus(c)
		runtime.Invalidate()
	}
	return button
}

// executeSlashCommand clears the composer draft, runs the command (nil safe)
// and requests the frame that hides the menu.
func executeSlashCommand(runtime contracts.UIRuntime, st *state, cmd contracts.CommandSpec) {
	st.draft = ""
	st.slashSel = 0
	st.slashDismissed = ""
	st.composerCaret = 0
	if cmd.Run != nil {
		_ = cmd.Run()
	}
	runtime.Invalidate()
}

// --- helpers ----------------------------------------------------------------

// truncateTrajectorySummary bounds an expanded tool summary.
func truncateTrajectorySummary(summary string) string {
	const max = 1200
	if len(summary) <= max {
		return summary
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(summary[cut]) {
		cut--
	}
	return summary[:cut] + "…"
}

// startNewSession creates a session, makes it active and navigates to it.
func startNewSession(st *state, chat contracts.ChatService, navigator contracts.Navigator) {
	id, err := chat.NewSession(st.tr("新对话", "New conversation"))
	if err != nil {
		return
	}
	if err := chat.SetActiveSession(id); err != nil {
		return
	}
	_ = navigator.Push(ScreenID)
}
