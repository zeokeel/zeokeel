package agent

// Bounded, race-safe read model of the synchronous subagent delegations
// (contracts.Subagents.Run: the subagent / subagent_fork tools). The agent
// service implements contracts.SubagentActivityLister over it so the
// inspector's Agents pane can show a delegation while it runs (运行中) and
// after it finishes (完成/失败/已取消). The registry keeps only snapshots:
// nothing here aliases the read model or the Rig run.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The inspector discovers the activity read model through this optional
// interface on the provided KeySubagents service.
var _ contracts.SubagentActivityLister = (*service)(nil)

const (
	// subagentActivityFinishedKeep bounds the finished rows kept for the
	// inspector: the newest finished delegations stay, older ones are pruned.
	// Running rows are never pruned.
	subagentActivityFinishedKeep = 8
	// subagentActivityPromptRunes bounds the stored prompt summary.
	subagentActivityPromptRunes = 240
	// subagentActivityDetailRunes bounds the stored final answer/error text, so
	// a long answer cannot grow the registry without bound.
	subagentActivityDetailRunes = 800
)

// subagentActivityRegistry keeps the tracked delegations in start order, oldest
// first. The caller (the service) never hands out the internal slice.
type subagentActivityRegistry struct {
	mu      sync.Mutex
	nextSeq uint64
	entries []contracts.SubagentActivity
}

// start registers one running delegation and returns its snapshot copy (the
// caller needs the generated ID to finish it). Started is set here, so callers
// never have to know the clock. Pruning runs after the append: a burst of
// finished rows cannot push a running one out.
func (r *subagentActivityRegistry) start(activity contracts.SubagentActivity) contracts.SubagentActivity {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq++
	activity.ID = fmt.Sprintf("subagent-%d", r.nextSeq)
	activity.Status = contracts.SubagentStatusRunning
	activity.Started = time.Now()
	activity.Finished = time.Time{}
	activity.Prompt = collapseActivityText(activity.Prompt, subagentActivityPromptRunes)
	r.entries = append(r.entries, activity)
	r.pruneLocked()
	return activity
}

// entry returns a copy of one tracked delegation ("" ID when unknown), for the
// durable history archive.
func (r *subagentActivityRegistry) entry(id string) (contracts.SubagentActivity, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return contracts.SubagentActivity{}, false
}

// finish records the outcome of one delegation. A running row only: a duplicate
// finish (defensive) leaves the first recorded outcome, and an unknown ID is
// ignored. The detail is bounded and only set for the finished row.
func (r *subagentActivityRegistry) finish(id, status, detail string) {
	if strings.TrimSpace(status) == contracts.SubagentStatusRunning {
		status = contracts.SubagentStatusDone
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.entries {
		entry := &r.entries[index]
		if entry.ID != id {
			continue
		}
		if entry.Status != contracts.SubagentStatusRunning {
			return
		}
		entry.Status = status
		entry.Finished = time.Now()
		entry.Detail = collapseActivityText(detail, subagentActivityDetailRunes)
		break
	}
	r.pruneLocked()
}

// pruneLocked drops the oldest finished rows until at most
// subagentActivityFinishedKeep remain. Running rows are kept in any case. The
// caller holds r.mu.
func (r *subagentActivityRegistry) pruneLocked() {
	finished := 0
	for _, entry := range r.entries {
		if entry.Status != contracts.SubagentStatusRunning {
			finished++
		}
	}
	if finished <= subagentActivityFinishedKeep {
		return
	}
	drop := finished - subagentActivityFinishedKeep
	kept := make([]contracts.SubagentActivity, 0, len(r.entries)-drop)
	for _, entry := range r.entries {
		if drop > 0 && entry.Status != contracts.SubagentStatusRunning {
			drop--
			continue
		}
		kept = append(kept, entry)
	}
	r.entries = kept
}

// snapshot returns a copy of every tracked delegation, oldest first. The
// returned structs and slice are the caller's; the registry is never aliased.
func (r *subagentActivityRegistry) snapshot() []contracts.SubagentActivity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == 0 {
		return nil
	}
	out := make([]contracts.SubagentActivity, len(r.entries))
	copy(out, r.entries)
	return out
}

// SubagentActivities implements contracts.SubagentActivityLister: the current
// synchronous delegations, oldest first, as private copies. It is nil-safe for
// embedders that build the service without a registry.
func (s *service) SubagentActivities() []contracts.SubagentActivity {
	if s == nil {
		return nil
	}
	return s.subagentActivities.snapshot()
}

// beginSubagentActivity registers one delegation on the service registry and
// returns its activity (ID included). It is the single place the delegation
// lifecycle is captured, so Run's error paths all show up the same way.
func (s *service) beginSubagentActivity(name, sessionID, model, prompt string, fork bool) contracts.SubagentActivity {
	activity := s.subagentActivities.start(contracts.SubagentActivity{
		Name:      strings.TrimSpace(name),
		SessionID: strings.TrimSpace(sessionID),
		Model:     strings.TrimSpace(model),
		Prompt:    prompt,
		Fork:      fork,
	})
	// The child session exists from the start (running), so a restart mid-run
	// still shows the interrupted child with its stable identity.
	if s.childSessions != nil {
		record := childSessionFromSubagentActivity(activity, s.childSessions.newID())
		record.Status = contracts.ChildAgentRunning
		s.childSessions.upsert(record)
	}
	return activity
}

// finishSubagentActivity records the outcome and the bounded detail of one
// delegation.
func (s *service) finishSubagentActivity(id string, ctx context.Context, runErr error, result contracts.SubagentResult) {
	status, detail := subagentActivityOutcome(ctx, runErr, result)
	s.subagentActivities.finish(id, status, detail)
	// Update the durable child-session record: the stable ID is already the row
	// identity the subagent chip will restore after a restart.
	if s.childSessions != nil {
		if entry, ok := s.subagentActivities.entry(id); ok {
			if record, found := s.childSessions.byBackendRef(entry.ID, entry.SessionID, contracts.ChildAgentSubagent); found {
				updated := childSessionFromSubagentActivity(entry, record.ID)
				s.childSessions.upsert(updated)
			} else {
				s.childSessions.upsert(childSessionFromSubagentActivity(entry, s.childSessions.newID()))
			}
		}
	}
}

// subagentActivityOutcome maps one Run return to the registry status and its
// bounded detail: a cancelled context is a cancellation, never a generic
// error, and a success keeps the child's final answer.
func subagentActivityOutcome(ctx context.Context, runErr error, result contracts.SubagentResult) (status, detail string) {
	if ctx != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return contracts.SubagentStatusCancelled, ""
		}
		return contracts.SubagentStatusError, ctx.Err().Error()
	}
	if runErr != nil {
		return contracts.SubagentStatusError, runErr.Error()
	}
	return contracts.SubagentStatusDone, result.Text
}

// collapseActivityText bounds one stored text to limit runes, collapsing
// whitespace so a multi-line task or answer stays one readable summary.
func collapseActivityText(text string, limit int) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	runes := []rune(collapsed)
	if len(runes) <= limit {
		return collapsed
	}
	return string(runes[:limit]) + "…"
}
