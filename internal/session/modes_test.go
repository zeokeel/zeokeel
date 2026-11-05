package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// modeHarness is the minimal live setup for the mode/goal/team tests. Unlike
// newHarness it lets a test choose its tools and provider script.
type modeHarness struct {
	t         *testing.T
	store     *store.Store
	broker    *state.Broker
	projector *state.Projector
	ctrl      *session.Controller
	agent     *runtime.Agent
	sessionID string
}

func newModeHarness(t *testing.T, provider runtime.Provider, register func(*runtime.Registry)) *modeHarness {
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

	registry := runtime.NewRegistry()
	if register != nil {
		register(registry)
	}
	agent, err := runtime.New(runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "test agent",
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	ctrl, err := session.New(session.Config{
		Store:       st,
		Broker:      broker,
		Projector:   projector,
		Agent:       agent,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})

	id, err := ctrl.CreateSession(context.Background(), "modes")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return &modeHarness{t: t, store: st, broker: broker, projector: projector, ctrl: ctrl, agent: agent, sessionID: id}
}

// toolCallsThenTextProvider calls the scripted tools on the first model call of
// a turn and answers with text afterwards.
type toolCallsThenTextProvider struct {
	calls []runtime.ToolCall

	mu    sync.Mutex
	count int
}

func (p *toolCallsThenTextProvider) Stream(ctx context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := p.count
	p.count++
	p.mu.Unlock()

	var chunks []runtime.Chunk
	if call == 0 {
		chunks = []runtime.Chunk{{ToolCalls: p.calls, Done: true}}
	} else {
		chunks = []runtime.Chunk{{Text: "turn done", Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	go func() {
		defer close(out)
		for _, chunk := range chunks {
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *toolCallsThenTextProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

// scriptedSubagentRunner is the fake child-agent runner the tools call.
type scriptedSubagentRunner struct {
	mu     sync.Mutex
	result runtime.SubagentResult
	err    error
	reqs   []runtime.SubagentRequest
}

func (r *scriptedSubagentRunner) RunSubagent(_ context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return r.result, r.err
}

func waitControllerIdle(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	ctrl.Wait()
	if phase := ctrl.Snapshot().Phase; phase != state.PhaseIdle {
		t.Fatalf("phase = %v, want idle", phase)
	}
}

func TestControllerModePersistenceAndSwitcher(t *testing.T) {
	h := newModeHarness(t, &echoOnlyProvider{}, nil)

	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Mode() = %q, want standard", got)
	}

	var switched []string
	h.ctrl.SetAgentModeSwitcher(func(mode string) error {
		switched = append(switched, mode)
		return nil
	})
	if err := h.ctrl.SetMode("deep"); err != nil {
		t.Fatalf("SetMode(deep): %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeDeep {
		t.Fatalf("Mode() = %q, want deep", got)
	}
	key := session.SessionModeKey(h.sessionID)
	if value, err := h.store.GetSetting(key); err != nil || value != runtime.ModeDeep {
		t.Fatalf("stored mode = %q, %v", value, err)
	}
	if len(switched) != 1 || switched[0] != runtime.ModeDeep {
		t.Fatalf("switcher calls = %v", switched)
	}

	// Empty means standard, like runtime.Config.Mode.
	if err := h.ctrl.SetMode(""); err != nil {
		t.Fatalf("SetMode(\"\"): %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Mode() = %q after empty set, want standard", got)
	}
	if err := h.ctrl.SetMode("turbo"); err == nil {
		t.Fatal("SetMode(turbo) = nil error")
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("failed SetMode changed mode to %q", got)
	}

	// A switcher failure is surfaced but the persisted mode stays durable.
	boom := errors.New("cannot rebuild")
	h.ctrl.SetAgentModeSwitcher(func(string) error { return boom })
	err := h.ctrl.SetMode("supervisor")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("SetMode with failing switcher error = %v", err)
	}
	if value, gerr := h.store.GetSetting(key); gerr != nil || value != runtime.ModeSupervisor {
		t.Fatalf("stored mode after switcher failure = %q, %v", value, gerr)
	}

	// A fresh session reports the default; reopening the first restores the
	// persisted mode from the settings table with a working switcher.
	if _, err := h.ctrl.CreateSession(context.Background(), "other"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("new session Mode() = %q, want standard", got)
	}
	h.ctrl.SetAgentModeSwitcher(func(mode string) error {
		switched = append(switched, mode)
		return h.agent.SetMode(mode)
	})
	if _, err := h.ctrl.OpenSession(context.Background(), h.sessionID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeSupervisor {
		t.Fatalf("reopened Mode() = %q, want supervisor", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeSupervisor {
		t.Fatalf("reopened Agent.Mode() = %q, want supervisor", got)
	}
}

// TestControllerOpenSessionSurfacesModeError proves a persisted mode that the
// switcher cannot rebuild is reported by OpenSession instead of leaving
// Controller.Mode() and Agent.Mode() silently divergent.
func TestControllerOpenSessionSurfacesModeError(t *testing.T) {
	h := newModeHarness(t, &echoOnlyProvider{}, nil)
	boom := errors.New("cannot rebuild")
	h.ctrl.SetAgentModeSwitcher(func(string) error { return boom })

	if err := h.ctrl.SetMode(runtime.ModeSupervisor); err == nil || !errors.Is(err, boom) {
		t.Fatalf("SetMode(supervisor) error = %v, want %v", err, boom)
	}
	// The persisted mode is supervisor while the runtime is still standard;
	// opening the session must report that it cannot be applied.
	_, err := h.ctrl.OpenSession(context.Background(), h.sessionID)
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("OpenSession error = %v, want the switcher failure", err)
	}
	if got := h.agent.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Agent.Mode() = %q after failed open, want standard", got)
	}
	// With a working switcher the same session opens in its persisted mode.
	h.ctrl.SetAgentModeSwitcher(h.agent.SetMode)
	if _, err := h.ctrl.OpenSession(context.Background(), h.sessionID); err != nil {
		t.Fatalf("OpenSession with working switcher: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeSupervisor {
		t.Fatalf("Mode() = %q, want supervisor", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeSupervisor {
		t.Fatalf("Agent.Mode() = %q, want supervisor", got)
	}
}

// newInstructionModeHarness builds a controller whose agent composes its
// instruction from Controller.Mode, the same dependency the app wires through
// registryOptions.ModeFunc. That makes the difference between applying a mode
// while the old session is still current and applying it after the target
// session became current observable through Agent.Instruction().
func newInstructionModeHarness(t *testing.T) (*session.Controller, *runtime.Agent) {
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
	projector := state.NewProjector(context.Background(), broker)
	var ctrl *session.Controller
	agent, err := runtime.New(runtime.Config{
		Provider:    &echoOnlyProvider{},
		Instruction: "preset=standard",
		InstructionFunc: func() string {
			return "preset=" + ctrl.Mode()
		},
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	ctrl, err = session.New(session.Config{
		Store:       st,
		Broker:      broker,
		Projector:   projector,
		Agent:       agent,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	ctrl.SetAgentModeSwitcher(agent.SetMode)
	t.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})
	return ctrl, agent
}

// TestOpenSessionInstructionFollowsReopenedMode is the regression for the
// Instruction() lag: OpenSession applied the mode before making the target
// session current, so the app's ModeFunc (Controller.Mode) composed the prompt
// from the session being left. Agent.Mode() switched immediately while
// Agent.Instruction() kept the previous preset until the next Run.
func TestOpenSessionInstructionFollowsReopenedMode(t *testing.T) {
	ctrl, agent := newInstructionModeHarness(t)
	ctx := context.Background()

	creatorID, err := ctrl.CreateSession(ctx, "creator")
	if err != nil {
		t.Fatalf("CreateSession(creator): %v", err)
	}
	if err := ctrl.SetMode(runtime.ModeCreator); err != nil {
		t.Fatalf("SetMode(creator): %v", err)
	}
	if got := agent.Instruction(); got != "preset=creator" {
		t.Fatalf("instruction after SetMode(creator) = %q, want preset=creator", got)
	}

	// A new Standard session resets the shared topology and prompt.
	stdID, err := ctrl.CreateSession(ctx, "standard")
	if err != nil {
		t.Fatalf("CreateSession(standard): %v", err)
	}
	if got := agent.Instruction(); got != "preset=standard" {
		t.Fatalf("instruction for the new standard session = %q, want preset=standard", got)
	}
	if _, err := ctrl.OpenSession(ctx, stdID); err != nil {
		t.Fatalf("OpenSession(standard): %v", err)
	}

	// Reopening the Creator session must switch the topology and the prompt
	// together, not leave the instruction describing Standard.
	if _, err := ctrl.OpenSession(ctx, creatorID); err != nil {
		t.Fatalf("OpenSession(creator): %v", err)
	}
	if got := ctrl.CurrentSessionID(); got != creatorID {
		t.Fatalf("current session = %q, want %q", got, creatorID)
	}
	if got := agent.Mode(); got != runtime.ModeCreator {
		t.Fatalf("Agent.Mode() after OpenSession(creator) = %q, want creator", got)
	}
	if got := agent.Instruction(); got != "preset=creator" {
		t.Fatalf("Agent.Instruction() after OpenSession(creator) = %q, want preset=creator", got)
	}
}

// TestOpenSessionRestoresPreviousSessionOnModeFailure pins the failure half of
// the same contract: a switcher rejection leaves the previous session current,
// so Mode(), ModeFunc consumers and Agent.Instruction() all keep describing it.
func TestOpenSessionRestoresPreviousSessionOnModeFailure(t *testing.T) {
	ctrl, agent := newInstructionModeHarness(t)
	ctx := context.Background()

	creatorID, err := ctrl.CreateSession(ctx, "creator")
	if err != nil {
		t.Fatalf("CreateSession(creator): %v", err)
	}
	if err := ctrl.SetMode(runtime.ModeCreator); err != nil {
		t.Fatalf("SetMode(creator): %v", err)
	}
	stdID, err := ctrl.CreateSession(ctx, "standard")
	if err != nil {
		t.Fatalf("CreateSession(standard): %v", err)
	}
	if got := ctrl.CurrentSessionID(); got != stdID {
		t.Fatalf("current session = %q, want the standard session %q", got, stdID)
	}

	boom := errors.New("cannot rebuild")
	ctrl.SetAgentModeSwitcher(func(string) error { return boom })
	if _, err := ctrl.OpenSession(ctx, creatorID); err == nil || !errors.Is(err, boom) {
		t.Fatalf("OpenSession(creator) error = %v, want %v", err, boom)
	}
	if got := ctrl.CurrentSessionID(); got != stdID {
		t.Fatalf("current session after failed open = %q, want the previous session %q", got, stdID)
	}
	if got := ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Mode() after failed open = %q, want standard", got)
	}
	if got := agent.Mode(); got != runtime.ModeStandard {
		t.Fatalf("Agent.Mode() after failed open = %q, want standard", got)
	}
	if got := agent.Instruction(); got != "preset=standard" {
		t.Fatalf("Agent.Instruction() after failed open = %q, want preset=standard", got)
	}
}

// TestControllerCreateSessionAppliesDefaultPreset covers the shared creation
// path: automation's CreateSession and the shell's new-session buttons both
// start in the preset Settings > Agent presets advertises.
func TestControllerCreateSessionAppliesDefaultPreset(t *testing.T) {
	h := newModeHarness(t, &echoOnlyProvider{}, nil)
	if err := h.store.SetSetting("agent.preset", runtime.ModeCreator); err != nil {
		t.Fatal(err)
	}
	id, err := h.ctrl.CreateSession(context.Background(), "automation")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeCreator {
		t.Fatalf("new session Mode() = %q, want creator", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeCreator {
		t.Fatalf("new session Agent.Mode() = %q, want creator", got)
	}
	if value, err := h.store.GetSetting(session.SessionModeKey(id)); err != nil || value != runtime.ModeCreator {
		t.Fatalf("stored session mode = %q, %v; want creator", value, err)
	}

	// An invalid preset is ignored so a bad settings row cannot make session
	// creation fail.
	if err := h.store.SetSetting("agent.preset", "turbo"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ctrl.CreateSession(context.Background(), "bad preset"); err != nil {
		t.Fatalf("CreateSession with invalid preset: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeStandard {
		t.Fatalf("invalid preset Mode() = %q, want standard", got)
	}
}

func TestControllerGoalAndTeamFromToolResults(t *testing.T) {
	provider := &toolCallsThenTextProvider{calls: []runtime.ToolCall{
		{
			ID:        "call-goal",
			Name:      "goal",
			Arguments: json.RawMessage(`{"action":"set","objective":"Ship the release","steps":["test","notes"],"progress":25}`),
		},
		{
			ID:        "call-sub",
			Name:      "subagent",
			Arguments: json.RawMessage(`{"task":"check the tests","agent":"researcher"}`),
		},
	}}
	runner := &scriptedSubagentRunner{result: runtime.SubagentResult{
		Agent:  "researcher",
		Task:   "check the tests",
		Output: "all tests pass",
		Depth:  1,
		Tools:  []string{"read_file"},
	}}
	var sub *tools.SubagentTool
	h := newModeHarness(t, provider, func(reg *runtime.Registry) {
		if err := reg.Register(tools.NewGoalTool()); err != nil {
			t.Fatal(err)
		}
		sub = tools.NewSubagentTool(tools.SubagentConfig{})
		if err := reg.Register(sub); err != nil {
			t.Fatal(err)
		}
	})
	// runtime.New auto-binds the real Agent; the test overrides it with the
	// scripted child runner so no second model call is needed.
	sub.SetSubagentRunner(runner)

	if err := h.ctrl.Submit(context.Background(), h.sessionID, "start the work"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitControllerIdle(t, h.ctrl)

	goal := h.ctrl.Goal()
	if goal == nil {
		t.Fatal("Goal() = nil after the goal tool ran")
	}
	if goal.Objective != "Ship the release" || goal.Progress != 25 || goal.Status != tools.GoalActive {
		t.Fatalf("goal = %+v", goal)
	}
	if len(goal.Steps) != 2 || goal.Steps[0] != "test" || goal.Steps[1] != "notes" {
		t.Fatalf("goal steps = %v", goal.Steps)
	}
	if sessionGoal := h.ctrl.SessionGoal(h.sessionID); sessionGoal == nil || sessionGoal.Objective != goal.Objective {
		t.Fatalf("SessionGoal() = %+v", sessionGoal)
	}

	team := h.ctrl.Team()
	if len(team) != 1 {
		t.Fatalf("Team() = %+v, want one teammate", team)
	}
	member := team[0]
	if member.Name != "researcher" || member.Task != "check the tests" ||
		member.Status != tools.SubagentStatusCompleted || member.Output != "all tests pass" ||
		member.CallID != "call-sub" {
		t.Fatalf("teammate = %+v", member)
	}
	if reqs := runner.reqs; len(reqs) != 1 || reqs[0].Task != "check the tests" || reqs[0].Depth != 1 {
		t.Fatalf("runner requests = %+v", reqs)
	}
	if got := provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}

	// Copies are independent: mutating the returned goal/team must not change
	// the controller's state.
	goal.Objective = "mutated"
	goal.Steps[0] = "mutated"
	team[0].Name = "mutated"
	if got := h.ctrl.Goal(); got.Objective != "Ship the release" || got.Steps[0] != "test" {
		t.Fatalf("Goal() leaked state: %+v", got)
	}
	if got := h.ctrl.Team(); got[0].Name != "researcher" {
		t.Fatalf("Team() leaked state: %+v", got)
	}

	// SetGoal replaces and nil clears; SetTeam replaces and empty clears.
	h.ctrl.SetGoal(&tools.Goal{Objective: "Second", Progress: 5, Status: tools.GoalActive})
	if got := h.ctrl.Goal(); got == nil || got.Objective != "Second" {
		t.Fatalf("SetGoal = %+v", got)
	}
	h.ctrl.SetGoal(nil)
	if got := h.ctrl.Goal(); got != nil {
		t.Fatalf("SetGoal(nil) = %+v", got)
	}
	h.ctrl.SetTeam([]tools.Teammate{{Name: "solo", Status: tools.SubagentStatusCompleted}})
	if got := h.ctrl.Team(); len(got) != 1 || got[0].Name != "solo" {
		t.Fatalf("SetTeam = %+v", got)
	}
	h.ctrl.SetTeam(nil)
	if got := h.ctrl.Team(); len(got) != 0 {
		t.Fatalf("SetTeam(nil) = %+v", got)
	}

	// Deleting the session drops the folded state.
	if err := h.ctrl.DeleteSession(context.Background(), h.sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := h.ctrl.SessionGoal(h.sessionID); got != nil {
		t.Fatalf("SessionGoal after delete = %+v", got)
	}
	if got := h.ctrl.SessionTeam(h.sessionID); len(got) != 0 {
		t.Fatalf("SessionTeam after delete = %+v", got)
	}
}

// echoOnlyProvider answers every model call with text so the tests that only
// exercise controller state do not need any tool.
type echoOnlyProvider struct{}

func (p *echoOnlyProvider) Stream(ctx context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	out := make(chan runtime.Chunk, 1)
	out <- runtime.Chunk{Text: "ok", Done: true}
	close(out)
	return out, nil
}

// modeNamedTool is a nameable runtime.Tool for the mode routing test.
type modeNamedTool struct{ name string }

func (t *modeNamedTool) Name() string        { return t.name }
func (t *modeNamedTool) Description() string { return "mode stub " + t.name }
func (t *modeNamedTool) Invoke(_ context.Context, _ json.RawMessage) (string, error) {
	return t.name + " ok", nil
}

func sessionHasTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestControllerDSHModesPersistAndRouteTools proves a mode switch persists per
// session and rebuilds the shared agent with that preset's tool set.
func TestControllerDSHModesPersistAndRouteTools(t *testing.T) {
	h := newModeHarness(t, &echoOnlyProvider{}, func(reg *runtime.Registry) {
		for _, name := range []string{"bash", "read_file", "terminal_open", "read_image", "working_directory"} {
			if err := reg.Register(&modeNamedTool{name: name}); err != nil {
				t.Fatal(err)
			}
		}
	})
	h.ctrl.SetAgentModeSwitcher(h.agent.SetMode)
	key := session.SessionModeKey(h.sessionID)

	cases := []struct {
		mode    string
		present []string
		absent  []string
	}{
		{
			mode:    runtime.ModeStandard,
			present: []string{"bash", "read_file", "terminal_open", "read_image", "working_directory"},
			absent:  []string{"settings_get", "settings_set"},
		},
		{
			mode:    runtime.ModeMinimal,
			present: []string{"terminal_open", "read_image", "working_directory"},
			absent:  []string{"bash", "read_file", "settings_get", "settings_set"},
		},
		{
			mode:    runtime.ModeCreator,
			present: []string{"bash", "read_file", "terminal_open", "read_image", "working_directory", "settings_get", "settings_set"},
			absent:  []string{},
		},
	}
	for _, tc := range cases {
		if err := h.ctrl.SetMode(tc.mode); err != nil {
			t.Fatalf("SetMode(%s): %v", tc.mode, err)
		}
		if got := h.ctrl.Mode(); got != tc.mode {
			t.Fatalf("Ctrl.Mode() = %q, want %q", got, tc.mode)
		}
		if stored, err := h.store.GetSetting(key); err != nil || stored != tc.mode {
			t.Fatalf("stored mode = %q, %v; want %q", stored, err, tc.mode)
		}
		if got := h.agent.Mode(); got != tc.mode {
			t.Fatalf("Agent.Mode() = %q, want %q", got, tc.mode)
		}
		names := h.agent.ToolNames()
		for _, name := range tc.present {
			if !sessionHasTool(names, name) {
				t.Fatalf("%s tools = %v, want %q", tc.mode, names, name)
			}
		}
		for _, name := range tc.absent {
			if sessionHasTool(names, name) {
				t.Fatalf("%s tools = %v, do not want %q", tc.mode, names, name)
			}
		}
	}

	// Reopening a session restores its persisted preset and applies it.
	if _, err := h.ctrl.CreateSession(context.Background(), "other-modes"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.ctrl.OpenSession(context.Background(), h.sessionID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got := h.ctrl.Mode(); got != runtime.ModeCreator {
		t.Fatalf("reopened Mode() = %q, want creator", got)
	}
	if got := h.agent.Mode(); got != runtime.ModeCreator {
		t.Fatalf("reopened Agent.Mode() = %q, want creator", got)
	}
}
