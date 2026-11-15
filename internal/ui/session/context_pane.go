package session

// The Context tab: the session's context inspector (DSH dsh-context P0).
//
// It renders the backend's folded read model (contracts.ContextInspector):
// current used/window, the most recent request snapshot (system prompt, tool
// catalog, history, prompt injections), the per-model-call token/cost table and
// the compaction/spill audit events. The pane never invents numbers: a session
// without recorded data shows an empty state, an unknown window stays unshown,
// and cost columns/totals only appear when the backend says the pricing is
// known. The view is cached per active session and re-read on tab entry, a
// conversation switch or a chat change, deduplicated by Revision.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// refreshSessionContext reads the active session's folded context view once.
// The inspector has no change subscription, so callers mark the state dirty on
// tab entry / session switch / chat change and this read happens at most once
// per change; Revision dedupes a re-read that folded nothing new.
func (st *state) refreshSessionContext(sessionID string) {
	if st == nil || st.contextService == nil {
		return
	}
	st.contextDirty = false
	if sessionID == "" {
		st.contextSessionID = ""
		st.contextRevision = 0
		st.contextData = contracts.SessionContext{}
		st.contextOK = false
		st.contextLoaded = true
		return
	}
	data, ok := st.contextService.SessionContext(sessionID)
	if !ok {
		// A cold session with no recorded context data: an honest empty state
		// instead of zeros.
		if st.contextSessionID != sessionID {
			st.contextRequests = ui.ListState{}
			st.contextSections = ui.ListState{}
			st.contextEvents = ui.ListState{}
		}
		st.contextSessionID = sessionID
		st.contextRevision = 0
		st.contextData = contracts.SessionContext{}
		st.contextOK = false
		st.contextLoaded = true
		return
	}
	if st.contextOK && st.contextSessionID == sessionID && data.Revision == st.contextRevision {
		return
	}
	if st.contextSessionID != sessionID {
		// A new conversation starts at its own top.
		st.contextRequests = ui.ListState{}
		st.contextSections = ui.ListState{}
		st.contextEvents = ui.ListState{}
	}
	st.contextSessionID = sessionID
	st.contextRevision = data.Revision
	st.contextData = data
	st.contextOK = true
	st.contextLoaded = true
}

// The Context tables' column measures. design has no table-column scale yet, so
// the widths are named here, in one place, instead of bare numbers repeated at
// every column: the numeric columns hold counts/bytes/costs right aligned, the
// text columns take what is left.
const (
	// contextNumColWidth is one numeric column: tokens, bytes, cache, cost.
	contextNumColWidth float32 = 76
	// contextTimeColWidth holds a compact HH:MM clock.
	contextTimeColWidth float32 = 56
	// contextIterColWidth holds the iteration ordinal.
	contextIterColWidth float32 = 48
	// contextPromptColWidth holds the prompt token count.
	contextPromptColWidth float32 = 88
	// contextSourceColWidth holds a prompt-section source name.
	contextSourceColWidth float32 = 92
	// contextModelMinWidth/contextModelMaxWidth bound the model-name column so a
	// long model id ellipsizes instead of squeezing the numeric columns.
	contextModelMinWidth float32 = 120
	contextModelMaxWidth float32 = 280
)

// contextArchiveView is the Context pane's archived-history viewer state. It is
// ui.Local state owned by the pane, not a module field: the pane never stores an
// element, and the viewer only exists while this pane does. sessionID/seq are the
// read currently shown, so a stale read that lands after another archive was
// opened is dropped instead of opening the wrong history.
type contextArchiveView struct {
	open      bool
	sessionID string
	seq       int64
	loading   bool
	loaded    bool
	ok        bool
	archive   contracts.ContextArchive
	list      ui.ListState
}

// Context page header actions: the settings page/anchor the pane talks about.
const (
	contextSettingsPageID    = "model"
	contextSettingsAnchorKey = "model.context-budget"
)

// buildContextPane renders the Context tab's body on the reading measure.
func buildContextPane(c *ui.Context, t *design.Tokens, st *state, chatSession contracts.ChatSession, hasSession bool) {
	pane := ui.Column(c).FillWidth().Grow(1)
	archiveView := ui.Local(pane, "session.context.archive", func() contextArchiveView {
		return contextArchiveView{}
	})
	if archiveView != nil && archiveView.sessionID != "" &&
		archiveView.sessionID != st.contextSessionID {
		// The open archive belonged to another conversation: a session switch
		// starts a fresh viewer instead of showing another session's history.
		*archiveView = contextArchiveView{}
	}
	pane.Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			// The body builds on the shared reading column (960 DIP centered,
			// capped at 92% of the pane, never grown), so its text column is the
			// transcript's and the composer dock's. The pane used to add its own
			// Grow(1), which widened the column ~13% and moved its left edge
			// (~64 DIP against the conversation), so switching tabs jumped the
			// text column.
			uikit.ReadingColumn(c, func(body *ui.Context) {
				ui.Column(body).FillWidth().Gap(design.Space5).
					PaddingY(design.Space5).Children(func() {
					buildContextHeader(c, t, st, hasSession)
					switch {
					case !hasSession:
						uikit.EmptyStateBlock(c,
							st.tr("还没有会话", "No conversation yet"),
							st.tr("打开或新建会话后，这里会显示它的上下文数据。",
								"Open or start a conversation and its context data shows here."), nil)
					case st.contextService == nil:
						// The tab is hidden without the backend; this is the
						// defensive branch for a stripped host.
						uikit.EmptyStateBlock(c,
							st.tr("上下文查看器不可用", "Context inspector is not available"),
							st.tr("当前后端没有提供上下文观测数据。", "This backend provides no context observability data."), nil)
					case !st.contextLoaded:
						uikit.EmptyState(c, st.tr("正在读取上下文…", "Reading the context…"))
					case !st.contextOK || contextDataEmpty(st.contextData):
						uikit.EmptyStateBlock(c,
							st.tr("本会话还没有上下文数据", "No context data for this conversation yet"),
							st.tr("运行一轮对话后，这里会显示请求快照、模型调用明细与压缩/溢出事件。",
								"Run one turn and the request snapshot, model-call details, and compaction/spill events show here."), nil)
					default:
						data := st.contextData
						buildContextOverview(c, t, st, data)
						buildContextSections(c, t, st, data.Snapshot)
						buildContextRequests(c, t, st, data)
						buildContextEvents(c, t, st, data, archiveView)
					}
				})
			})
		})
		if archiveView != nil && archiveView.open {
			buildContextArchiveModal(c, t, st, archiveView)
		}
	})
}

