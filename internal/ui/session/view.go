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
	}
	// Frame-time fallback for hosts that update the session state without an
	// extra notification: retry the queue once the run has settled. When this
	// frame already detected the settle, handleRunSettled owns the flush, so
	// posting the fallback too would send two queued prompts in one frame.
	if !settled && len(st.queuedFor(activeID)) > 0 && !chatSession.Running && !st.queuePaused[activeID] {
		runtime.Post(func() {
			processQueuedPrompts(chat, st, activeID)
			runtime.Invalidate()
		})
	}

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		buildHeader(c, t, runtime, cfg, st, chatSession, hasSession)
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
			buildComposer(c, t, runtime, navigator, chat, cfg, st, activeID, chatSession)
		}
		// Command-family dialogs (/goal, /plan, /memory): native Modals above
		// the screen; their buttons close them and request the next frame.
		buildGoalDialog(c, t, runtime, st)
		buildPlanDialog(c, t, runtime, st)
		buildMemoryDialog(c, t, runtime, st)
		buildFeedbackDialog(c, t, runtime, st)
		buildHelpDialog(c, t, runtime, st)
		// The attachment path fallback modal (ZK-09) is not command-family but
		// also lives above the screen, next to the composer it stages into.
		buildAttachmentPathDialog(c, t, runtime, st)
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
	prune(st.usageOpen)
	if st.expandedActions != "" && !keep[st.expandedActions] {
		st.expandedActions = ""
	}
}

// --- header -----------------------------------------------------------------

// buildHeader is the brand header: one title row, at most two actions and the
// native tab strip. The title wins the space it needs (it grows and ellipsizes
// last), the running state is one badge, the goal is one compact chip that
// opens its actions in a popover, and everything else lives in the overflow
// menu. The plan-mode badge belongs to the composer's mode chip, not here.
func buildHeader(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, cfg Config, st *state, chatSession contracts.ChatSession, hasSession bool) {
	th := t.Theme()
	// The N1 layout: when the conversation column is narrow (e.g. the 1024
	// window with the right inspector docked), the title keeps a readable
	// floor and the goal chip drops its text to a status chip.
	compactHeader := st.headerWidth > 0 && st.headerWidth < 560
	ui.Column(c).FillWidth().
		Padding(th.Space(2), th.Space(4), 0, th.Space(4)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		headerRow := ui.Row(c).FillWidth().Height(t.Scale(th.Space(8))).AlignItems(ui.Center).
			Gap(design.Space2)
		headerRow.Children(func() {
			title := st.localized(cfg.Title, "对话", "Chat")
			if hasSession && strings.TrimSpace(chatSession.Title) != "" {
				title = chatSession.Title
			}
			// The title is the one thing that must stay readable at every
			// width: it takes the remaining room, pushes the actions right and
			// ellipsizes instead of pushing them out of the window. On a narrow
			// column its minimum keeps at least a few characters visible.
			titleElement := design.Apply(ui.Text(c, title), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MinWidth(0).
				MaxLines(1).Ellipsis("…").Tooltip(title)
			if compactHeader {
				titleElement.MinWidth(120)
			}
			if hasSession && chatSession.Running {
				design.Apply(ui.Badge(c, st.tr("运行中", "Running")), t.Badge()).TextColor(t.WarningText)
			}
			buildGoalChip(c, t, runtime, st, compactHeader)
			if hasSession {
				buildHeaderMoreMenu(c, st, runtime, cfg, chatSession)
			}
		})
		// Read back for the next frame (Element.Bounds is the last committed
		// rect); the compact decision may lag a resize by one frame.
		st.headerWidth = headerRow.Bounds().W
		if st.exportErr != "" {
			buildErrorLine(c, t, st.tr("导出失败：", "Export failed: ")+st.exportErr)
		}
		buildTabs(c, t, cfg, st)
	})
	buildGoalMenuActions(c, t, runtime, st)
}

