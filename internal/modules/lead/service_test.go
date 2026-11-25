package lead_test

// TDD for the triage pass. The rules that matter: a pass is bounded, one
// failing item never stops the others, an item already turned into a task is
// never turned into a second one, and a planner that declines leaves the item
// exactly where it was.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/lead"
)

type fakeTriage struct {
	mu      sync.Mutex
	applied []contracts.TriageDecision
	ids     []string
	failOn  map[string]error
}

func newFakeTriage() *fakeTriage {
	return &fakeTriage{failOn: map[string]error{}}
}

func (f *fakeTriage) Apply(ctx context.Context, inboxID string, decision contracts.TriageDecision) (contracts.TriageResult, error) {
	if err, ok := f.failOn[inboxID]; ok {
		return contracts.TriageResult{}, err
	}
	f.mu.Lock()
	f.ids = append(f.ids, inboxID)
	f.applied = append(f.applied, decision)
	f.mu.Unlock()
	item := contracts.InboxItem{ID: inboxID, Title: "t", Status: contracts.InboxStatusTriaged}
	if decision.Action == contracts.InboxActionTask {
		item.Status = contracts.InboxStatusTask
		item.TaskIDs = []string{"task-" + inboxID}
	}
	return contracts.TriageResult{Inbox: item, TaskIDs: item.TaskIDs, Created: true}, nil
}

func newServices(t *testing.T, rules []lead.Rule) (*lead.Service, contracts.InboxService, *fakeTriage) {
	t.Helper()
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 1000})
	triage := newFakeTriage()
	svc := lead.NewService(inboxSvc, triage, lead.RulePlanner{Rules: rules}, lead.Config{MaxPerRun: 10, Now: time.Now})
	return svc, inboxSvc, triage
}

func arrive(t *testing.T, inboxSvc contracts.InboxService, source, title string) contracts.InboxItem {
	t.Helper()
	item, _, err := inboxSvc.Add(context.Background(), contracts.InboxItem{
		Source: source, Channel: "c", Sender: "owner", DedupKey: title, Title: title,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return item
}

func TestRunOnceDispatchesMatchingItems(t *testing.T) {
	svc, inboxSvc, triage := newServices(t, []lead.Rule{{
		Source: "webhook", Assignee: "research-bot", SubjectPrefix: "事件：",
	}})
	arrive(t, inboxSvc, "webhook", "崩溃告警")
	arrive(t, inboxSvc, "wechat", "私人消息")

	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Scanned != 2 || report.Dispatched != 1 || report.Skipped != 1 {
		t.Fatalf("report = %+v, want scanned 2 / dispatched 1 / skipped 1", report)
	}
	if len(triage.applied) != 1 || triage.applied[0].Task == nil {
		t.Fatalf("applied = %+v, want one task decision", triage.applied)
	}
	if triage.applied[0].Task.Assignee != "research-bot" {
		t.Fatalf("assignee = %q", triage.applied[0].Task.Assignee)
	}
	if triage.applied[0].Task.Subject != "事件：崩溃告警" {
		t.Fatalf("subject = %q, want the prefix applied", triage.applied[0].Task.Subject)
	}
}

func TestRunOnceHonoursIgnoreRules(t *testing.T) {
	svc, inboxSvc, triage := newServices(t, []lead.Rule{{Source: "email", Ignore: true}})
	arrive(t, inboxSvc, "email", "newsletter")

	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Ignored != 1 || report.Dispatched != 0 {
		t.Fatalf("report = %+v, want ignored 1", report)
	}
	if triage.applied[0].Action != contracts.InboxActionIgnored {
		t.Fatalf("action = %q, want ignored", triage.applied[0].Action)
	}
}

func TestRunOnceIsBounded(t *testing.T) {
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 1000})
	triage := newFakeTriage()
	svc := lead.NewService(inboxSvc, triage, lead.RulePlanner{Rules: []lead.Rule{{Source: "webhook", Assignee: "bot"}}},
		lead.Config{MaxPerRun: 2, Now: time.Now})
	for i := 0; i < 5; i++ {
		arrive(t, inboxSvc, "webhook", "event-"+string(rune('a'+i)))
	}
	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Scanned != 2 || report.Dispatched != 2 {
		t.Fatalf("report = %+v, want two items only", report)
	}
}

