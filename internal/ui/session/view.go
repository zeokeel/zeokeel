package session

import (
	"context"
	"fmt"
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
)

// Layout measures of the conversation surface: one centered reading measure
// for the transcript, the old shell's composer card width.
const (
	transcriptWidth  float32 = 840
	composerWidth    float32 = 868
	rightPanelWidth  float32 = 300
	maxSlashCommands         = 8
	maxMentionFiles          = 8
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
	activeID := chat.ActiveSession()
	chatSession, hasSession := chat.Session(activeID)

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		buildHeader(c, t, runtime, cfg, st, chatSession, hasSession)
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
	})
}

// --- header -----------------------------------------------------------------

// buildHeader is the brand header: one title row (title + running badge) over
// the native tab strip with the accent underline of the old shell.
func buildHeader(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, cfg Config, st *state, chatSession contracts.ChatSession, hasSession bool) {
	th := t.Theme()
	ui.Column(c).FillWidth().
		Padding(th.Space(2), th.Space(4), 0, th.Space(4)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Row(c).FillWidth().Height(t.Scale(th.Space(8))).AlignItems(ui.Center).
			Gap(design.Space2).Children(func() {
			title := st.localized(cfg.Title, "对话", "Chat")
			if hasSession && strings.TrimSpace(chatSession.Title) != "" {
				title = chatSession.Title
			}
			design.Apply(ui.Text(c, title), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			if hasSession && chatSession.Running {
				design.Apply(ui.Badge(c, st.tr("运行中", "Running")), t.Badge()).TextColor(t.WarningText)
			}
			if st.settings != nil && agentMode(st.settings) == contracts.AgentModePlan {
				design.Apply(ui.Badge(c, st.tr("计划模式", "Plan mode")), t.Badge()).
					TextColor(t.WarningText)
			}
			buildGoalChip(c, t, runtime, st)
			ui.Spacer(c).Grow(1)
			if hasSession {
				// Secondary conversation actions live in one overflow menu: three
				// equal-weight buttons in the header competed with the title and
				// the tabs for attention.
				ui.PopoverMenuButton(c, st.tr("更多", "More"), func(m *ui.Menu) {
					if m.Item(st.tr("复制会话 ID", "Copy session ID")).Chosen() {
						c.WriteClipboard(chatSession.ID)
					}
					if m.Item(st.tr("复制对话", "Copy conversation")).Chosen() {
						c.WriteClipboard(transcriptText(chatSession, st.english()))
					}
					if m.Item(st.tr("导出 Markdown", "Export Markdown")).Chosen() {
						exportSession(st, chatSession, cfg)
					}
					if m.Item(st.tr("导出 JSONL", "Export JSONL")).Chosen() {
						exportSessionJSONL(st, chatSession, cfg)
					}
				})
			}
		})
		if st.exportErr != "" {
			buildErrorLine(c, t, st.tr("导出失败：", "Export failed: ")+st.exportErr)
		} else if st.exportStatus != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				badge := design.Apply(ui.Badge(c, st.tr("已导出", "Exported")), t.Badge()).
					TextColor(t.Success)
				if st.exportPath != "" {
					badge.Tooltip(st.exportPath)
				}
			})
		}
		if st.memNotice != "" {
			design.Apply(ui.Badge(c, st.memNotice), t.Badge()).TextColor(t.Success)
		}
		buildTabs(c, t, cfg, st)
	})
}

// exportSessionJSONL writes the conversation as JSON Lines through its seam.
func exportSessionJSONL(st *state, chatSession contracts.ChatSession, cfg Config) {
	title := firstNonEmpty(chatSession.Title, cfg.Title, st.tr("会话", "Conversation"))
	// Localize the save dialog title per frame, like the Markdown path.
	ExportJSONLDialogTitle = st.tr("导出会话", "Export conversation")
	content, err := exportJSONLTranscript(chatSession)
	if err != nil {
		st.exportErr = err.Error()
		return
	}
	path, err := ExportJSONLSaver(sanitizeExportFilename(title)+".jsonl", content)
	switch {
	case err != nil:
		// Store the bare error: the header renders the localized prefix once,
		// exactly like the Markdown path (AUDIT-P3-4).
		st.exportErr = err.Error()
		st.exportStatus = ""
		st.exportPath = ""
		notifyExport(st, contracts.NotifyError, st.tr("导出失败：", "Export failed: ")+err.Error())
	case strings.TrimSpace(path) != "":
		st.exportStatus = "ok"
		st.exportPath = path
		st.exportErr = ""
		notifyExport(st, contracts.NotifySuccess, st.tr("对话已导出", "Conversation exported"))
	}
}

// buildGoalChip shows the active goal next to the session title: a small
// "goal" badge, one ellipsized line with the full text in the tooltip, and a
// native clear action backed by the host GoalTracker. It renders nothing when
// no goal is active or the backend is absent.
func buildGoalChip(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	if st.goal == nil || strings.TrimSpace(st.goal.Text) == "" {
		return
	}
	goal := st.goal
	ui.Row(c).Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		design.Apply(ui.Badge(c, st.tr("目标", "Goal")), t.Badge()).TextColor(t.Accent)
		design.Apply(ui.Text(c, goal.Text), t.Font(design.TypeXS)).FontWeight(600).
			TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…").MaxWidth(360).Tooltip(goal.Text)
		clear := ui.Button(c, st.tr("清除目标", "Clear goal")).
			Tooltip(st.tr("清除当前目标", "Clear the current goal"))
		if clear.Clicked() {
			if st.goalTracker != nil {
				st.goalTracker.Clear()
			}
			runtime.Invalidate()
		}
	})
}

