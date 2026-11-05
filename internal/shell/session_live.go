package shell

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// sessionRenderState is the renderer's own state for one App: where the
// transcript is scrolled, whether its scroll range has been measured, and
// when each message first showed. It lives here rather than in App because
// app.go is not part of this change; only the view goroutine touches it, as
// motion.go's state does.
type sessionRenderState struct {
	scroll        ui.ScrollState
	scrollChecked bool
	seen          map[string]time.Time
}

// sessionRenderStates maps each App to its render state, keyed the same way
// motion.go and home_live_menus.go key theirs.
var sessionRenderStates sync.Map // map[*App]*sessionRenderState

// renderState returns the render state of a, making it on first use.
func (a *App) renderState() *sessionRenderState {
	if s, ok := sessionRenderStates.Load(a); ok {
		return s.(*sessionRenderState)
	}
	s := &sessionRenderState{seen: make(map[string]time.Time)}
	actual, _ := sessionRenderStates.LoadOrStore(a, s)
	return actual.(*sessionRenderState)
}

// messageShownAt returns when the message key first appeared in a frame,
// recording now on first sight. It is the action bar's time label: snapshots
// carry no timestamp, so the view's own clock is the only honest source.
func (a *App) messageShownAt(key string, now time.Time) time.Time {
	st := a.renderState()
	if t, ok := st.seen[key]; ok {
		return t
	}
	st.seen[key] = now
	return now
}

// sessionLive renders the session screen from the controller's snapshot:
// transcript, streaming state, usage and the composer.
func (a *App) sessionLive(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	a.observeTurnTiming(c.Now(), snap)
	a.flushQueuedDraft(snap)
	if snap.Phase != state.PhaseRunning {
		// End the working indicator's clock whenever the turn is not
		// running, so the next one counts from zero.
		a.live().runningFor(c.Now(), false)
	}
	design.Apply(ui.Column(c), t.Main()).Children(func() {
		a.liveHeader(c, t, snap)
		// Turning Show coding view off removes the Trajectory tab; if it was
		// selected, fall back to Chat instead of rendering a hidden tab.
		if !a.currentSettings().ShowCodingView && a.uiState().tab == int(liveTrajectory) {
			a.uiState().tab = int(liveChat)
		}
		if a.uiState().tab == int(liveChat) {
			a.teamGoalBanner(c, t)
		}
		if a.uiState().tab == int(liveTrajectory) {
			a.trajectoryPane(c, t, snap)
			a.liveComposerArea(c, t, snap)
			return
		}
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			a.liveTranscript(c, t, snap)
			// The panels are fixed-width columns; at narrow widths (or with
			// the right sidebar docked) hiding them keeps the transcript and
			// the panel boundaries intact instead of overflowing the row.
			todoVisible, teamVisible := a.sidePanelsVisible(c)
			if teamVisible {
				a.teamPanel(c, t)
			}
			if todoVisible {
				a.liveTodoPanel(c, t)
			}
		})
		a.liveComposerArea(c, t, snap)
	})
}

// liveHeaderPanelIcon is the artwork of the header's right-sidebar control.
// It is the right-divider panel icon, matching the panel itself and the
// baseline; PanelLeft put the divider on the wrong side.
var liveHeaderPanelIcon = chromeIconPanelRight

