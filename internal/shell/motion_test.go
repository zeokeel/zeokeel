package shell

import (
	"bytes"
	"encoding/json"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// motionSnap folds events into a snapshot for the animation tests.
func motionSnap(events ...state.Event) *state.Snapshot {
	s := state.NewSnapshot()
	for _, ev := range events {
		s = state.Apply(s, ev)
	}
	return s
}

// TestMotionTokens pins the durations and curves extracted from DSH: each
// value is the one documented in the retired motion baseline (archived
// 2026-11-05), so a silent change
// to a token fails here.
func TestMotionTokens(t *testing.T) {
	durations := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"DurationTick", design.DurationTick, 100 * time.Millisecond},
		{"DurationFast", design.DurationFast, 120 * time.Millisecond},
		{"DurationEnter", design.DurationEnter, 150 * time.Millisecond},
		{"DurationBase", design.DurationBase, 160 * time.Millisecond},
		{"DurationSlow", design.DurationSlow, 200 * time.Millisecond},
		{"DurationTab", design.DurationTab, 300 * time.Millisecond},
		{"DurationSpin", design.DurationSpin, 800 * time.Millisecond},
		{"DurationPulse", design.DurationPulse, 1500 * time.Millisecond},
		{"DurationShimmer", design.DurationShimmer, 1500 * time.Millisecond},
		{"ShimmerDelay", design.ShimmerDelay, 300 * time.Millisecond},
	}
	for _, c := range durations {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (retired motion baseline)", c.name, c.got, c.want)
		}
	}
	if design.ShimmerSteps != 48 {
		t.Errorf("ShimmerSteps = %d, want 48", design.ShimmerSteps)
	}

	// The curves are MyGo easing functions with CSS's cubic-bezier values:
	// fixed at the endpoints, monotonic in between, and not the plain
	// cubic MyGo's EaseOut would use.
	for _, c := range []struct {
		name string
		ease ui.Easing
	}{
		{"EaseStandard", design.EaseStandard},
		{"EaseCSS", design.EaseCSS},
		{"EaseOut", design.EaseOut},
		{"EaseInOut", design.EaseInOut},
	} {
		if c.ease(0) != 0 || c.ease(1) != 1 {
			t.Errorf("%s endpoints: %v, %v", c.name, c.ease(0), c.ease(1))
		}
		last := float32(0)
		for i := 0; i <= 20; i++ {
			v := c.ease(float32(i) / 20)
			if v < last-1e-5 {
				t.Errorf("%s is not monotonic at %d/20: %v after %v", c.name, i, v, last)
			}
			last = v
		}
	}
	// The computed DSH timing function is cubic-bezier(0.4, 0, 0.2, 1),
	// which is not MyGo's cubic EaseOut at a quarter of the way.
	if design.EaseStandard(0.25) == ui.EaseOut(0.25) {
		t.Error("EaseStandard matches ui.EaseOut, not cubic-bezier(0.4, 0, 0.2, 1)")
	}
}

