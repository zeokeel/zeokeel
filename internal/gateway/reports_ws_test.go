package gateway

// reports.* (M4): the stored reports and a manual redelivery. The interesting
// half is the deferral: a report the human has not actually been told about must
// come back as deferred with its reason, not as a success.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeReports struct {
	rows       []contracts.Report
	delivery   contracts.ReportDelivery
	deliverErr error
	delivered  string
}

func (f *fakeReports) List(context.Context, int) ([]contracts.Report, error) { return f.rows, nil }
func (f *fakeReports) Get(_ context.Context, id string) (contracts.Report, bool, error) {
	for _, row := range f.rows {
		if row.ID == id {
			return row, true, nil
		}
	}
	return contracts.Report{}, false, nil
}
func (f *fakeReports) Put(_ context.Context, report contracts.Report) (contracts.Report, error) {
	return report, nil
}
func (f *fakeReports) Deliver(_ context.Context, id string) (contracts.ReportDelivery, error) {
	f.delivered = id
	return f.delivery, f.deliverErr
}
func (f *fakeReports) Sweep(context.Context) (contracts.ReportSweep, error) {
	return contracts.ReportSweep{}, nil
}

func reportGateway(t *testing.T, service contracts.ReportService) *Gateway {
	t.Helper()
	gateway := newTestGateway(t, &fakeChat{})
	if service != nil {
		if _, err := gateway.root.Provide(contracts.KeyReports, service); err != nil {
			t.Fatalf("provide reports: %v", err)
		}
	}
	return gateway
}

func TestReportsSnapshotLists(t *testing.T) {
	service := &fakeReports{rows: []contracts.Report{{ID: "report-1", Goal: "修好登录", Verdict: contracts.ReportVerdictPass}}}
	gateway := reportGateway(t, service)
	answer, problem := gateway.reportsSnapshot(context.Background(), nil)
	if problem != nil {
		t.Fatalf("reportsSnapshot: %+v", problem)
	}
	payload, ok := answer.(map[string]any)
	if !ok {
		t.Fatalf("answer = %T", answer)
	}
	rows, ok := payload["reports"].([]contracts.Report)
	if !ok || len(rows) != 1 || rows[0].Goal != "修好登录" {
		t.Fatalf("reports = %+v", payload["reports"])
	}
}

func TestReportsDeliverReportsDeferral(t *testing.T) {
	service := &fakeReports{delivery: contracts.ReportDelivery{
		ReportID: "report-1", Channel: contracts.NotifyChannelInApp,
		Deferred: true, Reason: "wechat window closed",
	}}
	gateway := reportGateway(t, service)
	body, _ := json.Marshal(reportDeliverParams{ID: "report-1"})
	answer, problem := gateway.reportsDeliver(context.Background(), body)
	if problem != nil {
		t.Fatalf("reportsDeliver: %+v", problem)
	}
	payload := answer.(map[string]any)
	if payload["deferred"] != true || payload["reason"] != "wechat window closed" {
		t.Fatalf("answer = %+v, want the deferral carried through", payload)
	}
	if service.delivered != "report-1" {
		t.Fatalf("delivered = %q", service.delivered)
	}
}

func TestReportsDeliverUnknownIsAParamsError(t *testing.T) {
	service := &fakeReports{deliverErr: contracts.ErrReportNotFound}
	gateway := reportGateway(t, service)
	body, _ := json.Marshal(reportDeliverParams{ID: "nope"})
	if _, problem := gateway.reportsDeliver(context.Background(), body); problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("problem = %+v, want codeInvalidParams", problem)
	}
}

func TestReportsUnavailableWithoutTheModule(t *testing.T) {
	gateway := reportGateway(t, nil)
	if _, problem := gateway.reportsSnapshot(context.Background(), nil); problem == nil {
		t.Fatal("reportsSnapshot answered without a backend")
	}
}
