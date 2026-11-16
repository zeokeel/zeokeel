package agent

// Tests for the engine-side goal continuation driver and the run-origin seam:
// a successful user run chains goal rounds until the cap blocks the goal, a
// failed/cancelled run never continues, a user Stop disarms the goal,
// automation/subagent runs never continue a goal, and every tool invocation
// carries the session and origin.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeGoalService is a scripted per-session contracts.GoalService whose rounds
// behave like the durable backend: AdmitRound counts and blocks at the cap.
type fakeGoalService struct {
	mu sync.Mutex

	goal   contracts.Goal
	has    bool
	armed  bool
	admits int
	// admitErr forces AdmitRound to fail (transport/backend failures).
	admitErr error
	// forgets counts Forget calls (agent.DeleteSession).
	forgets int
	// rollbacks counts RollbackRound calls (BE-10 send-failure refund);
	// rollbackRound remembers the last refunded round number.
	rollbacks     int
	rollbackRound int
	// forkCopies records goal copies requested by ForkSession.
	forkCopies int
	forkFrom   string
	forkTo     string
}

func newFakeGoalService(objective string, maxGoalRounds int) *fakeGoalService {
	return &fakeGoalService{
		has:   true,
		armed: true,
		goal: contracts.Goal{
			SessionID: "unset", ID: "goal-fake-1", Revision: 1, Objective: objective,
			Phase: contracts.GoalPhaseActive, MaxGoalRounds: maxGoalRounds,
		},
	}
}

func (f *fakeGoalService) Goal(string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has {
		return contracts.Goal{}, false
	}
	goal := f.goal
	if f.armed && goal.Phase == contracts.GoalPhaseActive {
		goal.Activation = contracts.GoalActivationArmed
	} else {
		goal.Activation = contracts.GoalActivationDisarmed
	}
	return goal, true
}

func (f *fakeGoalService) CreateGoal(context.Context, string, string, int) (contracts.Goal, error) {
	return contracts.Goal{}, errors.New("not used in these tests")
}

func (f *fakeGoalService) UpdateGoal(context.Context, string, contracts.GoalRef, contracts.GoalUpdate) (contracts.Goal, error) {
	return contracts.Goal{}, errors.New("not used in these tests")
}

func (f *fakeGoalService) AdmitRound(context.Context, string) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.admitErr != nil {
		return contracts.Goal{}, f.admitErr
	}
	if !f.has {
		return contracts.Goal{}, contracts.ErrGoalNotFound
	}
	if f.goal.Phase != contracts.GoalPhaseActive {
		return contracts.Goal{}, contracts.ErrGoalInvalidTransition
	}
	if f.goal.RoundsStarted >= f.goal.MaxGoalRounds {
		f.goal.Phase = contracts.GoalPhaseBlocked
		f.goal.BlockedReason = &contracts.GoalBlockedReason{
			Code: contracts.GoalBlockedCodeRoundLimit, Message: "round limit reached",
		}
		f.armed = false
		f.goal.Revision++
		return contracts.Goal{}, contracts.ErrGoalRoundLimit
	}
	f.admits++
	f.goal.RoundsStarted++
	f.goal.Revision++
	goal := f.goal
	goal.Activation = contracts.GoalActivationArmed
	return goal, nil
}

func (f *fakeGoalService) Disarm(string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed = false
	if !f.has {
		return contracts.Goal{}, false
	}
	goal := f.goal
	goal.Activation = contracts.GoalActivationDisarmed
	return goal, true
}

func (f *fakeGoalService) ClearGoal(context.Context, string, contracts.GoalRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hadGoal := f.has
	f.has = false
	f.armed = false
	return hadGoal, nil
}

// CopyGoal implements the optional goal-service fork hook.
func (f *fakeGoalService) CopyGoal(_ context.Context, fromSessionID, toSessionID string) (contracts.Goal, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has {
		return contracts.Goal{}, false, nil
	}
	f.forkCopies++
	f.forkFrom = fromSessionID
	f.forkTo = toSessionID
	copied := f.goal
	copied.SessionID = toSessionID
	copied.ID = "goal-fake-copy"
	copied.Revision = 1
	copied.Activation = contracts.GoalActivationDisarmed
	return copied, true, nil
}

