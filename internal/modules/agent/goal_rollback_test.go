package agent

// BE-10 (2026-11 audit): the continuation driver admits a durable round and
// then starts the run; when that send fails the admitted round must be
// refunded — otherwise the goal burns a round on a turn that never ran.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestGoalContinuationSendFailureRollsBackRound drives the driver with a
// session whose model list is explicitly empty: AdmitRound succeeds, the send
// fails with ErrNoModelConfigured, and the driver refunds the round.
func TestGoalContinuationSendFailureRollsBackRound(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: "[]", // no model: Send must fail
	})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	goals := newFakeGoalService("ship the feature", 5)
	svc.goalService = goals
	id, err := svc.NewSession("goal-rollback")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	goals.mu.Lock()
	goals.goal.SessionID = id
	goals.mu.Unlock()

	svc.continueGoalAfterRun(id, contracts.AgentRunOriginUser, nil, context.Background())

	if admits := goals.admitCount(); admits != 1 {
		t.Fatalf("admits = %d, want 1 (the driver admitted before the send)", admits)
	}
	rollbacks, round := goals.rollbackCount()
	if rollbacks != 1 || round != 1 {
		t.Fatalf("rollbacks = %d (round %d), want one refund of round 1 (BE-10)", rollbacks, round)
	}
	goal, _ := goals.snapshot()
	if goal.RoundsStarted != 0 {
		t.Fatalf("rounds after failed send = %d, want 0 (refunded)", goal.RoundsStarted)
	}
	// The session never started a run.
	session := mustSession(t, svc, id)
	if session.Running || len(session.Messages) != 0 {
		t.Fatalf("session = running:%v messages:%d, want no run started", session.Running, len(session.Messages))
	}
}

// Control: a successful continuation does NOT roll back (cap 1: the driver
// admits one round, runs it, and the next admission hits the round limit).
func TestGoalContinuationSuccessKeepsRound(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	goals := newFakeGoalService("ship the feature", 1)
	svc.goalService = goals
	id, err := svc.NewSession("goal-success")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	goals.mu.Lock()
	goals.goal.SessionID = id
	goals.mu.Unlock()

	svc.continueGoalAfterRun(id, contracts.AgentRunOriginUser, nil, context.Background())
	waitIdle(t, svc, id)

	if rollbacks, _ := goals.rollbackCount(); rollbacks != 0 {
		t.Fatalf("rollbacks = %d, want 0 on a successful round", rollbacks)
	}
	goal, _ := goals.snapshot()
	if goal.RoundsStarted != 1 {
		t.Fatalf("rounds = %d, want 1 (the successful round stays consumed)", goal.RoundsStarted)
	}
}
