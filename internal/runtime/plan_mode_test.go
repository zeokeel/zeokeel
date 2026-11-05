package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/zeokeel/zeokeel/internal/state"
)

// planCounterTool is a named tool that records whether it ran. Its name is
// what the plan-mode gate classifies, so the tests can exercise mutating,
// external and read-only names without the real implementations.
type planCounterTool struct {
	name  string
	calls atomic.Int64
}

func (t *planCounterTool) Name() string        { return t.name }
func (t *planCounterTool) Description() string { return "plan-mode test tool " + t.name }
func (t *planCounterTool) Invoke(_ context.Context, _ json.RawMessage) (string, error) {
	t.calls.Add(1)
	return t.name + " ran", nil
}

func planRegister(t *testing.T, reg *Registry, tools ...*planCounterTool) {
	t.Helper()
	for _, tool := range tools {
		if err := reg.Register(tool); err != nil {
			t.Fatalf("register %s: %v", tool.name, err)
		}
	}
}

// planToolEnds indexes the turn's tool_end events by call id.
func planToolEnds(events []state.Event) map[string]state.ToolEnd {
	out := make(map[string]state.ToolEnd)
	for _, ev := range events {
		if end, ok := ev.(state.ToolEnd); ok {
			out[end.CallID] = end
		}
	}
	return out
}

// TestPlanModeRejectsMutatingTools proves the runtime gate blocks the shell and
// file-write tools before they run, allows read_file through, and delivers the
// actionable plan-mode message as each blocked call's tool error.
func TestPlanModeRejectsMutatingTools(t *testing.T) {
	bash := &planCounterTool{name: "bash"}
	write := &planCounterTool{name: "write_file"}
	read := &planCounterTool{name: "read_file"}
	reg := NewRegistry()
	planRegister(t, reg, bash, write, read)

	planOn := true
	model := newScriptModel(func(call int) ([]*schema.Message, error) {
		switch call {
		case 0:
			return toolCall("bash", `{"cmd":"rm -rf /tmp/plan-mode-test"}`), nil
		case 1:
			return toolCall("write_file", `{"path":"a.txt","content":"x"}`), nil
		case 2:
			return toolCall("read_file", `{"path":"a.txt"}`), nil
		default:
			return finalText("done"), nil
		}
	})
	agent := newTestAgent(t, Config{
		einoModel:    model,
		Tools:        reg,
		PlanModeFunc: func() bool { return planOn },
	})

	events := runModeTurn(t, agent, "plan-session", "plan this change")
	ends := planToolEnds(events)

	if got := bash.calls.Load(); got != 0 {
		t.Fatalf("bash ran %d times in plan mode, want 0", got)
	}
	if got := write.calls.Load(); got != 0 {
		t.Fatalf("write_file ran %d times in plan mode, want 0", got)
	}
	if got := read.calls.Load(); got != 1 {
		t.Fatalf("read_file ran %d times in plan mode, want 1 (read-only must stay allowed)", got)
	}

	bashEnd, ok := ends["call-bash"]
	if !ok || !bashEnd.IsError {
		t.Fatalf("bash tool_end = %+v, want IsError", bashEnd)
	}
	writeEnd, ok := ends["call-write_file"]
	if !ok || !writeEnd.IsError {
		t.Fatalf("write_file tool_end = %+v, want IsError", writeEnd)
	}
	for name, end := range map[string]state.ToolEnd{"bash": bashEnd, "write_file": writeEnd} {
		if !strings.Contains(end.Result, "plan mode: submit a plan with the present tool and wait for user approval") {
			t.Fatalf("%s rejection = %q, want the plan-mode instruction", name, end.Result)
		}
		if !strings.Contains(end.Result, name) {
			t.Fatalf("%s rejection = %q, want the blocked tool name", name, end.Result)
		}
	}
	readEnd, ok := ends["call-read_file"]
	if !ok || readEnd.IsError {
		t.Fatalf("read_file tool_end = %+v, want a successful result", readEnd)
	}
	if strings.Contains(readEnd.Result, "plan mode") {
		t.Fatalf("read_file result = %q, plan mode should not annotate allowed tools", readEnd.Result)
	}

	// The block is a tool failure, not a failed turn: the model sees it and can
	// continue to present its plan.
	if phase := lastPhase(events); phase != state.PhaseIdle {
		t.Fatalf("turn phase = %v, want idle", phase)
	}
}