// TestMotionHelpers checks the transition helpers the live session builds
// with: each settles at exactly its rest value, never one epsilon off.
func TestMotionHelpers(t *testing.T) {
	if got := design.Progress(-time.Millisecond, design.DurationFast); got != 0 {
		t.Errorf("progress before the start = %v, want 0", got)
	}
	if got := design.Progress(design.DurationFast, design.DurationFast); got != 1 {
		t.Errorf("progress at the end = %v, want 1", got)
	}
	if got := design.Progress(design.DurationFast/2, design.DurationFast); got <= 0 || got >= 1 {
		t.Errorf("progress halfway = %v, want between 0 and 1", got)
	}
	if got := design.Progress(time.Second, 0); got != 1 {
		t.Errorf("progress of a zero duration = %v, want 1", got)
	}

	if got := design.OpacityAt(1); got != 1 {
		t.Errorf("opacity at rest = %v, want 1", got)
	}
	if got := design.HeightAt(1, 40); got != 40 {
		t.Errorf("height at rest = %v, want 40", got)
	}
	if got := design.HeightAt(0, 40); got != 0 {
		t.Errorf("height collapsed = %v, want 0", got)
	}
	if got := design.ScaleAt(1, design.ScaleFrom); got != 1 {
		t.Errorf("scale at rest = %v, want 1", got)
	}
	if got := design.ScaleAt(0, design.ScaleFrom); got != design.ScaleFrom {
		t.Errorf("scale entering = %v, want %v", got, design.ScaleFrom)
	}
	if got := design.RiseAt(1, design.MessageRise); got != 0 {
		t.Errorf("rise at rest = %v, want 0", got)
	}
	if got := design.RiseAt(0, design.MessageRise); got != design.MessageRise {
		t.Errorf("rise entering = %v, want %v", got, design.MessageRise)
	}

	// Streaming text starts dimmer than rest and reaches exactly 1.
	if opacity, moving := design.StreamOpacity(0); opacity != design.StreamFrom || !moving {
		t.Errorf("streaming at arrival = %v/%v, want %v/true", opacity, moving, design.StreamFrom)
	}
	if opacity, moving := design.StreamOpacity(design.DurationFast / 2); !moving || opacity <= design.StreamFrom || opacity >= 1 {
		t.Errorf("streaming halfway = %v/%v, want between %v and 1", opacity, moving, design.StreamFrom)
	}
	if opacity, moving := design.StreamOpacity(design.DurationFast); opacity != 1 || moving {
		t.Errorf("streaming at rest = %v/%v, want 1/false", opacity, moving)
	}

	// DSH reveals the second dot at 33.33% and the third at 66.66% of its
	// 1.5s cycle, step-end.
	dots := []struct {
		at   time.Duration
		want int
	}{
		{0, 1},
		{499 * time.Millisecond, 1},
		{500 * time.Millisecond, 2},
		{999 * time.Millisecond, 2},
		{1000 * time.Millisecond, 3},
		{1499 * time.Millisecond, 3},
		{1500 * time.Millisecond, 1},
		{2000 * time.Millisecond, 2},
	}
	for _, c := range dots {
		if got := design.DotsAt(c.at); got != c.want {
			t.Errorf("DotsAt(%v) = %d, want %d", c.at, got, c.want)
		}
	}

	if got := design.PulseOpacity(0); got != design.PulseMin {
		t.Errorf("pulse at 0 = %v, want %v", got, design.PulseMin)
	}
	if got := design.PulseOpacity(1); got != 1 {
		t.Errorf("pulse at 1 = %v, want 1", got)
	}

	// The row shimmer waits .3s, sweeps in 48 steps, and holds from two
	// thirds of its 1.5s period.
	if got := design.ShimmerPhase(design.ShimmerDelay); got != 0 {
		t.Errorf("shimmer at the delay = %v, want 0", got)
	}
	if got := design.ShimmerPhase(design.ShimmerDelay + 750*time.Millisecond); got != 0.75 {
		t.Errorf("shimmer halfway = %v, want 0.75", got)
	}
	if got := design.ShimmerPhase(design.ShimmerDelay + 1200*time.Millisecond); got != 1 {
		t.Errorf("shimmer after the sweep = %v, want 1", got)
	}
	if got := design.ShimmerPhase(design.ShimmerDelay + design.DurationShimmer); got != 0 {
		t.Errorf("shimmer of the next period = %v, want 0", got)
	}
}

// TestMotionStreamFadeStartsAndSettles drives the per-message fade state
// through a virtual clock: first sight is at rest, growth starts the fade,
// and DurationFast later the fade is over with an exact 1.
func TestMotionStreamFadeStartsAndSettles(t *testing.T) {
	now := time.Now()
	s := &liveState{stream: map[string]streamRec{}, expanded: map[string]bool{}}

	if opacity, moving := s.streamShown(now, "m1", "Hello"); opacity != 1 || moving {
		t.Fatalf("first sight = %v/%v, want 1/false (no hard fade on arrival)", opacity, moving)
	}
	if opacity, moving := s.streamShown(now, "m1", "Hello world"); opacity != design.StreamFrom || !moving {
		t.Fatalf("growth = %v/%v, want %v/true (fade starts)", opacity, moving, design.StreamFrom)
	}
	if opacity, moving := s.streamShown(now.Add(design.DurationFast/2), "m1", "Hello world"); !moving || opacity <= design.StreamFrom || opacity >= 1 {
		t.Fatalf("halfway = %v/%v, want between %v and 1", opacity, moving, design.StreamFrom)
	}
	if opacity, moving := s.streamShown(now.Add(design.DurationFast), "m1", "Hello world"); opacity != 1 || moving {
		t.Fatalf("at the end = %v/%v, want 1/false (settled)", opacity, moving)
	}
	// The same text keeps it settled.
	if opacity, moving := s.streamShown(now.Add(design.DurationFast+time.Second), "m1", "Hello world"); opacity != 1 || moving {
		t.Fatalf("unchanged = %v/%v, want 1/false", opacity, moving)
	}
	// New tokens start it over.
	if opacity, moving := s.streamShown(now.Add(2*design.DurationFast), "m1", "Hello world!"); opacity != design.StreamFrom || !moving {
		t.Fatalf("regrowth = %v/%v, want %v/true", opacity, moving, design.StreamFrom)
	}
}

