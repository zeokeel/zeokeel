package shell

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// AR-08: user-initiated session opening runs off the frame thread. The handler
// starts a background read and the next frame renders the previous content with
// an "Opening session…" banner; the result is applied at the start of a later
// build. A fast store still applies within one frame, so no test or user sees a
// flash.

const sessionOpenFastPath = 25 * time.Millisecond

// sessionOpenResult is one completed background open.
type sessionOpenResult struct {
	id        string
	snap      *state.Snapshot
	err       error
	beforeWS  string
	oldScreen Screen
	oldID     string
}

// sessionOpenLoader is the injectable read seam (tests simulate a slow store).
var sessionOpenLoader func(ctx context.Context, id string) (*state.Snapshot, error)

// openSessionAsync opens id without blocking the frame. The result lands in the
// shell's channel and is applied by applySessionOpenResults on the next build;
// the wait below only covers the fast path so a quick store opens immediately.
func (a *App) openSessionAsync(c *ui.Context, id string) {
	if a.Ctrl == nil {
		a.openSession(id)
		return
	}
	st := a.shellUI()
	if st.sessionOpenResults == nil {
		st.sessionOpenResults = make(chan sessionOpenResult, 4)
	}
	if st.sessionOpeningID == id {
		return
	}
	st.sessionOpeningID = id
	beforeWS := a.Ctrl.CurrentWorkspaceID()
	oldScreen := a.draftOwnerScreen()
	oldID := a.draftOwnerSessionID()
	if c != nil {
		c.Invalidate()
	}
	done := make(chan sessionOpenResult, 1)
	go func() {
		snap, err := a.loadSession(context.Background(), id)
		done <- sessionOpenResult{id: id, snap: snap, err: err, beforeWS: beforeWS,
			oldScreen: oldScreen, oldID: oldID}
	}()
	// Fast path: a local store usually answers in a few milliseconds, so wait
	// briefly and apply inline instead of flashing a loading banner. A slow
	// store skips this and continues in the background.
	select {
	case result := <-done:
		a.applySessionOpenResult(result)
	case <-time.After(sessionOpenFastPath):
		go func() {
			result := <-done
			st := a.shellUI()
			st.sessionOpenResults <- result
			// Best-effort repaint request; the controller's update loop also
			// repaints on the next state change.
			if c != nil {
				c.Invalidate()
			}
		}()
	}
}

// loadSession reads through the injectable seam or the controller.
func (a *App) loadSession(ctx context.Context, id string) (*state.Snapshot, error) {
	if sessionOpenLoader != nil {
		return sessionOpenLoader(ctx, id)
	}
	return a.Ctrl.OpenSession(ctx, id)
}

// applySessionOpenResults drains completed background opens at the start of a
// build, so all UI-state mutation stays on the UI thread.
func (a *App) applySessionOpenResults() {
	st := a.shellUI()
	if st.sessionOpenResults == nil {
		return
	}
	for {
		select {
		case result := <-st.sessionOpenResults:
			a.applySessionOpenResult(result)
		default:
			return
		}
	}
}

// applySessionOpenResult installs one opened snapshot on the UI thread.
func (a *App) applySessionOpenResult(result sessionOpenResult) {
	st := a.shellUI()
	st.sessionOpeningID = ""
	if result.err != nil || result.snap == nil {
		a.toastDeferred(nil, a.menuText("Could not open the session: ",
			"无法打开会话：")+errorText(result.err))
		return
	}
	a.SetSnapshot(result.snap)
	a.Screen = Session
	if ws := a.Ctrl.CurrentWorkspaceID(); ws != "" && ws != result.beforeWS {
		a.resetFiles()
		a.wsHoverID = ""
		a.setWorkspaceCollapsed(ws, false)
	}
	a.moveDraftToSession(result.oldScreen, result.oldID, result.id)
}

// errorText is a nil-safe error string for the async open path.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// liveSessionOpening renders the loading banner while a background open runs.
func (a *App) liveSessionOpening(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	if st.sessionOpeningID == "" {
		return
	}
	ui.Row(c).Padding(t.Theme().Space(2), 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.menuText("Opening session…", "正在打开会话…")).
			FontSize(12).TextColor(t.LabelTertiary)
	})
}
