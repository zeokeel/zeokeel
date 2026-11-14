package contracts

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
