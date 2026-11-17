package insights

// The Insights screen's frame: the range switch, the KPI row, the bounded
// daily/model/workspace tables and the session leaderboard. Every collection is
// a native mygo widget (ui.Table / ui.List); the screen draws no chart and no
// card wall. The data is the contracts.Insights aggregate read by the module's
// background loader; this file only formats it with the design tokens.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildScreenHeaderSlot is the screen's SlotScreenHeader contribution: the
// window-band title on the TypeM step (the same anatomy as the other screens).
func buildScreenHeaderSlot(c *ui.Context, cfg Config, st *state) {
	t := design.Of(c)
	title := st.localized(cfg.Title, "洞察", "Insights")
	description := st.tr(
		"跨会话汇总 token 用量、成本、工具调用与会话排行。",
		"Cross-session totals for tokens, cost, tool calls and the busiest conversations.")
	design.Apply(ui.Text(c, title), t.Font(design.TypeM)).FontWeight(design.WeightSemibold).
		TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Tooltip(description)
}

// buildScreenActionsSlot is the screen's SlotScreenActions contribution: the
// page's one immediate action, Refresh. The aggregate is cached, so the action
// asks the backend for a forced re-read; while a load is in flight the button is
// disabled instead of stacking requests.
func buildScreenActionsSlot(c *ui.Context, st *state) {
	label := st.tr("刷新", "Refresh")
	tooltip := st.tr("重新汇总当前时间范围", "Re-aggregate the current time range")
	uikit.IconButton(c, uikit.IconButtonOptions{
		Key:      "insights.refresh",
		Label:    label,
		Tooltip:  tooltip,
		Icon:     icons.Reload(),
		Size:     design.IconSizeS,
		Disabled: st.service == nil || st.loading,
		OnClick: func() {
			st.requestAggregate(true)
			st.runtime.Invalidate()
		},
	})
}

// buildScreen renders one frame of the Insights screen.
func buildScreen(c *ui.Context, cfg Config, st *state) {
	t := design.Of(c)
	// The aggregation is started from the frame, but it runs on its own
	// goroutine and its result arrives through UIRuntime.Post: the build never
	// reads the store.
	st.ensureLoaded()
	ui.Column(c).FillWidth().Grow(1).Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			// The page reads on the product's reading measure (the transcript's
			// 960 DIP column, capped at 92% of the content area): a wide window
			// centers it instead of stretching the tables' numeric columns to the
			// screen's far right.
			uikit.ReadingColumn(c, func(c *ui.Context) {
				buildToolbar(c, t, st)
				switch {
				case st.service == nil:
					uikit.EmptyStateBlock(c,
						st.tr("洞察后端未启用", "Insights backend not enabled"),
						st.tr("当前构建没有加载跨会话用量汇总模块。",
							"This build does not load the cross-session usage aggregation module."), nil)
				case st.errText != "":
					buildErrorState(c, t, st)
				case !st.hasData && st.loading:
					buildLoadingState(c, t, st)
				case !st.hasData:
					uikit.EmptyStateBlock(c,
						st.tr("还没有可统计的用量", "No usage to aggregate yet"),
						st.tr("运行一轮对话后，这里会按天、模型、工作区和会话汇总 token、成本与工具调用。",
							"Run one turn and tokens, cost and tool calls are summarized here by day, model, workspace and conversation."), nil)
				case insightsEmpty(st.data):
					uikit.EmptyStateBlock(c,
						st.tr("这个时间范围内还没有用量", "No usage in this time range"),
						st.tr("换到更长的范围，或先运行一轮对话。",
							"Switch to the longer range, or run a turn first."), nil)
				default:
					buildKPI(c, t, st, st.data)
					if st.data.Truncated {
						design.Apply(ui.Text(c, st.tr(
							fmt.Sprintf("统计达到单会话读取上限，较早的事件可能未计入（%d 个会话）。",
								st.data.TruncatedSessions),
							fmt.Sprintf("The per-session read bound was reached; older events may be missing (%d conversations).",
								st.data.TruncatedSessions))), t.Font(design.TypeXXS)).
							TextColor(t.LabelTertiary).MaxLines(2)
					}
					buildDailySection(c, t, st, st.data)
					buildModelSection(c, t, st, st.data)
					buildWorkspaceSection(c, t, st, st.data)
					buildSessionSection(c, t, st, st.data)
				}
			})
		})
	})
}

