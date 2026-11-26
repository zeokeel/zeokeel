package tasklease

// The review queue (M1): which tasks are waiting for a verdict right now.
//
// The audit loop polls this instead of subscribing to lease events, because the
// lease layer is durable and the process may restart between a submit and the
// verdict. "Who is in the review state" is the only question that survives a
// crash, and it is answerable from the same document the rest of the lease
// layer already writes.

import (
	"context"
	"sort"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// AwaitingReview implements contracts.TaskReviewQueue: every live lease whose
// state is review, sorted by task id so one sweep and the next see the same
// order (the audit loop's tests depend on it, and an operator diffing two
// reports should not see shuffling).
func (s *Service) AwaitingReview(ctx context.Context) ([]contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.leases))
	for id, lease := range s.leases {
		if lease.State == contracts.LeaseStateReview {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]contracts.TaskLease, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneLease(s.leases[id]))
	}
	return out, nil
}