// liveHeader is the static header's 76px block: a 30px title row, the 10px
// gap and the 25px Chat/Trajectory tabs. Reusing those metrics keeps the live
// header's bottom border at y=75 like baseline 61-session-chat, instead of the
// 6px-taller 48+34 header the live path used to paint. The phase label
// cross-fades its text and its color, as DSH's .1s/.12s state labels do.
func (a *App) liveHeader(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	title := a.T("session.new_session")
	if a.Ctrl != nil {
		if sess, err := a.Ctrl.CurrentSession(); err == nil && strings.TrimSpace(sess.Title) != "" {
			title = sess.Title
		}
	}
	ui.Column(c).Height(sessHeaderH).Padding(10, 12, 0, 20).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL3).Children(func() {
		ui.Row(c).Height(30).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(14).FixedLineHeight(20).FontWeight(500).
				TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…").PaddingX(8).Margin(-1, 0, 0, 0)
			ui.Box(c).Width(10)
			mst := a.modeUI()
			modeText := a.agentModeLabel(a.currentAgentMode())
			if a.currentAgentMode() == "standard" {
				modeText = a.T("common.standard_mode")
			}
			mode := ui.Row(c).Height(22).Gap(2).AlignItems(ui.Center).
				Label(modeText).Value(modeText).Role(ui.RolePopUpButton).Focusable()
			mode.Children(func() {
				ui.Row(c).Size(16, 16).Center().Children(func() {
					ui.Icon(c, sessModeIcon).Size(14, 14).TextColor(t.LabelTertiary)
				})
				ui.Text(c, modeText).FontSize(12).
					FixedLineHeight(22).TextColor(t.LabelTertiary).NoWrap()
			})
			mode.Expanded(mst.open)
			mode.OnClick(func() { mst.open = !mst.open })
			ui.Popover(c, mode, &mst.open, func() { a.agentModeMenu(c, t, &mst.open) })
			a.planHeaderToggle(c, t)
			// DSH shows no status text while idle: the header is title, mode
			// and actions only. Running/waiting/failed/paused/stopped still
			// get the cross-fading phase label.
			if snap.Phase != state.PhaseIdle {
				ui.Text(c, a.phaseLabel(snap.Phase)).FontSize(12).Grow(1).
					TextColor(livePhaseColor(t, snap.Phase)).
					Transition(design.ColorTransition()).
					ContentTransition(ui.ContentTransition{Animation: &design.AnimEnter})
			} else {
				ui.Box(c).Grow(1)
			}
			if a.Ctrl != nil {
				a.sessionHeaderMore(c, t)
				panelLabel := a.T("shell.open_right_sidebar")
				if a.rightOpen {
					// Distinct from the panel's own collapse control, which is
					// the one canonical "Collapse right sidebar" target.
					panelLabel = a.tr("Collapse right sidebar header", "收起右侧边栏（标题栏）")
				}
				panel := ui.Row(c).Size(28, 28).Radius(design.RadiusSM).Center().
					Label(panelLabel).Role(ui.RoleButton).Focusable()
				panel.Children(func() {
					// The control opens the panel on the right; PanelLeft's
					// divider is on the wrong side. Use the right-panel artwork
					// the Home header and the panel's own collapse control draw.
					ui.Icon(c, liveHeaderPanelIcon).Size(16, 16).TextColor(t.LabelSecondary)
				})
				panel.OnClick(func() {
					if a.rightOpen {
						a.collapseRightSidebar()
						return
					}
					a.rightOpen = true
				})
			}
		})
		a.liveTabs(c, t)
	})
}

// livePhaseColor is the phase label's color: accent while a turn runs or
// waits, danger when it failed, warning when paused, muted otherwise.
func livePhaseColor(t *design.Tokens, p state.Phase) ui.Color {
	switch p {
	case state.PhaseRunning, state.PhaseWaitingApproval, state.PhaseWaitingUser:
		return t.Accent
	case state.PhaseFailed:
		return t.Danger
	case state.PhasePaused:
		return t.WarningText
	default:
		return t.LabelTertiary
	}
}

