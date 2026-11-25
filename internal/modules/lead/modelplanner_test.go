package lead_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/lead"
)

type stubModel struct {
	answer string
	err    error
	prompt string
}

func (s *stubModel) Decide(_ context.Context, prompt string) (string, error) {
	s.prompt = prompt
	if s.err != nil {
		return "", s.err
	}
	return s.answer, nil
}

func TestModelPlannerMapsTaskDecision(t *testing.T) {
	model := &stubModel{answer: `{"action":"task","subject":"修崩溃","detail":"#12","assignee":"bot-1","note":"从告警来"}`}
	planner := lead.ModelPlanner{Model: model}
	decision, ok, err := planner.Plan(context.Background(), contracts.InboxItem{ID: "i1", Source: "webhook", Title: "崩溃"})
	if err != nil || !ok {
		t.Fatalf("Plan = (%+v, %v, %v)", decision, ok, err)
	}
	if decision.Action != contracts.InboxActionTask || decision.Task.Subject != "修崩溃" || decision.Task.Assignee != "bot-1" {
		t.Fatalf("decision = %+v", decision)
	}
	if model.prompt == "" {
		t.Fatal("the model was not given a prompt")
	}
}

func TestModelPlannerMapsSkipToNotHandled(t *testing.T) {
	planner := lead.ModelPlanner{Model: &stubModel{answer: `{"action":"skip"}`}}
	_, ok, err := planner.Plan(context.Background(), contracts.InboxItem{ID: "i1"})
	if err != nil || ok {
		t.Fatalf("Plan = (ok=%v, err=%v), want a declined item", ok, err)
	}
}

func TestModelPlannerWithoutModelIsTyped(t *testing.T) {
	_, _, err := lead.ModelPlanner{}.Plan(context.Background(), contracts.InboxItem{ID: "i1"})
	if !errors.Is(err, contracts.ErrLeadPlanModel) {
		t.Fatalf("err = %v, want ErrLeadPlanModel", err)
	}
}

func TestModelPlannerDrivesThePass(t *testing.T) {
	// End to end inside the pass: a model answer becomes a task on the board.
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 100})
	triage := newFakeTriage()
	planner := lead.ModelPlanner{Model: &stubModel{answer: `{"action":"task","subject":"来自模型","assignee":"bot-3"}`}}
	svc := lead.NewService(inboxSvc, triage, planner, lead.Config{MaxPerRun: 5, Now: time.Now})
	arrive(t, inboxSvc, "webhook", "随便")

	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Dispatched != 1 || len(triage.applied) != 1 || triage.applied[0].Task.Assignee != "bot-3" {
		t.Fatalf("report = %+v applied = %+v", report, triage.applied)
	}
}
