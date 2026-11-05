package shell

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// newFactoryOnlyApp is the production wiring that used to hide the first-run
// guidance: a per-session agent factory is installed (so HasAgent is true) but
// no agent/model is configured (so HasConfiguredModel is false).
func newFactoryOnlyApp(t *testing.T) *liveApp {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ws, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	broker := state.NewBroker()
	t.Cleanup(broker.Close)
	ctrl, err := session.New(session.Config{
		Store:  st,
		Broker: broker,
		AgentFactory: func(string) (*runtime.Agent, error) {
			return nil, errors.New("no model configured")
		},
		WorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })
	app := &App{Screen: Home, Ctrl: ctrl, Language: LangEN}
	ctrl.OnUpdate(app.SetSnapshot)
	// No agent can be built without a model, so the empty session screen is
	// driven by an explicit snapshot instead of CreateSession.
	snap := state.NewSnapshot()
	snap.SessionID = "first-run"
	snap.Phase = state.PhaseIdle
	app.SetSnapshot(snap)
	if ctrl.HasConfiguredModel() {
		t.Fatal("fixture should start without a configured model")
	}
	return &liveApp{app: app, ctrl: ctrl, store: st, dir: dir}
}

// TestHomeSetupCardShowsWithoutConfiguredModel pins the P2 regression fix: a
// controller with a session factory but no configured model still counts as
// not set up, so the Home card explains it and offers "Configure model".
func TestHomeSetupCardShowsWithoutConfiguredModel(t *testing.T) {
	h := newFactoryOnlyApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Finish setting up ZeoKeel") {
		t.Fatalf("setup card missing with a factory but no model: %q", tt.Texts())
	}
	if !tt.HasText("No model configured") || !tt.HasText("Configure model") {
		t.Fatalf("setup card missing the model CTA: %q", tt.Texts())
	}
	if err := tt.Click("Configure model"); err != nil {
		t.Fatalf("click Configure model: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings || h.app.shellUI().settingsTab != settingsTabModels {
		t.Fatalf("model CTA opened %v/%v", h.app.Screen, h.app.shellUI().settingsTab)
	}
}

// TestHomeSetupCardHidesWithConfiguredModel pins the other half: once the app
// reports a configured model the card disappears.
func TestHomeSetupCardHidesWithConfiguredModel(t *testing.T) {
	h := newFactoryOnlyApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Finish setting up ZeoKeel") {
		t.Fatalf("setup card missing before configuring a model: %q", tt.Texts())
	}
	h.ctrl.SetModelConfiguredFunc(func() bool { return true })
	tt.Frame()
	if tt.HasText("Finish setting up ZeoKeel") {
		t.Fatalf("setup card shown with a configured model: %q", tt.Texts())
	}
}

// TestEmptySessionAddModelShowsWithoutConfiguredModel pins the session empty
// state: with a factory but no model it warns and links to Settings > Models.
func TestEmptySessionAddModelShowsWithoutConfiguredModel(t *testing.T) {
	h := newFactoryOnlyApp(t)
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("No model is configured yet") || !tt.HasText("Add a model in Settings") {
		t.Fatalf("empty session lost the Add model action: %q", tt.Texts())
	}
	if err := tt.Click("Add a model in Settings"); err != nil {
		t.Fatalf("click Add a model in Settings: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Settings || h.app.shellUI().settingsTab != settingsTabModels {
		t.Fatalf("Add model opened %v/%v", h.app.Screen, h.app.shellUI().settingsTab)
	}
}

// TestEmptySessionAddModelHidesWithConfiguredModel pins that the warning and
// action disappear once the app reports a configured model.
func TestEmptySessionAddModelHidesWithConfiguredModel(t *testing.T) {
	h := newFactoryOnlyApp(t)
	h.app.Screen = Session
	h.ctrl.SetModelConfiguredFunc(func() bool { return true })
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if tt.HasText("Add a model in Settings") {
		t.Fatalf("Add model shown with a configured model: %q", tt.Texts())
	}
}