// buildHeaderMoreMenu is the header's overflow action: the clipboard and export
// commands of the conversation (the /export command remains the keyboard
// entry). Actions are grouped: clipboard first, then the two export formats.
func buildHeaderMoreMenu(c *ui.Context, st *state, runtime contracts.UIRuntime, cfg Config, chatSession contracts.ChatSession) {
	ui.PopoverMenuButton(c, st.tr("更多", "More"), func(m *ui.Menu) {
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
		// Archiving moved to the settings data page, but the conversation keeps
		// its own action; success is a toast with Undo (restores the flag).
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

// buildGoalChip is the header's single goal control: one compact chip with the
// status label and the goal text truncated at a bounded measure (the full text
// stays in the tooltip). Its actions open in a drawn PopoverMenuButton menu, so
// the chip keeps the native keyboard, focus and screen-reader behavior of every
// other menu button in the product (mygo menu-button.md) instead of a hand-drawn
// popover; the destructive items use MenuItem.Danger. On a narrow header the
// chip keeps only its status label so the title keeps room.
func buildGoalChip(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, compact bool) {
	if st.goal == nil || strings.TrimSpace(st.goal.Text) == "" {
		return
	}
	th := t.Theme()
	label, color, _, _ := goalStatus(st, t)
	// The accessible name carries the full goal text in both header widths; the
	// wide header also shows it as a child, while the compact header keeps only
	// the status label visible (N1).
	chip := ui.Button(c, "").
		Label(label + ": " + st.goal.Text).
		Tooltip(st.goal.Text).
		Height(t.Scale(th.Space(7))).PaddingX(design.Space2).
		Radius(design.RadiusSM).Shrink(0).Gap(design.Space1)
	chip.Children(func() {
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).FontWeight(600).
			TextColor(color).NoWrap()
		if !compact {
			design.Apply(ui.Text(c, st.goal.Text), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…").MaxWidth(240)
		}
	})
	chip.PopoverMenuButton(func(m *ui.Menu) { buildGoalMenu(m, t, runtime, st) })
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
		if feedback := normalizeSessionFeedback(message.Feedback); feedback != "" {
			label := "有帮助"
			if feedback == contracts.FeedbackBad {
				label = "没帮助"
			}
			if english {
				label = "Helpful"
				if feedback == contracts.FeedbackBad {
					label = "Not helpful"
				}
				fmt.Fprintf(&builder, "> User feedback: %s\n", label)
			} else {
				fmt.Fprintf(&builder, "> 用户反馈：%s\n", label)
			}
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
	tabs.List.Height(t.Scale(th.Space(9))).Margin(design.Space2, 0, 0, 0).
		Padding(0, 0, 0, th.Space(1)).Gap(design.Space4).
		AlignItems(ui.Stretch).Children(func() {
		for index, label := range labels {
			index, label := index, label
			active := st.tab == index
			tab := tabs.Tab(index).Height(t.Scale(th.Space(9))).PaddingX(design.Space1).
				Gap(0).Label(label)
			tab.Children(func() {
				ui.Column(c).Height(t.Scale(th.Space(9))).Justify(ui.SpaceBetween).Children(func() {
					color, weight := t.LabelSecondary, 500
					if active {
						color, weight = t.LabelPrimary, 600
					}
					design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
						FontWeight(weight).NoWrap().TextColor(color)
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

// backToLatestList shows the floating "back to latest" action over the
// transcript List while the user reads earlier rows; clicking it scrolls the
// official List to its end and follows again (list.md ScrollToEnd/FollowEnd).
func backToLatestList(c *ui.Context, t *design.Tokens, list *ui.ListState,
	follow *bool, label string) {
	if *follow {
		return
	}
	ui.Row(c).Absolute().Right(design.Space4).Bottom(design.Space4).Children(func() {
		button := design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton()).Label(label)
		button.Children(func() {
			ui.Icon(c, icons.ChevronDown()).Size(design.Space3, design.Space3).TextColor(t.OnPrimary)
		})
		if button.Clicked() {
			list.ScrollToEnd()
			*follow = true
			c.Invalidate()
		}
	})
}

// transcriptRowLabel names one transcript row for assistive technology and the
// List's type-ahead. It deliberately does not repeat the message's prose: the
// row's rendered content is the message (its Markdown source must not leak into
// the accessibility tree), so the name is the author role plus the row ordinal.
func transcriptRowLabel(st *state, message contracts.ChatMessage, index int) string {
	role := st.tr("消息", "Message")
	switch message.Role {
	case contracts.ChatRoleUser:
		role = st.tr("用户", "User")
	case contracts.ChatRoleAssistant:
		role = st.tr("助手", "Assistant")
	case contracts.ChatRoleTool:
		role = st.tr("工具", "Tool")
	case contracts.ChatRoleSystem:
		role = st.tr("系统", "System")
	}
	return fmt.Sprintf("%s %d", role, index+1)
}

// buildChatPane is the conversation transcript: the official List of messages
// on one centered reading column. The List virtualizes rows (only the visible
// ones build), keys them by message ID (a streaming reply grows its row in
// place and the user's place survives new content) and FollowEnd keeps the end
// in view while streaming; a user who scrolls up gets the floating back-to-
// latest action. Opening a fold in the last message calls HoldPlace, so the row
// does not jump under the pointer (mygo list.md).
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
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		st.chatList.Key = func(index int) any {
			if index < 0 || index >= len(messages) {
				return nil
			}
			return messages[index].ID
		}
		st.chatList.Label = func(index int) string {
			if index < 0 || index >= len(messages) {
				return ""
			}
			return transcriptRowLabel(st, messages[index], index)
		}
		// Each layout follows the end while the user stays there; scrolling away
		// stops the follow inside the List, and ScrollToEnd resumes it.
		st.chatList.FollowEnd = true
		if st.chatHoldPlace {
			st.chatList.HoldPlace()
			st.chatHoldPlace = false
		}
		list := ui.List(c, &st.chatList, len(messages)).
			Label(st.tr("对话消息", "Conversation messages")).
			Gap(design.Space5).
			PaddingY(design.Space5).
			FillWidth().Grow(1)
		if !hasSession || len(messages) == 0 {
			// List.Children shows while the list has no rows: the branded
			// first-run hero is that empty state (list.md "What else you build").
			list.Children(func() {
				ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
					design.Apply(ui.Column(c), t.Gap(design.Space5)).
						Width(design.TranscriptWidth).MaxWidthPercent(92).
						Children(func() {
							buildHero(c, t, cfg, navigator, chat, st)
						})
				})
			})
		}
		list.Rows(func(row ui.ListRow) {
			message := messages[row.Index]
			ui.Row(row.Context).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
				design.Apply(ui.Column(row.Context), t.Gap(design.Space5)).
					Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
					buildMessage(row.Context, t, runtime, message, st)
					if row.Index == len(messages)-1 && message.Role == contracts.ChatRoleAssistant && !chatSession.Running {
						buildRegenerateAction(row.Context, t, runtime, chat, st, chatSession, message)
					}
				})
			})
		})
		// AtEnd reads the last laid-out frame; wait for one layout so the
		// floating action never flashes on the first frame.
		if st.chatListSeen {
			st.chatFollowEnd = st.chatList.AtEnd()
		} else {
			st.chatListSeen = true
		}
		if hasSession && sessionErrorVisible(messages, chatSession.Error) {
			buildErrorLine(c, t, chatSession.Error)
		}
		backToLatestList(c, t, &st.chatList, &st.chatFollowEnd,
			st.tr("回到最新", "Back to latest"))
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
func buildMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
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
		buildAssistantMessage(c, t, runtime, message, st)
	}
}

// buildAssistantMessage renders the assistant turn: collapsible reasoning, the
// streaming body, the hover-only action row (before the tool cards, so it
// reads as the message's actions, not the last card's), errors, native tool
// cards and the muted, expandable usage footer.
// renderAssistantBody draws one assistant reply in event order. Tool calls
// carry the assistant text length when they arrived (ToolCall.TextOffset), so
// the transcript reads: text before the call, the call's card, then the text
// that followed it. Restored/legacy messages have no offsets (all zero) and
// keep the old whole-text-then-cards fallback; tool-only replies are unchanged.
func renderAssistantBody(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, message contracts.ChatMessage) {
	renderText := func(text string) {
		if text == "" {
			return
		}
		// Assistant prose is rendered by mygo's official Markdown view: the
		// streaming turn heals half-written syntax and the finished turn reuses
		// the same node position (and parse state) as a full view.
		if message.Streaming {
			markdown.Stream(c, text, st.markdownOptions(t))
		} else {
			markdown.View(c, text, st.markdownOptions(t))
		}
	}
	renderTool := func(tool contracts.ToolCall) {
		buildAssistantToolRow(c, t, runtime, st, message.ID, tool)
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
	legacy := true
	for _, tool := range message.Tools {
		if tool.TextOffset > 0 {
			legacy = false
			break
		}
	}
	if legacy {
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
// wins, decision tools keep the prominent card, and every other call uses the
// shared compact disclosure row (a muted line instead of a panel) with the
// friendly localized tool name; the protocol id stays in the tooltip.
func buildAssistantToolRow(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, messageID string, tool contracts.ToolCall) {
	call := contracts.ToolCall{ID: tool.ID, Name: tool.Name, Status: tool.Status, Summary: tool.Summary}
	key := "session.tool." + messageID + "." + tool.ID
	if runtime.RenderTool(c, call) {
		return
	}
	if prominentTool(tool.Name) {
		buildToolCard(c, t, st, call, key, 0)
		return
	}
	summary := strings.TrimSpace(tool.Summary)
	open, seen := st.toolOpen[key]
	if !seen {
		// H15: a failure or a very short result is readable without a click;
		// long successful results stay folded.
		open = toolcards.SummaryDefaultOpen(tool.Status, summary)
	}
	_, statusColor := toolStatusColors(t, tool.Status)
	toggled := false
	row := uikit.ToolRowOptions{
		Key:         key,
		Icon:        uikit.ToolIcon(tool.Name),
		Name:        uikit.ToolDisplayName(st.locale, tool.Name),
		RawName:     tool.Name,
		Status:      statusLabel(tool.Status, st),
		StatusColor: statusColor,
		Running:     normalizeStatus(tool.Status) == "running",
		Preview:     uikit.ToolPreview(summary, 96),
		Open:        open,
		Toggle: func(next bool) {
			toggled = true
			st.toolOpen[key] = next
			// The row grows/shrinks: keep the transcript where it is for this
			// layout so the clicked line does not jump (list.md HoldPlace).
			st.chatHoldPlace = true
			runtime.Invalidate()
		},
	}
	if summary != "" {
		row.Detail = func(cc *ui.Context) {
			design.Apply(ui.Text(cc, truncateTrajectorySummary(summary)),
				t.TextMuted(design.TypeXXS))
		}
	}
	uikit.ToolRow(c, row)
	if !seen && !toggled {
		// Remember the H15 default as the reader's state only when they did not
		// just toggle it in this frame.
		st.toolOpen[key] = open
	}
}

func buildAssistantMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
	outer := ui.Column(c.Key("session.message." + message.ID)).FillWidth().Gap(design.Space2)

	outer.Children(func() {
		// The thinking is one official disclosure per reply: open while the
		// model thinks (its words stream in Markdown), a quiet folded row once
		// the turn ends. The generating indicator below the reply is the
		// official spinner, not a badge plus a decorative glyph.
		buildReasoningDisclosure(c, t, st, message)
		renderAssistantBody(c, t, runtime, st, message)
		if message.Streaming {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Spinner(c).Label(st.tr("生成中", "Generating"))
				design.Apply(ui.Text(c, st.tr("生成中", "Generating")),
					t.TextMuted(design.TypeXS))
			})
		}
		if message.Error != "" {
			buildMessageError(c, t, st, message)
		}
		// H15: the action row closes the message block (prose → cards → actions)
		// and is right-aligned so it reads as belonging to this reply. Copy is
		// resident (one click, no pin); the ⋯ entry unfolds the rating actions.
		buildMessageActionButtons(c, t, runtime, message, st)
		buildUsageFooter(c, t, st, message)
	})
}

// buildReasoningDisclosure draws the reply's thinking as one official mygo
// disclosure (ui.CollapsibleBase): the trigger is a spinner and 思考中… while
// the model is thinking, then a quiet reasoning glyph, 思考 and the reasoning
// line count; the chevron comes from the disclosure's Progress, so it turns as
// the panel opens. The panel is the reasoning Markdown rendered by the official
// Markdown view in a muted, slightly smaller theme than the reply. Open by
// default while streaming (the thinking stays visible) and collapsed once the
// turn ends; a manual toggle per message is kept in st.thinkOpen. No duplicate
// raw-text preview row: the panel is the one place the reasoning text shows.
func buildReasoningDisclosure(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	if message.Reasoning == "" {
		return
	}
	open, chosen := st.thinkOpen[message.ID]
	if !chosen {
		open = message.Streaming
	}
	label := st.tr("思考", "Reasoning")
	if message.Streaming {
		label = st.tr("思考中…", "Thinking…")
	}
	key := "session.reasoning." + message.ID
	parts := ui.CollapsibleBase(c.Key(key), &open)
	// The trigger names itself for assistive technology: 思考中… while the model
	// thinks, 思考 once it finished (the line-count hint stays visual only).
	trigger := parts.Trigger.FillWidth().AlignItems(ui.Center).
		Gap(design.Space2).PaddingY(design.Space1).Radius(design.RadiusSM).
		Label(label)
	if parts.Trigger.Hovered() || parts.Trigger.Focused() {
		trigger.Background(t.BgHover)
	}
	trigger.Children(func() {
		if message.Streaming {
			ui.Spinner(c).Label(st.tr("思考中", "Thinking")).
				Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
		} else {
			ui.Icon(c, icons.Sparkles()).Size(design.Space3, design.Space3).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
			if lines := reasoningLineCount(message.Reasoning); lines > 1 {
				design.Apply(ui.Text(c, fmt.Sprintf(st.tr("%d 行", "%d lines"), lines)),
					t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
			}
		}
		ui.Spacer(c).Grow(1)
		ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary).Rotate(90 * parts.Progress())
	})
	parts.Panel(func() {
		// The thinking body sits under the trigger's text (past the glyph), as a
		// nested block; the muted Markdown theme keeps it subordinate to prose.
		design.Apply(ui.Column(c), t.Gap(design.Space1)).
			PaddingStart(design.Space3 + design.Space2).PaddingEnd(design.Space2).
			PaddingY(design.Space1).Children(func() {
			if message.Streaming {
				markdown.Stream(c, message.Reasoning, st.reasoningMarkdownOptions(t))
				return
			}
			markdown.View(c, message.Reasoning, st.reasoningMarkdownOptions(t))
		})
	})
	if parts.Trigger.Changed() {
		st.thinkOpen[message.ID] = open
		// The row grew or shrank: keep the transcript where it is for this
		// layout so the toggled block does not jump (list.md HoldPlace).
		st.chatHoldPlace = true
	}
}

// buildMessageActionButtons closes one assistant turn with its action row,
// right-aligned: the copy action is resident (H15 — no hover/pin needed for the
// most common action), and the ⋯ entry unfolds the rating actions when the
// backend rates messages. All heights are fixed so no action ever reflows the
// message.
func buildMessageActionButtons(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
	th := t.Theme()
	chat, feedbacker, hasText, showFeedback := messageActionTarget(st, message)
	if !hasText && !showFeedback {
		return
	}
	expanded := showFeedback && st.expandedActions == message.ID
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space1).
		Height(t.Scale(th.Space(6))).Children(func() {
		ui.Spacer(c).Grow(1)
		if expanded {
			// The two ratings are the official Toggle: pressed semantics for
			// assistive technology and Space/Enter, mutually exclusive (the
			// chosen value is carried across both toggles), and pressing the
			// pressed one again clears it. The bound bools are derived from the
			// read model and written back after the framework's input, as
			// mygo-maintenance asks.
			feedback := message.Feedback
			goodOn := feedback == contracts.FeedbackGood
			good := feedbackToggle(c, t, "session.good."+message.ID, st.tr("有帮助", "Helpful"),
				st.tr("这条回复有帮助", "This answer was helpful"), icons.Check(),
				&goodOn, t.Accent)
			if good.Changed() {
				if goodOn {
					feedback = contracts.FeedbackGood
				} else if feedback == contracts.FeedbackGood {
					feedback = ""
				}
			}
			badOn := feedback == contracts.FeedbackBad
			bad := feedbackToggle(c, t, "session.bad."+message.ID, st.tr("没帮助", "Not helpful"),
				st.tr("这条回复没有帮助", "This answer was not helpful"), icons.Close(),
				&badOn, t.DangerText)
			if bad.Changed() {
				if badOn {
					feedback = contracts.FeedbackBad
				} else if feedback == contracts.FeedbackBad {
					feedback = ""
				}
			}
			if feedback != message.Feedback {
				st.toggleMessageFeedback(chat, feedbacker, runtime, message, feedback)
			}
			if st.feedbackErr != "" {
				buildErrorLine(c, t, st.feedbackErr)
			}
		}
		if hasText {
			copied := st.copiedMessage == message.ID
			icon, color := icons.Copy(), t.LabelTertiary
			tooltip := st.tr("复制消息", "Copy message")
			if copied {
				icon, color = icons.Check(), t.SuccessText
				tooltip = st.tr("已复制", "Copied")
			}
			copyButton := actionIconButton(c, t, "session.copy."+message.ID, st.tr("复制消息", "Copy message"), tooltip, icon, color)
			if copyButton.Clicked() {
				c.WriteClipboard(message.Text)
				st.copiedMessage = message.ID
				runtime.Invalidate()
			}
		}
		if showFeedback {
			button := ui.Button(c.Key("session.menu."+message.ID), "").
				Label(st.tr("消息操作", "Message actions")).
				Tooltip(st.tr("消息操作", "Message actions")).
				Size(th.Space(6), th.Space(6)).PaddingX(0).Radius(design.RadiusSM)
			if expanded {
				button.Background(t.BgSelected)
			}
			button.Children(func() {
				ui.Icon(c, icons.More()).Size(design.Space3, design.Space3).
					TextColor(t.LabelSecondary)
			})
			if button.Clicked() {
				if expanded {
					st.expandedActions = ""
				} else {
					st.expandedActions = message.ID
				}
				st.chatHoldPlace = true
				runtime.Invalidate()
			}
		}
	})
}

// actionIconButton is one compact icon action of the message action row.
func actionIconButton(c *ui.Context, t *design.Tokens, key, label, tooltip string,
	icon *ui.SVG, color ui.Color) ui.Element {
	th := t.Theme()
	button := ui.Button(c.Key(key), "").Label(label).Tooltip(tooltip).
		Size(th.Space(6), th.Space(6)).PaddingX(0).Radius(design.RadiusSM)
	button.Children(func() {
		ui.Icon(c, icon).Size(design.Space3, design.Space3).TextColor(color)
	})
	return button
}

// feedbackToggle is one message-rating control: the official Toggle with an
// icon and no visible label, sized like the action row's icon buttons. Its
// pressed state is the official one (tint plus AT pressed state), not a
// hand-painted background; the caller writes the derived bool back.
func feedbackToggle(c *ui.Context, t *design.Tokens, key, label, tooltip string,
	icon *ui.SVG, on *bool, pressedColor ui.Color) ui.Element {
	th := t.Theme()
	toggle := ui.Toggle(c.Key(key), on, "").Label(label).Tooltip(tooltip).
		Size(th.Space(6), th.Space(6)).PaddingX(0).Radius(design.RadiusSM)
	color := t.LabelTertiary
	if *on {
		color = pressedColor
	}
	toggle.Children(func() {
		ui.Icon(c, icon).Size(design.Space3, design.Space3).TextColor(color)
	})
	return toggle
}

// messageActionTarget resolves the action affordances of one assistant turn:
// the optional rating half of the chat backend and whether the turn has text to
// copy.
func messageActionTarget(st *state, message contracts.ChatMessage) (chat contracts.ChatService,
	feedbacker contracts.SessionFeedbacker, hasText, showFeedback bool) {
	hasText = strings.TrimSpace(message.Text) != ""
	chat, _ = st.ctx.Get(contracts.KeyChat)
	feedbacker, canRate := chat.(contracts.SessionFeedbacker)
	sessionID := ""
	if chat != nil {
		sessionID = chat.ActiveSession()
	}
	showFeedback = canRate && chat != nil && sessionID != "" && message.ID != "" && !st.feedbackOpen
	return chat, feedbacker, hasText, showFeedback
}

// buildUsageFooter is the muted usage line of an assistant turn: the summary at
// 11px, expandable to the token breakdown. It is informational, never a
// competing button row. Token counts use the shared compact units ("173.6K",
// never "173,635"); the expanded detail keeps each exact count in a tooltip.
func buildUsageFooter(c *ui.Context, t *design.Tokens, st *state, message contracts.ChatMessage) {
	if message.Usage == nil {
		return
	}
	summary := st.tr("用时 ", "Took ") + formatUsageDuration(message.Usage.DurationMS) +
		" · " + uikit.FormatCompactCount(usageTotalTokens(message.Usage)) + " tokens"
	previous := st.usageOpen[message.ID]
	open := previous
	collapsible := ui.Collapsible(c.Key("session.usage."+message.ID), summary, &open, func() {
		type usageRow struct{ label, value, exact string }
		exact := func(count int) string { return strconv.Itoa(count) }
		rows := []usageRow{
			{st.tr("时长", "Duration"), formatUsageDuration(message.Usage.DurationMS), ""},
			{st.tr("输入 tokens", "Prompt tokens"), uikit.FormatCompactCount(message.Usage.PromptTokens), exact(message.Usage.PromptTokens)},
			{st.tr("输出 tokens", "Completion tokens"), uikit.FormatCompactCount(message.Usage.CompletionTokens), exact(message.Usage.CompletionTokens)},
		}
		if message.Usage.CachedTokens > 0 {
			rows = append(rows, usageRow{st.tr("缓存 tokens", "Cached tokens"), uikit.FormatCompactCount(message.Usage.CachedTokens), exact(message.Usage.CachedTokens)})
		}
		if suffix := st.usageContextWindowSuffix(); suffix != "" {
			rows = append(rows, usageRow{st.tr("上下文预算", "Context budget"), strings.TrimPrefix(suffix, " · "), ""})
		}
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			for _, row := range rows {
				row := row
				ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					design.Apply(ui.Text(c, row.label), t.TextMuted(design.TypeXXS)).Width(120).Shrink(0)
					value := design.Apply(ui.Text(c, row.value), t.Font(design.TypeXXS)).TextColor(t.LabelSecondary)
					if row.exact != "" {
						// The compact display keeps the row readable; the exact
						// count is one hover away for anyone reconciling numbers.
						value.Tooltip(st.tr("精确值：", "Exact: ") + row.exact)
					}
				})
			}
		})
	})
	design.Apply(collapsible, t.Font(design.TypeXXXS))
	st.usageOpen[message.ID] = open
	if previous != open {
		st.chatHoldPlace = true
	}
}

