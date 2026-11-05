package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// PTC tool: pipeline-style programmatic tool calling.
//
// DeepSeek Harness's PTC mode exposes a code-capable programmatic tool-calling
// runtime. ZeoKeel deliberately does not embed a JavaScript sandbox: its PTC
// mode keeps the standard tool set and adds one "ptc" tool that executes an
// ordered batch of ordinary tool calls and returns every result. The model can
// then filter, organize, deduplicate, count or summarize the batch in one
// reasoning step instead of spending one model round trip per call.
//
// The batch is strictly sequential and pipeline-style: call N+1 starts only
// after call N returns, and every call receives its own JSON arguments. A
// failing call records an error for that entry and does not stop the rest of
// the batch.

// PTC tool limits. A batch larger than this is rejected instead of letting one
// model call run unbounded work.
const (
	maxPTCCalls = 64
)

// PTCRequest is the JSON argument of the "ptc" tool.
type PTCRequest struct {
	Calls []PTCCall `json:"calls"`
}

// PTCCall is one call in a PTC batch.
type PTCCall struct {
	// Tool is the registered tool name to invoke.
	Tool string `json:"tool"`
	// Args is the JSON argument object for the tool. Omitted or null means {}.
	Args json.RawMessage `json:"args,omitempty"`
}

// PTCResult is one entry of a PTC batch response.
type PTCResult struct {
	Index  int    `json:"index"`
	Tool   string `json:"tool"`
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// PTCResponse is the JSON result of the "ptc" tool.
type PTCResponse struct {
	Results []PTCResult `json:"results"`
}

// ptcTool implements runtime.Tool for ModePTC.
type ptcTool struct {
	registry *Registry
	gate     Gate
	reviewer ToolReviewer
	planMode func() bool
}

var _ Tool = (*ptcTool)(nil)
var _ ApprovalGateBinder = (*ptcTool)(nil)
var _ ToolReviewBinder = (*ptcTool)(nil)
var _ planModeBinder = (*ptcTool)(nil)

// NewPTCTool returns the pipeline-style PTC batch tool over registry. The app
// registers it in the production registry so the tool is discoverable, while
// Agent.effectiveTools synthesizes the same tool for a bare runtime registry.
// Agent.New binds the approval Gate, the tool reviewer and the plan-mode
// resolver; nested calls consult all three per call.
func NewPTCTool(registry *Registry) Tool {
	return &ptcTool{registry: registry}
}

// newPTCTool builds the batch tool with the Gate already bound, used when the
// Agent synthesizes the tool for a runtime whose registry has no concrete
// instance.
func newPTCTool(registry *Registry, gate Gate, planMode func() bool, reviewer ToolReviewer) Tool {
	return &ptcTool{registry: registry, gate: gate, reviewer: reviewer, planMode: planMode}
}

// SetToolReviewer implements ToolReviewBinder. New calls it once before the
// agent serves turns, so the field is effectively read-only afterwards.
func (t *ptcTool) SetToolReviewer(reviewer ToolReviewer) { t.reviewer = reviewer }

// SetApprovalGate implements ApprovalGateBinder. New calls it once before the
// agent serves turns, so the field is effectively read-only afterwards.
func (t *ptcTool) SetApprovalGate(gate Gate) { t.gate = gate }

// SetPlanModeFunc implements planModeBinder. New calls it once before the
// agent serves turns; the function itself is read per nested call.
func (t *ptcTool) SetPlanModeFunc(fn func() bool) { t.planMode = fn }

// Name implements runtime.Tool.
func (t *ptcTool) Name() string { return ptcToolName }

const ptcDescription = `Execute a batch of tool calls sequentially in one step (pipeline-style PTC).

PTC means programmatic tool calling in ZeoKeel's pipeline form: you provide an ordered list of ordinary tool calls and the runtime executes them in order, one after another, then returns every result. There is no JavaScript, no code evaluation and no sandbox; only registered tools can be called.

Call it with a JSON object:
  {"calls":[{"tool":"read_file","args":{"path":"a.txt"}},{"tool":"grep","args":{"pattern":"TODO"}}]}

Rules:
- "calls" is required and must contain 1..64 entries.
- Each entry names a registered tool; "args" is that tool's JSON argument object (omitted means {}).
- Calls run strictly in order. A failing call records an error for that entry and the remaining calls still run.
- The result is {"results":[{"index":0,"tool":"read_file","ok":true,"output":"..."}]}.
- Images returned by a nested tool (such as read_image) are attached to the next model call like a direct tool image; the batch output still includes their image_data_url text.
- Use it to batch independent reads, searches or listings, then filter, organize, deduplicate, count or summarize the results yourself.
- The ptc tool itself and Creator-only settings tools cannot be called from a batch.
- Approvals behave the same inside a batch: every nested call that needs approval asks first, and a denied call fails only that entry.`

// Description implements runtime.Tool.
func (t *ptcTool) Description() string { return ptcDescription }

// Invoke implements runtime.Tool.
func (t *ptcTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil || t.registry == nil {
		return "", fmt.Errorf("ptc: tool is not initialized")
	}
	if len(strings.TrimSpace(string(args))) == 0 {
		return "", fmt.Errorf(`ptc: "calls" is required`)
	}
	var req PTCRequest
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return "", fmt.Errorf("ptc: invalid arguments: %w", err)
	}
	if len(req.Calls) == 0 {
		return "", fmt.Errorf(`ptc: "calls" is required and must not be empty`)
	}
	if len(req.Calls) > maxPTCCalls {
		return "", fmt.Errorf("ptc: %d calls exceeds the %d call batch limit", len(req.Calls), maxPTCCalls)
	}

	response := PTCResponse{Results: make([]PTCResult, 0, len(req.Calls))}
	for i, call := range req.Calls {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		name := strings.TrimSpace(call.Tool)
		if name == "" {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false, Error: `ptc: each call requires a "tool" name`,
			})
			continue
		}
		if name == ptcToolName || creatorOnlyToolNames[name] {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false,
				Error: fmt.Sprintf("ptc: tool %q is not available inside a PTC batch", name),
			})
			continue
		}
		target, ok := t.registry.Get(name)
		if !ok {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false,
				Error: fmt.Sprintf("ptc: unknown tool %q", name),
			})
			continue
		}
		callArgs, err := ptcCallArgs(call.Args)
		if err != nil {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false,
				Error: fmt.Sprintf("ptc: tool %q: %v", name, err),
			})
			continue
		}
		if t.planMode != nil && t.planMode() && PlanModeBlocksTool(name) {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false,
				Error: planModeBlockedError(name).Error(),
			})
			continue
		}
		if err := t.awaitNestedApproval(ctx, name, callArgs); err != nil {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false,
				Error: err.Error(),
			})
			continue
		}
		out, err := target.Invoke(ctx, callArgs)
		if err != nil {
			response.Results = append(response.Results, PTCResult{
				Index: i, Tool: name, OK: false,
				Error: err.Error(),
			})
			continue
		}
		response.Results = append(response.Results, PTCResult{
			Index: i, Tool: name, OK: true, Output: out,
		})
	}

	data, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("ptc: encode results: %w", err)
	}
	return string(data), nil
}