// buildContextHeader is the tab's page header: the page title and its two
// page-level actions (compact the context now, open the setting that sizes it).
// The header is the one home of the pane's actions (DESIGN.md §5: page actions
// at the page header, never floating in the body).
func buildContextHeader(c *ui.Context, t *design.Tokens, st *state, hasSession bool) {
	uikit.PageHeaderAction(c, st.tr("上下文", "Context"), "", func(actions *ui.Context) {
		ui.Row(actions).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			buildContextCompactAction(actions, t, st, hasSession)
			buildContextSettingsAction(actions, t, st)
		})
	})
}

// contextChatService returns the chat service the pane's actions use (the
// compaction command is its optional contracts.SessionCompactor half). The
// session module requires contracts.KeyChat, so the lookup cannot miss in a
// working assembly; a stripped test host simply gets no action.
func contextChatService(st *state) contracts.ChatService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyChat)
	if !ok || raw == nil {
		return nil
	}
	chat, _ := raw.(contracts.ChatService)
	return chat
}

// buildContextCompactAction draws the page's compact action. It only appears
// when the backend implements contracts.SessionCompactor; it is disabled while
// the session runs or a compaction is in flight, and the reason lives on the
// wrapper (a disabled control shows no tip of its own, tooltip.md).
func buildContextCompactAction(c *ui.Context, t *design.Tokens, st *state, hasSession bool) {
	if !hasSession {
		return
	}
	chat := contextChatService(st)
	if _, ok := chat.(contracts.SessionCompactor); !ok {
		return
	}
	label := st.tr("压缩上下文", "Compact context")
	if compactCommandEnabled(chat, st) {
		button := design.Apply(ui.Button(c, label), t.SecondaryButton())
		if button.Clicked() {
			runCompactCommand(chat, st, st.runtime)
			c.Invalidate()
		}
		return
	}
	reason := contextCompactDisabledReason(st, chat)
	if reason == "" {
		return
	}
	// The action is disabled and explains why. A disabled element shows no tip of
	// its own (mygo tooltip.md), so the tip lives on the wrapper the pointer
	// hovers; Description carries the same reason to assistive technology.
	ui.Row(c).Tooltip(reason).Children(func() {
		design.Apply(ui.Button(c, label), t.SecondaryButton()).Disabled(true).
			Description(reason)
	})
}

// contextCompactDisabledReason explains why the compact action cannot run now.
func contextCompactDisabledReason(st *state, chat contracts.ChatService) string {
	if st != nil && st.compacting {
		return st.tr("正在压缩上下文…", "Compacting the context…")
	}
	if chat == nil {
		return ""
	}
	sessionID := chat.ActiveSession()
	if session, ok := chat.Session(sessionID); ok && session.Running {
		return st.tr("会话正在运行，结束后才能压缩上下文",
			"The session is running; compact the context when it settles")
	}
	if session, ok := chat.Session(sessionID); !ok || len(session.Messages) == 0 {
		return st.tr("还没有可压缩的内容", "There is nothing to compact yet")
	}
	return ""
}

// buildContextSettingsAction draws the deep link to the setting that sizes the
// context: the model page's context-budget field. The host opens the page and
// reveals the field's anchor when it supports the anchored deep link.
func buildContextSettingsAction(c *ui.Context, t *design.Tokens, st *state) {
	label := st.tr("上下文设置", "Context settings")
	button := design.Apply(ui.Button(c, label), t.SecondaryButton())
	if button.Clicked() {
		openSettingsPageAt(st.navigator, contextSettingsPageID, contextSettingsAnchorKey)
		c.Invalidate()
	}
}

// openSettingsPageAt opens a settings page and reveals one searchable item's
// anchor. The anchored deep link is an optional host capability (navigator
// .OpenSettingsPageAt, the same locate path as a navigation-search hit); a host
// without it falls back to the existing page-level deep link, so the action still
// opens the right page (it just cannot scroll to the field).
func openSettingsPageAt(navigator contracts.Navigator, pageID, anchorKey string) {
	if navigator == nil {
		return
	}
	if opener, ok := navigator.(interface{ OpenSettingsPageAt(string, string) error }); ok {
		if err := opener.OpenSettingsPageAt(pageID, anchorKey); err == nil {
			return
		}
	}
	openSettingsPage(navigator, pageID)
}

// contextDataEmpty reports a fold with nothing recorded. It guards against a
// backend that answers ok=true for a cold session: the UI must still show the
// empty state instead of a fake "used 0 tokens" overview.
func contextDataEmpty(data contracts.SessionContext) bool {
	return data.Snapshot.At.IsZero() && len(data.Requests) == 0 && len(data.Events) == 0 && data.Used == 0
}

