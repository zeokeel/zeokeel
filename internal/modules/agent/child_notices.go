package agent

// Completion receipts for delegated children.
//
// DSH injects a "subagent finished" receipt into the Lead's inbox as a normal
// user message, so the parent model learns about asynchronous work on its next
// turn and the transcript shows what happened. ZeoKeel keeps the same semantics
// with the durable child-session store as the source of truth:
//
//   - the session UI derives quiet transcript rows from the finished child
//     records (internal/ui/session, ChatRoleNotice), so a reload shows the same
//     receipts;
//   - the next successful run of the parent session appends one user message per
//     finished, not-yet-notified child to the history it loads. The receipts are
//     staged while Rig loads history and committed only after the run succeeds
//     (a provider/stream failure or cancel keeps them pending for the retry),
//     and the record's Notified flag is persisted so a receipt reaches the model
//     exactly once.
//
// The receipt text is model-facing English (like the rest of the run guidance);
// the transcript copy is localized by the session UI from the same records.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// childNoticeRef identifies one finished child whose receipt is staged for the
// current run.
type childNoticeRef struct {
	id       string
	finished time.Time
}

// childNoticeBatch stages the receipts one run delivered to the model. commit
// persists their Notified flags only once the run succeeded.
type childNoticeBatch struct {
	service   *service
	sessionID string
	pending   []childNoticeRef
	loaded    bool
}

// newChildNoticeBatch returns the receipt batch of one run (nil for a service
// without a durable child-session store).
func newChildNoticeBatch(svc *service, sessionID string) *childNoticeBatch {
	if svc == nil || svc.childSessions == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return &childNoticeBatch{service: svc, sessionID: strings.TrimSpace(sessionID)}
}

// commit marks the staged receipts as notified (only successful runs call it).
func (b *childNoticeBatch) commit() {
	if b == nil || b.service == nil || b.service.childSessions == nil {
		return
	}
	for _, ref := range b.pending {
		b.service.childSessions.markNotified(ref.id, ref.finished)
	}
	b.pending = nil
}

// childNoticeHistory wraps the durable history of one run and appends the
// completion receipts of finished children before the model request is built.
type childNoticeHistory struct {
	inner rig.History
	batch *childNoticeBatch
}

var _ rig.History = (*childNoticeHistory)(nil)

// Load implements rig.History: the durable messages plus one receipt per
// finished, not-yet-notified child of this session.
func (h *childNoticeHistory) Load(ctx context.Context, sessionID string) ([]rig.Message, error) {
	messages, err := h.inner.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if h.batch.loaded {
		return messages, nil
	}
	h.batch.loaded = true
	notices, refs := h.batch.service.prepareChildNotices(sessionID)
	if len(notices) == 0 {
		return messages, nil
	}
	h.batch.pending = refs
	merged := make([]rig.Message, 0, len(messages)+len(notices))
	merged = append(merged, messages...)
	merged = append(merged, notices...)
	return merged, nil
}

// Append implements rig.History.
func (h *childNoticeHistory) Append(ctx context.Context, sessionID string, messages ...rig.Message) error {
	return h.inner.Append(ctx, sessionID, messages...)
}

// withChildNotices wraps a run's history with the child completion receipts. A
// nil history or batch keeps the history unchanged.
func withChildNotices(history rig.History, batch *childNoticeBatch) rig.History {
	if history == nil || batch == nil {
		return history
	}
	return &childNoticeHistory{inner: history, batch: batch}
}

// prepareChildNotices returns the receipts of this session's finished children
// without marking them; the run's batch commits the flags after success. It is
// called from the run's history Load, which happens on the run goroutine, never
// on the UI frame path.
func (s *service) prepareChildNotices(sessionID string) ([]rig.Message, []childNoticeRef) {
	if s == nil || s.childSessions == nil {
		return nil, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}
	type pendingNotice struct {
		finished time.Time
		message  rig.Message
		ref      childNoticeRef
	}
	var pending []pendingNotice
	for _, record := range s.childSessions.records() {
		if record.ParentSessionID != sessionID || record.Finished.IsZero() || record.Notified {
			continue
		}
		// A synchronous one-shot subagent's answer is already in the parent
		// history as the subagent tool result; only continuable teammate
		// completions need a separate model-facing receipt.
		if record.Kind != contracts.ChildAgentTeammate {
			continue
		}
		text := childNoticeTextForModel(record)
		if text == "" {
			continue
		}
		pending = append(pending, pendingNotice{
			finished: record.Finished,
			message: rig.Message{
				Role:    rig.RoleUser,
				Content: []rig.ContentPart{rig.TextPart(text)},
			},
			ref: childNoticeRef{id: record.ID, finished: record.Finished},
		})
	}
	sort.SliceStable(pending, func(i, j int) bool {
		return pending[i].finished.Before(pending[j].finished)
	})
	notices := make([]rig.Message, 0, len(pending))
	refs := make([]childNoticeRef, 0, len(pending))
	for _, notice := range pending {
		notices = append(notices, notice.message)
		refs = append(refs, notice.ref)
	}
	return notices, refs
}

// childNoticeTextForModel is the model-facing completion receipt of one child.
func childNoticeTextForModel(record childSessionRecord) string {
	if record.Kind != contracts.ChildAgentTeammate {
		return ""
	}
	kind := "teammate"
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = record.ID
	}
	name = `"` + name + `"`
	switch record.Status {
	case contracts.ChildAgentCompleted:
		text := fmt.Sprintf("Background %s %s finished and will do no further work unless you send it more.", kind, name)
		if output := strings.TrimSpace(record.Output); output != "" {
			text += "\nIts closing message: " + truncateTeamText(output, 800)
		}
		return text
	case contracts.ChildAgentFailed:
		detail := strings.TrimSpace(record.Error)
		if detail == "" {
			detail = "no error detail"
		}
		return fmt.Sprintf("Background %s %s failed: %s", kind, name, truncateTeamText(detail, 400))
	case contracts.ChildAgentInterrupted:
		return fmt.Sprintf("Background %s %s was interrupted; it will do no further work unless you send it more.", kind, name)
	default:
		return ""
	}
}
