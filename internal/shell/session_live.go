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
			// The Trajectory tab draws the same body frame as Chat: one main
			// column holding the pane above the composer. Sharing the frame
			// keeps the composer's element identity (and its one-shot
			// auto-focus) stable while the tab switches; the trajectory layout
			// is otherwise the pane over the composer, exactly as before.
			a.sessionBody(c, func() {
				ui.Column(c).Key("session-trajectory").Grow(1).Children(func() {
					a.trajectoryPane(c, t, snap)
				})
			}, func() {
				a.liveComposerArea(c, t, snap)
			}, nil)
			return
		}
		// The chat body: the transcript and the composer share one main
		// column, and the Todo/Team panels dock beside it for the row's whole
		// height and run to the window bottom like the right sidebar, so the
		// chat column no longer leaves a blank gap under its right edge.
		a.sessionBody(c, func() {
			ui.Column(c).Key("session-chat").Grow(1).Children(func() {
				a.liveTranscript(c, t, snap)
			})
		}, func() {
			a.liveComposerArea(c, t, snap)
		}, func() {
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
	})
}

// sessionBody draws the session's main column between the header and the
// window bottom: the tab's content column above the composer, and, when the
// chat docks them, the Todo/Team side panels beside both. The same keyed frame
// is used by Chat and Trajectory (their content differs by key), so the
// composer keeps one element identity across tab switches instead of being
// rebuilt — which would re-fire its one-shot auto-focus and steal focus from
// the tab.
func (a *App) sessionBody(c *ui.Context, content, composer, panels func()) {
	ui.Row(c).Key("session-body").Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Key("session-main").Grow(1).Children(func() {
			content()
			composer()
		})
		if panels != nil {
			panels()
		}
	})
}

// liveHeaderPanelIcon is the artwork of the header's right-sidebar control,
// matching the panel it opens.
var liveHeaderPanelIcon = chromeIconPanelRight

