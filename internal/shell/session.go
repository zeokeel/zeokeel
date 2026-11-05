package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Session screen geometry, measured from the DSH baseline screenshot
// baseline/screenshots/61-session-chat.png (1600x1000).
//
//   - sidebar 280
//   - main: header 76, transcript 845 wide (centered beside an 8px
//     scrollbar gutter), composer 877x98, usage footer 26.
//
// Every block below has the baseline's measured box: sizes and gaps place
// the boxes, and a per-line ink nudge aligns the glyph raster inside them.
const (
	sessHeaderH   float32 = 76
	sessTabsH     float32 = 25
	sessColumnW   float32 = 845
	sessGutter    float32 = 8
	sessComposerW float32 = 877
	sessComposerH float32 = 98
	sessUsageH    float32 = 26
	sessBottomH   float32 = sessComposerH + sessUsageH + 4
	sessLine      float32 = 24 // markdown body line box
)

// sessShareIcon is the assistant footer's share action. DSH renders SVG
// artwork there; the old static path drew a text glyph.
var sessShareIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12v7a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-7"/><path d="M16 6l-4-4-4 4"/><path d="M12 2v13"/></svg>`))

// sessModeIcon is the 14x14 line icon in the static session header's 16x16
// mode slot. The old path painted the text glyph "☁", which the baseline's
// SVG does not contain (baseline/css/08-session-chat.json elements 150-156).
var sessModeIcon = homeIconTune

// Session colors that the design tokens do not name, sampled from the
// baseline's computed styles.
var (
	sessFaint  = ui.Hex("#ADB2B8") // quaternary label, blockquote bar
	sessCardBg = ui.Hex("#FAFAFA")
	sessCardLn = ui.RGBA(0, 0, 0, 0.04)
	sessCodeBg = ui.Hex("#FAFAFA")
	sessUserBg = ui.Hex("#EDF3FE")
	sessPlusBg = ui.Hex("#F5F6F7")
)

// The static transcript pairs its light surfaces with theme-aware foregrounds.
// Hard-coding the light values made the dark fallback unreadable (1.00-1.07:1),
// so the dark theme uses the same raised/hover surfaces as the live renderer.
func sessUserSurface(t *design.Tokens) ui.Color {
	if t.Dark {
		return t.BgHover
	}
	return sessUserBg
}

func sessCardSurface(t *design.Tokens) ui.Color {
	if t.Dark {
		return t.BgRaised
	}
	return sessCardBg
}

func sessCodeSurface(t *design.Tokens) ui.Color {
	if t.Dark {
		return t.BgStrong
	}
	return sessCodeBg
}

func sessPlusSurface(t *design.Tokens) ui.Color {
	if t.Dark {
		return t.BgStrong
	}
	return sessPlusBg
}

func sessCardLine(t *design.Tokens) ui.Color {
	if t.Dark {
		return t.BorderL2
	}
	return sessCardLn
}

// session draws the Chat tab of a session: header, transcript, composer.
func (a *App) session(c *ui.Context, t *design.Tokens) {
	snap := a.Snapshot()
	if snap == nil && a.Ctrl != nil {
		// The shell normally receives snapshots through OnUpdate/SetSnapshot.
		// Falling back to the controller keeps a first live frame (and tests
		// that wire only Ctrl) from painting the static demo transcript.
		snap = a.Ctrl.Snapshot()
	}
	if snap != nil {
		a.sessionLive(c, t, snap)
		return
	}
	design.Apply(ui.Column(c), t.Main()).Children(func() {
		a.sessHeader(c, t)
		a.sessTranscript(c, t)
		a.sessComposerArea(c, t)
	})
}

