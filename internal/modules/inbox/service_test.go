package inbox_test

// These tests are written before the implementation (TDD): they pin the inbox'
// observable contract — dedupe, per-source rate budget, capacity, triage
// vocabulary and ordering — because the inbox is the single place where every
// channel meets the Lead, and interconnection bugs hide in exactly these rules.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newService(t *testing.T, tune func(*inbox.Config)) (*inbox.Service, *clock) {
	t.Helper()
	c := newClock()
	cfg := inbox.Config{
		Now:          c.now,
		DedupeWindow: 24 * time.Hour,
		MaxPerMinute: 60,
		MaxItems:     100,
		DefaultTTL:   7 * 24 * time.Hour,
	}
	if tune != nil {
		tune(&cfg)
	}
	return inbox.NewService(cfg), c
}

func item(source, key, title string) contracts.InboxItem {
	return contracts.InboxItem{
		Source:   source,
		Channel:  "chat-1",
		Sender:   "owner",
		DedupKey: key,
		Title:    title,
		Body:     title + " body",
	}
}

func TestAddAssignsIdentityAndStatus(t *testing.T) {
	svc, clock := newService(t, nil)
	ctx := context.Background()

	stored, merged, err := svc.Add(ctx, item("wechat", "k1", "查退款率"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if merged {
		t.Fatal("first add reported merged")
	}
	if stored.ID == "" {
		t.Fatal("stored item has no ID")
	}
	if stored.Status != contracts.InboxStatusNew {
		t.Fatalf("status = %q, want %q", stored.Status, contracts.InboxStatusNew)
	}
	if stored.Count != 1 {
		t.Fatalf("count = %d, want 1", stored.Count)
	}
	if !stored.ReceivedAt.Equal(clock.now()) || !stored.LastSeenAt.Equal(clock.now()) {
		t.Fatalf("timestamps = %v/%v, want %v", stored.ReceivedAt, stored.LastSeenAt, clock.now())
	}
	if stored.ExpiresAt.IsZero() {
		t.Fatal("default TTL did not set ExpiresAt")
	}
}

func TestAddRejectsMissingSourceOrDedupeKey(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, contracts.InboxItem{DedupKey: "k"}); !errors.Is(err, contracts.ErrInboxInvalid) {
		t.Fatalf("missing source err = %v, want ErrInboxInvalid", err)
	}
	if _, _, err := svc.Add(ctx, contracts.InboxItem{Source: "email"}); !errors.Is(err, contracts.ErrInboxInvalid) {
		t.Fatalf("missing dedupe key err = %v, want ErrInboxInvalid", err)
	}
}

func TestDedupeMergesWithinWindow(t *testing.T) {
	svc, clock := newService(t, nil)
	ctx := context.Background()

	first, _, err := svc.Add(ctx, item("slack", "thread-1", "第一条"))
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	clock.advance(2 * time.Hour)
	second, merged, err := svc.Add(ctx, item("slack", "thread-1", "第二条"))
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if !merged {
		t.Fatal("second add inside the window did not merge")
	}
	if second.ID != first.ID {
		t.Fatalf("merged id = %q, want %q", second.ID, first.ID)
	}
	if second.Count != 2 {
		t.Fatalf("merged count = %d, want 2", second.Count)
	}
	if !second.LastSeenAt.Equal(clock.now()) {
		t.Fatalf("LastSeenAt = %v, want %v", second.LastSeenAt, clock.now())
	}
	list, err := svc.List(ctx, contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("items = %d, want 1 (merge collapsed the duplicates)", len(list))
	}
}

func TestDedupeIsScopedBySource(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, item("wechat", "same-key", "微信")); err != nil {
		t.Fatalf("add wechat: %v", err)
	}
	if _, merged, err := svc.Add(ctx, item("email", "same-key", "邮件")); err != nil || merged {
		t.Fatalf("add email = (%v, %v), want (nil, false)", err, merged)
	}
	list, _ := svc.List(ctx, contracts.InboxFilter{})
	if len(list) != 2 {
		t.Fatalf("items = %d, want 2 (dedupe key is source-scoped)", len(list))
	}
}

