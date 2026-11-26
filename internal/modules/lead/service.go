// Package lead is the triage pass: the one place that decides what happens to a
// new inbox item. The planner is an interface on purpose — today a small rule
// set, later the Lead model — so the loop's guarantees (bounded pass, one
// failure never stops the others, handled items are never processed twice) hold
// whichever planner is plugged in.
package lead

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Rule is the contract rule type (kept as an alias so callers read naturally).
type Rule = contracts.LeadRule

// Planner decides one item. ok=false means "leave it for a human".
type Planner interface {
	Plan(ctx context.Context, item contracts.InboxItem) (contracts.TriageDecision, bool, error)
}

// RulePlanner is the deterministic planner (first matching rule wins).
type RulePlanner struct{ Rules []Rule }

// Plan implements Planner.
func (p RulePlanner) Plan(ctx context.Context, item contracts.InboxItem) (contracts.TriageDecision, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TriageDecision{}, false, err
	}
	haystack := item.Title + "\n" + item.Body
	for _, rule := range p.Rules {
		if rule.Source != "" && rule.Source != item.Source {
			continue
		}
		if rule.Contains != "" && !strings.Contains(haystack, rule.Contains) {
			continue
		}
		if rule.Ignore {
			return contracts.TriageDecision{Action: contracts.InboxActionIgnored, Note: "rule: ignore"}, true, nil
		}
		return contracts.TriageDecision{
			Action: contracts.InboxActionTask,
			Note:   "rule: dispatch",
			Task: &contracts.NewTask{
				Subject:  rule.SubjectPrefix + item.Title,
				Detail:   rule.DetailPrefix + item.Body,
				Assignee: rule.Assignee,
				// The envelope is the rules' own assignees: the assignee above is
				// always one of them, and declaring it keeps the contract the same
				// shape as the model planner's (triage validates both the same way).
				KnownAssignees: ruleAssignees(p.Rules),
			},
		}, true, nil
	}
	return contracts.TriageDecision{}, false, nil
}

// Config is the module's typed assembly config.
type Config struct {
	// MaxPerRun bounds one pass (0 = 20).
	MaxPerRun int `json:"maxPerRun"`
	// Now overrides the clock (tests).
	Now func() time.Time `json:"-"`
}

func (c Config) normalized() Config {
	if c.MaxPerRun <= 0 {
		c.MaxPerRun = 20
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Service implements contracts.LeadService.
type Service struct {
	inbox  contracts.InboxService
	triage contracts.TriageService
	cfg    Config

	// planner is the injected decision maker (nil = the rule planner built from
	// rules). A model-backed planner must be used as given, not rebuilt.
	planner Planner

	// mu guards rules: the settings UI may replace them while a pass runs.
	mu    sync.Mutex
	rules []Rule
	// path is the durable rule document (dir/rules.json); empty means in-memory
	// only (NewService, and unit tests that do not care about durability).
	path string
}

// NewService wires the pass.
func NewService(inboxService contracts.InboxService, triage contracts.TriageService, planner Planner, cfg Config) *Service {
	service := &Service{inbox: inboxService, triage: triage, cfg: cfg.normalized()}
	if rulePlanner, ok := planner.(RulePlanner); ok {
		// The rule planner's rules are editable through LeadRuleAdmin, so keep
		// them in one place; other planners are used exactly as injected.
		service.rules = append([]Rule(nil), rulePlanner.Rules...)
	} else if modelPlanner, ok := planner.(ModelPlanner); ok {
		// The model planner reads the LIVE rules: they are the prompt context it
		// decides with and the teammate envelope its assignee is checked against.
		// Without this it saw an empty rule set (the prompt said "no rules") while
		// still inventing assignees nobody could act as.
		modelPlanner.Rules = service.Rules
		service.planner = modelPlanner
	} else {
		service.planner = planner
	}
	return service
}

// Rules implements contracts.LeadRuleAdmin.
func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Rule(nil), s.rules...), nil
}

// SetRules implements contracts.LeadRuleAdmin.
func (s *Service) SetRules(ctx context.Context, rules []Rule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = append([]Rule(nil), rules...)
	// The caller's rule set is now the live one; failing to make it durable must
	// not silently look like success.
	return s.persistLocked()
}

// RunOnce implements contracts.LeadService.
func (s *Service) RunOnce(ctx context.Context) (contracts.LeadReport, error) {
	if err := ctx.Err(); err != nil {
		return contracts.LeadReport{}, err
	}
	items, err := s.inbox.List(ctx, contracts.InboxFilter{
		Statuses: []string{contracts.InboxStatusNew}, Limit: s.cfg.MaxPerRun,
	})
	if err != nil {
		return contracts.LeadReport{}, fmt.Errorf("lead: list inbox: %w", err)
	}

	report := contracts.LeadReport{Scanned: len(items)}
	s.mu.Lock()
	planner := s.planner
	if planner == nil {
		planner = RulePlanner{Rules: append([]Rule(nil), s.rules...)}
	}
	s.mu.Unlock()

	for _, item := range items {
		decision, ok, planErr := planner.Plan(ctx, item)
		if planErr != nil {
			report.Failed++
			report.Errors = append(report.Errors, fmt.Sprintf("%s: plan: %v", item.ID, planErr))
			continue
		}
		if !ok {
			report.Skipped++
			continue
		}
		result, applyErr := s.triage.Apply(ctx, item.ID, decision)
		if applyErr != nil {
			report.Failed++
			report.Errors = append(report.Errors, fmt.Sprintf("%s: apply: %v", item.ID, applyErr))
			continue
		}
		switch {
		case decision.Action == contracts.InboxActionIgnored:
			report.Ignored++
		case result.Dispatch != nil && result.Dispatch.Skipped != "":
			// The decision was made but no run started (no budget, the dispatch
			// ceiling, an assignee nobody can act as). Counting it as "dispatched"
			// would report work in flight that does not exist.
			report.Skipped++
		default:
			report.Dispatched++
		}
	}
	return report, nil
}

var _ contracts.LeadService = (*Service)(nil)
var _ contracts.LeadRuleAdmin = (*Service)(nil)
