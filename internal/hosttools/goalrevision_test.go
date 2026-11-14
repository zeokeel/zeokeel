package hosttools

// Tests for the DSH goal-tool adapter: session resolution from the tool
// context, the direct-human-turn rules for create/edit/pause/resume, the
// goal-round blocked threshold and the snapshot mapping.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	righosttools "github.com/zeokeel/zeokeel-rig/tools/hosttools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeGoalService is a scripted contracts.GoalService.
type fakeGoalService struct {
	mu sync.Mutex

	goal      contracts.Goal
	has       bool
	updateErr error
	createErr error

	created        int
	createdSession string
	createdRounds  int
	updates        int
	lastSessionID  string
	lastRef        contracts.GoalRef
	lastUpdate     contracts.GoalUpdate
}

func (f *fakeGoalService) Goal(sessionID string) (contracts.Goal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has || f.goal.SessionID != sessionID {
		return contracts.Goal{}, false
	}
	return f.goal, true
}

func (f *fakeGoalService) CreateGoal(_ context.Context, sessionID, objective string, maxGoalRounds int) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return contracts.Goal{}, f.createErr
	}
	f.created++
	f.createdSession = sessionID
	f.createdRounds = maxGoalRounds
	f.goal = contracts.Goal{
		SessionID: sessionID, ID: "goal-1", Revision: 1, Objective: objective,
		Phase: contracts.GoalPhaseActive, RoundsStarted: 0, MaxGoalRounds: maxGoalRounds,
		Activation: contracts.GoalActivationArmed,
	}
	f.has = true
	return f.goal, nil
}

func (f *fakeGoalService) UpdateGoal(_ context.Context, sessionID string, ref contracts.GoalRef, update contracts.GoalUpdate) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return contracts.Goal{}, f.updateErr
	}
	f.updates++
	f.lastSessionID = sessionID
	f.lastRef = ref
	f.lastUpdate = update
	next := f.goal
	next.Revision = ref.Revision + 1
	switch update.Action {
	case contracts.GoalActionPause:
		next.Phase = contracts.GoalPhasePaused
	case contracts.GoalActionResume:
		next.Phase = contracts.GoalPhaseActive
	case contracts.GoalActionComplete:
		next.Phase = contracts.GoalPhaseComplete
	case contracts.GoalActionBlocked:
		next.Phase = contracts.GoalPhaseBlocked
		next.BlockedReason = &contracts.GoalBlockedReason{Code: contracts.GoalBlockedCodeModelReported, Message: update.BlockedReason}
	case contracts.GoalActionEdit:
		if update.Objective != "" {
			next.Objective = update.Objective
		}
	}
	f.goal = next
	return next, nil
}

func (f *fakeGoalService) AdmitRound(_ context.Context, sessionID string) (contracts.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.goal.RoundsStarted++
	return f.goal, nil
}

func (f *fakeGoalService) Disarm(string) (contracts.Goal, bool) {
	return f.goal, f.has
}

func (f *fakeGoalService) ClearGoal(_ context.Context, sessionID string, ref contracts.GoalRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has || f.goal.SessionID != sessionID || f.goal.ID == "" {
		return false, nil
	}
	if ref.ID != f.goal.ID || ref.Revision != f.goal.Revision {
		return false, contracts.ErrGoalStaleRef
	}
	f.has = false
	return true, nil
}

func (f *fakeGoalService) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// goalTurnContext builds a tool context for one session and run origin.
func goalTurnContext(sessionID, origin string) context.Context {
	ctx := context.Background()
	if sessionID != "" {
		ctx = contracts.WithAgentSession(ctx, sessionID)
	}
	return contracts.WithAgentRunOrigin(ctx, origin)
}

func TestGoalRevisionCurrentGoal(t *testing.T) {
	goals := &fakeGoalService{has: true, goal: contracts.Goal{
		SessionID: "sess-1", ID: "goal-7", Revision: 3, Objective: "ship",
		Phase: contracts.GoalPhaseBlocked, RoundsStarted: 4, MaxGoalRounds: 5,
		BlockedReason: &contracts.GoalBlockedReason{Code: contracts.GoalBlockedCodeRoundLimit, Message: "5/5"},
		Activation:    contracts.GoalActivationDisarmed,
	}}
	store := NewGoalRevisionStore(goals)

	if _, ok := store.CurrentGoal(context.Background()); ok {
		t.Fatal("CurrentGoal without a session context reported a goal")
	}
	if _, ok := store.CurrentGoal(goalTurnContext("sess-2", contracts.AgentRunOriginUser)); ok {
		t.Fatal("CurrentGoal of another session reported a goal")
	}
	snapshot, ok := store.CurrentGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser))
	if !ok {
		t.Fatal("CurrentGoal of the owning session reported no goal")
	}
	if snapshot.ID != "goal-7" || snapshot.Revision != 3 || snapshot.Phase != righosttools.GoalPhaseBlocked ||
		snapshot.RoundsStarted != 4 || snapshot.MaxGoalRounds != 5 ||
		snapshot.Activation != righosttools.GoalActivationDisarmed {
		t.Fatalf("snapshot = %+v, want the mapped goal", snapshot)
	}
	if snapshot.BlockedReason == nil || snapshot.BlockedReason.Code != contracts.GoalBlockedCodeRoundLimit {
		t.Fatalf("blocked reason = %+v, want the mapped reason", snapshot.BlockedReason)
	}
}

