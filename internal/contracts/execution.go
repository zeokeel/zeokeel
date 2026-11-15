package contracts

import (
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Execution lifecycle durable events.
//
// The toolkit records the lifecycle of long-lived execution resources (Rig
// background jobs and model-facing terminal sessions) into the durable session
// event log when a store is wired. They make the runtime observable to later
// audits (which session started which job/terminal, with which workspace root
// and policy); they never carry full process output, and a closed app leaves the
// last event as the final known state (a missing finish event means the end is
// unknown — interrupted — not that the process is still running).
const (
	// ExecutionJobStarted is appended when job_start succeeds.
	ExecutionJobStarted = "execution/job_started"
	// ExecutionJobFinished is appended when a settled job's completion notice is
	// drained. A session killed with the app therefore has no finish event.
	ExecutionJobFinished = "execution/job_finished"
	// ExecutionTerminalOpened is appended when terminal_open succeeds.
	ExecutionTerminalOpened = "execution/terminal_opened"
	// ExecutionTerminalClosed is appended when terminal_close succeeds.
	ExecutionTerminalClosed = "execution/terminal_closed"
)

// The execution-history read model
// ---------------------------------
//
// The durable events above are written by the toolkit and read back here as one
// bounded record per resource, so the session header can show what ran in a
// conversation after an app restart. The events never carry process output: a
// record describes the lifecycle only, and the UI says so instead of implying
// the output is still available.

// Execution kinds (the two resources with a durable lifecycle).
const (
	// ExecutionKindJob is a background job (job_start).
	ExecutionKindJob = "job"
	// ExecutionKindTerminal is a model-facing terminal session (terminal_open).
	ExecutionKindTerminal = "terminal"
)

// Execution states: the lifecycle as folded from the durable events, with the
// in-process managers overlaying a truthful running state.
const (
	// ExecutionStateRunning is a resource the live manager still owns.
	ExecutionStateRunning = "running"
	// ExecutionStateFinished has a recorded end event (job_finished or
	// terminal_closed).
	ExecutionStateFinished = "finished"
	// ExecutionStateInterrupted started but has no recorded end and no live
	// owner: the app exited, or the process ended without the record being
	// observed. It is not a failure, and it is not evidence the process still
	// runs.
	ExecutionStateInterrupted = "interrupted"
)

// ExecutionRecord is one folded execution lifecycle record of a session.
type ExecutionRecord struct {
	// ID is the job_id or terminal session_id.
	ID string
	// SessionID is the conversation whose tool call started the resource.
	SessionID string
	// Kind is one of the ExecutionKind* values.
	Kind string
	// Command is the job's command line ("" for terminals).
	Command string
	// CWD is the working directory the resource started in (may be empty).
	CWD string
	// Root is the workspace root the tool call was bound to.
	Root string
	// Mode is the sandbox mode of that build (workspace-write, read-only, ...).
	Mode string
	// Transport is the terminal transport (pty/pipe; "" for jobs).
	Transport string
	// Started is the start event's time.
	Started time.Time
	// Ended is the end event's time; zero when the end was not recorded.
	Ended time.Time
	// State is one of the ExecutionState* values.
	State string
	// Outcome is the backend's terminal word (job: exited/killed/failed/
	// timed_out; terminal: closed). Empty while running or interrupted.
	Outcome string
}

// Duration returns the observed lifetime of the record: Ended-Started for a
// finished record, and zero when the end was not recorded (an interrupted
// record's duration is unknown, so the UI shows an honest dash) or the clock
// went backwards.
func (r ExecutionRecord) Duration() time.Duration {
	if r.Started.IsZero() || r.Ended.IsZero() {
		return 0
	}
	if span := r.Ended.Sub(r.Started); span > 0 {
		return span
	}
	return 0
}

// ExecutionHistoryService is the optional read model of a conversation's
// durable execution lifecycle records (the session header's background-jobs
// overlay reads it). Implementations must never synthesize output: the durable
// events only carry lifecycle metadata.
type ExecutionHistoryService interface {
	// ExecutionHistory returns the session's records, newest start first,
	// bounded by limit (limit <= 0 uses the implementation's default). A read
	// failure or a host without a store yields no rows.
	ExecutionHistory(sessionID string, limit int) []ExecutionRecord
}

// KeyExecutionHistory is provided by the toolkit module and consumed by the
// session header. It is optional: a host without it simply shows live jobs.
var KeyExecutionHistory = cordis.NewServiceKey[ExecutionHistoryService]("zeokeel/execution-history")