func TestDedupeWindowExpires(t *testing.T) {
	svc, clock := newService(t, func(c *inbox.Config) { c.DedupeWindow = time.Hour })
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, item("wechat", "k", "a")); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock.advance(2 * time.Hour)
	_, merged, err := svc.Add(ctx, item("wechat", "k", "b"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if merged {
		t.Fatal("add after the dedupe window still merged")
	}
	if list, _ := svc.List(ctx, contracts.InboxFilter{}); len(list) != 2 {
		t.Fatalf("items = %d, want 2", len(list))
	}
}

func TestRateLimitPerSourcePerMinute(t *testing.T) {
	svc, _ := newService(t, func(c *inbox.Config) { c.MaxPerMinute = 2 })
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, _, err := svc.Add(ctx, item("dingtalk", fmt.Sprintf("k%d", i), "ok")); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, _, err := svc.Add(ctx, item("dingtalk", "k2", "third")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("third add err = %v, want ErrInboxRateLimited", err)
	}
	// The budget is per source: another channel is unaffected.
	if _, _, err := svc.Add(ctx, item("email", "k2", "other source")); err != nil {
		t.Fatalf("other source add: %v", err)
	}
}

func TestRateLimitWindowSlides(t *testing.T) {
	svc, clock := newService(t, func(c *inbox.Config) { c.MaxPerMinute = 1 })
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, item("wechat", "k1", "a")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := svc.Add(ctx, item("wechat", "k2", "b")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("second err = %v, want rate limited", err)
	}
	clock.advance(61 * time.Second)
	if _, _, err := svc.Add(ctx, item("wechat", "k3", "c")); err != nil {
		t.Fatalf("after window: %v", err)
	}
}

// The budget is a token bucket now: burst MaxPerMinute, then one slot back every
// minute/MaxPerMinute. These tests pin that shape against the service clock, so
// they move time instead of sleeping.
func TestRateLimitRefillsGradually(t *testing.T) {
	svc, clock := newService(t, func(c *inbox.Config) { c.MaxPerMinute = 2 })
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, _, err := svc.Add(ctx, item("wechat", fmt.Sprintf("k%d", i), "ok")); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, _, err := svc.Add(ctx, item("wechat", "k-overflow", "x")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("third add err = %v, want ErrInboxRateLimited", err)
	}

	// Half of the 30s refill interval: still nothing.
	clock.advance(15 * time.Second)
	if _, _, err := svc.Add(ctx, item("wechat", "k-too-soon", "x")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("add at half interval err = %v, want ErrInboxRateLimited", err)
	}

	// A full refill interval (30s at 2/min) buys exactly one slot, no more.
	clock.advance(15 * time.Second)
	if _, _, err := svc.Add(ctx, item("wechat", "k-refilled", "x")); err != nil {
		t.Fatalf("add after one refill interval: %v", err)
	}
	if _, _, err := svc.Add(ctx, item("wechat", "k-greedy", "x")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("second add with no tokens err = %v, want ErrInboxRateLimited", err)
	}
}

func TestRateLimitBoundaryIsAFullMinute(t *testing.T) {
	svc, clock := newService(t, func(c *inbox.Config) { c.MaxPerMinute = 1 })
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, item("wechat", "k1", "a")); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock.advance(time.Minute - time.Nanosecond)
	if _, _, err := svc.Add(ctx, item("wechat", "k2", "b")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("just before a minute err = %v, want ErrInboxRateLimited", err)
	}
	// At exactly one minute the token is back — the same instant the old
	// "timestamp after now-1m" cutoff released it.
	clock.advance(time.Nanosecond)
	if _, _, err := svc.Add(ctx, item("wechat", "k3", "c")); err != nil {
		t.Fatalf("at exactly one minute: %v", err)
	}
}

// The budget is charged before dedupe, so a duplicate flood cannot bypass it and
// still merges: the third copy is refused, not folded in.
func TestRateLimitIsChargedBeforeDedupe(t *testing.T) {
	svc, _ := newService(t, func(c *inbox.Config) { c.MaxPerMinute = 2 })
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, item("wechat", "same", "a")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, merged, err := svc.Add(ctx, item("wechat", "same", "a")); err != nil || !merged {
		t.Fatalf("second = (%v, %v), want (nil, true)", err, merged)
	}
	if _, _, err := svc.Add(ctx, item("wechat", "same", "a")); !errors.Is(err, contracts.ErrInboxRateLimited) {
		t.Fatalf("third err = %v, want ErrInboxRateLimited (duplicates must not bypass the budget)", err)
	}
}