// liveTranscript is the scrollable message column, the working indicator and
// the tool cards, in the order the session built them. With more than one
// user turn a rail of jump buttons scrolls the chosen turn into view.
func (a *App) liveTranscript(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	streaming := -1
	if snap.Phase == state.PhaseRunning {
		for i, m := range snap.Messages {
			if m.Role == state.RoleAssistant && m.Text != "" {
				streaming = i
			}
		}
	}
	st := a.liveMenus()
	rs := a.renderState()
	turns := 0
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).TrackScroll(&rs.scroll).Children(func() {
			// DSH centers an 845px message column (baseline 61-session-chat:
			// x=514 w=845 at 1600x1000), exactly like the static transcript.
			ui.Row(c).Padding(0, sessGutter, 0, 1).Justify(ui.Center).
				AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(a.sessionTranscriptWidthFor(c)).
					Label(a.tr("Session transcript", "会话记录")).
					Padding(design.Space6, 0).Gap(design.Space4).Children(func() {
					for i, m := range snap.Messages {
						if m.Text == "" {
							continue
						}
						key := "msg:" + m.MsgID
						if m.MsgID == "" {
							// A snapshot built by hand may not name its messages;
							// the index keeps their keys distinct and stable.
							key = "msg-index:" + strconv.Itoa(i)
						}
						el := a.liveMessage(c, t, m, key, i == streaming, snap)
						if m.Role == state.RoleUser {
							turns++
							if st.pendingTurn == turns {
								el.ScrollIntoView()
								st.pendingTurn = 0
							}
						}
					}
					if snap.Phase == state.PhaseRunning {
						a.workingIndicator(c, t)
					}
					a.liveTools(c, t, snap)
					a.liveArtifacts(c, t)
					if snap.LastError != nil {
						ui.Text(c, a.T("session.error_prefix", snap.LastError.Text)).FontSize(13).TextColor(t.Danger)
					}
				})
			})
		})
		if turns > 1 {
			a.turnNav(c, t, turns)
		}
		// The scroll range is only known once the content has been laid out;
		// one extra frame after the first lets the Back to bottom control
		// decide from real geometry instead of guessing.
		if !rs.scrollChecked {
			rs.scrollChecked = true
			c.AnimationFrame()
		}
		if a.backToBottomVisible() {
			button := ui.Button(c, a.menuText("Back to bottom", "回到底部")).
				Absolute().Bottom(design.Space3).Right(design.Space6+24).
				Height(28).PaddingX(design.Space3).Radius(design.RadiusMax).
				Background(t.BgBase).Border(1, t.BorderL2).
				TextColor(t.LabelSecondary).FontSize(12).
				Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.08))
			if button.Clicked() {
				// math.MaxFloat32 is the ScrollState's "end": the container
				// clamps it to the real maximum on the next frame.
				rs.scroll.Y = math.MaxFloat32
			}
		}
	})
	if st.pendingTurn > turns {
		st.pendingTurn = 0
	}
}

// backToBottomVisible reports whether the transcript is scrolled up from its
// end. MaxY is set by the container when it lays the content out, so the
// control only appears once a real range is known.
func (a *App) backToBottomVisible() bool {
	st := a.renderState()
	return st.scroll.MaxY > 1 && st.scroll.Y < st.scroll.MaxY-1
}

// turnNav is the transcript's turn rail. Each mark asks the next frame to
// scroll that turn's first message into view, so no control is decorative.
// The visible rail stays small, but every mark is a named, focusable button
// with a 24px hit target, as DSH's "Jump to turn N" controls are.
func (a *App) turnNav(c *ui.Context, t *design.Tokens, turns int) {
	ui.Column(c).Width(28).Padding(design.Space6, 0, design.Space2, 0).
		Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		for i := 1; i <= turns; i++ {
			turn := i
			label := a.menuText(
				"Jump to turn "+strconv.Itoa(turn),
				"跳转到第 "+strconv.Itoa(turn)+" 轮",
			)
			mark := ui.Row(c).Size(24, 24).Center().Radius(design.RadiusXS).
				Label(label).Role(ui.RoleButton).Focusable()
			mark.Children(func() {
				ui.Row(c).Size(18, 12).Center().Radius(3).
					Background(t.BorderL2).Children(func() {
					ui.Text(c, strconv.Itoa(turn)).FontSize(8).TextColor(t.LabelTertiary)
				})
			})
			mark.OnClick(func() { a.liveMenus().pendingTurn = turn })
		}
	})
}