func TestGoalRevisionCreateNeedsAHumanTurn(t *testing.T) {
	goals := &fakeGoalService{}
	store := NewGoalRevisionStore(goals)

	for _, origin := range []string{contracts.AgentRunOriginGoal, contracts.AgentRunOriginAutomation, contracts.AgentRunOriginSubagent} {
		if _, err := store.CreateGoal(goalTurnContext("sess-1", origin), "objective", 0); !errors.Is(err, contracts.ErrGoalDirectHumanRequired) {
			t.Fatalf("CreateGoal from %q = %v, want ErrGoalDirectHumanRequired", origin, err)
		}
	}
	if goals.created != 0 {
		t.Fatalf("goal service created %d goals for non-human runs", goals.created)
	}

	snapshot, err := store.CreateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser), " ship it ", 0)
	if err != nil {
		t.Fatalf("CreateGoal from a human turn: %v", err)
	}
	if goals.createdSession != "sess-1" || goals.createdRounds != righosttools.DefaultMaxGoalRounds {
		t.Fatalf("created session/rounds = %q/%d, want sess-1/256", goals.createdSession, goals.createdRounds)
	}
	if snapshot.ID != "goal-1" || snapshot.Phase != righosttools.GoalPhaseActive ||
		snapshot.Activation != righosttools.GoalActivationArmed || snapshot.Objective != "ship it" {
		t.Fatalf("snapshot = %+v, want the created goal", snapshot)
	}

	// An explicit max_goal_rounds is passed through; a stale context without an
	// origin is the direct host/command path and stays allowed.
	if _, err := store.CreateGoal(goalTurnContext("sess-1", ""), "objective", 12); err != nil {
		t.Fatalf("CreateGoal from the host path: %v", err)
	}
	if goals.createdRounds != 12 {
		t.Fatalf("created rounds = %d, want the explicit 12", goals.createdRounds)
	}
}

func TestGoalRevisionCreateRejectsMissingSessionAndBadArguments(t *testing.T) {
	store := NewGoalRevisionStore(&fakeGoalService{})
	if _, err := store.CreateGoal(context.Background(), "objective", 0); err == nil || !strings.Contains(err.Error(), "session-scoped") {
		t.Fatalf("CreateGoal without a session = %v, want a session-scoped error", err)
	}
	if _, err := store.CreateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser), "  ", 0); err == nil {
		t.Fatal("CreateGoal with a blank objective succeeded")
	}
	if _, err := store.CreateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser), "objective", -1); err == nil {
		t.Fatal("CreateGoal with a negative round cap succeeded")
	}
}

func TestGoalRevisionUpdateHumanOnlyActions(t *testing.T) {
	goals := &fakeGoalService{has: true, goal: contracts.Goal{
		SessionID: "sess-1", ID: "goal-1", Revision: 2, Phase: contracts.GoalPhasePaused, MaxGoalRounds: 256,
	}}
	store := NewGoalRevisionStore(goals)
	ref := righosttools.GoalRef{ID: "goal-1", Revision: 2}

	for _, action := range []rig.GoalUpdateAction{rig.GoalEdit, rig.GoalPause, rig.GoalResume} {
		update := righosttools.GoalUpdate{Action: action, Objective: "new"}
		if _, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginGoal), ref, update); !errors.Is(err, contracts.ErrGoalDirectHumanRequired) {
			t.Fatalf("update_goal %s from a goal round = %v, want ErrGoalDirectHumanRequired", action, err)
		}
	}

	// A human turn may edit; the ref/action reach the service unchanged.
	if _, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser), ref,
		righosttools.GoalUpdate{Action: rig.GoalEdit, Objective: "new objective", MaxGoalRounds: 9}); err != nil {
		t.Fatalf("edit from a human turn: %v", err)
	}
	if goals.lastRef != (contracts.GoalRef{ID: "goal-1", Revision: 2}) {
		t.Fatalf("service ref = %+v, want the exact CAS ref", goals.lastRef)
	}
	if goals.lastUpdate.Action != contracts.GoalActionEdit || goals.lastUpdate.Objective != "new objective" || goals.lastUpdate.MaxGoalRounds != 9 {
		t.Fatalf("service update = %+v, want the mapped edit", goals.lastUpdate)
	}
}