// forkCopyCount reports how many fork copies the fake served.
func (f *fakeGoalService) forkCopyCount() (int, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forkCopies, f.forkFrom, f.forkTo
}

// Forget implements the optional goal-service delete hook.
func (f *fakeGoalService) Forget(string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgets++
	f.has = false
	f.armed = false
}

// RollbackRound implements the optional BE-10 refund hook: it refunds exactly
// the admitted round when nothing else advanced the goal (like the durable
// backend).
func (f *fakeGoalService) RollbackRound(_ context.Context, _ string, round int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollbacks++
	f.rollbackRound = round
	if f.has && f.goal.Phase == contracts.GoalPhaseActive && f.goal.RoundsStarted == round {
		f.goal.RoundsStarted--
		f.goal.Revision++
	}
	return nil
}

// rollbackCount reports how many round refunds the driver requested.
func (f *fakeGoalService) rollbackCount() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rollbacks, f.rollbackRound
}

func (f *fakeGoalService) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// snapshot returns a copy of the fake goal state.
func (f *fakeGoalService) snapshot() (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.goal, f.armed
}

// forgetCount returns how many times DeleteSession forgot the cached goal.
func (f *fakeGoalService) forgetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forgets
}

// admitCount returns how many continuation rounds the driver admitted.
func (f *fakeGoalService) admitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admits
}

// userMessages returns the session's user-role messages in order.
func userMessages(session contracts.ChatSession) []contracts.ChatMessage {
	out := make([]contracts.ChatMessage, 0, len(session.Messages))
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleUser {
			out = append(out, message)
		}
	}
	return out
}

// TestGoalContinuationChainsRoundsUntilCap covers the happy path: a successful
// user run is followed by automatic goal rounds, each marked with a GoalRound
// and a DSH-style prompt, until the round cap blocks the goal.
func TestGoalContinuationChainsRoundsUntilCap(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "capture"}), nil)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	goals := newFakeGoalService("ship the release", 2)
	svc.goalService = goals

	sessionID, err := svc.NewSession("goal chain")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the goal to reach its round cap", func() bool {
		goal, _ := goals.snapshot()
		return goal.Phase != contracts.GoalPhaseActive
	})
	session := waitIdle(t, svc, sessionID)

	if got := goals.admitCount(); got != 2 {
		t.Fatalf("admitted rounds = %d, want 2", got)
	}
	users := userMessages(session)
	if len(users) != 3 {
		t.Fatalf("user messages = %d, want 3 (start + 2 goal rounds)", len(users))
	}
	if users[0].GoalRound != nil {
		t.Fatalf("the human message carries a goal round marker: %+v", users[0].GoalRound)
	}
	for index, wantRound := range []int{1, 2} {
		message := users[index+1]
		if message.GoalRound == nil {
			t.Fatalf("goal round %d message has no GoalRound marker", wantRound)
		}
		if message.GoalRound.Round != wantRound || message.GoalRound.MaxGoalRounds != 2 ||
			message.GoalRound.GoalID != "goal-fake-1" {
			t.Fatalf("goal round %d marker = %+v, want round %d/2", wantRound, message.GoalRound, wantRound)
		}
		if !strings.Contains(message.Text, "ship the release") {
			t.Fatalf("goal round %d prompt = %q, want the objective", wantRound, message.Text)
		}
		if !strings.Contains(message.Text, "Round "+strconv.Itoa(wantRound)+"/2") {
			t.Fatalf("goal round %d prompt = %q, want the round budget", wantRound, message.Text)
		}
	}
	// Every assistant turn answered; the chain stopped without an error row.
	for index := 1; index < len(session.Messages); index += 2 {
		if session.Messages[index].Role != contracts.ChatRoleAssistant || session.Messages[index].Error != "" {
			t.Fatalf("assistant turn %d = %+v, want a clean answer", index, session.Messages[index])
		}
	}
	goal, armed := goals.snapshot()
	if goal.Phase != contracts.GoalPhaseBlocked || goal.BlockedReason == nil ||
		goal.BlockedReason.Code != contracts.GoalBlockedCodeRoundLimit {
		t.Fatalf("goal after the cap = %+v, want blocked(round-limit)", goal)
	}
	if armed {
		t.Fatal("goal stayed armed after the round limit block")
	}
}

