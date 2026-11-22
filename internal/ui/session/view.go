package session

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
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
	// tabContext is the session context inspector; it only exists when the
	// backend provides contracts.KeyContext (buildTabs hides it otherwise).
	tabContext
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
	// A new visit of the conversation screen starts with no transient agent
	// overlay open (the host builds only the current screen each frame).
	st.noteScreenVisit()
	// The frame's bottom dock starts unclaimed; the panes report the tops of the
	// controls they place at the bottom and the anchor is set once the body is
	// built (see reportDockTop).
	st.dockTop = 0
	// Keep the slash commands' registry titles in the host language (the
	// command palette reads them directly). Only an actual language change
	// rebuilds the registry; a stable frame never does.
	if lang := uikit.Language(st.locale); st.syncCommandLocale != nil && lang != st.commandLocale {
		st.commandLocale = lang
		st.syncCommandLocale()
	}
	activeID := chat.ActiveSession()
	// The goal dock shows only the active conversation's goal: a switch
	// re-reads the per-session service, so no other session's goal is ever
	// cached or displayed.
	st.syncActiveGoal(activeID)
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
	// A requested stop whose settle the subscription missed (a host without a
	// change event for it) completes from the read model here (UX-06).
	if st.stopRequested == activeID && hasSession && !chatSession.Running {
		st.finishStop()
	}
	// The reply is done: hand the keyboard back to the composer (only on the
	// conversation tab, never while some other input owns it). The goal
	// continuation itself is the agent backend's job; the UI only observes the
	// settle for this keyboard handoff.
	if observeRunSettledForFocus(chat, st) {
		st.runSettledAnnounce = true
		if st.tab == tabChat {
			st.focusComposer = true
		}
	}
	// The settle edge is announced once on the next session build (UX-19, S1
	// a11y): the subscription Post may have observed it before this frame.
	if st.runSettledAnnounce {
		st.runSettledAnnounce = false
		c.Announce(st.tr("回答已完成", "Reply finished"))
	}
	// A newly arrived approval announces itself (UX-19): the user is blocked
	// on a decision, and the inline card alone is silent for screen readers.
	st.announceApprovalArrival(c)
	// A child conversation has no context inspector of its own: return to the
	// conversation tab instead of showing an empty parent-context pane.
	if strings.TrimSpace(st.agents.childID) != "" && st.tab == tabContext {
		st.tab = tabChat
	}
	// Entering the Memory tab re-reads the store once (the cached read model is
	// what avoids render-time I/O in every other frame); entering the Context
	// tab (or switching conversations) re-reads the backend's folded view once.
	if st.contextService != nil && st.contextSessionID != activeID {
		st.contextDirty = true
	}
	if st.tab != st.lastTab {
		switch st.tab {
		case tabMemory:
			requestMemoryRefresh(st)
		case tabContext:
			st.contextDirty = true
		}
		st.lastTab = st.tab
	}
	if st.contextService != nil && st.tab == tabContext && st.contextDirty {
		st.refreshSessionContext(activeID)
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
		// A child conversation belongs to the parent session on screen: a
		// session switch always returns to the parent conversation (the child
		// switcher is rebuilt for the new session's children).
		st.agents.childID = ""
		st.agents.crumbChosen = -1
		st.closeAgentPopovers()
		st.agents.childSending = false
		st.agents.childSendErr = ""
		st.agents.childSendSeq++
	}
	// Find in the conversation (Cmd/Ctrl+F) is a screen-level chord: the bar
	// belongs to the transcript, so it opens on the conversation tab even when
	// the user is reading another tab.
	registerFindShortcut(c, st, runtime)
	// One screen-level Esc arbitration for every layer (UX-03): find bar, child
	// view, pending approval, suggestion menu, running turn — the same order the
	// stable session.escape command runs, so the window-level fallback and the
	// screen never disagree.
	registerSessionEscape(c, chat, st, runtime, activeID)

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		// The title, actions and tabs live in the host's B3 top band (registered
		// screen slots); the body keeps only its status/error lines.
		if st.exportErr != "" {
			buildErrorLine(c, t, st.tr("导出失败：", "Export failed: ")+st.exportErr)
		}
		if st.goalActionErr != "" {
			buildErrorLine(c, t, st.goalActionErr)
		}
		if strings.TrimSpace(st.agents.childID) != "" {
			// A parent approval stays reachable in a child conversation: the
			// inline banner is the only approval UI, so its slot renders above
			// the child body (the parent composer dock is not part of it).
			buildChildBannerDock(c, runtime, st)
			// A child conversation takes the whole main content area: its own
			// transcript/activity panes and, for a continuable teammate, its own
			// send box (the parent composer and dock stay with the parent).
			buildChildView(c, t, runtime, st, cfg)
		} else {
			switch st.tab {
			case tabTrajectory:
				buildTrajectoryPane(c, t, chatSession, hasSession, st)
			case tabMemory:
				buildMemoryPane(c, t, runtime, st)
			case tabContext:
				buildContextPane(c, t, st, chatSession, hasSession)
			default:
				buildChatPane(c, t, runtime, navigator, chat, cfg, st, chatSession, hasSession)
			}
			if hasSession {
				// The goal strip and the approval banner dock right above the
				// composer, on the transcript's reading measure.
				buildComposerDock(c, t, runtime, st)
				buildComposer(c, t, runtime, navigator, chat, cfg, st, activeID, chatSession)
			}
		}
		// One report per frame with the smallest top any pane claimed, so the host
		// anchors the toast stack above every bottom control at once.
		if service := st.toastAnchorService(); service != nil {
			service.ReportDockTop(st.dockTop)
		}
		// The draft-overwrite confirmation (UX-05): a native ConfirmDanger above
		// the screen, like the command dialogs.
		buildDraftOverwriteConfirm(c, st, runtime)
		// Command-family dialogs (/goal, /plan, /memory): native Modals above
		// the screen; their buttons close them and request the next frame.
		buildGoalDialog(c, t, runtime, st)
		buildPlanDialog(c, t, runtime, st)

		buildHelpDialog(c, t, runtime, st)
		// Current-session reminders: the create dialog, the session reminder
		// list and its destructive delete confirmation (native Modals above the
		// screen, like the command dialogs).
		buildReminderDialogs(c, t, runtime, st, chat)
		// The attachment image lightbox: message images and staged composer
		// images open the original above the screen (P1).
		buildAttachmentGallery(c, t, runtime, st)
		// The goal actions' destructive confirmation is the native AlertDialog:
		// it lives above the whole screen (the goal menu itself is in the dock).
		buildGoalMenuActions(c, runtime, st)
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
		// A multi-iteration turn's reasoning folds are keyed
		// "<messageID>.segN" (US-01): they belong to the base message.
		if base, _, found := strings.Cut(key, ".seg"); found && keep[base] {
			return true
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
	// The meta reveal of a message that left the conversation is gone too: a
	// reveal deadline for a message nobody shows would keep a frame scheduled.
	for key := range st.metaRevealAt {
		if !keep[key] {
			delete(st.metaRevealAt, key)
		}
	}
	for key := range st.metaStreamed {
		if !keep[key] {
			delete(st.metaStreamed, key)
		}
	}
}

// createDebounce is the window in which a second new-conversation request is the
// same gesture: a double click, or a click and the keyboard chord (A4-I-04). It
// is short enough that two deliberate clicks still make two conversations, and
// the same value as the shell's own new-session debounce, so a click on the
// sidebar and one in the hero cannot both create a conversation in one gesture.
const createDebounce = 600 * time.Millisecond

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
	st.clearComposerFailure()
	st.queueActionErr = ""
}

// cancelRunningTurn stops the active run the way the 停止 control does: the
// agent backend disarms the goal and owns the prompt inbox (a stop never clears
// the queued prompts; their drain is the backend's decision). The cancel is
// async in the real backend, so a successful request enters the stopping state
// (UX-06): the 已停止 toast fires only when the run settles (finishStop), and a
// second request while stopping is ignored. A failure is reported inline. It is
// shared by the stop button, Esc/Cmd+. and the screen shortcut so every
// interrupt path has identical semantics.
func cancelRunningTurn(chat contracts.ChatService, st *state, runtime contracts.UIRuntime, sessionID string) {
	if chat == nil || st == nil || sessionID == "" {
		return
	}
	if st.stopRequested == sessionID {
		// Already stopping: S8 forbids re-triggering the cancel.
		return
	}
	if err := chat.Cancel(sessionID); err != nil {
		st.commandErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
	} else {
		st.clearComposerFailure()
		st.stopRequested = sessionID
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
// owns Esc right now (command dialogs, memory confirm/edit). Esc must close
// those first, never interrupt a run behind them.
func sessionDialogOpen(st *state) bool {
	if st == nil {
		return false
	}
	return st.goalOpen || st.planOpen || st.memOpen ||
		st.helpOpen || st.memoryConfirmOpen
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
		st.clearComposerFailure()
	}
	runtime.Invalidate()
	return true
}

// announceApprovalArrival announces a newly arrived pending approval once
// (UX-19): the oldest pending request's ID is the edge — a new one announces,
// resolutions and an empty queue reset it so the next arrival announces again.
func (st *state) announceApprovalArrival(c *ui.Context) {
	if st == nil {
		return
	}
	raw, ok := st.ctx.Get(contracts.KeyApprovals)
	if !ok || raw == nil {
		return
	}
	approvals, ok := raw.(contracts.Approvals)
	if !ok || approvals == nil {
		return
	}
	oldest := ""
	if pending := approvals.Pending(); len(pending) > 0 {
		oldest = pending[0].ID
	}
	if oldest == st.lastAnnouncedApproval {
		return
	}
	st.lastAnnouncedApproval = oldest
	if oldest != "" {
		c.Announce(st.tr("需要审批：工具调用等待你的决定", "Approval required: a tool call waits for your decision"))
	}
}

// sessionEscape is the conversation screen's unified Esc arbitration (UX-03),
// shared by the screen-level shortcut and the stable session.escape command the
// window-level fallback calls. The order is fixed: the find bar (innermost
// surface) → the child-view detour → a pending approval (deny, keep the run) →
// modal dialogs (left to their own overlay shortcut) → an open suggestion menu
// (dismiss) → the running turn (stop). It reports whether it consumed the key.
func sessionEscape(chat contracts.ChatService, st *state, runtime contracts.UIRuntime, activeID string) bool {
	if st == nil {
		return false
	}
	// The find bar is the innermost focused surface: Esc closes it and never
	// stops the turn. The bar's field consumes the key when it has the focus;
	// this keeps the same promise when the focus sits on another control of
	// the bar (or the bar's Esc already closed it this frame).
	if st.findOpen {
		st.findOpen = false
		st.focusComposer = true
		runtime.Invalidate()
		return true
	}
	// A child conversation is a read-only detour: Esc returns to the parent
	// conversation instead of blind-denying the parent's approval card (the
	// banner stays visible in the child view, but the user returns to the
	// conversation to answer it) or stopping the hidden parent run.
	if strings.TrimSpace(st.agents.childID) != "" {
		st.exitChildView()
		return true
	}
	// A pending approval owns Esc next: deny the oldest request and keep the
	// turn running (the user is answering the agent, not stopping it). This
	// holds while idle too — a cross-session approval is still answerable.
	if denyPendingApproval(st, runtime) {
		return true
	}
	if sessionDialogOpen(st) {
		// The dialog's own overlay shortcut closes it; leave Esc to that.
		return false
	}
	if suggestionMenuOpen(st) {
		// Menu-first over the interrupt: this Esc only dismisses the menu.
		st.slashDismissed = st.draft
		st.mentionDismissed = st.draft
		runtime.Invalidate()
		return true
	}
	if chat != nil && activeID != "" {
		if session, ok := chat.Session(activeID); ok && session.Running {
			cancelRunningTurn(chat, st, runtime, activeID)
			return true
		}
	}
	return false
}

// registerSessionEscape wires the screen-level Esc through sessionEscape whenever
// Esc has screen-level work (a running turn, an open find bar, a child view, a
// pending approval or an open suggestion menu); otherwise the key belongs to
// the focused control. Cmd+. never belongs to a menu and interrupts directly
// while a turn runs.
func registerSessionEscape(c *ui.Context, chat contracts.ChatService, st *state, runtime contracts.UIRuntime, activeID string) {
	if activeID == "" {
		return
	}
	session, ok := chat.Session(activeID)
	running := ok && session.Running
	// The conversation's Esc handler is registered on the screen's own scope, and
	// only while no floating layer of this screen is open: a shortcut takes the
	// key before an overlay's own handler in some orders, and an open popover
	// (the team/subagent/jobs panels, the model and permission menus, the queue
	// and usage popovers, the suggestion menus) owns its Escape.
	//
	// Reaching the key while the focus is elsewhere in the window (the sidebar,
	// the top band) is the shell's job: uiroot's window-level fallback calls the
	// stable session.escape command, so the promise holds without a second
	// window-wide registration here (A4: K7/K8).
	overlayOpen := st.anyOverlayOpen()
	if !overlayOpen && (running || st.findOpen || strings.TrimSpace(st.agents.childID) != "" ||
		st.pendingApprovalCount() > 0 || suggestionMenuOpen(st)) {
		c.OnShortcut(0, ui.KeyEscape, func() {
			sessionEscape(chat, st, runtime, activeID)
		})
	}
	if running && !overlayOpen {
		c.OnShortcut(ui.Cmd, ui.KeyPeriod, func() {
			if sessionDialogOpen(st) {
				return
			}
			cancelRunningTurn(chat, st, runtime, activeID)
		})
	}
}

// --- screen chrome (host B3 top band slots) ---------------------------------

// buildScreenHeaderSlot is the conversation's SlotScreenHeader contribution: the
// conversation title (or the parent > child breadcrumb in a child conversation)
// followed by the signature Agent Run Band — 运行中, 智能体团队, 子智能体 N,
// 后台任务 and 待审批 — and a spacer, so the host's utilities stay at the band's
// end. The workspace name
// stays beside the title as quiet context when the band is wide enough. The
// measured row width goes back into st.headerWidth, which the entries use on
// the next frame to compact their words.
func buildScreenHeaderSlot(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService, cfg Config) {
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2)
	row.Children(func() {
		if strings.TrimSpace(st.agents.childID) != "" {
			buildChildBreadcrumb(c, t, st, chat, cfg)
		} else {
			buildBackToScreen(c, t, st)
			buildConversationTitle(c, t, st, chat, cfg)
		}
		buildAgentRunBand(c, t, st, chat)
		ui.Spacer(c).Grow(1)
	})
	st.headerWidth = row.Bounds().W
}

// backTargetNavigator is the optional half of the host navigator a session header
// uses to show where Back leads; a host without it simply has no back action.
type backTargetNavigator interface {
	CanGoBack() bool
	BackTarget() string
}

// backTargetLabel localizes the screen a back button returns to. Unknown
// screens fall back to a plain Back label rather than exposing a raw ID.
func backTargetLabel(st *state, screenID string) string {
	switch screenID {
	case "insights":
		return st.tr("洞察", "Insights")
	case "automation":
		return st.tr("自动化", "Automation")
	case "plugins":
		// The back target names the screen with the word the sidebar and its
		// page title use: 能力/Capabilities. The header used to say 插件/Plugins,
		// so one destination had two names on one screen (A5 F-01).
		return st.tr("能力", "Capabilities")
	default:
		return st.tr("返回", "Back")
	}
}

// buildBackToScreen draws the header's back affordance when the session was
// reached from another screen (insights/automation/plugins). It is a quiet
// secondary control before the title; the sidebar remains the primary
// navigation.
func buildBackToScreen(c *ui.Context, t *design.Tokens, st *state) {
	if st == nil || st.navigator == nil {
		return
	}
	back, ok := st.navigator.(backTargetNavigator)
	if !ok || !back.CanGoBack() {
		return
	}
	target := back.BackTarget()
	if target == "" || target == ScreenID {
		return
	}
	label := "‹ " + backTargetLabel(st, target)
	button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
		Height(design.RowCompactHeight).Shrink(0).Label(label)
	if button.Clicked() {
		st.navigator.Back()
	}
}

// buildConversationTitle is the header's title in the parent conversation: the
// session title (the configured default when it has none), with the workspace
// name as quiet context while the band is wide enough.
func buildConversationTitle(c *ui.Context, t *design.Tokens, st *state, chat contracts.ChatService, cfg Config) {
	title := sessionTitle(st, chat, cfg)
	// The workspace label is quiet context: it stays while the title keeps its
	// room and drops out on a narrow band (the Overview pane still names it).
	showWorkspace := st.headerWidth == 0 || st.headerWidth >= 460
	// The title sits in window chrome (B3 band), not in a page body: the TypeM
	// step keeps it in proportion with the 13 DIP tabs, 12 DIP badges and 16 DIP
	// band icons; TypeL made the band read top-heavy. It shrinks (and ellipsizes)
	// before the fixed-width agent entries do.
	// A title caps at 14 * 32 DIP: enough for a full sentence on a wide band,
	// still bounded so a very long title cannot push the utilities off. The old
	// 7 * 32 cap (220 px) truncated ordinary titles even after the zero-value
	// agent entries were removed.
	// The title keeps a readable floor at EVERY width, not only once the measured
	// band width reports narrow: st.headerWidth lags one frame (it is the previous
	// frame's laid-out width), so on the frame the inspector docks the band is
	// already narrow while headerWidth still reads wide. A conditional floor let
	// the title collapse to a single "…" there (2026-11 UI review P2). The floor is
	// eight TypeM CJK glyphs plus the ellipsis; the metadata entries compact to
	// icon+count on a narrow band (buildAgentRunBand) to give the title its room.
	design.Apply(ui.Text(c, title), t.Font(design.TypeM)).
		FontWeight(600).TextColor(t.LabelPrimary).Shrink(1).
		MinWidth(design.Space8*4 + design.Space4).
		MaxWidth(design.Space8 * 14).MaxLines(1).Ellipsis("…").Tooltip(title)
	if showWorkspace {
		buildHeaderWorkspace(c, t, st)
	}
}

// buildScreenActionsSlot is the conversation's SlotScreenActions contribution:
// the conversation-actions ⋯ menu. The run state and agent entries live in the
// header's Agent Run Band, before the spacer that puts these utilities at the
// band's end.
func buildScreenActionsSlot(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, chat contracts.ChatService, cfg Config) {
	activeID := chat.ActiveSession()
	session, hasSession := chat.Session(activeID)
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
	uikit.IconButton(c, uikit.IconButtonOptions{
		Label:   label,
		Tooltip: label,
		Icon:    icons.More(),
		Size:    t.Scale(design.IconSizeS),
		Variant: uikit.IconButtonNative,
	}).PopoverMenuButton(func(m *ui.Menu) {
		buildConversationActionsMenu(m, c, st, runtime, cfg, chatSession)
	})
}