// liveHeader is the brand header: one title row (title, mode chip, phase
// label and the screen actions) over the Chat/Trajectory tabs.
// All spacing and heights come from the theme's 4 DIP grid; the phase label
// keeps its color/content cross-fade while the actions are native buttons.
func (a *App) liveHeader(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	th := t.Theme()
	title := a.T("session.new_session")
	if a.Ctrl != nil {
		if sess, err := a.Ctrl.CurrentSession(); err == nil && strings.TrimSpace(sess.Title) != "" {
			title = sess.Title
		}
	}
	ui.Column(c).Padding(th.Space(2), th.Space(4), 0, th.Space(4)).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Row(c).Height(t.Scale(th.Space(8))).AlignItems(ui.Center).Children(func() {
			titleEl := design.Apply(ui.Text(c, title), t.Font(design.TypeBody))
			titleEl.FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			ui.Box(c).Width(th.Space(2))
			// The workspace the agent actually works in: the session screen had
			// no visible workspace indicator beyond the Files breadcrumb.
			a.sessionWorkspaceChip(c, t)
			mst := a.modeUI()
			modeText := a.agentModeLabel(a.currentAgentMode())
			if a.currentAgentMode() == "standard" {
				modeText = a.T("common.standard_mode")
			}
			mode := homeChip(c, t).
				Label(modeText).Value(modeText).Role(ui.RolePopUpButton).Focusable()
			mode.Children(func() {
				ui.Icon(c, sessModeIcon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelTertiary)
				modeTextEl := design.Apply(ui.Text(c, modeText), t.Font(design.TypeXS))
				modeTextEl.TextColor(t.LabelSecondary).NoWrap()
				ui.Icon(c, homeIconChevron).Size(th.Space(3), th.Space(3)).TextColor(t.LabelTertiary)
			})
			mode.Expanded(mst.open)
			mode.OnClick(func() { mst.open = !mst.open })
			ui.Popover(c, mode, &mst.open, func() { a.agentModeMenu(c, t, &mst.open) })
			// No status text while idle: the header is title, mode and
			// actions only. Running/waiting/failed/paused/stopped get the
			// cross-fading phase label.
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
				brandIconButton(c, t, liveHeaderPanelIcon, panelLabel, func() {
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

// liveTranscript is the scrollable transcript and its turn rail. It renders
// the session's record order (see buildTranscriptTurns): a turn's process
// block (summary line and cards) sits between the user message and the
// assistant answer it produced, so a turn that runs tools before answering
// reads top to bottom as user → tools → answer → completion footer. A session
// with several turns keeps each turn's tools inside that turn instead of
// stacking them below the latest answer. Snapshots without a Seq axis
// (hand-built fixtures) keep the legacy layout: all messages, then the
// session's tool block.
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
			// The transcript column is the brand column shared with the
			// composer: one centered measure of comfortable reading width.
			ui.Row(c).Padding(0, design.Space2).Justify(ui.Center).
				AlignItems(ui.Start).Children(func() {
				ui.Column(c).Width(a.sessionTranscriptWidthFor(c)).
					Label(a.tr("Session transcript", "会话记录")).
					Padding(design.Space6, 0).Gap(design.Space5).Children(func() {
					if transcriptEmpty(snap) {
						a.liveTranscriptEmpty(c, t)
						return
					}
					if transcriptOrderEnabled(snap) {
						turns = a.liveOrderedTranscript(c, t, snap, streaming)
						if snap.Phase == state.PhaseRunning {
							a.workingIndicator(c, t)
						}
					} else {
						turns = a.liveFlatTranscript(c, t, snap, streaming)
						if snap.Phase == state.PhaseRunning {
							a.workingIndicator(c, t)
						}
						a.liveTools(c, t, snap)
					}
					a.turnOutcomeMarker(c, t, snap)
					a.liveWorkspaceChanges(c, t, snap)
					a.liveArtifacts(c, t)
					// The record-ordered transcript renders each error inside
					// the turn that recorded it (see bucketTranscriptErrors).
					// Only snapshots without the Seq axis fall back to the
					// legacy session-tail error line.
					if snap.LastError != nil && !transcriptOrderEnabled(snap) {
						a.liveErrorCard(c, t, *snap.LastError)
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

// transcriptEmpty reports whether the Chat tab has nothing to show yet: no
// messages, no tool calls, no recorded error and no terminal turn. The
// composer is still the next action, so the empty state invites the first
// message instead of leaving a blank pane (UX-02). A running turn never counts
// as empty, because the working indicator and any first tokens belong on
// screen.
func transcriptEmpty(snap *state.Snapshot) bool {
	if snap == nil {
		return false
	}
	if snap.Phase == state.PhaseRunning || snap.Phase == state.PhaseWaitingApproval ||
		snap.Phase == state.PhaseWaitingUser {
		return false
	}
	return len(snap.Messages) == 0 && len(snap.Tools) == 0 &&
		snap.LastError == nil && len(snap.TurnMarks) == 0 &&
		snap.TurnReason == state.ReasonNone
}

// liveTranscriptEmpty draws the Chat tab's first-run empty state: the icon,
// a one-line invitation and the two composer hints, plus the Settings > Models
// CTA when no agent is configured. It reuses the Trajectory pane's empty-state
// language (small muted copy on the brand column) so every empty pane in the
// session reads the same way.
func (a *App) liveTranscriptEmpty(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Justify(ui.Center).PaddingY(design.Space8).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(th.Space(3)).Children(func() {
			ui.Row(c).Size(th.Space(10), th.Space(10)).Center().
				Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
				ui.Icon(c, icons.NewChat()).Size(th.Space(4), th.Space(4)).
					TextColor(t.LabelSecondary)
			})
			ui.Text(c, a.T("session.empty_title")).FontSize(15).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Text(c, a.T("session.empty_hint")).FontSize(12).
				FixedLineHeight(18).TextColor(t.LabelTertiary).MaxLines(2)
			if a.Ctrl != nil && !a.Ctrl.HasAgent() {
				ui.Text(c, a.T("session.empty_no_model")).FontSize(12).
					FixedLineHeight(18).TextColor(t.WarningText).MaxLines(2)
				button := design.Apply(
					ui.Button(c, a.T("session.empty_add_model")),
					t.PrimaryButton()).Height(th.Space(8)).PaddingX(design.Space4)
				if button.Clicked() {
					a.shellUI().settingsTab = settingsTabModels
					a.openSettings()
				}
			}
		})
	})
}

// turnMark renders one turn's terminal marker (stopped/failed/interrupted) at
// the end of that turn's transcript group. It is a no-op for completed turns
// and for hand-built fixtures that carry no marks.
func (a *App) turnMark(c *ui.Context, t *design.Tokens, snap *state.Snapshot, userMsgID string) {
	if snap == nil || userMsgID == "" || snap.Phase == state.PhaseRunning {
		return
	}
	label := a.turnMarkLabel(snap.TurnMarks[userMsgID])
	if label == "" {
		return
	}
	ui.Text(c, label).FontSize(12).FixedLineHeight(18).
		TextColor(t.LabelTertiary).NoWrap()
}

// turnMarkLabel localizes one terminal turn reason.
func (a *App) turnMarkLabel(reason state.StatusReason) string {
	switch reason {
	case state.ReasonStopped:
		return a.T("session.stopped")
	case state.ReasonInterrupted:
		return a.T("session.interrupted")
	case state.ReasonFailed:
		return a.T("session.turn_failed")
	default:
		return ""
	}
}

// turnOutcomeMarker is the transcript's closing line for a turn that did not
// complete normally. It is the compatibility path for sessions persisted before
// per-turn TurnEnd markers existed: without any marks only the latest terminal
// reason is known, so it is shown once at the end of the transcript. A stopped
// or interrupted turn has no assistant answer of its own and would otherwise be
// silent; a failed turn keeps its error line instead.
func (a *App) turnOutcomeMarker(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if snap == nil || snap.Phase == state.PhaseRunning || len(snap.TurnMarks) > 0 {
		return
	}
	// A recorded error already renders as the productized liveErrorCard; only
	// the silent terminal reasons need a marker here (PD-09).
	if snap.TurnReason == state.ReasonFailed {
		return
	}
	label := ""
	switch snap.TurnReason {
	case state.ReasonStopped, state.ReasonInterrupted:
		label = a.turnMarkLabel(snap.TurnReason)
	}
	if label == "" {
		return
	}
	ui.Text(c, label).FontSize(12).FixedLineHeight(18).
		TextColor(t.LabelTertiary).NoWrap()
}

// liveOrderedTranscript draws the Seq-ordered transcript body and returns the
// number of user turns it rendered (the turn rail's count). The working
// indicator and the artifacts stay the caller's business: the indicator goes
// after this body, the artifacts after the transcript column.
func (a *App) liveOrderedTranscript(c *ui.Context, t *design.Tokens, snap *state.Snapshot, streaming int) int {
	st := a.liveMenus()
	turns := 0
	plans := buildTranscriptTurns(snap)
	preambleErrors, turnErrors := bucketTranscriptErrors(snap, plans)
	// Errors recorded before the first user message belong above the first turn.
	a.liveTranscriptErrors(c, t, preambleErrors)
	for pi, plan := range plans {
		var turnUserMsgID string
		for _, entry := range plan.entries {
			if entry.tools != nil {
				a.liveToolSummary(c, t, entry.tools)
				continue
			}
			i := entry.msgIndex
			m := snap.Messages[i]
			el := a.liveMessage(c, t, m, transcriptMessageKey(m, i), i == streaming, snap)
			if m.Role == state.RoleUser {
				turns++
				turnUserMsgID = m.MsgID
				if st.pendingTurn == turns {
					el.ScrollIntoView()
					st.pendingTurn = 0
				}
			}
		}
		// A failure belongs after everything its turn produced and before the
		// next user message, so a later prompt never pushes an old error below
		// itself.
		a.liveTranscriptErrors(c, t, turnErrors[pi])
		// The per-turn marker closes the whole turn group (after its process
		// block and answer), anchored by the turn's user message.
		a.turnMark(c, t, snap, turnUserMsgID)
	}
	return turns
}

// liveTranscriptErrors draws one bucket of recorded errors as danger lines.
func (a *App) liveTranscriptErrors(c *ui.Context, t *design.Tokens, errs []state.ErrorInfo) {
	for _, info := range errs {
		a.liveErrorCard(c, t, info)
	}
}

// bucketTranscriptErrors assigns every recorded error to the turn that was
// active when it was folded, using the user message Seq that starts each turn
// plan. Errors recorded before the first user message go to the preamble
// bucket; an error with an unknown/zero position (hand-built snapshot) follows
// the last turn. This is what keeps a failed turn's error above the user
// message that came after it, both live and after reopening the session.
func bucketTranscriptErrors(snap *state.Snapshot, plans []turnPlan) (preamble []state.ErrorInfo, perTurn [][]state.ErrorInfo) {
	if snap == nil {
		return nil, nil
	}
	perTurn = make([][]state.ErrorInfo, len(plans))
	if len(snap.Errors) == 0 {
		return nil, perTurn
	}
	turnSeqs := make([]uint64, len(plans))
	for pi, plan := range plans {
		for _, entry := range plan.entries {
			if entry.msgIndex < 0 || entry.msgIndex >= len(snap.Messages) {
				continue
			}
			m := snap.Messages[entry.msgIndex]
			if m.Role == state.RoleUser {
				turnSeqs[pi] = m.Seq
				break
			}
		}
	}
	for _, info := range snap.Errors {
		// The last turn whose user message starts at or before the error owns
		// it; plans are in record order.
		owner := -1
		for pi := range plans {
			if turnSeqs[pi] > 0 && turnSeqs[pi] <= info.Seq {
				owner = pi
			}
		}
		if owner < 0 {
			preamble = append(preamble, info)
			continue
		}
		perTurn[owner] = append(perTurn[owner], info)
	}
	return preamble, perTurn
}

// liveFlatTranscript is the legacy layout for snapshots without a Seq axis:
// every message in list order. liveTranscript appends the session's whole tool
// block after it, matching the layout the renderer had before record ordering.
func (a *App) liveFlatTranscript(c *ui.Context, t *design.Tokens, snap *state.Snapshot, streaming int) int {
	st := a.liveMenus()
	turns := 0
	currentTurnUserMsgID := ""
	for i, m := range snap.Messages {
		if m.Text == "" {
			continue
		}
		el := a.liveMessage(c, t, m, transcriptMessageKey(m, i), i == streaming, snap)
		if m.Role == state.RoleUser {
			// Close the previous turn's marker before the next user message.
			a.turnMark(c, t, snap, currentTurnUserMsgID)
			currentTurnUserMsgID = m.MsgID
			turns++
			if st.pendingTurn == turns {
				el.ScrollIntoView()
				st.pendingTurn = 0
			}
		}
	}
	a.turnMark(c, t, snap, currentTurnUserMsgID)
	return turns
}

// transcriptMessageKey is the element key of one message: the MsgID when the
// snapshot names it, the index otherwise, so a hand-built message keeps a
// distinct and stable key.
func transcriptMessageKey(m state.Message, index int) string {
	if m.MsgID == "" {
		return "msg-index:" + strconv.Itoa(index)
	}
	return "msg:" + m.MsgID
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
// with a comfortable hit target.
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
// carries its action footer below it.
func (a *App) liveMessage(c *ui.Context, t *design.Tokens, m state.Message, key string, streaming bool, snap *state.Snapshot) ui.Element {
	th := t.Theme()
	if m.Role == state.RoleUser {
		col := ui.Column(c).Key(key).Gap(design.Space1).AlignItems(ui.End).
			Transition(design.MessageTransition())
		col.Children(func() {
			bubble := ui.Column(c).MaxWidthPercent(82)
			bubble.Children(func() {
				ui.Text(c, m.Text).FontSize(a.ConversationFontSize()).
					TextColor(t.LabelPrimary).
					Background(t.Accent.Alpha(0.10).Over(t.BgRaised)).
					Radius(th.Radius).
					Padding(th.Space(2.5), th.Space(3.5))
			})
			if len(m.Attachments) > 0 {
				a.messageAttachmentChips(c, t, snap.SessionID, m.Attachments)
			}
		})
		return col
	}
	col := ui.Column(c).Key(key).Gap(design.Space1).
		Transition(design.MessageTransition())
	col.Children(func() {
		body := ui.Column(c).Gap(design.Space2)
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
		body.Children(func() { a.markdownMessage(c, t, m.Text, streaming) })
		if !streaming {
			a.liveMessageActions(c, t, m, key, snap)
		}
	})
	return col
}

// workingIndicator is the working row shown while a turn runs: a spinner and
// the elapsed time, with the three-dot reveal on the motion scale's pulse
// cycle. The row fades in over the semantic enter animation.
func (a *App) workingIndicator(c *ui.Context, t *design.Tokens) {
	elapsed, running := a.live().runningFor(c.Now(), true)
	if !running {
		return
	}
	label := a.workingLabel(elapsed)
	th := t.Theme()
	ui.Row(c).Key("working").Height(t.Scale(th.Space(7))).AlignItems(ui.Center).Gap(design.Space2).
		Transition(design.IndicatorTransition()).Children(func() {
		// The spinner repaints without rebuilding the view, as MyGo's
		// spinners do; the text changes on the indicator's own clock.
		ui.Spinner(c).Size(14, 14).Label(a.T("session.working_indicator"))
		labelEl := design.Apply(ui.Text(c, label), t.Font(design.TypeXXS))
		labelEl.FontWeight(500).TextColor(t.LabelSecondary).NoWrap().
			ContentTransition(ui.ContentTransition{Animation: &design.AnimFast})
	})
	// Wake at the next dot boundary: the indicator reveals a dot at a third
	// and two thirds of its cycle, step-end.
	step := design.DurationPulse / 3
	c.After(step - elapsed%step)
}

// liveTools draws every tool call as one process block. It is the legacy
// placement for snapshots without a Seq axis: the caller already drew all
// messages, so this block appends after them.
func (a *App) liveTools(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if len(snap.Tools) == 0 {
		return
	}
	a.liveToolSummary(c, t, snapshotToolStates(snap))
}

// liveComposerArea is the fixed bottom block: the composer card, the menus
// above it and the usage footer, all in one centered brand column.
func (a *App) liveComposerArea(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	th := t.Theme()
	ui.Column(c).PaddingY(th.Space(2)).Children(func() {
		ui.Row(c).Justify(ui.Center).Children(func() {
			ui.Column(c).Width(a.sessionComposerWidthFor(c)).Gap(th.Space(1)).Children(func() {
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
				a.sessionComposerCard(c, t, snap)
				a.liveUsageFooter(c, t, snap)
			})
		})
	})
}

// sessionComposerCard is the Session composer: the same brand card as Home
// (Panel surface, theme corner, one soft shadow, 4 DIP padding) with a native
// multiline TextArea and a toolbar of native controls. The card carries the
// input's focus and hover border; the TextArea keeps the native editing
// semantics (/ commands, @ mentions, Enter/Shift+Enter handled by
// composerTextKey).
func (a *App) sessionComposerCard(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	th := t.Theme()
	width := a.sessionComposerWidthFor(c)
	card := brandComposerCard(c, t, width, a.tr("Session composer", "会话输入框"))
	card.Children(func() {
		input := brandComposerInput(c, t, &a.draft,
			a.T("session.composer_input"), a.T("home.composer_placeholder"),
			a.composerTextKey(c, false), &a.composerRef)
		input.AutoFocus()
		a.recordComposerCaret(input)
		card = brandComposerFocus(card, input, t)
		ui.Row(c).AlignItems(ui.Center).Gap(th.Space(1)).Children(func() {
			brandIconButton(c, t, icons.Plus(), a.T("composer.add_files_or_run_commands"),
				func() { a.attachOpen = !a.attachOpen })
			ui.Box(c).Width(th.Space(1))
			a.livePermissionChip(c, t)
			a.composerPlanBadge(c, t)
			ui.Spacer(c).Grow(1)
			a.liveModelMenuBadge(c, t)
			ui.Box(c).Width(th.Space(2))
			a.liveRunButton(c, t, snap)
		})
	})
}

// approvalCardsWithAlways draws one card per pending approval. Allow and Deny
// decide only this call; Always allow also records the tool in the controller's
// approval queue so later turns run it without a prompt. The buttons call the
// controller, which removes the request from the pending set.
func (a *App) approvalCardsWithAlways(c *ui.Context, t *design.Tokens, requests []state.ApprovalRequest) {
	th := t.Theme()
	for _, request := range requests {
		a.menuFrame(c, t, a.sessionComposerWidthFor(c), func() {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				reqLabel := design.Apply(ui.Text(c, a.T("composer.approval_required")), t.Font(design.TypeXXS))
				reqLabel.TextColor(t.LabelTertiary).Grow(1)
				reqName := design.Apply(ui.Text(c, request.Name), t.Font(design.TypeXS))
				reqName.FontWeight(600).TextColor(t.LabelPrimary)
			})
			argsText := design.Apply(ui.Text(c, compactArgs(request.Args)), t.Font(design.TypeXXS))
			argsText.TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("...")
			// The automatic reviewer recommended denial: keep the reason on the
			// card so the user can override it with full context. The buttons
			// below keep their meaning (Allow runs the call, Deny rejects it).
			if reason := strings.TrimSpace(request.ReviewReason); reason != "" {
				reviewNote := design.Apply(ui.Text(c, a.menuText(
					"Auto review suggested denial: ", "自动审查建议拒绝：")+reason),
					t.Font(design.TypeXXS))
				reviewNote.TextColor(t.WarningText).MaxLines(3).Ellipsis("…")
			}
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
				deny := ui.Button(c, a.T("composer.deny")).Height(th.Space(9)).
					PaddingX(th.Space(4)).Radius(design.RadiusSM).
					Border(1, t.BorderL2).Background(t.BgBase).TextColor(t.Danger)
				if deny.Clicked() && a.Ctrl != nil {
					a.Ctrl.Deny(request.CallID)
				}
			})
		})
	}
}

// liveRunButton is the composer's primary control: Send while idle, Stop
// while a turn runs, through the shared brand send button. With an empty draft
// and no running turn the control is disabled (skipped by keyboard focus and
// unable to submit) and shows the brand idle tint instead of the retired pale
// blue.
func (a *App) liveRunButton(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	running := snap.Phase == state.PhaseRunning
	empty := !running && strings.TrimSpace(a.draft) == ""
	label := a.T("session.send")
	if running {
		label = a.T("session.stop")
	}
	brandSendButton(c, t, label, empty, running, func() {
		if a.Ctrl == nil {
			return
		}
		if running {
			a.stopTurn()
			return
		}
		a.sendSessionDraft(c)
	})
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
	a.submitComposerText(c, text, alternate)
}

// submitComposerText is the composer's plain send path: it sends text that is
// not a slash command. /plan's optional message uses it too, so a running turn
// follows the persisted Send behavior (Queue keeps the text until the turn
// ends; Interrupt stops the turn and sends immediately) and a failed submit
// shows one shared toast.
func (a *App) submitComposerText(c *ui.Context, text string, alternate bool) {
	if a.Ctrl == nil {
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
			// cancelled turn to finish unwinding so it does not race it. The
			// turn's asynchronous team members are stopped with it.
			a.stopTurn()
			a.submitWhenIdle(id, text)
			a.toastDeferred(c, a.menuText("Stopped the turn and sent the message", "已停止当前回合并发送消息"))
			return
		}
		a.queueDraft(id, text)
		a.toastDeferred(c, a.menuText("Message queued until the turn finishes", "消息已排队，将在回合结束后发送"))
		return
	}
	if err := a.Ctrl.Submit(context.Background(), id, text); err != nil {
		if errors.Is(err, session.ErrNoAgent) {
			a.toastDeferred(c, a.menuText("No agent configured. Add a model in Settings > Models.",
				"尚未配置 Agent。请在 设置 > 模型 中添加模型。"))
		} else {
			a.toastDeferred(c, fmt.Sprintf(a.menuText("Could not send: %v", "发送失败：%v"), err))
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