// buildToolbar is the page's own control row: the 7/30-day switch on the left,
// the aggregate's fresh range and update time on the right (muted, so the range
// switch stays the one control in the row).
func buildToolbar(c *ui.Context, t *design.Tokens, st *state) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
		buildRangeSwitch(c, st)
		ui.Spacer(c).Grow(1)
		if st.hasData {
			design.Apply(ui.Text(c, insightsRangeLabel(st, st.data)), t.TextMuted(design.TypeXXS)).
				NoWrap().MaxLines(1)
		}
	})
}

// buildRangeSwitch renders the page's range control through the product's one
// segmented control (uikit.SegmentedControl: the same BgSelected/LabelPrimary
// chosen segment, Accent boundary, radio-group semantics and arrow keys the
// settings pages use). Switching reloads the aggregation.
func buildRangeSwitch(c *ui.Context, st *state) {
	labels := []string{st.tr("7 天", "7 days"), st.tr("30 天", "30 days")}
	uikit.SegmentedControl(c, uikit.SegmentedOptions{
		Key:      "insights.range",
		Label:    st.tr("时间范围", "Time range"),
		Labels:   labels,
		Selected: &st.rangeIndex,
	})
	// The widget writes st.rangeIndex as it takes the click or the arrow keys;
	// the data switch follows the value (the following pass), not a one-shot
	// Changed notice, so the range and the aggregate can never drift apart.
	if st.days != daysForRange(st.rangeIndex) {
		st.setRange(st.rangeIndex)
		st.runtime.Invalidate()
	}
}

// buildLoadingState is the first-load state: a spinner and one sentence, no
// skeleton numbers (an empty aggregate would look like real zeros).
func buildLoadingState(c *ui.Context, t *design.Tokens, st *state) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		PaddingY(design.Space6).Children(func() {
		ui.Spinner(c).Label(st.tr("正在汇总", "Aggregating")).Size(design.IconSizeS, design.IconSizeS)
		design.Apply(ui.Text(c, st.tr("正在汇总用量…", "Aggregating usage…")),
			t.TextMuted(design.TypeXS))
	})
}

// buildErrorState shows the backend failure with one retry action; a refresh
// failure that left earlier data on screen keeps that data below the error.
func buildErrorState(c *ui.Context, t *design.Tokens, st *state) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		uikit.ErrorLine(c, st.errText)
		if st.hasData {
			// Stale-but-real data stays visible under the error line.
			buildKPI(c, t, st, st.data)
			buildDailySection(c, t, st, st.data)
			buildModelSection(c, t, st, st.data)
			buildWorkspaceSection(c, t, st, st.data)
			buildSessionSection(c, t, st, st.data)
			return
		}
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			retry := design.Apply(ui.Button(c, st.tr("重试", "Retry")), t.SecondaryButton())
			if retry.Clicked() {
				st.requestAggregate(true)
				st.runtime.Invalidate()
			}
		})
	})
}

// buildKPI is the KPI row: four flat metric columns (label, value, one muted
// detail line), no cards. Cost appears only when at least one call was priced;
// a partial total is marked instead of being presented as complete.
func buildKPI(c *ui.Context, t *design.Tokens, st *state, data contracts.Insights) {
	totals := data.Totals
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(design.Space5).Children(func() {
		kpiCell(c, t, st.tr("词元 tokens", "Tokens"), uikit.FormatCompactCount(totals.TotalTokens),
			fmt.Sprintf(st.tr("输入 %s · 输出 %s", "%s in · %s out"),
				uikit.FormatCompactCount(totals.PromptTokens),
				uikit.FormatCompactCount(totals.CompletionTokens)))
		if totals.PricedCalls > 0 {
			hint := st.tr("估算", "Estimated")
			if totals.UnpricedCalls > 0 {
				hint += " · " + st.tr("部分模型未定价", "some models unpriced")
			}
			kpiCell(c, t, st.tr("成本", "Cost"), formatCostUSD(totals.CostUSD), hint)
		}
		kpiCell(c, t, st.tr("会话", "Sessions"), fmt.Sprintf("%d", totals.Sessions),
			fmt.Sprintf(st.tr("%d 轮对话 · %d 步工具调用", "%d turns · %d tool steps"), totals.Turns, totals.Steps))
		toolHint := ""
		if totals.Compactions > 0 || totals.Spills > 0 {
			toolHint = fmt.Sprintf(st.tr("压缩 %d · 溢出 %d", "%d compactions · %d spills"),
				totals.Compactions, totals.Spills)
		}
		kpiCell(c, t, st.tr("工具调用", "Tool calls"), fmt.Sprintf("%d", totals.ToolCalls), toolHint)
	})
}

