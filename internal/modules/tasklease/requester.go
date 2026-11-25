package tasklease

// Who asked for a task: attribution stored beside the other task-scoped rows.

import (
	"context"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SetRequestedBy implements contracts.TaskRequester.
func (s *Service) SetRequestedBy(ctx context.Context, taskID, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	taskID = strings.TrimSpace(taskID)
	userID = strings.TrimSpace(userID)
	if taskID == "" || userID == "" {
		return contracts.ErrTaskRequesterInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requesters == nil {
		s.requesters = make(map[string]string)
	}
	s.requesters[taskID] = userID
	return s.persistLocked()
}

// RequestedBy implements contracts.TaskRequester.
func (s *Service) RequestedBy(ctx context.Context, taskID string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	userID, ok := s.requesters[strings.TrimSpace(taskID)]
	return userID, ok, nil
}

var _ contracts.TaskRequester = (*Service)(nil)