// buildConversationActionsMenu fills the ⋯ menu: the clipboard and export
// commands of the conversation (the /export command remains the keyboard entry),
// the goal entry (设置目标 at zero state, 编辑目标 once a goal exists) and the
// archive action. Actions are grouped: clipboard first, the export formats, the
// goal, archiving last.
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
	// The raw log is only offered when the backend can archive the durable event
	// log (the optional contracts.SessionLogExporter): no capability, no entry.
	if exporter := st.sessionLogExporter(); exporter != nil {
		if m.Item(st.tr("导出原始日志（ZIP）", "Export raw log (ZIP)")).Chosen() {
			exportSessionLog(st, exporter, chatSession, cfg)
		}
	}
	// The goal entry: the dock only exists while a goal does, so the ⋯ menu
	// carries the zero-value entry too. One entry only — the dock's own menu
	// owns pause/resume/complete/clear once a goal is there.
	if st.goalService != nil {
		label := st.tr("设置目标", "Set goal")
		if st.goal != nil {
			label = st.tr("编辑目标", "Edit goal")
		}
		m.Separator()
		if m.Item(label).ID("session.conversation.goal").Chosen() {
			openGoalDialog(st, runtime)
		}
	}
	// Current-session reminders: the automation backend can deliver a scheduled
	// prompt into this conversation. Both entries appear only with that backend
	// (a headless product keeps the previous menu).
	if service := st.automationService(); service != nil && chatSession.ID != "" {
		m.Separator()
		if m.Item(st.tr("创建提醒…", "Create reminder…")).ID("session.reminder.create").Chosen() {
			st.openReminderCreate()
			runtime.Invalidate()
		}
		reminderLabel := st.tr("本会话提醒", "Session reminders")
		if tasks, err := st.sessionReminderTasks(chatSession.ID); err == nil {
			reminderLabel = fmt.Sprintf(st.tr("本会话提醒（%d）", "Session reminders (%d)"), len(tasks))
		}
		if m.Item(reminderLabel).ID("session.reminder.list").Chosen() {
			st.reminders.listOpen = true
			runtime.Invalidate()
		}
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

// composerDockMaxHeight is the dock's height budget ceiling (UX-02): ten Space8
// rows (320 DIP). The dock scrolls inside it, so a long todo list + goal +
// approval card can never push the composer out of the window; on short windows
// the budget shrinks to half the window height so the transcript keeps a share.
const composerDockMaxHeight float32 = design.Space8 * 10

// composerDockBudget adapts the dock's ceiling to the window height (the audit
// demo note: the transcript deserves a minimum share even at 900×600). Half the
// window leaves the header, a transcript band and the composer their room while
// a full approval card (banner + bounded middle + footer) still fits whole.
func composerDockBudget(c *ui.Context) float32 {
	_, height := c.Size()
	budget := composerDockMaxHeight
	if height > 0 {
		if adaptive := height / 2; adaptive < budget {
			budget = adaptive
		}
	}
	return budget
}

// reportDockTop records the top of a bottom dock for this frame's toast anchor
// (contracts.ToastAnchorService.ReportDockTop). Several panes can own the bottom
// of the screen — the composer dock always, the Memory tab's add row too — so the
// frame keeps the highest control (the smallest top): a notice must clear every
// one of them, not just the composer.
func (st *state) reportDockTop(top float32) {
	if st == nil || top <= 0 {
		return
	}
	if st.dockTop == 0 || top < st.dockTop {
		st.dockTop = top
	}
}

// buildComposerDock is the strip above the composer on the transcript's reading
// measure: the todo plan, the active goal and the host slot the approval module
// draws its inline card into, inside one height-budgeted scroll (UX-02). A
// pending approval docks FIRST so its decision buttons are reachable without
// scrolling; otherwise the ambient todo/goal order is kept.
func buildComposerDock(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	// The dock sits on the shared reading column, so it lines up with the
	// transcript and the composer card instead of a hand-rolled 960 frame
	// (uikit.ReadingColumn owns the 92% cap and the horizontal padding).
	uikit.ReadingColumn(c, func(c *ui.Context) {
		dock := ui.Scroll(c.Key("session.dock")).FillWidth().
			MaxHeight(composerDockBudget(c)).TrackScroll(&st.dockScroll).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
				if st.pendingApprovalCount() > 0 {
					// The blocking decision first: the card's banner and footer
					// stay visible at the dock's top; todo/goal scroll below.
					runtime.RenderSlot(c, contracts.SlotSessionBanner)
					buildTodoDock(c, t, runtime, st)
					buildGoalDock(c, t, runtime, st)
					return
				}
				// DSH order: the ambient todo plan first, then the goal driver,
				// then the (empty) banner slot.
				buildTodoDock(c, t, runtime, st)
				buildGoalDock(c, t, runtime, st)
				runtime.RenderSlot(c, contracts.SlotSessionBanner)
			})
		})
		// Report the dock's top so the host's toast stack clears an approval
		// card docked above the composer: the card's decision buttons are the
		// one control a notice must never cover (contracts.ReportDockTop).
		if rect := dock.Bounds(); rect.H > 0 {
			st.reportDockTop(rect.Y)
		}
	})
}

// buildChildBannerDock renders the host's session banner slot (the inline
// approval card) above a child conversation, on the transcript's reading
// measure: a pending parent approval must stay visible and reachable while the
// user reads a child, since the inline card is the product's only approval UI.
// The same height budget keeps a long card from pushing the child's send box
// out of the window (UX-02).
func buildChildBannerDock(c *ui.Context, runtime contracts.UIRuntime, st *state) {
	// Same reading column as the transcript and the composer dock.
	uikit.ReadingColumn(c, func(c *ui.Context) {
		ui.Scroll(c.Key("session.childBanner")).FillWidth().
			MaxHeight(composerDockBudget(c)).TrackScroll(&st.childBannerScroll).Children(func() {
			runtime.RenderSlot(c, contracts.SlotSessionBanner)
		})
	})
}

// exportSessionJSONL writes the conversation as JSON Lines through its seam.
func exportSessionJSONL(st *state, chatSession contracts.ChatSession, cfg Config) {
	title := textutil.FirstNonEmptyTrimmed(chatSession.Title, cfg.Title, st.tr("会话", "Conversation"))
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
	title := textutil.FirstNonEmptyTrimmed(chatSession.Title, cfg.Title, st.tr("会话", "Conversation"))
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
	labels := []string{
		st.localized(cfg.TabChat, "对话", "Chat"),
		st.localized(cfg.TabTrajectory, "轨迹", "Trajectory"),
		st.localized(cfg.TabMemory, "记忆", "Memory"),
	}
	// The Context tab is contributed only when the backend inspector exists;
	// without it the conversation keeps its three tabs exactly as before.
	if st.contextService != nil && strings.TrimSpace(st.agents.childID) == "" {
		labels = append(labels, st.localized(cfg.TabContext, "上下文", "Context"))
	}
	if st.tab >= len(labels) {
		st.tab = tabChat
	}
	// The official tab row (mygo tabs.go): one Tab stop, the arrows/Home/End walk
	// the choice, hover lifts the ink of the tab under the pointer, and the chosen
	// tab carries the 2 DIP accent underline inset under its own label. The
	// hand-drawn TabsBase + Box underline this replaces produced the same picture
	// with twenty lines of boxes and two margin fudges (A1-matrix: 迁移), so the
	// official row owns the look now. The type step rides on the list so every tab
	// text inherits 13 DIP; the chosen tab keeps mygo's 600 against the row's 500
	// (B-spec §3.1.2).
	tabs := ui.Tabs(c, &st.tab, labels...)
	tabs.Margin(0)
	design.Apply(tabs, t.Font(design.TypeXS), func(el ui.Element) ui.Element {
		return el.FontWeight(design.WeightMedium)
	})
}

// --- chat -------------------------------------------------------------------

// childNoticeMessages derives the transcript receipts of one conversation's
// finished children from the durable child-session read model. A child that
// finished in another session is never rendered here.
func childNoticeMessages(st *state, sessionID string) []contracts.ChatMessage {
	if st == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	children := st.childAgentService()
	if children == nil {
		return nil
	}
	agents := children.ChildAgents(sessionID)
	if len(agents) == 0 {
		return nil
	}
	notices := make([]contracts.ChatMessage, 0, len(agents))
	for _, agent := range agents {
		if strings.TrimSpace(agent.ParentSessionID) != sessionID || agent.Finished.IsZero() {
			continue
		}
		text, ok := childNoticeText(st, agent)
		if !ok {
			continue
		}
		notices = append(notices, contracts.ChatMessage{
			ID:        "child-notice-" + agent.ID,
			Role:      contracts.ChatRoleNotice,
			Text:      text,
			CreatedAt: agent.Finished,
			// Renderer-only status marker (fenced out below to keep the screen's
			// red budget): DESIGN §5.2 error semantics, no raw error string.
			Error: failedNoticeMarker(agent.Status),
		})
	}
	sort.SliceStable(notices, func(i, j int) bool {
		return notices[i].CreatedAt.Before(notices[j].CreatedAt)
	})
	capFailedNoticeMarkers(notices)
	return notices
}

// capFailedNoticeMarkers keeps the danger styling (red budget, DESIGN §5.5
// rule 5) to the two newest failed receipts; older failed children keep the
// explicit "运行失败" copy but render muted.
func capFailedNoticeMarkers(notices []contracts.ChatMessage) {
	visibleFailed := 0
	for index := len(notices) - 1; index >= 0; index-- {
		if notices[index].Error != "failed" {
			continue
		}
		if visibleFailed < 2 {
			visibleFailed++
			continue
		}
		notices[index].Error = ""
	}
}

// failedNoticeMarker is the renderer-only marker of a failed child receipt.
func failedNoticeMarker(status string) string {
	if status == contracts.ChildAgentFailed {
		return "failed"
	}
	return ""
}

// noticeSummary flattens one child answer/error into a short single line,
// dropping Markdown structure (fence blocks with their language tag and content,
// heading/list/quote markers, inline code/emphasis) so the receipt reads as a
// notice rather than "```bash … - …" noise. The caller bounds the length
// (truncateRunes / MaxLines); PreviewProse only cleans (2026-11 UI review P2).
func noticeSummary(text string) string {
	return uikit.PreviewProse(text, 0)
}

// mergeChildNotices places derived receipts chronologically among the settled
// transcript, so a later turn never appears older than a receipt. While the turn
// is streaming they go above the trailing live answer: the follow-the-end view
// stays pinned to the growing reply instead of a static receipt appended below.
func mergeChildNotices(messages []contracts.ChatMessage, notices []contracts.ChatMessage, running bool) []contracts.ChatMessage {
	if len(notices) == 0 {
		return messages
	}
	base := messages
	var tail []contracts.ChatMessage
	if running && len(messages) > 0 && messages[len(messages)-1].Streaming {
		base = messages[:len(messages)-1]
		tail = messages[len(messages)-1:]
	}
	merged := make([]contracts.ChatMessage, 0, len(messages)+len(notices))
	next := 0
	for _, message := range base {
		for next < len(notices) && !notices[next].CreatedAt.IsZero() &&
			!message.CreatedAt.IsZero() && notices[next].CreatedAt.Before(message.CreatedAt) {
			merged = append(merged, notices[next])
			next++
		}
		merged = append(merged, message)
	}
	merged = append(merged, notices[next:]...)
	merged = append(merged, tail...)
	return merged
}

// noticeChildID returns the child conversation a derived transcript receipt
// links to. The receipt messages are derived from the child-session read model
// with the "child-notice-" ID prefix (childNoticeMessages); any other notice —
// a host's plain receipt — has no child to open.
func noticeChildID(message contracts.ChatMessage) (string, bool) {
	if message.Role != contracts.ChatRoleNotice {
		return "", false
	}
	const prefix = "child-notice-"
	if !strings.HasPrefix(message.ID, prefix) {
		return "", false
	}
	childID := strings.TrimSpace(strings.TrimPrefix(message.ID, prefix))
	return childID, childID != ""
}

// childNoticeText is the localized one-line receipt of one finished child.
func childNoticeText(st *state, agent contracts.ChildAgent) (string, bool) {
	kind := st.tr("子智能体", "Subagent")
	if agent.Kind == contracts.ChildAgentTeammate {
		kind = st.tr("队友", "Teammate")
	}
	name := agentDisplayName(agent.Name, agent.ID)
	switch agent.Status {
	case contracts.ChildAgentCompleted:
		output := noticeSummary(agent.Output)
		if output != "" {
			return fmt.Sprintf(st.tr("%s「%s」已完成：%s", "%s \"%s\" finished: %s"),
				kind, name, truncateRunes(output, 160)), true
		}
		return fmt.Sprintf(st.tr("%s「%s」已完成", "%s \"%s\" finished"), kind, name), true
	case contracts.ChildAgentFailed:
		detail := noticeSummary(agent.Error)
		if detail == "" {
			return fmt.Sprintf(st.tr("%s「%s」运行失败", "%s \"%s\" failed"), kind, name), true
		}
		return fmt.Sprintf(st.tr("%s「%s」运行失败：%s", "%s \"%s\" failed: %s"),
			kind, name, truncateRunes(detail, 160)), true
	case contracts.ChildAgentInterrupted:
		return fmt.Sprintf(st.tr("%s「%s」已中断", "%s \"%s\" was interrupted"), kind, name), true
	default:
		return "", false
	}
}

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
	// The finished current turn's file output: after the final answer, the
	// turn-tail cards list the files it delivered and edited. A running or
	// failed turn has no settled answer yet, so it shows no cards.
	var turnFiles generatedFiles
	showTurnFiles := false
	if !chatSession.Running {
		if tail := tailTurnMessages(messages); len(tail) > 0 {
			last := tail[len(tail)-1]
			if !last.Streaming && strings.TrimSpace(last.Error) == "" {
				turnFiles = turnGeneratedFiles(tail)
				showTurnFiles = len(turnFiles.delivered) > 0 || len(turnFiles.edited) > 0
			}
		}
	}
	// Delegated-child receipts: the durable child-session records of this
	// conversation render as quiet transcript rows. They are derived (not stored
	// in model history), so a reload shows the same rows; mergeChildNotices keeps
	// the live ordering and puts them above the streaming answer.
	threadMessages := transcriptMessages(st, chatSession)
	// A restored run that never finished is not replayed by the backend: say so
	// above the transcript (and only there — the sidebar stays free of a
	// transient badge) with a continue/dismiss pair.
	buildInterruptedNotice(c, t, runtime, st, chatSession)
	// verbose keeps the whole timeline and never bundles steps; the other modes
	// fold a multi-step turn into one process row.
	sameTurn := st.transcriptSameTurn()
	if st.findOpen {
		buildFindBar(c, st, threadMessages, sameTurn)
	}
	uikit.SessionThread(c, uikit.SessionThreadOptions{
		Messages:  threadMessages,
		Running:   chatSession.Running,
		State:     &st.chatList,
		Locale:    st.locale,
		HoldPlace: &st.chatHoldPlace,
		FollowEnd: &st.chatFollowEnd,
		LaidOut:   &st.chatListSeen,
		Error:     visibleError,
		Empty: func(ec *ui.Context) {
			buildHero(ec, t, cfg, runtime, navigator, chat, st, hasSession)
		},
		Row: func(rc *ui.Context, message contracts.ChatMessage) {
			last := message.ID == lastID && message.Role == contracts.ChatRoleAssistant
			canRegenerate := hasRegenerator && !chatSession.Running && last
			buildMessage(rc, t, runtime, message, st, canRegenerate, last, defaultOpen(message.ID))
			if last && showTurnFiles {
				buildGeneratedFileCards(rc, t, st, turnFiles)
			}
		},
		SameTurn: sameTurn,
		GroupRow: func(rc *ui.Context, group []contracts.ChatMessage) {
			last := group[len(group)-1].ID == lastID
			canRegenerate := hasRegenerator && !chatSession.Running && last
			buildAssistantTurnGroup(rc, t, runtime, st, group, canRegenerate, last,
				defaultOpen(group[len(group)-1].ID))
			if last && showTurnFiles {
				buildGeneratedFileCards(rc, t, st, turnFiles)
			}
		},
	})
}

// buildHero is the branded first-run empty state: icon badge, title, hint, the
// native new-conversation action and — when a prerequisite is missing — the
// setup checklist that names it and deep-links to the fix (FR-07). It is the
// product's real landing: Home is not a navigation destination, so the first
// run must be guided here.
//
// hasSession reports whether an (empty) conversation is already open. The
// 新对话 action then only hands the keyboard to the composer instead of
// creating a second empty conversation (FR-30); a genuinely new conversation is
// created untitled so the backend still auto-titles it from the first prompt.
func buildHero(c *ui.Context, t *design.Tokens, cfg Config, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state, hasSession bool) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		PaddingY(design.Space8).Children(func() {
		ui.Icon(c, icons.NewChat()).Size(th.Space(6), th.Space(6)).
			TextColor(t.LabelTertiary)
		// The title says what this screen is for and the line under it says what
		// the product does; the old pair repeated "start a new conversation"
		// twice and described nothing (A5 F-11/F-12).
		design.Apply(ui.Text(c, st.tr("发第一条消息", "Send your first message")),
			t.Font(design.TypeM)).TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, st.localized(cfg.EmptyText,
			"描述你想完成的事，ZeoKeel 会读代码、改文件并验证结果。",
			"Describe what you want done — ZeoKeel reads the code, edits files and verifies the result.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		// Only a genuinely new conversation needs the action. An empty
		// conversation already on screen has the composer right under this
		// block, so a button that merely moved the caret there was a primary
		// action with no action (A5 F-13: removed).
		if !hasSession {
			label := st.tr("新建会话", "New session")
			// A ghost row, the same anatomy as the sidebar's 新对话: no edge, a
			// neutral fill only under the pointer, 32 DIP (B-spec §3.1.1).
			button := ui.Button(c, "").Label(label).Height(design.RowCompactHeight).
				PaddingX(design.Space2).Radius(design.RadiusSM).Gap(design.Space1)
			button = button.Background(ui.Transparent)
			if button.Hovered() || button.Focused() {
				button = button.Background(t.BgHover)
			}
			button = button.Transition(design.TransitionColors(c))
			button.Children(func() {
				ui.Icon(c, icons.Plus()).Size(design.IconSizeS, design.IconSizeS).
					TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, label), t.Font(design.TypeS)).
					TextColor(t.LabelPrimary)
			})
			if button.Clicked() {
				startNewSession(st, chat, navigator)
			}
		}
		buildHeroExamples(c, t, runtime, st)
		buildFirstRunChecklist(c, t, runtime, navigator, chat, st)
	})
}

