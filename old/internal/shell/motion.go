package shell

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/design"
)

// liveState is the motion state of one live session: when the running phase
// began, how much of each assistant message has shown, and which tool cards
// are open. It is live-only: the static shell (App.Ctrl == nil) never reads
// it, so the baseline renders stay frame-for-frame identical.
//
// The App type lives in app.go, which this change does not own, so the state
// is kept here, keyed by the App the view is drawn for. Only the view
// goroutine touches it; the controller publishes snapshots separately.
type liveState struct {
	// running is whether the last frame saw PhaseRunning, and since when.
	// The working indicator counts "Deep diving for Xm Ys" from since.
	running bool
	since   time.Time

	// stream is, per message MsgID, how many bytes of its text already
	// showed and when it last grew: the fade that follows arriving tokens.
	stream map[string]streamRec

	// expanded is which tool cards the user opened.
	expanded map[string]bool
}

// streamRec is how much of a streaming message has shown, and when it last
// grew.
type streamRec struct {
	shown int
	at    time.Time
}

// liveStates maps each App to its live motion state. Entries live as long as
// their App; the real app has one.
var liveStates sync.Map // map[*App]*liveState

// live returns the motion state of a, making it on first use.
func (a *App) live() *liveState {
	if s, ok := liveStates.Load(a); ok {
		return s.(*liveState)
	}
	s := &liveState{stream: make(map[string]streamRec), expanded: make(map[string]bool)}
	actual, _ := liveStates.LoadOrStore(a, s)
	return actual.(*liveState)
}

// runningFor updates the running phase's start time and returns how long it
// has run. When running is false the state resets, so the next turn counts
// from its own start.
func (s *liveState) runningFor(now time.Time, running bool) (time.Duration, bool) {
	if !running {
		s.running, s.since = false, time.Time{}
		return 0, false
	}
	if !s.running || s.since.IsZero() {
		s.running, s.since = true, now
	}
	d := now.Sub(s.since)
	if d < 0 {
		d = 0
	}
	return d, true
}

// streamShown returns the opacity of message id's streaming text at now,
// having just shown text, and whether it is still fading. The first frame
// that shows a message is at rest; every later growth restarts the fade.
func (s *liveState) streamShown(now time.Time, id, text string) (float32, bool) {
	rec, ok := s.stream[id]
	if !ok {
		s.stream[id] = streamRec{shown: len(text), at: now}
		return 1, false
	}
	if len(text) != rec.shown {
		rec.shown, rec.at = len(text), now
		s.stream[id] = rec
	}
	return design.StreamOpacity(now.Sub(rec.at))
}

// toggleTool opens or closes a tool card.
func (s *liveState) toggleTool(id string) {
	s.expanded[id] = !s.expanded[id]
}

// formatDiveTime formats the running time as DSH's "Xm Ys".
func formatDiveTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d / time.Second)
	return strconv.Itoa(seconds/60) + "m " + strconv.Itoa(seconds%60) + "s"
}

// indicatorLabel is the working indicator's text at elapsed: DSH's
// "Deep diving for Xm Ys" with its three dots revealing on the 1.5s
// step-end cycle.
func indicatorLabel(elapsed time.Duration) string {
	return "Deep diving for " + formatDiveTime(elapsed) + strings.Repeat(".", design.DotsAt(elapsed))
}
