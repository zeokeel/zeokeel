package hosttools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestAskerFunc(t *testing.T) {
	// A nil AskerFunc is not an interactive channel.
	if _, err := (AskerFunc)(nil).Ask(context.Background(), "q", nil); !errors.Is(err, ErrNoInteractive) {
		t.Fatalf("nil AskerFunc error = %v, want ErrNoInteractive", err)
	}
	asker := AskerFunc(func(ctx context.Context, question string, options []string) (string, error) {
		return question + ":" + strings.Join(options, ","), nil
	})
	answer, err := asker.Ask(context.Background(), "db", []string{"sqlite", "pg"})
	if err != nil || answer != "db:sqlite,pg" {
		t.Fatalf("AskerFunc answer = %q, %v", answer, err)
	}
}

func TestMemoryAsker(t *testing.T) {
	asker := NewMemoryAsker("sqlite", "postgres")
	answer, err := asker.Ask(context.Background(), "Which database?", []string{"sqlite", "postgres"})
	if err != nil || answer != "sqlite" {
		t.Fatalf("Ask = %q, %v; want sqlite", answer, err)
	}
	answer, err = asker.Ask(context.Background(), "And the replica?", nil)
	if err != nil || answer != "postgres" {
		t.Fatalf("second Ask = %q, %v; want postgres", answer, err)
	}
	// The queue is empty: no user is available in memory.
	if _, err := asker.Ask(context.Background(), "Anything else?", nil); !errors.Is(err, ErrNoInteractive) {
		t.Fatalf("empty queue error = %v, want ErrNoInteractive", err)
	}
	if _, err := asker.Ask(context.Background(), "   ", nil); err == nil {
		t.Fatal("Ask(blank question) succeeded")
	}

	asked := asker.Asked()
	if len(asked) != 3 {
		t.Fatalf("asked = %+v, want three recorded questions", asked)
	}
	if asked[0].Question != "Which database?" || asked[0].Answer != "sqlite" || len(asked[0].Options) != 2 {
		t.Fatalf("asked[0] = %+v", asked[0])
	}
	if asked[2].Answer != "" {
		t.Fatalf("unanswered question recorded answer %q", asked[2].Answer)
	}

	asker.Enqueue("later")
	if answer, err := asker.Ask(context.Background(), "q", nil); err != nil || answer != "later" {
		t.Fatalf("Ask after Enqueue = %q, %v", answer, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := asker.Ask(ctx, "q", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask(cancelled ctx) = %v, want context.Canceled", err)
	}
}

func TestMemoryAskerConcurrent(t *testing.T) {
	const workers, perWorker = 8, 10
	asker := NewMemoryAsker()
	for index := 0; index < workers*perWorker; index++ {
		asker.Enqueue("answer")
	}
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for count := 0; count < perWorker; count++ {
				if _, err := asker.Ask(context.Background(), "q", nil); err != nil {
					t.Errorf("Ask: %v", err)
					return
				}
			}
		}()
	}
	group.Wait()
	if got := len(asker.Asked()); got != workers*perWorker {
		t.Fatalf("asked questions = %d, want %d", got, workers*perWorker)
	}
}

func TestMemoryPresenter(t *testing.T) {
	presenter := NewMemoryPresenter()
	id, err := presenter.Present(context.Background(), "Test report", "# all green")
	if err != nil || id != "artifact-1" {
		t.Fatalf("Present = %q, %v; want artifact-1", id, err)
	}
	id, err = presenter.Present(context.Background(), "Plan", "steps")
	if err != nil || id != "artifact-2" {
		t.Fatalf("second Present = %q, %v; want artifact-2", id, err)
	}
	if _, err := presenter.Present(context.Background(), "  ", "body"); err == nil {
		t.Fatal("Present(blank title) succeeded")
	}
	artifacts := presenter.Artifacts()
	if len(artifacts) != 2 || artifacts[0].Title != "Test report" || artifacts[1].Body != "steps" {
		t.Fatalf("artifacts = %+v", artifacts)
	}
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := presenter.Present(context.Background(), "concurrent", ""); err != nil {
				t.Errorf("Present: %v", err)
			}
		}()
	}
	group.Wait()
	if got := len(presenter.Artifacts()); got != 22 {
		t.Fatalf("artifacts after concurrent presents = %d, want 22", got)
	}
	var presenterFunc PresenterFunc
	if _, err := presenterFunc.Present(context.Background(), "t", "b"); err == nil {
		t.Fatal("nil PresenterFunc succeeded")
	}
}