// heroExamplePrompts is the empty conversation's three starter prompts: real
// tasks the product does well, not feature names.
func (st *state) heroExamplePrompts() []string {
	return []string{
		st.tr("检查当前工作区并给出摘要", "Inspect the current workspace and summarize it"),
		// 「讨论」是会话概念的第三种叫法，只此一处（A5 F-14）: the session is
		// the thing the prompt refers to.
		st.tr("把这次会话整理成发布清单", "Turn this session into a release checklist"),
		st.tr("找出最近的编译错误并修复", "Find the recent build errors and fix them"),
	}
}

func buildHeroExamples(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	prompts := st.heroExamplePrompts()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1).Children(func() {
		for _, prompt := range prompts {
			prompt := prompt
			button := design.Apply(ui.Button(c, prompt), t.SecondaryButton()).
				MaxWidth(design.DialogWidthM).Label(prompt)
			if button.Clicked() {
				// A non-empty draft is the user's work: ask before replacing it
				// (UX-05); an empty one takes the example directly.
				requestDraftOverwrite(st, runtime, prompt)
			}
		}
	})
}

// buildFirstRunChecklist renders the missing prerequisites of the first run —
// no model, no API key, no workspace — with one action each that fixes it
// (FR-07). Home's setup card is no longer reachable, so this is the guided
// path; it stays quiet when everything is configured.
func buildFirstRunChecklist(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state) {
	if chat == nil || st == nil {
		return
	}
	cfg := chat.Config()
	missingModel := strings.TrimSpace(cfg.Model) == ""
	missingKey := strings.TrimSpace(cfg.APIKey) == ""
	missingWorkspace := false
	if raw, ok := st.ctx.Get(contracts.KeyWorkspace); ok && raw != nil {
		if workspace, ok := raw.(contracts.WorkspaceService); ok && workspace != nil {
			if current, open, err := workspace.Current(context.Background()); err != nil || !open ||
				strings.TrimSpace(current.ID) == "" {
				missingWorkspace = true
			}
		}
	}
	if !missingModel && !missingKey && !missingWorkspace {
		return
	}
	// The checklist card is the shared panel preset (BgRaised + BorderL2 +
	// RadiusLG), like Home's setup card — one surface ramp, one corner token,
	// not a hand-picked RadiusMD (2026-11 audit UD-22).
	design.Apply(ui.Column(c).FillWidth().Gap(design.Space2).
		Padding(design.Space3, design.Space4), t.Panel()).
		Children(func() {
			design.Apply(ui.Text(c, st.tr("开始前还需要：", "Before you start:")),
				t.Font(design.TypeXXS)).FontWeight(600).TextColor(t.LabelSecondary)
			if missingModel {
				buildFirstRunAction(c, t, st.tr("尚未配置模型", "No model configured"),
					st.tr("配置模型", "Configure model"), func() { openSettingsPage(navigator, settingsPageModel) })
			}
			if missingKey {
				buildFirstRunAction(c, t,
					st.tr("尚未配置 API Key", "No API key yet")+
						st.tr("（使用本地模型服务时可忽略）", " (ignore for local model servers)"),
					st.tr("配置 API Key", "Configure API key"), func() { openSettingsPage(navigator, settingsPageModel) })
			}
			if missingWorkspace {
				buildFirstRunAction(c, t,
					st.tr("未打开工作区", "No workspace open")+
						st.tr("（文件与命令工具需要工作区）", " (file and command tools need one)"),
					st.tr("添加工作区", "Add workspace"), func() {
						var workspace contracts.WorkspaceService
						if raw, ok := st.ctx.Get(contracts.KeyWorkspace); ok {
							workspace, _ = raw.(contracts.WorkspaceService)
						}
						startWorkspaceCreate(runtime, workspace, st)
					})
			}
		})
}

// buildFirstRunAction is one setup-checklist row: the missing prerequisite and
// the secondary action that fixes it. The action is secondary so the hero's
// 新对话 stays the page's single primary.
func buildFirstRunAction(c *ui.Context, t *design.Tokens, label, actionLabel string, run func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).Grow(1).MaxLines(2)
		button := design.Apply(ui.Button(c, actionLabel), t.SecondaryButton()).
			Height(design.RowCompactHeight).Shrink(0)
		if button.Clicked() && run != nil {
			run()
		}
	})
}

// buildMessage renders one transcript entry. User turns are right-aligned
// raised bubbles; assistant turns are plain brand body text with optional
// reasoning, tool cards and errors.
func buildMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state, canRegenerate, last, defaultOpen bool) {
	switch message.Role {
	case contracts.ChatRoleUser:
		if message.GoalRound != nil {
			// An engine-injected continuation round is not a human prompt: it
			// renders as a goal-round receipt instead of a user bubble.
			buildGoalRoundNotice(c, t, st, message)
			return
		}
		column := ui.Column(c.Key("session.message." + message.ID)).FillWidth().
			AlignItems(ui.End).Gap(design.Space1)
		column.Children(func() {
			if strings.TrimSpace(message.Text) != "" {
				// The user's own words are a filled surface, not an outlined
				// card: a white bubble with a hairline read as an unfilled form
				// field next to the composer, and as the same object as the
				// bordered artifacts beside it (VD-07). BgStrong fill, no edge,
				// the control radius, 12x8 padding, and at most 80% of the
				// column so a long message never reaches the far edge.
				bubble := ui.Column(c).Background(t.BgStrong).Radius(design.RadiusMD).
					MaxWidthPercent(80).Padding(design.Space2, design.Space3)
				bubble.Children(func() {
					design.Apply(ui.Text(c, message.Text), t.Font(design.TypeBody)).
						FontSize(t.Scale(st.messageFontSize())).TextColor(t.LabelPrimary)
				})
			}
			buildMessageAttachments(c, t, runtime, st, message.Attachments)
			// Quiet metadata/actions under the bubble: time, copy the original
			// text, and edit it back into the composer (never auto-sent).
			buildUserMessageMeta(c, t, runtime, st, message,
				st.metaRowRevealed(c, message, column.Hovered() || column.FocusWithin()))
		})
	case contracts.ChatRoleNotice:
		// A quiet receipt for a delegated child: at most two lines with a small
		// glyph, never a bubble or a badge (it is not a chat turn). A failed child
		// keeps error semantics (danger text + alert glyph, DESIGN §5.2/AP-08).
		failed := strings.TrimSpace(message.Error) != ""
		glyph := icons.Activity()
		color := t.LabelTertiary
		if failed {
			glyph = icons.AlertTriangle()
			color = t.DangerText
		}
		// A derived child receipt links back to the child's conversation: the
		// row's trailing action opens it, so a finished teammate/subagent is one
		// click away instead of hidden behind the child switcher.
		childID, linksChild := noticeChildID(message)
		ui.Row(c.Key("session.message." + message.ID)).FillWidth().
			AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, glyph).Size(design.Space4, design.Space4).TextColor(color)
			// The body is two lines at most; the tooltip keeps the full receipt
			// readable (a truncated receipt is still the whole story on hover).
			body := design.Apply(ui.Text(c, message.Text), t.Font(design.TypeXXS)).
				TextColor(color).Selectable().MaxLines(2).Ellipsis("…").Tooltip(message.Text)
			if linksChild {
				// The action sits at the row's end; the body takes the free room.
				body.Grow(1).Shrink(1).MinWidthPercent(0)
				label := st.tr("打开子会话", "Open child conversation")
				uikit.IconButton(c, uikit.IconButtonOptions{
					Key:     "session.notice.open." + childID,
					Label:   label,
					Tooltip: label,
					Icon:    icons.Message(),
					Size:    design.IconSizeS,
					OnClick: func() { st.openChildView(childID) },
				})
			}
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
		buildAssistantMessage(c, t, runtime, message, st, canRegenerate, last, defaultOpen)
	}
}

// buildAssistantMessage renders one assistant turn under the work-steps policy.
// While it streams, compact/standard show one live activity line plus the reply
// body and decision cards, while detailed/verbose keep the whole timeline; once
// finished, compact/standard/detailed fold the process into one summary row (open
// by default only for the latest turn under collapse=next-input), while verbose
// always shows the full timeline under its meta row. A failed turn never folds.
func buildAssistantMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state, canRegenerate, last, defaultOpen bool) {
	policy := st.workStepsPolicyFor()
	foldable := assistantProcessFoldable(message, policy.mode)
	open, explicit := st.processOpen[message.ID]
	if !explicit {
		open = defaultOpen
	}
	open = open && foldable
	// One message's blocks (the process fold, the reply, an error note) sit 12
	// DIP apart; the 24 DIP between two turns is the transcript List's own gap
	// (B-spec §3.2.2), so the rhythm comes from the two containers and never
	// from a margin on a single row.
	outer := ui.Column(c.Key("session.message." + message.ID)).FillWidth().Gap(design.Space3)
	outer.Children(func() {
		switch {
		case message.Streaming:
			buildAssistantStreaming(c, t, runtime, st, message, policy)
		case policy.mode == contracts.WorkStepsVerbose:
			// verbose: the full timeline, always open, with no fold summary row.
			buildAssistantTimeline(c, t, runtime, st, message)
		case foldable:
			open = buildProcessSummary(c, t, runtime, st, message, open)
			if open {
				buildAssistantTimeline(c, t, runtime, st, message)
			} else {
				buildAssistantReply(c, t, runtime, st, message)
			}
		default:
			// A pure-text turn has no process to fold: the reply alone.
			buildAssistantTimeline(c, t, runtime, st, message)
		}
		// A known tool failure gets its localized recovery hint outside the
		// process fold, so folding the timeline (the default) never hides the
		// explanation and the one-click fix (FR-31).
		if heading, kind, ok := st.turnToolRecovery(message); ok {
			buildRecoveryLine(c, t, st, runtime, heading, kind, "")
		}
		// The provider cut the final answer at the output-token limit: say so
		// where the answer ends (the explanation folds out on demand).
		if last && message.Truncated && !message.Streaming {
			buildTruncatedNotice(c, t, st, message.ID)
		}
		if message.Error != "" {
			buildMessageError(c, t, runtime, st, message)
			// A failed turn keeps one obvious way out: the labeled 重试 action
			// under the error, enabled even though the turn has no text to copy.
			if canRegenerate {
				buildRetryAction(c, t, runtime, st)
			}
		}
		if st.regenerateErr != "" && canRegenerate && strings.TrimSpace(message.Error) == "" {
			buildErrorLine(c, t, st.regenerateErr)
		}
		// The turn's meta is the message's last line (B-spec §3.2.4: one meta row
		// at the bottom, revealed on hover or keyboard focus, with a fixed 24 DIP
		// height so revealing it never moves the transcript).
		buildAssistantMetaRow(c, t, runtime, st, message, canRegenerate,
			st.metaRowRevealed(c, message, outer.Hovered() || outer.FocusWithin()))
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
		// The generating indicator is the shared run-status row (S8): the official
		// spinner with its status copy, never a fake progress bar. It yields while
		// the thinking row still spins — one spinner at a time — and appears the
		// moment the reply streams prose.
		if !activeReasoningSegment(message) {
			uikit.RunStatus(c, uikit.RunStatusOptions{
				Running:     true,
				StatusLabel: st.tr("生成中", "Generating"),
				Locale:      st.locale,
			})
		}
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
	message contracts.ChatMessage, open bool) bool {
	return buildProcessFoldRow(c, t, runtime, st, message.ID, open,
		processSummaryParts(st, message.Reasoning, reasoningSegmentCount(message), len(message.Tools)))
}

// buildProcessFoldRow is the one disclosure row a folded process renders: the
// chevron, the summary and the turn's meta (time/usage/⋯) on one row. The fold
// key is the message ID for a single turn and the first message ID for a grouped
// multi-step turn, so both remember their state the same way.
func buildProcessFoldRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state,
	key string, open bool, parts []string) bool {
	summary := strings.Join(parts, " · ")
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		// The disclosure is created inside the row so its trigger is the row's
		// only child; the turn's meta is the message's own bottom row now.
		end := ui.CollapsibleBase(c.Key("session.process."+key), &open)
		trigger := end.Trigger.Grow(1).AlignItems(ui.Center).Gap(design.Space2).
			PaddingY(design.Space1).Radius(design.RadiusSM).Label(summary)
		if end.Trigger.Hovered() || end.Trigger.Focused() {
			trigger = trigger.Background(t.BgHover)
		}
		trigger = trigger.Transition(design.TransitionColors(c))
		trigger.Children(func() {
			// The fold header carries the same fixed 16 DIP marker column as the
			// activity rows, with the disclosure chevron as its marker.
			ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
				ui.Icon(c, icons.ChevronRight()).Size(design.Space4, design.Space4).
					TextColor(t.LabelTertiary).Rotate(90 * end.Progress())
			})
			// The fold summary is a row title, not metadata: 13 DIP at weight 500
			// (the same step as the reasoning row's 思考), so the process line and
			// the thinking line under it read as one family.
			design.Apply(ui.Text(c, summary), t.Font(design.TypeXS)).
				FontWeight(design.WeightMedium).TextColor(t.LabelSecondary)
		})
		if end.Trigger.Changed() {
			st.processOpen[key] = open
			st.chatHoldPlace = true
			runtime.Invalidate()
		}
	})
	return open
}