// sessHeader draws the 76px header: session title, mode chip, window
// actions, then the Chat/Trajectory tab row.
func (a *App) sessHeader(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Height(sessHeaderH).Padding(10, 12, 0, 20).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL3).Children(func() {
		ui.Row(c).Height(30).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "纯 Go MyGo+Eino AI agent 规划").FontSize(14).FixedLineHeight(20).
				FontWeight(500).TextColor(t.LabelPrimary).Width(236).PaddingX(8).
				NoWrap().Margin(-1, 0, 0, 0)
			ui.Box(c).Width(10)
			ui.Row(c).Height(22).Gap(2).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Size(16, 16).Center().Label("Mode icon").Children(func() {
					ui.Icon(c, sessModeIcon).Size(14, 14).TextColor(t.LabelTertiary)
				})
				ui.Text(c, a.T("common.standard_mode")).FontSize(12).FixedLineHeight(22).
					TextColor(t.LabelTertiary).NoWrap()
			})
			ui.Box(c).Grow(1)
			ui.Row(c).Gap(8).Children(func() {
				a.sessHeaderDots(c, t)
				a.sessHeaderPanel(c, t)
			})
		})
		ui.Row(c).Height(sessTabsH).Margin(10, 0, 0, 0).Padding(0, 0, 0, 8).
			Gap(36).Children(func() {
			a.sessTab(c, t, a.T("session.chat"), true)
			a.sessTab(c, t, a.T("session.trajectory"), false)
		})
	})
}

// sessTab draws one Chat/Trajectory tab; the active one carries a 2px
// accent underline on the header's bottom border.
func (a *App) sessTab(c *ui.Context, t *design.Tokens, label string, active bool) {
	col := ui.Column(c).Height(sessTabsH).Justify(ui.SpaceBetween)
	if active {
		col.Width(30)
	}
	col.Children(func() {
		text := ui.Text(c, label).FontSize(13).FixedLineHeight(16).FontWeight(500).
			NoWrap().Margin(-1, 0, 0, 0)
		if active {
			text.TextColor(t.Accent)
			ui.Box(c).Height(2).Margin(0, 0, -1, 0).Background(t.Accent)
		} else {
			text.TextColor(t.LabelSecondary)
		}
	})
}

// sessHeaderDots is the "more actions" button: a horizontal ellipsis.
func (a *App) sessHeaderDots(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Size(28, 28).Center().Label(a.T("session.more_actions")).
		Role(ui.RoleButton).Focusable().Children(func() {
		ui.Text(c, "⋯").FontSize(13).FontWeight(700).TextColor(t.LabelSecondary)
	})
}

// sessHeaderPanel is the "open right sidebar" button: a panel outline.
func (a *App) sessHeaderPanel(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Size(28, 28).Center().Children(func() {
		ui.Box(c).Size(14, 14).Border(1, t.LabelSecondary).Radius(3).Children(func() {
			ui.Row(c).Grow(1).Justify(ui.End).Children(func() {
				ui.Box(c).Width(1).Height(12).Background(t.LabelSecondary)
			})
		})
	})
}

// sessTranscript is the scrolling message column: 845 wide, centered in
// the main column beside the 8px scrollbar gutter. The placeholder history
// is static and deterministic. Clip() bounds it to the space the layout gave
// it, so at 1200x800 (where the fixed fixture is taller than the viewport)
// the transcript cannot paint through the usage footer or the composer.
func (a *App) sessTranscript(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Grow(1).Padding(0, sessGutter, 0, 1).Justify(ui.Center).
		AlignItems(ui.Start).Clip().Label("Session transcript").
		Children(func() {
			ui.Column(c).Width(a.sessionTranscriptWidthFor(c)).Children(func() {
				a.sessMessages(c, t)
			})
		})
}