// TestMotionRunningClock checks that the working indicator's elapsed time
// starts with the running phase and restarts for the next turn.
func TestMotionRunningClock(t *testing.T) {
	s := &liveState{}
	start := time.Now()
	if d, ok := s.runningFor(start, true); d != 0 || !ok {
		t.Fatalf("start = %v/%v, want 0/true", d, ok)
	}
	if d, ok := s.runningFor(start.Add(2*time.Minute+3*time.Second), true); !ok || d != 2*time.Minute+3*time.Second {
		t.Fatalf("after 2m3s = %v/%v", d, ok)
	}
	if d, ok := s.runningFor(start.Add(3*time.Minute), false); d != 0 || ok {
		t.Fatalf("idle = %v/%v, want 0/false", d, ok)
	}
	if d, ok := s.runningFor(start.Add(time.Hour), true); d != 0 || !ok {
		t.Fatalf("next turn's start = %v/%v, want 0/true", d, ok)
	}

	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0m 0s"},
		{65 * time.Second, "1m 5s"},
		{12*time.Minute + 7*time.Second, "12m 7s"},
		{-time.Second, "0m 0s"},
	} {
		if got := formatDiveTime(c.d); got != c.want {
			t.Errorf("formatDiveTime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestMotionWorkingIndicator renders the live session while a turn runs:
// the spinner and "Deep diving for Xm Ys" show, the dots reveal on the 1.5s
// cycle, and the row is gone with no frame pending once the turn ends.
func TestMotionWorkingIndicator(t *testing.T) {
	running := motionSnap(
		state.Token{MsgID: "m1", Text: "thinking"},
		state.Status{Phase: state.PhaseRunning},
	)
	h := newLiveSnapshotApp(t, Session, running)
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if _, ok := tt.Find("Deep diving for 0m 0s."); !ok {
		t.Fatalf("working label missing at start; texts: %v", tt.Texts())
	}
	if !tt.HasText("Working") {
		t.Fatalf("spinner label missing; texts: %v", tt.Texts())
	}

	// The second dot shows at 500 ms, on DSH's step-end cycle.
	tt.Advance(600 * time.Millisecond)
	if _, ok := tt.Find("Deep diving for 0m 0s.."); !ok {
		t.Fatalf("working label did not reveal its second dot; texts: %v", tt.Texts())
	}

	// A finished turn removes the indicator; nothing animates at rest.
	h.app.SetSnapshot(motionSnap(
		state.Token{MsgID: "m1", Text: "thinking"},
		state.Status{Phase: state.PhaseIdle},
	))
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText("Deep diving") {
		t.Fatalf("working indicator still shows when idle; texts: %v", tt.Texts())
	}
	if tt.FramePending() {
		t.Error("a frame is still pending at rest")
	}
}

// TestMotionLiveSettlesToRestFrame is the no-hard-cut check at the frame
// level: after a new message and the streaming fade settle, the live frame
// is pixel-identical to a view that never animated.
func TestMotionLiveSettlesToRestFrame(t *testing.T) {
	first := motionSnap(
		state.Token{MsgID: "m1", Text: "first answer"},
		state.Status{Phase: state.PhaseIdle},
		state.ToolStart{CallID: "call-1", Name: "echo", Args: json.RawMessage(`{"path":"README.md"}`)},
		state.ToolEnd{CallID: "call-1", Result: "ok", Duration: 12 * time.Millisecond},
	)
	h := newLiveSnapshotApp(t, Session, first)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(2 * design.DurationSlow)

	// A second message arrives as the turn starts streaming.
	grown := motionSnap(
		state.Token{MsgID: "m1", Text: "first answer"},
		state.Token{MsgID: "m2", Text: "second answer"},
		state.Status{Phase: state.PhaseRunning},
		state.ToolStart{CallID: "call-1", Name: "echo", Args: json.RawMessage(`{"path":"README.md"}`)},
		state.ToolEnd{CallID: "call-1", Result: "ok", Duration: 12 * time.Millisecond},
	)
	h.app.SetSnapshot(grown)
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("a new message did not start its appear animation")
	}
	tt.Advance(4 * design.DurationSlow)
	if tt.FramePending() {
		t.Fatal("still animating after the appear duration")
	}

	// Re-express the state without a running turn so the working
	// indicator is gone, then settle.
	rest := motionSnap(
		state.Token{MsgID: "m1", Text: "first answer"},
		state.Token{MsgID: "m2", Text: "second answer"},
		state.Status{Phase: state.PhaseIdle},
		state.ToolStart{CallID: "call-1", Name: "echo", Args: json.RawMessage(`{"path":"README.md"}`)},
		state.ToolEnd{CallID: "call-1", Result: "ok", Duration: 12 * time.Millisecond},
	)
	h.app.SetSnapshot(rest)
	tt.Frame()
	tt.Advance(4 * design.DurationSlow)
	if tt.FramePending() {
		t.Fatal("still animating at rest")
	}

	// A view of the same state that never animated draws the same pixels: a
	// fresh runtime builds the first frame at rest. The composer is left out:
	// its text caret blinks on the clock, so the two views' first frames differ
	// in that one column. Everything the motion tests animate is above it.
	want := ui.Render(h.app.View, 1600, 1000, 1)
	got := tt.Image()
	if got.Stride != want.Stride {
		t.Fatalf("stride %d, want %d", got.Stride, want.Stride)
	}
	transcript := image.Rect(280, 76, 1600, 820)
	for y := transcript.Min.Y; y < transcript.Max.Y; y++ {
		row := y * got.Stride
		a := got.Pix[row+transcript.Min.X*4 : row+transcript.Max.X*4]
		b := want.Pix[row+transcript.Min.X*4 : row+transcript.Max.X*4]
		if !bytes.Equal(a, b) {
			t.Fatalf("the settled live frame differs from the never-animated rest frame at y=%d", y)
		}
	}
}

// TestMotionStreamingFadeFrames checks the fade in the real view: growth
// asks for frames and the fade settles on its own.
func TestMotionStreamingFadeFrames(t *testing.T) {
	base := motionSnap(
		state.Token{MsgID: "m1", Text: "a"},
		state.Status{Phase: state.PhaseRunning},
	)
	h := newLiveSnapshotApp(t, Session, base)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(4 * design.DurationSlow)

	h.app.SetSnapshot(motionSnap(
		state.Token{MsgID: "m1", Text: "a new token arrived"},
		state.Status{Phase: state.PhaseRunning},
	))
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("streaming text did not start its fade")
	}
	tt.Advance(4 * design.DurationFast)
	if tt.FramePending() {
		t.Fatal("streaming text is still fading after DurationFast")
	}
}

// TestMotionStreamReduceMotion checks that "less motion" takes the fade at
// once: the text still grows, and no frame is left pending.
func TestMotionStreamReduceMotion(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, motionSnap(
		state.Token{MsgID: "m1", Text: "a"},
		state.Status{Phase: state.PhaseRunning},
	))
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Advance(4 * design.DurationSlow)

	h.app.SetSnapshot(motionSnap(
		state.Token{MsgID: "m1", Text: "a new token arrived"},
		state.Status{Phase: state.PhaseRunning},
	))
	tt.Frame()
	tt.Advance(4 * design.DurationFast)
	if !tt.HasText("a new token arrived") {
		t.Fatalf("text did not grow under less motion; texts: %v", tt.Texts())
	}
	if tt.FramePending() {
		t.Fatal("less motion still asks for animation frames")
	}
}