// TestGoalContinuationRequiresASuccessfulRun covers a provider failure: no
// round is admitted and no continuation starts.
func TestGoalContinuationRequiresASuccessfulRun(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) {
		return failingProvider{}, nil
	}
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, _ := svc.NewSession("failing")
	if err := svc.Send(sessionID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	time.Sleep(20 * time.Millisecond)
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("admitted rounds after a failed run = %d, want 0", got)
	}
	if len(userMessages(session)) != 1 {
		t.Fatalf("user messages = %d, want only the human message", len(userMessages(session)))
	}
	if session.Messages[len(session.Messages)-1].Error == "" {
		t.Fatal("the failed run left no error row")
	}
}

// TestGoalContinuationSkippedForAutomationSessions pins the provenance rule: an
// automation session (origin setting) never chains goal rounds.
func TestGoalContinuationSkippedForAutomationSessions(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, _ := svc.NewSession("automation")
	if err := settings.Set(contracts.SessionOriginSettingKey(sessionID), contracts.SessionOriginAutomation); err != nil {
		t.Fatalf("set origin: %v", err)
	}
	if err := svc.Send(sessionID, "scheduled task"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	time.Sleep(20 * time.Millisecond)
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("automation session admitted %d goal rounds, want 0", got)
	}
}

// TestGoalContinuationSkipsARunningSession covers the preemption guard: when the
// session is already running (a queued user message won the race), no round is
// admitted.
func TestGoalContinuationSkipsARunningSession(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, _ := svc.NewSession("running")
	svc.mu.Lock()
	svc.sessions[sessionID].running = true
	svc.mu.Unlock()
	svc.continueGoalAfterRun(sessionID, contracts.AgentRunOriginUser, nil, context.Background())
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("admitted rounds over a running session = %d, want 0", got)
	}
}

// TestCancelDisarmsTheGoal covers the Stop semantics: Cancel disarms the goal
// and the cancelled run never chains a continuation.
func TestCancelDisarmsTheGoal(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	provider := &blockingProvider{started: make(chan struct{}, 1)}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, _ := svc.NewSession("cancel")
	if err := svc.Send(sessionID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("run never reached the provider")
	}
	if err := svc.Cancel(sessionID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	time.Sleep(20 * time.Millisecond)
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("cancelled run admitted %d goal rounds, want 0", got)
	}
	if _, armed := goals.snapshot(); armed {
		t.Fatal("Cancel left the goal armed")
	}
	assistant := session.Messages[len(session.Messages)-1]
	if assistant.Error != "cancelled" {
		t.Fatalf("assistant error = %q, want cancelled", assistant.Error)
	}
}