// buildContextOverview draws the used/window anchors, the optional total cost
// and the most recent request snapshot.
func buildContextOverview(c *ui.Context, t *design.Tokens, st *state, data contracts.SessionContext) {
	uikit.Section(c, st.tr("概览", "Overview"), func(body *ui.Context) {
		rows := []uikit.PropertyRow{
			{Label: st.tr("上下文用量", "Context used"),
				Value: contextUsageText(st, data.Used, data.Window)},
		}
		if data.CostKnown {
			rows = append(rows, uikit.PropertyRow{
				Label: st.tr("累计成本", "Total cost"), Value: formatContextUSD(data.TotalCostUSD),
			})
		}
		// The overview is a property sheet: one 160 DIP label column and a
		// bounded, left-aligned value, not a label/value pair stretched across
		// the whole reading column.
		uikit.PropertySheet(body, uikit.PropertySheetOptions{
			Rows: rows, Key: "context.overview",
		})

		snapshot := data.Snapshot
		if snapshot.At.IsZero() {
			design.Apply(ui.Text(body, st.tr("本会话还没有请求快照。",
				"No request snapshot for this conversation yet.")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			return
		}
		ui.Column(body).FillWidth().Gap(design.Space2).Margin(design.Space2, 0, 0, 0).Children(func() {
			design.Apply(ui.Text(body, st.tr("最近一次请求", "Most recent request")),
				t.Font(design.TypeXXS)).FontWeight(design.WeightSemibold).TextColor(t.LabelSecondary)
			history := snapshot.History
			snapshotRows := []uikit.PropertyRow{
				{Label: st.tr("时间", "Time"), Value: formatTimestamp(st.locale, snapshot.At)},
				{Label: st.tr("模型", "Model"), Value: firstNonEmpty(snapshot.Model, st.tr("未知", "Unknown"))},
			}
			if snapshot.Window > 0 {
				snapshotRows = append(snapshotRows, uikit.PropertyRow{
					Label: st.tr("窗口", "Window"), Value: uikit.FormatCompactCount(snapshot.Window) + " tokens",
				})
			}
			snapshotRows = append(snapshotRows,
				uikit.PropertyRow{
					Label: st.tr("系统提示", "System prompt"),
					Value: formatContextBytes(snapshot.SystemPromptBytes) + " · " +
						uikit.FormatCompactCount(snapshot.SystemPromptTokens) + " tokens",
				},
				uikit.PropertyRow{
					Label: st.tr("估算输入", "Estimated prompt"),
					Value: uikit.FormatCompactCount(snapshot.EstimatedPrompt) + " tokens",
				},
				uikit.PropertyRow{
					Label: st.tr("历史消息", "History"),
					Value: fmt.Sprintf(st.tr("%d 条（用户 %d / 助手 %d / 工具 %d）",
						"%d messages (user %d / assistant %d / tool %d)"),
						history.Messages, history.User, history.Assistant, history.Tool),
				},
			)
			uikit.PropertySheet(body, uikit.PropertySheetOptions{
				Rows: snapshotRows, Key: "context.snapshot",
			})
			buildContextToolsRow(body, t, st, snapshot.Tools)
			buildContextInjectedRow(body, t, st, snapshot.Injected)
		})
	})
}

// contextToolNameLimit bounds the tool-name preview in the snapshot summary;
// the full catalog stays in the tooltip.
const contextToolNameLimit = 8

// buildContextToolsRow is the tool catalog summary: the count and a bounded name
// list (the full list stays in the tooltip), stacked so a long catalog can never
// squeeze the label.
func buildContextToolsRow(c *ui.Context, t *design.Tokens, st *state, tools []contracts.ContextTool) {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	summary := st.tr("未登记工具", "No tools")
	if len(names) > 0 {
		preview := names
		if len(preview) > contextToolNameLimit {
			preview = preview[:contextToolNameLimit]
		}
		summary = fmt.Sprintf(st.tr("%d 个 · %s", "%d · %s"), len(names), strings.Join(preview, ", "))
		if len(names) > len(preview) {
			summary += fmt.Sprintf(st.tr("，另 %d 个", ", %d more"), len(names)-len(preview))
		}
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		design.Apply(ui.Text(c, st.tr("工具目录", "Tool catalog")), t.TextMuted(design.TypeXS))
		design.Apply(ui.Text(c, summary), t.Font(design.TypeXS)).TextColor(t.LabelPrimary).
			Selectable().MaxLines(1).Ellipsis("…").Tooltip(strings.Join(names, ", "))
	})
}

// buildContextInjectedRow shows the optional prompt injections as one chip row;
// only the sources that were actually present appear.
func buildContextInjectedRow(c *ui.Context, t *design.Tokens, st *state, injected contracts.ContextInjected) {
	type chip struct{ zh, en string }
	var chips []chip
	if injected.AgentsMD {
		chips = append(chips, chip{"AGENTS.md", "AGENTS.md"})
	}
	if injected.Preset {
		chips = append(chips, chip{st.tr("预设", "Preset"), st.tr("预设", "Preset")})
	}
	if injected.Memory {
		chips = append(chips, chip{st.tr("记忆", "Memory"), st.tr("记忆", "Memory")})
	}
	if injected.Skills {
		chips = append(chips, chip{st.tr("技能", "Skills"), st.tr("技能", "Skills")})
	}
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		design.Apply(ui.Text(c, st.tr("注入来源", "Injected")), t.TextMuted(design.TypeXS))
		if len(chips) == 0 {
			design.Apply(ui.Text(c, st.tr("无", "None")), t.Font(design.TypeXS)).TextColor(t.LabelTertiary)
			return
		}
		for _, chip := range chips {
			design.Apply(ui.Badge(c, chip.zh), t.Badge()).TextColor(t.LabelSecondary)
		}
	})
}