// kpiCell is one metric of the KPI row: a muted micro-label, the value and an
// optional muted detail line. Grow+MinWidth keep the four metrics from wrapping
// into a second row on a narrow window.
//
// The value takes the page's TypeM step with 600 weight rather than TypeXL/L:
// §5.3 reserves TypeL (20/28) for settings pages' and dialogs' H1, and spending
// it on a metric value gave this one screen five sizes (TypeM/S/L/XS/XXS)
// against the four the rule allows. TypeM/600 keeps the value a level above the
// 14/13/12 text around it, so the metric row still reads as the page's data.
func kpiCell(c *ui.Context, t *design.Tokens, label, value, hint string) {
	ui.Column(c).Grow(1).Shrink(1).MinWidth(design.Space8 * 3).Gap(design.Space1).Children(func() {
		design.Apply(ui.Text(c, label), t.TextMuted(design.TypeXXS)).MaxLines(1).Ellipsis("…")
		design.Apply(ui.Text(c, value), t.Font(design.TypeM)).FontWeight(design.WeightSemibold).
			TextColor(t.LabelPrimary).MaxLines(1).NoWrap()
		if hint != "" {
			design.Apply(ui.Text(c, hint), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).
				MaxLines(1).Ellipsis("…")
		}
	})
}

// buildDailySection is the per-day table, newest day first. Empty days render
// as "—" rather than a fake zero row.
func buildDailySection(c *ui.Context, t *design.Tokens, st *state, data contracts.Insights) {
	uikit.Section(c, st.tr("按天", "By day"), func(body *ui.Context) {
		// Only days with activity are listed: a placeholder "—" row for every
		// day the user did not use the app was noise, not information.
		days := activeDays(data.Daily)
		count := len(days)
		if count == 0 {
			uikit.EmptyState(body, st.tr("这个时间范围内没有按天用量。",
				"No daily usage in this time range."))
			return
		}
		columns := []ui.TableColumn{
			{Title: st.tr("日期", "Day"), MinWidth: design.Space8 * 4, MaxWidth: design.Space8 * 6},
			// The header names the metric only: "词元 tokens" truncated at this
			// column width while the row had room to spare; the unit stays on
			// the KPI label above.
			{Title: st.tr("词元", "Tokens"), Width: design.Space8 * 3, Align: ui.End},
		}
		if daysCostVisible(days) {
			columns = append(columns, ui.TableColumn{
				Title: st.tr("成本", "Cost"), Width: design.Space8 * 3, Align: ui.End,
			})
		}
		columns = append(columns,
			ui.TableColumn{Title: st.tr("会话", "Sessions"), Width: design.Space8 * 2, Align: ui.End},
			ui.TableColumn{Title: st.tr("工具", "Tools"), Width: design.Space8 * 2, Align: ui.End},
		)
		st.dailyTable.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			return days[count-1-index].Day.Format("2006-01-02")
		}
		st.dailyTable.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			day := days[count-1-index]
			return formatDay(st, day.Day) + " · " + uikit.FormatCompactCount(day.TotalTokens) + " tokens"
		}
		withCost := daysCostVisible(days)
		ui.Table(body, &st.dailyTable, columns, count, func(row, col int) {
			day := days[count-1-row]
			empty := day.TotalTokens == 0 && day.ToolCalls == 0 && day.Sessions == 0 && day.Turns == 0
			value := "—"
			color := t.LabelTertiary
			switch {
			case col == 0:
				value = formatDay(st, day.Day)
				color = t.LabelPrimary
			case empty:
				// An empty day renders an em dash, not a fake zero row.
			case col == 1:
				value = uikit.FormatCompactCount(day.TotalTokens)
				color = t.LabelSecondary
			case withCost && col == 2:
				value = formatCostCell(day.CostUSD, day.CostKnown)
				if day.CostKnown {
					color = t.LabelSecondary
				}
			case col == 2+boolToInt(withCost):
				value = fmt.Sprintf("%d", day.Sessions)
				color = t.LabelSecondary
			default:
				value = fmt.Sprintf("%d", day.ToolCalls)
				color = t.LabelSecondary
			}
			design.Apply(ui.Text(body, value), t.Font(design.TypeXS)).TextColor(color).NoWrap()
		}).Label(st.tr("按天用量", "Daily usage")).MaxHeight(design.Space8 * 10)
	})
}