// toggleMessageFeedback writes/toggles one rating through the optional
// SessionFeedbacker. Clicking the current value clears it.
func (st *state) toggleMessageFeedback(chat contracts.ChatService, feedbacker contracts.SessionFeedbacker,
	runtime contracts.UIRuntime, message contracts.ChatMessage, value string) {
	sessionID := chat.ActiveSession()
	if sessionID == "" {
		return
	}
	next := value
	if message.Feedback == value {
		next = ""
	}
	if err := feedbacker.SetMessageFeedback(sessionID, message.ID, next); err != nil {
		st.feedbackErr = st.tr("反馈保存失败：", "Failed to save feedback: ") + err.Error()
	} else {
		st.feedbackErr = ""
	}
	runtime.Invalidate()
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

// toolStatus is the one status presentation of a tool card: a badge tinted
// with the status color and its WCAG-AA text variant.
func toolStatusBadge(c *ui.Context, t *design.Tokens, st *state, status string) {
	label := statusLabel(status, st)
	color, text := toolStatusColors(t, status)
	design.Apply(ui.Badge(c, label), t.Badge(), func(el ui.Element) ui.Element {
		return el.Background(color.Alpha(statusTintAlpha(status))).TextColor(text)
	})
}

// statusTintAlpha is the badge fill opacity of a status. The success tint is
// deliberately lighter (0.08) than the warning/danger tints (0.12): SuccessText
// on an 8% Success tint reaches WCAG-AA for small text, while 12% falls just
// short.
func statusTintAlpha(status string) float32 {
	if normalizeStatus(status) == "ok" {
		return 0.08
	}
	return 0.12
}

// toolStatusColors maps a tool status to the tint/fill color and its text
// variant (the one anatomy every tool card shares, in this package and in
// internal/ui/toolcards).
func toolStatusColors(t *design.Tokens, status string) (tint, text ui.Color) {
	switch normalizeStatus(status) {
	case "running":
		return t.Warning, t.WarningText
	case "ok":
		return t.Success, t.SuccessText
	case "error":
		return t.Danger, t.DangerText
	default:
		return t.LabelTertiary, t.LabelTertiary
	}
}

// buildToolCard is the one decision-tool card anatomy: icon, friendly tool
// name (the protocol id stays in its tooltip), status, optional step metadata
// and the official collapsible summary. Decision tools (plan review, exit plan,
// ask user) keep this full card in the transcript and the trajectory timeline
// shows every call this way; background calls use the compact disclosure row in
// uikit.ToolRow. A running call shows the official spinner with its WCAG-AA
// status color instead of the status badge.
func buildToolCard(c *ui.Context, t *design.Tokens, st *state, call contracts.ToolCall, key string, step int) {
	friendly := uikit.ToolDisplayName(st.locale, call.Name)
	running := normalizeStatus(call.Status) == "running"
	card := design.Apply(ui.Column(c.Key(key)), t.Panel()).FillWidth().
		Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, uikit.ToolIcon(call.Name)).Size(design.Space4, design.Space4).
				TextColor(t.LabelSecondary)
			name := design.Apply(ui.Text(c, friendly), t.Font(design.TypeS)).FontWeight(600).
				TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			if friendly != call.Name {
				name.Tooltip(call.Name)
			}
			if running {
				status := statusLabel(call.Status, st)
				_, textColor := toolStatusColors(t, call.Status)
				ui.Spinner(c).Label(status).Size(design.Space3, design.Space3).
					TextColor(textColor)
				design.Apply(ui.Text(c, status), t.Font(design.TypeXXXS)).
					TextColor(textColor).NoWrap()
			} else {
				toolStatusBadge(c, t, st, call.Status)
			}
			ui.Spacer(c).Grow(1)
			if step > 0 {
				design.Apply(ui.Text(c, fmt.Sprintf(st.tr("步骤 %d", "Step %d"), step)),
					t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
			}
		})
		if summary := strings.TrimSpace(call.Summary); summary != "" {
			open, seen := st.toolOpen[key]
			if !seen {
				// H15: a failure or a very short result is readable without a
				// click; long successful results stay folded.
				open = toolcards.SummaryDefaultOpen(call.Status, summary)
			}
			ui.Collapsible(c.Key(key+".summary"), st.tr("详情", "Details"), &open, func() {
				design.Apply(ui.Text(c, truncateTrajectorySummary(summary)), t.TextMuted(design.TypeXS))
			})
			st.toolOpen[key] = open
		}
	})
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
							buildToolCard(row.Context, t, st, item.tool, "session.trajectory."+item.key, row.Index+1)
						})
					})
				})
		}
	})
}