// buildContextSections draws the system prompt's section breakdown (task-20):
// the rendered prompt is the concatenation of these sections, so the table and
// its sum describe the prompt size without the UI loading or showing the text.
// Older snapshots without sections hide the whole block instead of inventing
// one row of "unknown".
func buildContextSections(c *ui.Context, t *design.Tokens, st *state, snapshot contracts.ContextSnapshot) {
	sections := snapshot.Sections
	if len(sections) == 0 {
		return
	}
	uikit.Section(c, st.tr("系统提示分段", "System prompt sections"), func(body *ui.Context) {
		design.Apply(ui.Text(body, st.tr(
			"仅展示分段的元数据（大小/估算 tokens），正文不进入事件。",
			"Only section metadata is shown (size/estimated tokens); the text is not part of the event.")),
			t.TextMuted(design.TypeXS)).MaxLines(2)
		count := len(sections)
		st.contextSections.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			section := sections[index]
			if id := strings.TrimSpace(section.ID); id != "" {
				return id
			}
			return fmt.Sprintf("%d-%s-%s", index, section.Source, section.Hash)
		}
		st.contextSections.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			return contextSectionLabel(st, sections[index])
		}
		columns := []ui.TableColumn{
			{Title: st.tr("分段", "Section")},
			{Title: st.tr("来源", "Source"), Width: contextSourceColWidth},
			{Title: st.tr("字节", "Bytes"), Width: contextNumColWidth, Align: ui.End},
			{Title: "Tokens", Width: contextNumColWidth, Align: ui.End},
		}
		ui.Table(c, &st.contextSections, columns, count, func(row, col int) {
			section := sections[row]
			label := contextSectionLabel(st, section)
			source := strings.TrimSpace(section.Source)
			value := ""
			color := t.LabelSecondary
			switch col {
			case 0:
				value = label
				color = t.LabelPrimary
			case 1:
				value = firstNonEmpty(source, "—")
				color = t.LabelTertiary
			case 2:
				value = formatContextBytes(section.Bytes)
			default:
				value = uikit.FormatCompactCount(section.Tokens)
			}
			cell := design.Apply(ui.Text(c, value), t.Font(design.TypeXS)).TextColor(color).NoWrap()
			if col == 0 {
				tip := st.tr("分段 ID：", "Section ID: ") +
					firstNonEmpty(strings.TrimSpace(section.ID), "—")
				if hash := strings.TrimSpace(section.Hash); hash != "" {
					tip += st.tr("\n内容哈希：", "\nContent hash: ") + hash
				}
				cell.Tooltip(tip)
			}
		}).Label(st.tr("系统提示分段列表", "System prompt sections")).
			MaxWidth(design.TranscriptWidth).MaxHeight(design.Space6 * 7)

		// The section sums are the prompt's real size; the estimated-prompt share
		// only appears when the window is known, so no fake percentage is drawn.
		var totalBytes, totalTokens int
		for _, section := range sections {
			totalBytes += section.Bytes
			totalTokens += section.Tokens
		}
		rows := []uikit.PropertyRow{{
			Label: st.tr("分段合计", "Section total"),
			Value: formatContextBytes(totalBytes) + " · " +
				uikit.FormatCompactCount(totalTokens) + " tokens",
		}}
		if estimate, window := snapshot.EstimatedPrompt, snapshot.Window; estimate > 0 && window > 0 {
			percent := int(float64(estimate) / float64(window) * 100)
			rows = append(rows, uikit.PropertyRow{
				Label: st.tr("估算输入占比", "Estimated prompt share"),
				Value: uikit.FormatCompactCount(estimate) + " / " +
					uikit.FormatCompactCount(window) + fmt.Sprintf(" tokens (%d%%)", percent),
			})
		} else if estimate > 0 {
			rows = append(rows, uikit.PropertyRow{
				Label: st.tr("估算输入", "Estimated prompt"),
				Value: uikit.FormatCompactCount(estimate) + " tokens",
			})
		}
		uikit.PropertySheet(body, uikit.PropertySheetOptions{
			Rows: rows, Key: "context.sections",
		})
	})
}

// contextSectionLabel localizes the stable prompt-section IDs the backend
// records (identity/environment/…); an unknown id falls back to the recorded
// label and then to the id itself. The raw id stays in the row tooltip.
func contextSectionLabel(st *state, section contracts.ContextSection) string {
	id := strings.TrimSpace(section.ID)
	switch id {
	case "identity":
		return st.tr("身份", "Identity")
	case "environment":
		return st.tr("运行环境", "Environment")
	case "working-directory":
		return st.tr("工作目录", "Working directory")
	case "approval":
		return st.tr("审批策略", "Approval policy")
	case "runtime-mode":
		return st.tr("运行模式", "Runtime mode")
	case "config":
		return st.tr("配置提示词", "Configured prompt")
	case "agents-md":
		return "AGENTS.md"
	case "preset-persona":
		return st.tr("预设人设", "Preset persona")
	case "preset-prompt":
		return st.tr("预设提示词", "Preset prompt")
	case "memory":
		return st.tr("记忆", "Memory")
	case "skills":
		return st.tr("技能", "Skills")
	case "tool-guidance":
		return st.tr("工具说明", "Tool guidance")
	default:
		if label := strings.TrimSpace(section.Label); label != "" && label != id {
			return label
		}
		if id != "" {
			return id
		}
		return st.tr("未命名分段", "Unnamed section")
	}
}

// buildContextRequests draws the per-model-call table, newest first. Cost is a
// column only when the session's pricing is known; a row without a known cost
// shows an em dash rather than a fake zero.
func buildContextRequests(c *ui.Context, t *design.Tokens, st *state, data contracts.SessionContext) {
	uikit.Section(c, st.tr("模型调用", "Model calls"), func(body *ui.Context) {
		if len(data.Requests) == 0 {
			uikit.EmptyState(body, st.tr("还没有模型调用记录。", "No model calls recorded yet."))
			return
		}
		rows := data.Requests
		count := len(rows)
		columns := []ui.TableColumn{
			{Title: st.tr("轮次", "Iter"), Width: contextIterColWidth, Align: ui.End},
			{Title: st.tr("模型", "Model"), MinWidth: contextModelMinWidth, MaxWidth: contextModelMaxWidth},
			{Title: st.tr("提示词", "Prompt"), Width: contextPromptColWidth, Align: ui.End},
			{Title: st.tr("完成", "Completion"), Width: contextNumColWidth, Align: ui.End},
			{Title: st.tr("缓存", "Cached"), Width: contextNumColWidth, Align: ui.End},
			{Title: st.tr("上下文", "Context"), Width: contextNumColWidth, Align: ui.End},
			{Title: st.tr("时间", "Time"), Width: contextTimeColWidth, Align: ui.End},
		}
		if data.CostKnown {
			columns = append(columns, ui.TableColumn{
				Title: st.tr("成本", "Cost"), Width: contextNumColWidth, Align: ui.End,
			})
		}
		st.contextRequests.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			// The live fold may not stamp the durable Seq yet (it is assigned by
			// the event log), so the key also carries the call's identity: two
			// zero-seq rows must never share one state.
			request := rows[count-1-index]
			return fmt.Sprintf("%d-%d-%d", request.Seq, request.Iteration, request.At.UnixNano())
		}
		st.contextRequests.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			request := rows[count-1-index]
			return fmt.Sprintf(st.tr("第 %d 次调用，%s", "Call %d, %s"), request.Iteration, request.Model)
		}
		ui.Table(c, &st.contextRequests, columns, count, func(row, col int) {
			request := rows[count-1-row]
			value := ""
			color := t.LabelSecondary
			switch col {
			case 0:
				value = fmt.Sprintf("%d", request.Iteration)
				color = t.LabelTertiary
			case 1:
				value = firstNonEmpty(request.Model, "—")
				color = t.LabelPrimary
			case 2:
				value = uikit.FormatCompactCount(request.Prompt)
			case 3:
				value = uikit.FormatCompactCount(request.Completion)
			case 4:
				value = uikit.FormatCompactCount(request.Cached)
				color = t.LabelTertiary
			case 5:
				value = uikit.FormatCompactCount(request.ContextTokens)
			case 6:
				value = messageClock(st.locale, request.At)
				color = t.LabelTertiary
			default:
				if request.CostKnown {
					value = formatContextUSD(request.CostUSD)
				} else {
					value = "—"
					color = t.LabelTertiary
				}
			}
			design.Apply(ui.Text(c, value), t.Font(design.TypeXS)).TextColor(color).NoWrap()
		}).Label(st.tr("模型调用列表", "Model calls")).
			MaxWidth(design.TranscriptWidth).MaxHeight(design.Space6 * 9)
	})
}

