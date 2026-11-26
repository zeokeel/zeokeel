package hosttools

// The report tool (M4): how the lead tells the human it is done.
//
// Free-form "here is what I did" is what the chat transcript already is; this
// tool exists so the closing message has a shape an audit can check — goal,
// evidence, open questions, verdict — and so delivery ("did the human actually
// get told?") is a recorded fact rather than an assumption.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ToolReport is the model-visible name.
const ToolReport = "report"

// reportService is the slice of contracts.ReportService the tool uses.
type reportService interface {
	Put(ctx context.Context, report contracts.Report) (contracts.Report, error)
	Deliver(ctx context.Context, id string) (contracts.ReportDelivery, error)
}

// reportDeps resolves the report backend lazily (the module may activate after
// this one).
type reportDeps struct {
	service func() (reportService, bool)
}

type reportTool struct{ deps reportDeps }

func (t *reportTool) Name() string { return ToolReport }

func (t *reportTool) Description() string {
	return "Report finished work to the human in a structured shape: the goal, the evidence that proves it, the questions " +
		"still open, and a verdict. A 'pass' needs at least one evidence row. Use it when a task or a workflow run is done — " +
		"the chat transcript is not a report."
}

func (t *reportTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"goal": stringProperty("What was asked, in one or two sentences."),
		"verdict": map[string]any{
			"type":        "string",
			"description": "pass, partial or fail.",
			"enum":        []any{contracts.ReportVerdictPass, contracts.ReportVerdictPartial, contracts.ReportVerdictFail},
		},
		"evidence": map[string]any{
			"type":        "array",
			"description": "Proof rows: kind (file|link|output|screenshot), ref, optional note.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"kind": stringProperty("file, link, output or screenshot."),
					"ref":  stringProperty("Path, URL or command output reference."),
					"note": stringProperty("One line of context (optional)."),
				},
				"required": []any{"kind", "ref"},
			},
		},
		"openQuestions": map[string]any{
			"type":        "array",
			"description": "Things the human still has to decide.",
			"items":       map[string]any{"type": "string"},
		},
		"taskId":  stringProperty("The board task this report is about (optional)."),
		"deliver": map[string]any{"type": "boolean", "description": "Send it now (default true)."},
	}, "goal", "verdict")
}

func (t *reportTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Goal     string `json:"goal"`
		Verdict  string `json:"verdict"`
		Evidence []struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
			Note string `json:"note"`
		} `json:"evidence"`
		OpenQuestions []string `json:"openQuestions"`
		TaskID        string   `json:"taskId"`
		Deliver       *bool    `json:"deliver"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return rig.ToolResult{}, err
	}
	if err := contextError(ctx); err != nil {
		return rig.ToolResult{}, err
	}
	if t.deps.service == nil {
		return errorResult("report: this product has no report backend"), nil
	}
	service, ok := t.deps.service()
	if !ok || service == nil {
		return errorResult("report: this product has no report backend"), nil
	}
	report := contracts.Report{
		Goal: args.Goal, Verdict: args.Verdict, OpenQuestions: args.OpenQuestions,
		TaskID: strings.TrimSpace(args.TaskID), Author: contracts.PresetLeadID,
	}
	for _, item := range args.Evidence {
		report.Evidence = append(report.Evidence, contracts.ReportEvidence{Kind: item.Kind, Ref: item.Ref, Note: item.Note})
	}
	stored, err := service.Put(ctx, report)
	if err != nil {
		return errorResult("report: %v", err), nil
	}
	deliver := args.Deliver == nil || *args.Deliver
	if !deliver {
		return rig.ToolResult{Content: fmt.Sprintf("stored report %s (not delivered)", stored.ID)}, nil
	}
	delivery, err := service.Deliver(ctx, stored.ID)
	if err != nil {
		return rig.ToolResult{Content: fmt.Sprintf("stored report %s, delivery failed: %v", stored.ID, err)}, nil
	}
	if delivery.Deferred {
		return rig.ToolResult{Content: fmt.Sprintf(
			"stored report %s; it could not be delivered (%s) and is queued as a deferred digest", stored.ID, delivery.Reason)}, nil
	}
	return rig.ToolResult{Content: fmt.Sprintf("stored report %s and delivered via %s", stored.ID, delivery.Channel)}, nil
}
