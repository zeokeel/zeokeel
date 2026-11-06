package shell

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/sessionreference"
	"github.com/zeokeel/zeokeel/internal/state"
)

// The footer's action glyphs are inline Lucide paths, the same icon family
// the rest of the shell embeds. Drawing them as SVG (rather than emoji or
// symbol characters) keeps Copy, Retry and the thumbs visible on every
// platform's fallback font.
var (
	actionCopyIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>`))
	actionRetryIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>`))
	actionGoodIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M7 10v12"/><path d="M15 5.88 14 10h5.83a2 2 0 0 1 1.92 2.56l-2.33 8A2 2 0 0 1 17.5 22H4a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2h2.76a2 2 0 0 0 1.79-1.11L12 2a3.13 3.13 0 0 1 3 3.88Z"/></svg>`))
	actionBadIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M17 14V2"/><path d="M9 18.12 10 14H4.17a2 2 0 0 1-1.92-2.56l2.33-8A2 2 0 0 1 6.5 2H20a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-2.76a2 2 0 0 0-1.79 1.11L12 22a3.13 3.13 0 0 1-3-3.88Z"/></svg>`))
)

// liveMessageActions is the brand footer under a completed assistant message:
// the time it first showed, the ghost Copy/Retry/feedback buttons and a
// completion summary. Copy puts the answer on the clipboard, Retry resubmits
// the last user message, and the feedback buttons persist a local good/bad
// marker for the message (this build has no feedback transport, so the toast
// says what was actually stored).
func (a *App) liveMessageActions(c *ui.Context, t *design.Tokens, m state.Message, key string, snap *state.Snapshot) {
	if a.Ctrl == nil {
		return
	}
	th := t.Theme()
	shown := a.messageShownAt(key, c.Now())
	ui.Row(c).Height(th.Space(7)).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, shown.Format("15:04")).FontSize(12).FixedLineHeight(20).
			TextColor(t.LabelTertiary).NoWrap()
		copyButton := messageActionButton(c, t, actionCopyIcon, a.menuText("Copy", "复制"))
		copyButton.OnClick(func() {
			c.WriteClipboard(m.Text)
			c.Toast(a.menuText("Copied", "已复制"))
		})
		retry := messageActionButton(c, t, actionRetryIcon, a.menuText("Retry", "重试"))
		retry.OnClick(func() { a.retryLastUserMessage(c) })
		rating := ""
		if snap != nil {
			rating = snap.Feedback[m.MsgID]
		}
		good := messageActionButton(c, t, actionGoodIcon, a.menuText("Good response", "好的回答"))
		good.OnClick(func() { a.recordMessageFeedback(c, m, "good") })
		if rating == "good" {
			good = good.Background(t.BgHover)
		}
		bad := messageActionButton(c, t, actionBadIcon, a.menuText("Bad response", "差的回答"))
		bad.OnClick(func() { a.recordMessageFeedback(c, m, "bad") })
		if rating == "bad" {
			bad = bad.Background(t.BgHover)
		}
		if summary := a.completionSummary(snap); summary != "" {
			ui.Text(c, summary).FontSize(12).FixedLineHeight(20).
				TextColor(t.LabelTertiary).NoWrap().PaddingX(th.Space(1))
		}
	})
}

// completionSummary is the footer's short completion line. The view only
// knows a duration when it saw the whole running phase; otherwise it says
// the message completed without inventing one. A stopped, interrupted or failed
// turn has no completion line: claiming "completed" there would misreport what
// happened.
func (a *App) completionSummary(snap *state.Snapshot) string {
	if snap != nil && snap.TurnReason != state.ReasonNone {
		return ""
	}
	st := a.liveMenus()
	if !st.turnRunning && st.lastTurnDuration > 0 {
		return a.menuText("Completed in ", "完成于 ") + formatTurnDuration(st.lastTurnDuration)
	}
	return a.menuText("Completed", "已完成")
}

// feedbackSettingKey is the local store row holding one message's feedback.
func feedbackSettingKey(sessionID, msgID string) string {
	return "feedback." + sessionID + "." + msgID
}

// recordMessageFeedback persists the good/bad marker as a feedback row (and
// the legacy setting). A write failure is reported instead of a success toast,
// so the control never claims feedback was recorded when it was not.
func (a *App) recordMessageFeedback(c *ui.Context, m state.Message, value string) {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		c.Toast(a.menuText("Feedback is not available", "反馈不可用"))
		return
	}
	sessionID := a.Ctrl.CurrentSessionID()
	if snap := a.Snapshot(); snap != nil && snap.SessionID != "" {
		sessionID = snap.SessionID
	}
	msgID := m.MsgID
	if msgID == "" {
		msgID = "last"
	}
	if err := a.Ctrl.RecordFeedback(context.Background(), sessionID, msgID, value, ""); err != nil {
		c.Toast(a.menuText("Could not record feedback", "反馈记录失败"))
		return
	}
	c.Toast(a.menuText("Feedback recorded", "反馈已记录"))
}