// buildContextEvents draws the compact/spill audit timeline, newest first: an
// exact compaction is a plain result, a heuristic token-drop compaction is
// labelled 推断, and a spill shows its path and on-disk size.
func buildContextEvents(c *ui.Context, t *design.Tokens, st *state, data contracts.SessionContext,
	archiveView *contextArchiveView) {
	uikit.Section(c, st.tr("上下文事件", "Context events"), func(body *ui.Context) {
		if len(data.Events) == 0 {
			uikit.EmptyState(body, st.tr("还没有压缩或溢出事件。",
				"No compaction or spill events yet."))
			return
		}
		events := data.Events
		count := len(events)
		archiver, _ := st.contextService.(contracts.ContextArchiveService)
		st.contextEvents.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			event := events[count-1-index]
			return fmt.Sprintf("%d-%s-%d", event.Seq, event.Kind, event.At.UnixNano())
		}
		st.contextEvents.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			event := events[count-1-index]
			return contextEventLabel(st, event)
		}
		ui.List(c, &st.contextEvents, count).
			Label(st.tr("上下文事件列表", "Context events")).
			MaxWidth(design.TranscriptWidth).
			MaxHeight(design.Space6 * 7).
			Rows(func(row ui.ListRow) {
				index := count - 1 - row.Index
				event := events[index]
				buildContextEventRow(row.Context, t, st, event,
					contextArchiveSeq(events, index), archiveView, archiver)
			})
	})
}

// contextArchiveSeq names the durable compact_archive event a timeline row can
// open the raw history of: the archive row itself, or the compaction/recovery
// row whose archive is the event recorded immediately before it. 0 means the row
// has no archive (no action).
func contextArchiveSeq(events []contracts.ContextEvent, index int) int64 {
	if index < 0 || index >= len(events) {
		return 0
	}
	event := events[index]
	if event.Kind == contracts.ContextEventArchive {
		if event.Seq > 0 {
			return event.Seq
		}
		return 0
	}
	if event.Kind != contracts.ContextEventCompact && event.Kind != contracts.ContextEventRecovery {
		return 0
	}
	if index == 0 {
		return 0
	}
	previous := events[index-1]
	if previous.Kind == contracts.ContextEventArchive && previous.Seq > 0 {
		return previous.Seq
	}
	return 0
}

// buildContextEventRow is one audit entry: the kind badge, the detail, the
// before/after or path/bytes meta line, the time and — for a row whose archive is
// readable — the action that opens the archived raw history.
func buildContextEventRow(c *ui.Context, t *design.Tokens, st *state, event contracts.ContextEvent,
	archiveSeq int64, archiveView *contextArchiveView, archiver contracts.ContextArchiveService) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).PaddingY(design.Space1).Children(func() {
		design.Apply(ui.Badge(c, contextEventKindLabel(st, event)), t.Badge()).
			TextColor(t.LabelSecondary)
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			detail := strings.TrimSpace(event.Detail)
			switch event.Kind {
			case contracts.ContextEventCompact:
				detail = contextCompactDetail(st, event)
			case contracts.ContextEventRecovery:
				detail = contextRecoveryDetail(st, event.Detail)
			case contracts.ContextEventArchive:
				detail = contextArchiveDetail(st, event)
			}
			if detail == "" {
				detail = st.tr("上下文事件", "Context event")
			}
			detailText := design.Apply(ui.Text(c, detail), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("…")
			if event.Kind == contracts.ContextEventRecovery {
				if raw := strings.TrimSpace(event.Detail); raw != "" {
					detailText.Tooltip(st.tr("后端结果：", "Backend outcome: ") + raw)
				}
			}
			if meta := contextEventMeta(st, event); meta != "" {
				design.Apply(ui.Text(c, meta), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).NoWrap().Ellipsis("…").Tooltip(meta)
			}
		})
		if clock := messageClock(st.locale, event.At); clock != "" {
			design.Apply(ui.Text(c, clock), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).NoWrap().
				Tooltip(uikit.FormatDateTime(st.locale, event.At))
		}
		// The archive's raw history is readable only when the backend offers the
		// optional read-back and the folded row carries the durable archive
		// sequence: a row without one shows no action (never a dead button).
		if archiveSeq > 0 && archiver != nil && archiveView != nil {
			label := st.tr("查看归档", "View archive")
			button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
				Height(design.RowCompactHeight)
			if button.Clicked() {
				startContextArchiveRead(st, archiveView, archiver, st.contextSessionID, archiveSeq)
				c.Invalidate()
			}
		}
	})
}

