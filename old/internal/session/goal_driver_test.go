package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

const continuationPrefix = "Continue working toward the goal. Objective: "

// goalDriverProvider scripts one goal tool call per user turn and answers with
// text after the tool result, which is enough for the runtime's tool loop.
// completeAt completes the goal on that 1-based user turn; 0 never completes.
type goalDriverProvider struct {
	mu         sync.Mutex
	reqs       []runtime.Request
	turns      int
	completeAt int
	objective  string
	noGoal     bool
	blockAuto  atomic.Bool
}

func (p *goalDriverProvider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	call := len(p.reqs) - 1
	objective := p.objective
	completeAt := p.completeAt
	noGoal := p.noGoal
	p.mu.Unlock()

	_, auto := goalLastUser(req.Messages)
	if auto && p.blockAuto.Load() {
		out := make(chan runtime.Chunk)
		go func() {
			defer close(out)
			<-ctx.Done()
		}()
		return out, nil
	}

	if goalToolResultAfterLastUser(req.Messages) || noGoal {
		return goalChunkStream([]runtime.Chunk{{Text: "turn done", Done: true}}), nil
	}

	p.mu.Lock()
	p.turns++
	turn := p.turns
	p.mu.Unlock()
	if completeAt > 0 && turn >= completeAt {
		return goalToolCallStream(call, `{"action":"complete"}`), nil
	}
	progress := turn * 10
	if progress > 90 {
		progress = 90
	}
	args, err := json.Marshal(map[string]any{
		"action":    "set",
		"objective": objective,
		"steps":     []string{"write the driver", "test it"},
		"progress":  progress,
	})
	if err != nil {
		return nil, err
	}
	return goalToolCallStream(call, string(args)), nil
}

func (p *goalDriverProvider) setObjective(objective string) {
	p.mu.Lock()
	p.objective = objective
	p.mu.Unlock()
}

func (p *goalDriverProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

func (p *goalDriverProvider) requestAt(i int) (runtime.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.reqs) {
		return runtime.Request{}, false
	}
	return p.reqs[i], true
}

func goalChunkStream(chunks []runtime.Chunk) <-chan runtime.Chunk {
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out
}

func goalToolCallStream(call int, args string) <-chan runtime.Chunk {
	return goalChunkStream([]runtime.Chunk{{
		ToolCalls: []runtime.ToolCall{{
			ID:        fmt.Sprintf("goal-call-%d", call),
			Name:      "goal",
			Arguments: json.RawMessage(args),
		}},
		Done: true,
	}})
}

func goalLastUser(messages []runtime.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != runtime.RoleUser {
			continue
		}
		content := messages[i].Content
		return content, strings.HasPrefix(strings.TrimSpace(content), continuationPrefix)
	}
	return "", false
}

func goalToolResultAfterLastUser(messages []runtime.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case runtime.RoleUser:
			return false
		case runtime.RoleTool:
			return true
		}
	}
	return false
}

// newGoalDriverHarness builds the shared mode harness with a real goal tool and
// installs the driver through SetGoalDriver (the Config path is exercised by the
// app wiring and by TestGoalDriverConfigEnablesDriver).
func newGoalDriverHarness(t *testing.T, provider runtime.Provider, driver *session.GoalRoundDriver) *modeHarness {
	t.Helper()
	h := newModeHarness(t, provider, func(reg *runtime.Registry) {
		if err := reg.Register(tools.NewGoalTool()); err != nil {
			t.Fatalf("register goal: %v", err)
		}
	})
	h.ctrl.SetGoalDriver(driver)
	return h
}

