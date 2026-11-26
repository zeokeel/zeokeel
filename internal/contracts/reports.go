package contracts

// Structured reports (M4): what the lead says when it is done.
//
// A report is not chat prose. It has a goal, the evidence that backs it, the
// questions that are still open, and a verdict — the four things a person needs
// to decide whether to accept the work, and the four things an audit can check
// without reading a transcript.
//
// Delivery is separate from the record on purpose: the record must survive an
// undeliverable channel (WeChat's 24h reply window is the standing example), and
// "was it delivered" is a fact about the report, not about whether it exists.

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Report verdicts. They are a closed vocabulary so a UI (and an auditor) can
// branch on them instead of parsing prose.
const (
	ReportVerdictPass    = "pass"
	ReportVerdictPartial = "partial"
	ReportVerdictFail    = "fail"
)

// Report errors are typed: the gateway answers 404/400 from the sentinel.
var (
	// ErrReportNotFound means the requested id does not exist.
	ErrReportNotFound = errors.New("report: not found")
	// ErrReportInvalid means the report failed validation (empty goal, unknown
	// verdict, too many evidence rows).
	ErrReportInvalid = errors.New("report: invalid")
)

// ReportEvidence is one proof attached to a report. Same vocabulary as the
// lease's evidence (file/link/output/screenshot) so a report and a submission
// cannot drift into two dialects of "what proves it".
type ReportEvidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}

// Report is one finished piece of work, as told to the human.
type Report struct {
	ID string `json:"id"`
	// TaskID / WorkflowRunID / SessionID are the provenance: what work this
	// report is about. All optional — a conversational report may have none.
	TaskID        string `json:"taskId,omitempty"`
	WorkflowRunID string `json:"workflowRunId,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	// Goal is what was asked. Required: a report without a goal cannot be read.
	Goal string `json:"goal"`
	// Evidence is what proves it. A pass with no evidence is refused at write
	// time — the same rule the task lease applies to a submission.
	Evidence []ReportEvidence `json:"evidence,omitempty"`
	// OpenQuestions are the things the human still has to decide.
	OpenQuestions []string `json:"openQuestions,omitempty"`
	// Verdict is pass / partial / fail.
	Verdict string `json:"verdict"`
	// Author is who reports (a role id such as "lead", or a user identity).
	Author string    `json:"author,omitempty"`
	At     time.Time `json:"at"`
	// Delivered reports that at least one external channel accepted the report.
	Delivered bool `json:"delivered"`
	// Deferred reports that only the in-app channel took it (a closed WeChat
	// window, quiet hours, no bound channel) — the human has not been told yet.
	Deferred    bool   `json:"deferred"`
	DeferReason string `json:"deferReason,omitempty"`
}

// ReportDelivery is the outcome of one delivery attempt.
type ReportDelivery struct {
	ReportID string
	// Channel is the channel that accepted it ("inapp" when nothing external did).
	Channel string
	// Attempts / Errors come from the dispatcher's receipt.
	Attempts []string
	Errors   []string
	Deferred bool
	Reason   string
}

// ReportSweep is one retry pass over deferred reports.
type ReportSweep struct {
	Retried   int
	Delivered int
	Digests   int
	Errors    []string
}

// ReportService is the report backend.
type ReportService interface {
	// List returns the newest reports first, at most limit (0 = the default).
	List(ctx context.Context, limit int) ([]Report, error)
	Get(ctx context.Context, id string) (Report, bool, error)
	// Put validates and stores one report; the returned value is the stored one
	// (id and time filled in when the caller omitted them).
	Put(ctx context.Context, report Report) (Report, error)
	// Deliver sends one stored report through the notification router.
	Deliver(ctx context.Context, id string) (ReportDelivery, error)
	// Sweep retries the deferred reports as one digest, so a closed channel does
	// not turn into a queue of unread messages.
	Sweep(ctx context.Context) (ReportSweep, error)
}

// KeyReports is provided by the reports module.
var KeyReports = cordis.NewServiceKey[ReportService]("zeokeel/reports")