// sessMessages lays out the visible transcript blocks (the history is
// scrolled to this state in the baseline).
func (a *App) sessMessages(c *ui.Context, t *design.Tokens) {
	// Tail of an image attachment, clipped by the sticky header: 640x50
	// visible with rounded bottom corners.
	a.sessClipCard(c, t)
	ui.Box(c).Height(1)

	a.sessBody(c, t, "ZeoKeel + AGENT 方框（结构对齐 DSH 的 deepseek HARNESS）")
	ui.Box(c).Height(16)

	a.sessQuote(c, t, "预览用的是系统字体（DejaVu/Nimbus），确定方向后我再按最终字体出矢量资产。")
	ui.Box(c).Height(16)

	a.sessRichLine(c, t, 24, -1,
		ui.Span{Text: "2. 状态类控件怎么测？", Weight: 600},
		ui.Span{Text: " 插件开关、设置项、Sign out 等会改你当前实例。建议我起一个"},
		ui.Span{Text: "隔离的 DSH 实例/独立 profile", Weight: 600},
		ui.Span{Text: " 专门做破坏性"},
	)
	a.sessBody(c, t, "探测（不影响你正在用的窗口）；不隔离的话我只能标注“待确认”。你选哪种？")
	ui.Box(c).Height(32)

	a.sessHeading(c, t, "下一步（按优先级）")
	ui.Box(c).Height(16)

	a.sessList(c, t)
	ui.Box(c).Height(16)

	a.sessBody(c, t, "你定完字标和探测方式，我继续把剩余屏与行为补齐。")
	ui.Box(c).Height(16)

	a.sessNote(c, t, a.T("session.host_note"))
	ui.Box(c).Height(16)

	a.sessFileCards(c, t)
	ui.Box(c).Height(20)

	a.sessMessageFooter(c, t)
	ui.Box(c).Height(6)

	a.sessUserBubble(c, t, "选择a，不指定字体，用系统的，单独启动一个实例，然后探测，要整全了")
	ui.Box(c).Height(6)

	a.sessUserFooter(c, t, "12:28")
	ui.Box(c).Height(16)

	a.sessStopped(c, t)
	ui.Box(c).Height(16)

	a.sessToolSummary(c, t)
	ui.Box(c).Height(6)

	a.sessErrorNote(c, t)
}

// sessClipCard draws the clipped image tail at the top of the transcript:
// a light rounded card holding the "logo B" preview.
func (a *App) sessClipCard(c *ui.Context, t *design.Tokens) {
	ui.Box(c).Width(640).Height(50).Background(t.BgSidebar).Radius(design.RadiusLG).
		Clip().Children(func() {
		ui.Row(c).Padding(0, 0, 0, 29).Gap(4).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Width(41).Height(12).Radius(10).Background(t.White).
				Border(1, sessCardLn).Center().Children(func() {
				ui.Box(c).Width(20).Height(9).Radius(4).Background(ui.Hex("#5B4BFB"))
			})
			ui.Text(c, "ZeoKeel").FontSize(28).FontWeight(600).TextColor(t.LabelPrimary).
				LetterSpacing(1.2).FixedLineHeight(30).Width(140).NoWrap().
				Margin(-24, 0, 0, 0)
			ui.Box(c).Width(105).Height(10).Radius(4).Background(ui.Hex("#F1F3F5"))
		})
	})
}

// sessBody is one line of 14/24 markdown body text.
func (a *App) sessBody(c *ui.Context, t *design.Tokens, line string) {
	a.sessTextLine(c, t, line, 14, sessLine, -1)
}

// sessTextLine draws one line of text in a fixed-height row, nudged by dy
// DIPs so its ink sits on the baseline's raster.
func (a *App) sessTextLine(c *ui.Context, t *design.Tokens, line string, size, height, dy float32) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(height).AlignItems(ui.Center).Children(func() {
		ui.Text(c, line).FontSize(size).FixedLineHeight(height).TextColor(t.LabelPrimary).
			MaxLines(1).Ellipsis("…").Margin(dy, 0, 0, 0)
	})
}

// sessHeading is an h2: 19/28 bold.
func (a *App) sessHeading(c *ui.Context, t *design.Tokens, line string) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(28).AlignItems(ui.Center).Children(func() {
		ui.Text(c, line).FontSize(19).FixedLineHeight(28).FontWeight(700).
			TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Margin(-2, 0, 0, 0)
	})
}