func waitGoalCondition(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitGoalRounds(t *testing.T, ctrl *session.Controller, sessionID string, want int) {
	t.Helper()
	waitGoalCondition(t, fmt.Sprintf("%d goal rounds (last %d)", want, ctrl.GoalRounds(sessionID)), func() bool {
		return ctrl.GoalRounds(sessionID) == want
	})
}

func assertGoalRoundsStay(t *testing.T, ctrl *session.Controller, sessionID string, want int) {
	t.Helper()
	if got := ctrl.GoalRounds(sessionID); got != want {
		t.Fatalf("GoalRounds(%q) = %d, want %d", sessionID, got, want)
	}
}

func goalUserMessages(t *testing.T, h *modeHarness) []string {
	t.Helper()
	stored, err := h.store.ListMessages(h.sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var out []string
	for _, msg := range stored {
		if msg.Role == string(state.RoleUser) {
			out = append(out, msg.Content)
		}
	}
	return out
}

func TestGoalDriverContinuesUntilComplete(t *testing.T) {
	provider := &goalDriverProvider{completeAt: 3, objective: "Ship the goal driver"}
	h := newGoalDriverHarness(t, provider, session.NewGoalRoundDriver())
	ctx := context.Background()

	if err := h.ctrl.Submit(ctx, h.sessionID, "start"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitGoalCondition(t, "the goal to complete after automatic continuations", func() bool {
		goal := h.ctrl.SessionGoal(h.sessionID)
		return goal != nil && goal.Status == tools.GoalCompleted &&
			h.ctrl.GoalRounds(h.sessionID) == 2 && h.ctrl.Snapshot().Phase == state.PhaseIdle
	})
	h.ctrl.Wait()

	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)
	goal := h.ctrl.SessionGoal(h.sessionID)
	if goal.Objective != "Ship the goal driver" || goal.Progress != 100 || goal.Status != tools.GoalCompleted {
		t.Fatalf("completed goal = %+v", goal)
	}

	// The initial prompt plus exactly one user prompt per continuation turn.
	users := goalUserMessages(t, h)
	if len(users) != 3 {
		t.Fatalf("user prompts = %d, want 3: %q", len(users), users)
	}
	if users[0] != "start" {
		t.Fatalf("first user prompt = %q, want start", users[0])
	}
	for i, wantProgress := range []int{10, 20} {
		want := fmt.Sprintf("%sShip the goal driver. Progress: %d%% (steps: write the driver; test it). Mark the goal complete when done.", continuationPrefix, wantProgress)
		if users[i+1] != want {
			t.Fatalf("continuation prompt %d = %q, want %q", i+1, users[i+1], want)
		}
	}

	// Two model calls per turn: tool call then final text.
	if got := provider.requestCount(); got != 6 {
		t.Fatalf("provider calls = %d, want 6", got)
	}

	// A completed goal must not spawn another turn after the idle transition.
	time.Sleep(50 * time.Millisecond)
	h.ctrl.Wait()
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)
	if got := provider.requestCount(); got != 6 {
		t.Fatalf("provider calls after idle = %d, want 6", got)
	}
}

func TestGoalDriverMaxRoundsCaps(t *testing.T) {
	tests := []struct {
		name    string
		max     int
		setting string
		want    int
	}{
		{name: "config budget", max: 3, want: 3},
		{name: "per-session setting", max: 6, setting: "2", want: 2},
		{name: "hard cap", max: 50, want: session.MaxGoalRounds},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &goalDriverProvider{objective: "Never complete"}
			h := newGoalDriverHarness(t, provider, session.NewGoalRoundDriver(session.GoalRoundDriverConfig{MaxRounds: tt.max}))
			if tt.setting != "" {
				if err := h.store.SetSetting(session.SessionGoalRoundsKey(h.sessionID), tt.setting); err != nil {
					t.Fatalf("SetSetting: %v", err)
				}
			}
			if err := h.ctrl.Submit(context.Background(), h.sessionID, "start"); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			waitGoalRounds(t, h.ctrl, h.sessionID, tt.want)
			waitIdleAndDrain(t, h.ctrl)
			assertGoalRoundsStay(t, h.ctrl, h.sessionID, tt.want)
			goal := h.ctrl.SessionGoal(h.sessionID)
			if goal == nil || goal.Status != tools.GoalActive {
				t.Fatalf("goal = %+v, want an active goal at the cap", goal)
			}

			// The initial turn plus one turn per continuation, two calls each.
			if got, want := provider.requestCount(), 2*(tt.want+1); got != want {
				t.Fatalf("provider calls = %d, want %d", got, want)
			}
			if got, want := len(goalUserMessages(t, h)), tt.want+1; got != want {
				t.Fatalf("user prompts = %d, want %d", got, want)
			}

			// Exhausted budget must stay exhausted: no extra turn appears.
			time.Sleep(50 * time.Millisecond)
			h.ctrl.Wait()
			assertGoalRoundsStay(t, h.ctrl, h.sessionID, tt.want)
			if got, want := provider.requestCount(), 2*(tt.want+1); got != want {
				t.Fatalf("provider calls after idle = %d, want %d", got, want)
			}
		})
	}
}