func TestTriageTaskRecordsTaskIDs(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	stored, _, err := svc.Add(ctx, item("wechat", "k", "查退款率"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	updated, err := svc.Triage(ctx, []string{stored.ID}, contracts.InboxActionTask, []string{"task-1", "task-2"})
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	list, _ := svc.List(ctx, contracts.InboxFilter{Statuses: []string{contracts.InboxStatusTask}})
	if len(list) != 1 {
		t.Fatalf("task items = %d, want 1", len(list))
	}
	if got := list[0].TaskIDs; len(got) != 2 || got[0] != "task-1" || got[1] != "task-2" {
		t.Fatalf("TaskIDs = %v, want [task-1 task-2]", got)
	}
}

func TestGetReturnsStoredAndRejectsMissing(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	stored, _, err := svc.Add(ctx, item("wechat", "k", "标题"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := svc.Get(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != stored.ID || got.Title != "标题" {
		t.Fatalf("Get = %+v, want the stored item", got)
	}
	if _, err := svc.Get(ctx, "missing"); !errors.Is(err, contracts.ErrInboxUnknownItem) {
		t.Fatalf("missing err = %v, want ErrInboxUnknownItem", err)
	}
}

func TestTriageRejectsUnknownAction(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	stored, _, _ := svc.Add(ctx, item("wechat", "k", "x"))
	if _, err := svc.Triage(ctx, []string{stored.ID}, "delete-everything", nil); !errors.Is(err, contracts.ErrInboxUnknownAction) {
		t.Fatalf("err = %v, want ErrInboxUnknownAction", err)
	}
}

func TestTriageRejectsUnknownID(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	if _, err := svc.Triage(ctx, []string{"nope"}, contracts.InboxActionIgnored, nil); !errors.Is(err, contracts.ErrInboxUnknownItem) {
		t.Fatalf("err = %v, want ErrInboxUnknownItem", err)
	}
}

func TestTriageUpdatesExistingDecision(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	stored, _, _ := svc.Add(ctx, item("wechat", "k", "x"))
	if _, err := svc.Triage(ctx, []string{stored.ID}, contracts.InboxActionIgnored, nil); err != nil {
		t.Fatalf("first triage: %v", err)
	}
	if _, err := svc.Triage(ctx, []string{stored.ID}, contracts.InboxActionTask, []string{"t"}); err != nil {
		t.Fatalf("re-triage: %v", err)
	}
	list, _ := svc.List(ctx, contracts.InboxFilter{Statuses: []string{contracts.InboxStatusTask}})
	if len(list) != 1 {
		t.Fatalf("re-triage did not move the item: %d task items", len(list))
	}
}

func TestCapacityRejectsWhenFull(t *testing.T) {
	svc, _ := newService(t, func(c *inbox.Config) { c.MaxItems = 2 })
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, _, err := svc.Add(ctx, item("wechat", fmt.Sprintf("k%d", i), "x")); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, _, err := svc.Add(ctx, item("wechat", "k3", "overflow")); !errors.Is(err, contracts.ErrInboxFull) {
		t.Fatalf("err = %v, want ErrInboxFull", err)
	}
}

func TestListHidesExpiredUnlessAsked(t *testing.T) {
	svc, clock := newService(t, func(c *inbox.Config) { c.DefaultTTL = time.Hour })
	ctx := context.Background()

	if _, _, err := svc.Add(ctx, item("wechat", "k", "stale")); err != nil {
		t.Fatalf("add: %v", err)
	}
	clock.advance(2 * time.Hour)

	list, _ := svc.List(ctx, contracts.InboxFilter{})
	if len(list) != 0 {
		t.Fatalf("expired item still listed: %d", len(list))
	}
	list, _ = svc.List(ctx, contracts.InboxFilter{IncludeExpired: true})
	if len(list) != 1 {
		t.Fatalf("IncludeExpired items = %d, want 1", len(list))
	}
}

func TestListOrdersByPriorityThenRecencyAndLimits(t *testing.T) {
	svc, clock := newService(t, nil)
	ctx := context.Background()

	low, _, _ := svc.Add(ctx, item("wechat", "low", "低"))
	clock.advance(time.Minute)
	high, _, _ := svc.Add(ctx, contracts.InboxItem{
		Source: "wechat", Channel: "chat-1", Sender: "owner", DedupKey: "high",
		Title: "高", Priority: 9,
	})
	clock.advance(time.Minute)
	recent, _, _ := svc.Add(ctx, item("wechat", "recent", "新"))

	list, _ := svc.List(ctx, contracts.InboxFilter{})
	if len(list) != 3 {
		t.Fatalf("items = %d, want 3", len(list))
	}
	if list[0].ID != high.ID {
		t.Fatalf("first item = %q, want the high-priority one", list[0].Title)
	}
	if list[1].ID != recent.ID || list[2].ID != low.ID {
		t.Fatalf("order = %q,%q, want recent then low", list[1].Title, list[2].Title)
	}

	limited, _ := svc.List(ctx, contracts.InboxFilter{Limit: 2})
	if len(limited) != 2 {
		t.Fatalf("limited = %d, want 2", len(limited))
	}
}

func TestStatsCountsWaitingItems(t *testing.T) {
	svc, _ := newService(t, nil)
	ctx := context.Background()

	a, _, _ := svc.Add(ctx, item("wechat", "a", "a"))
	if _, _, err := svc.Add(ctx, item("email", "b", "b")); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.Triage(ctx, []string{a.ID}, contracts.InboxActionTriaged, nil); err != nil {
		t.Fatalf("triage: %v", err)
	}
	stats, err := svc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total != 2 || stats.New != 1 || stats.Triaged != 1 {
		t.Fatalf("stats = %+v, want total 2 / new 1 / triaged 1", stats)
	}
}

func TestConcurrentAddIsRaceFreeAndComplete(t *testing.T) {
	svc, _ := newService(t, func(c *inbox.Config) {
		c.MaxPerMinute = 1000
		c.MaxItems = 1000
	})
	ctx := context.Background()

	const workers, perWorker = 20, 5
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				_, _, err := svc.Add(ctx, item("wechat", fmt.Sprintf("w%d-k%d", w, i), "x"))
				if err != nil {
					t.Errorf("worker %d add %d: %v", w, i, err)
				}
			}
		}(w)
	}
	wg.Wait()

	list, err := svc.List(ctx, contracts.InboxFilter{Limit: 1000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != workers*perWorker {
		t.Fatalf("items = %d, want %d", len(list), workers*perWorker)
	}
}
