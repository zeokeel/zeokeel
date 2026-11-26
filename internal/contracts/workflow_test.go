package contracts

// The workflow definition's contract (M2): normalization, validation and the two
// orderings the runner depends on.
//
// These are pure functions on purpose. The runner's correctness (which stage may
// start now) is decided here, in code with no clock, no store and no chat
// service; the module only asks the questions.

import (
	"errors"
	"strings"
	"testing"
)

func validPipeline() Workflow {
	return Workflow{
		ID:   "release-checklist",
		Name: "发布检查",
		Stages: []WorkflowStage{
			{ID: "collect", Prompt: "收集本次变更", Acceptance: "列出所有改动文件"},
			{ID: "test", Prompt: "跑测试", Acceptance: "全部通过", DependsOn: []string{"collect"}},
			{ID: "report", Prompt: "写报告", DependsOn: []string{"collect"}},
			{ID: "ship", Prompt: "合并", DependsOn: []string{"test", "report"}},
		},
	}
}

func TestNormalizeWorkflowFillsDefaults(t *testing.T) {
	wf := NormalizeWorkflow(Workflow{
		ID:   "  wf-1 ",
		Name: "  名字 ",
		Stages: []WorkflowStage{
			{ID: " step-a ", DependsOn: []string{" ", ""}},
		},
	})
	if wf.ID != "wf-1" || wf.Name != "名字" {
		t.Fatalf("workflow = %+v, want trimmed id and name", wf)
	}
	stage := wf.Stages[0]
	if stage.ID != "step-a" {
		t.Fatalf("stage id = %q, want step-a", stage.ID)
	}
	if stage.Name != "step-a" {
		t.Fatalf("stage name = %q, want the id as the default label", stage.Name)
	}
	if stage.Role != PresetExecutorID {
		t.Fatalf("stage role = %q, want executor by default", stage.Role)
	}
	if len(stage.DependsOn) != 0 {
		t.Fatalf("stage deps = %v, want blank entries dropped", stage.DependsOn)
	}
}

func TestValidateWorkflowAcceptsAPipeline(t *testing.T) {
	wf := NormalizeWorkflow(validPipeline())
	if err := ValidateWorkflow(wf); err != nil {
		t.Fatalf("ValidateWorkflow: %v", err)
	}
}

func TestValidateWorkflowRejectsBrokenDocuments(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Workflow)
		want error
	}{
		{"no id", func(wf *Workflow) { wf.ID = "" }, ErrWorkflowInvalid},
		{"bad id", func(wf *Workflow) { wf.ID = "Bad Id" }, ErrWorkflowInvalid},
		{"no name", func(wf *Workflow) { wf.Name = "" }, ErrWorkflowInvalid},
		{"no stages", func(wf *Workflow) { wf.Stages = nil }, ErrWorkflowInvalid},
		{"stage without id", func(wf *Workflow) { wf.Stages[0].ID = "" }, ErrWorkflowInvalid},
		{"duplicate stage", func(wf *Workflow) { wf.Stages[1].ID = "collect" }, ErrWorkflowInvalid},
		{"unknown dependency", func(wf *Workflow) { wf.Stages[1].DependsOn = []string{"nope"} }, ErrWorkflowInvalid},
		{"self dependency", func(wf *Workflow) { wf.Stages[1].DependsOn = []string{"test"} }, ErrWorkflowInvalid},
		{"cycle", func(wf *Workflow) { wf.Stages[0].DependsOn = []string{"ship"} }, ErrWorkflowCycle},
		{"bad role", func(wf *Workflow) { wf.Stages[0].Role = "Executor!" }, ErrWorkflowInvalid},
		{"overlong prompt", func(wf *Workflow) { wf.Stages[0].Prompt = strings.Repeat("x", MaxWorkflowPromptLen+1) }, ErrWorkflowInvalid},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			wf := NormalizeWorkflow(validPipeline())
			test.edit(&wf)
			err := ValidateWorkflow(wf)
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

// TestValidateWorkflowNamesTheField: the message is surfaced to a person or a
// model, so "invalid" alone is not an answer.
func TestValidateWorkflowNamesTheField(t *testing.T) {
	wf := NormalizeWorkflow(validPipeline())
	wf.Stages[1].DependsOn = []string{"nope"}
	err := ValidateWorkflow(wf)
	if err == nil || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "test") {
		t.Fatalf("err = %v, want the stage and the unknown dependency named", err)
	}
}

func TestStageOrderIsTopologicalAndStable(t *testing.T) {
	wf := NormalizeWorkflow(validPipeline())
	order, err := StageOrder(wf)
	if err != nil {
		t.Fatalf("StageOrder: %v", err)
	}
	position := make(map[string]int, len(order))
	for i, id := range order {
		position[id] = i
	}
	// collect before test and report; both before ship.
	if position["collect"] > position["test"] || position["collect"] > position["report"] {
		t.Fatalf("order = %v, want collect first", order)
	}
	if position["test"] > position["ship"] || position["report"] > position["ship"] {
		t.Fatalf("order = %v, want ship last", order)
	}
	// Declaration order breaks ties: test is declared before report.
	if position["test"] > position["report"] {
		t.Fatalf("order = %v, want declaration order inside one layer", order)
	}
	// Repeating the call gives the same answer (a report must be diffable).
	again, err := StageOrder(wf)
	if err != nil {
		t.Fatalf("StageOrder twice: %v", err)
	}
	if strings.Join(order, ",") != strings.Join(again, ",") {
		t.Fatalf("order changed between calls: %v then %v", order, again)
	}
}

func TestReadyStagesRespectsDependencies(t *testing.T) {
	wf := NormalizeWorkflow(validPipeline())

	ready := ReadyStages(wf, map[string]bool{})
	if len(ready) != 1 || ready[0].ID != "collect" {
		t.Fatalf("ready = %+v, want only collect", ready)
	}

	ready = ReadyStages(wf, map[string]bool{"collect": true})
	ids := []string{ready[0].ID, ready[1].ID}
	if len(ready) != 2 || strings.Join(ids, ",") != "test,report" {
		t.Fatalf("ready = %+v, want test and report", ready)
	}

	ready = ReadyStages(wf, map[string]bool{"collect": true, "test": true, "report": true})
	if len(ready) != 1 || ready[0].ID != "ship" {
		t.Fatalf("ready = %+v, want only ship", ready)
	}

	ready = ReadyStages(wf, map[string]bool{"collect": true, "test": true, "report": true, "ship": true})
	if len(ready) != 0 {
		t.Fatalf("ready = %+v, want none when everything is done", ready)
	}
}