func TestGoalDriverStopsOnUserStop(t *testing.T) {
	provider := &goalDriverProvider{objective: "Stop test"}
	provider.blockAuto.Store(true)
	h := newGoalDriverHarness(t, provider, session.NewGoalRoundDriver())
	ctx := context.Background()

	if err := h.ctrl.Submit(ctx, h.sessionID, "start"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Wait until the automatic continuation turn has actually started and its
	// provider stream is blocked, then stop it.
	waitGoalCondition(t, "the automatic continuation to block in the provider", func() bool {
		return h.ctrl.GoalRounds(h.sessionID) == 1 &&
			provider.requestCount() == 3 &&
			h.ctrl.Snapshot().Phase == state.PhaseRunning
	})
	h.ctrl.Stop()
	waitIdleAndDrain(t, h.ctrl)

	// Stop cancels the current continuation and suppresses the next one, so the
	// counted round never grows past the turn the user stopped.
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 1)
	if got := provider.requestCount(); got != 3 {
		t.Fatalf("provider calls after Stop = %d, want 3", got)
	}
	if goal := h.ctrl.SessionGoal(h.sessionID); goal == nil || goal.Status != tools.GoalActive {
		t.Fatalf("goal after Stop = %+v, want still active", goal)
	}
	time.Sleep(50 * time.Millisecond)
	h.ctrl.Wait()
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 1)
	if got := provider.requestCount(); got != 3 {
		t.Fatalf("provider calls after Stop settled = %d, want 3", got)
	}
}

func TestGoalDriverNoGoalNoContinuation(t *testing.T) {
	tests := []struct {
		name string
		goal *tools.Goal
	}{
		{name: "no goal"},
		{name: "completed goal", goal: &tools.Goal{Objective: "already done", Status: tools.GoalCompleted, Progress: 100}},
		{name: "abandoned goal", goal: &tools.Goal{Objective: "gave up", Status: tools.GoalAbandoned, Progress: 30}},
		{name: "empty objective", goal: &tools.Goal{Objective: "   ", Status: tools.GoalActive, Progress: 10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &goalDriverProvider{noGoal: true, objective: "unused"}
			h := newGoalDriverHarness(t, provider, session.NewGoalRoundDriver())
			if tt.goal != nil {
				h.ctrl.SetGoal(tt.goal)
			}
			if err := h.ctrl.Submit(context.Background(), h.sessionID, "hello"); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			waitIdleAndDrain(t, h.ctrl)
			assertGoalRoundsStay(t, h.ctrl, h.sessionID, 0)
			if got := provider.requestCount(); got != 1 {
				t.Fatalf("provider calls = %d, want 1 (no continuation turn)", got)
			}
			if got := len(goalUserMessages(t, h)); got != 1 {
				t.Fatalf("user prompts = %d, want 1", got)
			}
			time.Sleep(30 * time.Millisecond)
			h.ctrl.Wait()
			assertGoalRoundsStay(t, h.ctrl, h.sessionID, 0)
			if got := provider.requestCount(); got != 1 {
				t.Fatalf("provider calls after idle = %d, want 1", got)
			}
		})
	}
}

