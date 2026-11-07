package session

import (
	"context"
	"fmt"
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
		buildHeader(c, t, cfg, st, chatSession, hasSession)
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
	})
}

// --- header -----------------------------------------------------------------

// buildHeader is the brand header: one title row (title + running badge) over
// the native tab strip with the accent underline of the old shell.
func buildHeader(c *ui.Context, t *design.Tokens, cfg Config, st *state, chatSession contracts.ChatSession, hasSession bool) {
	th := t.Theme()
	ui.Column(c).FillWidth().
		Padding(th.Space(2), th.Space(4), 0, th.Space(4)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Row(c).FillWidth().Height(t.Scale(th.Space(8))).AlignItems(ui.Center).
			Gap(design.Space2).Children(func() {
			title := cfg.Title
			if hasSession && strings.TrimSpace(chatSession.Title) != "" {
				title = chatSession.Title
			}
			design.Apply(ui.Text(c, title), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			if hasSession && chatSession.Running {
				design.Apply(ui.Badge(c, "运行中"), t.Badge()).TextColor(t.WarningText)
			}
			ui.Spacer(c).Grow(1)
			if hasSession {
				copyID := ui.Button(c, "复制 ID").Tooltip("复制会话 ID")
				if copyID.Clicked() {
					c.WriteClipboard(chatSession.ID)
				}
				copyChat := ui.Button(c, "复制对话").Tooltip("复制会话内容")
				if copyChat.Clicked() {
					c.WriteClipboard(transcriptText(chatSession))
				}
			}
		})
		buildTabs(c, t, cfg, st)
	})
}

