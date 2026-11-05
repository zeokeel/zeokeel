package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ReportMaxBytes caps one report tool result (16 KiB).
const ReportMaxBytes = 16 * 1024

const reportDescription = `Return a bounded structured report for a subagent or Ralph round.

Call it with a JSON object:
  {"summary":"Implemented the parser and tests pass"}
  {"summary":"Blocked on a missing credential","details":"The build cannot reach the registry without TOKEN."}

"summary" is required and must contain non-whitespace text. "details" is
optional free-form text. The tool returns
{"kind":"report","summary":...,"details":...,"truncated":...} and never writes
any file or session state: the caller folds the report into its own flow.

The complete JSON result is capped at 16384 bytes. Oversized details are
truncated first; if the summary alone still does not fit, it is truncated too,
and "truncated":true marks the result.`

// ReportResult is the structured report document returned by ReportTool.
type ReportResult struct {
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
	Details   string `json:"details,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// ReportTool implements the report tool. It holds no state.
type ReportTool struct{}

// NewReportTool returns the report tool.
func NewReportTool() *ReportTool { return &ReportTool{} }

// Name implements runtime.Tool.
func (t *ReportTool) Name() string { return "report" }

// Description implements runtime.Tool.
func (t *ReportTool) Description() string { return reportDescription }

// Invoke implements runtime.Tool.
func (t *ReportTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in struct {
		Summary string `json:"summary"`
		Details string `json:"details"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		return "", errors.New(`report: "summary" is required and must contain non-whitespace text`)
	}
	return encodeReport(ReportResult{
		Kind:    "report",
		Summary: summary,
		Details: in.Details,
	}), nil
}

// encodeReport marshals res, shrinking details and then the summary until the
// complete JSON document fits ReportMaxBytes. The result is always valid JSON.
func encodeReport(res ReportResult) string {
	for i := 0; i < 12; i++ {
		encoded, err := json.Marshal(res)
		if err != nil {
			break
		}
		if len(encoded) <= ReportMaxBytes {
			return string(encoded)
		}
		excess := len(encoded) - ReportMaxBytes
		switch {
		case res.Details != "":
			keep := len(res.Details) - excess - 64
			if keep <= 0 {
				res.Details = ""
			} else {
				res.Details = truncateBytes(res.Details, keep, "details truncated")
			}
			res.Truncated = true
		case res.Summary != "":
			keep := len(res.Summary) - excess - 64
			if keep <= 0 {
				res.Summary = ""
			} else {
				res.Summary = truncateBytes(res.Summary, keep, "summary truncated")
			}
			res.Truncated = true
		default:
			i = 12
		}
	}
	fallback, err := json.Marshal(ReportResult{
		Kind:      "report",
		Summary:   utf8Prefix(res.Summary, 256),
		Truncated: true,
		Note:      fmt.Sprintf("report exceeds %d bytes; content was truncated", ReportMaxBytes),
	})
	if err != nil {
		return `{"kind":"report","truncated":true,"note":"report encoding failed"}`
	}
	return string(fallback)
}