// buildModelSection is the per-model table (calls, total tokens, cached tokens
// and the optional cost).
func buildModelSection(c *ui.Context, t *design.Tokens, st *state, data contracts.Insights) {
	uikit.Section(c, st.tr("按模型", "By model"), func(body *ui.Context) {
		rows := data.Models
		count := len(rows)
		if count == 0 {
			uikit.EmptyState(body, st.tr("还没有模型调用记录。", "No model calls recorded yet."))
			return
		}
		columns := []ui.TableColumn{
			{Title: st.tr("模型", "Model"), MinWidth: design.Space8 * 5, MaxWidth: design.Space8 * 10},
			{Title: st.tr("调用", "Calls"), Width: design.Space8 * 2, Align: ui.End},
			{Title: st.tr("词元", "Tokens"), Width: design.Space8 * 3, Align: ui.End},
			{Title: st.tr("缓存", "Cached"), Width: design.Space8 * 3, Align: ui.End},
		}
		if modelsCostVisible(rows) {
			columns = append(columns, ui.TableColumn{
				Title: st.tr("成本", "Cost"), Width: design.Space8 * 3, Align: ui.End,
			})
		}
		st.modelTable.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			return rows[index].Model
		}
		st.modelTable.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			row := rows[index]
			return textutil.FirstNonEmptyTrimmed(row.Model, st.tr("未知模型", "Unknown model")) + " · " +
				uikit.FormatCompactCount(row.TotalTokens) + " tokens"
		}
		ui.Table(body, &st.modelTable, columns, count, func(row, col int) {
			model := rows[row]
			value := ""
			color := t.LabelSecondary
			switch col {
			case 0:
				value = textutil.FirstNonEmptyTrimmed(model.Model, st.tr("未知模型", "Unknown model"))
				color = t.LabelPrimary
			case 1:
				value = fmt.Sprintf("%d", model.Calls)
			case 2:
				value = uikit.FormatCompactCount(model.TotalTokens)
			case 3:
				value = uikit.FormatCompactCount(model.CachedTokens)
			default:
				value = formatCostCell(model.CostUSD, model.CostKnown)
				if !model.CostKnown {
					color = t.LabelTertiary
				}
			}
			design.Apply(ui.Text(body, value), t.Font(design.TypeXS)).TextColor(color).NoWrap().Ellipsis("…")
		}).Label(st.tr("按模型用量", "Usage by model")).MaxHeight(design.Space8 * 8)
	})
}

// buildWorkspaceSection is the per-workspace table. It is omitted when every
// session is unassociated (one empty row would be noise), and the unassociated
// group is labelled explicitly when it appears next to real workspaces.
func buildWorkspaceSection(c *ui.Context, t *design.Tokens, st *state, data contracts.Insights) {
	if !workspacesUseful(data.Workspaces) {
		return
	}
	uikit.Section(c, st.tr("按工作区", "By workspace"), func(body *ui.Context) {
		rows := data.Workspaces
		count := len(rows)
		columns := []ui.TableColumn{
			{Title: st.tr("工作区", "Workspace"), MinWidth: design.Space8 * 5, MaxWidth: design.Space8 * 10},
			{Title: st.tr("会话", "Sessions"), Width: design.Space8 * 2, Align: ui.End},
			{Title: st.tr("词元", "Tokens"), Width: design.Space8 * 3, Align: ui.End},
		}
		if workspacesCostVisible(rows) {
			columns = append(columns, ui.TableColumn{
				Title: st.tr("成本", "Cost"), Width: design.Space8 * 3, Align: ui.End,
			})
		}
		st.workspaceTable.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			return rows[index].WorkspaceID
		}
		st.workspaceTable.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			return workspaceLabel(st, rows[index])
		}
		ui.Table(body, &st.workspaceTable, columns, count, func(row, col int) {
			workspace := rows[row]
			value := ""
			color := t.LabelSecondary
			switch col {
			case 0:
				value = workspaceLabel(st, workspace)
				color = t.LabelPrimary
			case 1:
				value = fmt.Sprintf("%d", workspace.Sessions)
			case 2:
				value = uikit.FormatCompactCount(workspace.TotalTokens)
			default:
				value = formatCostCell(workspace.CostUSD, workspace.CostKnown)
				if !workspace.CostKnown {
					color = t.LabelTertiary
				}
			}
			design.Apply(ui.Text(body, value), t.Font(design.TypeXS)).TextColor(color).NoWrap().Ellipsis("…")
		}).Label(st.tr("按工作区用量", "Usage by workspace")).MaxHeight(design.Space8 * 6)
	})
}

