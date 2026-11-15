package uiroot

// Tests for the host close guard's read model: running chat/automation runs,
// background jobs and active goals count as work in flight; finished, paused and
// abandoned work does not.

import (
	"context"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// runningChat is shortcutChat with a scripted session list (the running flag is
// what Root.RunningWork reads).
type runningChat struct {
	shortcutChat
	sessions []contracts.ChatSessionInfo
}

func (c *runningChat) Sessions() []contracts.ChatSessionInfo { return c.sessions }

// fakeJobs is a contracts.JobService with a scripted list.
type fakeJobs struct {
	jobs []contracts.BackgroundJob
}

func (f fakeJobs) Jobs() []contracts.BackgroundJob  { return f.jobs }
func (f fakeJobs) KillJob(string) error             { return nil }
func (f fakeJobs) JobOutput(string) (string, error) { return "", nil }

// kernelWith builds a disposable kernel root, provides via fn and returns it.
func kernelWith(t *testing.T, fn func(cordis.Context)) cordis.Context {
	t.Helper()
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	fn(kernel)
	return kernel
}

func TestRunningWorkEmpty(t *testing.T) {
	root := New(Config{AppName: "ZeoKeelTest"})
	if root.RunningWork() {
		t.Fatal("RunningWork() = true on an idle root")
	}
	var nilRoot *Root
	if nilRoot.RunningWork() {
		t.Fatal("RunningWork() = true on a nil root")
	}
}

func TestRunningWorkChatSessions(t *testing.T) {
	idle := &runningChat{sessions: []contracts.ChatSessionInfo{{ID: "s1", Running: false}}}
	root := New(Config{AppName: "ZeoKeelTest", Chat: idle})
	if root.RunningWork() {
		t.Fatal("RunningWork() = true with only finished sessions")
	}

	busy := &runningChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Running: false},
		{ID: "s2", Running: true},
	}}
	root = New(Config{AppName: "ZeoKeelTest", Chat: busy})
	if !root.RunningWork() {
		t.Fatal("RunningWork() = false with a running session")
	}
}

func TestRunningWorkChatFromKernel(t *testing.T) {
	busy := &runningChat{sessions: []contracts.ChatSessionInfo{{ID: "s2", Running: true}}}
	kernel := kernelWith(t, func(kernel cordis.Context) {
		if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(busy)); err != nil {
			t.Fatalf("provide chat: %v", err)
		}
	})
	root := New(Config{AppName: "ZeoKeelTest"})
	root.SetKernelContext(kernel)
	if !root.RunningWork() {
		t.Fatal("RunningWork() = false with a running kernel chat session")
	}
}

func TestRunningWorkBackgroundJobs(t *testing.T) {
	kernel := kernelWith(t, func(kernel cordis.Context) {
		if _, err := kernel.Provide(contracts.KeyJobs, contracts.JobService(fakeJobs{
			jobs: []contracts.BackgroundJob{{ID: "job-1", Running: true}},
		})); err != nil {
			t.Fatalf("provide jobs: %v", err)
		}
	})
	root := New(Config{AppName: "ZeoKeelTest"})
	root.SetKernelContext(kernel)
	if !root.RunningWork() {
		t.Fatal("RunningWork() = false with a running background job")
	}
}

// fakeGoalService is a contracts.GoalService with scripted per-session goals.
type fakeGoalService struct {
	goals map[string]contracts.Goal
}

func (f fakeGoalService) Goal(sessionID string) (contracts.Goal, bool) {
	goal, ok := f.goals[sessionID]
	return goal, ok
}
func (f fakeGoalService) CreateGoal(context.Context, string, string, int) (contracts.Goal, error) {
	return contracts.Goal{}, nil
}
func (f fakeGoalService) UpdateGoal(context.Context, string, contracts.GoalRef, contracts.GoalUpdate) (contracts.Goal, error) {
	return contracts.Goal{}, nil
}
func (f fakeGoalService) AdmitRound(context.Context, string) (contracts.Goal, error) {
	return contracts.Goal{}, nil
}
func (f fakeGoalService) ClearGoal(context.Context, string, contracts.GoalRef) (bool, error) {
	return false, nil
}
func (f fakeGoalService) Disarm(string) (contracts.Goal, bool) { return contracts.Goal{}, false }
func (f fakeGoalService) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

func TestRunningWorkGoalV2(t *testing.T) {
	busy := &runningChat{sessions: []contracts.ChatSessionInfo{{ID: "s1"}}}
	kernel := kernelWith(t, func(kernel cordis.Context) {
		if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(busy)); err != nil {
			t.Fatalf("provide chat: %v", err)
		}
		if _, err := kernel.Provide(contracts.KeyGoal, contracts.GoalService(fakeGoalService{
			goals: map[string]contracts.Goal{
				"s1": {SessionID: "s1", ID: "goal-1", Phase: contracts.GoalPhaseActive},
			},
		})); err != nil {
			t.Fatalf("provide goal: %v", err)
		}
	})
	root := New(Config{AppName: "ZeoKeelTest"})
	root.SetKernelContext(kernel)
	if !root.RunningWork() {
		t.Fatal("RunningWork() = false with an active per-session goal")
	}

	// A paused/blocked/complete goal is not work in flight.
	for _, phase := range []contracts.GoalPhase{
		contracts.GoalPhasePaused, contracts.GoalPhaseBlocked, contracts.GoalPhaseComplete,
	} {
		kernel := kernelWith(t, func(kernel cordis.Context) {
			if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(busy)); err != nil {
				t.Fatalf("provide chat: %v", err)
			}
			if _, err := kernel.Provide(contracts.KeyGoal, contracts.GoalService(fakeGoalService{
				goals: map[string]contracts.Goal{
					"s1": {SessionID: "s1", ID: "goal-1", Phase: phase},
				},
			})); err != nil {
				t.Fatalf("provide goal: %v", err)
			}
		})
		root := New(Config{AppName: "ZeoKeelTest"})
		root.SetKernelContext(kernel)
		if root.RunningWork() {
			t.Fatalf("RunningWork() = true with a %s goal", phase)
		}
	}
}