// exportSession writes the conversation as Markdown through the export seam
// and records the result for the header status line.
func exportSession(st *state, chatSession contracts.ChatSession, cfg Config) {
	title := firstNonEmpty(chatSession.Title, cfg.Title, st.tr("会话", "Conversation"))
	ExportDialogTitle = st.tr("导出会话", "Export conversation")
	ExportJSONLDialogTitle = st.tr("导出会话", "Export conversation")
	path, err := ExportSaver(sanitizeExportFilename(title)+".md",
		exportTranscript(chatSession, title, st.english()))
	switch {
	case err != nil:
		st.exportErr = err.Error()
		st.exportStatus = ""
		st.exportPath = ""
		notifyExport(st, contracts.NotifyError, st.tr("导出失败：", "Export failed: ")+err.Error())
	case strings.TrimSpace(path) != "":
		st.exportStatus = "ok"
		st.exportPath = path
		st.exportErr = ""
		notifyExport(st, contracts.NotifySuccess, st.tr("对话已导出", "Conversation exported"))
	}
}

// notifyExport raises the optional desktop toast for one export outcome; the
// header badge/status stays the source of truth when no notifier is present.
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

// buildChatPane is the scrollable transcript on one centered reading column.
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
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Column(c), t.Gap(design.Space5)).
				Width(transcriptWidth).MaxWidthPercent(92).
				PaddingY(design.Space5).Children(func() {
				if !hasSession || len(chatSession.Messages) == 0 {
					buildHero(c, t, cfg, navigator, chat, st)
				} else {
					for index, message := range chatSession.Messages {
						buildMessage(c, t, runtime, message, st)
						last := index == len(chatSession.Messages)-1
						if last && message.Role == contracts.ChatRoleAssistant && !chatSession.Running {
							buildRegenerateAction(c, t, runtime, chat, st, chatSession, message)
						}
					}
				}
				if hasSession && chatSession.Error != "" {
					buildErrorLine(c, t, chatSession.Error)
				}
			})
		})
	})
}

// buildHero is the branded first-run empty state: icon badge, title, hint and
// the native new-conversation action.
func buildHero(c *ui.Context, t *design.Tokens, cfg Config, navigator contracts.Navigator, chat contracts.ChatService, st *state) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		PaddingY(design.Space8).Children(func() {
		ui.Row(c).Size(th.Space(10), th.Space(10)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icons.NewChat()).Size(th.Space(5), th.Space(5)).
				TextColor(t.LabelSecondary)
		})
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
		ui.Column(c).FillWidth().AlignItems(ui.End).Gap(design.Space1).Children(func() {
			if strings.TrimSpace(message.Text) != "" {
				design.Apply(ui.Column(c), t.Panel()).
					MaxWidthPercent(70).Padding(design.Space3, design.Space4).Children(func() {
					design.Apply(ui.Text(c, message.Text), t.Font(design.TypeBody)).
						TextColor(t.LabelPrimary)
				})
			}
			buildMessageAttachments(c, t, st, message.Attachments)
		})
	case contracts.ChatRoleTool, contracts.ChatRoleSystem:
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Badge(c, message.Role), t.Badge())
			design.Apply(ui.Text(c, message.Text), t.TextMuted(design.TypeXS))
		})
	default:
		buildAssistantMessage(c, t, runtime, message, st)
	}
}

