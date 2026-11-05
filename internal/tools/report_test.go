package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func reportResult(t *testing.T, raw string) ReportResult {
	t.Helper()
	out, err := NewReportTool().Invoke(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(out) > ReportMaxBytes {
		t.Fatalf("report is %d bytes, want <= %d", len(out), ReportMaxBytes)
	}
	if !json.Valid([]byte(out)) {
		t.Fatal("report is not valid JSON")
	}
	var result ReportResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out)
	}
	return result
}

func TestReportReturnsStructuredJSON(t *testing.T) {
	args, err := json.Marshal(map[string]string{
		"summary": "Implemented the parser",
		"details": "All targeted tests pass.",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result := reportResult(t, string(args))
	if result.Kind != "report" || result.Summary != "Implemented the parser" || result.Details != "All targeted tests pass." || result.Truncated {
		t.Fatalf("result = %+v", result)
	}
	if strings.TrimSpace(NewReportTool().Description()) == "" {
		t.Fatal("report description is empty")
	}
	if NewReportTool().Name() != "report" {
		t.Fatalf("Name = %q", NewReportTool().Name())
	}
}

func TestReportValidation(t *testing.T) {
	tool := NewReportTool()
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"summary":"  "}`)); err == nil {
		t.Fatal("report with a blank summary = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("report without a summary = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("report with invalid JSON = nil error")
	}
}

func TestReportCapsOversizedDetails(t *testing.T) {
	args, err := json.Marshal(map[string]string{
		"summary": "keep me",
		"details": strings.Repeat("detail ", 20_000),
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result := reportResult(t, string(args))
	if result.Summary != "keep me" {
		t.Fatalf("summary = %q, want the summary to survive details truncation", result.Summary)
	}
	if !result.Truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(result.Details) >= 160_000 {
		t.Fatalf("details length = %d, want truncation", len(result.Details))
	}
}

func TestReportCapsOversizedSummary(t *testing.T) {
	summary := strings.Repeat("summary ", 20_000)
	args, err := json.Marshal(map[string]string{"summary": summary})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result := reportResult(t, string(args))
	if !result.Truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(result.Summary) >= len(summary) {
		t.Fatalf("summary length = %d, want truncation", len(result.Summary))
	}
}

func TestReportKeepsUTF8ValidWhenCapping(t *testing.T) {
	summary := strings.Repeat("世界", 20_000)
	args, err := json.Marshal(map[string]string{"summary": summary})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := NewReportTool().Invoke(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(out) > ReportMaxBytes {
		t.Fatalf("report is %d bytes, want <= %d", len(out), ReportMaxBytes)
	}
	if !utf8.ValidString(out) {
		t.Fatal("report output is not valid UTF-8")
	}
	if !json.Valid([]byte(out)) {
		t.Fatal("report output is not valid JSON")
	}
}