// sessNote is the small grey host note (12/18).
func (a *App) sessNote(c *ui.Context, t *design.Tokens, line string) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(18).AlignItems(ui.Center).Children(func() {
		ui.Text(c, line).FontSize(12).FixedLineHeight(18).TextColor(t.LabelSecondary).
			MaxLines(1).Ellipsis("…").Margin(-1, 0, 0, 0)
	})
}

// sessRichLine is one line of markdown body text with styled spans.
func (a *App) sessRichLine(c *ui.Context, t *design.Tokens, height, dy float32, spans ...ui.Span) {
	for i := range spans {
		spans[i].Size = 14
		if spans[i].Color == (ui.Color{}) {
			spans[i].Color = t.LabelPrimary
		}
	}
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(height).AlignItems(ui.Center).Children(func() {
		ui.RichText(c, spans...).FixedLineHeight(height).MaxLines(1).Ellipsis("…").
			Margin(dy, 0, 0, 0)
	})
}

// sessQuote is a blockquote: a 2px rule with 14px of inset text.
func (a *App) sessQuote(c *ui.Context, t *design.Tokens, line string) {
	ui.Row(c).Height(sessLine).Width(a.sessionTranscriptWidthFor(c)).
		BorderWidth(0, 0, 0, 2).BorderColor(sessFaint).
		Padding(0, 0, 0, 14).AlignItems(ui.Center).Children(func() {
		ui.Text(c, line).FontSize(14).FixedLineHeight(sessLine).TextColor(t.LabelPrimary).
			MaxLines(1).Ellipsis("…").Margin(-1, 0, 0, 0)
	})
}

// sessList is the ordered list of next steps. Markers sit in an 18px
// gutter so the text starts at the baseline's list inset.
func (a *App) sessList(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Width(a.sessionTranscriptWidthFor(c)).Children(func() {
		a.sessListItem(c, t, "1.", 48, func() {
			a.sessBody(c, t, "补齐剩余屏：设置子页（Models / Built-in plugins / Agent presets / Shortcuts / About）、插件页全部 Experimental 行、自动")
			ui.Row(c).Height(24).Width(a.sessionTranscriptWidthFor(c)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "化任务弹层、命令面板").FontSize(14).FixedLineHeight(24).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Margin(-1, 0, 0, 0)
				ui.Box(c).Width(5)
				a.sessCodePill(c, t, "/", 16)
				ui.Text(c, "、 ").FontSize(14).FixedLineHeight(24).TextColor(t.LabelPrimary).
					MaxLines(1).Ellipsis("…").Margin(-1, 0, 0, 0)
				a.sessCodePill(c, t, "@", 24)
				ui.Text(c, " 引用、附件/拖拽").FontSize(14).FixedLineHeight(24).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Margin(-1, 0, 0, 0)
			})
		})
		ui.Box(c).Height(6)
		a.sessListItem(c, t, "2.", 24, func() {
			a.sessBody(c, t, "会话态：streaming、工具卡片展开、审批卡片、提问卡片、错误态、右侧边栏（Files/Terminal/Browser）、深色主题、窄屏")
		})
		ui.Box(c).Height(6)
		a.sessListItem(c, t, "3.", 24, func() {
			ui.Row(c).Height(24).Width(a.sessionTranscriptWidthFor(c)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "逐控件写行为说明，填").FontSize(14).FixedLineHeight(24).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").Margin(-1, 0, 0, 0)
				ui.Box(c).Width(9)
				a.sessCodePill(c, t, "docs/baseline/screens/*.md", 181)
				ui.Text(c, " 的“行为”列，覆盖率达 100% 后进入 M0 编码").FontSize(14).
					FixedLineHeight(24).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").
					Margin(-1, 0, 0, 0)
			})
		})
	})
}