// buildAssistantMessage renders the assistant turn: collapsible reasoning, the
// streaming body, a status badge, errors and native tool cards.
func buildAssistantMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
	th := t.Theme()
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		if message.Reasoning != "" {
			open := st.thinkOpen[message.ID]
			ui.Collapsible(c, st.tr("思考", "Reasoning"), &open, func() {
				design.Apply(ui.Text(c, message.Reasoning), t.TextMuted(design.TypeXS))
			})
			st.thinkOpen[message.ID] = open
		}
		if message.Text != "" {
			// Assistant prose is rendered by mygo's official Markdown view: the
			// streaming turn heals half-written syntax and the finished turn
			// reuses the same node position (and parse state) as a full view.
			if message.Streaming {
				markdown.Stream(c, message.Text, st.markdownOptions(t))
			} else {
				markdown.View(c, message.Text, st.markdownOptions(t))
			}
		}
		if message.Streaming {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				design.Apply(ui.Badge(c, st.tr("生成中", "Generating")), t.Badge()).TextColor(t.Accent)
				ui.Icon(c, icons.Sparkles()).Size(th.Space(4), th.Space(4)).TextColor(t.Accent)
			})
		}
		if message.Error != "" {
			buildErrorLine(c, t, message.Error)
		}
		for _, tool := range message.Tools {
			call := contracts.ToolCall{ID: tool.ID, Name: tool.Name, Status: tool.Status, Summary: tool.Summary}
			if !runtime.RenderTool(c, call) {
				buildToolFallback(c, t, st, call)
			}
		}
		// Message action row: copy the assistant text to the native clipboard
		// and, when the backend implements SessionFeedbacker, rate the turn.
		hasText := strings.TrimSpace(message.Text) != ""
		chat, _ := st.ctx.Get(contracts.KeyChat)
		feedbacker, canRate := chat.(contracts.SessionFeedbacker)
		sessionID := ""
		if chat != nil {
			sessionID = chat.ActiveSession()
		}
		showFeedback := canRate && chat != nil && sessionID != "" && message.ID != "" && !st.feedbackOpen
		if hasText || showFeedback {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if hasText {
					copyButton := ui.Button(c, st.tr("复制", "Copy")).
						Tooltip(st.tr("复制消息", "Copy message"))
					if copyButton.Clicked() {
						c.WriteClipboard(message.Text)
						st.copiedMessage = message.ID
					}
					if st.copiedMessage == message.ID {
						design.Apply(ui.Badge(c, st.tr("已复制", "Copied")), t.Badge()).
							TextColor(t.Success)
					}
				}
				if showFeedback {
					goodButton := ui.Button(c, st.tr("有帮助", "Helpful")).
						Tooltip(st.tr("这条回复有帮助", "This answer was helpful"))
					if message.Feedback == contracts.FeedbackGood {
						design.Apply(goodButton, t.PrimaryButton())
					}
					if goodButton.Clicked() {
						st.toggleMessageFeedback(chat, feedbacker, runtime, message, contracts.FeedbackGood)
					}
					badButton := ui.Button(c, st.tr("没帮助", "Not helpful")).
						Tooltip(st.tr("这条回复没有帮助", "This answer was not helpful"))
					if message.Feedback == contracts.FeedbackBad {
						design.Apply(badButton, t.PrimaryButton())
					}
					if badButton.Clicked() {
						st.toggleMessageFeedback(chat, feedbacker, runtime, message, contracts.FeedbackBad)
					}
					if message.Feedback != "" {
						design.Apply(ui.Badge(c, st.tr("已反馈", "Rated")), t.Badge()).
							TextColor(t.Success)
					}
				}
			})
			if st.feedbackErr != "" {
				buildErrorLine(c, t, st.feedbackErr)
			}
		}
		// Usage footer: duration and token accounting of the assistant turn.
		if message.Usage != nil {
			design.Apply(ui.Text(c, st.tr("用时 ", "Took ")+formatUsageDuration(message.Usage.DurationMS)+
				" · "+formatTokenCount(usageTotalTokens(message.Usage))+" tokens"+
				usageCachedSuffix(st, message.Usage)),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		}
	})
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

// buildErrorLine is the brand danger row (icon + WCAG-AA danger text).
func buildErrorLine(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText)
	})
}

// buildToolFallback is the native card used when no toolcards module is active.
func buildToolFallback(c *ui.Context, t *design.Tokens, st *state, call contracts.ToolCall) {
	status := statusLabel(call.Status, st)
	color := statusColor(t, call.Status)
	card := design.Apply(ui.Column(c), t.Panel()).
		Padding(design.Space3).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, status), t.Badge()).TextColor(color)
			design.Apply(ui.Text(c, call.Name), t.Font(design.TypeXS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Spacer(c).Grow(1)
			if call.ID != "" {
				design.Apply(ui.Text(c, call.ID), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary)
			}
		})
		if summary := strings.TrimSpace(call.Summary); summary != "" {
			design.Apply(ui.Text(c, summary), t.TextMuted(design.TypeXS))
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

// buildTrajectoryPane is the branded tool timeline: count badge, native
// scrollable cards and collapsible summaries.
func buildTrajectoryPane(c *ui.Context, t *design.Tokens, chatSession contracts.ChatSession, hasSession bool, st *state) {
	items := trajectoryItems(chatSession)
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Column(c), t.Gap(design.Space3)).
				Width(transcriptWidth).MaxWidthPercent(92).
				PaddingY(design.Space5).Children(func() {
				ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					design.Apply(ui.Text(c, st.tr("轨迹", "Trajectory")), t.Font(design.TypeS)).
						FontWeight(600).TextColor(t.LabelPrimary)
					if len(items) > 0 {
						design.Apply(ui.Badge(c, fmt.Sprintf(st.tr("%d 个工具调用", "%d tool calls"), len(items))), t.Badge())
					}
				})
				switch {
				case !hasSession:
					buildPaneEmpty(c, t, st.tr("还没有会话，暂无可查看的轨迹。", "No session yet; nothing to inspect."))
				case len(items) == 0:
					buildPaneEmpty(c, t, st.tr("本次会话还没有工具调用。", "No tool calls in this conversation yet."))
				default:
					for index, item := range items {
						buildTrajectoryCard(c, t, index, item, st)
					}
				}
			})
		})
	})
}

// buildPaneEmpty is the centered muted copy shared by empty panes.
func buildPaneEmpty(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).PaddingY(design.Space8).Children(func() {
		ui.Row(c).Size(th.Space(10), th.Space(10)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icons.Terminal()).Size(th.Space(5), th.Space(5)).TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, text), t.TextMuted(design.TypeXS))
	})
}