// messageActionButton is one subtle brand action control: a borderless native
// ButtonBase with the theme's hover tint, an accessible label and a tooltip.
func messageActionButton(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string) ui.Element {
	th := t.Theme()
	button := ui.ButtonBase(c).Size(th.Space(7), th.Space(7)).Center().
		Radius(design.RadiusSM).Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
	})
	return button
}

// retryLastUserMessage resubmits the text of the most recent user message.
// The controller refuses a second concurrent turn, so a click while one runs
// reports why instead of silently doing nothing.
func (a *App) retryLastUserMessage(c *ui.Context) {
	if a.Ctrl == nil {
		return
	}
	snap := a.Snapshot()
	if snap == nil {
		snap = a.Ctrl.Snapshot()
	}
	if snap != nil && snap.Phase == state.PhaseRunning {
		c.Toast(a.menuText("Wait for the current turn to finish", "请等待当前回合结束"))
		return
	}
	text := lastUserText(snap)
	if text == "" {
		c.Toast(a.menuText("Nothing to retry", "没有可重试的消息"))
		return
	}
	id := a.Ctrl.CurrentSessionID()
	if id == "" && snap != nil {
		id = snap.SessionID
	}
	if id == "" {
		return
	}
	if err := a.Ctrl.Submit(context.Background(), id, text); err != nil {
		c.Toast(a.menuText("Could not retry the last message", "无法重试上一条消息"))
	}
}

// lastUserText returns the most recent user message's text, trimmed.
func lastUserText(snap *state.Snapshot) string {
	if snap == nil {
		return ""
	}
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		if snap.Messages[i].Role != state.RoleUser {
			continue
		}
		// The injected referenced-session snapshot is user-role history for the
		// model, not a message the user typed: retry must resend their prompt.
		if sessionreference.IsContextText(snap.Messages[i].Text) {
			continue
		}
		if text := strings.TrimSpace(snap.Messages[i].Text); text != "" {
			return text
		}
	}
	return ""
}

// liveUsageFooter is the composer's usage row: pill segments for turns,
// steps, output tokens, completion time and rate, and cache share. Segments
// whose numbers the snapshot and view clock cannot supply are omitted rather
// than invented.
func (a *App) liveUsageFooter(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if snap == nil {
		return
	}
	segments := a.usageSegments(snap)
	// The footer is as wide as the composer card and wraps its pills: at
	// narrow windows with the docked panel the detailed segments are wider
	// than the card, and wrapping keeps them inside it.
	th := t.Theme()
	if len(segments) == 0 {
		// Before any turn has run there is nothing to show, but the row keeps
		// its height so the composer does not jump (and so the plan badge does
		// not slide under a toast) the moment the first counters arrive.
		ui.Row(c).Width(a.sessionComposerWidthFor(c)).Height(th.Space(6))
		return
	}
	ui.Row(c).Width(a.sessionComposerWidthFor(c)).Gap(th.Space(2)).
		Wrap().AlignItems(ui.Center).AlignContent(ui.Center).Justify(ui.Center).
		Label(a.tr("Session usage", "会话用量")).Children(func() {
		for _, segment := range segments {
			ui.Row(c).Height(th.Space(6)).PaddingX(th.Space(2)).
				Radius(design.RadiusSM).Shrink(0).
				Background(t.BgHover).AlignItems(ui.Center).Children(func() {
				ui.Text(c, segment).FontSize(12).FixedLineHeight(20).
					TextColor(t.LabelTertiary).NoWrap()
			})
		}
	})
}

// usageSegments builds the footer's pill labels in the footer's order. Settings >
// General > Performance & usage controls the detail: Detailed (the default)
// adds the step, output-token, completion-rate and cache segments; Standard
// keeps only the turn count.
func (a *App) usageSegments(snap *state.Snapshot) []string {
	u := snap.Usage
	// Nothing has run yet: no "0 turns 0 steps 0 out tok" strip. DSH hides the
	// activity pill the same way until a step settles.
	if u.Turns == 0 && u.Steps == 0 && u.In == 0 && u.Out == 0 && u.CacheHit == 0 {
		return nil
	}
	detailed := a.currentSettings().PerformanceUsage != session.PerformanceStandard
	segments := []string{
		a.countSegment("%d turn", "%d turns", "%d 轮", u.Turns),
	}
	if detailed {
		segments = append(segments,
			a.countSegment("%d step", "%d steps", "%d 步", u.Steps),
			a.countSegment("%d out tok", "%d out tok", "%d 输出 tok", u.Out),
		)
	} else {
		// Keep Standard explicit rather than an unexplained, shorter row: the
		// user can see why the detailed segments are absent.
		segments = append(segments, a.menuText("Performance: Standard", "性能：标准"))
	}
	st := a.liveMenus()
	// "Completed in" and a completion rate describe a turn that actually
	// completed; a stopped/interrupted/failed turn keeps the counters but shows
	// no completion time, and the rate only covers this turn's output growth.
	completed := !st.turnRunning && snap.Phase != state.PhaseRunning &&
		snap.TurnReason == state.ReasonNone && st.lastTurnDuration > 0
	if detailed && completed {
		d := st.lastTurnDuration
		segments = append(segments, a.menuText("Completed in ", "完成于 ")+formatTurnDuration(d))
		turnOut := u.Out - st.turnStartUsage.Out
		if turnOut > 0 {
			if tps := int(float64(turnOut)/d.Seconds() + 0.5); tps > 0 {
				segments = append(segments, fmt.Sprintf("%d tok/s", tps))
			}
		}
	}
	if u.Advisory != "" {
		segments = append(segments, a.menuText("cost advisory", "成本提醒"))
	}
	if detailed && u.In > 0 && u.CacheHit > 0 {
		pct := u.CacheHit * 100 / u.In
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		segments = append(segments, fmt.Sprintf("%s %d%%", a.menuText("cache", "缓存"), pct))
	}
	return segments
}

