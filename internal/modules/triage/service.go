// Package triage connects the two facts that must never disagree: "something
// arrived" (the inbox) and "someone is working on it" (the task board). It is
// the only path from an inbound event to work, so a botched write here would
// either lose a request or duplicate it.
package triage

import (
	"context"
	"fmt"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Service implements contracts.TriageService over an inbox and a task board.
type Service struct {
	inbox contracts.InboxService
	board contracts.TaskBoard
	// assigner is the optional owner half; boards without it still work
	// (unassigned tasks).
	assigner contracts.TaskAssigner
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
	if decision.Task.Assignee != "" && s.assigner != nil {
		if _, err := s.assigner.Assign(ctx, boardTask.ID, decision.Task.Assignee); err != nil {
			return contracts.TriageResult{}, fmt.Errorf("triage: assign task: %w", err)
		}
	}

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

var _ contracts.TriageService = (*Service)(nil)
