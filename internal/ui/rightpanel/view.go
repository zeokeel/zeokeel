package rightpanel

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Inspector pane indexes; they match the module state.
const (
	tabOverview = iota
	tabTools
	tabModel
)

// buildPanel renders the whole inspector for one frame: heading, the native
// pane tabs and the selected pane. The host owns the panel width; the module
// fills it.
func buildPanel(c *ui.Context, t *design.Tokens, chat contracts.ChatService, cfg Config, st *state) {
	ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Sliders()).Size(16, 16).TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, cfg.Title), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
		})
		buildTabs(c, t, cfg, st)
		// The inspector can outgrow a short rail (a long tool list); the native
		// scroll keeps the pane usable without the module owning the geometry.
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			switch st.tab {
			case tabTools:
				buildToolsPane(c, t, chat, st)
			case tabModel:
				buildModelPane(c, t, chat)
			default:
				buildOverviewPane(c, t, chat, cfg)
			}
		})
	})
}

// buildTabs is the native pane strip, painted as the brand's quiet label with
// the accent underline (same language as the session header tabs).
func buildTabs(c *ui.Context, t *design.Tokens, cfg Config, st *state) {
	th := t.Theme()
	labels := []string{cfg.OverviewTab, cfg.ToolsTab, cfg.ModelTab}
	tabs := ui.TabsBase(c, &st.tab, len(labels))
	tabs.List.Height(t.Scale(th.Space(8))).Padding(0, 0, 0, th.Space(1)).
		Gap(design.Space3).AlignItems(ui.Stretch).Children(func() {
		for index, label := range labels {
			index, label := index, label
			active := st.tab == index
			tab := tabs.Tab(index).Height(t.Scale(th.Space(8))).PaddingX(0).
				Gap(0).Label(label)
			tab.Children(func() {
				ui.Column(c).Height(t.Scale(th.Space(8))).Justify(ui.SpaceBetween).Children(func() {
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

// buildOverviewPane shows the active session's title, message count, update
// time, state badges and error text; without an active session it shows the
// configured empty copy.
func buildOverviewPane(c *ui.Context, t *design.Tokens, chat contracts.ChatService, cfg Config) {
	activeID := chat.ActiveSession()
	chatSession, ok := chat.Session(activeID)
	if activeID == "" || !ok {
		buildEmptyPane(c, t, icons.Message(), cfg.EmptyText)
		return
	}
	title := strings.TrimSpace(chatSession.Title)
	if title == "" {
		title = "未命名会话"
	}
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeM)).
			FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		if chatSession.Running || chatSession.Error != "" {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				if chatSession.Running {
					design.Apply(ui.Badge(c, "运行中"), t.Badge()).TextColor(t.WarningText)
				}
				if chatSession.Error != "" {
					design.Apply(ui.Badge(c, "错误"), t.Badge()).TextColor(t.DangerText)
				}
			})
		}
		detailRow(c, t, "消息", fmt.Sprintf("%d", len(chatSession.Messages)))
		detailRow(c, t, "更新时间", formatUpdatedAt(chatSession.UpdatedAt))
		detailRow(c, t, "会话 ID", firstNonEmpty(chatSession.ID, "—"))
		if chatSession.Error != "" {
			buildErrorLine(c, t, chatSession.Error)
		}
	})
}

// buildToolsPane aggregates every ToolCall of the session, in message order,
// each with a status badge and a collapsible summary.
func buildToolsPane(c *ui.Context, t *design.Tokens, chat contracts.ChatService, st *state) {
	chatSession, ok := chat.Session(chat.ActiveSession())
	if !ok {
		buildEmptyPane(c, t, icons.Terminal(), "还没有工具调用")
		return
	}
	type toolItem struct {
		key  string
		tool contracts.ToolCall
	}
	items := make([]toolItem, 0, 8)
	for messageIndex, message := range chatSession.Messages {
		for toolIndex, tool := range message.Tools {
			items = append(items, toolItem{
				key:  fmt.Sprintf("%d-%d-%s", messageIndex, toolIndex, tool.ID),
				tool: tool,
			})
		}
	}
	if len(items) == 0 {
		buildEmptyPane(c, t, icons.Terminal(), "还没有工具调用")
		return
	}

	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		design.Apply(ui.Text(c, "工具调用"), t.Font(design.TypeXS)).
			FontWeight(600).TextColor(t.LabelPrimary)
		ui.Spacer(c).Grow(1)
		design.Apply(ui.Badge(c, fmt.Sprintf("%d", len(items))), t.Badge())
	})
	for _, item := range items {
		tool := item.tool
		status := statusLabel(tool.Status)
		color := statusColor(t, tool.Status)
		card := design.Apply(ui.Column(c), t.Panel()).
			Padding(design.Space2).Gap(design.Space1)
		card.Children(func() {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				design.Apply(ui.Badge(c, status), t.Badge()).TextColor(color)
				design.Apply(ui.Text(c, tool.Name), t.Font(design.TypeXS)).
					FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				ui.Spacer(c).Grow(1)
				if tool.ID != "" {
					design.Apply(ui.Text(c, tool.ID), t.Font(design.TypeXXS)).
						TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
				}
			})
			if summary := strings.TrimSpace(tool.Summary); summary != "" {
				open := st.open[item.key]
				ui.Collapsible(c, "摘要", &open, func() {
					design.Apply(ui.Text(c, truncateSummary(summary)), t.TextMuted(design.TypeXXS))
				})
				st.open[item.key] = open
			}
		})
	}
}

