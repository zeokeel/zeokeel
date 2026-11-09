package rightpanel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Inspector pane indexes; they match the module state.
const (
	tabOverview = iota
	tabTools
	tabLog
	tabFiles
	tabChanges
	tabTerminal
	tabAgents
	tabTasks
)

// logLimit bounds the session event rows the Log pane reads per frame.
const logLimit = 50

// paneLabels returns the pane names in tab order, localized for the frame.
func paneLabels(cfg Config, locale contracts.LocaleService) []string {
	return []string{
		cfg.overviewLabel(locale), cfg.toolsLabel(locale), cfg.logLabel(locale),
		cfg.filesLabel(locale), cfg.changesLabel(locale), cfg.terminalLabel(locale),
		cfg.agentsLabel(locale), cfg.tasksLabel(locale),
	}
}

// buildPanel renders the whole inspector for one frame: the compact pane
// selector with the active pane's action, and the selected pane. Eight tabs do
// not fit a 320 DIP inspector, so the pane strip became one menu button naming
// the current pane; the host owns the panel width and the module fills it.
func buildPanel(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, chat contracts.ChatService, log eventReader, workspace contracts.WorkspaceService, terminal contracts.TerminalService, team contracts.TeamService, board contracts.TaskBoard, artifacts contracts.ArtifactLister, cfg Config, st *state) {
	// The pane-switch commands' palette hint names the current pane.
	st.syncPaneCommands()
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		// One header line: the pane selector names the current pane and the
		// pane's action plus the close control sit at its right edge. The
		// inspector's own title is assistive-only: the host's drawer header
		// already names it ("Inspector"), so drawing a visible title too would
		// stack two headings above the pane.
		header := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2)
		header.Label(cfg.titleLabel(st.locale))
		header.Children(func() {
			buildPaneSelector(c, cfg, st, runtime)
			ui.Spacer(c).Grow(1)
			buildPaneAction(c, runtime, terminal, st)
			buildPaneClose(c, t, runtime, st)
		})
		// The inspector can outgrow a short rail (a long tool list); the native
		// scroll keeps the pane usable without the module owning the geometry.
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			switch st.tab {
			case tabTools:
				buildToolsPane(c, t, chat, st)
			case tabLog:
				buildLogPane(c, t, log, chat, st.locale, st)
			case tabFiles:
				buildFilesPane(c, t, workspace, artifacts, runtime, st)
			case tabChanges:
				buildChangesPane(c, t, workspace, runtime, st)
			case tabTerminal:
				buildTerminalPane(c, t, terminal, runtime, st)
			case tabAgents:
				buildAgentsPane(c, t, team, runtime, st)
			case tabTasks:
				buildTasksPane(c, t, board, runtime, st)
			default:
				buildOverviewPane(c, t, chat, cfg, st.locale, st)
			}
		})
	})
}

// buildPaneSelector is the compact pane picker: one menu button naming the
// pane that shows, with every pane as a checked menu item. It replaces the
// eight-tab strip, whose ink already overflowed the 320 DIP inspector.
func buildPaneSelector(c *ui.Context, cfg Config, st *state, runtime contracts.UIRuntime) {
	labels := paneLabels(cfg, st.locale)
	if st.tab < 0 || st.tab >= len(labels) {
		st.tab = 0
	}
	label := uikit.T(st.locale, "切换面板", "Switch pane")
	selector := ui.PopoverMenuButton(c, labels[st.tab], func(m *ui.Menu) {
		for index, pane := range labels {
			index, pane := index, pane
			if m.Item(pane).Checked(index == st.tab).Chosen() && st.tab != index {
				st.tab = index
				runtime.Invalidate()
			}
		}
	})
	selector.Label(label).Tooltip(label)
}

// inspectorToggleCommandID is the host command that opens or closes the docked
// inspector. The module's own close control runs it, so the header's right edge
// has the close affordance in both the docked and the drawer presentation.
const inspectorToggleCommandID = "uiroot.toggle-inspector"

