package lead

// ModelPlanner is the bridge between the triage pass and a model: it asks
// leadplan for a prompt, hands it to an injected model, and maps the parsed
// decision onto the pass's vocabulary. The pass still owns every guarantee
// (bounded, one failure never stops the others, handled items are not new).

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/leadplan"
)

// ModelPlanner implements Planner over a contracts.PlannerModel.
type ModelPlanner struct {
	// Model is the injected completion endpoint.
	Model contracts.PlannerModel
	// Rules supplies the current rules for prompt context (may be nil).
	Rules func(ctx context.Context) ([]contracts.LeadRule, error)
	// OpenTasks reports how much work is already in flight (may be nil).
	OpenTasks func(ctx context.Context) (int, error)
}

// Plan implements Planner.
func (p ModelPlanner) Plan(ctx context.Context, item contracts.InboxItem) (contracts.TriageDecision, bool, error) {
	if p.Model == nil {
		return contracts.TriageDecision{}, false, fmt.Errorf("%w: no model configured", contracts.ErrLeadPlanModel)
	}
	var rules []contracts.LeadRule
	if p.Rules != nil {
		loaded, err := p.Rules(ctx)
		if err != nil {
			return contracts.TriageDecision{}, false, err
		}
		rules = loaded
	}
	openTasks := 0
	if p.OpenTasks != nil {
		loaded, err := p.OpenTasks(ctx)
		if err != nil {
			return contracts.TriageDecision{}, false, err
		}
		openTasks = loaded
	}

	decision, ok, err := leadplan.Decide(ctx, p.Model, item, rules, openTasks)
	if err != nil || !ok {
		return contracts.TriageDecision{}, false, err
	}
	switch decision.Action {
	case contracts.LeadActionTask:
		return contracts.TriageDecision{
			Action: contracts.InboxActionTask,
			Note:   decision.Note,
			Task: &contracts.NewTask{
				Subject: decision.Subject, Detail: decision.Detail, Assignee: decision.Assignee,
				// The teammate envelope: the model was told "assignee: a teammate
				// name taken from the rules", so the rules ARE the set a real
				// assignee can come from. Declaring it lets triage refuse a name the
				// model invented instead of writing it to the board as an owner
				// (2026-11-26 audit, 中高: the assignee was never validated).
				KnownAssignees: ruleAssignees(rules),
			},
		}, true, nil
	case contracts.LeadActionIgnore:
		return contracts.TriageDecision{Action: contracts.InboxActionIgnored, Note: decision.Note}, true, nil
	default:
		return contracts.TriageDecision{}, false, nil
	}
}

// ruleAssignees lists the teammate names the current rules name — the set the
// model may assign to, and the envelope triage enforces.
func ruleAssignees(rules []contracts.LeadRule) []string {
	seen := make(map[string]bool, len(rules))
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		name := strings.TrimSpace(rule.Assignee)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

var _ Planner = ModelPlanner{}