// buildModelPane shows the effective model configuration. The API key is
// reduced to a set/not-set label: the secret never reaches the screen.
func buildModelPane(c *ui.Context, t *design.Tokens, chat contracts.ChatService) {
	config := chat.Config()
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, "模型配置"), t.Font(design.TypeM)).
			FontWeight(600).TextColor(t.LabelPrimary)
		detailRow(c, t, "模型", firstNonEmpty(config.Model, "未配置"))
		detailRow(c, t, "Base URL", firstNonEmpty(config.BaseURL, "未配置"))
		detailRow(c, t, "API Key", apiKeyState(config.APIKey))
		detailRow(c, t, "上下文预算", budgetLabel(config.ContextBudget))
		detailRow(c, t, "最大迭代", iterationsLabel(config.MaxIterations))
		design.Apply(ui.Text(c, "API key 已打码，不会显示明文。"), t.TextMuted(design.TypeXXS))
	})
}

// buildEmptyPane is the centered empty state of one pane.
func buildEmptyPane(c *ui.Context, t *design.Tokens, icon *ui.SVG, text string) {
	th := t.Theme()
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space6).Children(func() {
		ui.Row(c).Size(th.Space(9), th.Space(9)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, text), t.TextMuted(design.TypeXS))
	})
}

// buildErrorLine is the inspector's danger row.
func buildErrorLine(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).TextColor(t.DangerText)
	})
}

// detailRow is one label/value row of an inspector card.
func detailRow(c *ui.Context, t *design.Tokens, label, value string) {
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		ui.Spacer(c).Grow(1)
		design.Apply(ui.Text(c, value), t.Font(design.TypeXS)).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
	})
}

// statusLabel maps a tool status to its inspector label.
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

// formatUpdatedAt renders a session update timestamp, "—" when unset.
func formatUpdatedAt(updated time.Time) string {
	if updated.IsZero() {
		return "—"
	}
	return updated.Local().Format("2006-01-02 15:04")
}

// apiKeyState reports whether an API key is configured, never the key itself.
func apiKeyState(apiKey string) string {
	if strings.TrimSpace(apiKey) == "" {
		return "未设置"
	}
	return "已设置（已隐藏）"
}

// budgetLabel renders ContextBudget, "默认" when zero.
func budgetLabel(budget int) string {
	if budget <= 0 {
		return "默认"
	}
	return fmt.Sprintf("%d tokens", budget)
}

// iterationsLabel renders MaxIterations, "默认" when zero.
func iterationsLabel(iterations int) string {
	if iterations <= 0 {
		return "默认"
	}
	return fmt.Sprintf("%d", iterations)
}

// truncateSummary bounds an expanded tool summary (UTF-8 safe).
func truncateSummary(summary string) string {
	const max = 1200
	if len(summary) <= max {
		return summary
	}
	cut := max
	for cut > 0 && (summary[cut]&0xC0) == 0x80 {
		cut--
	}
	return summary[:cut] + "…"
}
