package tasklease

// Held sends (approval=before_send): the outbound half of the approval gate.

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Hold implements contracts.TaskSendHolder.
func (s *Service) Hold(ctx context.Context, taskID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return contracts.ErrLeaseInvalid
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: held text is empty", contracts.ErrLeaseInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heldSends == nil {
		s.heldSends = make(map[string]string)
	}
	s.heldSends[taskID] = text
	return s.persistLocked()
}

// Held implements contracts.TaskSendHolder.
func (s *Service) Held(ctx context.Context, taskID string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	text, ok := s.heldSends[strings.TrimSpace(taskID)]
	return text, ok, nil
}

// ApproveSend implements contracts.TaskSendHolder: the text leaves once.
func (s *Service) ApproveSend(ctx context.Context, taskID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	taskID = strings.TrimSpace(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	text, ok := s.heldSends[taskID]
	if !ok {
		return "", contracts.ErrHeldSendUnknown
	}
	delete(s.heldSends, taskID)
	if err := s.persistLocked(); err != nil {
		// Put it back: a message nobody approved must not disappear because the
		// store failed.
		s.heldSends[taskID] = text
		return "", err
	}
	return text, nil
}

var _ contracts.TaskSendHolder = (*Service)(nil)
