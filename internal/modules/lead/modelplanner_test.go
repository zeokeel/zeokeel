package lead_test

import (
	"context"
	"errors"
	"strings"
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

// TestModelPlannerDeclaresItsTeammateEnvelope pins the seam the triage unit uses
// to validate an assignee (2026-11-26 audit, 中高): the model may only assign to a
// name the rules name, and the planner — the only party that knows them — declares
// that set on the decision. Without it triage cannot tell a rule-derived teammate
// from an invented one.
func TestModelPlannerDeclaresItsTeammateEnvelope(t *testing.T) {
	rules := []contracts.LeadRule{
		{Source: "webhook", Assignee: "bot-1"},
		{Source: "email", Assignee: "fin-ops"},
		{Source: "webhook", Assignee: "bot-1"},
	}
	planner := lead.ModelPlanner{
		Model: &stubModel{answer: `{"action":"task","subject":"x","assignee":"fin-ops"}`},
		Rules: func(context.Context) ([]contracts.LeadRule, error) { return rules, nil },
	}
	decision, ok, err := planner.Plan(context.Background(), contracts.InboxItem{ID: "i1", Source: "email"})
	if err != nil || !ok {
		t.Fatalf("Plan = (ok=%v, err=%v)", ok, err)
	}
	if got := strings.Join(decision.Task.KnownAssignees, ","); got != "bot-1,fin-ops" {
		t.Fatalf("KnownAssignees = %q, want the rules' assignees (deduplicated, in order)", got)
	}
	// The prompt carries the same rules: the envelope and the instruction cannot
	// describe different worlds.
	if !strings.Contains(planner.Model.(*stubModel).prompt, `assignee="bot-1"`) {
		t.Fatalf("prompt does not list the rules: %q", planner.Model.(*stubModel).prompt)
	}
}

// TestServiceHandsTheLiveRulesToTheModelPlanner: a model-backed planner built by
// PlannerFromSettings used to get Rules=nil, so it prompted with no rules at all
// while still inventing assignees. The service now supplies its own live rule set.
func TestServiceHandsTheLiveRulesToTheModelPlanner(t *testing.T) {
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 100})
	triage := newFakeTriage()
	model := &stubModel{answer: `{"action":"task","subject":"来自模型","assignee":"bot-3"}`}
	svc := lead.NewService(inboxSvc, triage,
		lead.ModelPlanner{Model: model}, lead.Config{MaxPerRun: 5, Now: time.Now})
	if err := svc.SetRules(context.Background(), []lead.Rule{{Source: "webhook", Assignee: "bot-3"}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	arrive(t, inboxSvc, "webhook", "告警")

	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !strings.Contains(model.prompt, `assignee="bot-3"`) {
		t.Fatalf("the model planner was not given the live rules: %q", model.prompt)
	}
	if len(triage.applied) != 1 || strings.Join(triage.applied[0].Task.KnownAssignees, ",") != "bot-3" {
		t.Fatalf("applied = %+v, want the decision to carry the rule envelope", triage.applied)
	}
}