// sessListItem draws a list row: the marker in the 18px gutter, then the
// item content, which fn builds.
func (a *App) sessListItem(c *ui.Context, t *design.Tokens, marker string, height float32, fn func()) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(height).AlignItems(ui.Start).Children(func() {
		ui.Row(c).Width(18).Height(24).AlignItems(ui.Center).Children(func() {
			ui.Text(c, marker).FontSize(14).FixedLineHeight(24).TextColor(t.LabelPrimary).
				Width(18).TextAlign(ui.End).NoWrap().Margin(-1, 0, 0, 0)
		})
		ui.Column(c).Grow(1).Children(fn)
	})
}

// sessCodePill is an inline code span: 12px text on a light rounded
// background, at the baseline's measured width.
func (a *App) sessCodePill(c *ui.Context, t *design.Tokens, code string, width float32) {
	ui.Box(c).Width(width).Height(21).Background(sessCodeSurface(t)).Radius(design.RadiusSM).
		Padding(0, 0, 0, 5).Children(func() {
		ui.Text(c, code).FontSize(12).FixedLineHeight(21).TextColor(t.LabelPrimary).NoWrap()
	})
}

// sessFileCards is the two-up grid of file attachments below the reply.
func (a *App) sessFileCards(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(60).Gap(10).Children(func() {
		a.sessFileCard(c, t, "index.md",
			"基线文档总入口：14 个基础屏 + 12 个滚动位 + 控件清单 + 9 条行为探测记录")
		a.sessFileCard(c, t, "PLAN.md",
			"总体规划 v0.2：已落地 ZeoKeel 品牌、内置插件范围、基线文档门禁")
	})
}

// sessFileCard draws one file attachment card (417x60).
func (a *App) sessFileCard(c *ui.Context, t *design.Tokens, name, desc string) {
	ui.Row(c).Width(417).Height(60).Background(sessCardSurface(t)).Border(1, sessCardLine(t)).
		Radius(design.RadiusLG).Padding(8, 10, 8, 10).Gap(10).
		AlignItems(ui.Start).Children(func() {
		inner := ui.RGBA(255, 255, 255, 0.5)
		if t.Dark {
			inner = ui.RGBA(255, 255, 255, 0.06)
		}
		ui.Box(c).Size(40, 40).Margin(1, 0, 0, 0).Background(inner).
			Border(1, sessCardLine(t)).Radius(10).Center().Children(func() {
			ui.Box(c).Size(20, 20).Background(t.Accent).Radius(4).Center().Children(func() {
				ui.Text(c, "MD").FontSize(6).FontWeight(700).TextColor(t.OnPrimary).NoWrap()
			})
		})
		ui.Column(c).Grow(1).Padding(3, 0, 0, 0).Children(func() {
			ui.Text(c, name).FontSize(13).FixedLineHeight(20).FontWeight(500).
				TextColor(t.LabelPrimary).NoWrap()
			ui.Text(c, desc).FontSize(10).FixedLineHeight(16).TextColor(t.LabelTertiary).
				MaxLines(1).Ellipsis("...")
		})
	})
}

// sessMessageFooter is the assistant message action row: copy/reaction
// icons, the token usage chip, and the timestamp.
func (a *App) sessMessageFooter(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(28).Margin(0, 0, 0, -6).
		AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			for _, icon := range []*ui.SVG{actionCopyIcon, actionGoodIcon, actionBadIcon, sessShareIcon} {
				ui.Row(c).Size(28, 28).Center().Children(func() {
					ui.Icon(c, icon).Size(16, 16).TextColor(t.LabelTertiary)
				})
			}
		})
		ui.Box(c).Width(16)
		ui.Row(c).Padding(6, 8, 6, 8).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Data()).Size(13, 13).TextColor(t.LabelTertiary)
			ui.Text(c, a.T("session.usage_tokens", "10.1M")).FontSize(12).FixedLineHeight(20).
				TextColor(t.LabelTertiary).NoWrap().Margin(-1, 0, 0, 0)
		})
		ui.Box(c).Width(8)
		ui.Text(c, "12:26").FontSize(12).FixedLineHeight(20).TextColor(t.LabelTertiary).
			NoWrap().Margin(-1, 0, 0, 0)
	})
}