// liveMessage draws one message. A message that appears fades and lifts in;
// the streaming assistant message also fades its content as tokens arrive, so
// new text is not a hard cut, and settles at exactly its rest frame. Assistant
// text goes through the Markdown renderer; a completed assistant message
// carries its DSH-style footer below it.
func (a *App) liveMessage(c *ui.Context, t *design.Tokens, m state.Message, key string, streaming bool, snap *state.Snapshot) ui.Element {
	if m.Role == state.RoleUser {
		col := ui.Column(c).Key(key).Gap(design.Space1).AlignItems(ui.End).
			Transition(design.MessageTransition())
		col.Children(func() {
			ui.Text(c, m.Text).FontSize(a.ConversationFontSize()).TextColor(t.LabelPrimary).
				Background(t.BgHover).Radius(design.RadiusMD).Padding(design.Space3)
			if len(m.Attachments) > 0 {
				a.messageAttachmentChips(c, t, snap.SessionID, m.Attachments)
			}
		})
		return col
	}
	col := ui.Column(c).Key(key).Gap(design.Space1).
		Transition(design.MessageTransition())
	col.Children(func() {
		body := ui.Column(c).Gap(10)
		if streaming {
			opacity, moving := a.live().streamShown(c.Now(), m.MsgID, m.Text)
			if c.Preferences().ReduceMotion {
				opacity, moving = 1, false
			}
			if opacity < 1 {
				body.Opacity(opacity).Translate(0, design.RiseAt(opacity, design.StreamRise))
			}
			if moving {
				c.AnimationFrame()
			}
		}
		body.Children(func() { a.markdownBlocks(c, t, m.Text) })
		if !streaming {
			a.liveMessageActions(c, t, m, key, snap)
		}
	})
	return col
}

// workingIndicator is the "Deep diving for Xm Ys" row shown while a turn
// runs: a spinner and the elapsed time, with DSH's three dots revealing on
// its 1.5s step-end cycle. The row fades in over DSH's .15s ease-out.
func (a *App) workingIndicator(c *ui.Context, t *design.Tokens) {
	elapsed, running := a.live().runningFor(c.Now(), true)
	if !running {
		return
	}
	label := a.workingLabel(elapsed)
	ui.Row(c).Key("working").Height(28).AlignItems(ui.Center).Gap(design.Space2).
		Transition(design.IndicatorTransition()).Children(func() {
		// The spinner repaints without rebuilding the view, as MyGo's
		// spinners do; the text changes on the indicator's own clock.
		ui.Spinner(c).Size(14, 14).Label(a.T("session.working_indicator"))
		ui.Text(c, label).FontSize(12).FixedLineHeight(18).FontWeight(500).
			TextColor(t.LabelSecondary).NoWrap().
			ContentTransition(ui.ContentTransition{Animation: &design.AnimFast})
	})
	// Wake at the next 0.5s dot boundary: DSH reveals a dot at 33.33% and
	// 66.66% of its 1.5s cycle, step-end.
	step := design.DurationPulse / 3
	c.After(step - elapsed%step)
}

// liveTools draws the session's tool calls, ordered by call ID so a map's
// iteration order never moves them from frame to frame.
func (a *App) liveTools(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if len(snap.Tools) == 0 {
		return
	}
	a.liveToolSummary(c, t, snap)
}

// liveComposerArea is the fixed bottom block: the composer card, the menus
// above it and the usage footer.
func (a *App) liveComposerArea(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	ui.Column(c).PaddingY(design.Space4).Children(func() {
		// The menus, composer and usage footer share DSH's 877px centered
		// column; without this the live screen painted full-width while the
		// static renderer and the baseline kept the centered column.
		ui.Row(c).Justify(ui.Center).Padding(0, sessGutter, 0, 0).Children(func() {
			ui.Column(c).Width(a.sessionComposerWidthFor(c)).Gap(design.Space2).Children(func() {
				// The plan review card comes first: while plan mode is on and a
				// "Plan" artifact exists, review is the screen's primary action.
				if plan := a.planReviewArtifact(); plan != nil {
					a.planReviewCard(c, t, *plan)
				}
				a.questionCards(c, t)
				if msg := a.modeUI().err; msg != "" {
					// A failed SetMode is persisted but not applied; surface it
					// on the live session instead of only in the Home preview.
					ui.Text(c, msg).FontSize(11).FixedLineHeight(16).
						TextColor(t.Danger).MaxLines(2).Ellipsis("…")
				}
				if pending := a.pendingApprovals(); len(pending) > 0 {
					a.approvalCardsWithAlways(c, t, pending)
				}
				if a.slashMenuVisible() {
					a.commandPalette(c, t)
				} else if entries := a.visibleMentionEntries(); len(entries) > 0 {
					a.mentionMenu(c, t, entries)
				}
				if a.attachOpen {
					a.attachmentMenu(c, t)
				}
				a.pendingAttachmentChips(c, t, snap)
				design.Apply(ui.Column(c), t.Composer()).Width(a.sessionComposerWidthFor(c)).
					Label(a.tr("Session composer", "会话输入框")).
					Padding(design.Space3, 1, design.Space3, 1).
					Gap(design.Space2).Children(func() {
					// Multiline: Enter sends, Shift+Enter inserts a line break.
					ui.TextAreaBase(c, &a.draft).Label(a.T("session.composer_input")).
						Placeholder(a.T("home.composer_placeholder")).FontSize(14).
						MinHeight(28).AutoFocus().Bind(&a.composerRef).
						OnTextKey(a.composerTextKey(c, false))
					ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
						plus := ui.Button(c, "").Tooltip(a.T("composer.add_files_or_run_commands")).
							Label(a.T("composer.add_files_or_run_commands"))
						plus.Children(func() {
							ui.Icon(c, icons.Plus()).Size(14, 14).TextColor(t.LabelSecondary)
						})
						if plus.Clicked() {
							a.attachOpen = !a.attachOpen
						}
						a.livePermissionChip(c, t)
						ui.Spacer(c).Grow(1)
						a.liveModelMenuBadge(c, t)
						a.liveRunButton(c, t, snap)
					})
				})
				a.liveUsageFooter(c, t, snap)
			})
		})
	})
}