// countSegment picks the singular or plural phrasing for a counter.
func (a *App) countSegment(one, many, zh string, n int) string {
	if a.lang() == LangZH {
		return fmt.Sprintf(zh, n)
	}
	if n == 1 {
		return fmt.Sprintf(one, n)
	}
	return fmt.Sprintf(many, n)
}

// toolSummaryCounts is the breakdown the summary line states.
type toolSummaryCounts struct {
	commands int
	files    int
	tools    int
}

// countToolSummary classifies one process block's tool calls for its summary
// line. The buckets: shells and jobs are commands, file and image readers are
// files, and every other tool is counted generically. The session passes only
// the block's calls, so counts are per turn/process block, not cumulative.
func countToolSummary(tools []state.ToolState) toolSummaryCounts {
	var counts toolSummaryCounts
	for _, tool := range tools {
		switch tool.Name {
		case "bash", "pwsh", "terminal", "job_start", "job_list", "job_output", "job_kill":
			counts.commands++
		case "read_file", "read_image":
			counts.files++
		default:
			counts.tools++
		}
	}
	return counts
}

// toolSummaryText is the collapsible summary line above the tool cards.
func (a *App) toolSummaryText(counts toolSummaryCounts) string {
	parts := make([]string, 0, 3)
	if a.lang() == LangZH {
		if counts.commands > 0 {
			parts = append(parts, fmt.Sprintf("运行 %d 条命令", counts.commands))
		}
		if counts.files > 0 {
			parts = append(parts, fmt.Sprintf("读取 %d 个文件", counts.files))
		}
		if counts.tools > 0 {
			parts = append(parts, fmt.Sprintf("调用 %d 个工具", counts.tools))
		}
		return strings.Join(parts, "，")
	}
	if counts.commands > 0 {
		parts = append(parts, fmt.Sprintf("Ran %d %s", counts.commands, toolPlural(counts.commands, "command", "commands")))
	}
	if counts.files > 0 {
		parts = append(parts, fmt.Sprintf("read %d %s", counts.files, toolPlural(counts.files, "file", "files")))
	}
	if counts.tools > 0 {
		parts = append(parts, fmt.Sprintf("called %d %s", counts.tools, toolPlural(counts.tools, "tool", "tools")))
	}
	return strings.Join(parts, ", ")
}

// toolPlural picks the singular or plural noun.
func toolPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// liveToolSummary draws one process block: the summary row and, while it is
// open, the block's cards below it. The tools argument is that block's subset
// of the session, so a multi-turn transcript shows one summary per turn with
// that turn's counts. Each block folds on its own: the key is its first call's
// ID, stable for the life of the block, instead of one session-wide toggle.
func (a *App) liveToolSummary(c *ui.Context, t *design.Tokens, tools []state.ToolState) {
	if len(tools) == 0 {
		return
	}
	st := a.liveMenus()
	label := a.toolSummaryText(countToolSummary(tools))
	if label == "" {
		return
	}
	key := tools[0].CallID
	if st.toolSummaryCollapsed == nil {
		st.toolSummaryCollapsed = make(map[string]bool)
	}
	collapsed := st.toolSummaryCollapsed[key]
	th := t.Theme()
	row := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(1)).
		Gap(th.Space(1)).Radius(design.RadiusSM).
		AlignItems(ui.Center).Justify(ui.Start).Label(label)
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		caret := "›"
		if !collapsed {
			caret = "⌄"
		}
		ui.Text(c, caret).FontSize(11).TextColor(t.LabelTertiary).NoWrap()
		ui.Text(c, label).FontSize(13).FixedLineHeight(20).
			TextColor(t.LabelSecondary).NoWrap()
	})
	if row.Clicked() {
		st.toolSummaryCollapsed[key] = !collapsed
		collapsed = !collapsed
	}
	if !collapsed {
		a.liveToolCards(c, t, tools)
	}
}