// sessUserBubble is a right-aligned user message (14/22, radius 20).
func (a *App) sessUserBubble(c *ui.Context, t *design.Tokens, text string) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Justify(ui.End).Children(func() {
		ui.Box(c).Background(sessUserSurface(t)).Radius(design.RadiusXL).
			Padding(9, 16, 11, 16).Children(func() {
			ui.Text(c, text).FontSize(14).FixedLineHeight(22).TextColor(t.LabelPrimary).
				MaxLines(1).Ellipsis("…")
		})
	})
}

// sessUserFooter is the timestamp and copy action under a user message.
func (a *App) sessUserFooter(c *ui.Context, t *design.Tokens, clock string) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(28).Justify(ui.End).Gap(8).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, clock).FontSize(13).FixedLineHeight(24).TextColor(t.LabelTertiary).
			Padding(0, 12, 0, 0).NoWrap().Margin(-6, 0, 0, 0)
		ui.Row(c).Size(28, 28).Center().Children(func() {
			ui.Icon(c, actionCopyIcon).Size(14, 14).TextColor(t.LabelTertiary)
		})
	})
}

// sessStopped is the turn status row above the tool summary.
func (a *App) sessStopped(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(33).Padding(0, 0, 8, 0).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).AlignItems(ui.Start).
		Children(func() {
			ui.Text(c, a.T("session.stopped")).FontSize(14).FixedLineHeight(24).TextColor(t.LabelTertiary).
				MaxLines(1).Ellipsis("…").Margin(1, 0, 0, 0)
		})
}

// sessToolSummary is the collapsed "Ran commands, ..." disclosure row.
func (a *App) sessToolSummary(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(16).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "›").FontSize(14).FixedLineHeight(16).TextColor(t.LabelTertiary).
			Width(16).NoWrap()
		ui.Text(c, a.T("session.tool_summary")).FontSize(14).
			FixedLineHeight(16).TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").
			Margin(1, 0, 0, 0)
	})
}

// sessErrorNote is the red stopped-turn diagnostic under the summary. It uses
// the theme's danger token, which is the baseline's #EC1313 in light and
// #F25A5A in dark, so the static renderer matches the live one.
func (a *App) sessErrorNote(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(44).Padding(2, 0, 2, 2).
		AlignItems(ui.Start).Gap(10).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(t.Danger).Margin(5, 0, 0, 0).
			AlignSelf(ui.Start)
		ui.Column(c).Grow(1).Children(func() {
			ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(20).AlignItems(ui.Center).Children(func() {
				ui.RichText(c,
					ui.Span{Text: a.T("session.task_stopped"), Size: 13, Weight: 600, Color: t.Danger},
					ui.Span{Text: a.T("session.task_stopped_detail_1"), Size: 13, Color: t.LabelSecondary},
				).FixedLineHeight(20).MaxLines(1).Ellipsis("…").Margin(1, 0, 0, 0)
			})
			ui.Row(c).Width(a.sessionTranscriptWidthFor(c)).Height(20).AlignItems(ui.Center).Children(func() {
				ui.RichText(c, ui.Span{Text: a.T("session.task_stopped_detail_2"), Size: 13, Color: t.LabelSecondary}).
					FixedLineHeight(20).MaxLines(1).Ellipsis("…").Margin(1, 0, 0, 0)
			})
		})
	})
}

// sessComposerArea is the fixed bottom block: the composer card and the
// usage footer, both centered like the transcript column.
func (a *App) sessComposerArea(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Height(sessBottomH).Padding(0, 0, 4, 0).Children(func() {
		ui.Row(c).Height(sessComposerH).Padding(0, sessGutter, 0, 0).Justify(ui.Center).
			Children(func() {
				a.sessComposer(c, t)
			})
		a.sessUsage(c, t)
	})
}