// buildSessionSection is the session leaderboard: a native List whose rows show
// the title, model, tokens and optional cost. A row click opens the
// conversation; Enter opens the keyboard-chosen row.
func buildSessionSection(c *ui.Context, t *design.Tokens, st *state, data contracts.Insights) {
	uikit.Section(c, st.tr("会话排行", "Top conversations"), func(body *ui.Context) {
		if len(data.Sessions) == 0 {
			uikit.EmptyState(body, st.tr("这个时间范围内没有会话活动。",
				"No conversation activity in this time range."))
			return
		}
		// A local filter over the rows already on screen: the range read is not
		// repeated for a query. The click and Enter paths below run over the
		// filtered slice, so a filtered row still opens its conversation.
		ui.SearchField(body, &st.sessionQuery).
			Label(st.tr("筛选会话", "Filter conversations")).
			Placeholder(st.tr("标题、模型或工作区…", "Title, model or workspace…")).
			FillWidth()
		workspaceNames := st.workspaceNames(data.Workspaces)
		rows := filterSessions(st, data.Sessions, workspaceNames)
		count := len(rows)
		if count == 0 {
			uikit.EmptyState(body, st.tr("没有匹配的会话。", "No conversations match the filter."))
			return
		}
		st.sessionList.Key = func(index int) any {
			if index < 0 || index >= count {
				return nil
			}
			return rows[index].SessionID
		}
		st.sessionList.Label = func(index int) string {
			if index < 0 || index >= count {
				return ""
			}
			return st.sessionLabel(rows[index])
		}
		st.sessionList.Selected = &st.sessionChosen
		// A filter can shrink the rows under the keyboard choice: keep the app
		// choice inside them instead of leaving Enter on a stale index.
		if st.sessionChosen >= count {
			st.sessionList.Choose(count - 1)
		}
		list := ui.List(body, &st.sessionList, count).
			Label(st.tr("会话排行列表", "Top conversations list")).
			MaxHeight(design.Space8 * 12).
			Rows(func(row ui.ListRow) {
				buildSessionRow(row, t, st, rows[row.Index], workspaceNames)
			})
		if list.Submitted() && st.sessionChosen >= 0 && st.sessionChosen < count {
			st.openSession(rows[st.sessionChosen].SessionID)
		}
	})
}

// activeDays keeps the days with activity: the table then lists only days the
// product was used, without padding the window with zero rows.
func activeDays(days []contracts.InsightsDay) []contracts.InsightsDay {
	active := make([]contracts.InsightsDay, 0, len(days))
	for _, day := range days {
		if day.TotalTokens == 0 && day.ToolCalls == 0 && day.Sessions == 0 && day.Turns == 0 {
			continue
		}
		active = append(active, day)
	}
	return active
}