func TestMemoryPlanLifecycle(t *testing.T) {
	plan := NewMemoryPlan()
	if got := plan.State(); got != PlanIdle {
		t.Fatalf("initial state = %q, want idle", got)
	}
	if _, err := plan.Exit("approve"); err == nil {
		t.Fatal("approve from idle succeeded")
	}
	if got, err := plan.Exit("status"); err != nil || got != PlanIdle {
		t.Fatalf("status from idle = %q, %v", got, err)
	}
	if _, err := plan.Review("   "); err == nil {
		t.Fatal("Review(blank) succeeded")
	}
	verdict, err := plan.Review("# Plan\n1. do it")
	if err != nil || verdict != PlanReviewing {
		t.Fatalf("Review = %q, %v; want reviewing", verdict, err)
	}
	if got := plan.Content(); !strings.Contains(got, "do it") {
		t.Fatalf("Content = %q", got)
	}
	if _, err := plan.Exit("bogus"); err == nil {
		t.Fatal("Exit(bogus) succeeded")
	}
	state, err := plan.Exit("approve")
	if err != nil || state != PlanApproved {
		t.Fatalf("approve = %q, %v; want approved", state, err)
	}
	if _, err := plan.Exit("cancel"); err == nil {
		t.Fatal("cancel after approval succeeded")
	}

	cancelled := NewMemoryPlan()
	if _, err := cancelled.Review("plan"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if state, err := cancelled.Exit("cancel"); err != nil || state != PlanCancelled {
		t.Fatalf("cancel = %q, %v; want cancelled", state, err)
	}
}

func TestMemoryPlanConcurrent(t *testing.T) {
	plan := NewMemoryPlan()
	var group sync.WaitGroup
	for index := 0; index < 10; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = plan.Review("plan")
			_ = plan.State()
			_, _ = plan.Exit("status")
		}()
	}
	group.Wait()
}

func TestMemoryGoals(t *testing.T) {
	goals := NewMemoryGoals()
	if _, _, _, ok := goals.Get(); ok {
		t.Fatal("Get on empty store reported a goal")
	}
	if goals.Clear() {
		t.Fatal("Clear on empty store reported a goal")
	}
	if _, err := goals.Set("  "); err == nil {
		t.Fatal("Set(blank) succeeded")
	}
	id, err := goals.Set("Ship v0.4")
	if err != nil || id != "goal-1" {
		t.Fatalf("Set = %q, %v; want goal-1", id, err)
	}
	gotID, text, status, ok := goals.Get()
	if !ok || gotID != id || text != "Ship v0.4" || status != GoalActive {
		t.Fatalf("Get = %q, %q, %q, %v", gotID, text, status, ok)
	}
	// Updating keeps the goal id; clearing removes it.
	if again, err := goals.Set("Ship v0.4 by Friday"); err != nil || again != id {
		t.Fatalf("second Set = %q, %v; want the same id", again, err)
	}
	if !goals.Clear() {
		t.Fatal("Clear did not report the goal")
	}
	if _, _, _, ok := goals.Get(); ok {
		t.Fatal("Get after Clear reported a goal")
	}
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, _ = goals.Set("goal")
			_, _, _, _ = goals.Get()
		}(index)
	}
	group.Wait()
}

func TestMemoryTeam(t *testing.T) {
	team := NewMemoryTeam()
	if members := team.List(); len(members) != 0 {
		t.Fatalf("List on empty team = %+v", members)
	}
	if _, err := team.Spawn("  "); err == nil {
		t.Fatal("Spawn(blank) succeeded")
	}
	id, err := team.Spawn("research")
	if err != nil || id != "member-1" {
		t.Fatalf("Spawn = %q, %v; want member-1", id, err)
	}
	if second, err := team.Spawn("reviewer"); err != nil || second != "member-2" {
		t.Fatalf("second Spawn = %q, %v", second, err)
	}
	members := team.List()
	if len(members) != 2 || members[0].Name != "research" || members[0].Status != TeamActive {
		t.Fatalf("List = %+v", members)
	}
	if err := team.Interrupt(id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if got := team.List()[0].Status; got != TeamInterrupted {
		t.Fatalf("status after Interrupt = %q, want interrupted", got)
	}
	if err := team.Close("member-2"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := team.List()[1].Status; got != TeamClosed {
		t.Fatalf("status after Close = %q, want closed", got)
	}
	if err := team.Close("member-99"); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("Close(unknown) = %v, want ErrUnknownMember", err)
	}
	if err := team.Interrupt("  "); err == nil {
		t.Fatal("Interrupt(blank id) succeeded")
	}
}

func TestMemoryTeamConcurrent(t *testing.T) {
	team := NewMemoryTeam()
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if id, err := team.Spawn("worker"); err == nil {
				if err := team.Interrupt(id); err != nil {
					t.Errorf("Interrupt: %v", err)
				}
			}
			_ = team.List()
		}()
	}
	group.Wait()
	if got := len(team.List()); got != 20 {
		t.Fatalf("members = %d, want 20", got)
	}
}

func TestNewMemoryHost(t *testing.T) {
	host := NewMemoryHost()
	if host.Asker == nil || host.Presenter == nil || host.Plan == nil || host.Goals == nil || host.Team == nil {
		t.Fatalf("NewMemoryHost has nil capabilities: %+v", host)
	}
}
