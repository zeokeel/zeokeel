package session

// The restored-session interruption notice.
//
// ChatSession.Interrupted is a read-model hint the agent backend sets when a
// restore finds a run that never finished (a crash/kill left the durable
// checkpoint in requested/tool_pending). The turn was not replayed, so the
// product says so in one quiet line above the transcript and offers the only
// two honest actions: continue in the composer (the user decides what to send)
// and dismiss the notice for this app run. The continue action names that
// destination ("在输入框继续"): a bare 继续 read as "replay the turn", while the
// action only moves the keyboard into the composer; its tooltip repeats the
// feedback for a pointer hover. The hint is not durable: the backend
// clears it when the next run starts, and the notice disappears with it.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildInterruptedNotice renders the restored-run notice above the transcript:
// a neutral status badge (an interrupted run is not a red failure), one quiet
// explanation, the 继续 action and a dismiss icon. It renders nothing for a
// normal session, while a run is starting, or after the user dismissed it.
func buildInterruptedNotice(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, session contracts.ChatSession) {
	if st == nil || session.ID == "" || !session.Interrupted || session.Running {
		return
	}
	if st.interruptedDismissed[session.ID] {
		return
	}
	ui.Row(c).FillWidth().Justify(ui.Center).PaddingY(design.Space3).Children(func() {
		ui.Row(c.Key("session.interrupted."+session.ID)).
			Width(design.TranscriptWidth).MaxWidthPercent(92).
			AlignItems(ui.Center).Gap(design.Space2).
			Padding(design.Space2, design.Space3).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(design.RadiusMD).
			Children(func() {
				uikit.StatusBadge(c, t, uikit.StatusInterrupted,
					st.tr("上次运行被中断", "Last run was interrupted"))
				design.Apply(ui.Text(c, st.tr(
					"未重放；请检查工作区状态，然后在输入框继续。",
					"The turn was not replayed; check the workspace, then continue in the composer.")),
					t.Font(design.TypeXXS)).TextColor(t.LabelSecondary).Grow(1).MaxLines(2)
				continueLabel := st.tr("在输入框继续", "Continue in the composer")
				cont := design.Apply(ui.Button(c, continueLabel), t.SecondaryButton()).
					Height(design.RowCompactHeight).Shrink(0).
					Label(continueLabel).
					Tooltip(st.tr("把光标移到输入框；发送内容由你决定",
						"Puts the cursor in the composer; you decide what to send"))
				if cont.Clicked() {
					// Continuing is the user's call: hand them the composer with
					// the history intact instead of replaying a side-effecting
					// turn on their behalf.
					st.focusComposer = true
					runtime.Invalidate()
				}
				uikit.IconButton(c, uikit.IconButtonOptions{
					Key:     "session.interrupted.dismiss." + session.ID,
					Label:   st.tr("忽略上次运行中断提示", "Dismiss the interrupted-run notice"),
					Tooltip: st.tr("忽略（仅本次隐藏）", "Dismiss (hidden until this app restarts)"),
					Icon:    icons.Close(),
					Size:    design.IconSizeS,
					OnClick: func() {
						st.interruptedDismissed[session.ID] = true
						runtime.Invalidate()
					},
				})
			})
	})
}
