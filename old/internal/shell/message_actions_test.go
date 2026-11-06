package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// summaryTool is a scripted tool with the name a summary test needs.
type summaryTool struct{ name string }

func (t summaryTool) Name() string        { return t.name }
func (t summaryTool) Description() string { return "Test tool." }
func (t summaryTool) Invoke(_ context.Context, _ json.RawMessage) (string, error) {
	return "ok", nil
}

func TestAssistantMessageCopyRetryAndFeedback(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{{text: "first answer"}}}
	app := m6LiveApp(t, provider)
	m6Submit(t, app, "original question")

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Copy"); err != nil {
		t.Fatalf("click Copy: %v", err)
	}
	if got := tt.Clipboard(); got != "first answer" {
		t.Fatalf("clipboard = %q, want %q", got, "first answer")
	}
	if !tt.HasText("Copied") {
		t.Fatalf("Copy did not toast: %v", tt.Texts())
	}

	if err := tt.Click("Good response"); err != nil {
		t.Fatalf("click Good response: %v", err)
	}
	if !tt.HasText("Feedback recorded") {
		t.Fatalf("feedback button was dead: %v", tt.Texts())
	}

	if err := tt.Click("Retry"); err != nil {
		t.Fatalf("click Retry: %v", err)
	}
	m6WaitIdle(t, app.Ctrl)
	app.Ctrl.Wait()
	app.SetSnapshot(app.Ctrl.Snapshot())

	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 2 {
		t.Fatalf("provider calls after Retry = %d, want 2", calls)
	}
	found := false
	for _, m := range app.Snapshot().Messages {
		if m.Role == state.RoleUser && m.Text == "original question" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Retry did not resubmit the last user text: %+v", app.Snapshot().Messages)
	}
}

func TestToolSummaryCollapsesAndExpandsCards(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "bash", args: `{"cmd":"ls"}`},
		{tool: "read_file", args: `{"path":"README.md"}`},
		{tool: "echo", args: `{"x":1}`},
		{text: "all done"},
	}}
	app := m6LiveApp(t, provider,
		summaryTool{name: "bash"}, summaryTool{name: "read_file"}, summaryTool{name: "echo"})
	m6Submit(t, app, "run the tools")

	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	const summary = "Ran 1 command, read 1 file, called 1 tool"
	if !tt.HasText(summary) {
		t.Fatalf("tool summary missing %q: %v", summary, tt.Texts())
	}
	for _, card := range []string{"bash · done", "read_file · done", "echo · done"} {
		if !tt.HasText(card) {
			t.Fatalf("tool card %q missing with the summary open: %v", card, tt.Texts())
		}
	}
	if err := tt.Click(summary); err != nil {
		t.Fatalf("click summary: %v", err)
	}
	tt.Frame()
	if !tt.HasText(summary) {
		t.Fatalf("summary disappeared when collapsed: %v", tt.Texts())
	}
	if tt.HasText("bash · done") || tt.HasText("read_file · done") || tt.HasText("echo · done") {
		t.Fatalf("cards still visible after collapse: %v", tt.Texts())
	}
	if err := tt.Click(summary); err != nil {
		t.Fatalf("click summary to expand: %v", err)
	}
	tt.Frame()
	if !tt.HasText("bash · done") {
		t.Fatalf("cards did not return after expanding: %v", tt.Texts())
	}
}

func TestLiveUsageFooterCompletedTokPerSecondAndCache(t *testing.T) {
	// The running snapshot holds the usage from before this turn; the idle
	// snapshot carries the cumulative totals after it. The completion rate must
	// use only the growth since the running phase began.
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseRunning
	snap.Usage = state.Usage{Turns: 1, Steps: 1, In: 150, Out: 50, CacheHit: 40}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(2 * time.Second)

	idle := *snap
	idle.Phase = state.PhaseIdle
	idle.Usage = state.Usage{Turns: 2, Steps: 3, In: 200, Out: 100, CacheHit: 50}
	h.app.SetSnapshot(&idle)
	tt.Frame()

	for _, want := range []string{"Completed in 2.0s", "25 tok/s", "cache 25%"} {
		if !tt.HasText(want) {
			t.Fatalf("usage footer missing %q: %v", want, tt.Texts())
		}
	}

	// A later unobserved turn changes the counters without a running frame;
	// the old clock must not be paired with the new tokens.
	next := idle
	next.Usage = state.Usage{Turns: 2, Steps: 2, In: 100, Out: 80, CacheHit: 10}
	h.app.SetSnapshot(&next)
	tt.Frame()
	if tt.HasText("Completed in") || tt.HasText("tok/s") {
		t.Fatalf("footer reused the previous turn's clock: %v", tt.Texts())
	}
	if !tt.HasText("cache 10%") {
		t.Fatalf("new turn's cache share is missing: %v", tt.Texts())
	}
}

func TestLiveUsageFooterOmitsUnmeasurableRate(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Usage = state.Usage{Turns: 1, Steps: 1, In: 10, Out: 20, CacheHit: 5}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if tt.HasText("Completed in") || tt.HasText("tok/s") {
		t.Fatalf("footer invented a duration or rate: %v", tt.Texts())
	}
	if !tt.HasText("cache 50%") {
		t.Fatalf("footer missing the computable cache share: %v", tt.Texts())
	}
}

func TestTurnNavigationScrollsToTurn(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "first turn question"},
		{MsgID: "a1", Role: state.RoleAssistant, Text: strings.Repeat("long answer line\n", 300)},
		{MsgID: "u2", Role: state.RoleUser, Text: "second turn question"},
		{MsgID: "a2", Role: state.RoleAssistant, Text: "second answer"},
	}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	before, ok := tt.Find("second turn question")
	if !ok {
		t.Fatalf("second turn is missing: %v", tt.Texts())
	}
	if err := tt.Click("Jump to turn 2"); err != nil {
		t.Fatalf("click jump to turn 2: %v", err)
	}
	tt.Frame()
	after, ok := tt.Find("second turn question")
	if !ok {
		t.Fatalf("second turn disappeared after jumping: %v", tt.Texts())
	}
	if after.Y < 0 || after.Y >= 1000 {
		t.Fatalf("jump did not bring the turn into the window: y=%v (was %v)", after.Y, before.Y)
	}
	if after.Y == before.Y {
		t.Fatalf("jump control did not scroll: y stayed %v", after.Y)
	}
	if got := h.app.liveMenus().pendingTurn; got != 0 {
		t.Fatalf("jump request was not consumed: pendingTurn=%d", got)
	}
}