// contextEventKindLabel is the audit row's badge: the event class, the
// heuristic marker for an inferred compaction, and the recovery outcome.
func contextEventKindLabel(st *state, event contracts.ContextEvent) string {
	switch event.Kind {
	case contracts.ContextEventSpill:
		return st.tr("溢出", "Spill")
	case contracts.ContextEventRecovery:
		return contextRecoveryKindLabel(st, event.Detail)
	case contracts.ContextEventArchive:
		if event.Truncated {
			return st.tr("归档（截断）", "Archive (truncated)")
		}
		return st.tr("归档", "Archive")
	default:
		return contextCompactKindLabel(st, event)
	}
}

// contextCompactKindLabel names a compaction event: an inferred legacy drop,
// a manual /compact result, an exact automatic compaction, or a skipped/failed
// automatic attempt (Outcome). A failed attempt is never labelled as a
// successful compaction.
func contextCompactKindLabel(st *state, event contracts.ContextEvent) string {
	if event.Inferred {
		return st.tr("压缩（推断）", "Compaction (inferred)")
	}
	if contextCompactIsManual(event.Detail) {
		return st.tr("压缩", "Compaction")
	}
	if outcome := strings.TrimSpace(event.Outcome); outcome != "" {
		return st.tr("自动压缩跳过/失败：", "Auto-compaction skipped/failed: ") +
			contextCompactOutcomeLabel(st, outcome)
	}
	return st.tr("自动压缩", "Auto-compaction")
}

// contextCompactIsManual reports whether the durable compact event came from
// the user's /compact command (Detail "manual"; legacy events may carry the
// localized 手动 copy).
func contextCompactIsManual(detail string) bool {
	detail = strings.ToLower(strings.TrimSpace(detail))
	return detail == "manual" || strings.Contains(detail, "手动")
}

// contextCompactOutcomeLabel localizes the product-side automatic compaction
// outcome; an unknown outcome falls back to its raw value (never invented).
func contextCompactOutcomeLabel(st *state, outcome string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "failed":
		return st.tr("压缩失败", "compaction failed")
	case "no-reduction":
		return st.tr("没有可减少的内容", "no usable reduction")
	case "archive-failed":
		return st.tr("归档原文失败", "archiving the raw history failed")
	case "replace-failed":
		return st.tr("替换历史失败", "replacing the history failed")
	default:
		return strings.TrimSpace(outcome)
	}
}

// contextCompactOutcomeDetail explains one skipped/failed automatic compaction:
// the history is unchanged, so the copy never claims it was compacted.
func contextCompactOutcomeDetail(st *state, outcome string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "failed":
		return st.tr("自动压缩未能完成，历史保持不变。",
			"The automatic compaction failed; the history is unchanged.")
	case "no-reduction":
		return st.tr("自动压缩没有减少内容，历史保持不变。",
			"The automatic compaction reduced nothing; the history is unchanged.")
	case "archive-failed":
		return st.tr("归档压缩前的原文失败，历史保持不变。",
			"Archiving the raw history failed; the history is unchanged.")
	case "replace-failed":
		return st.tr("替换压缩后的历史失败，历史保持不变。",
			"Replacing the history failed; the history is unchanged.")
	default:
		return st.tr("自动压缩未完成，历史保持不变：",
			"The automatic compaction did not complete; the history is unchanged: ") +
			strings.TrimSpace(outcome)
	}
}

// contextCompactDetail is the compaction row's explanation: manual/automatic
// provenance for a successful event, and the localized reason for a
// skipped/failed one (Detail carries the raw backend key).
func contextCompactDetail(st *state, event contracts.ContextEvent) string {
	if event.Inferred {
		return strings.TrimSpace(event.Detail)
	}
	if contextCompactIsManual(event.Detail) {
		return st.tr("手动压缩", "Manual compaction")
	}
	if outcome := strings.TrimSpace(event.Outcome); outcome != "" {
		return contextCompactOutcomeDetail(st, outcome)
	}
	switch strings.ToLower(strings.TrimSpace(event.Detail)) {
	case "", "auto":
		return st.tr("运行前自动压缩", "Automatic pre-run compaction")
	default:
		return strings.TrimSpace(event.Detail)
	}
}

// contextRecoveryKindLabel names an automatic overflow recovery and its outcome
// (the backend records the outcome in Detail; success is the plain kind).
func contextRecoveryKindLabel(st *state, detail string) string {
	lower := strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.HasSuffix(lower, ":compact-failed"):
		return st.tr("自动压缩重试（压缩失败）", "Recovery retry (compaction failed)")
	case strings.HasSuffix(lower, ":failed"):
		return st.tr("自动压缩重试（失败）", "Recovery retry (failed)")
	case strings.Contains(lower, "recovery"):
		return st.tr("自动压缩重试（成功）", "Recovery retry (succeeded)")
	default:
		return st.tr("自动压缩重试", "Recovery retry")
	}
}

// contextRecoveryDetail is the recovery row's explanation, localized from the
// backend outcome instead of showing the raw event kind.
func contextRecoveryDetail(st *state, detail string) string {
	lower := strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.HasSuffix(lower, ":compact-failed"):
		return st.tr("自动压缩失败，本回合没有重试。",
			"The automatic compaction failed; the turn was not retried.")
	case strings.HasSuffix(lower, ":failed"):
		return st.tr("自动压缩后重试本回合失败。",
			"The turn was retried after compaction and failed again.")
	case strings.Contains(lower, "recovery"):
		return st.tr("上下文溢出：自动压缩后重试了本回合。",
			"Context overflow: the turn was retried after an automatic compaction.")
	default:
		return ""
	}
}

