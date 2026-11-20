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
	"encoding/json"
	"sort"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

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
	store executionEventLog
	live  executionLiveness
}

var _ contracts.ExecutionHistoryService = (*executionHistoryService)(nil)

// newExecutionHistoryService builds the read model over an optional store and an
// optional live resolver. Both may be nil: without a store the history is
// empty, and without live managers every unclosed record is interrupted.
func newExecutionHistoryService(store executionEventLog, live executionLiveness) *executionHistoryService {
	return &executionHistoryService{store: store, live: live}
}

// ExecutionHistory implements contracts.ExecutionHistoryService: the newest
// bounded records of one session, newest start first. A failed read yields no
// rows (header chrome never fails the screen) and a host without a store has no
// history to show.
func (s *executionHistoryService) ExecutionHistory(sessionID string, limit int) []contracts.ExecutionRecord {
	if s == nil || s.store == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	events, err := s.store.Last(context.Background(), sessionID, executionHistoryEvents)
	if err != nil {
		return nil
	}
	records := foldExecutionHistory(events, sessionID, s.live)
	if limit = executionHistoryLimit(limit); len(records) > limit {
		records = records[:limit]
	}
	return records
}

// executionHistoryLimit normalizes a caller's cap onto the read model's bounds.
func executionHistoryLimit(limit int) int {
	if limit <= 0 {
		return executionHistoryDefaultLimit
	}
	if limit > executionHistoryLimitMax {
		return executionHistoryLimitMax
	}
	return limit
}

// executionStartedPayload is the durable payload of job_started and
// terminal_opened (both write the routing details the UI can show).
type executionStartedPayload struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	CWD       string `json:"cwd"`
	Root      string `json:"root"`
	Mode      string `json:"mode"`
	Transport string `json:"transport"`
}

// executionEndedPayload is the durable payload of job_finished and
// terminal_closed (the terminal word, when the backend records one).
type executionEndedPayload struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// foldExecutionHistory pairs the durable start/end events per resource id,
// overlays the live managers and returns the records newest start first. A
// finish/close without its start in the bounded window is dropped (the record's
// beginning is unknowable), and an unreadable payload is skipped instead of
// failing the whole read.
func foldExecutionHistory(events []rig.SessionEvent, sessionID string, live executionLiveness) []contracts.ExecutionRecord {
	records := make([]contracts.ExecutionRecord, 0, 8)
	byID := make(map[string]int, 8)
	for _, event := range events {
		if event.SessionID != "" && event.SessionID != sessionID {
			continue
		}
		switch event.Type {
		case contracts.ExecutionJobStarted, contracts.ExecutionTerminalOpened:
			var payload executionStartedPayload
			if len(event.Payload) == 0 || json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			id := strings.TrimSpace(payload.ID)
			if id == "" {
				continue
			}
			if _, exists := byID[id]; exists {
				// One resource starts once; a duplicate start is a replay, not a
				// second record.
				continue
			}
			record := contracts.ExecutionRecord{
				ID:        id,
				SessionID: sessionID,
				Kind:      contracts.ExecutionKindJob,
				Command:   payload.Command,
				CWD:       payload.CWD,
				Root:      payload.Root,
				Mode:      payload.Mode,
				Transport: payload.Transport,
				Started:   event.Time,
			}
			if event.Type == contracts.ExecutionTerminalOpened {
				record.Kind = contracts.ExecutionKindTerminal
				record.Command = ""
			}
			byID[id] = len(records)
			records = append(records, record)
		case contracts.ExecutionJobFinished, contracts.ExecutionTerminalClosed:
			var payload executionEndedPayload
			if len(event.Payload) == 0 || json.Unmarshal(event.Payload, &payload) != nil {
				continue
			}
			index, ok := byID[strings.TrimSpace(payload.ID)]
			if !ok {
				// Orphan end (its start is outside the bounded window).
				continue
			}
			record := &records[index]
			if !record.Ended.IsZero() {
				// The first recorded end is the truth; a duplicate close is noise.
				continue
			}
			record.Ended = event.Time
			record.State = contracts.ExecutionStateFinished
			if event.Type == contracts.ExecutionJobFinished {
				record.Outcome = strings.TrimSpace(payload.State)
			} else {
				record.Outcome = "closed"
			}
		}
	}
	for index := range records {
		if records[index].State != "" {
			continue
		}
		if live != nil && live.running(records[index].Kind, records[index].ID) {
			records[index].State = contracts.ExecutionStateRunning
			continue
		}
		records[index].State = contracts.ExecutionStateInterrupted
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Started.Equal(records[j].Started) {
			return records[i].ID > records[j].ID
		}
		return records[i].Started.After(records[j].Started)
	})
	return records
}

// groupExecutionLiveness resolves live ownership from the retained root-scoped
// builds: a job is running when its merged job_list row says so, a terminal when
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