// TestPlanModeOffUnchanged proves the gate is inert when the resolver says off
// (and when it is nil): the same mutating tool runs normally with no plan-mode
// annotation.
func TestPlanModeOffUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func() bool
	}{
		{name: "no resolver", fn: nil},
		{name: "off", fn: func() bool { return false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bash := &planCounterTool{name: "bash"}
			reg := NewRegistry()
			planRegister(t, reg, bash)
			model := newScriptModel(func(call int) ([]*schema.Message, error) {
				if call == 0 {
					return toolCall("bash", `{"cmd":"echo ok"}`), nil
				}
				return finalText("done"), nil
			})
			agent := newTestAgent(t, Config{einoModel: model, Tools: reg, PlanModeFunc: tc.fn})
			events := runModeTurn(t, agent, "off-session", "run it")

			if got := bash.calls.Load(); got != 1 {
				t.Fatalf("bash calls with plan mode off = %d, want 1", got)
			}
			end, ok := planToolEnds(events)["call-bash"]
			if !ok {
				t.Fatalf("missing bash tool_end: %+v", events)
			}
			if end.IsError || strings.Contains(end.Result, "plan mode") {
				t.Fatalf("bash tool_end with plan mode off = %+v, want normal behavior", end)
			}
		})
	}
}

// TestPlanModePTCNestedCallsBlocked closes the batch bypass: a PTC batch is a
// single allowed outer call, but each nested mutating call must be rejected by
// the same plan-mode policy. Read-only nested calls still run.
func TestPlanModePTCNestedCallsBlocked(t *testing.T) {
	bash := &planCounterTool{name: "bash"}
	read := &planCounterTool{name: "read_file"}
	reg := NewRegistry()
	planRegister(t, reg, bash, read)

	model := newScriptModel(func(call int) ([]*schema.Message, error) {
		if call == 0 {
			return toolCall("ptc", `{"calls":[{"tool":"bash","args":{"cmd":"echo no"}},{"tool":"read_file","args":{"path":"a.txt"}}]}`), nil
		}
		return finalText("done"), nil
	})
	agent := newTestAgent(t, Config{
		einoModel:    model,
		Tools:        reg,
		Mode:         ModePTC,
		PlanModeFunc: func() bool { return true },
	})
	events := runModeTurn(t, agent, "ptc-plan-session", "batch")

	if got := bash.calls.Load(); got != 0 {
		t.Fatalf("nested bash ran %d times in plan mode, want 0", got)
	}
	if got := read.calls.Load(); got != 1 {
		t.Fatalf("nested read_file ran %d times in plan mode, want 1", got)
	}
	end, ok := planToolEnds(events)["call-ptc"]
	if !ok {
		t.Fatalf("missing ptc tool_end: %+v", events)
	}
	if end.IsError {
		t.Fatalf("ptc batch itself should complete; tool_end = %+v", end)
	}
	var response PTCResponse
	if err := json.Unmarshal([]byte(end.Result), &response); err != nil {
		t.Fatalf("decode ptc result %q: %v", end.Result, err)
	}
	if len(response.Results) != 2 {
		t.Fatalf("ptc results = %+v, want 2 entries", response.Results)
	}
	if response.Results[0].OK || !strings.Contains(response.Results[0].Error, "plan mode: submit a plan with the present tool") {
		t.Fatalf("nested bash result = %+v, want the plan-mode error", response.Results[0])
	}
	if !response.Results[1].OK {
		t.Fatalf("nested read_file result = %+v, want success", response.Results[1])
	}
}