// buildRegenerateAction is the native secondary action under the last assistant
// turn: it retries the last user message through contracts.SessionRegenerator.
// It is only built when the backend supports regeneration and the turn is not
// streaming; an empty or failed reply leaves the action disabled.
func buildRegenerateAction(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	chat contracts.ChatService, st *state, chatSession contracts.ChatSession,
	message contracts.ChatMessage) {
	regenerator, ok := chat.(contracts.SessionRegenerator)
	if !ok {
		return
	}
	label := st.tr("重新生成", "Regenerate")
	hasContent := strings.TrimSpace(message.Text) != "" || len(message.Tools) > 0
	disabled := !hasContent || message.Error != ""
	// A compact secondary action under the message, not a full-width bar: the
	// icon plus the label is enough for a retry control.
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
				Label(label).Tooltip(label).Disabled(disabled)
			button.Children(func() {
				color := t.LabelSecondary
				if disabled {
					color = t.LabelTertiary
				}
				ui.Icon(c, icons.Reload()).Size(design.Space3, design.Space3).TextColor(color)
			})
			if button.Clicked() && !disabled {
				if err := regenerator.Regenerate(chatSession.ID); err != nil {
					st.regenerateErr = st.tr("重新生成失败：", "Regenerate failed: ") + err.Error()
				} else {
					st.regenerateErr = ""
				}
				runtime.Invalidate()
			}
		})
		if st.regenerateErr != "" {
			buildErrorLine(c, t, st.regenerateErr)
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
// configuration of the thinking panel: the reply's body size minus one DIP, in
// the secondary text color (strong text stays primary so a bolded conclusion
// reads), with headings kept subordinate to the reply's own headings. It
// follows the light/dark theme and the ui.chatFontSize setting like the prose.
func (st *state) reasoningMarkdownOptions(t *design.Tokens) *markdown.Options {
	size := st.messageFontSize() - 1
	if size < 11 {
		size = 11
	}
	if st.mdReasoning.options != nil && st.mdReasoning.dark == t.Dark && st.mdReasoning.size == size {
		return st.mdReasoning.options
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = size
	theme.Text = t.LabelSecondary
	theme.Muted = t.LabelTertiary
	theme.Strong = t.LabelPrimary
	theme.Code = t.LabelSecondary
	theme.QuoteText = t.LabelTertiary
	theme.BlockSpacing = 0.6
	// The panel's headings must never outrank the reply's: keep them small
	// multiples of the thinking size instead of the Markdown defaults, so even
	// at the large chat font size the panel's H1 stays below the reply's 17 DIP.
	sizes := [6]float32{1.1, 1.05, 1.0, 0.95, 0.9, 0.85}
	for index := range theme.Headings {
		theme.Headings[index].Size = sizes[index]
		theme.Headings[index].Weight = 600
		theme.Headings[index].Color = t.LabelSecondary
	}
	theme.Headings[5].Color = t.LabelTertiary
	st.mdReasoning.options = &markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
	}
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

// buildMemoryPane is the Memory tab's real panel: the workspace memory store's
// entries (or search results), divider rows with a row-end delete icon, and the
// add row. The read model is cached in state and refreshed only when the query
// changes or a mutation asks for it — the frame builder never reads the store
// while nothing changed. Without a memory backend (contracts.KeyMemory is not
// provided) it shows the disabled empty state instead of failing.
func buildMemoryPane(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	store, hasStore := st.ctx.Get(contracts.KeyMemory)
	query := strings.TrimSpace(st.memoryQuery)
	if hasStore && (!st.memoriesFresh || st.memoryRefresh || st.memoriesQuery != query) {
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
			buildMemoryTools(c, t, runtime, store, st, len(memories))
			if listErr != nil {
				uikit.ErrorLine(c, st.tr("读取记忆失败：", "Failed to read memory: ")+listErr.Error())
			}
		})
		switch {
		case listErr != nil:
			// The error row above is the whole content.
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
// labeled input and the primary add action, no card wrapper.
func buildMemoryAdd(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, store contracts.MemoryStore, st *state) {
	draft := strings.TrimSpace(st.memoryDraft)
	ui.Column(c.Key("session.memory.add")).FillWidth().Gap(design.Space2).
		PaddingY(design.Space3).Children(func() {
		ui.Divider(c)
		ui.TextInput(c, &st.memoryDraft).Label(st.tr("新建记忆", "New memory")).
			Placeholder(st.tr("输入要记住的内容", "Enter something to remember")).FillWidth()
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Spacer(c).Grow(1)
			add := design.Apply(ui.PrimaryButton(c, st.tr("添加记忆", "Add memory")), t.PrimaryButton()).
				Disabled(draft == "")
			if add.Clicked() && draft != "" {
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
		})
		if st.memoryError != "" {
			uikit.ErrorLine(c, st.memoryError)
		}
	})
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

// buildComposer is the brand composer card: the borderless native multiline
// input, a quiet action row of native PopoverMenuButton controls (attach, model,
// session settings) and the send/stop control pinned to the right, so a narrow
// window never hides the primary action. The action row is deliberately a plain
// Row with three menu buttons rather than ui.Toolbar: the toolbar's overflow
// menu would take the send control with it, and the row is already bounded to
// four controls (the audit's H7). The Enter/Shift+Enter hint moved out of the
// layout (placeholder and the send button's tooltip); the suggestion menus are
// keyboard-driven and never focus the input every frame.
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
	th := t.Theme()
	running := chatSession.Running
	queueAllowed := running && sendWhileRunningEnabled(st.settings)
	// The composer never disappears: even while a turn runs the TextArea stays
	// visible and editable, so the user can keep composing. The send position
	// shows 停止 plus 排队 when queueing is on; with queueing explicitly off,
	// Enter is a newline (not a send) until the reply ends and the draft is kept.
	var input ui.Element

	ui.Column(c.Key("session.composer")).FillWidth().PaddingY(th.Space(2)).AlignItems(ui.Center).Children(func() {
		workspace, _ := st.ctx.Get(contracts.KeyWorkspace)
		var composerCard ui.Element
		ui.Row(c.Key("session.composer.row")).FillWidth().Justify(ui.Center).Children(func() {
			card := design.Apply(ui.Column(c.Key("session.composer.card")), t.Composer()).
				Width(design.TranscriptWidth).MaxWidthPercent(92).
				Padding(th.Space(3)).Gap(design.Space2).
				Label(st.tr("会话输入框", "Session composer"))
			// The low-frequency chips fold into the 会话设置 group when the
			// composer is narrow, so the toolbar never has to overflow its
			// unlabeled » button (H7). The width comes from the last laid-out
			// frame.
			composerCard = card
			composerWidth := card.Bounds().W
			// 1024-wide windows give the card ~640 DIP, 900 gives ~526: everything
			// below 800 folds the mode/persona chips into the group, which keeps the
			// toolbar within its row and avoids the unlabeled » overflow.
			compactControls := composerWidth > 0 && composerWidth < 800
			veryCompact := composerWidth > 0 && composerWidth < 480
			card.Children(func() {
				buildPendingAttachments(c, t, runtime, st)
				placeholder := st.localized(cfg.ComposerPlaceholder, "输入消息…", "Ask ZeoKeel…")
				input = ui.TextArea(c.Key("session.composer.input"), &st.draft).
					Label(placeholder).
					Placeholder(placeholder).
					Background(ui.Transparent).Border(0, ui.Transparent).
					Padding(0, th.Space(1)).
					MinHeight(th.Space(9)).Grow(1).
					Bind(&st.composerInput).
					OnTextKey(composerTextKey(runtime, chat, st, activeID, running, queueAllowed))
				if st.focusComposer {
					// One-shot: opening/switching a conversation or a finished run
					// asks for the keyboard here. The @/// suggestion menus are
					// driven by this same input, so focusing it never fights them.
					st.focusComposer = false
					st.composerInput.Focus(c)
				}
				if running && !queueAllowed {
					// Queueing is off: the composer is editable but Enter does
					// not send. Say where the text goes instead of leaving the
					// user wondering why Enter inserted a line break.
					design.Apply(ui.Text(c, st.tr("回复生成中，Enter 暂不可用；草稿会保留，本轮结束后可发送",
						"Generating: Enter is unavailable; your draft is kept for when this turn ends")),
						t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(1)
				}
				queued := st.queuedFor(activeID)
				queueErr := st.queueErrorFor(activeID)
				if len(queued) > 0 || queueErr != "" {
					var queueAnchor ui.Element
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						if len(queued) > 0 {
							queueAnchor = buildQueueBadge(c, t, runtime, st, activeID)
						}
						if queueErr != "" {
							buildErrorLine(c, t, queueErr)
						}
					})
					if queueAnchor.Valid() {
						buildQueuePopover(c, t, runtime, chat, st, activeID, queueAnchor, running)
					}
				}
				if running && queueAllowed && len(st.pendingAttachments) > 0 {
					// The queue carries text only: say so instead of silently
					// leaving the staged files behind.
					design.Apply(ui.Text(c, st.tr("本轮只发送文字；附件会保留在输入框，随下一条消息发送",
						"Queue sends text only; attachments stay in the composer for your next message")),
						t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(1)
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					buildComposerToolbar(c, t, runtime, navigator, chat, st, running, compactControls, veryCompact)
					ui.Spacer(c).Grow(1)
					if running {
						if queueAllowed {
							buildQueueSendButton(c, t, st, activeID)
						}
						buildStopButton(c, t, cfg, chat, st, activeID)
					} else {
						buildSendButton(c, t, cfg, chat, st, activeID)
					}
				})
			})
			// The composer's focus/hover ring also applies while a reply runs:
			// the input is editable then too, so the ring must not disappear.
			switch {
			case input.Focused():
				card.Border(1, t.Accent)
			case input.Hovered():
				card.Border(1, t.BorderL3)
			}
		})
		// The DSH-style usage strip sits directly under the composer card: one
		// muted line of session statistics (rounds, steps, speed, tokens,
		// cache/context) with the exact numbers in its tooltip.
		buildSessionStatusStrip(c, t, chat, st, chatSession)
		// An empty draft forgets an earlier Esc dismissal: typing the same "/"
		// or "@..." again after clearing the input must reopen the menu instead
		// of silently staying dismissed for the rest of the session.
		if strings.TrimSpace(st.draft) == "" {
			st.slashDismissed = ""
			st.mentionDismissed = ""
		}
		// The suggestion menus are anchored above the composer (H8): they are
		// an overlay, so they never push the transcript around. The keyboard
		// handling stays in the composer's OnTextKey (Up/Down/Enter/Tab/Esc).
		mentionActive := false
		var suggestionRow ui.Element
		if query, tokenStart, active := mentionQuery(st.draft); active && st.mentionDismissed != st.draft {
			mentionActive = true
			if composerCard.Valid() {
				suggestionRow = buildMentionPopover(c, t, runtime, workspace, chat, st, query, tokenStart, composerCard)
			}
		}
		if !mentionActive {
			// The next @ opens on a fresh workspace listing.
			st.mentionFresh = false
		}
		if query, active := slashQuery(st.draft); active && !running && st.slashDismissed != st.draft {
			if composerCard.Valid() {
				suggestionRow = buildSlashPopover(c, t, runtime, st, query, composerCard)
			}
		}
		if suggestionRow.Valid() && input.Valid() {
			// The composer keeps the keyboard focus while a suggestion menu is
			// open; the highlighted option is its ActiveDescendant, so assistive
			// technology reads the option the arrows are on (mygo
			// accessibility.md: a list box a text area drives).
			input.ActiveDescendant(suggestionRow)
		}
	})
}

// composerTextKey is the composer's native key hook: the suggestion menus take
// the arrow keys, Enter/Tab and Esc while they are open; Enter sends when idle,
// queues while a turn runs with queueing on, and does nothing (the TextArea
// inserts a newline) while a turn runs with queueing explicitly off. A slash
// draft is never sent as a chat message.
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
			if _, _, active := mentionQuery(st.draft); active && st.mentionDismissed != st.draft {
				st.mentionDismissed = st.draft
				runtime.Invalidate()
				return true
			}
			return false
		}
		// The slash menu only exists while no turn runs (its popover is not
		// built while running): never execute an invisible command from Enter.
		if query, active := slashQuery(st.draft); active && !running && st.slashDismissed != st.draft {
			if handleSlashMenuKey(k, runtime, st, query) {
				return true
			}
		}
		if query, tokenStart, active := mentionQuery(st.draft); active && st.mentionDismissed != st.draft {
			if handleMentionMenuKey(k, runtime, chat, st, query, tokenStart) {
				return true
			}
		}
		if k.Key != ui.KeyEnter || k.Mods != 0 {
			return false
		}
		if running {
			// A running turn never calls chat.Send through Enter: queue the
			// draft when queueing is on, otherwise let Enter insert a newline
			// (the draft stays in the composer and is sent once the reply ends).
			if !queueAllowed {
				return false
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

// buildComposerToolbar is the composer's action row: one attach menu, the
// model chip and one session menu, followed by the fixed send/stop control.
//
// The row used to expose model, permission, agent mode, persona and 会话设置 as
// separate chips plus standalone "/" and "路径" buttons: eight equally weighted
// controls under one input read as visual noise and gave the security/model
// state too much chrome. The current layout keeps at most four controls and
// folds the low-frequency choices into the session menu. Non-default security
// or mode states stay visible on the menu button itself (e.g. 全部允许, 计划),
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
// mode, reasoning effort, persona and prompt presets) behind one button. Its
// label keeps a non-default security/mode state visible.
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
			if m.Item(st.tr("打开模型设置…", "Open model settings…")).Chosen() {
				openSettingsPage(navigator, settingsPageModel)
			}
			m.Separator()
		}
		buildSessionPermissionMenuItems(m, runtime, chat, st, activeID)
		m.Separator()
		buildSessionModeMenuItems(m, runtime, st)
		m.Separator()
		buildSessionThinkingMenuItems(m, runtime, st, chat, activeID)
		// Persona and prompt presets are lists that can grow with user data:
		// nested submenus keep the outer menu short enough to fit a 704x720
		// window (the tail items stay reachable without scrolling).
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
		"权限、对话模式、思考强度与提示词预设。工作区只自动允许工作区内的文件工具，shell/网络仍会询问；全部允许不再弹窗；弹窗中的「本会话内始终允许」只对本会话生效。",
		"Permission, mode, thinking and prompt presets. Workspace only auto-allows workspace file tools; shell/network still ask. Allow all stops every prompt; the dialog's Always allow in this session only covers this conversation."))
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
	// Warning-level security states own the label first; a non-default chat
	// mode then owns it before the (now default) quiet workspace state, so
	// choosing 精简/创作 is visible on the composer button.
	switch permissionMode(st.settings) {
	case contracts.PermissionFull:
		return st.tr("全部允许", "Allow all"), true
	case contracts.PermissionNever:
		return st.tr("禁止工具", "Tools off"), true
	}
	switch currentChatModeOption(st.settings).key {
	case contracts.ChatModeMinimal:
		return st.tr("精简", "Minimal"), false
	case contracts.ChatModeCreator:
		return st.tr("创作", "Creator"), false
	}
	if permissionMode(st.settings) == contracts.PermissionWorkspace {
		return st.tr("工作区权限", "Workspace"), false
	}
	// "Session settings", not "Settings": the global settings surface is
	// already named Settings, and two entries with one name lose the user.
	return st.tr("会话设置", "Session settings"), false
}

