package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// Session layout on the brand spacing grid (design.Space1 = 4 DIPs). The
// centered transcript and the composer share one column width, 180 units =
// 720 DIPs, like the Home composer; layout.go clamps that column for narrow
// windows and a docked panel. sessTabsH is also read by the Chat/Trajectory
// tab row in trajectory.go so the header stays one piece.
var (
	sessTabsH     = 7 * design.Space1   // 28 DIP tab row
	sessColumnW   = 180 * design.Space1 // 720 DIP centered transcript column
	sessComposerW = 180 * design.Space1 // 720 DIP centered composer card
)

// sessModeIcon is the line icon in the session header's mode chip.
var sessModeIcon = homeIconTune

// minSessionColumnW is the narrowest centered column the session keeps before
// the window itself limits it.
const minSessionColumnW float32 = 70 * design.Space1 // 280 DIPs

// session draws the Chat tab of a session: header, transcript, composer.
// The live snapshot is the only renderer; the retired static transcript is
// gone (pixel parity retired 2026-11-05).
func (a *App) session(c *ui.Context, t *design.Tokens) {
	snap := a.Snapshot()
	if snap == nil && a.Ctrl != nil {
		// The shell normally receives snapshots through OnUpdate/SetSnapshot.
		// Falling back to the controller keeps a first live frame (and tests
		// that wire only Ctrl) from painting an empty transcript.
		snap = a.Ctrl.Snapshot()
	}
	if snap == nil {
		design.Apply(ui.Column(c), t.Main())
		return
	}
	a.sessionLive(c, t, snap)
}
