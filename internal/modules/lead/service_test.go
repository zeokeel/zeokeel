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