// buildSessionPermissionMenuItems adds the permission-mode choices, one quiet
// explanation row so 工作区（默认） is understood, and the session-scoped
// "clear remembered tools" action once the backend has remembered any tool for
// this conversation.
func buildSessionPermissionMenuItems(m *ui.Menu, runtime contracts.UIRuntime,
	chat contracts.ChatService, st *state, activeID string) {
	if st.settings != nil {
		current := permissionMode(st.settings)
		for _, mode := range []string{contracts.PermissionAsk, contracts.PermissionWorkspace, contracts.PermissionFull, contracts.PermissionNever} {
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
		// The workspace default is the one choice users misread ("I gave it
		// workspace permission, why does it keep asking?"): the section header
		// row carries the explanation in its tooltip, so the menu does not grow
		// another row (a taller menu would overflow the 704x720 window).
		m.Item(st.tr("权限模式", "Permission mode")).Disabled(true).Tooltip(st.tr(
			"工作区（默认）：工作区内的文件读写自动允许，shell/网络仍会询问；弹窗中选择「本会话内始终允许」可停止重复询问；全部允许不再弹窗。",
			"Workspace (default): workspace file reads/writes are auto-allowed; shell/network still ask. Choose \"Always allow in this session\" in the dialog to stop repeats; Allow all stops every prompt."))
	}
	// The approval dialog's 本会话内始终允许 remembers tools for one session;
	// this is the one place to see that it is active and to forget it.
	permissions, ok := chat.(contracts.SessionToolPermissions)
	if !ok || activeID == "" {
		return
	}
	allowed := permissions.AllowedTools(activeID)
	if len(allowed) == 0 {
		return
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
// default. An ID the catalog does not know still gets a synthesized entry with
// an inferred family, so custom model names keep the generic thinking modes.
func sessionThinkingModelInfo(st *state, chat contracts.ChatService, activeID string) contracts.ModelInfo {
	model := ""
	if session, ok := chat.Session(activeID); ok {
		model = strings.TrimSpace(session.Model)
	}
	if model == "" {
		model = strings.TrimSpace(chat.Config().Model)
	}
	if catalog, ok := st.ctx.Get(contracts.KeyModelCatalog); ok && catalog != nil {
		if info, found := contracts.FindModelInfo(catalog.Models(), model); found {
			return info
		}
	}
	return contracts.ResolveModelInfo(contracts.DefaultModelCatalog(), model)
}

// buildSessionThinkingMenuItems adds the per-model thinking-strength section:
// the active model's family (plus any user-defined modes) decides the choices,
// and a conversation stores only the selected mode ID, so a change applies to
// the next run without a restart. The legacy per-session reasoning-effort key
// still resolves to the matching reasoning_effort mode, so existing
// conversations keep their behavior until a mode is picked explicitly.
func buildSessionThinkingMenuItems(m *ui.Menu, runtime contracts.UIRuntime,
	st *state, chat contracts.ChatService, activeID string) {
	if st.settings == nil || activeID == "" {
		return
	}
	info := sessionThinkingModelInfo(st, chat, activeID)
	modes := contracts.ThinkingModesForModel(info)
	if len(modes) == 0 {
		// A custom family with no user modes has no thinking field to send:
		// hide the whole section instead of offering a no-op choice.
		return
	}
	current, _ := contracts.ResolveThinkingMode(info,
		st.settings.Get(contracts.SessionThinkingSettingKey(activeID), ""),
		st.settings.Get(contracts.SessionEffortSettingKey(activeID), ""))
	m.Item(st.tr("思考强度：", "Thinking: ") + current.DisplayName(st.english())).Disabled(true)
	key := contracts.SessionThinkingSettingKey(activeID)
	for _, mode := range modes {
		mode := mode
		item := m.Item(mode.DisplayName(st.english()))
		item.Radio(mode.ID == current.ID)
		if item.Chosen() {
			_ = st.settings.Set(key, mode.ID)
			runtime.Invalidate()
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
		ui.PopoverMenuButton(c, model, func(m *ui.Menu) {
			// mygo v0.8.3 menu metadata: the model note on a second line and
			// the context window as a trailing value, bounded so a long model
			// name cannot make the drawn menu overflow.
			m.MaxWidth(380)
			m.Item(st.tr("选择模型", "Choose a model")).Disabled(true)
			m.Separator()
			if hasCatalog && catalog != nil {
				for _, info := range catalog.Models() {
					label := modelMenuLabel(info)
					item := m.Item(label)
					item.ID(info.ID)
					if note := strings.TrimSpace(info.Note); note != "" {
						item.Subtitle(note)
					}
					if info.ContextWindow > 0 {
						item.Trailing(formatContextWindow(info.ContextWindow))
					}
					item.Radio(info.ID == effectiveModel)
					if item.Chosen() {
						// Model-switch failures stay quiet: the chip keeps
						// showing the effective model of the next frame.
						if err := switcher.SetSessionModel(activeID, info.ID); err == nil {
							runtime.Invalidate()
						}
					}
				}
			} else {
				item := m.Item(model)
				item.Checked(true)
				item.Disabled(true)
			}
			m.Separator()
			if m.Item(st.tr("打开模型设置", "Open model settings")).Chosen() {
				openSettingsPage(navigator, settingsPageModel)
			}
		}).Tooltip(st.tr("切换本会话模型：", "Switch this session's model: ") + effectiveModel)
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

// buildQueueBadge is the running composer's queued-prompt entry: the count is
// a button (H9) that opens the queue popover, where each queued prompt can be
// inspected and cancelled, or the queue cleared.
func buildQueueBadge(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, sessionID string) ui.Element {
	th := t.Theme()
	count := len(st.queuedFor(sessionID))
	label := fmt.Sprintf(st.tr("已排队 %d 条", "%d queued"), count)
	tip := st.tr("查看或取消排队的消息", "View or cancel queued messages")
	if st.queuePaused[sessionID] {
		tip = st.tr("已停止：排队消息不会自动发送，展开后可手动发送或取消",
			"Stopped: queued messages are not sent automatically; open to send or cancel them")
	}
	button := ui.Button(c, label).Label(label).
		Height(th.Space(5)).PaddingX(design.Space2).Radius(design.RadiusMax).
		Background(t.BgHover).TextColor(t.Accent).
		Tooltip(tip)
	if st.queueMenuOpen {
		button.Background(t.BgSelected)
	}
	if button.Clicked() {
		st.queueMenuOpen = !st.queueMenuOpen
		runtime.Invalidate()
	}
	return button
}

// buildQueuePopover lists the active session's queued prompts above the
// composer: each row can be cancelled, the footer clears the whole queue and,
// after 停止 paused it, offers an explicit 发送排队消息 action instead of
// starting another run on its own.
func buildQueuePopover(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	chat contracts.ChatService, st *state, sessionID string, anchor ui.Element, running bool) {
	queued := st.queuedFor(sessionID)
	if !st.queueMenuOpen || len(queued) == 0 {
		return
	}
	paused := st.queuePaused[sessionID]
	panelWidth := anchor.Bounds().W
	if panelWidth < 360 {
		// The anchor is the small count badge: give the prompt rows room to be
		// readable instead of truncating to the badge width.
		panelWidth = 360
	}
	open := true
	ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
		panel.Width(panelWidth).MaxWidthPercent(92).Padding(design.Space2).Gap(design.Space1).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, design.Space2, 0).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusLG).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.12)).
			Label(st.tr("排队消息", "Queued messages"))
		if paused && !running {
			// The stop paused the queue: make the changed behavior explicit.
			design.Apply(ui.Text(c, st.tr("已停止：排队消息不会自动发送，可手动发送或取消",
				"Stopped: queued messages are not sent automatically; send or cancel them here")),
				t.TextMuted(design.TypeXXXS)).MaxLines(2)
		}
		// The queued prompts are the official List: list/listitem role and row
		// names for assistive technology, keyboard scrolling, and stable rows
		// as the queue grows, capped so a long queue stays a popover.
		st.queueList.Key = func(row int) any { return row }
		st.queueList.Label = func(row int) string {
			if row < 0 || row >= len(queued) {
				return ""
			}
			return queued[row]
		}
		ui.List(c, &st.queueList, len(queued)).
			Label(st.tr("排队消息", "Queued messages")).
			MaxHeight(design.Space6 * 8).
			FillWidth().
			Rows(func(row ui.ListRow) {
				index := row.Index
				rowC := row.Context
				ui.Row(rowC).FillWidth().
					AlignItems(ui.Center).Gap(design.Space2).PaddingY(design.Space1).
					Children(func() {
						design.Apply(ui.Text(rowC, queuedSummary(queued[index])), t.Font(design.TypeXS)).
							TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
						remove := ui.Button(rowC, "").
							Label(st.tr("取消排队", "Cancel queued")).
							Tooltip(st.tr("取消这条排队消息", "Cancel this queued message")).
							Size(t.Theme().Space(5), t.Theme().Space(5)).PaddingX(0).
							Radius(design.RadiusSM)
						remove.Children(func() {
							ui.Icon(rowC, icons.Close()).Size(design.Space3, design.Space3).
								TextColor(t.LabelTertiary)
						})
						if remove.Clicked() {
							cancelQueuedPrompt(st, sessionID, index)
							runtime.Invalidate()
						}
					})
			})
		ui.Divider(c)
		ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).
			Gap(design.Space2).Children(func() {
			if paused && !running {
				send := design.Apply(ui.Button(c, st.tr("发送排队消息", "Send queued")), t.SecondaryButton()).
					Label(st.tr("发送排队消息", "Send queued")).
					Tooltip(st.tr("发送第一条排队消息；其余消息会在这轮结束后继续",
						"Send the first queued message; the rest follow when that turn ends"))
				if send.Clicked() {
					resumeQueue(chat, st, sessionID)
					runtime.Invalidate()
				}
			}
			clear := design.Apply(ui.Button(c, st.tr("清空全部", "Clear all")), t.SecondaryButton()).
				Label(st.tr("清空全部", "Clear all"))
			if clear.Clicked() {
				clearQueuedPrompts(st, sessionID)
				runtime.Invalidate()
			}
		})
	})
	if !open {
		st.queueMenuOpen = false
	}
	if len(st.queuedFor(sessionID)) == 0 {
		st.queueMenuOpen = false
	}
}

