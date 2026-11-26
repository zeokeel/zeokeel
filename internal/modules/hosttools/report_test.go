package hosttools

// The report tool (M4) at its seams: the structured shape, and the two outcomes
// the lead must be able to tell apart — delivered, or stored and deferred.

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeReports struct {
	stored     *contracts.Report
	delivery   contracts.ReportDelivery
	deliverErr error
	delivered  bool
}

func (f *fakeReports) Put(_ context.Context, report contracts.Report) (contracts.Report, error) {
	// Mirror the backend's rule the tool depends on: a pass needs evidence.
	if report.Verdict == contracts.ReportVerdictPass && len(report.Evidence) == 0 {
		return contracts.Report{}, contracts.ErrReportInvalid
	}
	report.ID = "report-1"
	f.stored = &report
	return report, nil
}

func (f *fakeReports) Deliver(_ context.Context, id string) (contracts.ReportDelivery, error) {
	f.delivered = true
	f.delivery.ReportID = id
	return f.delivery, f.deliverErr
}

func reportToolSet(service *fakeReports) rig.Tool {
	for _, tool := range []rig.Tool{&reportTool{deps: reportDeps{service: func() (reportService, bool) { return service, service != nil }}}} {
		if tool.Name() == ToolReport {
			return tool
		}
	}
	return nil
}

func TestReportToolStoresAndDelivers(t *testing.T) {
	service := &fakeReports{delivery: contracts.ReportDelivery{Channel: contracts.NotifyChannelWeChat}}
	tool := reportToolSet(service)
	result := invoke(t, tool, "s1", `{"goal":"修好登录","verdict":"pass","evidence":[{"kind":"output","ref":"go test ./auth/..."}]}`)
	if result.IsError || !service.delivered {
		t.Fatalf("result=%+v delivered=%v", result, service.delivered)
	}
	if service.stored == nil || service.stored.Verdict != contracts.ReportVerdictPass || len(service.stored.Evidence) != 1 {
		t.Fatalf("stored = %+v", service.stored)
	}
	if !strings.Contains(result.Content, "delivered via wechat") {
		t.Fatalf("content = %q, want the channel", result.Content)
	}
}

func TestReportToolKeepsADeferredReport(t *testing.T) {
	service := &fakeReports{delivery: contracts.ReportDelivery{Channel: contracts.NotifyChannelInApp, Deferred: true, Reason: "window closed"}}
	tool := reportToolSet(service)
	result := invoke(t, tool, "s1", `{"goal":"修好登录","verdict":"partial","evidence":[{"kind":"link","ref":"https://ci/1"}]}`)
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Content, "deferred") {
		t.Fatalf("content = %q, want it to say the report was deferred", result.Content)
	}
}

func TestReportToolReportsValidationAsToolError(t *testing.T) {
	service := &fakeReports{}
	tool := reportToolSet(service)
	// A pass with no evidence is refused by the backend; the model must see it as
	// a tool error rather than a silent success.
	result := invoke(t, tool, "s1", `{"goal":"修好登录","verdict":"pass"}`)
	if !result.IsError || !strings.Contains(result.Content, "report:") {
		t.Fatalf("result = %+v, want a readable tool error", result)
	}
	if service.delivered {
		t.Fatal("a refused report was delivered")
	}
}
