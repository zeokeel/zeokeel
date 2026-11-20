package toolkit

// The session header's background-jobs chip (contracts.JobService) is backed by
// the root-scoped Rig job manager that lives inside each workspace build. Rig
// v0.13.2 exports the manager's job_* tools but no public job snapshot, so the
// read model is decoded from the stable text of the job_list tool and kill goes
// through job_kill (the tool-facing API the model itself uses).
//
// Every call resolves the build for the workspace root that is current at call
// time: a workspace switch builds a fresh manager and retires the old one, so
// caching a manager or one of its tools would freeze the chip on a dead root.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// jobToolInvoker invokes one background-job tool against the job manager of the
// workspace root that is current when the call runs. *rebindGroup implements it
// by dispatching through forRoot, exactly like the model-facing tool proxies.
type jobToolInvoker interface {
	invokeJobTool(ctx context.Context, name, arguments string) (rig.ToolResult, error)
}

// jobService is the toolkit's contracts.JobService implementation.
type jobService struct {
	invoker jobToolInvoker
}

var _ contracts.JobService = (*jobService)(nil)

// newJobService wraps the live root-scoped job tools in a contracts.JobService.
func newJobService(invoker jobToolInvoker) *jobService {
	return &jobService{invoker: invoker}
}

// staleJobLister is the rebind group's optional permission-downgrade read model
// (2026-11 audit RG-09). Test fakes of jobToolInvoker need not implement it.
type staleJobLister interface {
	stalePermissionJobs() []contracts.BackgroundJob
}

// The UI consumes StalePermissionJobs through the optional contracts interface
// (type assertion on KeyJobs), keeping JobService itself unchanged.
var _ contracts.JobPermissionReporter = (*jobService)(nil)

// StalePermissionJobs reports the running jobs that still follow an older
// permission identity after a permission/sandbox rebuild (count = len). The UI
// shows them as a disclosure hint with a kill entry; KillJob reaches them
// because job ids route to their owner build. A backend without the rebind
// group reports none. Downgrade non-retroactivity itself is an OS fact and is
// intentionally not changed here.
func (service *jobService) StalePermissionJobs() []contracts.BackgroundJob {
	if service == nil || service.invoker == nil {
		return nil
	}
	if lister, ok := service.invoker.(staleJobLister); ok {
		return lister.stalePermissionJobs()
	}
	return nil
}

// jobLister is the rebind group's typed job read model: the manager's own
// Snapshot(), never the job_list text. Test fakes of jobToolInvoker need not
// implement it.
type jobLister interface {
	listJobs() []contracts.BackgroundJob
}

// Jobs lists the manager's retained jobs, oldest first. The header chip is
// read-only chrome: a missing manager yields no rows rather than surfacing an
// error that would fail the session surface.
func (service *jobService) Jobs() []contracts.BackgroundJob {
	if service == nil || service.invoker == nil {
		return nil
	}
	if lister, ok := service.invoker.(jobLister); ok {
		return lister.listJobs()
	}
	return nil
}

// backgroundJobsFromSnapshots maps the manager's typed snapshots onto the UI
// read model. Kind comes from the job id's "<kind>-<hex>" shape, Running from
// the state, and Duration from Started/Finished (a running job counts up to
// now).
func backgroundJobsFromSnapshots(snapshots []jobs.JobSnapshot, now time.Time) []contracts.BackgroundJob {
	rows := make([]contracts.BackgroundJob, 0, len(snapshots))
	for _, snap := range snapshots {
		end := snap.Finished
		if end.IsZero() {
			end = now
		}
		var duration time.Duration
		if !snap.Started.IsZero() && end.After(snap.Started) {
			duration = end.Sub(snap.Started)
		}
		kind := snap.ID
		if index := strings.IndexByte(kind, '-'); index > 0 {
			kind = kind[:index]
		}
		rows = append(rows, contracts.BackgroundJob{
			ID:       snap.ID,
			Kind:     kind,
			Command:  snap.Command,
			State:    snap.State,
			Status:   snap.State,
			ExitCode: snap.ExitCode,
			Duration: duration,
			Running:  snap.State == "running",
		})
	}
	return rows
}