// queuedSummary truncates one queued prompt for the queue row.
func queuedSummary(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	const limit = 80
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "…"
}

// buildSendButton is the composer's primary control: a native primary button
// (accent fill, native hover/pressed/disabled) that never takes part in the
// toolbar's overflow, so it stays visible however narrow the window gets.
func buildSendButton(c *ui.Context, t *design.Tokens, cfg Config, chat contracts.ChatService, st *state, activeID string) {
	th := t.Theme()
	hasContent := strings.TrimSpace(st.draft) != "" || len(st.pendingAttachments) > 0
	label := st.localized(cfg.SendLabel, "发送", "Send")
	hint := st.localized(cfg.ComposerHint, "Enter 发送 · Shift+Enter 换行", "Enter to send · Shift+Enter for newline")
	button := design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton()).
		Label(label).Tooltip(hint).Shrink(0)
	glyph := t.OnPrimary
	if !hasContent {
		button = button.Disabled(true)
		glyph = t.LabelTertiary
	}
	button.Children(func() {
		ui.Icon(c, icons.Send()).Size(th.Space(4), th.Space(4)).TextColor(glyph)
	})
	if button.Clicked() && hasContent {
		submitDraft(chat, activeID, st)
	}
}

// buildQueueSendButton is the running composer's secondary control when
// ui.sendWhileRunning is on: it queues the draft ("排队", with the queue
// explanation in its tooltip) instead of calling chat.Send while the current
// turn is still running.
func buildQueueSendButton(c *ui.Context, t *design.Tokens, st *state, activeID string) {
	hasContent := strings.TrimSpace(st.draft) != ""
	label := st.tr("排队", "Queue")
	tip := st.tr("当前回复尚未结束：加入排队，本轮结束后自动发送", "Still generating: queue it and send when this turn ends")
	if len(st.pendingAttachments) > 0 {
		tip = st.tr("本轮只发送文字；暂存的附件会保留在输入框",
			"Queue sends text only; staged attachments stay in the composer")
	}
	button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
		Label(label).Shrink(0).
		Tooltip(tip)
	if !hasContent {
		button.Disabled(true)
	}
	if button.Clicked() && hasContent {
		queueDraft(st, activeID)
	}
}