// buildPaneClose is the inspector's close control: it runs the host's
// toggle-inspector command when the host registered it (the command is the one
// source for the open state). Hosts without the command simply have no close
// control here; the window chrome remains the entry.
func buildPaneClose(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, st *state) {
	// In the drawer presentation the host already draws the panel's header with
	// its own close control, so a second X in the module's header row would be
	// redundant. The drawer is used when the window cannot dock the inspector;
	// the same geometry the host uses tells this module which presentation it is
	// in (expanded sidebar worst case).
	if width, _ := c.Size(); width > 0 && width < design.SidebarWidth+design.ContentMinWidth+design.Space6+design.InspectorMinWidth {
		return
	}
	var run func() error
	for _, command := range runtime.Commands() {
		if command.ID == inspectorToggleCommandID && command.Run != nil {
			run = command.Run
			break
		}
	}
	if run == nil {
		return
	}
	label := st.tr("关闭检查器", "Close inspector")
	close := ui.ButtonBase(c).Size(24, 24).Center().Radius(design.RadiusSM).
		Label(label).Tooltip(label)
	close = rowTint(t, close, false)
	close.Children(func() {
		ui.Icon(c, icons.Close()).Size(14, 14).TextColor(t.LabelSecondary)
	})
	if close.Clicked() {
		_ = run()
		runtime.Invalidate()
	}
}

// buildPaneAction is the one pane-level action of the active pane, at the
// pane header's right edge. Panes without a page action keep it empty.
func buildPaneAction(c *ui.Context, runtime contracts.UIRuntime, terminal contracts.TerminalService, st *state) {
	switch st.tab {
	case tabFiles:
		if ui.Button(c, st.tr("刷新", "Refresh")).
			Tooltip(st.tr("重新读取文件列表", "Reload the file list")).Clicked() {
			st.filesPreview = make(map[string]*filePreview)
			st.filesErr = ""
			st.filesLoaded = false // re-read the listing once, not every frame
			runtime.Invalidate()
		}
	case tabChanges:
		if ui.Button(c, st.tr("刷新", "Refresh")).
			Tooltip(st.tr("重新读取工作区变更", "Reload the workspace changes")).Clicked() {
			st.changesLoaded = false
			st.changesErr = nil
			runtime.Invalidate()
		}
	case tabTasks:
		if ui.Button(c, st.tr("刷新", "Refresh")).
			Tooltip(st.tr("重新加载任务", "Reload tasks")).Clicked() {
			st.tasksLoaded = false
			st.tasksErr = ""
			runtime.Invalidate()
		}
	case tabTerminal:
		if ui.Button(c, st.tr("清空", "Clear")).Clicked() {
			if st.term != nil {
				st.term.clearOutput()
			}
			runtime.Invalidate()
		}
	}
}

// buildOverviewPane shows the conversation's user-facing overview: title, run
// state, message count, update time and the model in use. The developer
// metadata (session ID, endpoint, API-key state, budget, iteration cap) lives in
// a collapsed "Diagnostics" disclosure so it is one click away without being the
// first thing the inspector shows.
func buildOverviewPane(c *ui.Context, t *design.Tokens, chat contracts.ChatService, cfg Config,
	locale contracts.LocaleService, st *state) {
	activeID := chat.ActiveSession()
	chatSession, ok := chat.Session(activeID)
	if activeID == "" || !ok {
		buildEmptyPane(c, cfg.emptyLabel(locale))
		return
	}
	title := strings.TrimSpace(chatSession.Title)
	if title == "" {
		title = uikit.T(locale, "未命名会话", "Untitled session")
	}
	effective := chat.Config()
	model := firstNonEmpty(strings.TrimSpace(effective.Model), uikit.T(locale, "未配置", "Not configured"))
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, title), t.Font(design.TypeM)).
			FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		if chatSession.Running || chatSession.Error != "" {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				if chatSession.Running {
					design.Apply(ui.Badge(c, uikit.T(locale, "运行中", "Running")), t.Badge()).TextColor(t.WarningText)
				}
				if chatSession.Error != "" {
					design.Apply(ui.Badge(c, uikit.T(locale, "错误", "Error")), t.Badge()).TextColor(t.DangerText)
				}
			})
		}
		uikit.KeyValueRows(c, []uikit.KeyValue{
			{Key: uikit.T(locale, "消息", "Messages"), Value: fmt.Sprintf("%d", len(chatSession.Messages))},
			{Key: uikit.T(locale, "更新时间", "Updated"), Value: formatUpdatedAt(locale, chatSession.UpdatedAt)},
			{Key: uikit.T(locale, "模型", "Model"), Value: model},
		})
		if chatSession.Error != "" {
			uikit.ErrorLine(c, chatSession.Error)
		}
		// Diagnostics: the technical metadata the overview used to put on the
		// first screen, collapsed by default.
		open := st.open["overview/diagnostics"]
		ui.Collapsible(c, uikit.T(locale, "诊断信息", "Diagnostics"), &open, func() {
			diag := []uikit.KeyValue{
				{Key: uikit.T(locale, "会话 ID", "Session ID"), Value: firstNonEmpty(chatSession.ID, "—")},
			}
			if baseURL := strings.TrimSpace(effective.BaseURL); baseURL != "" {
				diag = append(diag, uikit.KeyValue{Key: "Base URL", Value: baseURL})
			}
			apiKey := uikit.T(locale, "未设置", "Not set")
			if strings.TrimSpace(effective.APIKey) != "" {
				apiKey = uikit.T(locale, "已设置（已隐藏）", "Set (hidden)")
			}
			diag = append(diag,
				uikit.KeyValue{Key: "API Key", Value: apiKey},
				uikit.KeyValue{Key: uikit.T(locale, "上下文预算", "Context budget"),
					Value: firstNonEmpty(intLabel(effective.ContextBudget), uikit.T(locale, "默认", "Default"))},
				uikit.KeyValue{Key: uikit.T(locale, "最大迭代", "Max iterations"),
					Value: firstNonEmpty(intLabel(effective.MaxIterations), uikit.T(locale, "默认", "Default"))},
			)
			uikit.KeyValueRows(c, diag)
		})
		st.open["overview/diagnostics"] = open
	})
}

