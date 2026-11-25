package leadplan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestParseDecisionTable(t *testing.T) {
	type parseCase struct {
		name    string
		raw     string
		want    contracts.PlanDecision
		wantErr error
	}

	tests := []parseCase{
		{
			name: "bare object",
			raw:  `{"action":"task","subject":"重开发票","detail":"抬头写错","assignee":"fin-ops","note":"客户要求"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "重开发票", Detail: "抬头写错", Assignee: "fin-ops", Note: "客户要求"},
		},
		{
			name: "fenced json",
			raw:  "```json\n{\"action\":\"ignore\",\"note\":\"广告\"}\n```",
			want: contracts.PlanDecision{Action: contracts.LeadActionIgnore, Note: "广告"},
		},
		{
			name: "fenced without language",
			raw:  "```\n{\"action\":\"skip\"}\n```",
			want: contracts.PlanDecision{Action: contracts.LeadActionSkip},
		},
		{
			name: "fenced inside prose",
			raw:  "Sure, here is my answer:\n```json\n{\"action\":\"task\",\"subject\":\"检查部署\"}\n```\nLet me know.",
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "检查部署"},
		},
		{
			name: "json wrapped in prose",
			raw:  "Here is the decision:\n{\"action\":\"task\",\"subject\":\"检查部署\",\"detail\":\"看日志\"}\nThanks!",
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "检查部署", Detail: "看日志"},
		},
		{
			name: "prose containing braces before the json",
			raw:  `Use {curly} braces like this: {"action":"ignore"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionIgnore},
		},
		{
			name: "nested object inside the decision",
			raw:  `{"action":"task","subject":"x","meta":{"k":{"n":1}}}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "x"},
		},
		{
			name: "extra keys are ignored",
			raw:  `{"action":"task","subject":"x","confidence":0.91,"tags":["a","b"]}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "x"},
		},
		{
			name: "whitespace is trimmed",
			raw:  "  {\"action\":\"task\",\"subject\":\"  hello  \",\"detail\":\"  d  \",\"assignee\":\"  bob  \",\"note\":\"  n  \"}\n",
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "hello", Detail: "d", Assignee: "bob", Note: "n"},
		},
		{
			name: "uppercase action is normalized",
			raw:  `{"action":" TASK ","subject":"x"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "x"},
		},
		{
			name: "ignore needs no subject",
			raw:  `{"action":"ignore"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionIgnore},
		},
		{
			name: "json with trailing text",
			raw:  `{"action":"ignore"} — done`,
			want: contracts.PlanDecision{Action: contracts.LeadActionIgnore},
		},
		{
			name:    "empty answer",
			raw:     "   \n\t ",
			wantErr: contracts.ErrLeadPlanParse,
		},
		{
			name:    "prose only",
			raw:     "I would create a task for this item.",
			wantErr: contracts.ErrLeadPlanParse,
		},
		{
			name:    "json array is not a decision",
			raw:     `[{"action":"task","subject":"x"}]`,
			wantErr: contracts.ErrLeadPlanParse,
		},
		{
			name:    "json string is not a decision",
			raw:     `"task"`,
			wantErr: contracts.ErrLeadPlanParse,
		},
		{
			name:    "unbalanced object",
			raw:     `{"action":"task","subject":"x"`,
			wantErr: contracts.ErrLeadPlanParse,
		},
		{
			name:    "wrong field type",
			raw:     `{"action":"task","subject":42}`,
			wantErr: contracts.ErrLeadPlanParse,
		},
		{
			name:    "unknown action",
			raw:     `{"action":"dispatch","subject":"x"}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
		{
			name:    "missing action",
			raw:     `{"subject":"x"}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
		{
			name:    "task without subject",
			raw:     `{"action":"task","detail":"no subject here"}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
		{
			name:    "task with blank subject",
			raw:     `{"action":"task","subject":"   "}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
	}

	// Caps are measured in runes, not bytes: 200 CJK characters are 600 bytes.
	tests = append(tests,
		parseCase{
			name: "subject at the cap",
			raw:  `{"action":"task","subject":"` + strings.Repeat("长", maxSubjectRunes) + `"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: strings.Repeat("长", maxSubjectRunes)},
		},
		parseCase{
			name:    "subject over the cap",
			raw:     `{"action":"task","subject":"` + strings.Repeat("长", maxSubjectRunes+1) + `"}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
		parseCase{
			name: "detail at the cap",
			raw:  `{"action":"task","subject":"x","detail":"` + strings.Repeat("a", maxDetailRunes) + `"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "x", Detail: strings.Repeat("a", maxDetailRunes)},
		},
		parseCase{
			name:    "detail over the cap",
			raw:     `{"action":"task","subject":"x","detail":"` + strings.Repeat("a", maxDetailRunes+1) + `"}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
		parseCase{
			name: "assignee at the cap",
			raw:  `{"action":"task","subject":"x","assignee":"` + strings.Repeat("b", maxAssigneeRunes) + `"}`,
			want: contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "x", Assignee: strings.Repeat("b", maxAssigneeRunes)},
		},
		parseCase{
			name:    "assignee over the cap",
			raw:     `{"action":"task","subject":"x","assignee":"` + strings.Repeat("b", maxAssigneeRunes+1) + `"}`,
			wantErr: contracts.ErrLeadPlanInvalid,
		},
	)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDecision(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if got != (contracts.PlanDecision{}) {
					t.Errorf("a rejected answer must not return a decision, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("decision = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseDecisionErrorsAreTyped(t *testing.T) {
	_, err := ParseDecision("nonsense")
	if err == nil || !errors.Is(err, contracts.ErrLeadPlanParse) {
		t.Fatalf("want ErrLeadPlanParse, got %v", err)
	}
	_, err = ParseDecision(`{"action":"nope"}`)
	if err == nil || !errors.Is(err, contracts.ErrLeadPlanInvalid) {
		t.Fatalf("want ErrLeadPlanInvalid, got %v", err)
	}
	if errors.Is(err, contracts.ErrLeadPlanParse) {
		t.Fatalf("an invalid decision must not be reported as a parse failure: %v", err)
	}
}

// fakeModel is the injected model seam: it records the prompt it was given and
// answers with canned text (or an error).
type fakeModel struct {
	answer string
	err    error
	prompt string
	calls  int
}

func (f *fakeModel) Decide(ctx context.Context, prompt string) (string, error) {
	f.calls++
	f.prompt = prompt
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

var _ contracts.PlannerModel = (*fakeModel)(nil)

func TestDecideTask(t *testing.T) {
	model := &fakeModel{answer: `{"action":"task","subject":"重开发票","detail":"抬头写错","assignee":"fin-ops"}`}
	decision, ok, err := Decide(context.Background(), model, testItem(), testRules(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("a task decision must be applied (ok=true)")
	}
	want := contracts.PlanDecision{Action: contracts.LeadActionTask, Subject: "重开发票", Detail: "抬头写错", Assignee: "fin-ops"}
	if decision != want {
		t.Errorf("decision = %+v, want %+v", decision, want)
	}
	if model.calls != 1 {
		t.Errorf("model calls = %d, want 1", model.calls)
	}
	if !strings.Contains(model.prompt, testItem().Title) {
		t.Error("Decide must hand the model a prompt built from the item")
	}
}

func TestDecideIgnore(t *testing.T) {
	model := &fakeModel{answer: `{"action":"ignore","note":"广告推广"}`}
	decision, ok, err := Decide(context.Background(), model, testItem(), testRules(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("ignore is a decision and must be applied (ok=true)")
	}
	if decision.Action != contracts.LeadActionIgnore || decision.Note != "广告推广" {
		t.Errorf("decision = %+v", decision)
	}
}

func TestDecideSkip(t *testing.T) {
	model := &fakeModel{answer: `{"action":"skip","note":"信息不足"}`}
	decision, ok, err := Decide(context.Background(), model, testItem(), testRules(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("skip means no side effect: ok must be false")
	}
	if decision.Action != contracts.LeadActionSkip {
		t.Errorf("the decision is still returned for the audit trail, got %+v", decision)
	}
}

func TestDecideUnparsableAnswer(t *testing.T) {
	model := &fakeModel{answer: "I think this is probably spam."}
	_, ok, err := Decide(context.Background(), model, testItem(), testRules(), 0)
	if !errors.Is(err, contracts.ErrLeadPlanParse) {
		t.Fatalf("want ErrLeadPlanParse, got %v", err)
	}
	if ok {
		t.Fatal("a garbage answer must not be applied")
	}
}

func TestDecideInvalidAnswer(t *testing.T) {
	model := &fakeModel{answer: `{"action":"task"}`}
	_, ok, err := Decide(context.Background(), model, testItem(), testRules(), 0)
	if !errors.Is(err, contracts.ErrLeadPlanInvalid) {
		t.Fatalf("want ErrLeadPlanInvalid, got %v", err)
	}
	if ok {
		t.Fatal("an invalid decision must not be applied")
	}
}

func TestDecideModelErrorIsWrapped(t *testing.T) {
	boom := errors.New("provider timeout")
	model := &fakeModel{err: boom}
	_, ok, err := Decide(context.Background(), model, testItem(), testRules(), 0)
	if !errors.Is(err, contracts.ErrLeadPlanModel) {
		t.Fatalf("want ErrLeadPlanModel, got %v", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("the provider error must stay in the chain, got %v", err)
	}
	if ok {
		t.Fatal("a failed model call must not be applied")
	}
}

func TestDecideNilModel(t *testing.T) {
	_, ok, err := Decide(context.Background(), nil, testItem(), testRules(), 0)
	if !errors.Is(err, contracts.ErrLeadPlanModel) {
		t.Fatalf("want ErrLeadPlanModel for a nil model, got %v", err)
	}
	if ok {
		t.Fatal("ok must be false when no model is wired")
	}
}

func TestDecideCancelledContextDoesNotCallModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	model := &fakeModel{answer: `{"action":"ignore"}`}
	_, ok, err := Decide(ctx, model, testItem(), testRules(), 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if ok {
		t.Fatal("ok must be false on a cancelled pass")
	}
	if model.calls != 0 {
		t.Errorf("model calls = %d, want 0", model.calls)
	}
}
