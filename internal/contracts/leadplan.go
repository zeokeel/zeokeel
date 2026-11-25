package contracts

import (
	"context"
	"errors"
)

// The planner's action vocabulary. It is deliberately narrower than the inbox
// vocabulary: a model may only create a task, drop the item as handled, or
// refuse to decide. It may never archive or silently rewrite an item.
const (
	// LeadActionTask turns the item into exactly one task.
	LeadActionTask = "task"
	// LeadActionIgnore marks the item handled without creating work (mapped to
	// InboxActionIgnored by the caller).
	LeadActionIgnore = "ignore"
	// LeadActionSkip leaves the item for a human: no opinion, no side effect.
	LeadActionSkip = "skip"
)

// PlanRequest is everything one planning round hands to the model. The rules
// are passed as data (the model follows them; it does not edit them), and
// OpenTasks is context that makes "this would duplicate existing work" visible.
type PlanRequest struct {
	Item      InboxItem
	Rules     []LeadRule
	OpenTasks int
}

// PlanDecision is the model's validated answer for one item. Fields are already
// trimmed and length-checked by the planner package; Action is one of the
// LeadAction constants. Subject is required for LeadActionTask only.
type PlanDecision struct {
	Action   string
	Subject  string
	Detail   string
	Assignee string
	// Note is one line of reasoning kept for the audit trail.
	Note string
}

// PlannerModel is the injected model call: prompt in, raw answer out. Keeping
// it this small is the point — the planner never imports a provider, so the
// same code runs against a real model, a fake, or no model at all.
type PlannerModel interface {
	Decide(ctx context.Context, prompt string) (string, error)
}

// The planner's typed failures. Callers tell them apart because the loop's
// reaction differs: a model failure is transient (retry later, leave the item
// new), an unparsable answer is a bad answer (same item, next pass), and an
// invalid decision is a well-formed answer that broke the rules.
var (
	// ErrLeadPlanModel is returned when the model call itself failed.
	ErrLeadPlanModel = errors.New("leadplan: model call failed")
	// ErrLeadPlanParse is returned when the answer contains no usable JSON
	// object.
	ErrLeadPlanParse = errors.New("leadplan: unusable model answer")
	// ErrLeadPlanInvalid is returned for valid JSON that is not a valid
	// decision (unknown action, missing subject, oversized field).
	ErrLeadPlanInvalid = errors.New("leadplan: invalid decision")
)
