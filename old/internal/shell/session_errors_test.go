package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// TestClassifySessionError pins the mapping from raw provider/net errors to the
// product-facing card class (PD-09). Misclassifying is worse than staying
// generic, so the table only contains messages the wire actually produces.
func TestClassifySessionError(t *testing.T) {
	cases := []struct {
		text string
		want sessionErrorKind
	}{
		{"openai: chat completions: 401 Unauthorized: invalid api key", sessionErrorAuth},
		{"provider: 403 forbidden", sessionErrorAuth},
		{"openai: 429 Too Many Requests: rate limit reached", sessionErrorRateLimit},
		{"dial tcp 127.0.0.1:18080: connect: connection refused", sessionErrorNetwork},
		{"context deadline exceeded", sessionErrorNetwork},
		{"This model's maximum context length is 65536 tokens", sessionErrorContext},
		{"openai: 404 Not Found: model not found", sessionErrorModel},
		{"runtime: tool \"bash\" call \"call_1\" denied by user", sessionErrorGeneric},
		{"", sessionErrorGeneric},
	}
	for _, tc := range cases {
		if got := classifySessionError(tc.text); got != tc.want {
			t.Errorf("classifySessionError(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// TestFailedTurnErrorCardAuth covers the auth card: a classified title, the
// cause, and the Settings > Models action that fixes it.
func TestFailedTurnErrorCardAuth(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.TurnReason = state.ReasonFailed
	snap.LastError = &state.ErrorInfo{Text: "openai: chat completions: 401 Unauthorized: invalid api key"}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Model authentication failed") {
		t.Fatalf("auth card title missing: %q", tt.Texts())
	}
	if !tt.HasText("The API key was rejected") {
		t.Fatalf("auth card cause missing: %q", tt.Texts())
	}
	if tt.HasText("This turn failed") {
		t.Fatalf("failed-turn text marker duplicated the error card: %q", tt.Texts())
	}
	if err := tt.Click("Open model settings"); err != nil {
		t.Fatalf("click model settings action: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("model settings action opened %v, want Settings", h.app.Screen)
	}
	if got := h.app.shellUI().settingsTab; got != settingsTabModels {
		t.Fatalf("model settings action selected %v, want Models", got)
	}
}

// TestFailedTurnErrorCardDetails covers the network card: retry is offered and
// the raw error stays folded until Technical details is expanded.
func TestFailedTurnErrorCardDetails(t *testing.T) {
	raw := "dial tcp 127.0.0.1:18080: connect: connection refused"
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.TurnReason = state.ReasonFailed
	snap.LastError = &state.ErrorInfo{Text: raw}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Could not reach the model") {
		t.Fatalf("network card title missing: %q", tt.Texts())
	}
	if !tt.HasText("Retry") {
		t.Fatalf("network card has no retry action: %q", tt.Texts())
	}
	if tt.HasText(raw) {
		t.Fatalf("raw error shown before Technical details: %q", tt.Texts())
	}
	if err := tt.Click("Technical details"); err != nil {
		t.Fatalf("click technical details: %v", err)
	}
	tt.Frame()
	if !tt.HasText(raw) {
		t.Fatalf("expanded card did not show the raw error: %q", tt.Texts())
	}
	if err := tt.Click("Hide details"); err != nil {
		t.Fatalf("click hide details: %v", err)
	}
	tt.Frame()
	if tt.HasText(raw) {
		t.Fatalf("collapsed card still shows the raw error: %q", tt.Texts())
	}
}

// TestFailedTurnErrorCardGeneric covers the fallback class, and that a plain
// stopped/interrupted turn keeps its existing marker (no error card).
func TestFailedTurnErrorCardGeneric(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.TurnReason = state.ReasonFailed
	snap.LastError = &state.ErrorInfo{Text: "runtime: unexpected state"}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("This turn failed") {
		t.Fatalf("generic error card title missing: %q", tt.Texts())
	}
	if !tt.HasText("Technical details") {
		t.Fatalf("generic error card has no details toggle: %q", tt.Texts())
	}

	stopped := state.NewSnapshot()
	stopped.Phase = state.PhaseIdle
	stopped.TurnReason = state.ReasonStopped
	h2 := newLiveSnapshotApp(t, Session, stopped)
	tt2 := ui.NewTester(h2.app.View, 1600, 1000)
	tt2.Frame()
	if !tt2.HasText("Stopped") {
		t.Fatalf("stopped marker missing: %q", tt2.Texts())
	}
	if tt2.HasText("This turn failed") {
		t.Fatalf("stopped turn rendered a failure card: %q", tt2.Texts())
	}
}