// intLabel renders a positive integer or "" for zero.
func intLabel(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

// buildToolsPane aggregates every ToolCall of the session, in message order,
// in the official native list: one row per call with a status badge, the name
// and a collapsible summary. Up/Down choose a row and Enter toggles its
// summary, so the pane is usable without the pointer (docs/ui/list.md).
func buildToolsPane(c *ui.Context, t *design.Tokens, chat contracts.ChatService, st *state) {
	chatSession, ok := chat.Session(chat.ActiveSession())
	if !ok {
		buildEmptyPane(c, uikit.T(st.locale, "还没有工具调用", "No tool calls yet"))
		return
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
		buildEmptyPane(c, uikit.T(st.locale, "还没有工具调用", "No tool calls yet"))
		return
	}

	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, uikit.T(st.locale,
			fmt.Sprintf("%d 个工具调用", len(items)), fmt.Sprintf("%d tool calls", len(items)))),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		st.toolsList.Key = func(row int) any { return items[row].key }
		st.toolsList.Label = func(row int) string { return items[row].tool.Name }
		st.toolsList.Selected = &st.toolsSel
		list := ui.List(c, &st.toolsList, len(items)).
			Label(uikit.T(st.locale, "工具调用列表", "Tool calls")).
			Gap(design.Space1).
			Rows(func(row ui.ListRow) {
				buildToolRow(row, t, st, items[row.Index])
			})
		// Enter toggles the chosen call's summary; the arrow keys already chose
		// the row through the list's own Selected handling.
		if list.Submitted() && st.toolsSel >= 0 && st.toolsSel < len(items) {
			key := items[st.toolsSel].key
			st.open[key] = !st.open[key]
			runtimeInvalidate(st)
		}
	})
}

// toolItem is one row of the Tools pane: the tool call and its stable key.
type toolItem struct {
	key  string
	tool contracts.ToolCall
}

// buildToolRow is one tool call row: status badge, name, id and the foldable
// summary.
func buildToolRow(row ui.ListRow, t *design.Tokens, st *state, item toolItem) {
	c := row.Context
	tool := item.tool
	status := statusLabel(tool.Status, st.locale)
	color := statusColor(t, tool.Status)
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
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
			ui.Collapsible(c, uikit.T(st.locale, "摘要", "Summary"), &open, func() {
				design.Apply(ui.Text(c, truncateSummary(summary)), t.TextMuted(design.TypeXXS))
			})
			st.open[item.key] = open
		}
	})
}

// runtimeInvalidate requests one more frame after a list action changed state.
func runtimeInvalidate(st *state) {
	if st.runtime != nil {
		st.runtime.Invalidate()
	}
}

// rowTint paints a self-drawn ButtonBase control's interaction states: selected
// and pressed use the stronger selection tint, hover the lighter hover tint.
// The inspector's lists are official ui.List widgets, which paint their own row
// states; this remains only for the standalone bare buttons (the close control,
// the artifact title toggle and the task overflow button) that have no native
// look and must draw their own hover/pressed feedback.
func rowTint(t *design.Tokens, el ui.Element, selected bool) ui.Element {
	switch {
	case el.Pressed():
		return el.Background(t.BgSelected)
	case selected:
		return el.Background(t.BgSelected)
	case el.Hovered():
		return el.Background(t.BgHover)
	default:
		return el
	}
}

