package reports_test

// The digest window (M4 follow-up): a closed WeChat window means the report
// cannot be pushed, so the product retries on a schedule a human can live with —
// once at the digest hour, otherwise on a bounded retry interval — instead of
// poking the channel every minute.

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/reports"
)

// clock is a movable time source for the digest tests.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func (c *clock) set(hour, minute int) {
	c.at = time.Date(2026, 11, 26, hour, minute, 0, 0, time.Local)
}

func digestService(t *testing.T) (*reports.Service, *fakeDispatcher, *clock) {
	t.Helper()
	c := &clock{}
	c.set(6, 0)
	dispatcher := &fakeDispatcher{receipt: contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp, Reason: "window closed"}}
	cfg := reports.Config{Now: c.now, DigestAt: "08:00", RetryInterval: 30 * time.Minute}
	return reports.New(dispatcher, nil, cfg), dispatcher, c
}

func deferOne(t *testing.T, svc *reports.Service) {
	t.Helper()
	stored, err := svc.Put(context.Background(), passingReport())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := svc.Deliver(context.Background(), stored.ID); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
}

func TestDigestWaitsForTheDigestHour(t *testing.T) {
	svc, dispatcher, c := digestService(t)
	deferOne(t, svc)
	c.set(6, 5)
	report, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Retried != 0 || report.Digests != 0 {
		t.Fatalf("sweep = %+v, want nothing sent before the digest hour", report)
	}
	if len(dispatcher.sends) != 1 {
		t.Fatalf("sends = %d, want only the original delivery attempt", len(dispatcher.sends))
	}
}

func TestDigestGoesOutOnceAtTheDigestHour(t *testing.T) {
	svc, dispatcher, c := digestService(t)
	deferOne(t, svc)
	// The channel recovers overnight.
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelWeChat, Attempts: []string{"wechat"}}
	c.set(8, 0)
	first, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if first.Digests != 1 || first.Delivered != 1 {
		t.Fatalf("first sweep = %+v, want one digest", first)
	}
	// A second sweep the same morning must not repeat it.
	c.set(8, 30)
	second, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if second.Digests != 0 {
		t.Fatalf("second sweep = %+v, want no second digest", second)
	}
}

func TestDigestRetriesOnTheIntervalOutsideTheHour(t *testing.T) {
	svc, dispatcher, c := digestService(t)
	deferOne(t, svc)
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelWeChat}
	// Before the digest hour, but past the retry interval: the channel may have
	// recovered, so one retry is worth making.
	c.set(6, 31)
	report, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Digests != 1 {
		t.Fatalf("sweep = %+v, want a digest on the retry interval", report)
	}
}

func TestDigestAtParsesAndFallsBack(t *testing.T) {
	svc, dispatcher, c := digestService(t)
	deferOne(t, svc)
	// A malformed digest hour must not digest at an invented time: the retry
	// interval still applies, so a recovered channel is not blocked forever.
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp, Reason: "window closed"}
	broken := reports.New(dispatcher, nil, reports.Config{Now: c.now, DigestAt: "25:99", RetryInterval: 30 * time.Minute})
	deferOne(t, broken)
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelWeChat}
	c.set(9, 0)
	report, err := broken.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Digests != 1 {
		t.Fatalf("sweep = %+v, want the interval retry to still work", report)
	}
	_ = svc
}