// awaitNestedApproval enforces the Gate and automatic review for one call
// inside a batch. The outer "ptc" invocation has its own approval (handled by
// the eino tool wrapper), but that decision does not cover the nested tools:
// each nested call that the Gate requires is reviewed and, if review is
// unavailable or fails, gets its own ApprovalRequest with a unique call id. A
// nil gate keeps the historical unrestricted behavior for bare runtimes that
// configure none.
func (t *ptcTool) awaitNestedApproval(ctx context.Context, name string, args json.RawMessage) error {
	if t.gate == nil || !t.gate.RequireApproval(name, args) {
		return nil
	}
	if allow, reason, reviewed := reviewToolCall(ctx, t.reviewer, name, args); reviewed {
		if allow {
			return nil
		}
		return autoReviewDeniedError(reason)
	}
	return awaitGateApproval(ctx, t.gate, uuid.NewString(), name, args)
}

// ptcCallArgs normalizes one call's arguments. A model may send an object, a
// JSON string containing an object, or omit the field; all become a valid raw
// JSON argument object.
func ptcCallArgs(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage("{}"), nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("invalid args string: %w", err)
		}
		trimmed = strings.TrimSpace(text)
		if trimmed == "" {
			return json.RawMessage("{}"), nil
		}
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("args is not valid JSON")
	}
	return json.RawMessage(trimmed), nil
}