// approvalCardsWithAlways draws one card per pending approval. Allow and Deny
// decide only this call; Always allow also records the tool in the controller's
// approval queue so later turns run it without a prompt. The buttons call the
// controller, which removes the request from the pending set.
func (a *App) approvalCardsWithAlways(c *ui.Context, t *design.Tokens, requests []state.ApprovalRequest) {
	for _, request := range requests {
		a.menuFrame(c, t, 868, func() {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.T("composer.approval_required")).FontSize(12).FixedLineHeight(18).
					TextColor(t.LabelTertiary).Grow(1)
				ui.Text(c, request.Name).FontSize(13).FixedLineHeight(20).
					FontWeight(600).TextColor(t.LabelPrimary)
			})
			ui.Text(c, compactArgs(request.Args)).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("...")
			ui.Row(c).Gap(design.Space2).Children(func() {
				allow := design.Apply(ui.Button(c, a.T("composer.allow")), t.PrimaryButton())
				if allow.Clicked() && a.Ctrl != nil {
					a.Ctrl.Approve(request.CallID)
				}
				always := design.Apply(ui.Button(c, a.T("session.always_allow")), t.PrimaryButton())
				always.Tooltip(a.T("session.always_allow_tooltip"))
				if always.Clicked() && a.Ctrl != nil {
					a.Ctrl.ApproveAlways(request.CallID)
				}
				deny := ui.Button(c, a.T("composer.deny")).Height(38).PaddingX(design.Space4).
					Radius(design.RadiusSM).Border(1, t.BorderL2).Background(t.BgBase).
					TextColor(t.Danger)
				if deny.Clicked() && a.Ctrl != nil {
					a.Ctrl.Deny(request.CallID)
				}
			})
		})
	}
}

// liveRunButton is the composer's primary control: Send while idle, Stop
// while a turn runs. The label cross-fades and the colors follow the state
// change, as DSH's composer button does. With an empty draft and no running
// turn the whole button is disabled (not just its glyph), so it is skipped by
// keyboard focus and cannot submit.
func (a *App) liveRunButton(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	running := snap.Phase == state.PhaseRunning
	empty := !running && strings.TrimSpace(a.draft) == ""
	label := a.T("session.send")
	if running {
		label = a.T("session.stop")
	}
	bg, fg := t.Accent, t.AccentText
	if empty {
		// DSH's empty composer shows the theme-aware disabled fill, not the
		// active accent.
		bg, fg = sendIdleFill(t), sendIdleGlyph(t)
	}
	button := ui.Button(c, "").Size(34, 34).Radius(design.RadiusMax).
		Background(bg).TextColor(fg).Label(label).Disabled(empty)
	button.Children(func() {
		if running {
			ui.Text(c, "■").FontSize(14).FontWeight(700).TextColor(fg)
			return
		}
		ui.Icon(c, homeIconArrowUp).Size(16, 16).TextColor(fg)
	})
	if empty || !button.Clicked() || a.Ctrl == nil {
		return
	}
	if running {
		a.Ctrl.Stop()
		return
	}
	a.sendSessionDraft(c)
}