// buildTrajectoryCard renders one tool call of the timeline.
func buildTrajectoryCard(c *ui.Context, t *design.Tokens, index int, item trajectoryItem, st *state) {
	tool := item.tool
	status := statusLabel(tool.Status, st)
	color := statusColor(t, tool.Status)
	card := design.Apply(ui.Column(c), t.Panel()).
		Padding(design.Space3).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, fmt.Sprintf(st.tr("步骤 %d", "Step %d"), index+1)), t.Badge())
			design.Apply(ui.Badge(c, status), t.Badge()).TextColor(color)
			design.Apply(ui.Text(c, tool.Name), t.Font(design.TypeXS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			if tool.ID != "" {
				design.Apply(ui.Text(c, tool.ID), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			}
			ui.Spacer(c).Grow(1)
		})
		if summary := strings.TrimSpace(tool.Summary); summary != "" {
			open := st.toolOpen[item.key]
			ui.Collapsible(c, st.tr("结果摘要", "Result summary"), &open, func() {
				design.Apply(ui.Text(c, truncateTrajectorySummary(summary)), t.TextMuted(design.TypeXS))
			})
			st.toolOpen[item.key] = open
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
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		button := ui.Button(c, label).Tooltip(label).Disabled(disabled)
		if button.Clicked() && !disabled {
			if err := regenerator.Regenerate(chatSession.ID); err != nil {
				st.regenerateErr = st.tr("重新生成失败：", "Regenerate failed: ") + err.Error()
			} else {
				st.regenerateErr = ""
			}
			runtime.Invalidate()
		}
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
}

// markdownOptions returns the brand Markdown configuration: body text at the
// design body size (14) with its 22 DIP line box, the default extension set and
// no reveal pacing (the reply is already complete when it is drawn; streaming
// uses the library's own healing parser).
func (st *state) markdownOptions(t *design.Tokens) *markdown.Options {
	if st.md.options != nil && st.md.dark == t.Dark {
		return st.md.options
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = 14
	theme.LineHeight = 22.0 / 14
	st.md.options = &markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
	}
	st.md.dark = t.Dark
	return st.md.options
}

// buildMemoryPane is the Memory tab's real panel: the workspace memory store's
// entries (or search results), native per-entry delete buttons and an add row.
// Without a memory backend (contracts.KeyMemory is not provided) it shows the
// disabled empty state instead of failing.
func buildMemoryPane(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	store, hasStore := st.ctx.Get(contracts.KeyMemory)
	var memories []contracts.Memory
	var listErr error
	if hasStore {
		if query := strings.TrimSpace(st.memoryQuery); query != "" {
			memories, listErr = store.Search(context.Background(), query)
		} else {
			memories, listErr = store.List(context.Background())
		}
	}

	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Column(c), t.Gap(design.Space4)).
				Width(transcriptWidth).MaxWidthPercent(92).
				PaddingY(design.Space5).Children(func() {
				// Heading: title + entry count.
				ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					design.Apply(ui.Text(c, st.tr("记忆", "Memory")), t.Font(design.TypeS)).
						FontWeight(600).TextColor(t.LabelPrimary)
					if hasStore && listErr == nil {
						design.Apply(ui.Badge(c, fmt.Sprintf("%d", len(memories))), t.Badge())
					}
					ui.Spacer(c).Grow(1)
				})
				if !hasStore {
					buildPaneEmpty(c, t, st.tr("记忆后端未启用", "Memory backend not enabled"))
					return
				}

				// Search (store.Search for a non-empty query, List otherwise).
				ui.SearchField(c, &st.memoryQuery).
					Label(st.tr("搜索记忆", "Search memory")).Placeholder(st.tr("搜索记忆", "Search memory")).FillWidth()
				if listErr != nil {
					buildErrorLine(c, t, st.tr("读取记忆失败：", "Failed to read memory: ")+listErr.Error())
				}

				switch {
				case listErr != nil:
					// The error row above is the whole content.
				case len(memories) == 0 && strings.TrimSpace(st.memoryQuery) != "":
					buildPaneEmpty(c, t, st.tr("没有匹配的记忆", "No matching memories"))
				case len(memories) == 0:
					buildPaneEmpty(c, t, st.tr("还没有记忆", "No memories yet"))
				default:
					for _, memory := range memories {
						buildMemoryCard(c, t, runtime, store, st, memory)
					}
				}

				// Add row: native input + primary action, disabled while empty.
				buildMemoryAdd(c, t, runtime, store, st)
			})
		})
	})
}

// buildMemoryCard is one memory entry: body text, scope/created metadata and a
// native delete action.
func buildMemoryCard(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, store contracts.MemoryStore, st *state, memory contracts.Memory) {
	th := t.Theme()
	card := design.Apply(ui.Column(c), t.Panel()).
		Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
			design.Apply(ui.Text(c, memory.Text), t.Font(design.TypeBody)).
				TextColor(t.LabelPrimary).Grow(1)
			remove := ui.Button(c, st.tr("删除", "Delete")).Label(st.tr("删除记忆", "Delete memory")).Height(th.Space(7)).
				PaddingX(design.Space2).Radius(design.RadiusSM).TextColor(t.DangerText)
			if remove.Clicked() {
				if store.Remove(context.Background(), memory.ID) {
					st.memoryError = ""
				} else {
					st.memoryError = st.tr("删除记忆失败：", "Delete failed: ") + firstNonEmpty(memory.ID, st.tr("未知条目", "unknown entry"))
				}
				runtime.Invalidate()
			}
		})
		meta := formatTimestamp(memory.Created)
		if scope := memoryScopeLabel(st, memory.Scope); scope != "" {
			meta = scope + " · " + meta
		}
		design.Apply(ui.Text(c, meta), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
	})
}

