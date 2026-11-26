package replyrelay_test

// TDD for the chat-reply approval path: a message a channel adapter filed in the
// inbox must be able to answer a pending approval, and the item must then leave
// the "new" queue. A message that is not a reply must be left exactly where it
// was — the Lead still has to triage it.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/replyrelay"
)

type fakeInbox struct {
	mu    sync.Mutex
	items []contracts.InboxItem
}

func (f *fakeInbox) Add(_ context.Context, item contracts.InboxItem) (contracts.InboxItem, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
	return item, false, nil
}

func (f *fakeInbox) List(_ context.Context, filter contracts.InboxFilter) ([]contracts.InboxItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.InboxItem
	for _, item := range f.items {
		if item.Status == contracts.InboxStatusArchived {
			continue
		}
		if len(filter.Statuses) > 0 {
			match := false
			for _, status := range filter.Statuses {
				if item.Status == status {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (f *fakeInbox) Get(_ context.Context, id string) (contracts.InboxItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items {
		if item.ID == id {
			return item, nil
		}
	}
	return contracts.InboxItem{}, contracts.ErrInboxUnknownItem
}

func (f *fakeInbox) Triage(_ context.Context, ids []string, action string, _ []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := 0
	for i := range f.items {
		for _, id := range ids {
			if f.items[i].ID == id {
				f.items[i].Status = action
				changed++
			}
		}
	}
	return changed, nil
}

func (f *fakeInbox) Stats(context.Context) (contracts.InboxStats, error) {
	return contracts.InboxStats{}, nil
}

type fakeActions struct {
	mu       sync.Mutex
	replies  []string
	applied  []string
	applyErr error
}

func (f *fakeActions) IssueToken(string) (string, time.Time, error) { return "t", time.Now(), nil }

func (f *fakeActions) Apply(context.Context, string, contracts.ApprovalDecision, string) error {
	return nil
}

func (f *fakeActions) ApplyReply(_ context.Context, text string) (contracts.ReplyOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, text)
	if f.applyErr != nil {
		return contracts.ReplyOutcome{}, f.applyErr
	}
	if text != "同意" {
		return contracts.ReplyOutcome{Applied: false}, nil
	}
	f.applied = append(f.applied, "req-1")
	return contracts.ReplyOutcome{Applied: true, ApprovalID: "req-1", Decision: contracts.ApprovalAllow}, nil
}

func newService(t *testing.T) (*replyrelay.Service, *fakeInbox, *fakeActions) {
	t.Helper()
	inbox := &fakeInbox{}
	actions := &fakeActions{}
	return replyrelay.New(inbox, actions, replyrelay.Config{Enabled: boolPtr(true)}), inbox, actions
}

func boolPtr(value bool) *bool { return &value }

func seed(t *testing.T, inbox *fakeInbox, item contracts.InboxItem) {
	t.Helper()
	if item.Status == "" {
		item.Status = contracts.InboxStatusNew
	}
	inbox.items = append(inbox.items, item)
}

func TestReplyArchivesTheItemAndAnswersTheApproval(t *testing.T) {
	service, inbox, actions := newService(t)
	seed(t, inbox, contracts.InboxItem{ID: "i1", Source: "wechat", Title: "同意", Body: "同意"})

	report, err := service.Relay(context.Background())
	if err != nil {
		t.Fatalf("Relay: %v", err)
	}
	if report.Applied != 1 || len(actions.applied) != 1 {
		t.Fatalf("report = %+v applied = %v, want the approval answered", report, actions.applied)
	}
	item, _ := inbox.Get(context.Background(), "i1")
	if item.Status != contracts.InboxStatusArchived {
		t.Fatalf("item status = %q, want archived once it answered a request", item.Status)
	}
}

func TestANonReplyIsLeftForTheLead(t *testing.T) {
	service, inbox, _ := newService(t)
	seed(t, inbox, contracts.InboxItem{ID: "i1", Source: "wechat", Title: "帮我看下退款率", Body: "帮我看下退款率"})

	report, err := service.Relay(context.Background())
	if err != nil {
		t.Fatalf("Relay: %v", err)
	}
	if report.Applied != 0 {
		t.Fatalf("report = %+v, want nothing applied", report)
	}
	item, _ := inbox.Get(context.Background(), "i1")
	if item.Status != contracts.InboxStatusNew {
		t.Fatalf("item status = %q, want it still waiting for triage", item.Status)
	}
}

func TestNonChannelSourcesAreIgnored(t *testing.T) {
	// webhook/cron is machine input; dingtalk/feishu are outbound only now, so a
	// row claiming to come from them is not a fresh human utterance either; and
	// email has no authenticated sender at all (the From header is unverified),
	// so its text must never reach ApplyReply — that would let anyone who can
	// mail the mailbox answer whatever approval is first in the queue.
	for _, source := range []string{"webhook", "dingtalk", "feishu", "cron", "email"} {
		t.Run(source, func(t *testing.T) {
			service, inbox, actions := newService(t)
			seed(t, inbox, contracts.InboxItem{ID: "i1", Source: source, Title: "同意", Body: "同意"})

			if _, err := service.Relay(context.Background()); err != nil {
				t.Fatalf("Relay: %v", err)
			}
			if len(actions.replies) != 0 {
				t.Fatalf("%s text was parsed as an approval reply: %v", source, actions.replies)
			}
		})
	}
}

func TestNoPendingApprovalIsNotAnError(t *testing.T) {
	service, inbox, actions := newService(t)
	actions.applyErr = contracts.ErrApprovalNoPending
	seed(t, inbox, contracts.InboxItem{ID: "i1", Source: "wechat", Title: "同意", Body: "同意"})

	report, err := service.Relay(context.Background())
	if err != nil {
		t.Fatalf("Relay: %v", err)
	}
	if report.Applied != 0 || len(report.Errors) != 0 {
		t.Fatalf("report = %+v, want a quiet pass", report)
	}
	item, _ := inbox.Get(context.Background(), "i1")
	if item.Status != contracts.InboxStatusNew {
		t.Fatalf("item status = %q, want it untouched", item.Status)
	}
}

func TestTheSameItemIsNotParsedTwice(t *testing.T) {
	service, inbox, actions := newService(t)
	seed(t, inbox, contracts.InboxItem{ID: "i1", Source: "wechat", Title: "随便说说", Body: "随便说说"})

	if _, err := service.Relay(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if _, err := service.Relay(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(actions.replies) != 1 {
		t.Fatalf("replies = %v, want the item parsed once (a re-parse every tick is noise, and a stale reply can answer a newer request)", actions.replies)
	}
}
