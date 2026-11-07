package session

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Tab indexes used by the native ui.Tabs control; they match the state field.
const (
	tabChat = iota
	tabTrajectory
	tabMemory
)

// buildScreen renders one frame of the conversation screen. It only reads the
// chat read model: every mutation happens in a click handler (NewSession, Send,
// Cancel), so the build stays side-effect free apart from the native input
// controls' own value updates.
func buildScreen(
	c *ui.Context,
	runtime contracts.UIRuntime,
	navigator contracts.Navigator,
	chat contracts.ChatService,
	cfg Config,
	st *state,
) {
	theme := c.Theme()
	activeID := chat.ActiveSession()
	chatSession, hasSession := chat.Session(activeID)

	ui.Column(c).FillWidth().Grow(1).Gap(12).Children(func() {
		// Header: session title and a running badge when the agent is busy.
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			title := cfg.Title
			if hasSession && strings.TrimSpace(chatSession.Title) != "" {
				title = chatSession.Title
			}
			ui.Text(c, title).Bold().FontSize(20)
			ui.Spacer(c).Grow(1)
			if hasSession && chatSession.Running {
				ui.Badge(c, "运行中")
			}
		})

		// Native tabs: chat / trajectory / memory (Phase 1 placeholders for the
		// latter two). The host's session.header.tabs slot remains available for
		// later contributions.
		ui.Tabs(c, &st.tab, cfg.TabChat, cfg.TabTrajectory, cfg.TabMemory)
		switch st.tab {
		case tabTrajectory:
			emptyPanel(c, "轨迹面板将在后续阶段接入。", theme.TextMuted)
		case tabMemory:
			emptyPanel(c, "记忆面板将在后续阶段接入。", theme.TextMuted)
		default:
			buildChatTab(c, runtime, navigator, chat, cfg, st, activeID, chatSession, hasSession)
		}
	})
}

// emptyPanel is the native empty state used by tabs that are not wired yet.
func emptyPanel(c *ui.Context, text string, color ui.Color) {
	ui.Column(c).FillWidth().Grow(1).Gap(8).Children(func() {
		ui.Text(c, text).FontSize(13).TextColor(color)
	})
}

// buildChatTab renders the conversation itself plus the composer.
func buildChatTab(
	c *ui.Context,
	runtime contracts.UIRuntime,
	navigator contracts.Navigator,
	chat contracts.ChatService,
	cfg Config,
	st *state,
	activeID string,
	chatSession contracts.ChatSession,
	hasSession bool,
) {
	theme := c.Theme()
	if !hasSession {
		ui.Column(c).FillWidth().Grow(1).Gap(10).Children(func() {
			ui.Text(c, cfg.EmptyText).FontSize(14).TextColor(theme.TextMuted)
			if ui.PrimaryButton(c, "新对话").Clicked() {
				startNewSession(chat, navigator)
			}
		})
		return
	}

	if len(chatSession.Messages) == 0 {
		ui.Column(c).FillWidth().Grow(1).Gap(10).Children(func() {
			ui.Text(c, cfg.EmptyText).FontSize(14).TextColor(theme.TextMuted)
			if ui.PrimaryButton(c, "新对话").Clicked() {
				startNewSession(chat, navigator)
			}
		})
	} else {
		// Native scroll container: the conversation can grow arbitrarily without
		// pushing the composer off screen.
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			for _, message := range chatSession.Messages {
				buildMessage(c, runtime, message, st)
			}
		})
	}

	if chatSession.Error != "" {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Badge(c, "错误")
			ui.Text(c, chatSession.Error).FontSize(13).TextColor(theme.Danger)
		})
	}

	buildComposer(c, chat, cfg, st)
}

// buildMessage renders one message with native controls only.
func buildMessage(c *ui.Context, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
	theme := c.Theme()
	switch message.Role {
	case contracts.ChatRoleUser:
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Badge(c, "你")
			ui.Text(c, message.Text).FontSize(14)
		})
	case contracts.ChatRoleTool:
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Badge(c, "工具")
			ui.Text(c, message.Text).FontSize(13).TextColor(theme.TextMuted)
		})
	case contracts.ChatRoleSystem:
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Badge(c, "系统")
			ui.Text(c, message.Text).FontSize(12).TextColor(theme.TextMuted)
		})
	default:
		buildAssistantMessage(c, runtime, message, st)
	}
	ui.Divider(c)
}

// buildAssistantMessage renders the assistant turn: streaming text, collapsible
// reasoning, per-message errors and one native tool card per tool call.
func buildAssistantMessage(c *ui.Context, runtime contracts.UIRuntime, message contracts.ChatMessage, st *state) {
	theme := c.Theme()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if message.Reasoning != "" {
			// ui.Collapsible draws the native disclosure control; the open/closed
			// state lives per message in the module's UI state.
			open := st.thinkOpen[message.ID]
			ui.Collapsible(c, "思考", &open, func() {
				ui.Text(c, message.Reasoning).FontSize(13).TextColor(theme.TextMuted)
			})
			st.thinkOpen[message.ID] = open
		}
		if message.Text != "" {
			ui.Text(c, message.Text).FontSize(14)
		}
		if message.Streaming {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Badge(c, "生成中")
			})
		}
		if message.Error != "" {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Badge(c, "错误")
				ui.Text(c, message.Error).FontSize(13).TextColor(theme.Danger)
			})
		}
		for _, tool := range message.Tools {
			call := contracts.ToolCall{
				ID:      tool.ID,
				Name:    tool.Name,
				Status:  tool.Status,
				Summary: tool.Summary,
			}
			if !runtime.RenderTool(c, call) {
				// No module registered a renderer for this tool: fall back to a
				// native badge + text row rather than drawing a card ourselves.
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Badge(c, firstNonEmpty(tool.Name, "工具"))
					if tool.Status != "" {
						ui.Text(c, tool.Status).FontSize(12).TextColor(theme.TextMuted)
					}
					if tool.Summary != "" {
						ui.Text(c, tool.Summary).FontSize(13)
					}
				})
			}
		}
	})
}

// buildComposer renders the native input and the send/stop action. While a run
// is in flight the composer collapses to a stop row.
func buildComposer(c *ui.Context, chat contracts.ChatService, cfg Config, st *state) {
	theme := c.Theme()
	activeID := chat.ActiveSession()
	chatSession, hasSession := chat.Session(activeID)
	running := hasSession && chatSession.Running

	ui.Divider(c)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		if running {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "正在生成回复…").FontSize(12).TextColor(theme.TextMuted)
				ui.Spacer(c).Grow(1)
				if ui.Button(c, cfg.StopLabel).Clicked() {
					_ = chat.Cancel(activeID)
				}
			})
			return
		}
		if strings.TrimSpace(st.draft) == "" {
			ui.Text(c, cfg.ComposerPlaceholder).FontSize(12).TextColor(theme.TextMuted)
		}
		ui.TextArea(c, &st.draft).FillWidth().Height(84).Label(cfg.ComposerPlaceholder)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c).Grow(1)
			if ui.PrimaryButton(c, cfg.SendLabel).Clicked() {
				prompt := strings.TrimSpace(st.draft)
				if prompt == "" || activeID == "" {
					return
				}
				// Send is asynchronous in the service: the UI must not block. The
				// service notifies subscribers and the posted Invalidate redraws.
				if err := chat.Send(activeID, prompt); err == nil {
					st.draft = ""
				}
			}
		})
	})
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