// contextArchiveDetail explains a compact_archive event from its metadata; the
// archived text itself is never part of the read model, so the row describes
// only what was kept (and what the bound omitted).
func contextArchiveDetail(st *state, event contracts.ContextEvent) string {
	count := event.ArchivedMessages
	omitted := contextArchiveOmitted(event.Detail)
	switch {
	case event.Truncated && omitted > 0:
		return fmt.Sprintf(st.tr(
			"归档了 %d 条消息；另 %d 条超出上限未保留（截断）。",
			"Archived %d messages; %d more exceeded the bound and were not kept (truncated)."),
			count, omitted)
	case event.Truncated:
		return fmt.Sprintf(st.tr("归档了 %d 条消息（已截断）。",
			"Archived %d messages (truncated)."), count)
	default:
		return fmt.Sprintf(st.tr(
			"归档了 %d 条消息；原始正文另行持久化，不在事件中。",
			"Archived %d messages; the raw text is stored separately, not in this event."), count)
	}
}

// contextArchiveOmitted extracts the omitted-message count from the backend's
// "manual archive: N messages archived, M omitted (truncated)" detail.
func contextArchiveOmitted(detail string) int {
	lower := strings.ToLower(detail)
	index := strings.Index(lower, " omitted")
	if index < 0 {
		return 0
	}
	end := index
	start := end
	for start > 0 && detail[start-1] >= '0' && detail[start-1] <= '9' {
		start--
	}
	if start == end {
		return 0
	}
	count, err := strconv.Atoi(detail[start:end])
	if err != nil || count < 0 {
		return 0
	}
	return count
}

// contextEventLabel is the accessible row name of one audit event.
func contextEventLabel(st *state, event contracts.ContextEvent) string {
	kind := contextEventKindLabel(st, event)
	switch event.Kind {
	case contracts.ContextEventCompact:
		if detail := contextCompactDetail(st, event); detail != "" {
			return kind + "：" + detail
		}
		return kind
	case contracts.ContextEventRecovery:
		if detail := contextRecoveryDetail(st, event.Detail); detail != "" {
			return kind + "：" + detail
		}
		return kind
	case contracts.ContextEventArchive:
		return kind + "：" + contextArchiveDetail(st, event)
	}
	if detail := strings.TrimSpace(event.Detail); detail != "" {
		return kind + "：" + detail
	}
	return kind
}

// contextEventMeta is the second, muted line of an audit row.
func contextEventMeta(st *state, event contracts.ContextEvent) string {
	if event.Kind == contracts.ContextEventSpill {
		path := strings.TrimSpace(event.Path)
		if path == "" {
			return formatContextBytes(event.Bytes)
		}
		return path + " · " + formatContextBytes(event.Bytes)
	}
	if event.Kind == contracts.ContextEventArchive {
		parts := make([]string, 0, 2)
		if event.ArchivedBytes > 0 {
			parts = append(parts, formatContextBytes(event.ArchivedBytes))
		}
		if event.Before > 0 || event.After > 0 {
			parts = append(parts, uikit.FormatCompactCount(event.Before)+" → "+
				uikit.FormatCompactCount(event.After)+" tokens")
		}
		return strings.Join(parts, " · ")
	}
	parts := make([]string, 0, 2)
	if event.Before > 0 || event.After > 0 {
		parts = append(parts, uikit.FormatCompactCount(event.Before)+" → "+
			uikit.FormatCompactCount(event.After)+" tokens")
	}
	// A recovery's Saved field counts the retry, not replaced messages, and a
	// skipped/failed compaction leaves the history untouched (the payload still
	// carries counts), so only a real successful compaction shows the message
	// delta.
	if event.Kind == contracts.ContextEventCompact && strings.TrimSpace(event.Outcome) == "" &&
		(event.Replaced > 0 || event.Saved > 0) {
		parts = append(parts, fmt.Sprintf(st.tr("消息 %d → %d", "messages %d → %d"),
			event.Replaced, event.Saved))
	}
	return strings.Join(parts, " · ")
}

// contextUsageText renders the used/window anchors without inventing a window.
func contextUsageText(st *state, used, window int) string {
	if window <= 0 {
		return st.tr("已用 ", "Used ") + uikit.FormatCompactCount(used) + " tokens"
	}
	percent := int(float64(used) / float64(window) * 100)
	return uikit.FormatCompactCount(used) + " / " + uikit.FormatCompactCount(window) +
		" tokens" + fmt.Sprintf(" (%d%%)", percent)
}

// formatContextBytes renders a byte count for the snapshot rows ("12.3 KB").
func formatContextBytes(bytes int) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// formatContextUSD renders an estimated price without fake precision: cents
// when the amount is meaningful, four decimals for sub-cent calls.
func formatContextUSD(amount float64) string {
	switch {
	case amount >= 1:
		return fmt.Sprintf("$%.2f", amount)
	case amount >= 0.01:
		return fmt.Sprintf("$%.3f", amount)
	default:
		return fmt.Sprintf("$%.4f", amount)
	}
}

// startContextArchiveRead opens the archive viewer and reads one archive off the
// UI thread. The durable read is bounded (one archive event, the contract's read
// caps); the result comes back through UIRuntime.Post so build never blocks on
// the store, and a result for an archive that was replaced while it read is
// dropped.
func startContextArchiveRead(st *state, view *contextArchiveView,
	archiver contracts.ContextArchiveService, sessionID string, seq int64) {
	if st == nil || view == nil || archiver == nil || sessionID == "" || seq <= 0 {
		return
	}
	view.open = true
	view.sessionID = sessionID
	view.seq = seq
	view.loading = true
	view.loaded = false
	view.ok = false
	view.archive = contracts.ContextArchive{}
	view.list = ui.ListState{}
	runtime := st.runtime
	go func() {
		archive, ok := archiver.ContextArchive(sessionID, seq)
		runtime.Post(func() {
			if view.sessionID != sessionID || view.seq != seq || !view.open {
				return
			}
			view.loading = false
			view.loaded = true
			view.ok = ok
			view.archive = archive
			runtime.Invalidate()
		})
	}()
}

