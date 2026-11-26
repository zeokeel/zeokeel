package reports_test

// The report backend (M4): what a report must contain, what "delivered" means,
// and how an undeliverable channel degrades into one digest instead of a queue.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/reports"
)

type fakeDispatcher struct {
	receipt contracts.DispatchReceipt
	err     error
	sends   []string
}

func (f *fakeDispatcher) Deliver(_ context.Context, category, text string) (contracts.DispatchReceipt, error) {
	f.sends = append(f.sends, category+": "+text)
	return f.receipt, f.err
}

func newService(t *testing.T) (*reports.Service, *fakeDispatcher) {
	t.Helper()
	dispatcher := &fakeDispatcher{receipt: contracts.DispatchReceipt{Channel: contracts.NotifyChannelWeChat, Attempts: []string{"wechat"}}}
	clock := time.Unix(1_700_000_000, 0)
	return reports.New(dispatcher, nil, reports.Config{Now: func() time.Time { return clock }}), dispatcher
}

func passingReport() contracts.Report {
	return contracts.Report{
		Goal: "把登录页修好",
		Evidence: []contracts.ReportEvidence{
			{Kind: "output", Ref: "go test ./auth/...", Note: "全部通过"},
		},
		Verdict: contracts.ReportVerdictPass,
		Author:  contracts.PresetLeadID,
	}
}

func TestPutValidatesAndStores(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	stored, err := svc.Put(ctx, passingReport())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored.ID == "" || stored.At.IsZero() {
		t.Fatalf("stored = %+v, want id and time filled in", stored)
	}
	got, ok, err := svc.Get(ctx, stored.ID)
	if err != nil || !ok || got.Goal != "把登录页修好" {
		t.Fatalf("Get = (%+v, %v, %v)", got, ok, err)
	}
}

func TestPutRefusesAPassWithoutEvidence(t *testing.T) {
	svc, _ := newService(t)
	report := passingReport()
	report.Evidence = nil
	if _, err := svc.Put(context.Background(), report); !errors.Is(err, contracts.ErrReportInvalid) {
		t.Fatalf("err = %v, want ErrReportInvalid", err)
	}
}

func TestPutRefusesBrokenReports(t *testing.T) {
	svc, _ := newService(t)
	cases := map[string]func(*contracts.Report){
		"no goal":         func(r *contracts.Report) { r.Goal = "  " },
		"unknown verdict": func(r *contracts.Report) { r.Verdict = "great" },
		"evidence row":    func(r *contracts.Report) { r.Evidence = []contracts.ReportEvidence{{Kind: "file"}} },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			report := passingReport()
			edit(&report)
			if _, err := svc.Put(context.Background(), report); !errors.Is(err, contracts.ErrReportInvalid) {
				t.Fatalf("err = %v, want ErrReportInvalid", err)
			}
		})
	}
}

func TestDeliverMarksDeliveredWhenAChannelAccepts(t *testing.T) {
	svc, dispatcher := newService(t)
	ctx := context.Background()
	stored, err := svc.Put(ctx, passingReport())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	delivery, err := svc.Deliver(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Deferred || delivery.Channel != contracts.NotifyChannelWeChat {
		t.Fatalf("delivery = %+v, want wechat and not deferred", delivery)
	}
	if len(dispatcher.sends) != 1 || !strings.Contains(dispatcher.sends[0], "把登录页修好") {
		t.Fatalf("sends = %v, want the report text", dispatcher.sends)
	}
	got, _, _ := svc.Get(ctx, stored.ID)
	if !got.Delivered || got.Deferred {
		t.Fatalf("stored = %+v, want delivered and not deferred", got)
	}
}

// The WeChat 24h window closes, quiet hours hit, or no channel is bound: the
// dispatcher falls back to in-app. That is NOT delivery — the human has not been
// told — so the report stays deferred and keeps its reason.
func TestInAppOnlyIsDeferred(t *testing.T) {
	svc, dispatcher := newService(t)
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp, Reason: "wechat window closed"}
	ctx := context.Background()
	stored, err := svc.Put(ctx, passingReport())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	delivery, err := svc.Deliver(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !delivery.Deferred || !strings.Contains(delivery.Reason, "window") {
		t.Fatalf("delivery = %+v, want deferred with the reason", delivery)
	}
	got, _, _ := svc.Get(ctx, stored.ID)
	if got.Delivered || !got.Deferred || got.DeferReason == "" {
		t.Fatalf("stored = %+v, want deferred", got)
	}
}

func TestSweepSendsOneDigestForAllDeferred(t *testing.T) {
	svc, dispatcher := newService(t)
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp, Reason: "window closed"}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		stored, err := svc.Put(ctx, passingReport())
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := svc.Deliver(ctx, stored.ID); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	// The channel recovers before the sweep.
	dispatcher.receipt = contracts.DispatchReceipt{Channel: contracts.NotifyChannelWeChat, Attempts: []string{"wechat"}}
	report, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Retried != 3 || report.Digests != 1 {
		t.Fatalf("sweep = %+v, want 3 retried as one digest", report)
	}
	if len(dispatcher.sends) != 4 {
		t.Fatalf("sends = %d, want 3 deliveries + 1 digest", len(dispatcher.sends))
	}
	if !strings.Contains(dispatcher.sends[3], "3 份报告") {
		t.Fatalf("digest = %q, want it to say how many accumulated", dispatcher.sends[3])
	}
	rows, _ := svc.List(ctx, 10)
	for _, row := range rows {
		if !row.Delivered {
			t.Fatalf("row %s still deferred after a successful digest", row.ID)
		}
	}
}

func TestDeliverUnknownReport(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.Deliver(context.Background(), "nope"); !errors.Is(err, contracts.ErrReportNotFound) {
		t.Fatalf("err = %v, want ErrReportNotFound", err)
	}
}

func TestReportsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	dispatcher := &fakeDispatcher{receipt: contracts.DispatchReceipt{Channel: contracts.NotifyChannelInApp}}
	cfg := reports.Config{}
	first, err := reports.Open(dir, dispatcher, nil, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	stored, err := first.Put(context.Background(), passingReport())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	second, err := reports.Open(dir, dispatcher, nil, cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, err := second.Get(context.Background(), stored.ID)
	if err != nil || !ok || got.Goal != stored.Goal {
		t.Fatalf("report after reopen = (%+v, %v, %v)", got, ok, err)
	}
}