// buildEmptyPane is the centered empty state of one pane: a title and hint in
// flow, without the decorative icon-in-a-colored-circle anti-pattern.
func buildEmptyPane(c *ui.Context, text string) {
	uikit.EmptyStateBlock(c, text, "", nil)
}

// statusLabel maps a tool status to its inspector label.
func statusLabel(status string, locale contracts.LocaleService) string {
	switch normalizeStatus(status) {
	case "running":
		return uikit.T(locale, "运行中", "Running")
	case "ok":
		return uikit.T(locale, "完成", "Completed")
	case "error":
		return uikit.T(locale, "失败", "Failed")
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return uikit.T(locale, "未知", "Unknown")
	}
}

// statusColor maps a tool status to its brand text color token.
func statusColor(t *design.Tokens, status string) ui.Color {
	switch normalizeStatus(status) {
	case "running":
		return t.WarningText
	case "ok":
		return t.SuccessText
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

// formatUpdatedAt renders a session update timestamp in the user's date
// format, "—" when unset. It goes through the shared locale helper.
func formatUpdatedAt(locale contracts.LocaleService, updated time.Time) string {
	return firstNonEmpty(uikit.FormatDateTime(locale, updated), "—")
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

// buildLogPane is the inspector's durable session log: the newest events of the
// active session, read from the SQLite-backed session log once per session (or
// when the chat reports new events) and shown in the official native list. It
// degrades to an explanatory empty state without a store.
func buildLogPane(c *ui.Context, t *design.Tokens, log eventReader, chat contracts.ChatService,
	locale contracts.LocaleService, st *state) {
	active := chat.ActiveSession()
	if active == "" {
		buildEmptyPane(c, uikit.T(locale, "还没有活动会话", "No active session"))
		return
	}
	if log == nil {
		buildEmptyPane(c, uikit.T(locale, "日志需要持久化会话存储", "The log requires a persistent session store"))
		return
	}
	// The events are cached: only a session change or a chat notification drops
	// the cache, so rendering never queries the store every frame (P1).
	st.loadLogOnce(log, active)
	if st.logErr != nil {
		uikit.ErrorLine(c, uikit.T(locale, "读取会话日志失败：", "Failed to read the session log: ")+st.logErr.Error())
		return
	}
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, uikit.T(locale,
			fmt.Sprintf("%d 条会话事件", len(st.logEvents)), fmt.Sprintf("%d session events", len(st.logEvents)))),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		if len(st.logEvents) == 0 {
			buildEmptyPane(c, uikit.T(locale, "还没有会话事件", "No session events yet"))
			return
		}
		events := st.logEvents
		st.logList.Key = func(row int) any { return events[len(events)-1-row].Seq }
		st.logList.Label = func(row int) string { return events[len(events)-1-row].Type }
		ui.List(c, &st.logList, len(events)).
			Label(uikit.T(locale, "会话事件", "Session events")).
			Gap(design.Space1).
			Rows(func(row ui.ListRow) {
				buildLogRow(row, t, locale, events[len(events)-1-row.Index])
			})
	})
}

// buildLogRow is one durable event row: sequence, type, clock and a bounded
// payload preview.
func buildLogRow(row ui.ListRow, t *design.Tokens, locale contracts.LocaleService, event rig.SessionEvent) {
	c := row.Context
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, fmt.Sprintf("#%d", event.Seq)), t.TextMuted(design.TypeXXXS))
			design.Apply(ui.Text(c, event.Type), t.Font(design.TypeXXS)).FontWeight(600).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Text(c, uikit.FormatClock(locale, event.Time)), t.TextMuted(design.TypeXXXS))
		})
		if payload := strings.TrimSpace(string(event.Payload)); payload != "" {
			design.Apply(ui.Text(c, truncateRunes(payload, 160)), t.MonoFamily()).
				FontSize(11).TextColor(t.LabelTertiary).MaxLines(3)
		}
	})
}

// loadLogOnce fills the Log pane's event cache for one session: the durable
// store is read only when the session changed or the cache was dropped (the
// chat subscription drops it when new events may exist), never from plain
// rendering (P1).
func (st *state) loadLogOnce(log eventReader, sessionID string) {
	if st.logLoaded && st.logSession == sessionID {
		return
	}
	st.logEvents, st.logErr = log.Last(context.Background(), sessionID, logLimit)
	st.logLoaded = true
	st.logSession = sessionID
}

// truncateRunes cuts s to limit runes, appending an ellipsis when it was cut.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