// transcriptText formats the session read model for the clipboard: one block
// per message, tool calls summarised inline (the old shell's copy action).
func transcriptText(chatSession contracts.ChatSession) string {
	var builder strings.Builder
	for _, message := range chatSession.Messages {
		if strings.TrimSpace(message.Text) == "" && len(message.Tools) == 0 {
			continue
		}
		role := message.Role
		switch role {
		case "user":
			role = "用户"
		case "assistant":
			role = "助手"
		}
		fmt.Fprintf(&builder, "%s: %s\n", role, strings.TrimSpace(message.Text))
		for _, call := range message.Tools {
			fmt.Fprintf(&builder, "[工具] %s (%s)\n", call.Name, call.Status)
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// buildTabs draws the Chat/Trajectory/Memory strip with mygo's native tabs
// (TabsBase) for selection and keyboard navigation, painted as the brand's
// quiet label + accent underline.
func buildTabs(c *ui.Context, t *design.Tokens, cfg Config, st *state) {
	th := t.Theme()
	labels := []string{cfg.TabChat, cfg.TabTrajectory, cfg.TabMemory}
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
					buildHero(c, t, cfg, navigator, chat)
				} else {
					for _, message := range chatSession.Messages {
						buildMessage(c, t, runtime, message, st)
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
func buildHero(c *ui.Context, t *design.Tokens, cfg Config, navigator contracts.Navigator, chat contracts.ChatService) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		PaddingY(design.Space8).Children(func() {
		ui.Row(c).Size(th.Space(10), th.Space(10)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icons.NewChat()).Size(th.Space(5), th.Space(5)).
				TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, "开始新对话"), t.Font(design.TypeM)).
			FontWeight(600).TextColor(t.LabelPrimary)
		design.Apply(ui.Text(c, cfg.EmptyText), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(2)
		button := design.Apply(ui.PrimaryButton(c, "新对话"), t.PrimaryButton())
		if button.Clicked() {
			startNewSession(chat, navigator)
		}
	})
}

// buildMessage renders one transcript entry. User turns are right-aligned
// raised bubbles; assistant turns are plain brand body text with optional
// reasoning, tool cards and errors.
func buildMessage(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
	switch message.Role {
	case contracts.ChatRoleUser:
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			design.Apply(ui.Column(c), t.Panel()).
				MaxWidthPercent(70).Padding(design.Space3, design.Space4).Children(func() {
				design.Apply(ui.Text(c, message.Text), t.Font(design.TypeBody)).
					TextColor(t.LabelPrimary)
			})
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
			ui.Collapsible(c, "思考", &open, func() {
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
				design.Apply(ui.Badge(c, "生成中"), t.Badge()).TextColor(t.Accent)
				ui.Icon(c, icons.Sparkles()).Size(th.Space(4), th.Space(4)).TextColor(t.Accent)
			})
		}
		if message.Error != "" {
			buildErrorLine(c, t, message.Error)
		}
		for _, tool := range message.Tools {
			call := contracts.ToolCall{ID: tool.ID, Name: tool.Name, Status: tool.Status, Summary: tool.Summary}
			if !runtime.RenderTool(c, call) {
				buildToolFallback(c, t, call)
			}
		}
	})
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
func buildToolFallback(c *ui.Context, t *design.Tokens, call contracts.ToolCall) {
	status := statusLabel(call.Status)
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
					design.Apply(ui.Text(c, "轨迹"), t.Font(design.TypeS)).
						FontWeight(600).TextColor(t.LabelPrimary)
					if len(items) > 0 {
						design.Apply(ui.Badge(c, fmt.Sprintf("%d 个工具调用", len(items))), t.Badge())
					}
				})
				switch {
				case !hasSession:
					buildPaneEmpty(c, t, "还没有会话，暂无可查看的轨迹。")
				case len(items) == 0:
					buildPaneEmpty(c, t, "本次会话还没有工具调用。")
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
	status := statusLabel(tool.Status)
	color := statusColor(t, tool.Status)
	card := design.Apply(ui.Column(c), t.Panel()).
		Padding(design.Space3).Gap(design.Space1)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Badge(c, fmt.Sprintf("步骤 %d", index+1)), t.Badge())
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
			ui.Collapsible(c, "结果摘要", &open, func() {
				design.Apply(ui.Text(c, truncateTrajectorySummary(summary)), t.TextMuted(design.TypeXS))
			})
			st.toolOpen[item.key] = open
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
					design.Apply(ui.Text(c, "记忆"), t.Font(design.TypeS)).
						FontWeight(600).TextColor(t.LabelPrimary)
					if hasStore && listErr == nil {
						design.Apply(ui.Badge(c, fmt.Sprintf("%d", len(memories))), t.Badge())
					}
					ui.Spacer(c).Grow(1)
				})
				if !hasStore {
					buildPaneEmpty(c, t, "记忆后端未启用")
					return
				}

				// Search (store.Search for a non-empty query, List otherwise).
				ui.SearchField(c, &st.memoryQuery).
					Label("搜索记忆").Placeholder("搜索记忆").FillWidth()
				if listErr != nil {
					buildErrorLine(c, t, "读取记忆失败："+listErr.Error())
				}

				switch {
				case listErr != nil:
					// The error row above is the whole content.
				case len(memories) == 0 && strings.TrimSpace(st.memoryQuery) != "":
					buildPaneEmpty(c, t, "没有匹配的记忆")
				case len(memories) == 0:
					buildPaneEmpty(c, t, "还没有记忆")
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
			remove := ui.Button(c, "删除").Label("删除记忆").Height(th.Space(7)).
				PaddingX(design.Space2).Radius(design.RadiusSM).TextColor(t.DangerText)
			if remove.Clicked() {
				if store.Remove(context.Background(), memory.ID) {
					st.memoryError = ""
				} else {
					st.memoryError = "删除记忆失败：" + firstNonEmpty(memory.ID, "未知条目")
				}
				runtime.Invalidate()
			}
		})
		meta := formatTimestamp(memory.Created)
		if scope := memoryScopeLabel(memory.Scope); scope != "" {
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
		design.Apply(ui.Text(c, "新建记忆"), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		ui.TextInput(c, &st.memoryDraft).Label("记忆内容").
			Placeholder("输入要记住的内容").FillWidth()
		if draft == "" {
			design.Apply(ui.Text(c, "输入内容后可添加。"), t.TextMuted(design.TypeXXS))
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Spacer(c).Grow(1)
			add := design.Apply(ui.PrimaryButton(c, "添加记忆"), t.PrimaryButton()).
				Disabled(draft == "")
			if add.Clicked() && draft != "" {
				if _, err := store.Add(context.Background(), contracts.Memory{
					Text:  draft,
					Scope: memoryScopeWorkspace,
				}); err != nil {
					st.memoryError = "添加记忆失败：" + err.Error()
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
func memoryScopeLabel(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "workspace", "工作区":
		return "工作区"
	case "global", "全局":
		return "全局"
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
		if query, active := slashQuery(st.draft); active {
			buildSlashMenu(c, t, runtime, st, query)
		}
		ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
			card := design.Apply(ui.Column(c), t.Panel()).
				FillWidth().MaxWidth(composerWidth).MaxWidthPercent(94).
				Radius(design.RadiusXL).
				Shadow(0, 2, 4, 0, ui.RGBA(0, 0, 0, 0.05)).
				Padding(th.Space(3)).Gap(design.Space2).
				Label("会话输入框")
			card.Children(func() {
				if !running {
					input = ui.TextArea(c, &st.draft).
						Label(cfg.ComposerPlaceholder).
						Placeholder(cfg.ComposerPlaceholder).
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
					hint := cfg.ComposerHint
					if running {
						hint = "正在生成回复…"
					}
					design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(1)
					if running {
						buildStopButton(c, t, cfg, chat, activeID)
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
	model := "未配置模型"
	if cfg := chat.Config(); cfg.Model != "" {
		model = cfg.Model
	}
	modelButton := ui.Button(c, model).Tooltip("打开模型设置")
	if modelButton.Clicked() {
		navigator.ToggleSettings()
	}
	if !running {
		slash := ui.Button(c, "/").Tooltip("命令：输入 / 打开命令菜单")
		if slash.Clicked() {
			st.draft += "/"
			runtime.Invalidate()
		}
		attach := ui.Button(c, "附件").Tooltip("附件（下一阶段支持）").Disabled(true)
		_ = attach
	}
}

// buildSendButton is the composer's primary control: the native button keeps
// focus/Enter/Space/disabled semantics, painted in the brand accent with the
// chevron glyph.
func buildSendButton(c *ui.Context, t *design.Tokens, cfg Config, chat contracts.ChatService, st *state, activeID string) {
	th := t.Theme()
	hasDraft := strings.TrimSpace(st.draft) != ""
	fill, glyph := t.Accent, t.AccentText
	if !hasDraft {
		fill, glyph = t.Accent.Alpha(0.16).Over(t.BgRaised), t.Accent
	}
	button := ui.Button(c, cfg.SendLabel).Label(cfg.SendLabel).Tooltip(cfg.SendLabel).
		Height(t.Scale(th.Space(8))).PaddingX(design.Space3).
		Radius(th.Radius).Background(fill).TextColor(glyph).Disabled(!hasDraft)
	button.Children(func() {
		ui.Icon(c, icons.ChevronRight()).Size(th.Space(4), th.Space(4)).TextColor(glyph)
	})
	switch {
	case !hasDraft:
	case button.Pressed():
		button.Background(t.AccentPressed)
	case button.Hovered():
		button.Background(t.AccentHover)
	}
	if button.Clicked() && hasDraft {
		submitDraft(chat, activeID, st)
	}
}

// buildStopButton is the running composer's control: a native button with the
// stop square glyph, calling Cancel.
func buildStopButton(c *ui.Context, t *design.Tokens, cfg Config, chat contracts.ChatService, activeID string) {
	th := t.Theme()
	button := ui.Button(c, cfg.StopLabel).Label(cfg.StopLabel).Tooltip(cfg.StopLabel).
		Height(t.Scale(th.Space(8))).PaddingX(design.Space3).
		Radius(th.Radius).Background(t.BgStrong).TextColor(t.LabelPrimary)
	button.Children(func() {
		ui.Box(c).Size(th.Space(2.5), th.Space(2.5)).Radius(design.RadiusXS).Background(t.Danger)
	})
	if button.Clicked() {
		_ = chat.Cancel(activeID)
	}
}

// submitDraft sends the trimmed draft (if any) and clears it on success.
func submitDraft(chat contracts.ChatService, activeID string, st *state) bool {
	prompt := strings.TrimSpace(st.draft)
	if prompt == "" || activeID == "" {
		return false
	}
	if err := chat.Send(activeID, prompt); err != nil {
		return false
	}
	st.draft = ""
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
			design.Apply(ui.Text(c, "没有匹配的命令"), t.TextMuted(design.TypeXS)).
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
func statusLabel(status string) string {
	switch normalizeStatus(status) {
	case "running":
		return "运行中"
	case "ok":
		return "完成"
	case "error":
		return "失败"
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return "未知"
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
func startNewSession(chat contracts.ChatService, navigator contracts.Navigator) {
	id, err := chat.NewSession("新对话")
	if err != nil {
		return
	}
	if err := chat.SetActiveSession(id); err != nil {
		return
	}
	_ = navigator.Push(ScreenID)
}
