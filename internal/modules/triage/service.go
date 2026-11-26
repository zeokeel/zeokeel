// Package triage connects the two facts that must never disagree: "something
// arrived" (the inbox) and "someone is working on it" (the task board). It is
// the only path from an inbound event to work, so a botched write here would
// either lose a request or duplicate it.
package triage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Service implements contracts.TriageService over an inbox and a task board.
type Service struct {
	inbox contracts.InboxService
	board contracts.TaskBoard
	// assigner is the optional owner half; boards without it still work
	// (unassigned tasks).
	assigner contracts.TaskAssigner
	// dispatcher is the optional wake-up half (dispatch.go): a task assigned to
	// the lead role actually starts a run instead of only recording an owner.
	dispatcher *Dispatcher
}

// NewService wires the triage service. board may also implement
// contracts.TaskAssigner; when it does, an Assignee on the decision is applied.
func NewService(inboxService contracts.InboxService, board contracts.TaskBoard) *Service {
	service := &Service{inbox: inboxService, board: board}
	if assigner, ok := board.(contracts.TaskAssigner); ok {
		service.assigner = assigner
	}
	return service
}

// WithDispatcher attaches the wake-up connector and returns the service (chain
// style, so wiring stays one expression). Without it a dispatch keeps the
// historical behaviour: the owner string is written and nothing runs.
func (s *Service) WithDispatcher(dispatcher *Dispatcher) *Service {
	s.dispatcher = dispatcher
	return s
}

// Apply implements contracts.TriageService.
//
// Order matters: the board write happens first, and the inbox is only marked
// once the task exists. A failed board write therefore leaves the item in
// "new" for the next triage pass instead of silently dropping the request.
func (s *Service) Apply(ctx context.Context, inboxID string, decision contracts.TriageDecision) (contracts.TriageResult, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TriageResult{}, err
	}

	item, err := s.inbox.Get(ctx, inboxID)
	if err != nil {
		return contracts.TriageResult{}, err
	}

	switch decision.Action {
	case contracts.InboxActionTask:
		return s.applyTask(ctx, item, decision)
	case contracts.InboxActionTriaged, contracts.InboxActionIgnored, contracts.InboxActionArchived:
		if _, err := s.inbox.Triage(ctx, []string{item.ID}, decision.Action, nil); err != nil {
			return contracts.TriageResult{}, err
		}
		updated, err := s.inbox.Get(ctx, item.ID)
		if err != nil {
			return contracts.TriageResult{}, err
		}
		return contracts.TriageResult{Inbox: updated}, nil
	default:
		return contracts.TriageResult{}, contracts.ErrInboxUnknownAction
	}
}

func (s *Service) applyTask(ctx context.Context, item contracts.InboxItem, decision contracts.TriageDecision) (contracts.TriageResult, error) {
	if decision.Task == nil || decision.Task.Subject == "" {
		return contracts.TriageResult{}, contracts.ErrTriageInvalid
	}
	// Triage twice must not create a second task: if the item already became a
	// task, report the existing ids unchanged.
	if item.Status == contracts.InboxStatusTask && len(item.TaskIDs) > 0 {
		return contracts.TriageResult{Inbox: item, TaskIDs: item.TaskIDs, Created: false}, nil
	}

	boardTask, err := s.board.Create(ctx, decision.Task.Subject, decision.Task.Detail)
	if err != nil {
		return contracts.TriageResult{}, fmt.Errorf("triage: create task: %w", err)
	}
	assignee := strings.TrimSpace(decision.Task.Assignee)
	// Assignee validation (2026-11-26 audit, 中高): the model picks this name, and
	// nothing checked it — an invented name was written to the board as an owner,
	// where the UI and the audit read it as "someone is on this".
	//
	// The check is only active when the caller DECLARED its teammates
	// (NewTask.KnownAssignees): rules name teammates that are not roles ("bot-1"),
	// so a caller that does not say who its teammates are would see legitimate
	// rule-derived assignees refused. The shipping planner always declares them
	// (lead.ModelPlanner fills the envelope from the same rules it prompts with),
	// so the model's invented names are refused in the product while a direct
	// caller keeps the historical behaviour.
	unknownAssignee := ""
	if assignee != "" && len(decision.Task.KnownAssignees) > 0 {
		known := knownAssignees(decision.Task.KnownAssignees)
		if !known.has(assignee) {
			unknownAssignee = fmt.Sprintf(
				"assignee %q 不在已知队友名单内（%s）；任务已建，未写 owner、未派单",
				assignee, strings.Join(known.names(), ", "))
		}
	}
	if unknownAssignee != "" {
		result, err := s.finishTask(ctx, item, boardTask)
		if err != nil {
			return contracts.TriageResult{}, err
		}
		result.Dispatch = &contracts.TriageDispatch{TaskID: boardTask.ID, Skipped: unknownAssignee}
		return result, nil
	}
	if assignee != "" && s.assigner != nil {
		if _, err := s.assigner.Assign(ctx, boardTask.ID, assignee); err != nil {
			return contracts.TriageResult{}, fmt.Errorf("triage: assign task: %w", err)
		}
	}

	result, err := s.finishTask(ctx, item, boardTask)
	if err != nil {
		return contracts.TriageResult{}, err
	}
	// Wake the lead LAST: the task exists, the owner is recorded and the inbox
	// is marked, so a wake-up that fails cannot leave a half-dispatched inbox
	// item (and cannot make triage look like it lost the request).
	if s.dispatcher.Wants(assignee) {
		result.Dispatch = s.dispatcher.Wake(ctx, boardTask, decision.Task.Subject, decision.Task.Detail)
	}
	return result, nil
}

// finishTask marks the inbox item as turned into the created task and returns the
// result. It is the shared tail of every task decision, including the one that
// deliberately does not dispatch: the board write and the inbox state must agree
// whatever the dispatch decided.
func (s *Service) finishTask(ctx context.Context, item contracts.InboxItem, boardTask contracts.BoardTask) (contracts.TriageResult, error) {
	taskIDs := []string{boardTask.ID}
	if _, err := s.inbox.Triage(ctx, []string{item.ID}, contracts.InboxActionTask, taskIDs); err != nil {
		return contracts.TriageResult{}, err
	}
	updated, err := s.inbox.Get(ctx, item.ID)
	if err != nil {
		return contracts.TriageResult{}, err
	}
	return contracts.TriageResult{Inbox: updated, TaskIDs: taskIDs, Created: true}, nil
}

// knownAssigneeSet is the set of assignee names one decision may use: the
// caller's envelope plus the built-in roles (always, so an envelope-less caller
// can still dispatch to a role).
type knownAssigneeSet map[string]bool

// knownAssignees builds the set case-insensitively; empty names are ignored.
func knownAssignees(envelope []string) knownAssigneeSet {
	set := knownAssigneeSet{}
	for _, role := range []string{contracts.PresetLeadID, contracts.PresetExecutorID, contracts.PresetAuditorID} {
		set[strings.ToLower(role)] = true
	}
	for _, name := range envelope {
		if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

// has reports whether name is in the set (case-insensitive, trimmed).
func (s knownAssigneeSet) has(name string) bool {
	return s[strings.ToLower(strings.TrimSpace(name))]
}

// names lists the set's members in sorted order (the readable half of a skip
// reason: "here is who you could have named").
func (s knownAssigneeSet) names() []string {
	out := make([]string, 0, len(s))
	for name := range s {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

var _ contracts.TriageService = (*Service)(nil)