// buildMemoryAdd is the native add row at the bottom of the Memory tab.
func buildMemoryAdd(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, store contracts.MemoryStore, st *state) {
	draft := strings.TrimSpace(st.memoryDraft)
	card := design.Apply(ui.Column(c), t.Panel()).
		Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, st.tr("新建记忆", "New memory")), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		ui.TextInput(c, &st.memoryDraft).Label(st.tr("记忆内容", "Memory text")).
			Placeholder(st.tr("输入要记住的内容", "Enter something to remember")).FillWidth()
		if draft == "" {
			design.Apply(ui.Text(c, st.tr("输入内容后可添加。", "Type something to add it.")), t.TextMuted(design.TypeXXS))
		}
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
				}
				runtime.Invalidate()
			}
		})
		if st.memoryError != "" {
			buildErrorLine(c, t, st.memoryError)
		}
	})
}

// formatTimestamp renders a local display timestamp, "—" when unset.
func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04")
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

// buildComposer is the brand composer card: raised panel, borderless native
// multiline input, then a toolbar with a hint and the send/stop control. Enter
// sends (Shift+Enter keeps a newline) through the native text key hook.
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
	var input ui.Element

	ui.Column(c).FillWidth().PaddingY(th.Space(2)).AlignItems(ui.Center).Children(func() {
		// "@" mentions reference workspace files and other conversations (the
		// old composer's file/chat mentions); the suggestion panel sits above
		// the composer exactly like the slash menu.
		if query, tokenStart, active := mentionQuery(st.draft); active {
			var workspace contracts.WorkspaceService
			if service, ok := st.ctx.Get(contracts.KeyWorkspace); ok {
				workspace = service
			}
			if workspace != nil || len(mentionSessionRows(chat, query)) > 0 {
				buildMentionMenu(c, t, runtime, workspace, chat, st, query, tokenStart)
			}
		}
		if query, active := slashQuery(st.draft); active {
			buildSlashMenu(c, t, runtime, st, query)
		}
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			card := design.Apply(ui.Column(c), t.Panel()).
				FillWidth().MaxWidth(composerWidth).MaxWidthPercent(94).
				Radius(design.RadiusXL).
				Shadow(0, 2, 4, 0, ui.RGBA(0, 0, 0, 0.05)).
				Padding(th.Space(3)).Gap(design.Space2).
				Label(st.tr("会话输入框", "Session composer"))
			card.Children(func() {
				buildPendingAttachments(c, t, runtime, st)
				placeholder := st.localized(cfg.ComposerPlaceholder, "输入消息…", "Ask ZeoKeel…")
				if !running {
					input = ui.TextArea(c, &st.draft).
						Label(placeholder).
						Placeholder(placeholder).
						Background(ui.Transparent).Border(0, ui.Transparent).
						Padding(0, th.Space(1)).
						MinHeight(th.Space(9)).Grow(1).
						OnTextKey(func(k ui.TextKey) bool {
							if k.Key != ui.KeyEnter || k.Mods != 0 {
								return false
							}
							if submitDraft(chat, activeID, st) {
								k.SetText("")
							}
							return true
						})
					// The slash menu is inserted above the composer, which
					// shifts the frame's element order; keep the native text
					// input focused so typing and Enter keep working while the
					// menu is open (P2 keeps the send semantics unchanged).
					if _, slashActive := slashQuery(st.draft); slashActive {
						input.Focus()
					}
				}
				ui.Row(c).FillWidth().AlignItems(ui.Center).
					Gap(design.Space2).Children(func() {
					buildComposerToolbar(c, t, runtime, navigator, chat, st, running)
					ui.Spacer(c).Grow(1)
					hint := st.localized(cfg.ComposerHint,
						"Enter 发送 · Shift+Enter 换行",
						"Enter to send · Shift+Enter for newline")
					if running {
						hint = st.tr("正在生成回复…", "Generating a reply…")
					}
					design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(1)
					if running {
						buildStopButton(c, t, cfg, chat, st, activeID)
					} else {
						buildSendButton(c, t, cfg, chat, st, activeID)
					}
				})
			})
			switch {
			case !running && input.Focused():
				card.Border(1, t.Accent)
			case !running && input.Hovered():
				card.Border(1, t.BorderL3)
			}
		})
	})
}