func TestGoalDriverSingleSubmitPerIdle(t *testing.T) {
	provider := &goalDriverProvider{objective: "One per idle"}
	h := newGoalDriverHarness(t, provider, session.NewGoalRoundDriver(session.GoalRoundDriverConfig{MaxRounds: 3}))
	ctx := context.Background()

	if err := h.ctrl.Submit(ctx, h.sessionID, "start"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitGoalRounds(t, h.ctrl, h.sessionID, 3)
	waitIdleAndDrain(t, h.ctrl)

	stored, err := h.store.ListMessages(h.sessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	userTurns := map[string]bool{}
	users := 0
	for _, msg := range stored {
		if msg.Role != string(state.RoleUser) {
			continue
		}
		users++
		if msg.TurnID == "" {
			t.Fatalf("user message %q has no turn id", msg.Content)
		}
		userTurns[msg.TurnID] = true
	}
	// Initial turn plus three continuation turns, one distinct turn each: no
	// idle transition submitted a duplicate continuation.
	if users != 4 || len(userTurns) != 4 {
		t.Fatalf("user prompts = %d across %d turns, want 4/4: %+v", users, len(userTurns), stored)
	}
	if got := provider.requestCount(); got != 8 {
		t.Fatalf("provider calls = %d, want 8", got)
	}
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 3)

	time.Sleep(50 * time.Millisecond)
	h.ctrl.Wait()
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 3)
	if got := provider.requestCount(); got != 8 {
		t.Fatalf("provider calls after idle = %d, want 8", got)
	}
}

func TestGoalRoundsResetOnNewGoal(t *testing.T) {
	provider := &goalDriverProvider{objective: "First objective"}
	h := newGoalDriverHarness(t, provider, session.NewGoalRoundDriver(session.GoalRoundDriverConfig{MaxRounds: 2}))
	ctx := context.Background()

	if err := h.ctrl.Submit(ctx, h.sessionID, "start"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitGoalRounds(t, h.ctrl, h.sessionID, 2)
	waitIdleAndDrain(t, h.ctrl)
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)

	// Progress updates for the same active objective are not a new goal: the
	// budget must survive so the cap remains enforceable.
	first := h.ctrl.SessionGoal(h.sessionID)
	if first == nil {
		t.Fatal("SessionGoal = nil")
	}
	progressOnly := *first
	progressOnly.Progress = 55
	h.ctrl.SetGoal(&progressOnly)
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)

	// A new objective resets the count.
	provider.setObjective("Second objective")
	h.ctrl.SetGoal(&tools.Goal{Objective: "Second objective", Status: tools.GoalActive, Progress: 5})
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 0)

	if err := h.ctrl.Submit(ctx, h.sessionID, "again"); err != nil {
		t.Fatalf("Submit again: %v", err)
	}
	waitGoalRounds(t, h.ctrl, h.sessionID, 2)
	waitIdleAndDrain(t, h.ctrl)
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)
	if goal := h.ctrl.SessionGoal(h.sessionID); goal == nil || goal.Objective != "Second objective" {
		t.Fatalf("goal after reset rounds = %+v, want Second objective", goal)
	}

	// The continuation prompts must carry the new objective, not the old one.
	found := false
	for _, prompt := range goalUserMessages(t, h) {
		if strings.Contains(prompt, continuationPrefix+"Second objective. Progress: ") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no continuation prompt named the new objective: %q", goalUserMessages(t, h))
	}
}

// TestGoalDriverConfigEnablesDriver covers the Config.GoalDriver wiring without
// SetGoalDriver: the controller must continue the active goal from the very
// first turn.
func TestGoalDriverConfigEnablesDriver(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ws, err := st.CreateWorkspace("goal-config", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	registry := runtime.NewRegistry()
	if err := registry.Register(tools.NewGoalTool()); err != nil {
		t.Fatalf("register goal: %v", err)
	}
	provider := &goalDriverProvider{completeAt: 2, objective: "Config wiring"}
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
		GoalDriver:  session.NewGoalRoundDriver(),
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
		_ = st.Close()
	})

	id, err := ctrl.CreateSession(context.Background(), "config goal")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(context.Background(), id, "start"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitGoalCondition(t, "Config.GoalDriver to complete the goal", func() bool {
		goal := ctrl.SessionGoal(id)
		return goal != nil && goal.Status == tools.GoalCompleted &&
			ctrl.GoalRounds(id) == 1
	})
	ctrl.Wait()
	assertGoalRoundsStay(t, ctrl, id, 1)
}