func TestGoalRevisionUpdatePassesStaleRefThrough(t *testing.T) {
	goals := &fakeGoalService{has: true, goal: contracts.Goal{
		SessionID: "sess-1", ID: "goal-1", Revision: 4, Phase: contracts.GoalPhaseActive,
	}, updateErr: contracts.ErrGoalStaleRef}
	store := NewGoalRevisionStore(goals)

	if _, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser),
		righosttools.GoalRef{ID: "goal-1", Revision: 3},
		righosttools.GoalUpdate{Action: rig.GoalPause}); !errors.Is(err, contracts.ErrGoalStaleRef) {
		t.Fatalf("stale ref = %v, want ErrGoalStaleRef passed through", err)
	}
}

func TestGoalRevisionBlockedThresholdForGoalRounds(t *testing.T) {
	goals := &fakeGoalService{has: true, goal: contracts.Goal{
		SessionID: "sess-1", ID: "goal-1", Revision: 5, Phase: contracts.GoalPhaseActive,
		RoundsStarted: 2, MaxGoalRounds: 10,
	}}
	store := NewGoalRevisionStore(goals)
	ref := righosttools.GoalRef{ID: "goal-1", Revision: 5}
	blocked := righosttools.GoalUpdate{Action: rig.GoalBlocked, BlockedReason: "needs credentials"}

	if _, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginGoal), ref, blocked); err == nil {
		t.Fatal("blocked from goal round 2 was accepted")
	} else if !strings.Contains(err.Error(), "at least 3") {
		t.Fatalf("blocked from goal round 2 = %v, want the 3-round threshold message", err)
	}
	if goals.updates != 0 {
		t.Fatalf("goal service received %d updates below the threshold", goals.updates)
	}

	// With three started rounds the goal rund may report blocked.
	goals.mu.Lock()
	goals.goal.RoundsStarted = 3
	goals.mu.Unlock()
	if _, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginGoal), ref, blocked); err != nil {
		t.Fatalf("blocked from goal round 3: %v", err)
	}
	if goals.lastUpdate.Action != contracts.GoalActionBlocked || goals.lastUpdate.BlockedReason != "needs credentials" {
		t.Fatalf("service update = %+v, want the blocked action", goals.lastUpdate)
	}

	// A blocked action without a reason is rejected before the service.
	goals.mu.Lock()
	goals.goal.RoundsStarted = 5
	goals.mu.Unlock()
	if _, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginGoal), ref,
		righosttools.GoalUpdate{Action: rig.GoalBlocked}); err == nil {
		t.Fatal("blocked without a reason was accepted")
	}
}

func TestGoalRevisionCompleteFromAGoalRound(t *testing.T) {
	goals := &fakeGoalService{has: true, goal: contracts.Goal{
		SessionID: "sess-1", ID: "goal-1", Revision: 1, Phase: contracts.GoalPhaseActive, MaxGoalRounds: 3,
	}}
	store := NewGoalRevisionStore(goals)

	snapshot, err := store.UpdateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginGoal),
		righosttools.GoalRef{ID: "goal-1", Revision: 1},
		righosttools.GoalUpdate{Action: rig.GoalComplete})
	if err != nil {
		t.Fatalf("complete from a goal round: %v", err)
	}
	if snapshot.Phase != righosttools.GoalPhaseComplete {
		t.Fatalf("phase = %q, want complete", snapshot.Phase)
	}
	if goals.lastUpdate.Action != contracts.GoalActionComplete {
		t.Fatalf("service update = %+v, want complete", goals.lastUpdate)
	}
}

func TestGoalRevisionWithoutABackend(t *testing.T) {
	store := NewLazyGoalRevisionStore(func() contracts.GoalService { return nil })
	if _, ok := store.CurrentGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser)); ok {
		t.Fatal("CurrentGoal without a backend reported a goal")
	}
	if _, err := store.CreateGoal(goalTurnContext("sess-1", contracts.AgentRunOriginUser), "objective", 0); err == nil ||
		!strings.Contains(err.Error(), "no goal backend") {
		t.Fatalf("CreateGoal without a backend = %v, want the unavailable message", err)
	}
}
