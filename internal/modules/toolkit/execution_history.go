package toolkit

// Durable execution-history read model (contracts.KeyExecutionHistory).
//
// The execution routing already records the lifecycle of background jobs and
// terminal sessions as durable events (execution/job_started, job_finished,
// terminal_opened, terminal_closed) for the session that invoked the tool. This
// file folds those events back into one bounded record per resource so the
// session header can show what ran in a conversation after an app restart —
// when the live managers (and their output) are gone.
//
// The read is bounded (the newest executionHistoryEvents events of the session)
// and truthful: a start without a recorded end is "interrupted" (the end was
// not observed), and the live managers overlay "running" only while they still
// own the resource. Output is never synthesized: the durable events do not
// carry it, and the UI labels the group accordingly.

import (
	"context"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	rigexecution "github.com/zeokeel/zeokeel-rig/session/execution"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// executionHistoryEvents bounds the durable event window folded per read
	// (matching the product's other event folds). Older execution records fall
	// out of the window instead of the UI reading an unbounded log.
	executionHistoryEvents = 4000
	// executionHistoryDefaultLimit is the record cap when a caller passes 0.
	executionHistoryDefaultLimit = 50
	// executionHistoryLimitMax caps a caller's limit so the overlay stays a
	// compact list.
	executionHistoryLimitMax = 200
)

// executionEventLog is the slice of contracts.Store this read model needs, so a
// unit test can fold a scripted log without a database.
type executionEventLog interface {
	Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error)
}

// executionLiveness reports whether the in-process manager still owns one
// resource; the durable fold asks it only for records without an end event.
type executionLiveness interface {
	running(kind, id string) bool
}

// executionHistoryService is the toolkit's contracts.ExecutionHistoryService.
type executionHistoryService struct {
	inner *rigexecution.Service
}

var _ contracts.ExecutionHistoryService = (*executionHistoryService)(nil)

// newExecutionHistoryService builds the read model over an optional store and an
// optional live resolver. Both may be nil.
func newExecutionHistoryService(store executionEventLog, live executionLiveness) *executionHistoryService {
	var log rigexecution.EventLog
	if store != nil {
		log = store
	}
	return &executionHistoryService{inner: rigexecution.NewService(log, livenessAdapter{live: live})}
}

// ExecutionHistory implements contracts.ExecutionHistoryService: the newest
// bounded records of one session, newest start first.
func (s *executionHistoryService) ExecutionHistory(sessionID string, limit int) []contracts.ExecutionRecord {
	if s == nil || s.inner == nil {
		return nil
	}
	records := s.inner.History(sessionID, limit)
	out := make([]contracts.ExecutionRecord, 0, len(records))
	for _, record := range records {
		out = append(out, contracts.ExecutionRecord{
			ID:        record.ID,
			SessionID: record.SessionID,
			Kind:      record.Kind,
			Command:   record.Command,
			CWD:       record.CWD,
			Root:      record.Root,
			Mode:      record.Mode,
			Transport: record.Transport,
			Started:   record.Started,
			Ended:     record.Ended,
			State:     record.State,
			Outcome:   record.Outcome,
		})
	}
	return out
}

// livenessAdapter bridges the toolkit's live resolver to the SDK interface.
type livenessAdapter struct{ live executionLiveness }

func (a livenessAdapter) Running(kind, id string) bool {
	if a.live == nil {
		return false
	}
	return a.live.running(kind, id)
}

// groupExecutionLiveness resolves live ownership from the retained root-scoped
// builds: a job is running when its manager's snapshot says so, a terminal when
// its merged terminal_list block is still running.
type groupExecutionLiveness struct {
	group *rebindGroup
}

// running implements executionLiveness.
func (l groupExecutionLiveness) running(kind, id string) bool {
	if l.group == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	ctx := context.Background()
	switch kind {
	case contracts.ExecutionKindJob:
		return l.group.runningJob(ctx, id)
	case contracts.ExecutionKindTerminal:
		result, err := l.group.mergeTerminalLists(ctx)
		if err != nil || result.IsError {
			return false
		}
		return executionListHasRunningTerminal(result.Content, id)
	default:
		return false
	}
}

// executionListHasRunningTerminal reports whether one session id's block in the
// merged terminal_list output is still running.
func executionListHasRunningTerminal(listText, id string) bool {
	for _, block := range strings.Split(listText, "\n\n") {
		sessionID := ""
		running := false
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if value, found := strings.CutPrefix(line, "session_id: "); found {
				sessionID = strings.TrimSpace(value)
			}
			if line == "running: true" {
				running = true
			}
		}
		if sessionID == id {
			return running
		}
	}
	return false
}
