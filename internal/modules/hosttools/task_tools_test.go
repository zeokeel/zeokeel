package hosttools

// task_tools (M3b): the lead's half of "assign the tools this task may use".
//
// The enforcement already exists — a session bound to a task may only call the
// tools that task's binding names — but nothing could WRITE that binding except
// the browser. A lead that dispatches work must be able to say "this task may use
// these tools", or the executor inherits whatever its role allows and the
// assignment is a promise nobody made.

import (
	"context"
	"errors"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// toolsLease extends fakeLease with the binding write.
type toolsLease struct {
	fakeLease
	setTask   string
	setTools  []string
	setErr    error
	cleared   bool
	lastCalls int
}

func (f *toolsLease) SetTools(_ context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	if f.setErr != nil {
		return contracts.TaskTools{}, f.setErr
	}
	f.setTask = taskID
	f.setTools = append([]string(nil), tools...)
	f.lastCalls++
	if len(tools) == 0 {
		f.cleared = true
	}
	return contracts.TaskTools{TaskID: taskID, Tools: tools}, nil
}

func taskToolSet(lease *toolsLease, binder fakeBinder) map[string]rig.Tool {
	out := map[string]rig.Tool{}
	for _, tool := range newTaskTools(taskDeps{
		lease:  func() (taskLeaseService, bool) { return lease, lease != nil },
		binder: func() (taskBinder, bool) { return binder, binder.task != "" },
	}) {
		out[tool.Name()] = tool
	}
	return out
}

func TestTaskToolsBindsTheNamedTools(t *testing.T) {
	lease := &toolsLease{fakeLease: fakeLease{held: &contracts.TaskLease{TaskID: "task-1", Owner: "lead", State: contracts.LeaseStateClaimed}}}
	tools := taskToolSet(lease, fakeBinder{task: "task-1"})

	result := invoke(t, tools[ToolTaskTools], "s1", `{"taskId":"task-1","tools":["read_file","apply_patch","bash"]}`)
	if result.IsError {
		t.Fatalf("task_tools errored: %q", result.Content)
	}
	if lease.setTask != "task-1" || len(lease.setTools) != 3 {
		t.Fatalf("binding = %q %v, want task-1 with three tools", lease.setTask, lease.setTools)
	}
	if !strings.Contains(result.Content, "3") {
		t.Fatalf("content = %q, want the count", result.Content)
	}
}

// Assigning the tools of a task the lead is bound to needs no explicit id: the
// binding row already answers "which task".
func TestTaskToolsDefaultsToTheBoundTask(t *testing.T) {
	lease := &toolsLease{fakeLease: fakeLease{held: &contracts.TaskLease{TaskID: "task-1", Owner: "lead", State: contracts.LeaseStateClaimed}}}
	tools := taskToolSet(lease, fakeBinder{task: "task-1"})
	if result := invoke(t, tools[ToolTaskTools], "s1", `{"tools":["read_file"]}`); result.IsError {
		t.Fatalf("task_tools errored: %q", result.Content)
	}
	if lease.setTask != "task-1" || len(lease.setTools) != 1 {
		t.Fatalf("binding = %q %v, want the bound task", lease.setTask, lease.setTools)
	}
}

func TestTaskToolsRefusesAnEmptyBindingSilently(t *testing.T) {
	lease := &toolsLease{}
	tools := taskToolSet(lease, fakeBinder{task: "task-1"})
	// An empty list would mean "no binding", which is wider than what the lead
	// said. The tool refuses instead of widening.
	result := invoke(t, tools[ToolTaskTools], "s1", `{"taskId":"task-1","tools":[]}`)
	if !result.IsError || lease.lastCalls != 0 {
		t.Fatalf("result=%+v calls=%d, want a refusal and no write", result, lease.lastCalls)
	}
}

func TestTaskToolsReportsABadName(t *testing.T) {
	lease := &toolsLease{setErr: contracts.ErrTaskToolsInvalid}
	tools := taskToolSet(lease, fakeBinder{task: "task-1"})
	result := invoke(t, tools[ToolTaskTools], "s1", `{"taskId":"task-1","tools":["Bad Name!"]}`)
	if !result.IsError || !strings.Contains(result.Content, "task_tools") {
		t.Fatalf("result = %+v, want a readable error", result)
	}
}

func TestTaskToolsNeedsATask(t *testing.T) {
	lease := &toolsLease{}
	tools := taskToolSet(lease, fakeBinder{}) // unbound session, no taskId in args
	result := invoke(t, tools[ToolTaskTools], "s1", `{"tools":["read_file"]}`)
	if !result.IsError || lease.lastCalls != 0 {
		t.Fatalf("result=%+v calls=%d, want a refusal without a task", result, lease.lastCalls)
	}
	_ = errors.Is
}