// KillJob stops a running job through the model-facing job_kill tool. Unknown
// ids fail with the tool's "unknown job" message; an already-finished job is
// reported as an error too (the header only offers kill on running rows, so a
// click landing after completion is a stale-row condition the caller shows).
func (service *jobService) KillJob(id string) error {
	if service == nil || service.invoker == nil {
		return errors.New("kill job: background jobs are unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("kill job: job id is required")
	}
	arguments, err := json.Marshal(struct {
		JobID string `json:"job_id"`
	}{JobID: id})
	if err != nil {
		return fmt.Errorf("kill job %q: encode arguments: %w", id, err)
	}
	result, invokeErr := service.invoker.invokeJobTool(context.Background(), jobs.ToolJobKill, string(arguments))
	if invokeErr != nil {
		return fmt.Errorf("kill job %q: %w", id, invokeErr)
	}
	if result.IsError {
		return fmt.Errorf("kill job %q: %s", id, strings.TrimSpace(result.Content))
	}
	if outcome := jobKillOutcome(result.Content); outcome == "already-finished" {
		return fmt.Errorf("kill job %q: job has already finished", id)
	}
	return nil
}

// jobOutputTailLines is how many trailing output lines one UI read asks for:
// the panel shows the newest output of a chatty job, not its whole history.
const jobOutputTailLines = 200

// JobOutput returns the newest output of one retained job through the
// model-facing job_output tool. The read asks for a tail rather than the
// default "new output since the last read", so it never consumes the model's
// output cursor; the returned body is bounded to
// contracts.JobOutputMaxBytes (newest bytes kept) and the contract's stable
// errors are wrapped for the UI to localize.
func (service *jobService) JobOutput(id string) (string, error) {
	if service == nil || service.invoker == nil {
		return "", contracts.ErrJobUnavailable
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("job output: job id is required")
	}
	arguments, err := json.Marshal(struct {
		JobID string `json:"job_id"`
		Tail  int    `json:"tail"`
	}{JobID: id, Tail: jobOutputTailLines})
	if err != nil {
		return "", fmt.Errorf("job output %q: encode arguments: %w", id, err)
	}
	result, invokeErr := service.invoker.invokeJobTool(context.Background(), jobs.ToolJobOutput, string(arguments))
	if invokeErr != nil {
		return "", fmt.Errorf("job output %q: %w", id, invokeErr)
	}
	if result.IsError {
		content := strings.TrimSpace(result.Content)
		if jobToolReportedUnknown(content) {
			return "", fmt.Errorf("%w: %s", contracts.ErrJobNotFound, content)
		}
		return "", fmt.Errorf("job output %q: %s", id, content)
	}
	return boundJobOutput(parseJobOutput(result.Content)), nil
}

// jobToolReportedUnknown reports the manager's unknown-job diagnostic, so the
// service can map it to the contract's stable sentinel.
func jobToolReportedUnknown(content string) bool {
	return strings.Contains(strings.ToLower(content), "unknown job")
}

// parseJobOutput extracts the output body from Rig's job_output text:
//
//	job_id: … / kind: … / command: … / state: … / status: … / exit_code: … /
//	duration_ms: … / output: <body> | output: (none)
//
// CRLF is normalized to LF, and CRLF-terminated output keeps its final newline.
func parseJobOutput(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	// The metadata values (command, label) are single-line free text, so the
	// first line that IS "output:" is the body's header — a command that ends
	// with the word "output:" cannot be mistaken for it.
	lines := strings.Split(normalized, "\n")
	for index, line := range lines {
		switch line {
		case "output: (none)":
			return ""
		case "output:":
			return strings.Join(lines[index+1:], "\n")
		}
	}
	return ""
}

// boundJobOutput keeps the newest contracts.JobOutputMaxBytes of one read and
// prefixes the contract's truncation marker when older bytes were dropped. The
// cut moves to a line boundary so the panel starts at a whole line (a single
// very long line has none within the budget and still cuts).
func boundJobOutput(text string) string {
	if len(text) <= contracts.JobOutputMaxBytes {
		return text
	}
	cut := text[len(text)-contracts.JobOutputMaxBytes:]
	// Start at the first whole line, unless one giant line (a minified build, a
	// progress bar without newlines) would eat most of the budget: then the new
	// bytes are the honest choice over an empty panel.
	if index := strings.IndexByte(cut, '\n'); index >= 0 &&
		len(cut)-index-1 >= contracts.JobOutputMaxBytes/2 {
		cut = cut[index+1:]
	}
	return contracts.JobOutputTruncatedMarker + cut
}

// jobKillOutcome returns the "outcome: …" word job_kill prefixes to its final
// state ("cancellation-requested", "already-finished"; empty when absent).
func jobKillOutcome(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if outcome, found := strings.CutPrefix(strings.TrimSpace(line), "outcome: "); found {
			return strings.TrimSpace(outcome)
		}
	}
	return ""
}