// TestMotionStreamIdleHasNoFrames checks that a settled, idle session asks
// for no frames: the animation state costs nothing at rest.
func TestMotionStreamIdleHasNoFrames(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, motionSnap(
		state.Token{MsgID: "m1", Text: "a settled answer"},
		state.Status{Phase: state.PhaseIdle},
	))
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("an idle session still asks for frames")
	}
}

// TestMotionToolCardExpandCollapse clicks a tool card open and shut: the
// detail's height animates, and the final frames hold the settled heights.
func TestMotionToolCardExpandCollapse(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, motionSnap(
		state.ToolStart{CallID: "call-1", Name: "echo", Args: json.RawMessage(`{"path":"README.md"}`)},
		state.ToolEnd{CallID: "call-1", Result: "tool output", Duration: 42 * time.Millisecond},
		state.Status{Phase: state.PhaseIdle},
	))
	// Work details defaults to Detailed, which starts the card open; pin the
	// collapsed start this test exercises.
	if err := h.store.SetSetting(session.SettingWorkDetails, session.WorkDetailsStandard); err != nil {
		t.Fatalf("work details setting: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(4 * design.DurationSlow)

	if tt.HasText("README.md") {
		t.Fatalf("a collapsed card shows its arguments; texts: %v", tt.Texts())
	}
	if err := tt.Click("Tool echo"); err != nil {
		t.Fatalf("click tool card: %v", err)
	}
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("opening a tool card did not animate")
	}
	tt.Advance(4 * design.DurationSlow)
	if !tt.HasText("README.md") || !tt.HasText("tool output") {
		t.Fatalf("open card is missing its detail; texts: %v", tt.Texts())
	}
	if tt.FramePending() {
		t.Fatal("the open card still animates after DurationSlow")
	}

	if err := tt.Click("Tool echo"); err != nil {
		t.Fatalf("click open tool card: %v", err)
	}
	tt.Frame()
	tt.Advance(4 * design.DurationSlow)
	if tt.HasText("README.md") {
		t.Fatalf("a closed card still shows its arguments; texts: %v", tt.Texts())
	}
	if tt.FramePending() {
		t.Fatal("the closed card still animates after DurationSlow")
	}
}

