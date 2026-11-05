package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// Session screen geometry shared with the live renderer (session_live.go,
// trajectory.go and the layout helpers).
const (
	sessHeaderH   float32 = 76
	sessTabsH     float32 = 25
	sessColumnW   float32 = 845
	sessGutter    float32 = 8
	sessComposerW float32 = 877
)

// sessModeIcon is the 14x14 line icon in the session header's mode slot.
var sessModeIcon = homeIconTune

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