// TestGoalContinuationAdmitFailureStops covers a backend failure: the driver
// stops instead of retrying or sending a round.
func TestGoalContinuationAdmitFailureStops(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	goals := newFakeGoalService("objective", 5)
	goals.admitErr = errors.New("store unavailable")
	svc.goalService = goals

	sessionID, _ := svc.NewSession("admit failure")
	if err := svc.Send(sessionID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	time.Sleep(20 * time.Millisecond)
	if len(userMessages(session)) != 1 {
		t.Fatalf("user messages = %d, want only the human message", len(userMessages(session)))
	}
}

// TestRunOriginResolution covers the provenance mapping: explicit origins win,
// then the session origin setting, else the user.
func TestRunOriginResolution(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	cases := []struct {
		name     string
		session  string
		explicit string
		want     string
	}{
		{name: "user session", session: "s1", want: contracts.AgentRunOriginUser},
		{name: "goal continuation", session: "s1", explicit: contracts.AgentRunOriginGoal, want: contracts.AgentRunOriginGoal},
		{name: "automation setting", session: "s2", want: contracts.AgentRunOriginAutomation},
		{name: "subagent setting", session: "s3", want: contracts.AgentRunOriginSubagent},
	}
	for _, test := range cases {
		if test.session == "s2" {
			_ = svc.settings.Set(contracts.SessionOriginSettingKey("s2"), contracts.SessionOriginAutomation)
		}
		if test.session == "s3" {
			_ = svc.settings.Set(contracts.SessionOriginSettingKey("s3"), contracts.SessionOriginSubagent)
		}
		if got := svc.runOrigin(test.session, test.explicit); got != test.want {
			t.Fatalf("%s origin = %q, want %q", test.name, got, test.want)
		}
	}
}

// TestToolContextCarriesSessionAndOrigin covers the tool-scope stamping: the
// process-wide tool instance sees the owning session and the run origin.
func TestToolContextCarriesSessionAndOrigin(t *testing.T) {
	probe := &contextProbeTool{}
	tools := withAgentScope([]rig.Tool{probe}, "sess-9", contracts.AgentRunOriginGoal)
	if len(tools) != 1 {
		t.Fatalf("wrapped tools = %d, want 1", len(tools))
	}
	if _, err := tools[0].Invoke(context.Background(), rig.ToolCall{Name: probe.Name()}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if probe.sessionID != "sess-9" {
		t.Fatalf("tool session = %q, want sess-9", probe.sessionID)
	}
	if probe.origin != contracts.AgentRunOriginGoal {
		t.Fatalf("tool origin = %q, want goal", probe.origin)
	}

	// An empty scope leaves the tool unwrapped.
	if unchanged := withAgentScope([]rig.Tool{probe}, "", ""); unchanged[0] != rig.Tool(probe) {
		t.Fatal("empty scope wrapped the tool")
	}
}

// contextProbeTool records the session/origin it observed.
type contextProbeTool struct {
	sessionID string
	origin    string
}

func (p *contextProbeTool) Name() string           { return "context_probe" }
func (p *contextProbeTool) Description() string    { return "probe" }
func (p *contextProbeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (p *contextProbeTool) Invoke(ctx context.Context, _ rig.ToolCall) (rig.ToolResult, error) {
	p.sessionID = contracts.AgentSessionFromContext(ctx)
	p.origin = contracts.AgentRunOriginFromContext(ctx)
	return rig.ToolResult{Content: "ok"}, nil
}

// failingProvider fails every stream request.
type failingProvider struct{}

func (failingProvider) Name() string { return "failing" }
func (failingProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	return nil, errors.New("provider boom")
}

// sync is used by the fake goal service.
var _ = sync.Mutex{}

// TestDeleteSessionForgetsTheCachedGoal covers the delete hook: DeleteSession
// must drop the goal backend's cached goal/activation for the removed session so
// a late read cannot show (or revive) it.
func TestDeleteSessionForgetsTheCachedGoal(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, err := svc.NewSession("delete")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := goals.forgetCount(); got != 1 {
		t.Fatalf("Forget calls = %d, want 1", got)
	}
	if _, ok := goals.Goal(sessionID); ok {
		t.Fatal("the goal backend still reports a goal for the deleted session")
	}
}

// TestGoalContinuationSkipsATombstonedSession covers the deleted-session guard:
// a session tombstoned without removing the read-model entry (its run state can
// still look idle) must not admit another goal round.
func TestGoalContinuationSkipsATombstonedSession(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, err := svc.NewSession("tombstoned")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.markSessionDeleted(sessionID)
	svc.continueGoalAfterRun(sessionID, contracts.AgentRunOriginUser, nil, context.Background())
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("a tombstoned session admitted %d goal rounds, want 0", got)
	}
}

// TestGoalContinuationSkipsADeletedSession covers the full delete path: no round
// is admitted and the goal cache is dropped.
func TestGoalContinuationSkipsADeletedSession(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(nil), nil)
	goals := newFakeGoalService("objective", 5)
	svc.goalService = goals

	sessionID, err := svc.NewSession("deleted")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	svc.continueGoalAfterRun(sessionID, contracts.AgentRunOriginUser, nil, context.Background())
	if got := goals.admitCount(); got != 0 {
		t.Fatalf("a deleted session admitted %d goal rounds, want 0", got)
	}
	if got := goals.forgetCount(); got != 1 {
		t.Fatalf("Forget calls = %d, want 1", got)
	}
}