func TestOneFailingItemDoesNotStopThePass(t *testing.T) {
	svc, inboxSvc, triage := newServices(t, []lead.Rule{{Source: "webhook", Assignee: "bot"}})
	first := arrive(t, inboxSvc, "webhook", "first")
	arrive(t, inboxSvc, "webhook", "second")
	triage.failOn[first.ID] = errors.New("board down")

	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Failed != 1 || report.Dispatched != 1 {
		t.Fatalf("report = %+v, want one failure and one dispatch", report)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("errors = %v, want one line naming the item", report.Errors)
	}
}

func TestSecondPassDoesNotReprocessHandledItems(t *testing.T) {
	svc, inboxSvc, _ := newServices(t, []lead.Rule{{Source: "webhook", Assignee: "bot"}})
	item := arrive(t, inboxSvc, "webhook", "once")
	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// The fake triage does not move the inbox item, so move it the way the real
	// triage service would, then check the second pass skips it.
	if _, err := inboxSvc.Triage(context.Background(), []string{item.ID}, contracts.InboxActionTask, []string{"task-1"}); err != nil {
		t.Fatalf("Triage: %v", err)
	}
	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if report.Scanned != 0 {
		t.Fatalf("second pass scanned %d items, want 0 (only new items are triaged)", report.Scanned)
	}
}

// customPlanner proves the seam that a model-backed planner plugs into: the
// service must use the planner it was constructed with, not rebuild rules.
type customPlanner struct {
	calls int
	give  contracts.TriageDecision
}

func (p *customPlanner) Plan(_ context.Context, _ contracts.InboxItem) (contracts.TriageDecision, bool, error) {
	p.calls++
	return p.give, true, nil
}

func TestRunOnceUsesTheInjectedPlanner(t *testing.T) {
	inboxSvc := inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 1000})
	triage := newFakeTriage()
	planner := &customPlanner{give: contracts.TriageDecision{
		Action: contracts.InboxActionTask,
		Task:   &contracts.NewTask{Subject: "from the model", Assignee: "bot-9"},
	}}
	svc := lead.NewService(inboxSvc, triage, planner, lead.Config{MaxPerRun: 5, Now: time.Now})
	arrive(t, inboxSvc, "webhook", "anything at all")

	report, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if planner.calls != 1 {
		t.Fatalf("planner calls = %d, want 1 (a custom planner must not be replaced by the rule planner)", planner.calls)
	}
	if report.Dispatched != 1 || len(triage.applied) != 1 || triage.applied[0].Task.Subject != "from the model" {
		t.Fatalf("report = %+v applied = %+v", report, triage.applied)
	}
}

// The module builds a model planner from the chat settings when they are
// complete, and keeps the rule planner when they are not: a half-configured
// endpoint must never silently start deciding on its own.
type stubSettings map[string]string

func (s stubSettings) Get(key, fallback string) string {
	if value, ok := s[key]; ok {
		return value
	}
	return fallback
}

func TestPlannerFromSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings stubSettings
		wantKind string
	}{
		{"complete settings build a model planner", stubSettings{
			contracts.SettingsChatBaseURL: "https://api.example.com/v1",
			contracts.SettingsChatAPIKey:  "sk-test",
			contracts.SettingsChatModel:   "triage-model",
		}, "model"},
		{"missing model keeps the rule planner", stubSettings{
			contracts.SettingsChatBaseURL: "https://api.example.com/v1",
			contracts.SettingsChatAPIKey:  "sk-test",
		}, "rules"},
		{"missing base url keeps the rule planner", stubSettings{
			contracts.SettingsChatModel: "triage-model",
		}, "rules"},
		{"empty settings keep the rule planner", stubSettings{}, "rules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planner, err := lead.PlannerFromSettings(tc.settings, nil)
			if err != nil {
				t.Fatalf("PlannerFromSettings: %v", err)
			}
			switch tc.wantKind {
			case "model":
				if _, ok := planner.(lead.ModelPlanner); !ok {
					t.Fatalf("planner = %T, want a ModelPlanner", planner)
				}
			default:
				if _, ok := planner.(lead.RulePlanner); !ok {
					t.Fatalf("planner = %T, want the rule planner", planner)
				}
			}
		})
	}
}