// sessComposer is the message input surface (877x98, radius 28).
func (a *App) sessComposer(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Width(a.sessionComposerWidthFor(c)).Height(sessComposerH).Background(t.BgRaised).
		Label("Session composer").
		Radius(28).
		Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.06)).
		Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.03)).
		Shadow(0, 0, 24, 0, ui.RGBA(0, 0, 0, 0.03)).
		Padding(8, 0, 0, 0).Gap(12).Children(func() {
		ui.Row(c).Height(36).Padding(4, 8, 0, 14).AlignItems(ui.Start).Children(func() {
			ui.Text(c, a.T("session.composer_placeholder")).
				FontSize(14).FixedLineHeight(24).TextColor(sessFaint).NoWrap().
				Margin(-1, 0, 0, 0)
		})
		ui.Row(c).Height(42).Padding(0, 8, 4, 8).Gap(12).AlignItems(ui.Center).
			Children(func() {
				// "Add files or run commands".
				ui.Row(c).Size(28, 28).Background(sessPlusSurface(t)).Radius(design.RadiusMax).
					Center().Children(func() {
					ui.Icon(c, icons.Plus()).Size(15, 15).TextColor(t.LabelSecondary)
				})
				// Permission selector.
				ui.Row(c).Height(28).Padding(0, 4, 0, 8).Gap(2).AlignItems(ui.Center).
					Margin(2, 0, 0, 0).Children(func() {
					ui.Icon(c, homeIconShield).Size(14, 14).TextColor(t.LabelSecondary)
					ui.Text(c, a.T("settings.workspace_write")).FontSize(13).FixedLineHeight(20).
						FontWeight(500).TextColor(t.LabelSecondary).Width(110).NoWrap()
					ui.Text(c, "⌄").FontSize(11).TextColor(sessFaint).Width(12).NoWrap()
				})
				ui.Box(c).Grow(1)
				// Model selector: name, reasoning budget badge, effort level.
				ui.Row(c).Height(28).Padding(0, 4, 0, 8).Gap(4).AlignItems(ui.Center).
					Margin(2, 12, 0, 0).Children(func() {
					ui.Text(c, "DeepSeek-V4.1-Flash").FontSize(13).FixedLineHeight(20).
						TextColor(t.LabelSecondary).Width(138).NoWrap()
					ui.Box(c).Background(t.Accent.Alpha(0.10)).Radius(design.RadiusMax).
						Padding(1, 8, 1, 8).Children(func() {
						ui.Text(c, "1017").FontSize(11).FixedLineHeight(17).FontWeight(500).
							TextColor(t.Accent).NoWrap()
					})
					ui.Text(c, a.T("common.high")).FontSize(13).FixedLineHeight(20).TextColor(sessFaint).
						Width(30).NoWrap()
					ui.Text(c, "⌄").FontSize(11).TextColor(sessFaint).Width(14).NoWrap()
				})
				// Disabled send button: the same theme-aware fill and glyph
				// the live composers use.
				ui.Row(c).Size(34, 34).Background(sendIdleFill(t)).Radius(design.RadiusMax).
					Label(a.T("composer.send_message")).Role(ui.RoleButton).
					Center().AlignSelf(ui.Start).Children(func() {
					ui.Text(c, "↑").FontSize(17).FontWeight(600).TextColor(sendIdleGlyph(t)).NoWrap()
				})
			})
	})
}

// sessUsage is the run-stats footer under the composer.
func (a *App) sessUsage(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(sessUsageH).Padding(4, sessGutter, 0, 0).Justify(ui.Center).
		Label("Session usage").
		Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				a.sessUsageItem(c, t, icons.Reload(), a.T("session.usage_turns"))
				a.sessUsageItem(c, t, icons.Data(), a.T("session.usage_cache"))
				a.sessUsageItem(c, t, icons.Clock(), a.T("session.usage_context"))
			})
		})
}

// sessUsageItem is one icon + label group of the usage footer.
func (a *App) sessUsageItem(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string) {
	ui.Row(c).Padding(1, 8, 1, 8).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).Size(13, 13).TextColor(t.LabelTertiary)
		ui.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.LabelTertiary).
			NoWrap().Margin(-2, 0, 0, 0)
	})
}