// buildStopButton is the running composer's control in the send position: a
// visible danger-styled native button that cancels the run.
func buildStopButton(c *ui.Context, t *design.Tokens, cfg Config, chat contracts.ChatService, st *state, activeID string) {
	th := t.Theme()
	label := st.localized(cfg.StopLabel, "停止", "Stop")
	button := design.Apply(ui.Button(c, label), t.DangerButton()).
		Label(label).Tooltip(label).Shrink(0)
	button.Children(func() {
		ui.Icon(c, icons.Stop()).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.DangerText)
	})
	if button.Clicked() {
		st.autoContinueSuppressed = true
		// A stop must stop: keep the queued prompts for review instead of
		// letting the settle handler start another run automatically.
		pauseQueue(st, activeID)
		if err := chat.Cancel(activeID); err != nil {
			st.commandErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
		} else {
			st.commandErr = ""
			st.toast(contracts.NotifyInfo, st.tr("已停止", "Stopped"))
		}
	}
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

// modelMenuLabel is one model menu row: the display name plus its note, when
// the catalogue provides one.
func modelMenuLabel(info contracts.ModelInfo) string {
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = info.ID
	}
	if note := strings.TrimSpace(info.Note); note != "" {
		return name + " · " + note
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

// usageContextWindowSuffix renders the model context-window budget suffix from
// the optional model catalogue (empty when there is no catalogue, the model is
// unknown, or its ContextWindow is unset).
func (st *state) usageContextWindowSuffix() string {
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
			size := formatContextWindow(info.ContextWindow)
			if st.english() {
				return " · " + size + " context"
			}
			return " · 预算 " + size
		}
	}
	return ""
}