// processSummaryParts is the fold summary copy: 过程, 思考 (when the turn
// reasoned — with the segment count when it thought across several iterations,
// US-01) and the tool count. Duration/tokens live in the meta half of the same
// row, never twice.
func processSummaryParts(st *state, reasoning string, segments, tools int) []string {
	parts := []string{st.tr("过程", "Process")}
	if strings.TrimSpace(reasoning) != "" {
		if segments > 1 {
			parts = append(parts, fmt.Sprintf(st.tr("%d 段思考", "%d reasoning segments"), segments))
		} else {
			parts = append(parts, st.tr("思考", "Reasoning"))
		}
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

// reasoningSegmentCount is the fold summary's segment count: the recorded
// per-iteration segments, or one for a legacy message whose flat Reasoning has
// no boundaries.
func reasoningSegmentCount(message contracts.ChatMessage) int {
	if len(message.ReasoningSegments) > 0 {
		return len(message.ReasoningSegments)
	}
	if strings.TrimSpace(message.Reasoning) != "" {
		return 1
	}
	return 0
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
	group []contracts.ChatMessage, canRegenerate, last, defaultOpen bool) {
	key := group[0].ID
	open, explicit := st.processOpen[key]
	if !explicit {
		open = defaultOpen
	}
	tools := 0
	segments := 0
	var reasoning strings.Builder
	for _, message := range group {
		tools += len(message.Tools)
		segments += reasoningSegmentCount(message)
		reasoning.WriteString(message.Reasoning)
	}
	// The group's meta reports the whole turn: summed duration/tokens on the
	// last message's clock and actions (copy/regenerate apply to that result).
	metaMessage := group[len(group)-1]
	metaMessage.Usage = groupedMessageUsage(group)
	turn := ui.Column(c.Key("session.turn." + key)).FillWidth().Gap(design.Space3)
	turn.Children(func() {
		open = buildProcessFoldRow(c, t, runtime, st, key, open,
			processSummaryParts(st, reasoning.String(), segments, tools))
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
		// The recovery hint of a known tool failure stays outside the fold, so
		// the default folded turn still says what went wrong and how to fix it.
		if heading, kind, ok := st.groupToolRecovery(group); ok {
			buildRecoveryLine(c, t, st, runtime, heading, kind, "")
		}
		if last && metaMessage.Truncated && !metaMessage.Streaming {
			buildTruncatedNotice(c, t, st, metaMessage.ID)
		}
		// The whole turn's meta closes the row, revealed on hover or focus.
		buildAssistantMetaRow(c, t, runtime, st, metaMessage, canRegenerate,
			st.metaRowRevealed(c, metaMessage, turn.Hovered() || turn.FocusWithin()))
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
	// The reply's prose takes the reading measure (design.ProseMeasure) while a
	// table keeps the full column: uikit.MarkdownReply owns that split, so the
	// session only passes its theme (B-spec §3.2.1/§3.6).
	if message.Streaming {
		uikit.MarkdownReplyStream(c, message.Text, st.markdownOptions(t))
		return
	}
	uikit.MarkdownReply(c, message.Text, st.markdownOptions(t))
}

// buildAssistantTimeline draws the expanded process: the activity band
// (per-iteration reasoning disclosures and compact tool rows, indented Space4,
// Space1 row spacing, inline chevrons) interleaved with the reply prose in the
// recorded order (2026-11 audit US-01/US-02).
func buildAssistantTimeline(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, message contracts.ChatMessage) {
	timeline := ui.Column(c).FillWidth().Gap(design.Space1)
	timeline.Children(func() {
		renderAssistantBody(c, t, runtime, st, message)
	})
}

// timelineEvent is one renderable event of an assistant turn: a reasoning
// segment (segment >= 0) or a tool call (tool >= 0), positioned at offset bytes
// into the message's cumulative reply text.
type timelineEvent struct {
	offset  int
	segment int
	tool    int
}

// reasoningSegmentsOrFallback returns the message's per-iteration reasoning
// segments. A message that carries the flat Reasoning string but no segments
// (an older read model) keeps the legacy single aggregated block at offset 0.
func reasoningSegmentsOrFallback(message contracts.ChatMessage) []contracts.ReasoningSegment {
	if len(message.ReasoningSegments) > 0 {
		return message.ReasoningSegments
	}
	if strings.TrimSpace(message.Reasoning) == "" {
		return nil
	}
	return []contracts.ReasoningSegment{{Text: message.Reasoning, Iteration: 1, TextOffset: 0}}
}

// mergeTimelineEvents merges the reasoning segments and tool calls into one
// chronological event list. Both sequences are individually in arrival order
// with non-decreasing TextOffsets, and the arrival order across them is
// non-decreasing too (iteration k thinks, then calls; iteration k+1 starts
// after those calls). Offsets are clamped into [0, textLen].
//
// Tie convention: at equal offsets the read model records no finer order (a
// tool call carries no iteration), so ties alternate segment → tool → segment:
// a segment's own calls follow it, and the next iteration's thinking follows
// the previous calls. The first segment of iteration 1 precedes any tied tool;
// when the first recorded segment belongs to a later iteration (iteration 1
// reasoned nothing), the tied tools of the earlier iterations come first.
func mergeTimelineEvents(segments []contracts.ReasoningSegment, tools []contracts.ToolCall, textLen int) []timelineEvent {
	clamp := func(offset int) int {
		if offset < 0 {
			return 0
		}
		if offset > textLen {
			return textLen
		}
		return offset
	}
	events := make([]timelineEvent, 0, len(segments)+len(tools))
	si, ti := 0, 0
	for si < len(segments) || ti < len(tools) {
		switch {
		case si >= len(segments):
			events = append(events, timelineEvent{offset: clamp(tools[ti].TextOffset), segment: -1, tool: ti})
			ti++
		case ti >= len(tools):
			events = append(events, timelineEvent{offset: clamp(segments[si].TextOffset), segment: si, tool: -1})
			si++
		default:
			so, to := clamp(segments[si].TextOffset), clamp(tools[ti].TextOffset)
			takeSegment := false
			switch {
			case so < to:
				takeSegment = true
			case so > to:
				takeSegment = false
			case len(events) == 0:
				// Nothing rendered yet: iteration 1's thinking precedes its
				// calls; a first segment of a later iteration means the earlier
				// iterations called tools without reasoning.
				takeSegment = segments[si].Iteration <= 1
			default:
				// A tie means no prose separates the two events, so the offset
				// cannot order them; only the iteration boundary can. Iteration
				// 1's thinking precedes its calls (si == 0), and every later
				// segment follows the calls of the iteration before it — so on a
				// tie the tools go first. This must not depend on how many events
				// were already emitted (the old alternating rule made the order
				// flip as the turn grew, which re-ordered blocks between frames).
				takeSegment = si == 0
			}
			if takeSegment {
				events = append(events, timelineEvent{offset: so, segment: si, tool: -1})
				si++
			} else {
				events = append(events, timelineEvent{offset: to, segment: -1, tool: ti})
				ti++
			}
		}
	}
	return events
}

// activeReasoningSegment reports whether the turn's last reasoning segment is
// still the live channel: nothing has arrived after it (no prose, no tool call).
// While it is, the transcript shows the thinking row's own spinner and must not
// also show the generating row: two spinners a few DIP apart say the same thing
// twice (user report: 思考中… and 生成中 side by side).
func activeReasoningSegment(message contracts.ChatMessage) bool {
	if !message.Streaming {
		return false
	}
	segments := reasoningSegmentsOrFallback(message)
	if len(segments) == 0 {
		return false
	}
	last := segments[len(segments)-1]
	if len(message.Text) > last.TextOffset {
		return false // prose started after the thinking
	}
	for _, call := range message.Tools {
		if call.TextOffset > last.TextOffset {
			return false // a call started after the thinking
		}
	}
	return true
}

// renderAssistantBody draws one assistant reply in event order: thinking
// segments, prose and tool cards interleaved by their recorded TextOffsets
// (US-01/US-02). Live turns record where in the reply each tool call and each
// iteration's thinking arrived (ToolCall.TextOffset / ReasoningSegment.TextOffset,
// gated by ChatMessage.ToolTimelineKnown for the calls). An all-zero offset set
// on a live message is valid order — every event happened before any prose.
// Messages whose tool timeline was never recorded keep the whole-text-then-cards
// fallback for the calls (their reasoning segments still precede the prose).
func renderAssistantBody(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage) {
	// renderText draws one slice of the reply. Only the slice after the last
	// event — the tail still being written — is streaming markdown: a slice that
	// already sits before a tool row or a thinking block is finished text, and
	// re-parsing it as a live document let a block boundary move under the reader
	// on every delta (non-monotonic height, the jump the streaming turn showed).
	renderText := func(text, blockKey string, live bool) {
		if text == "" {
			return
		}
		// The block is keyed by the event it belongs to, never by its place in the
		// current list: mygo keeps a Markdown view's parsed document and per-block
		// caches in a ui.Local on the view's own element, and an unkeyed element is
		// identified by its sibling position — one event inserted above the others
		// then hands every later block its neighbour's state, which mygo correctly
		// discards ("another text, not more of it"), re-parsing and re-laying-out
		// the whole reply on each arrival (the flicker the streaming turn showed).
		uikit.MarkdownBlock(c, text, uikit.MarkdownBlockOptions{
			Key:       blockKey,
			Streaming: live && message.Streaming,
			Markdown:  st.markdownOptions(t),
		})
	}
	renderTool := func(tool contracts.ToolCall) {
		buildAssistantToolRow(c, t, runtime, st, message.ID, tool, true)
	}
	segments := reasoningSegmentsOrFallback(message)
	renderSegment := func(index int) {
		ui.Column(c).FillWidth().PaddingStart(design.Space4).Children(func() {
			buildReasoningSegmentDisclosure(c, t, st, message, segments[index], index, len(segments))
		})
	}

	text := message.Text
	// Only the explicit per-message flag certifies the tool offsets. A legacy
	// or not-yet-hydrated message may have no offsets at all (all zero), which
	// is indistinguishable from a live turn whose calls all came first, so the
	// heuristic "any offset > 0" cannot decide: the flag does. Without it the
	// thinking segments still render first, then the whole prose, then the
	// cards (the pre-timeline fallback).
	if !message.ToolTimelineKnown && len(message.Tools) > 0 {
		for index := range segments {
			renderSegment(index)
		}
		// No timeline to split by: the whole body is the one live slice while the
		// turn runs.
		renderText(text, message.ID+".body", true)
		for _, tool := range message.Tools {
			renderTool(tool)
		}
		return
	}
	// Chronological events: [prev:offset] text, the thinking block or the call,
	// then the next segment. An event whose offset went backwards (or past the
	// end) is clamped, so events that arrive together render consecutively.
	events := mergeTimelineEvents(segments, message.Tools, len(text))
	pos := 0
	for eventIndex, event := range events {
		offset := event.offset
		if offset < pos {
			offset = pos
		}
		// The slice before event k keeps the key .slice.k for its whole life: when
		// the event arrives the slice stops growing under the same identity, so its
		// parsed blocks survive the cut instead of being rebuilt.
		renderText(text[pos:offset], fmt.Sprintf("%s.slice.%d", message.ID, eventIndex), false)
		if event.segment >= 0 {
			renderSegment(event.segment)
		} else {
			renderTool(message.Tools[event.tool])
		}
		pos = offset
	}
	// The tail is the next slice, so it inherits the key the same text keeps once
	// an event cuts it.
	renderText(text[pos:], fmt.Sprintf("%s.slice.%d", message.ID, len(events)), true)
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
	open, seen := st.toolOpen[key]
	if !seen {
		// H15: a failure or a very short result is readable without a click;
		// long successful results stay folded. The call record (not its status
		// word) decides, so a shell command that exited nonzero opens too.
		open = uikit.SummaryOpenForCall(tool)
	}
	// One derivation for every surface: uikit folds a shell call's exit code
	// into the kind, so a bash that exited non-zero is a failure here too (the
	// row used to draw a check for it while the job panel said failed).
	statusKind := uikit.ToolCallStatusKind(call)
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
		Status:        uikit.ToolCallStatusLabel(st.locale, call),
		StatusColor:   uikit.StatusColor(t, statusKind),
		State:         statusKind,
		Running:       statusKind == uikit.StatusRunning,
		Preview:       uikit.ToolCallPreview(st.locale, call),
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
		Sections: uikit.ToolPanelSections(st.locale, call),
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

// buildReasoningSegmentDisclosure draws ONE iteration's thinking through the
// shared ReasoningTrace entry (US-01/US-02): open with the official spinner
// while that iteration is the one still thinking, folded to one quiet row
// (glue glyph, 思考, line count) once it ends, the muted reasoning Markdown in
// the panel. Each segment is keyed by its index — one key per segment, from the
// first one on — so a fold keeps its identity and its open state for the whole
// turn. The activity band wants the chevron right after the label, so
// InlineChevron is on.
func buildReasoningSegmentDisclosure(c *ui.Context, t *design.Tokens, st *state,
	message contracts.ChatMessage, segment contracts.ReasoningSegment, index, total int) {
	if strings.TrimSpace(segment.Text) == "" {
		return
	}
	// The key carries the index in every case, never only once a second segment
	// arrives: the state key and the component identity are derived from it, so a
	// key that changes when total goes 1 → 2 reset both the fold state and the
	// widget (the block rebuilt and re-laid out mid-stream, which is the visible
	// jump the turn used to show). index is stable for a given segment.
	stateKey := fmt.Sprintf("%s.seg%d", message.ID, index)
	open, chosen := st.thinkOpen[stateKey]
	// Only the last segment of a streaming message is still thinking: the
	// earlier iterations finished even while the turn runs.
	streaming := message.Streaming && index == total-1 && activeReasoningSegment(message)
	if !chosen {
		open = streaming
	}
	uikit.ReasoningTrace(c, uikit.ReasoningTraceOptions{
		Key:           "session.reasoning." + stateKey,
		Reasoning:     segment.Text,
		Streaming:     streaming,
		Open:          &open,
		InlineChevron: true,
		MarkerColumn:  true,
		OnToggle: func(next bool) {
			st.thinkOpen[stateKey] = next
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
	summary := usageSummaryText(st, message.Usage)
	// A row with no clock and no usage would be an isolated ⋯ (a restored turn
	// whose durable boundary could not be mapped honestly): draw no meta row at
	// all instead of an actions-only one.
	if !hasTime && summary == "" {
		return false
	}
	if hasTime {
		buildMessageTime(c, t, st.locale, message.CreatedAt)
	}
	if summary != "" {
		if hasTime {
			metaSeparator(c, t)
		}
		// Metadata is 12/18 at the smallest (AGENTS §3.12, VD-11): 11 DIP stays for
		// kbd and gutters, never for a sentence a reader has to parse.
		design.Apply(ui.Text(c, summary), t.Font(design.TypeXXS)).
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

// buildAssistantMetaRow draws the turn's meta as the message's last row: the
// clock, the usage summary and the ⋯ actions, right-aligned on a fixed 24 DIP
// line (B-spec §3.2.4). The line is always laid out, so revealing it never moves
// the transcript by a DIP; what changes is its opacity — it shows while the
// pointer rests on the message, when the keyboard focus is inside it, and for
// two seconds after a reply settles (metaRowRevealed). The row is built either
// way, so assistive technology always reads the time and the actions, and Tab
// reaches the ⋯ of a message the pointer never touched.
func buildAssistantMetaRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage, canRegenerate, shown bool) {
	if !assistantMetaHasContent(message) {
		return
	}
	if message.Streaming {
		// A streaming reply has no turn meta yet: its clock would be the start of
		// a run that has not finished and its usage a partial count, so the row
		// stays out until the turn settles (and then shows for two seconds
		// through metaRowRevealed).
		return
	}
	row := ui.Row(c).FillWidth().Height(design.Space6).
		AlignItems(ui.Center).Gap(design.Space1)
	if !shown {
		row = row.Opacity(0)
	}
	row.Children(func() {
		ui.Spacer(c).Grow(1)
		buildAssistantMetaContent(c, t, runtime, st, message, canRegenerate)
	})
}

// metaRevealDuration is how long a reply that just finished shows its meta row
// on its own: long enough to read the time and the tokens once, short enough
// that the transcript goes quiet again (B-spec §3.2.4: two seconds).
const metaRevealDuration = 2 * time.Second

// metaRowRevealed reports whether a message's meta row shows this frame: while
// the pointer rests on the message or the keyboard focus is inside it, and for
// metaRevealDuration after a streaming reply settles. The settle window needs no
// timer thread: the frame that starts it asks for one more frame at its end
// (Context.After), which hides the row again without another interaction. Under
// reduced motion the condition is unchanged — only the transitions are gone
// (design.ReduceMotion), so there is never a second set of rules.
func (st *state) metaRowRevealed(c *ui.Context, message contracts.ChatMessage, pointerInside bool) bool {
	if st.metaStreamed == nil {
		st.metaStreamed = make(map[string]bool)
	}
	if message.Streaming {
		st.metaStreamed[message.ID] = true
		delete(st.metaRevealAt, message.ID)
		return false
	}
	if pointerInside {
		return true
	}
	if st.metaStreamed[message.ID] {
		delete(st.metaStreamed, message.ID)
		if st.metaRevealAt == nil {
			st.metaRevealAt = make(map[string]time.Time)
		}
		st.metaRevealAt[message.ID] = c.Now()
		c.After(metaRevealDuration)
	}
	at, ok := st.metaRevealAt[message.ID]
	if !ok {
		return false
	}
	if c.Now().Sub(at) < metaRevealDuration {
		return true
	}
	delete(st.metaRevealAt, message.ID)
	return false
}

// assistantMetaHasContent reports whether a finished turn's meta row has
// something to say beyond its actions menu: the clock or the usage summary.
func assistantMetaHasContent(message contracts.ChatMessage) bool {
	return !message.CreatedAt.IsZero() || message.Usage != nil
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
	uikit.IconButton(c, uikit.IconButtonOptions{
		Key:      key,
		Label:    label,
		Tooltip:  label,
		Icon:     icons.More(),
		Size:     t.Scale(design.IconSizeS),
		Variant:  uikit.IconButtonNative,
		InkColor: t.LabelTertiary,
	}).PopoverMenuButton(build)
}

// metaSeparator is the muted dot between the parts of a compact meta row: a 2
// DIP round mark rather than a "·" glyph, so the separator belongs to the shape
// vocabulary and not to the type scale (VD-11; the row gap supplies the spacing).
func metaSeparator(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Box(c).Size(th.Space(0.5), th.Space(0.5)).Radius(design.RadiusMax).
		Background(t.BorderL3)
}

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
// edited message themselves. A message whose durable clock could not be mapped
// (a restored history without the event) draws no row: a lone ⋯ under the
// bubble is exactly the metadata noise the audit found, so copy/edit for that
// message are unavailable rather than hidden behind an unlabelled dot.
func buildUserMessageMeta(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage, shown bool) {
	hasText := strings.TrimSpace(message.Text) != ""
	if message.CreatedAt.IsZero() {
		return
	}
	// The same 24 DIP meta line and reveal rule as the reply's (one anatomy for
	// both authors): always laid out so it never jumps, faded out until the
	// pointer or the keyboard focus reaches the message.
	row := ui.Row(c).FillWidth().Height(design.Space6).
		AlignItems(ui.Center).Gap(design.Space1)
	if !shown {
		row = row.Opacity(0)
	}
	row.Children(func() {
		ui.Spacer(c).Grow(1)
		buildMessageTime(c, t, st.locale, message.CreatedAt)
		if !hasText {
			return
		}
		metaSeparator(c, t)
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
	requestDraftOverwrite(st, runtime, message.Text)
}

// requestDraftOverwrite stages text into the composer (edit-message, hero
// example): an empty draft takes it directly, but a non-empty draft is the
// user's half-written work — replacing it asks through ConfirmDanger first
// (UX-05, DESIGN rule 9: Cancel is the Default, the overwrite is the danger).
func requestDraftOverwrite(st *state, runtime contracts.UIRuntime, text string) {
	if st == nil {
		return
	}
	if strings.TrimSpace(st.draft) == "" || st.draft == text {
		st.draft = text
		st.slashDismissed = st.draft
		st.mentionDismissed = st.draft
		st.focusComposer = true
		runtime.Invalidate()
		return
	}
	st.draftOverwritePending = text
	st.draftOverwriteOpen = true
	runtime.Invalidate()
}

// buildDraftOverwriteConfirm draws the overwrite confirmation while one is
// pending: confirming applies the staged text, cancelling (button, Esc or
// backdrop) keeps the draft untouched.
func buildDraftOverwriteConfirm(c *ui.Context, st *state, runtime contracts.UIRuntime) {
	if st == nil || st.draftOverwritePending == "" {
		return
	}
	uikit.ConfirmDanger(c, &st.draftOverwriteOpen, uikit.ConfirmDangerOptions{
		Key:     "session.draftOverwrite",
		Title:   st.tr("覆盖当前草稿？", "Overwrite the current draft?"),
		Message: st.tr("输入框里还有未发送的内容，覆盖后无法恢复。", "The composer still holds unsent text; overwriting it cannot be undone."),
		Confirm: st.tr("覆盖草稿", "Overwrite draft"),
		Cancel:  st.tr("取消", "Cancel"),
		OnConfirm: func() {
			st.draft = st.draftOverwritePending
			st.draftOverwritePending = ""
			st.slashDismissed = st.draft
			st.mentionDismissed = st.draft
			st.focusComposer = true
			runtime.Invalidate()
		},
	})
	if !st.draftOverwriteOpen {
		// Cancelled: the draft stays as the user wrote it.
		st.draftOverwritePending = ""
	}
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
	design.Apply(ui.Text(c, clock), t.Font(design.TypeXXS)).
		TextColor(t.LabelTertiary).NoWrap().
		Description(uikit.FormatDateTime(locale, instant))
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
func buildMessageError(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage) {
	kind := strings.ToLower(strings.TrimSpace(message.ErrorKind))
	// The iteration cap running out is not a provider failure: explain the
	// limit instead of making rig's English diagnostic the headline. The cap is
	// assembly-level product definition (unlimited by default) — there is no
	// user setting that raises it (v0.2.50 removal), so the way forward is a
	// new conversation, not a settings action.
	if kind == contracts.MessageErrorKindIterations {
		buildRecoveryLine(c, t, st, runtime,
			st.tr("已达最大迭代次数", "Maximum iterations reached"),
			"", strings.TrimSpace(message.Error))
		design.Apply(ui.Text(c, st.tr(
			"本轮已达到工具/模型迭代上限。建议新建会话继续。",
			"This turn hit the tool/model iteration limit. Start a new conversation to continue.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(3)
		return
	}
	// Historical compatibility (v0.2.50): loop protection was removed, so no
	// live run produces kind loop anymore. A restored run_end persisted by an
	// older version's breaker gets one neutral historical note — no trip-budget
	// parsing, no blame copy — with the raw durable error kept as muted
	// diagnostic detail so no information is lost.
	if kind == contracts.MessageErrorKindLoop {
		design.Apply(ui.Text(c, st.tr(
			"该回合曾被旧版循环保护停止",
			"This turn was stopped by the legacy loop guard (removed in this version)")),
			t.TextMuted(design.TypeXS)).MaxLines(2)
		if strings.TrimSpace(message.Error) != "" {
			design.Apply(ui.Text(c, message.Error), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(3)
		}
		return
	}
	// An OS-sandbox denial is a permission-mode question, not a provider
	// failure (RG-03): explain the two-layer model in plain words and offer the
	// permission settings, instead of dumping rig's English attribution as the
	// headline.
	if kind == contracts.MessageErrorKindSandboxDenied {
		buildRecoveryLine(c, t, st, runtime,
			st.tr("疑似被 OS 沙箱拒绝", "Likely denied by the OS sandbox"),
			recoveryKindPermission, strings.TrimSpace(message.Error))
		design.Apply(ui.Text(c, st.tr(
			"当前权限模式可能限制了该操作（全局权限决定 OS 围栏，会话级『所有权限』不解除它）",
			"The global permission mode may restrict this operation (a session-level 'all permissions' does not lift the OS wall)")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(3)
		return
	}
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
	open, seen := st.toolOpen[key]
	if !seen {
		// H15: a failure or a very short result is readable without a click;
		// long successful results stay folded. The rule reads the call record, so
		// a shell command that exited nonzero opens its output like a failure.
		open = uikit.SummaryOpenForCall(call)
	}
	toggled := false
	uikit.ToolCallCard(c, uikit.ToolCallCardOptions{
		Key:    key,
		Call:   call,
		Locale: st.locale,
		// The state word comes from the same derivation as the row's kind, so a
		// decision card for a shell call that exited non-zero reads 失败 and never
		// disagrees with the glyph beside it.
		StatusLabel: uikit.ToolCallStatusLabel(st.locale, call),
		Sections:    uikit.ToolPanelSections(st.locale, call),
		Open:        &open,
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

// trajectoryItem is one entry of the execution trace: a tool call, one
// iteration's thinking, or the reply-body summary (US-04 — the 轨迹 tab carries
// the whole trace, not only the calls).
type trajectoryItem struct {
	key string
	// messageID is the message the entry belongs to: the jump back to the
	// conversation scrolls to the List row that renders it (a merged child
	// receipt shifts message indexes, so the id is the stable identity).
	messageID string
	// kind is "tool", "reasoning" or "text".
	kind    string
	tool    contracts.ToolCall
	segment contracts.ReasoningSegment
	// text is the reply body of a "text" entry (previewed at render).
	text string
}

// trajectoryItems flattens the session's execution trace in message order:
// each assistant message contributes its thinking segments and tool calls
// interleaved by their recorded offsets, then its reply-body summary. Tool
// calls keep their historical keys so an open detail survives the upgrade.
func trajectoryItems(chatSession contracts.ChatSession) []trajectoryItem {
	items := make([]trajectoryItem, 0, 8)
	for messageIndex, message := range chatSession.Messages {
		segments := reasoningSegmentsOrFallback(message)
		events := mergeTimelineEvents(segments, message.Tools, len(message.Text))
		for _, event := range events {
			if event.segment >= 0 {
				items = append(items, trajectoryItem{
					key:       fmt.Sprintf("%d-r%d", messageIndex, event.segment),
					messageID: message.ID,
					kind:      "reasoning",
					segment:   segments[event.segment],
				})
				continue
			}
			tool := message.Tools[event.tool]
			items = append(items, trajectoryItem{
				key:       fmt.Sprintf("%d-%d-%s", messageIndex, event.tool, tool.ID),
				messageID: message.ID,
				kind:      "tool",
				tool:      tool,
			})
		}
		if message.Role == contracts.ChatRoleAssistant && strings.TrimSpace(message.Text) != "" {
			items = append(items, trajectoryItem{
				key:       fmt.Sprintf("%d-text", messageIndex),
				messageID: message.ID,
				kind:      "text",
				text:      message.Text,
			})
		}
	}
	return items
}

// toolKnownDuration returns the call's wall time. It is known only when both
// ends are stamped: a zero start/end is unknown (restored from an event without
// a timestamp, or still running), and a reversed interval is a clock skew rather
// than a negative duration.
func toolKnownDuration(call contracts.ToolCall) (time.Duration, bool) {
	if call.StartedAt.IsZero() || call.EndedAt.IsZero() || call.EndedAt.Before(call.StartedAt) {
		return 0, false
	}
	return call.EndedAt.Sub(call.StartedAt), true
}

// formatTrajectoryDuration renders a tool duration with the usage strip's
// vocabulary: milliseconds under a second, one decimal of seconds above it, and
// "<1ms" instead of a misleading 0ms for a sub-millisecond call.
func formatTrajectoryDuration(duration time.Duration) string {
	if duration <= 0 {
		return formatUsageDuration(0)
	}
	if milliseconds := duration.Milliseconds(); milliseconds > 0 {
		return formatUsageDuration(milliseconds)
	}
	return "<1ms"
}

// trajectoryTimingEntry is one known tool duration in the timing overview.
type trajectoryTimingEntry struct {
	name     string
	duration time.Duration
}

// trajectoryTimingOverview is the pane's timing header: the sum of the calls
// whose both ends are known, plus the two slowest of them. Calls without a known
// interval are left out of both numbers (a guessed duration would be dishonest);
// with no known interval at all the header says "—".
func trajectoryTimingOverview(st *state, items []trajectoryItem) string {
	label := st.tr("已知耗时 ", "Known time ")
	total := time.Duration(0)
	entries := make([]trajectoryTimingEntry, 0, len(items))
	for _, item := range items {
		duration, ok := toolKnownDuration(item.tool)
		if !ok {
			continue
		}
		total += duration
		entries = append(entries, trajectoryTimingEntry{
			name:     textutil.FirstNonEmptyTrimmed(uikit.ToolDisplayName(st.locale, item.tool.Name), item.tool.Name),
			duration: duration,
		})
	}
	if len(entries) == 0 {
		return label + "—"
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].duration > entries[j].duration })
	if len(entries) > 2 {
		entries = entries[:2]
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.name+" "+formatTrajectoryDuration(entry.duration))
	}
	return label + formatTrajectoryDuration(total) +
		st.tr(" · 最慢：", " · Slowest: ") + strings.Join(parts, st.tr("、", ", "))
}

// trajectoryToolDurationLabel is one card's duration line: the measured interval
// or "—" when either end (or both) is unknown.
func trajectoryToolDurationLabel(st *state, call contracts.ToolCall) string {
	label := st.tr("用时 ", "Took ")
	if duration, ok := toolKnownDuration(call); ok {
		return label + formatTrajectoryDuration(duration)
	}
	return label + "—"
}

// buildTrajectoryPane is the execution trace (US-04): thinking segments, tool
// cards and reply-body summaries in arrival order on the shared reading column,
// following the end while the stream runs. The tab already says 轨迹, so the
// pane carries the counts (and the known-time overview) as muted lines instead
// of repeating the title and a badge.
func buildTrajectoryPane(c *ui.Context, t *design.Tokens, chatSession contracts.ChatSession, hasSession bool, st *state) {
	items := trajectoryItems(chatSession)
	toolCount, thoughtCount := 0, 0
	// The tool card's 步骤 N counts tool calls only: thinking/body rows share
	// the trace but are not execution steps (US-04).
	steps := make([]int, len(items))
	for index, item := range items {
		switch item.kind {
		case "tool":
			toolCount++
			steps[index] = toolCount
		case "reasoning":
			thoughtCount++
		}
	}
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		if len(items) > 0 {
			// The count and timing lines sit on the transcript's reading measure,
			// so they line up with the cards below and with the prose.
			uikit.ReadingColumn(c, func(body *ui.Context) {
				ui.Column(body).FillWidth().Gap(design.Space1).Children(func() {
					counts := fmt.Sprintf(st.tr("%d 个工具调用", "%d tool calls"), toolCount)
					if thoughtCount > 0 {
						counts += " · " + fmt.Sprintf(st.tr("%d 段思考", "%d reasoning segments"), thoughtCount)
					}
					design.Apply(ui.Text(body, counts), t.TextMuted(design.TypeXXS))
					design.Apply(ui.Text(body, trajectoryTimingOverview(st, items)),
						t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
					// The rows themselves are the way back to the conversation now
					// that the per-row buttons are gone; the pane says so once
					// instead of spending a column of controls on it.
					design.Apply(ui.Text(body, st.tr("选择一行后按回车，或在行上双击，跳转到该消息。",
						"Press Enter on a row, or double-click it, to jump to that message.")),
						t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
				})
			})
		}
		switch {
		case !hasSession:
			uikit.EmptyStateBlock(c,
				st.tr("还没有会话，暂无可查看的轨迹。", "No session yet; nothing to inspect."),
				st.tr("开始一段对话后，它的执行轨迹会出现在这里。", "Start a conversation and its execution trace shows here."), nil)
		case len(items) == 0:
			uikit.EmptyStateBlock(c,
				st.tr("本次会话还没有执行轨迹。", "No execution trace in this conversation yet."),
				st.tr("思考、工具调用与正文摘要会按时间顺序列在这里。", "Thinking, tool calls and reply summaries are listed here in order."), nil)
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
				return trajectoryItemLabel(st, items[index])
			}
			// The timeline follows the streaming end: a call appended while the
			// turn runs comes into view at the bottom, as the transcript does. The
			// List stops following when the reader scrolls away from the end.
			st.trajList.FollowEnd = true
			if st.trajSelected < -1 || st.trajSelected >= len(items) {
				st.trajSelected = -1
			}
			st.trajList.Selected = &st.trajSelected
			// The trace is rows with hairlines, not cards: no gap between them,
			// list motion from the shared tokens, and the keep-your-place flag
			// after a fold (VD-17).
			st.trajList.FollowAnimation = design.ListFollowAnimation(c)
			st.trajList.MoveAnimation = design.ListMoveAnimation(c)
			if st.trajHoldPlace {
				st.trajList.HoldPlace()
				st.trajHoldPlace = false
			}
			list := ui.List(c, &st.trajList, len(items)).
				Label(st.tr("会话轨迹", "Conversation trajectory")).
				Gap(0).
				PaddingY(design.Space4).
				Grow(1).
				Rows(func(row ui.ListRow) {
					item := items[row.Index]
					uikit.ReadingColumn(row.Context, func(body *ui.Context) {
						buildTrajectoryRow(body, t, st, chatSession, item, steps[row.Index])
					})
				})
			// Enter (or a double click) on the chosen row jumps, as the explicit
			// card action does. Arrow keys only move the choice: navigating the
			// timeline must not switch tabs under the reader.
			if st.trajSelected >= 0 && st.trajSelected < len(items) && list.Submitted() {
				st.jumpToTrajectoryMessage(chatSession, items[st.trajSelected])
			}
		}
	})
}

// trajectoryItemLabel is one trace row's accessible name: the tool's friendly
// name, the thinking row, or the reply body.
func trajectoryItemLabel(st *state, item trajectoryItem) string {
	switch item.kind {
	case "reasoning":
		return st.tr("思考", "Reasoning")
	case "text":
		return st.tr("正文", "Reply text")
	default:
		return textutil.FirstNonEmptyTrimmed(uikit.ToolDisplayName(st.locale, item.tool.Name),
			st.tr("工具调用", "Tool call"))
	}
}

// buildTrajectoryRow draws one trace entry: the shared tool card for a call,
// the shared reasoning disclosure for a thinking segment, and a quiet one-line
// preview for the reply body. Every row ends with the same jump action back to
// the conversation (the tool row also keeps its known-duration line).
func buildTrajectoryRow(c *ui.Context, t *design.Tokens, st *state,
	chatSession contracts.ChatSession, item trajectoryItem, step int) {
	// One row anatomy for every kind of trace entry (VD-17): the state glyph,
	// the name, the one-line summary and the duration on a single line, with a
	// hairline under it and no card. The row is the target — the list's choice
	// plus Enter or a double click jumps to the message — so the right edge no
	// longer carries a column of "show in conversation" buttons.
	body := ui.Column(c).FillWidth().PaddingY(design.Space1).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL1)
	body.Children(func() {
		// The row's own content takes the room; the jump back to the message is a
		// trailing action that appears while the pointer rests on the row or the
		// keyboard focus is inside it — one row anatomy, no column of buttons
		// (VD-17). The action keeps the row's name, so it is still one click away
		// for the pointer and the keyboard.
		line := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2)
		line.Children(func() {
			content := ui.Column(c).FillWidth().Grow(1).Shrink(1).MinWidthPercent(0)
			content.Children(func() {
				switch item.kind {
				case "reasoning":
					key := "session.trajectory.reasoning." + item.key
					open, seen := st.thinkOpen[key]
					if !seen {
						open = false
					}
					uikit.ReasoningTrace(c, uikit.ReasoningTraceOptions{
						Key:          key,
						Reasoning:    item.segment.Text,
						Open:         &open,
						MarkerColumn: true,
						// The chevron follows the row's own text (B-spec §3.2.5), the
						// same place the transcript band puts it.
						InlineChevron: true,
						OnToggle: func(next bool) {
							st.thinkOpen[key] = next
						},
						Locale:   st.locale,
						Markdown: st.reasoningMarkdownOptions(t),
					})
				case "text":
					ui.Row(c).FillWidth().Height(design.RowCompactHeight).
						AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
							ui.Icon(c, icons.Message()).Size(design.IconSizeS, design.IconSizeS).
								TextColor(t.LabelTertiary)
						})
						design.Apply(ui.Text(c, st.tr("正文", "Reply text")), t.Font(design.TypeXS)).
							FontWeight(design.WeightMedium).TextColor(t.LabelSecondary).NoWrap()
						design.Apply(ui.Text(c, uikit.PreviewProse(item.text, 96)), t.Font(design.TypeXXS)).
							TextColor(t.LabelTertiary).Grow(1).Shrink(1).MinWidthPercent(0).
							MaxLines(1).Ellipsis("…").
							Description(uikit.PreviewProse(item.text, 400))
					})
				default:
					key := "session.trajectory.tool." + item.key
					open, seen := st.toolOpen[key]
					if !seen {
						open = uikit.SummaryOpenForCall(item.tool)
					}
					statusKind := uikit.ToolCallStatusKind(item.tool)
					toggled := false
					uikit.ToolRow(c, uikit.ToolRowOptions{
						Key:          key,
						Icon:         uikit.ToolIcon(item.tool.Name),
						Name:         uikit.ToolDisplayName(st.locale, item.tool.Name),
						RawName:      item.tool.Name,
						Status:       uikit.ToolCallStatusLabel(st.locale, item.tool),
						StatusColor:  uikit.StatusColor(t, statusKind),
						State:        statusKind,
						Running:      statusKind == uikit.StatusRunning,
						Preview:      uikit.ToolCallPreview(st.locale, item.tool),
						Duration:     trajectoryToolDurationLabel(st, item.tool),
						Open:         open,
						MarkerColumn: true,
						Toggle: func(next bool) {
							toggled = true
							st.toolOpen[key] = next
							st.trajHoldPlace = true
							st.runtime.Invalidate()
						},
						Sections: uikit.ToolPanelSections(st.locale, item.tool),
					})
					if !seen && !toggled {
						st.toolOpen[key] = open
					}
				}
			})
			reveal := line.Hovered() || line.FocusWithin()
			action := uikit.IconButton(c, uikit.IconButtonOptions{
				Key:   "session.trajectory.jump." + item.key,
				Label: st.tr("在对话中定位 ", "Locate in conversation ") + trajectoryItemLabel(st, item),
				Icon:  icons.Message(),
				Size:  design.IconSizeS,
				OnClick: func() {
					st.jumpToTrajectoryMessage(chatSession, item)
				},
			})
			if !reveal {
				action = action.Opacity(0)
			}
		})
	})
}

func (st *state) jumpToTrajectoryMessage(chatSession contracts.ChatSession, item trajectoryItem) {
	row := transcriptRowForMessageID(st, chatSession, item.messageID)
	if row < 0 {
		return
	}
	st.tab = tabChat
	st.chatList.ScrollIntoView(row)
	st.trajSelected = -1
	st.runtime.Invalidate()
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
	// UD-02: the OS text-size preference multiplies the chat font-size setting;
	// the scaled size doubles as the cache key so a preference change rebuilds.
	size := t.Scale(st.messageFontSize())
	if st.md.options != nil && st.md.dark == t.Dark && st.md.size == size {
		return st.md.options
	}
	st.md.options = uikit.ChatMarkdown(t, size)
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
	size := t.Scale(st.messageFontSize() - 1)
	if size < t.Scale(11) {
		size = t.Scale(11)
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
	trimmed := strings.TrimSpace(scope)
	// A memory bound to one workspace persists as "workspace:<id>": show it as
	// the current project's workspace scope, never the raw storage value
	// (Wave 3; the settings page owns the cross-workspace label).
	if strings.HasPrefix(strings.ToLower(trimmed), contracts.MemoryScopeWorkspacePrefix) {
		return st.tr("工作区（本会话项目）", "Workspace (this project)")
	}
	switch strings.ToLower(trimmed) {
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
	// Queueing is the backend inbox: only a host that implements
	// contracts.PromptQueue can accept a prompt while the turn runs.
	queueAllowed := running && st.promptQueue != nil
	queuedTotal := 0
	if running {
		queuedTotal = len(st.queuedPromptEntries(activeID))
	}
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
	// Without a model there is nothing to call: sending is disabled and the
	// composer says why with a link to the model page (FR-31). The assembly's
	// default model keeps this false only in a genuinely unconfigured product.
	modelReady := strings.TrimSpace(chat.Config().Model) != ""
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
		sendHint = composerHintText(st, running, slashMenuActive, queueAllowed, modelReady)
	}
	hasContent := strings.TrimSpace(st.draft) != "" || len(st.pendingAttachments) > 0
	hasDraft := strings.TrimSpace(st.draft) != ""
	// A pending approval owns the turn: the send control stays disabled and out
	// of the focus ring (B-spec §3.2.7, F-02), the composer stops competing for
	// the screen's accent, and the reason shows beside the control.
	approvalPending := st.pendingApprovalCount()
	blocked := approvalPending > 0
	blockedReason := ""
	if blocked && !running {
		blockedReason = st.tr("处理审批后可发送", "Answer the approval to send")
	}
	// The send's one-line note, immediately left of the control (the placeholder
	// already covers an empty draft, so it stays empty then): the fix that is one
	// click away when the model is missing, the reason when a decision owns the
	// turn.
	sendNote := ""
	var sendNoteAction func()
	switch {
	case !modelReady && !running && hasContent:
		// Only while there is something to send: an empty composer already says
		// what to do (the placeholder), and a standing "configure a model" line
		// under an empty box is a warning about a state the user cannot act on
		// yet. Typing the first word is when the disabled send needs its reason.
		sendNote = st.tr("先配置模型", "Configure a model first")
		sendNoteAction = func() { openSettingsPage(navigator, settingsPageModel) }
	case blocked:
		sendNote = st.tr("处理审批后可发送", "Answer the approval to send")
	}
	queueTip := st.tr("当前回复尚未结束。加入队列后，本轮结束自动发送。",
		"Still generating. Queued messages send when this turn ends.")
	if len(st.pendingAttachments) > 0 {
		queueTip = st.tr("本轮只发送文字，附件留在输入框。",
			"This turn sends text only; attachments stay in the composer.")
	}
	var composerWidth float32
	// The fold points are the card's own width, measured last frame. They sit
	// below the point where the toolbar's three switchers, the key hint and the
	// send control stop fitting together; the card spans the transcript's text
	// column now, which is 32 DIP narrower than the old 960 frame, so the
	// thresholds moved with it rather than folding the hint at the default
	// window (1200 wide: 785 DIP card).
	compactControls := func() bool { return composerWidth > 0 && composerWidth < 720 }
	veryCompact := func() bool { return composerWidth > 0 && composerWidth < 440 }
	var queueSend func() bool
	if queueAllowed {
		queueSend = func() bool { return enqueueDraft(st, activeID) }
	}
	// The clipboard read runs from the input's paste callback, which outlives
	// this build pass: capture the services value now instead of the Context.
	composerServices := c.Services()
	composerCard, composerInput := uikit.ComposerShell(c, uikit.ComposerShellOptions{
		Key:         "session.composer",
		Draft:       &st.draft,
		Placeholder: placeholder,
		Running:     running,
		CardLabel:   st.tr("会话输入框", "Session composer"),
		// The input keeps the framework default (no Label: the placeholder is the
		// name). Deliberate, per Lead 2026-11-18: this screen has exactly one
		// composer, so the placeholder is unique and only moves with the draft —
		// there is no anchor ambiguity to fix (the one real ambiguity, several child
		// composers, is solved by the child's fixed 发送给 <名> name). A fixed role
		// name here would move 84 cross-owner assertions for a case that cannot
		// mislead anyone.
		// The conversation has exactly one composer on screen, so its input keeps
		// the placeholder as its name (the shell's fallback): a stable role name
		// would read better in theory, but the placeholder is a stable string here
		// too (it only changes with the draft/running state, and the card label
		// 会话输入框 already names the surface) and 81 frame tests across three
		// packages anchor on it. The child composer, where two names can collide,
		// passes an explicit InputLabel instead.
		Locale:    st.locale,
		InputBind: &st.composerInput,
		Focus:     &st.focusComposer,
		OnWidth:   func(width float32) { composerWidth = width },
		OnPasteImage: func() bool {
			return pasteClipboardImage(st, runtime, composerServices)
		},
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
			if st.commandRecovery != "" {
				buildComposerRecovery(ic, t, runtime, st)
			}
			// The missing model is the action row's note now ("先配置模型", one click
			// to the model settings), so the composer does not say it twice.
			if running && !queueAllowed {
				// Queueing is off: the composer is editable but Enter does not
				// send. Say where the text goes instead of leaving the user
				// wondering why Enter inserted a line break.
				design.Apply(ui.Text(ic, st.tr("回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送",
					"Generating: Enter is unavailable; your draft is kept for when this turn ends")),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(1)
			}
			if running && queueAllowed && len(st.pendingAttachments) > 0 {
				// The queue carries text only: say so instead of silently leaving
				// the staged files behind.
				design.Apply(ui.Text(ic, st.tr("本轮只发送文字，附件留在输入框。",
					"This turn sends text only; attachments stay in the composer.")),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(1)
			}
		},
		// The queue lives in the status bar under the card now (B-spec §3.2.6:
		// the bar's left zone is the run state and the queue ×N), so the card
		// itself stays one input and one action row.
		Toolbar: func(ic *ui.Context) {
			buildComposerToolbar(ic, t, runtime, navigator, chat, st, running,
				compactControls(), veryCompact())
		},
		Hint: func(*ui.Context) string {
			// A quiet key hint on wide composers only (C-23): Enter sends,
			// Shift+Enter breaks the line, and a slash draft explains what Enter
			// will do (C-05/C-22). Narrow composers keep the full copy in the
			// send control's description. While a turn runs the hint names the
			// two keys that work then (Enter queues, Esc stops) instead of
			// letting the controls alone carry the semantics (R4).
			if compactControls() {
				return ""
			}
			return composerVisibleHint(st, running, slashMenuActive, queueAllowed, modelReady)
		},
		SendEnabled:       hasContent && modelReady,
		SendBlocked:       blocked,
		SendBlockedReason: blockedReason,
		Notice:            sendNote,
		NoticeAction:      sendNoteAction,
		Attention:         blocked,
		SendLabel:         sendLabel,
		SendDescription:   sendHint,
		Send: func() bool {
			if !modelReady || blocked {
				return false
			}
			return submitDraft(chat, activeID, st)
		},
		QueueSend:    queueSend,
		QueueEnabled: hasDraft,
		QueueCount:   queuedTotal,
		QueueOpen:    &st.queueMenuOpen,
		QueuePanel: func(pc *ui.Context, anchor ui.Element) {
			if queuedTotal == 0 {
				return
			}
			uikit.QueuePanel(pc, t, uikit.QueuePanelOptions{
				Queued:      st.queuedPromptTexts(activeID),
				Running:     running,
				QueuePaused: !running && queuedTotal > 0,
				Open:        &st.queueMenuOpen,
				List:        &st.queueList,
				Locale:      st.locale,
				KeyPrefix:   "session.composer." + activeID,
				OnCancel: func(index int) {
					removeQueuedPrompt(st, activeID, index)
					st.runtime.Invalidate()
				},
				OnClear: func() {
					clearQueuedPrompts(st, activeID)
					st.runtime.Invalidate()
				},
				ConfirmOpen: ui.Local(anchor, "clearConfirm", func() bool { return false }),
			}, anchor)
		},
		// The queue's size is the ×N chip beside these controls (QueueCount), not
		// part of the button's name: a label that changes width with the count made
		// the row shift as prompts arrived, and "运行中 ×2" on the status bar read
		// as two turns running (p2-inbox audit).
		QueueLabel:       st.tr("排队", "Queue"),
		QueueDescription: queueTip,
		Stop: func() {
			cancelRunningTurn(chat, st, st.runtime, activeID)
		},
		Stopping:        running && st.stopRequested == activeID,
		StopLabel:       stopLabel,
		StopDescription: st.tr("停止 · 会中断当前回复", "Stop · interrupts the current reply"),
		// Files dragged from the file manager onto the composer are its
		// attachments (A4-I-03): the card lights its edge while they hover and
		// stages them through the same background read the picker uses. The
		// window's own handler only sees what no element takes, so a drop here
		// is never staged twice.
		OnFiles: func(paths []string) {
			stageAttachmentFilesAsync(runtime, st, activeID, paths, func(int) {
				runtime.Invalidate()
			})
		},
		DragHint: st.tr("松开以添加附件", "Drop to attach"),
		OnKey:    composerTextKey(runtime, chat, st, activeID, running, queueAllowed),
		Usage: func(ic *ui.Context) {
			// The DSH-style usage strip sits directly under the composer card:
			// one muted line of session statistics (rounds, steps, speed, tokens,
			// cache/context) with the exact numbers in its tooltip.
			buildSessionStatusStrip(ic, t, chat, st, chatSession, composerWidth)
		},
	})
	// The queued-prompts (inbox) popover is a reported overlay: ComposerShell
	// flips queueMenuOpen on an outside click, so report after it builds
	// (2026-11 UI review #13).
	st.reportOverlay("queue", st.queueMenuOpen)
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
	// The slash and @ suggestion menus are one reported overlay site (only one
	// shows at a time): the toast stack yields while a suggestion menu is open
	// (2026-11 UI review #13). The menus are derived from the draft each frame, so
	// a built suggestion row is the open signal.
	st.reportOverlay("suggestion", suggestionRow.Valid())
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
			enqueueDraft(st, activeID)
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
func composerHintText(st *state, running, slashMenuActive, queueAllowed, modelReady bool) string {
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
	if running {
		return composerRunningHintText(st, queueAllowed)
	}
	// Idle with a pending approval (possibly another conversation's): Esc still
	// answers it, so the hint names that instead of promising only send keys
	// (UX-17, PM-12's idle extension).
	if st.pendingApprovalCount() > 0 {
		return st.tr("Enter 或 Cmd/Ctrl+Enter 发送 · Esc 拒绝审批",
			"Enter or Cmd/Ctrl+Enter to send · Esc denies the approval")
	}
	if !modelReady {
		return st.tr("尚未配置模型：发送已禁用", "No model configured: sending is disabled")
	}
	return st.tr("Enter 或 Cmd/Ctrl+Enter 发送 · Shift+Enter 换行",
		"Enter or Cmd/Ctrl+Enter to send · Shift+Enter for a newline")
}

// pendingApprovalCount reports how many tool approvals are waiting. The composer
// hint reads it so the running key line never promises "Esc 停止" while the
// first Escape actually denies the pending approval (PM-12).
func (st *state) pendingApprovalCount() int {
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

// composerRunningHintText is the running-turn key line: while a reply streams
// Enter queues the draft (when queueing is on) and Esc stops the turn, so the
// hint has to say so instead of leaving the two keys undiscoverable (R4). With
// queueing off only Esc acts, and the copy says the stop interrupts the reply.
// A pending approval takes the first Escape (the card's own "Esc = 拒绝"), so
// the hint names that instead of the stop (PM-12).
func composerRunningHintText(st *state, queueAllowed bool) string {
	if st.pendingApprovalCount() > 0 {
		if queueAllowed {
			return st.tr("Enter 排队 · Esc 拒绝审批（再按停止）",
				"Enter to queue · Esc denies the approval (again to stop)")
		}
		return st.tr("Esc 拒绝审批 · 再按一次会中断回复",
			"Esc denies the approval · a second press interrupts the reply")
	}
	if queueAllowed {
		return st.tr("Enter 排队 · Esc 停止", "Enter to queue · Esc to stop")
	}
	return st.tr("Esc 停止 · 会中断当前回复", "Esc stops · interrupts the current reply")
}

// composerVisibleHint is the short copy the wide composer's single line can
// always show in both languages without an ellipsis: the idle state is just
// "Enter 发送 · Shift+Enter 换行" / "Enter to send · Shift+Enter newline" (the
// full chord list stays in the send tooltip), while the slash-draft states keep
// their explanatory copy because it explains what Enter will do.
func composerVisibleHint(st *state, running, slashMenuActive, queueAllowed, modelReady bool) string {
	if _, slashDraft := slashQuery(st.draft); slashDraft {
		return composerHintText(st, running, slashMenuActive, queueAllowed, modelReady)
	}
	if running {
		return composerRunningHintText(st, queueAllowed)
	}
	if st.pendingApprovalCount() > 0 {
		return st.tr("Enter 发送 · Esc 拒绝审批", "Enter to send · Esc denies the approval")
	}
	if !modelReady {
		return st.tr("尚未配置模型：发送已禁用", "No model configured: sending is disabled")
	}
	return st.tr("Enter 发送 · Shift+Enter 换行", "Enter to send · Shift+Enter newline")
}

// buildComposerToolbar is the composer's quiet action row: one attach button,
// the model chip and the single permission menu, all inside one official
// ui.Toolbar. In a toolbar mygo gives those controls no face of their own until
// hovered/pressed/focused (P2-12 replaced the row of small bordered boxes with
// no hand-written state), and the controls that do not fit fold into the
// toolbar's trailing overflow menu. The send/queue/stop control is drawn by
// ComposerShell outside this toolbar, so overflow can never take it.
//
// The permission menu now holds only the permission mode, so its button shows
// the current preset itself, and the model chip is always built: at a narrow
// width the official toolbar (or its overflow menu) keeps that entry reachable
// instead of the old very-compact session-menu fallback. Only the
// all-permissions preset warns on the button.
func buildComposerToolbar(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state, running, compactControls, veryCompact bool) {
	uikit.ComposerToolbar(c, uikit.ComposerToolbarOptions{
		Key:    "session.composer.toolbar",
		Label:  st.tr("输入框工具栏", "Composer toolbar"),
		Locale: st.locale,
		Items: func(ic *ui.Context) {
			buildAttachButton(ic, t, runtime, st, running)
			buildModelChip(ic, t, runtime, navigator, chat, st, compactControls, veryCompact)
			buildSessionMenu(ic, t, runtime, st)
		},
	})
}

// buildAttachButton is the composer's single attach control: one click opens
// the native file picker (startAttachmentPick) with no menu in between; drag
// and drop and image pasting stay the other attachment paths. When the picker
// is unavailable it reports the reason and the remaining paths in the
// composer's error row instead of asking for a typed path.
func buildAttachButton(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, running bool) {
	label := st.tr("附件", "Attach")
	tip := st.tr("添加文件或图片", "Add files or images")
	if running {
		tip = st.tr("添加文件或图片；本轮结束后仍会保留",
			"Add files or images; they stay staged for your next message")
	}
	if st.attachBusy {
		label = st.tr("选择中…", "Choosing…")
	}
	uikit.ComposerAttachButton(c, uikit.ComposerAttachOptions{
		Key:     "session.composer.attach",
		Label:   label,
		Tooltip: tip,
		Locale:  st.locale,
		Busy:    st.attachBusy,
		OnPick:  func() { startAttachmentPick(runtime, st) },
	})
}

// buildSessionMenu is the composer's single permission menu. After the approved
// audit it carries only the permission mode; the chat mode, persona, prompt
// presets, behavior-settings link and clear-remembered-tools row left the
// composer (their data keys and backend commands stay). The button is labelled
// with the current permission preset itself (工作区修改 / 所有权限 / 只读) so the
// composer never shows a separate destination name. Without a settings store
// there is no permission to show or choose, so the button is not built at all.
func buildSessionMenu(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st.settings == nil {
		return
	}
	label, warn := st.permissionMenuLabel()
	button := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		buildSessionPermissionMenuItems(m, runtime, st)
	})
	description := st.tr("权限模式：", "Permission mode: ") + label + "。" +
		permissionGlobalNote(st)
	button = button.Description(description).Tooltip(description)
	if warn {
		button.TextColor(t.WarningText)
	} else {
		design.Apply(button, t.TextMuted(design.TypeXS))
	}
}

// permissionMenuLabel names the composer's permission button: the current
// permission preset itself (工作区修改 / 所有权限 / 只读) marked 「全局」 because
// this switch writes the GLOBAL agent.permission — it sets both the approval
// gate and the OS sandbox for every conversation, unlike the automation
// surface's session-level override (2026-11 audit (a)/US-05). Only the
// all-permissions preset is warning-level; the read-only and workspace presets
// stay quiet (a stricter permission is not a warning). Without a settings store
// the button is not built; the label then falls back to the default preset.
func (st *state) permissionMenuLabel() (label string, warn bool) {
	global := st.tr("（全局）", " (global)")
	if st.settings == nil {
		return st.permissionLabel(contracts.PermissionWorkspace) + global, false
	}
	mode := permissionMode(st.settings)
	return st.permissionLabel(mode) + global, mode == contracts.PermissionFull
}

// buildSessionPermissionMenuItems adds the permission group header FIRST and
// then exactly the three selectable presets (工作区修改 / 所有权限 / 只读), one
// Radio item each. The disabled header row keeps the group name above its
// choices and carries the explanation in its tooltip, so the menu does not grow
// another row; the current value is already on the toolbar button.
func buildSessionPermissionMenuItems(m *ui.Menu, runtime contracts.UIRuntime, st *state) {
	if st.settings == nil {
		return
	}
	current := permissionMode(st.settings)
	// The button itself names the current permission, so the disabled group
	// header stays a plain group name. The one explanation is a visible note row
	// at the foot of the menu, and it says the two facts that matter without
	// repeating the settings page's paragraph: the permission text has one
	// canonical home (Settings → Behavior) and one short restatement here.
	m.Item(st.tr("权限模式", "Permission mode")).Disabled(true)
	m.Separator()
	for _, mode := range []string{contracts.PermissionWorkspace, contracts.PermissionFull, contracts.PermissionReadOnly} {
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
	// The active session's effective modes make the two-layer model concrete:
	// its approval policy may carry a session-level override (automation), but
	// the OS sandbox always follows the global preset (2026-11 audit (a)).
	m.Separator()
	m.Item(permissionGlobalNote(st)).Disabled(true)
	if disclosure := sessionPermissionDisclosure(st); disclosure != "" {
		m.Item(disclosure).Disabled(true)
	}
}

// permissionGlobalNote is the permission switch's one-sentence explanation: it
// says what the mode controls (the approval gate and the OS sandbox, two layers
// that this one switch sets globally) and the fact a reader needs to trust the
// shell. The composer button carries the same sentence as its description, so
// the two surfaces cannot drift into a third wording.
func permissionGlobalNote(st *state) string {
	return st.tr("全局生效：审批与系统沙箱都跟随此模式；沙箱可用时 shell 类工具自动放行。",
		"Applies everywhere: approval and the system sandbox follow this mode; shell tools are auto-allowed when the sandbox is available.")
}

// sessionPermissionDisclosure reads the active session's wave-A read model
// (EffectiveApprovalMode / EffectiveSandboxMode / PermissionScope) and renders
// the one-line two-layer disclosure for the permission menu, mirroring the
// behavior page's explanation. It returns "" when the backend does not expose
// the effective modes (a stripped assembly), so the menu then keeps just the
// three presets.
func sessionPermissionDisclosure(st *state) string {
	if st == nil {
		return ""
	}
	chat, ok := st.ctx.Get(contracts.KeyChat)
	if !ok || chat == nil {
		return ""
	}
	active := chat.ActiveSession()
	if active == "" {
		return ""
	}
	var info contracts.ChatSessionInfo
	found := false
	for _, session := range chat.Sessions() {
		if session.ID == active {
			info, found = session, true
			break
		}
	}
	if !found || info.EffectiveApprovalMode == "" {
		return ""
	}
	scope := st.tr("全局跟随", "follows global")
	if info.PermissionScope == contracts.PermissionScopeSession {
		scope = st.tr("会话级覆盖", "session override")
	}
	return st.tr("当前会话：审批＝", "Current session: approval = ") +
		st.permissionLabel(info.EffectiveApprovalMode) + "（" + scope + "）" +
		st.tr(" · 系统沙箱＝", " · System sandbox = ") +
		st.permissionLabel(info.EffectiveSandboxMode) +
		st.tr("（跟随全局）", " (follows global)")
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
	// When the model has no configured default (the family default), the follow
	// row and the built-in 「默认」 mode resolve to the same provider default, so
	// they merge into one row instead of offering two near-synonym "defaults"
	// (2026-11 audit US-07). With a configured default the two differ (follow =
	// the model's tuned default, 「默认」 = the provider default) and both stay.
	familyDefault := strings.TrimSpace(thinking.info.DefaultThinking) == ""
	followLabel := st.tr("跟随模型默认（"+label+"）", "Follow the model default ("+label+")")
	if familyDefault {
		followLabel = st.tr("默认（跟随模型）", "Default (follow model)")
	}
	follow := m.Item(followLabel)
	// The trade-off copy is an inline subtitle (a second muted line inside the
	// submenu row), not a Tooltip: a tooltip floats above the submenu and covered
	// the parent menu's first row and target band (2026-11 UI review P2).
	follow.Subtitle(st.tr("不设置覆盖，使用模型/提供方自身的默认推理设置。",
		"No override: use the model/provider's own default reasoning setting."))
	followChecked := thinking.explicit == "" ||
		(familyDefault && strings.EqualFold(thinking.explicit, contracts.ThinkingModeDefault))
	follow.Radio(followChecked)
	if follow.Chosen() {
		clearSessionThinkingOverride(st.settings, activeID, modelID)
		runtime.Invalidate()
	}
	modelKey := contracts.SessionModelThinkingSettingKey(activeID, modelID)
	for _, mode := range thinking.modes {
		mode := mode
		if familyDefault && mode.ID == contracts.ThinkingModeDefault {
			continue // merged into the follow row above
		}
		item := m.Item(mode.DisplayName(st.english()))
		// Each strength档 teaches its trade-off (deeper = slower/costlier) so the
		// choice is informed rather than a guess (2026-11 audit US-07). The hint is
		// an inline subtitle, not a floating tooltip that would cover the parent
		// menu (2026-11 UI review P2).
		if hint := thinkingModeHint(st, mode.ID); hint != "" {
			item.Subtitle(hint)
		}
		item.Radio(strings.EqualFold(thinking.explicit, mode.ID))
		if item.Chosen() {
			_ = st.settings.Set(modelKey, mode.ID)
			clearLegacySessionThinking(st.settings, activeID)
			runtime.Invalidate()
		}
	}
}

// thinkingModeHint is one reasoning mode's plain-language trade-off hint, shown
// as the menu item's inline subtitle (2026-11 audit US-07; 2026-11 UI review P2
// moved it off the floating tooltip that covered the parent menu). The default
// mode carries no hint (it is the follow row's subject); unknown/custom modes
// keep none.
func thinkingModeHint(st *state, modeID string) string {
	switch modeID {
	case contracts.ThinkingModeReasoningLow:
		return st.tr("轻度思考：更快、更省，复杂问题可能不够深入。",
			"Light thinking: faster and cheaper; may be too shallow for hard problems.")
	case contracts.ThinkingModeReasoningMedium:
		return st.tr("中度思考：速度与深度的平衡。",
			"Medium thinking: a balance of speed and depth.")
	case contracts.ThinkingModeReasoningHigh, contracts.ThinkingModeQwenDeep:
		return st.tr("深度思考：更慢、更贵，复杂问题更可靠。",
			"Deep thinking: slower and costlier; more reliable on hard problems.")
	case contracts.ThinkingModeQwenOn:
		return st.tr("开启思考：先推理再回答。", "Thinking on: reason before answering.")
	case contracts.ThinkingModeQwenOff:
		return st.tr("关闭思考：最快、最省，直接回答。", "Thinking off: fastest and cheapest; answer directly.")
	default:
		return ""
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
			item.Trailing(uikit.FormatContextWindow(info.ContextWindow))
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

// modelChipLabel is the model chip's visible text within its width: the full id
// when it fits, else a leading ellipsis and as much of the id's tail as the room
// takes (…-opus-4.1). Measurement uses the same face the chip draws, so the cut
// lands where the paint would.
func modelChipLabel(c *ui.Context, t *design.Tokens, model string, width float32) string {
	model = strings.TrimSpace(model)
	if model == "" || width <= 0 {
		return model
	}
	// The chip reserves room for its leading glyph and its trailing chevron; a
	// conservative Space3 on each side keeps the text clear of both.
	budget := width - design.Space3*2
	if budget <= 0 {
		return model
	}
	size, _, weight := t.Metrics(design.TypeXS)
	// MeasureText wraps at the budget, so the height tells whether it wrapped:
	// one line means the string fits.
	oneLine := func(s string) bool {
		w, h := c.MeasureText(0, ui.Span{
			Text: s, Font: design.FontStack(), Size: size, Weight: weight,
		})
		_, lineHeight := c.MeasureText(0, ui.Span{
			Text: "M", Font: design.FontStack(), Size: size, Weight: weight,
		})
		return w <= budget && h <= lineHeight+1
	}
	if oneLine(model) {
		return model
	}
	runes := []rune(model)
	for keep := len(runes) - 1; keep > 0; keep-- {
		candidate := "…" + string(runes[len(runes)-keep:])
		if oneLine(candidate) {
			return candidate
		}
	}
	return "…"
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
	// The model list is the only configured source of models; an effective
	// model without a list row comes from the assembly default. Say that on the
	// chip so "no models configured" in settings and a model id here do not
	// contradict each other (FR-26).
	catalog, hasCatalog := st.ctx.Get(contracts.KeyModelCatalog)
	var catalogModels []contracts.ModelInfo
	if hasCatalog && catalog != nil {
		catalogModels = catalog.Models()
	}
	unconfiguredDefault := effectiveModel != "" && hasCatalog && catalog != nil && len(catalogModels) == 0
	model := effectiveModel
	if model == "" {
		model = st.tr("未配置", "Not set")
	} else if unconfiguredDefault {
		model += st.tr("（默认模型，尚未配置连接）", " (default model, no connection configured)")
	}
	// A narrow composer gives the model chip a bounded measure so the
	// low-frequency controls (or the labeled 更多工具 menu) always fit beside
	// the send control. The bound is a width, not a shortened string: the chip
	// ellipsizes at paint time and the full id stays in its tooltip and its
	// accessible name (A1-matrix: data-layer truncation leaves tooltip, a11y and
	// copy with a cut string).
	var chipWidth float32
	switch {
	case veryCompact:
		chipWidth = design.Space8 * 4
	case compactControls:
		chipWidth = design.Space8 * 6
	}
	switcher, canSwitch := chat.(contracts.SessionModelSwitcher)
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
		chip := ui.PopoverMenuButton(c, model, func(m *ui.Menu) {
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
			if unconfiguredDefault {
				m.Item(st.tr("默认模型来自装配，添加模型行后可覆盖",
					"The default model comes from the assembly; add a model row to override")).Disabled(true)
			}
			modelRow := m.Submenu(st.tr("模型", "Model"), func(sub *ui.Menu) {
				buildSessionModelChoices(sub, runtime, st, switcher, activeID, effectiveModel, models)
			})
			modelRow.Trailing(sessionChipModelLabel(st, models, effectiveModel))
			if hasThinking {
				buildSessionThinkingRow(m, runtime, st, thinking, activeID)
			}
			// The menu's order carries its grouping even where the theme's 1 DIP
			// separator is faint: the two option submenus are the whole menu from
			// the top, one official Separator, and the single action last. The
			// order is pinned by the frame tests (mention_effort_test.go asserts
			// 模型 / 推理等级 / - / 打开模型设置…), so a later edit cannot move the
			// action up among the options.
			m.Separator()
			if m.Item(st.tr("打开模型设置…", "Open model settings…")).Chosen() {
				openSettingsPage(navigator, settingsPageModel)
			}
		}, ui.PopoverMenuButtonOptions{Open: &st.agents.modelMenuOpen})
		// The chip's visible text keeps the model id's *tail* when it must be cut:
		// a model is recognized by its last words ("…-opus-4.1", not "claude-…"),
		// the way AppKit truncates a file name from the start. The full id stays in
		// the accessible name and the tooltip. mygo's own paint-time ellipsis cuts
		// the end of a PopoverMenuButton's label, so the cut is made here, by
		// measure, against the room the chip actually has.
		if chipWidth > 0 {
			chip = chip.MaxWidth(chipWidth)
		}
		chip = chip.Label(modelChipLabel(c, t, chipModel, chipWidth)).
			Description(tooltip).Tooltip(tooltip).Transition(design.TransitionColors(c))
		// The model menu (and its 模型/推理等级 submenus) is one reported overlay:
		// the toast stack yields while it is open (2026-11 UI review #13).
		st.reportOverlay("model", st.agents.modelMenuOpen)
	} else {
		modelButton := ui.Button(c, model).Tooltip(st.tr("打开模型设置", "Open model settings"))
		if modelButton.Clicked() {
			openSettingsPage(navigator, settingsPageModel)
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
			return uikit.FormatContextWindow(info.ContextWindow)
		}
	}
	return ""
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

// sessionUsageSpeed is the run's average output speed in tokens per second, or 0
// when the turn is too short for the number to mean anything.
func sessionUsageSpeed(stats sessionUsageStats) float64 {
	if stats.durationMS < minStatusSpeedDurationMS || stats.completion <= 0 {
		return 0
	}
	return float64(stats.completion) / (float64(stats.durationMS) / 1000)
}

// sessionUsageCachePercent is the share of the prompt tokens that came from the
// provider's cache, or 0 when the session has no prompt accounting.
func sessionUsageCachePercent(stats sessionUsageStats) int {
	if stats.prompt <= 0 {
		return 0
	}
	percent := int(math.Round(float64(stats.cached) * 100 / float64(stats.prompt)))
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent
}

// buildSessionStatusStrip renders the status bar right under the composer (S8,
// B-spec §3.2.6): one 32 DIP row in three zones — the run state and the queue on
// the left, the context gauge with its 详情 popover on the right. It is
// information, never a card; every sentence in it is at least 12 DIP, the numbers
// are mono so they line up as the context grows, and the exact figures live in
// the 详情 popover instead of a six-item line.
func buildSessionStatusStrip(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	st *state, session contracts.ChatSession, composerWidth float32) {
	stats := collectSessionUsage(session)
	window := 0
	if stats.hasUsage {
		window = st.sessionContextWindow(chat, session)
	}
	used := 0
	if stats.contextTokens > 0 && window > 0 {
		used = stats.contextTokens
	}
	// The bar names the run as a run, not as this particular message's
	// generation: the transcript's own live line already says 生成中 inside the
	// reply, and two different words keep the chrome and the message from
	// repeating each other (F-06: running = accent spinner + a neutral word).
	statusLabel := st.tr("运行中", "Running")
	if session.Running && st.stopRequested == session.ID {
		statusLabel = st.tr("停止中…", "Stopping…")
	}
	// The queue badge stays in the bar only when the composer shows no queue
	// button (an idle conversation with queued prompts keeps them from view
	// otherwise); a running turn carries the count on the 排队 control itself.
	queued := []string(nil)
	if !session.Running {
		queued = st.queuedPromptTexts(session.ID)
	}
	// While the conversation tab shows the streaming reply, its own live line
	// already says the turn is generating, so the bar drops its run row (the user
	// saw 生成中 and 运行中 stacked). Another tab, a child view or a stop in flight
	// keeps the row: nothing else on screen says it then.
	runStateHidden := session.Running && st.tab == tabChat &&
		strings.TrimSpace(st.agents.childID) == "" && st.stopRequested != session.ID
	uikit.StatusStrip(c, uikit.StatusStripOptions{
		Running:        session.Running,
		RunStateHidden: runStateHidden,
		StatusLabel:    statusLabel,
		Queued:         queued,
		QueueOpen:      &st.queueMenuOpen,
		QueueList:      &st.queueList,
		QueueError:     st.queueActionErr,
		OnQueueCancel: func(index int) {
			removeQueuedPrompt(st, session.ID, index)
			st.runtime.Invalidate()
		},
		OnQueueClear: func() {
			clearQueuedPrompts(st, session.ID)
			st.runtime.Invalidate()
		},
		Used:   used,
		Window: window,
		// The bar's read-out in the usage audit's order: the run's shape, its
		// volume, then the context ring. A part without a truthful figure is left
		// out by the strip itself.
		Metrics: uikit.StatusStripMetrics{
			Rounds:       stats.rounds,
			Steps:        stats.steps,
			Speed:        sessionUsageSpeed(stats),
			Tokens:       stats.completion,
			CachePercent: sessionUsageCachePercent(stats),
		},
		// A narrow composer keeps the ring alone: the two text groups would wrap the
		// bar onto a second line, and every figure they carry is either inlined in
		// the wide bar or in the ring's own accessible copy (used / window).
		// The narrow window (1024) gives the composer card about 684 DIP; the
		// standard one gives 785. 720 is the same cutoff the composer's own compact
		// toolbar uses, so the bar folds exactly when the controls above it do.
		Narrow:    composerWidth > 0 && composerWidth < 720,
		KeyPrefix: session.ID,
		Locale:    st.locale,
	})
}

// recoveryKinds are the known backend failure classes the conversation can
// explain with a localized heading and a one-click fixing action (FR-31).
const (
	recoveryKindModel      = "model"
	recoveryKindWorkspace  = "workspace"
	recoveryKindSearch     = "search"
	recoveryKindPermission = "permission"
)

// recoveryFor maps one backend failure message to a localized heading and the
// action that fixes it. The mapping is deliberately narrow — only the classes
// the product can repair from one place — and everything else keeps the raw
// backend text so no diagnostic information is lost.
func (st *state) recoveryFor(text string) (heading, kind string, ok bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "[sandbox:"):
		// rig v0.18.1+'s stable OS-sandbox attribution line (RG-03): explain the
		// two-layer permission model and offer the permission settings instead
		// of leaving the user at the OS wall with a raw English diagnostic.
		return st.tr("疑似被 OS 沙箱拒绝", "Likely denied by the OS sandbox"),
			recoveryKindPermission, true
	case strings.Contains(lower, "no model configured"):
		return st.tr("未配置模型：请先在设置 → 模型中添加模型",
			"No model configured: add one in Settings → Model"), recoveryKindModel, true
	case strings.Contains(lower, "no roots configured"):
		return st.tr("未打开工作区：文件与命令工具需要一个工作区",
			"No workspace open: file and command tools need one"), recoveryKindWorkspace, true
	case strings.Contains(lower, "web search endpoint"), strings.Contains(lower, "websearch.endpoint"):
		return st.tr("未配置搜索后端：在设置 → 搜索中填写端点",
			"Search backend not configured: add an endpoint in Settings → Search"), recoveryKindSearch, true
	case strings.Contains(lower, "being compacted"):
		return st.tr("会话正在压缩，请稍后再试",
			"Session is being compacted; try again shortly"), "", true
	case strings.Contains(lower, "history changed during compaction"):
		return st.tr("压缩期间历史被修改，已取消",
			"History changed during compaction; cancelled"), "", true
	}
	return "", "", false
}

// turnToolRecovery reports the localized fix for one message's most recent
// failed tool call, when that failure is a class the UI can explain (no
// workspace / unconfigured search backend). Only the latest failing call is
// considered: a retry repeats the same failure and identical hints would stack.
func (st *state) turnToolRecovery(message contracts.ChatMessage) (heading, kind string, ok bool) {
	return st.groupToolRecovery([]contracts.ChatMessage{message})
}

// groupToolRecovery is turnToolRecovery for a grouped multi-step turn: the
// latest failing tool call across the group's messages in reverse order.
func (st *state) groupToolRecovery(group []contracts.ChatMessage) (heading, kind string, ok bool) {
	for messageIndex := len(group) - 1; messageIndex >= 0; messageIndex-- {
		tools := group[messageIndex].Tools
		for index := len(tools) - 1; index >= 0; index-- {
			tool := tools[index]
			if uikit.ToolCallStatusKind(tool) != uikit.StatusError {
				continue
			}
			// The persisted classification wins: the bounded summary may have
			// cut the attribution line off (RG-03).
			if tool.ErrorKind == contracts.MessageErrorKindSandboxDenied {
				return st.tr("疑似被 OS 沙箱拒绝", "Likely denied by the OS sandbox"),
					recoveryKindPermission, true
			}
			return st.recoveryFor(strings.TrimSpace(tool.Summary))
		}
	}
	return "", "", false
}

// recoveryActionLabel names the button of one recovery class.
func (st *state) recoveryActionLabel(kind string) string {
	switch kind {
	case recoveryKindModel:
		return st.tr("打开模型设置", "Open model settings")
	case recoveryKindWorkspace:
		return st.tr("打开工作区", "Open workspace")
	case recoveryKindSearch:
		return st.tr("打开搜索设置", "Open search settings")
	case recoveryKindPermission:
		return st.tr("打开权限设置", "Open permission settings")
	default:
		return ""
	}
}

// runRecovery performs the one-click fix of a known failure: the model/search
// pages open the settings surface on that page; the workspace action runs the
// native folder picker (the workspace seam the @ menu uses).
func (st *state) runRecovery(runtime contracts.UIRuntime, kind string) {
	switch kind {
	case recoveryKindModel:
		openSettingsPage(st.navigator, settingsPageModel)
	case recoveryKindSearch:
		openSettingsPage(st.navigator, settingsPageSearch)
	case recoveryKindPermission:
		openSettingsPage(st.navigator, settingsPageBehavior)
	case recoveryKindWorkspace:
		var workspace contracts.WorkspaceService
		if raw, ok := st.ctx.Get(contracts.KeyWorkspace); ok {
			workspace, _ = raw.(contracts.WorkspaceService)
		}
		startWorkspaceCreate(runtime, workspace, st)
	}
}

// reportComposerFailure records one composer-level send failure. Known classes
// become a localized heading plus action with the raw text kept as selectable
// detail; unknown failures keep the previous prefix+raw line.
func (st *state) reportComposerFailure(err error) {
	if st == nil || err == nil {
		return
	}
	if heading, kind, ok := st.recoveryFor(err.Error()); ok {
		st.commandErr = ""
		st.commandRecovery = heading
		st.commandRecoveryKind = kind
		st.commandDetail = err.Error()
		return
	}
	st.commandErr = st.tr("发送失败：", "Send failed: ") + err.Error()
	st.clearComposerRecovery()
}

// clearComposerFailure clears every composer failure line (after a delivered
// message or when the conversation changes).
func (st *state) clearComposerFailure() {
	if st == nil {
		return
	}
	st.commandErr = ""
	st.clearComposerRecovery()
}

// clearComposerRecovery clears only the mapped-failure fields.
func (st *state) clearComposerRecovery() {
	st.commandRecovery = ""
	st.commandRecoveryKind = ""
	st.commandDetail = ""
}

// buildTruncatedNotice is the truncated-answer hint under the final reply: the
// provider stopped at the output-token limit (finish_reason "length"), so the
// answer is partial. The explanation folds out on demand and says how to
// continue or raise the limit.
func buildTruncatedNotice(c *ui.Context, t *design.Tokens, st *state, messageID string) {
	root := ui.Column(c.Key("session.truncated." + messageID)).FillWidth()
	open := ui.Local(root, "open", func() bool { return false })
	root.Children(func() {
		parts := ui.CollapsibleBase(c.Key("session.truncated.trigger."+messageID), open)
		trigger := parts.Trigger.Grow(1).AlignItems(ui.Center).Gap(design.Space2).
			PaddingY(design.Space1).Radius(design.RadiusSM).
			Label(st.tr("回答被截断", "Answer truncated"))
		if parts.Trigger.Hovered() || parts.Trigger.Focused() {
			trigger = trigger.Background(t.BgHover)
		}
		trigger.Children(func() {
			ui.Icon(c, icons.AlertTriangle()).Size(design.Space4, design.Space4).
				TextColor(t.WarningText)
			design.Apply(ui.Text(c, st.tr("回答被截断", "Answer truncated")),
				t.Font(design.TypeXXS)).TextColor(t.WarningText)
			ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
				TextColor(t.LabelTertiary).Rotate(90 * parts.Progress())
		})
		parts.Panel(func() {
			design.Apply(ui.Text(c, st.tr(
				"模型在输出上限处停止，这条回答可能不完整。可以要求它继续，或在设置 → 模型中提高最大输出 tokens。",
				"The model stopped at its output limit, so this answer may be incomplete. Ask it to continue, or raise the max output tokens in Settings → Model.")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(3)
		})
	})
}

// buildRecoveryLine is the shared localized-failure row: the localized heading,
// the fixing action and, when the caller has one, the raw backend text as
// selectable technical detail. Nothing is hidden: the raw text stays copyable.
func buildRecoveryLine(c *ui.Context, t *design.Tokens, st *state, runtime contracts.UIRuntime,
	heading, kind, detail string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, heading), t.Font(design.TypeXS)).
				TextColor(t.DangerText).Grow(1).MaxLines(2).Selectable()
			if kind != "" {
				if label := st.recoveryActionLabel(kind); label != "" {
					button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
						Height(design.RowCompactHeight).Shrink(0)
					if button.Clicked() {
						st.runRecovery(runtime, kind)
					}
				}
			}
		})
		if detail != "" {
			design.Apply(ui.Text(c, st.tr("技术细节：", "Technical details: ")+detail),
				t.MonoFamily(), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(3).Selectable()
		}
	})
}

// buildComposerRecovery renders one mapped send failure under the composer
// input (the attachment row owns the generic raw error line).
func buildComposerRecovery(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st.commandRecovery == "" {
		return
	}
	buildRecoveryLine(c, t, st, runtime, st.commandRecovery, st.commandRecoveryKind, st.commandDetail)
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
	// Without a model the composer disables sending and explains why (FR-31);
	// the keyboard path must respect the same gate instead of firing a request
	// that can only fail.
	if strings.TrimSpace(chat.Config().Model) == "" {
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
	} else if err := deliverPrompt(chat, st, activeID, prompt); err != nil {
		// C-04: a failed send must not be silent. Known backend classes get a
		// localized heading with a recovery action and keep the raw text as
		// selectable detail; unknown failures keep the prefix+raw line. The
		// draft is kept so the user can retry either way.
		st.reportComposerFailure(err)
		return false
	}
	// A delivered message clears an older composer error (e.g. a stop failure).
	st.clearComposerFailure()
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
	// The delivered draft must not resurrect after a restart (UX-18).
	st.persistDraftText(activeID, "")
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
		// The Esc arbitration is keyboard plumbing for the window fallback
		// (UX-03), not a slash action: it must not take a menu slot from the
		// user-facing commands.
		if cmd.ID == "session.escape" {
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
			title := textutil.FirstNonEmptyTrimmed(cmd.Title, cmd.ID)
			// The same skeleton as the @ menu: 32 DIP, Space2 sides, the name at
			// 14/500 and the command's sentence at 12/18 beside it — the hint is a
			// sentence, so it uses the metadata step, never the 11px micro label
			// (DESIGN §5.3, audit UD-05).
			row := composerSuggestionRow(c, t, composerSuggestionRowOptions{
				Key:      "session.slash." + cmd.ID,
				Label:    title,
				Title:    title,
				Detail:   strings.TrimSpace(cmd.Hint),
				Selected: index == sel,
				Index:    index,
				SetSize:  len(matches),
				OnClick: func() {
					st.slashSel = index
					executeSlashCommand(runtime, st, cmd)
					st.composerInput.Focus(c)
				},
			})
			if index == sel {
				highlighted = row
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
		st.mentionNoWorkspace = false
		st.mentionWorkspaces = nil
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
	// No current workspace is a normal, explainable state (R7), not a read
	// failure: check it first so the menu can show the localized explanation and
	// the open/create recovery actions instead of the backend's English error.
	// The registered workspace list is cached here (once per menu open) for the
	// 打开工作区 action, so the frame never reads it.
	if _, ok, err := workspace.Current(ctx); err != nil || !ok {
		st.mentionFiles = nil
		st.mentionErr = nil
		st.mentionNoWorkspace = true
		st.mentionWorkspaces, _ = workspace.List(ctx)
		return
	}
	st.mentionNoWorkspace = false
	st.mentionWorkspaces = nil
	var (
		entries []contracts.FileEntry
		loadErr error
	)
	if strings.TrimSpace(query) == "" {
		entries, loadErr = workspace.Files(ctx, "")
	} else {
		entries, loadErr = workspace.Search(ctx, query, maxMentionFiles)
	}
	if loadErr != nil && mentionNoWorkspaceError(loadErr) {
		// The backend can lose the current workspace between Current and the
		// read: keep the same explainable state instead of surfacing its raw
		// "workspace: no current workspace" string.
		st.mentionFiles = nil
		st.mentionErr = nil
		st.mentionNoWorkspace = true
		st.mentionWorkspaces, _ = workspace.List(ctx)
		return
	}
	if len(entries) > maxMentionFiles {
		entries = entries[:maxMentionFiles]
	}
	st.mentionFiles, st.mentionErr = entries, loadErr
}

// mentionNoWorkspaceError recognizes the workspace backend's "no current
// workspace" error without importing the backend module (the UI may not import
// it): the @ menu maps it to the localized, recoverable state (R7).
func mentionNoWorkspaceError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no current workspace")
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
			case st.mentionNoWorkspace:
				// An explainable state with a recovery path, separated from the
				// candidate rows by the divider the session section draws below;
				// the text is selectable so a user can copy the state.
				buildMentionNoWorkspaceBlock(c, t, runtime, workspace, st)
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

// WorkspacePicker is the @ menu's 新建工作区 folder-picker seam: the default
// opens the native mygo directory dialog; tests replace it with a scripted
// selection so no desktop backend is needed. The title is a parameter so a
// language switch cannot race another window's dialog.
var WorkspacePicker = func(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     title,
		Directory: true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// buildMentionNoWorkspaceBlock is the @ menu's no-workspace state: the localized
// explanation (instead of the backend's English error), a hint and the
// open/create recovery actions (R7).
func buildMentionNoWorkspaceBlock(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	workspace contracts.WorkspaceService, st *state) {
	ui.Column(c).FillWidth().Gap(design.Space2).
		PaddingX(design.Space2).PaddingY(design.Space1).Children(func() {
		design.Apply(ui.Text(c, st.tr("当前会话没有工作区", "This conversation has no workspace")),
			t.Font(design.TypeXS)).TextColor(t.LabelSecondary).Selectable()
		design.Apply(ui.Text(c, st.tr("打开或新建工作区后，可用 @ 引用其中的文件。",
			"Open or create a workspace to reference its files with @.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		if st.mentionActionErr != "" {
			uikit.ErrorLine(c, st.mentionActionErr)
		}
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if len(st.mentionWorkspaces) > 0 {
				open := ui.PopoverMenuButton(c, st.tr("打开工作区", "Open workspace"), func(m *ui.Menu) {
					for _, entry := range st.mentionWorkspaces {
						entry := entry
						item := m.Item(entry.Name).Trailing(entry.Root)
						if item.Chosen() {
							openWorkspaceForMention(runtime, workspace, st, entry.ID)
						}
					}
				})
				design.Apply(open, t.SecondaryButton()).Height(design.RowCompactHeight)
			}
			create := design.Apply(ui.Button(c, st.tr("新建工作区", "New workspace")),
				t.SecondaryButton()).Height(design.RowCompactHeight)
			create.Disabled(st.workspaceBusy)
			if create.Clicked() && !st.workspaceBusy {
				startWorkspaceCreate(runtime, workspace, st)
			}
		})
	})
}

// openWorkspaceForMention makes one registered workspace current and refreshes
// the @ menu's file list; the read model is a bounded in-memory call.
func openWorkspaceForMention(runtime contracts.UIRuntime, workspace contracts.WorkspaceService,
	st *state, id string) {
	if workspace == nil || strings.TrimSpace(id) == "" {
		return
	}
	if _, err := workspace.Open(context.Background(), id); err != nil {
		st.mentionActionErr = st.tr("打开工作区失败：", "Failed to open the workspace: ") + err.Error()
	} else {
		st.mentionActionErr = ""
		st.mentionNoWorkspace = false
		st.mentionFresh = false
		st.toast(contracts.NotifySuccess, st.tr("已打开工作区", "Workspace opened"))
	}
	runtime.Invalidate()
}

// startWorkspaceCreate runs the native directory picker off the UI thread (the
// dialog blocks its caller) and publishes the created workspace through
// UIRuntime.Post, so a slow picker never freezes a frame.
func startWorkspaceCreate(runtime contracts.UIRuntime, workspace contracts.WorkspaceService, st *state) {
	if workspace == nil || st.workspaceBusy {
		return
	}
	st.workspaceBusy = true
	title := st.tr("选择工作区文件夹", "Choose workspace folder")
	runtime.Invalidate()
	go func() {
		dir, err := WorkspacePicker(title)
		runtime.Post(func() {
			st.workspaceBusy = false
			switch {
			case err != nil:
				st.mentionActionErr = st.tr("无法打开文件夹选择器：", "Could not open the folder picker: ") + err.Error()
			case strings.TrimSpace(dir) == "":
				// Canceled: nothing changes.
			default:
				if _, createErr := workspace.Create(context.Background(), filepath.Base(dir), dir); createErr != nil {
					st.mentionActionErr = st.tr("打开工作区失败：", "Failed to open the workspace: ") + createErr.Error()
				} else {
					st.mentionActionErr = ""
					st.mentionNoWorkspace = false
					st.mentionFresh = false
					st.toast(contracts.NotifySuccess, st.tr("已打开工作区", "Workspace opened"))
				}
			}
			runtime.Invalidate()
		})
	}()
}

// composerSuggestionRow is the one row skeleton both composer suggestion menus
// share (the slash commands and the @ references): a 32 DIP row with Space2 side
// padding, an optional 16 DIP glyph, the name at 14/500, an optional 12 DIP
// tertiary detail that takes the room left and ellipsizes, and an optional
// trailing 12 DIP meta. Two menus over the same composer with two densities read
// as two products (p2-slash vs p2-mentions audit), so the metrics live here.
type composerSuggestionRowOptions struct {
	Key      string
	Label    string // accessible name
	Title    string // 14/500, the row's own name
	Detail   string // 12/18 tertiary, after the name
	Meta     string // 12/18 tertiary, right-aligned
	Icon     *ui.SVG
	HasIcon  bool
	Selected bool
	Index    int
	SetSize  int
	OnClick  func()
}

func composerSuggestionRow(c *ui.Context, t *design.Tokens, o composerSuggestionRowOptions) ui.Element {
	// The fixed 32 DIP height holds the 22 DIP name line with 5 DIP above and
	// below: the same row the composer's own tool row uses, not a tighter one.
	button := ui.ButtonBase(c.Key(o.Key)).FillWidth().Justify(ui.Start).
		AlignItems(ui.Center).Gap(design.Space2).
		Height(design.RowCompactHeight).Padding(design.Space0, design.Space2).
		Radius(design.RadiusSM).Label(o.Label).
		Role(ui.RoleOption).Checked(o.Selected)
	if o.SetSize > 0 {
		button = button.PositionInSet(o.Index+1, o.SetSize)
	}
	switch {
	case button.Pressed() || o.Selected:
		button = button.Background(t.BgSelected)
	case button.Hovered():
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		if o.HasIcon {
			ui.Icon(c, o.Icon).Size(design.IconSizeS, design.IconSizeS).TextColor(t.LabelSecondary)
		}
		design.Apply(ui.Text(c, o.Title), t.Font(design.TypeS)).
			TextColor(t.LabelPrimary).Shrink(1).MaxLines(1).Ellipsis("…")
		switch {
		case o.Detail != "":
			design.Apply(ui.Text(c, o.Detail), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).Shrink(1).MinWidthPercent(0).
				MaxLines(1).Ellipsis("…")
		case o.Meta != "":
			ui.Spacer(c).Grow(1)
		}
		if o.Meta != "" {
			design.Apply(ui.Text(c, o.Meta), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap().Shrink(0)
		}
	})
	if o.OnClick != nil && button.Clicked() {
		o.OnClick()
	}
	return button
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
		key = "session.mention.file." + textutil.FirstNonEmptyTrimmed(row.file.Path, label)
		if !row.file.Dir {
			detail = formatAttachmentSize(int(row.file.Size))
		}
	default:
		icon = icons.Message()
		label = textutil.FirstNonEmptyTrimmed(row.session.Title, row.session.ID)
		key = "session.mention.session." + textutil.FirstNonEmptyTrimmed(row.session.ID, label)
		detail = sessionReferenceIDPrefix(row.session.ID)
	}
	return composerSuggestionRow(c, t, composerSuggestionRowOptions{
		Key:      key,
		Label:    label,
		Title:    label,
		Meta:     detail,
		Icon:     icon,
		HasIcon:  true,
		Index:    index,
		SetSize:  total,
		Selected: index == sel,
		OnClick: func() {
			st.mentionSel = index
			st.draft = applyMention(st, st.draft, tokenStart, tokenEnd, row)
			st.composerInput.Focus(c)
			runtime.Invalidate()
		},
	})
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
	// A new conversation is a one-shot action: a double click (or a click plus
	// the keyboard chord) must not leave two empty conversations behind, each
	// taking the active slot (A4-I-04). The guard is the state's, so the hero and
	// any other entry in this module share it.
	if st == nil {
		return
	}
	now := time.Now()
	if !st.createAt.IsZero() && now.Sub(st.createAt) < createDebounce {
		return
	}
	st.createAt = now
	// Untitled on purpose: the backend names a conversation from its first
	// prompt, and a pre-filled placeholder ("新对话") would pin that title
	// forever (FR-30).
	id, err := chat.NewSession("")
	if err != nil {
		return
	}
	if err := chat.SetActiveSession(id); err != nil {
		return
	}
	_ = navigator.Push(ScreenID)
}

// --- find in the conversation (Cmd/Ctrl+F) ---------------------------------

// findMatch is one transcript match: the message index in the merged transcript
// and the List row that renders it (assistant steps of one turn share a row).
type findMatch struct {
	message int
	row     int
}

// registerFindShortcut binds Cmd/Ctrl+F to the conversation find bar. The bar
// belongs to the transcript, so the chord switches to the conversation tab first
// when the user is reading another tab; a child conversation has no transcript
// of its own, so the chord does nothing there.
func registerFindShortcut(c *ui.Context, st *state, runtime contracts.UIRuntime) {
	if c == nil || st == nil {
		return
	}
	c.OnShortcut(ui.Cmd, ui.KeyF, func() {
		if strings.TrimSpace(st.agents.childID) != "" {
			return
		}
		if st.tab != tabChat {
			st.tab = tabChat
		}
		st.findOpen = true
		st.findScrollPending = true
		runtime.Invalidate()
	})
}

// buildFindBar draws the official ui.FindBar over the transcript and keeps its
// state: the query is matched against every message's prose and reasoning, the
// matches map to the transcript's List rows, and the current match's row is
// scrolled into view whenever the query or the match moves. The bar owns its
// keyboard (Enter/Shift+Enter/previous/next/Done/Esc); the screen owns Esc as
// well, so closing the bar never stops the running turn.
func buildFindBar(c *ui.Context, st *state, messages []contracts.ChatMessage,
	sameTurn func(prev, next contracts.ChatMessage) bool) {
	if st == nil {
		return
	}
	signature := findSignature(st.findQuery, messages)
	if st.findSig != signature {
		st.findMatches = findMessageMatches(st.findQuery, messages,
			uikit.BuildSessionRowMap(messages, sameTurn))
		st.findSig = signature
		st.clampFindCurrent()
	}
	before := st.findQuery
	bar := ui.FindBar(c, &st.findOpen, &st.findQuery, len(st.findMatches), &st.findCurrent)
	queryChanged := st.findQuery != before
	if queryChanged {
		// The input of this pass typed into the field: recompute now so the
		// scroll (and the next frame's count) use the new query.
		st.findMatches = findMessageMatches(st.findQuery, messages,
			uikit.BuildSessionRowMap(messages, sameTurn))
		st.findSig = findSignature(st.findQuery, messages)
		st.findCurrent = 0
	}
	if !st.findOpen {
		// The bar was closed by Esc or Done in this pass: hand the keyboard back
		// to the composer (the bar's field is gone, and a closed bar must not
		// leave the window without a focus owner — the next Cmd/Ctrl+F would be
		// dropped otherwise).
		st.focusComposer = true
		return
	}
	if (bar.Changed() || queryChanged || st.findScrollPending) && len(st.findMatches) > 0 {
		st.findScrollPending = false
		st.clampFindCurrent()
		if row := st.findMatches[st.findCurrent].row; row >= 0 {
			st.chatList.ScrollIntoView(row)
		}
	}
}

// clampFindCurrent keeps the current match inside the match list.
func (st *state) clampFindCurrent() {
	if len(st.findMatches) == 0 {
		st.findCurrent = 0
		return
	}
	if st.findCurrent < 0 {
		st.findCurrent = 0
	}
	if st.findCurrent >= len(st.findMatches) {
		st.findCurrent = len(st.findMatches) - 1
	}
}

// findSignature identifies what the cached matches were computed from: the query
// and the transcript's message identities and text sizes. A stable frame reuses
// the cache; a streaming reply, a merged receipt or an edited message changes it.
func findSignature(query string, messages []contracts.ChatMessage) string {
	var builder strings.Builder
	builder.WriteString(query)
	for _, message := range messages {
		builder.WriteByte('\x00')
		builder.WriteString(message.ID)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(len(message.Text) + len(message.Reasoning)))
	}
	return builder.String()
}

// findMessageMatches lists the messages whose prose or reasoning contains the
// query, case-insensitively, mapped to their transcript List rows. One message
// counts once even when both its prose and its reasoning match.
func findMessageMatches(query string, messages []contracts.ChatMessage,
	mapping uikit.SessionRowMap) []findMatch {
	if query == "" {
		return nil
	}
	needle := strings.ToLower(query)
	matches := make([]findMatch, 0, 8)
	for index, message := range messages {
		haystack := message.Text
		if message.Reasoning != "" {
			haystack += "\n" + message.Reasoning
		}
		if strings.Contains(strings.ToLower(haystack), needle) {
			matches = append(matches, findMatch{message: index, row: mapping.RowOf(index)})
		}
	}
	return matches
}

// transcriptMessages returns the messages the transcript renders: the durable
// child-completion receipts merged into the persisted history in order.
func transcriptMessages(st *state, chatSession contracts.ChatSession) []contracts.ChatMessage {
	return mergeChildNotices(chatSession.Messages,
		childNoticeMessages(st, chatSession.ID), chatSession.Running)
}

// transcriptSameTurn returns the grouping rule the transcript List uses, or nil
// when the work-steps mode keeps every step its own row (verbose). Find and the
// trajectory jump must map messages to rows with the same rule.
func (st *state) transcriptSameTurn() func(prev, next contracts.ChatMessage) bool {
	if st.workStepsPolicyFor().mode == contracts.WorkStepsVerbose {
		return nil
	}
	return assistantTurnContinues
}

// transcriptRowForMessageID maps one persisted message id to the transcript's
// List row that renders it, or -1 when the message is not in the transcript.
func transcriptRowForMessageID(st *state, chatSession contracts.ChatSession, messageID string) int {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return -1
	}
	messages := transcriptMessages(st, chatSession)
	mapping := uikit.BuildSessionRowMap(messages, st.transcriptSameTurn())
	for index, message := range messages {
		if message.ID == messageID {
			return mapping.RowOf(index)
		}
	}
	return -1
}