// buildContextArchiveModal renders the archived history as a native read-only
// modal: the dialog header carries the archive's provenance (kind, message count,
// size, truncation), the body is a virtualized official List of role/text/tool
// call rows, and the footer closes it.
func buildContextArchiveModal(c *ui.Context, t *design.Tokens, st *state, view *contextArchiveView) {
	ui.Modal(c, &view.open, func() {
		design.Apply(ui.Column(c.Key("session.context.archive")), t.Panel()).
			Width(design.DialogWidthL).Padding(design.Space4).Gap(design.Space3).
			Children(func() {
				uikit.DialogHeader(c, uikit.DialogHeaderOptions{
					Key:        "session.context.archive",
					Title:      st.tr("归档原文", "Archived history"),
					Subtitle:   contextArchiveSubtitle(st, view),
					CloseLabel: st.tr("关闭", "Close"),
					OnClose: func() {
						view.open = false
						c.Invalidate()
					},
				})
				switch {
				case view.loading:
					reading := st.tr("读取归档原文…", "Reading the archived history…")
					ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						ui.Spinner(c).Label(reading)
						design.Apply(ui.Text(c, reading), t.TextMuted(design.TypeXS))
					})
				case !view.ok:
					uikit.EmptyState(c, st.tr("没有找到归档原文。",
						"No archived history was found."))
				default:
					buildContextArchiveList(c, t, st, view)
				}
				uikit.DialogFooter(c, []uikit.DialogAction{{
					Label: st.tr("关闭", "Close"), Default: true,
					OnClick: func() {
						view.open = false
						c.Invalidate()
					},
				}})
			})
	})
}

// contextArchiveSubtitle is the modal's provenance line: the archive kind, the
// replaced message count, the serialized size and — when the archive event hit
// its bound — how many messages were not kept. It is empty until the read lands.
func contextArchiveSubtitle(st *state, view *contextArchiveView) string {
	if view == nil || !view.loaded || !view.ok {
		return ""
	}
	archive := view.archive
	parts := make([]string, 0, 4)
	if kind := contextArchiveKindLabel(st, archive.Kind); kind != "" {
		parts = append(parts, kind)
	}
	if archive.RawMessageCount > 0 {
		parts = append(parts, fmt.Sprintf(st.tr("%d 条消息", "%d messages"), archive.RawMessageCount))
	}
	if archive.Bytes > 0 {
		parts = append(parts, formatContextBytes(archive.Bytes))
	}
	if archive.Truncated {
		parts = append(parts, fmt.Sprintf(st.tr("归档已截断：另有 %d 条未保留",
			"Archive truncated: %d more messages were not kept"), archive.OmittedMessages))
	}
	return strings.Join(parts, " · ")
}

// contextArchiveKindLabel localizes the compaction kind the archive recorded; an
// unknown kind stays raw instead of being guessed.
func contextArchiveKindLabel(st *state, kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "manual":
		return st.tr("手动压缩", "Manual compaction")
	case "auto":
		return st.tr("自动压缩", "Automatic compaction")
	case "recovery":
		return st.tr("溢出恢复", "Overflow recovery")
	default:
		return strings.TrimSpace(kind)
	}
}

// contextArchiveRoleLabel names one archived message's author role.
func contextArchiveRoleLabel(st *state, role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return st.tr("用户", "User")
	case "assistant":
		return st.tr("助手", "Assistant")
	case "tool":
		return st.tr("工具", "Tool")
	case "system":
		return st.tr("系统", "System")
	case "", "unknown":
		return st.tr("消息", "Message")
	default:
		return role
	}
}

// buildContextArchiveList is the archive body: the official List virtualizes the
// archived messages (one bounded row each), so a long archived history stays
// scrollable inside the dialog's bounded body.
func buildContextArchiveList(c *ui.Context, t *design.Tokens, st *state, view *contextArchiveView) {
	messages := view.archive.Messages
	if len(messages) == 0 {
		uikit.EmptyState(c, st.tr("归档里没有可显示的原文。",
			"The archive holds no readable history."))
		return
	}
	count := len(messages)
	view.list.Key = func(index int) any {
		if index < 0 || index >= count {
			return nil
		}
		return fmt.Sprintf("%d-%d", view.archive.Seq, index)
	}
	view.list.Label = func(index int) string {
		if index < 0 || index >= count {
			return ""
		}
		return fmt.Sprintf(st.tr("第 %d 条消息，%s", "Message %d, %s"),
			index+1, contextArchiveRoleLabel(st, messages[index].Role))
	}
	ui.List(c, &view.list, count).
		Label(st.tr("归档原文列表", "Archived history list")).
		FillWidth().MaxHeight(design.DialogBodyMax).
		Rows(func(row ui.ListRow) {
			buildContextArchiveRow(row.Context, t, st, messages[row.Index])
		})
}

// buildContextArchiveRow is one archived message: its role badge, the raw text
// (selectable, shown in full up to the read bound) and the tool calls it carried.
// The truncation the reader applied to the text or the arguments is stated next
// to it, never silently dropped.
func buildContextArchiveRow(c *ui.Context, t *design.Tokens, st *state, message contracts.ContextArchiveMessage) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space2).PaddingY(design.Space1).Children(func() {
		design.Apply(ui.Badge(c, contextArchiveRoleLabel(st, message.Role)), t.Badge()).
			TextColor(t.LabelSecondary)
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			text := strings.TrimSpace(message.Text)
			if text == "" {
				text = st.tr("（无文本）", "(no text)")
			}
			design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).Selectable()
			if len(message.ToolCalls) > 0 {
				names := make([]string, 0, len(message.ToolCalls))
				arguments := make([]string, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					names = append(names, call.Name)
					if argumentsText := strings.TrimSpace(call.Arguments); argumentsText != "" {
						arguments = append(arguments, argumentsText)
					}
				}
				calls := design.Apply(ui.Text(c, st.tr("工具调用：", "Tool calls: ")+
					strings.Join(names, ", ")), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).NoWrap().Ellipsis("…")
				if len(arguments) > 0 {
					calls.Tooltip(strings.Join(arguments, "\n"))
				}
			}
			if message.Truncated {
				design.Apply(ui.Text(c, st.tr("（文本已截断）", "(text truncated)")),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			}
		})
	})
}