// filterSessions narrows the leaderboard by its local query: the title, the
// model and the workspace (its display name and its ID) are matched, so
// "model-a" or a workspace name finds its conversations.
func filterSessions(st *state, sessions []contracts.InsightsSession,
	workspaceNames map[string]string) []contracts.InsightsSession {
	needle := strings.ToLower(strings.TrimSpace(st.sessionQuery))
	if needle == "" {
		return sessions
	}
	filtered := make([]contracts.InsightsSession, 0, len(sessions))
	for _, session := range sessions {
		haystack := strings.ToLower(session.Title + "\x00" + session.Model + "\x00" +
			session.WorkspaceID + "\x00" + workspaceNames[session.WorkspaceID])
		if strings.Contains(haystack, needle) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

// buildSessionRow is one leaderboard row: the conversation title and its muted
// meta line on the left, tokens and the optional cost at the row's end. The
// row content observes the pointer so a single click opens the conversation
// (the List's Submitted keeps Enter and a double click working).
//
// The chosen row is painted by mygo's List in the theme's accent fill, so all
// of its own copy switches to Tokens.AccentText while row.Selected(): leaving
// LabelPrimary/LabelTertiary there would paint the near-black ink (light) or
// the near-white ink (dark) on the accent and measure 2.4:1 / 1.9:1, below the
// 4.5:1 the row's small text needs. A secondary tone would dim to a blend
// below AA as well, so the whole row takes the on-accent foreground and keeps
// its hierarchy through weight and size, as the settings list rows do.
func buildSessionRow(row ui.ListRow, t *design.Tokens, st *state,
	session contracts.InsightsSession, workspaceNames map[string]string) {
	titleColor, metaColor, valueColor := t.LabelPrimary, t.LabelTertiary, t.LabelPrimary
	costColor, unpricedColor := t.LabelSecondary, t.LabelTertiary
	if row.Selected() {
		titleColor, metaColor, valueColor = t.AccentText, t.AccentText, t.AccentText
		costColor, unpricedColor = t.AccentText, t.AccentText
	}
	content := ui.Row(row.Context).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		PaddingX(design.Space2).PaddingY(design.Space1).Radius(design.RadiusSM)
	content.ObserveInput(func(ev ui.InputEvent) {
		if ev.Kind != ui.InputPointerUp || ev.Button != 0 {
			return
		}
		st.openSession(session.SessionID)
	})
	content.Children(func() {
		ui.Column(row.Context).Grow(1).Shrink(1).MinWidth(design.Space0).Gap(design.Space1).Children(func() {
			title := textutil.FirstNonEmptyTrimmed(session.Title, st.tr("未命名会话", "Untitled conversation"))
			design.Apply(ui.Text(row.Context, title), t.Font(design.TypeS)).
				FontWeight(design.WeightMedium).TextColor(titleColor).
				MaxLines(1).Ellipsis("…").Tooltip(title)
			meta := make([]string, 0, 3)
			if model := strings.TrimSpace(session.Model); model != "" {
				meta = append(meta, model)
			}
			if workspace := session.WorkspaceID; workspace != "" {
				meta = append(meta, textutil.FirstNonEmptyTrimmed(workspaceNames[workspace], workspace))
			}
			if !session.UpdatedAt.IsZero() {
				meta = append(meta, uikit.FormatDateTime(st.locale, session.UpdatedAt))
			}
			if len(meta) > 0 {
				design.Apply(ui.Text(row.Context, strings.Join(meta, " · ")), t.Font(design.TypeXXS)).
					TextColor(metaColor).MaxLines(1).Ellipsis("…")
			}
		})
		// The trailing numbers are right-aligned at fixed token widths, so the
		// leaderboard reads as columns even though it is a List.
		design.Apply(ui.Text(row.Context, uikit.FormatCompactCount(session.TotalTokens)),
			t.Font(design.TypeXS)).TextColor(valueColor).NoWrap().
			Width(design.Space8 * 3).TextAlign(ui.End).
			Tooltip(st.tr("本时间段内的 token 总量", "Tokens in this time range"))
		if sessionsCostVisible(st.data.Sessions) {
			cost := formatCostCell(session.CostUSD, session.CostKnown)
			if session.CostKnown {
				design.Apply(ui.Text(row.Context, cost), t.Font(design.TypeXS)).
					TextColor(costColor).NoWrap().Width(design.Space8 * 3).TextAlign(ui.End)
			} else {
				design.Apply(ui.Text(row.Context, cost), t.Font(design.TypeXS)).
					TextColor(unpricedColor).NoWrap().Width(design.Space8 * 3).TextAlign(ui.End).
					Tooltip(st.tr("该会话的模型未配置价格",
						"This conversation's model has no pricing configured"))
			}
		}
	})
}

// workspaceNames maps the aggregate's workspace IDs to their display names.
func (st *state) workspaceNames(workspaces []contracts.InsightsWorkspace) map[string]string {
	names := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		if id := strings.TrimSpace(workspace.WorkspaceID); id != "" {
			if name := strings.TrimSpace(workspace.Name); name != "" {
				names[id] = name
			}
		}
	}
	return names
}

// sessionLabel is the accessible name of one leaderboard row.
func (st *state) sessionLabel(session contracts.InsightsSession) string {
	title := textutil.FirstNonEmptyTrimmed(session.Title, st.tr("未命名会话", "Untitled conversation"))
	label := title
	if model := strings.TrimSpace(session.Model); model != "" {
		label += " · " + model
	}
	label += " · " + uikit.FormatCompactCount(session.TotalTokens) + " tokens"
	if session.CostKnown {
		label += " · " + formatCostUSD(session.CostUSD)
	}
	return label
}

// insightsEmpty reports an aggregate with nothing recorded. It guards against an
// aggregate that answers with zero rows but a non-zero window.
func insightsEmpty(data contracts.Insights) bool {
	return data.Totals.Sessions == 0 && data.Totals.TotalTokens == 0 &&
		data.Totals.ToolCalls == 0 && len(data.Models) == 0 && len(data.Sessions) == 0 &&
		data.Totals.Turns == 0
}

// insightsRangeLabel is the toolbar's fresh-data line: the window's dates and
// when the aggregate was produced.
func insightsRangeLabel(st *state, data contracts.Insights) string {
	if len(data.Daily) == 0 {
		return ""
	}
	from := data.Daily[0].Day
	to := data.Daily[len(data.Daily)-1].Day
	label := formatDay(st, from) + " – " + formatDay(st, to)
	if !data.GeneratedAt.IsZero() {
		label += " · " + fmt.Sprintf(st.tr("更新于 %s", "updated %s"),
			uikit.FormatClock(st.locale, data.GeneratedAt))
	}
	return label
}

// formatDay renders one calendar day for the tables: a compact numeric date in
// Chinese and a short month/day in English.
func formatDay(st *state, day time.Time) string {
	if day.IsZero() {
		return "—"
	}
	if st.english() {
		return day.Format("Jan 2")
	}
	return day.Format("01-02")
}

// formatCostCell renders one aggregate's cost or the em dash for an unknown
// price (a missing price is never rendered as zero).
func formatCostCell(amount float64, known bool) string {
	if !known {
		return "—"
	}
	return formatCostUSD(amount)
}

// formatCostUSD renders an estimated price without fake precision: cents when
// the amount is meaningful, more decimals for sub-cent totals.
func formatCostUSD(amount float64) string {
	switch {
	case amount <= 0:
		return "$0"
	case amount >= 1:
		return fmt.Sprintf("$%.2f", amount)
	case amount >= 0.01:
		return fmt.Sprintf("$%.3f", amount)
	default:
		return fmt.Sprintf("$%.4f", amount)
	}
}

// daysCostVisible reports whether any day row is priced (the cost column then
// exists, with "—" for the unpriced days).
func daysCostVisible(days []contracts.InsightsDay) bool {
	for _, day := range days {
		if day.CostKnown {
			return true
		}
	}
	return false
}

// modelsCostVisible reports whether any model row is priced.
func modelsCostVisible(models []contracts.InsightsModel) bool {
	for _, model := range models {
		if model.CostKnown {
			return true
		}
	}
	return false
}

// workspacesUseful reports whether the workspace dimension has anything worth a
// table: at least one associated workspace (a single "unassociated" row would
// only repeat the session total).
func workspacesUseful(workspaces []contracts.InsightsWorkspace) bool {
	for _, workspace := range workspaces {
		if strings.TrimSpace(workspace.WorkspaceID) != "" {
			return true
		}
	}
	return false
}

// workspacesCostVisible reports whether any workspace row is priced.
func workspacesCostVisible(workspaces []contracts.InsightsWorkspace) bool {
	for _, workspace := range workspaces {
		if workspace.CostKnown {
			return true
		}
	}
	return false
}

// sessionsCostVisible reports whether any leaderboard row is priced.
func sessionsCostVisible(sessions []contracts.InsightsSession) bool {
	for _, session := range sessions {
		if session.CostKnown {
			return true
		}
	}
	return false
}

// workspaceLabel renders one workspace row's name (the backend resolves the
// display name; the raw ID is the fallback and the unassociated group says so).
func workspaceLabel(st *state, workspace contracts.InsightsWorkspace) string {
	if name := strings.TrimSpace(workspace.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(workspace.WorkspaceID); id != "" {
		return id
	}
	return st.tr("未关联工作区", "No workspace")
}

// boolToInt converts a flag for the column offset arithmetic.
func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