// buildComposerToolbar is the composer's bottom-left control row from the old
// shell: the model chip (opens the model settings page), the slash-command
// entry (fills the draft with "/" so the command menu opens) and the attach
// control (a native button whose file picker lands with the attachment slice).
func buildComposerToolbar(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, st *state, running bool) {
	// The model chip shows the session's effective model (session override,
	// then the configured model) and opens the native model menu when the chat
	// backend can switch a session's model. Without that half (or without an
	// active session) it keeps the old behavior: open the settings surface.
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
		model = st.tr("未配置模型", "No model configured")
	}
	switcher, canSwitch := chat.(contracts.SessionModelSwitcher)
	catalog, hasCatalog := st.ctx.Get(contracts.KeyModelCatalog)
	if canSwitch && hasSession && activeID != "" {
		ui.PopoverMenuButton(c, model, func(m *ui.Menu) {
			m.Item(st.tr("选择模型", "Choose a model")).Disabled(true)
			m.Separator()
			if hasCatalog && catalog != nil {
				for _, info := range catalog.Models() {
					label := modelMenuLabel(info)
					item := m.Item(label)
					item.Checked(info.ID == effectiveModel)
					if item.Chosen() {
						// Model-switch failures stay quiet: the chip keeps
						// showing the effective model of the next frame.
						if err := switcher.SetSessionModel(activeID, info.ID); err == nil {
							runtime.Invalidate()
						}
					}
				}
			} else {
				current := model
				item := m.Item(current)
				item.Checked(true)
				item.Disabled(true)
			}
			m.Separator()
			if m.Item(st.tr("打开模型设置", "Open model settings")).Chosen() {
				navigator.ToggleSettings()
			}
		}).Tooltip(st.tr("切换本会话模型", "Switch this session's model"))
	} else {
		modelButton := ui.Button(c, model).Tooltip(st.tr("打开模型设置", "Open model settings"))
		if modelButton.Clicked() {
			navigator.ToggleSettings()
		}
	}
	// The current agent permission mode sits next to the model chip; the
	// 权限模式 slash command cycles it, this badge only reports it (per frame,
	// so the switch applies on the next frame).
	if st.settings != nil {
		badge := design.Apply(ui.Badge(c, st.permissionLabel(permissionMode(st.settings))), t.Badge())
		badge.Tooltip(st.tr("当前权限模式", "Current permission mode"))
	}
	// Reasoning-effort selector: per-session setting, hidden without a store.
	if st.settings != nil && activeID != "" {
		current := effortValue(st.settings, activeID)
		ui.PopoverMenuButton(c, st.tr("推理：", "Reasoning: ")+st.effortLabel(current), func(m *ui.Menu) {
			m.Item(st.tr("推理强度", "Reasoning effort")).Disabled(true)
			m.Separator()
			for _, option := range effortOptions {
				option := option
				item := m.Item(st.effortLabel(option.value))
				item.Checked(option.value == current)
				if item.Chosen() {
					_ = st.settings.Set(contracts.SessionEffortSettingKey(activeID), option.value)
					runtime.Invalidate()
				}
			}
		}).Tooltip(st.tr("本会话的推理强度", "Reasoning effort for this session"))
	}
	// Agent mode selector: normal or plan (per-session setting, hidden without
	// a settings store). The header shows the 计划模式 badge while plan mode is
	// active.
	if st.settings != nil {
		mode := agentMode(st.settings)
		label := st.tr("普通", "Normal")
		if mode == contracts.AgentModePlan {
			label = st.tr("计划", "Plan")
		}
		ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
			normal := m.Item(st.tr("普通模式", "Normal mode"))
			normal.Checked(mode == contracts.AgentModeNormal)
			if normal.Chosen() && mode != contracts.AgentModeNormal {
				_ = st.settings.Set(contracts.SettingAgentMode, contracts.AgentModeNormal)
				runtime.Invalidate()
			}
			plan := m.Item(st.tr("计划模式", "Plan mode"))
			plan.Checked(mode == contracts.AgentModePlan)
			if plan.Chosen() && mode != contracts.AgentModePlan {
				_ = st.settings.Set(contracts.SettingAgentMode, contracts.AgentModePlan)
				runtime.Invalidate()
			}
		}).Tooltip(st.tr("代理模式", "Agent mode"))
	}
	if !running {
		presets := effectivePromptPresets(st)
		ui.PopoverMenuButton(c, st.tr("预设", "Presets"), func(m *ui.Menu) {
			m.Item(st.tr("插入预设", "Insert preset")).Disabled(true)
			m.Separator()
			if len(presets) == 0 {
				m.Item(st.tr("还没有预设", "No presets yet")).Disabled(true)
				return
			}
			for _, preset := range presets {
				item := m.Item(strings.TrimSpace(preset.Name))
				if item.Chosen() {
					insertPromptPreset(st, preset.Prompt)
					runtime.Invalidate()
				}
				// Native MenuItem has no secondary text; a disabled note line
				// shows the prompt summary under each title instead.
				if summary := promptSummary(preset.Prompt); summary != "" {
					m.Item(summary).Disabled(true)
				}
			}
		}).Tooltip(st.tr("把提示词预设插入当前草稿", "Insert a prompt preset into the draft"))
		slash := ui.Button(c, "/").Tooltip(st.tr("命令：输入 / 打开命令菜单", "Commands: type / to open the command menu"))
		if slash.Clicked() {
			st.draft += "/"
			runtime.Invalidate()
		}
		attach := ui.Button(c, st.tr("附件", "Attach")).Tooltip(st.tr("添加文件或图片", "Add files or images"))
		if attach.Clicked() {
			AttachmentDialogTitle = st.tr("选择附件", "Choose attachments")
			AttachmentFilterName = st.tr("文件与图片", "Files and images")
			paths, err := AttachmentPicker()
			if err != nil {
				st.attachErr = st.tr("无法打开文件选择器：", "Could not open the file picker: ") + err.Error()
			} else {
				stageAttachmentFiles(st, paths)
			}
			runtime.Invalidate()
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

// effortOption is one reasoning-effort choice: the stored value ("" = default)
// and its short label.
type effortOption struct {
	value string
	zh    string
	en    string
}

// effortOptions lists the reasoning-effort choices in menu order.
var effortOptions = []effortOption{
	{value: "", zh: "默认", en: "Default"},
	{value: contracts.SessionEffortLow, zh: "低", en: "Low"},
	{value: contracts.SessionEffortMedium, zh: "中", en: "Medium"},
	{value: contracts.SessionEffortHigh, zh: "高", en: "High"},
}

// effortValue reads the session's stored effort, normalizing unknown values to
// the default ("").
func effortValue(settings contracts.SettingsService, sessionID string) string {
	raw := strings.ToLower(strings.TrimSpace(settings.Get(contracts.SessionEffortSettingKey(sessionID), "")))
	switch raw {
	case contracts.SessionEffortLow, contracts.SessionEffortMedium, contracts.SessionEffortHigh:
		return raw
	default:
		return ""
	}
}

// effortLabel renders one effort value with its short localized label.
func (st *state) effortLabel(value string) string {
	for _, option := range effortOptions {
		if option.value == value {
			return st.tr(option.zh, option.en)
		}
	}
	return st.tr("默认", "Default")
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

// usageCachedSuffix renders the cached-token suffix (empty when none).
func usageCachedSuffix(st *state, usage *contracts.MessageUsage) string {
	if usage.CachedTokens <= 0 {
		return ""
	}
	return " · " + st.tr("缓存 ", "cached ") + formatTokenCount(usage.CachedTokens)
}

// formatUsageDuration renders a turn duration: milliseconds under one second,
// otherwise one decimal of seconds.
func formatUsageDuration(durationMS int64) string {
	if durationMS < 1000 {
		return fmt.Sprintf("%dms", durationMS)
	}
	return fmt.Sprintf("%.1fs", float64(durationMS)/1000)
}

// formatTokenCount groups thousands with commas (1234 → 1,234).
func formatTokenCount(count int) string {
	digits := strconv.Itoa(count)
	if len(digits) <= 3 {
		return digits
	}
	var builder strings.Builder
	first := len(digits) % 3
	if first > 0 {
		builder.WriteString(digits[:first])
	}
	for index := first; index < len(digits); index += 3 {
		if builder.Len() > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(digits[index : index+3])
	}
	return builder.String()
}

// buildSendButton is the composer's primary control: the native button keeps
// focus/Enter/Space/disabled semantics, painted in the brand accent with the
// chevron glyph.
func buildSendButton(c *ui.Context, t *design.Tokens, cfg Config, chat contracts.ChatService, st *state, activeID string) {
	th := t.Theme()
	hasContent := strings.TrimSpace(st.draft) != "" || len(st.pendingAttachments) > 0
	fill, glyph := t.Accent, t.AccentText
	if !hasContent {
		fill, glyph = t.Accent.Alpha(0.16).Over(t.BgRaised), t.Accent
	}
	sendLabel := st.localized(cfg.SendLabel, "发送", "Send")
	button := ui.Button(c, sendLabel).Label(sendLabel).Tooltip(sendLabel).
		Height(t.Scale(th.Space(8))).PaddingX(design.Space3).
		Radius(th.Radius).Background(fill).TextColor(glyph).Disabled(!hasContent)
	button.Children(func() {
		ui.Icon(c, icons.ChevronRight()).Size(th.Space(4), th.Space(4)).TextColor(glyph)
	})
	switch {
	case !hasContent:
	case button.Pressed():
		button.Background(t.AccentPressed)
	case button.Hovered():
		button.Background(t.AccentHover)
	}
	if button.Clicked() && hasContent {
		submitDraft(chat, activeID, st)
	}
}

// buildStopButton is the running composer's control: a native button with the
// stop square glyph, calling Cancel.
func buildStopButton(c *ui.Context, t *design.Tokens, cfg Config, chat contracts.ChatService, st *state, activeID string) {
	th := t.Theme()
	stopLabel := st.localized(cfg.StopLabel, "停止", "Stop")
	button := ui.Button(c, stopLabel).Label(stopLabel).Tooltip(stopLabel).
		Height(t.Scale(th.Space(8))).PaddingX(design.Space3).
		Radius(th.Radius).Background(t.BgStrong).TextColor(t.LabelPrimary)
	button.Children(func() {
		ui.Box(c).Size(th.Space(2.5), th.Space(2.5)).Radius(design.RadiusXS).Background(t.Danger)
	})
	if button.Clicked() {
		_ = chat.Cancel(activeID)
	}
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
		matches = append(matches, cmd)
		if len(matches) == maxSlashCommands {
			break
		}
	}
	return matches
}

// buildSlashMenu is the painter-free composer suggestion panel: a brand card
// above the composer, one native ButtonBase row per matching command.
func buildSlashMenu(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state, query string) {
	matches := matchingCommands(runtime, query)
	menu := design.Apply(ui.Column(c), t.Panel()).
		Width(composerWidth).MaxWidthPercent(94).Shrink(0).
		Padding(design.Space2).Gap(design.Space1)
	menu.Children(func() {
		if len(matches) == 0 {
			design.Apply(ui.Text(c, st.tr("没有匹配的命令", "No matching commands")), t.TextMuted(design.TypeXS)).
				PaddingX(design.Space2).PaddingY(design.Space1)
			return
		}
		for _, cmd := range matches {
			cmd := cmd
			row := ui.ButtonBase(c).FillWidth().AlignItems(ui.Center).
				Gap(design.Space2).Padding(design.Space1, design.Space2).
				Radius(design.RadiusSM)
			if row.Hovered() {
				row.Background(t.BgHover)
			}
			row.Children(func() {
				ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
					TextColor(t.LabelTertiary)
				title := firstNonEmpty(cmd.Title, cmd.ID)
				design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).FontWeight(600).
					TextColor(t.LabelPrimary)
				if hint := strings.TrimSpace(cmd.Hint); hint != "" {
					design.Apply(ui.Text(c, hint), t.TextMuted(design.TypeXXXS))
				}
			})
			if row.Clicked() {
				executeSlashCommand(runtime, st, cmd)
			}
		}
	})
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
// active session is excluded, the query matches the title or id
// case-insensitively, and the list is capped like the file suggestions.
func mentionSessionRows(chat contracts.ChatService, query string) []contracts.ChatSessionInfo {
	active := chat.ActiveSession()
	needle := strings.ToLower(strings.TrimSpace(query))
	rows := make([]contracts.ChatSessionInfo, 0, maxMentionFiles)
	for _, info := range chat.Sessions() {
		if info.ID == active {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(info.Title+"\x00"+info.ID), needle) {
			continue
		}
		rows = append(rows, info)
		if len(rows) == maxMentionFiles {
			break
		}
	}
	return rows
}

// buildMentionMenu is the composer's @ suggestion panel: a brand card listing
// the workspace files the query matches and, when other conversations exist,
// a 会话 group referencing them. Choosing a file inserts "@path " into the
// draft; choosing a conversation inserts "@session:<id>" (the backend expands
// the referenced transcript when the prompt is sent).
func buildMentionMenu(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	workspace contracts.WorkspaceService, chat contracts.ChatService, st *state,
	query string, tokenStart int) {
	ctx := context.Background()
	var (
		entries []contracts.FileEntry
		loadErr error
	)
	if workspace != nil {
		if strings.TrimSpace(query) == "" {
			entries, loadErr = workspace.Files(ctx, "")
		} else {
			entries, loadErr = workspace.Search(ctx, query, maxMentionFiles)
		}
		if len(entries) > maxMentionFiles {
			entries = entries[:maxMentionFiles]
		}
	}
	sessions := mentionSessionRows(chat, query)
	if workspace == nil && len(sessions) == 0 {
		return
	}
	menu := design.Apply(ui.Column(c), t.Panel()).
		Width(composerWidth).MaxWidthPercent(94).Shrink(0).
		Padding(design.Space2).Gap(design.Space1)
	menu.Children(func() {
		if workspace != nil {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Icon(c, icons.Search()).Size(design.Space3, design.Space3).
					TextColor(t.LabelTertiary)
				design.Apply(ui.Text(c, st.tr("工作区文件", "Workspace files")), t.TextMuted(design.TypeXXS)).Grow(1)
				design.Apply(ui.Text(c, st.tr("@ 引用文件", "@ reference a file")), t.TextMuted(design.TypeXXXS))
			})
			switch {
			case loadErr != nil:
				design.Apply(ui.Text(c, st.tr("读取工作区失败：", "Failed to read the workspace: ")+loadErr.Error()),
					t.TextMuted(design.TypeXS)).TextColor(t.DangerText).PaddingX(design.Space2)
			case len(entries) == 0:
				design.Apply(ui.Text(c, st.tr("没有匹配的文件", "No matching files")), t.TextMuted(design.TypeXS)).
					PaddingX(design.Space2).PaddingY(design.Space1)
			default:
				for _, entry := range entries {
					entry := entry
					row := ui.ButtonBase(c).FillWidth().AlignItems(ui.Center).
						Gap(design.Space2).Padding(design.Space1, design.Space2).
						Radius(design.RadiusSM)
					if row.Hovered() {
						row.Background(t.BgHover)
					}
					row.Children(func() {
						icon := icons.Globe()
						if entry.Dir {
							icon = icons.Folder()
						}
						ui.Icon(c, icon).Size(design.Space3, design.Space3).
							TextColor(t.LabelSecondary)
						design.Apply(ui.Text(c, "/"+entry.Path), t.Font(design.TypeXS)).
							TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
						if !entry.Dir {
							design.Apply(ui.Text(c, formatAttachmentSize(int(entry.Size))),
								t.TextMuted(design.TypeXXXS))
						}
					})
					if row.Clicked() {
						st.draft = st.draft[:tokenStart] + "@" + entry.Path + " "
						runtime.Invalidate()
					}
				}
			}
		}
		if len(sessions) > 0 {
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
			for _, info := range sessions {
				info := info
				title := firstNonEmpty(info.Title, info.ID)
				row := ui.ButtonBase(c).FillWidth().AlignItems(ui.Center).
					Gap(design.Space2).Padding(design.Space1, design.Space2).
					Radius(design.RadiusSM).Label(title)
				if row.Hovered() {
					row.Background(t.BgHover)
				}
				row.Children(func() {
					ui.Icon(c, icons.Message()).Size(design.Space3, design.Space3).
						TextColor(t.LabelSecondary)
					design.Apply(ui.Text(c, title), t.Font(design.TypeXS)).
						TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
					design.Apply(ui.Text(c, info.ID), t.MonoFamily()).FontSize(10).
						TextColor(t.LabelTertiary).NoWrap()
				})
				if row.Clicked() {
					st.draft = st.draft[:tokenStart] + " " + contracts.FormatSessionReference(info.ID) + " "
					runtime.Invalidate()
				}
			}
		}
	})
}

// executeSlashCommand clears the composer draft, runs the command (nil safe)
// and requests the frame that hides the menu.
func executeSlashCommand(runtime contracts.UIRuntime, st *state, cmd contracts.CommandSpec) {
	st.draft = ""
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

// statusColor maps a tool status to its brand color.
func statusColor(t *design.Tokens, status string) ui.Color {
	switch normalizeStatus(status) {
	case "running":
		return t.WarningText
	case "ok":
		return t.Success
	case "error":
		return t.DangerText
	default:
		return t.LabelTertiary
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