// reasoningLineCount counts the non-blank lines of a finished reasoning body,
// the quiet size hint the folded reasoning row shows instead of a raw tail.
func reasoningLineCount(text string) int {
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	return lines
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
		design.Apply(ui.Text(c, line), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).
			Width(design.TranscriptWidth).MaxWidthPercent(92).
			MaxLines(1).Ellipsis("…").Tooltip(exact)
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
		return false
	}
	// The attachments are in the conversation now; their decoded previews are
	// no longer needed and would otherwise stay in memory for the session's
	// lifetime (the map only grows as images are staged).
	for _, attachment := range attachments {
		delete(st.thumbnails, attachment.ID)
	}
	st.draft = ""
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
// highlight, Enter/Tab run the highlighted command, Esc is handled by the
// caller (it closes the menu without clearing the draft). A slash draft is
// never sent as a chat message, so Enter is swallowed when nothing matches.
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
		if k.Mods != 0 {
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
		panel.Width(panelWidth).MaxWidthPercent(92).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, design.Space2, 0).
			Padding(design.Space2).Gap(design.Space1).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusLG).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.12)).
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
func mentionQuery(draft string) (query string, tokenStart int, ok bool) {
	start := strings.LastIndexAny(draft, " \t\n") + 1
	token := draft[start:]
	if !strings.HasPrefix(token, "@") {
		return "", 0, false
	}
	query = token[1:]
	if strings.ContainsAny(query, " \t\n") {
		return "", 0, false
	}
	return query, start, true
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

// applyMention replaces the current @ token with the chosen reference.
func applyMention(st *state, draft string, tokenStart int, row mentionRow) string {
	var replacement string
	switch row.kind {
	case mentionFile:
		replacement = "@" + row.file.Path + " "
	default:
		replacement = " " + contracts.FormatSessionReference(row.session.ID) + " "
	}
	if tokenStart > len(draft) {
		tokenStart = len(draft)
	}
	next := draft[:tokenStart] + replacement
	st.draft = next
	return next
}

// handleMentionMenuKey drives the @ menu from the keyboard: Up/Down move the
// highlight, Enter/Tab insert the highlighted reference, Esc is handled by the
// caller. With no rows the keys keep their normal editing meaning.
func handleMentionMenuKey(k ui.TextKey, runtime contracts.UIRuntime, chat contracts.ChatService,
	st *state, query string, tokenStart int) bool {
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
		if k.Mods != 0 || len(rows) == 0 {
			return false
		}
		row := rows[clampSelection(st.mentionSel, len(rows))]
		next := applyMention(st, st.draft, tokenStart, row)
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
	query string, tokenStart int, anchor ui.Element) ui.Element {
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
		panel.Width(panelWidth).MaxWidthPercent(92).
			AttachTo(anchor, ui.AnchorTopStart, ui.AnchorBottomStart).
			Margin(0, 0, design.Space2, 0).
			Padding(design.Space2).Gap(design.Space1).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusLG).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.12)).
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
			option := buildMentionRow(c, t, runtime, st, index, sel, len(rows), row, tokenStart)
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
	index, sel, total int, row mentionRow, tokenStart int) ui.Element {
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
		st.draft = applyMention(st, st.draft, tokenStart, row)
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
	if cmd.Run != nil {
		_ = cmd.Run()
	}
	runtime.Invalidate()
}

// --- helpers ----------------------------------------------------------------

// statusLabel maps a tool status to its card label.
func statusLabel(status string, st *state) string {
	switch normalizeStatus(status) {
	case "running":
		return st.tr("运行中", "Running")
	case "ok":
		return st.tr("完成", "Completed")
	case "error":
		return st.tr("失败", "Failed")
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return st.tr("未知", "Unknown")
	}
}

// normalizeStatus folds the status spellings Rig uses into running/ok/error.
func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "in-progress", "active", "started":
		return "running"
	case "ok", "success", "succeeded", "done", "complete", "completed":
		return "ok"
	case "error", "failed", "failure", "canceled", "cancelled":
		return "error"
	default:
		return ""
	}
}

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