// TestMotionToolDotPulse checks the running status dot's 1.5s pulse: it
// keeps asking for frames while the call runs and stops when it finishes.
func TestMotionToolDotPulse(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, motionSnap(
		state.ToolStart{CallID: "call-1", Name: "echo", Args: json.RawMessage(`{"path":"README.md"}`)},
		state.Status{Phase: state.PhaseRunning},
	))
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(2 * time.Second)
	if !tt.FramePending() {
		t.Fatal("the running status dot is not pulsing")
	}

	h.app.SetSnapshot(motionSnap(
		state.ToolStart{CallID: "call-1", Name: "echo", Args: json.RawMessage(`{"path":"README.md"}`)},
		state.ToolEnd{CallID: "call-1", Result: "ok", Duration: time.Second},
		state.Status{Phase: state.PhaseIdle},
	))
	tt.Frame()
	tt.Advance(4 * design.DurationSlow)
	if tt.FramePending() {
		t.Fatal("the status dot still pulses after the call finished")
	}
}

// TestMotionRunButtonState checks the composer's Send/Stop swap: the label
// follows the phase and the change is animated, settling on the new label.
func TestMotionRunButtonState(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, motionSnap(state.Status{Phase: state.PhaseIdle}))
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("Send") {
		t.Fatalf("idle composer has no Send; texts: %v", tt.Texts())
	}

	h.app.SetSnapshot(motionSnap(state.Status{Phase: state.PhaseRunning}))
	tt.Frame()
	if !tt.HasText("Stop") {
		t.Fatalf("running composer has no Stop; texts: %v", tt.Texts())
	}
	if tt.HasText("Send") {
		t.Fatalf("running composer still shows Send; texts: %v", tt.Texts())
	}
	tt.Advance(4 * design.DurationEnter)
	if !tt.HasText("Stop") {
		t.Fatalf("Stop did not settle; texts: %v", tt.Texts())
	}
	if tt.FramePending() {
		t.Fatal("the run button still animates after DurationEnter")
	}
}
