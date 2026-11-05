package shell

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/state"
)

// blockingShellTool holds one tool call open until the turn is cancelled, so a
// footer test can look at the running session mid-turn.
type blockingShellTool struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingShellTool) Name() string        { return "block" }
func (b *blockingShellTool) Description() string { return "Blocks until cancelled." }
func (b *blockingShellTool) Invoke(ctx context.Context, _ json.RawMessage) (string, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return "", ctx.Err()
}

// TestUsageFooterHidesBeforeAnyUsage pins the zero state: a fresh session shows
// no "0 turns 0 steps 0 out tok" strip at all.
func TestUsageFooterHidesBeforeAnyUsage(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, state.NewSnapshot())
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if tt.HasText("Session usage") {
		t.Fatalf("usage footer rendered before any turn: %v", tt.Texts())
	}
	for _, zero := range []string{"0 turns", "0 steps", "0 out tok", "0 轮", "0 步", "0 输出 tok"} {
		if tt.HasText(zero) {
			t.Fatalf("fresh session rendered %q", zero)
		}
	}
}

// TestUsageFooterStoppedTurnKeepsCountersWithoutCompletion pins the footer of a
// stopped turn: the cumulative counters stay, but "Completed in"/tok/s are not
// claimed for a turn that did not complete.
func TestUsageFooterStoppedTurnKeepsCountersWithoutCompletion(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.TurnReason = state.ReasonStopped
	snap.Usage = state.Usage{Turns: 2, Steps: 3, In: 200, Out: 40, CacheHit: 50}
	h := newLiveSnapshotApp(t, Session, snap)
	h.app.liveMenus().lastTurnDuration = 5 * time.Second
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	for _, want := range []string{"2 turns", "3 steps", "40 out tok", "cache 25%"} {
		if !tt.HasText(want) {
			t.Fatalf("stopped turn footer missing %q: %v", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"Completed in", "tok/s"} {
		if tt.HasText(unwanted) {
			t.Fatalf("stopped turn footer claimed %q: %v", unwanted, tt.Texts())
		}
	}
}

// TestUsageFooterShowsCostAdvisory pins the footer half of the soft budget
// advisory: the same warning the model received surfaces as a short chip.
func TestUsageFooterShowsCostAdvisory(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseRunning
	snap.Usage = state.Usage{
		Turns: 1, Steps: 30, Out: 1200,
		Advisory: "Cost advisory (not a hard stop): 30 model steps",
	}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("cost advisory") {
		t.Fatalf("footer missing the cost advisory chip: %v", tt.Texts())
	}
	// Without an advisory there is no chip.
	plain := *snap
	plain.Usage.Advisory = ""
	h.app.SetSnapshot(&plain)
	tt.Frame()
	if tt.HasText("cost advisory") {
		t.Fatalf("footer invented a cost advisory: %v", tt.Texts())
	}
}

// TestTurnOutcomeMarker pins the transcript marker a stopped or interrupted
// turn leaves in place of an answer (and the absence of a completion line).
func TestTurnOutcomeMarker(t *testing.T) {
	cases := []struct {
		name   string
		reason state.StatusReason
		want   string
	}{
		{"stopped", state.ReasonStopped, "Stopped"},
		{"interrupted", state.ReasonInterrupted, "Interrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := state.NewSnapshot()
			snap.Phase = state.PhaseIdle
			snap.TurnReason = tc.reason
			h := newLiveSnapshotApp(t, Session, snap)
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			if !tt.HasText(tc.want) {
				t.Fatalf("%s marker missing: %v", tc.name, tt.Texts())
			}
		})
	}

	// A completed turn shows no marker.
	completed := state.NewSnapshot()
	completed.Phase = state.PhaseIdle
	h := newLiveSnapshotApp(t, Session, completed)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, unwanted := range []string{"Stopped", "Interrupted"} {
		if tt.HasText(unwanted) {
			t.Fatalf("completed turn rendered %q marker: %v", unwanted, tt.Texts())
		}
	}
}

// TestUsageFooterLiveDuringTurnAndStoppedAfter covers the real controller path:
// the footer counts grow while the turn runs (usage deltas are published after
// each model call), and a Stop closes the pending tool card and leaves the
// stopped marker instead of a completion line.
func TestUsageFooterLiveDuringTurnAndStoppedAfter(t *testing.T) {
	block := &blockingShellTool{started: make(chan struct{})}
	provider := &m6Provider{steps: []m6Step{
		{tool: "echo", args: `{"path":"README.md"}`},
		{text: "first answer", usage: &runtime.Usage{InputTokens: 10, OutputTokens: 40, CachedInputTokens: 5, TotalTokens: 50}},
		{tool: "block"},
	}}
	app := m6LiveApp(t, provider, &m6EchoTool{}, block)
	app.Language = LangEN
	m6Submit(t, app, "first question")

	// First turn done: one turn, two model calls (tool call + answer), 40 out.
	if got := app.Snapshot().Usage; got.Turns != 1 || got.Steps != 2 || got.Out != 40 {
		t.Fatalf("after first turn usage = %+v, want 1 turn / 2 steps / 40 out", got)
	}

	// Second turn: its first model call completes (step 3, turn 2) and then the
	// blocking tool runs. The footer must already show the new counts.
	if err := app.Ctrl.Submit(context.Background(), app.Ctrl.CurrentSessionID(), "second question"); err != nil {
		t.Fatalf("Submit second: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snap := app.Snapshot()
		if snap.Phase == state.PhaseRunning && snap.Usage.Turns == 2 && snap.Usage.Steps == 3 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking tool never started")
	}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{"2 turns", "3 steps", "40 out tok"} {
		if !tt.HasText(want) {
			t.Fatalf("running footer missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("Completed in") {
		t.Fatalf("running turn claimed a completion time: %v", tt.Texts())
	}

	app.Ctrl.Stop()
	m6WaitIdle(t, app.Ctrl)
	app.Ctrl.Wait()
	app.SetSnapshot(app.Ctrl.Snapshot())
	tt.Frame()
	if !tt.HasText("Stopped") {
		t.Fatalf("stopped turn marker missing: %v", tt.Texts())
	}
	if !tt.HasText("block · cancelled") {
		t.Fatalf("stopped turn's tool card was not closed: %v", tt.Texts())
	}
	if tt.HasText("Completed in") || tt.HasText("tok/s") {
		t.Fatalf("stopped turn claimed completion: %v", tt.Texts())
	}
	for _, want := range []string{"2 turns", "3 steps"} {
		if !tt.HasText(want) {
			t.Fatalf("stopped turn lost the counters %q: %v", want, tt.Texts())
		}
	}
}