// sendSessionDraft submits the composer's text and clears it. Enter and the
// send button share this path. While a turn runs, the persisted Send behavior
// decides: Queue keeps the text and submits it when the turn ends, Interrupt
// stops the running turn and submits immediately. Cmd/Ctrl+Enter requests the
// other behavior, as the setting's description says.
func (a *App) sendSessionDraft(c *ui.Context) { a.submitComposer(c, false) }

// submitAlternate is Cmd/Ctrl+Enter: the send behavior opposite to the
// configured one. It only applies to the session composer; the Home composer
// sends through its own path and creates a session.
func (a *App) submitAlternate(c *ui.Context) {
	switch a.Screen {
	case Home:
		// Home's composer creates a session, so both Enter forms share that
		// path instead of submitting into whatever session was last open.
		a.sendHomeDraft(c)
	case Session:
		a.submitComposer(c, true)
	}
}

// submitComposer implements both send paths.
func (a *App) submitComposer(c *ui.Context, alternate bool) {
	if a.Ctrl == nil {
		return
	}
	text := strings.TrimSpace(a.draft)
	if text == "" {
		return
	}
	// A typed slash command executes locally instead of being sent as prose;
	// the "/" palette lists these commands, so Enter must honor them too.
	if isSlashCommand(text) {
		a.runSlashCommand(text)
		return
	}
	id := a.Ctrl.CurrentSessionID()
	if id == "" {
		return
	}
	snap := a.currentSnapshot()
	if snap != nil && snap.Phase == state.PhaseRunning {
		interrupt := a.currentSettings().SendBehavior == session.SendInterrupt
		if alternate {
			interrupt = !interrupt
		}
		a.draft = ""
		if interrupt {
			// Stop marks the turn idle immediately; Submit waits for the
			// cancelled turn to finish unwinding so it does not race it.
			a.Ctrl.Stop()
			a.submitWhenIdle(id, text)
			c.Toast(a.menuText("Stopped the turn and sent the message", "已停止当前回合并发送消息"))
			return
		}
		a.queueDraft(id, text)
		c.Toast(a.menuText("Message queued until the turn finishes", "消息已排队，将在回合结束后发送"))
		return
	}
	if err := a.Ctrl.Submit(context.Background(), id, text); err != nil {
		if errors.Is(err, session.ErrNoAgent) {
			c.Toast(a.menuText("No agent configured. Add a model in Settings > Models.",
				"尚未配置 Agent。请在 设置 > 模型 中添加模型。"))
		} else {
			c.Toast(fmt.Sprintf(a.menuText("Could not send: %v", "发送失败：%v"), err))
		}
		return
	}
	a.draft = ""
}

// queueDraft remembers one message for sessionID. A second queued message
// replaces the first: the composer only holds one draft.
func (a *App) queueDraft(sessionID, text string) {
	st := a.shellUI()
	if st.queued == nil {
		st.queued = make(map[string]string)
	}
	st.queued[sessionID] = text
}

// flushQueuedDraft submits the session's queued message once the snapshot is
// no longer running. It runs on the view goroutine, so the map is not shared.
func (a *App) flushQueuedDraft(snap *state.Snapshot) {
	if snap == nil || snap.Phase == state.PhaseRunning || a.Ctrl == nil {
		return
	}
	id := snap.SessionID
	if id == "" {
		id = a.Ctrl.CurrentSessionID()
	}
	st := a.shellUI()
	text, ok := st.queued[id]
	if !ok {
		return
	}
	delete(st.queued, id)
	a.submitWhenIdle(id, text)
}

// submitWhenIdle waits for the current turn to finish unwinding, then submits
// text. It runs off the view goroutine so the frame does not block.
func (a *App) submitWhenIdle(sessionID, text string) {
	go func() {
		a.Ctrl.Wait()
		_ = a.Ctrl.Submit(context.Background(), sessionID, text)
	}()
}
