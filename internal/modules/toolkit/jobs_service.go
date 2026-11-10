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
	"strconv"
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

// Jobs lists the manager's retained jobs, oldest first. The header chip is
// read-only chrome: a missing manager or a failed listing yields no rows rather
// than surfacing an error that would fail the session surface.
func (service *jobService) Jobs() []contracts.BackgroundJob {
	if service == nil || service.invoker == nil {
		return nil
	}
	result, err := service.invoker.invokeJobTool(context.Background(), jobs.ToolJobList, "{}")
	if err != nil || result.IsError {
		return nil
	}
	return parseJobList(result.Content)
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

// parseJobList decodes the stable text format emitted by Rig's job_list tool
// (tools/jobs/tools.go, listTool):
//
//	jobs: <n>
//	- <id> [<kind>] owner=<owner>? state=<state> status=<status> exit=<code> duration_ms=<ms> command=<command>
//
// The " owner=" segment is optional (Rig omits it for unowned jobs) and is not
// part of the UI read model. state/status/exit/duration_ms are single tokens up
// to the next fixed key; status is a DSH status word that may carry a detail in
// parentheses, and command is free text to the end of the line (with spaces,
// UTF-8 and Rig's 120-byte truncation ellipsis).
//
// Unparseable rows are skipped rather than failing the whole list so one odd
// line can never hide the rest of the manager's state from the header.
func parseJobList(text string) []contracts.BackgroundJob {
	rows := make([]contracts.BackgroundJob, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		row, ok := parseJobListRow(strings.TrimPrefix(line, "- "))
		if !ok {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	return rows
}

// parseJobListRow parses one "- id [kind] ..." body. Fields are cut in the
// order Rig writes them, so free-text values (status detail, command) cannot be
// mistaken for later keys.
func parseJobListRow(body string) (contracts.BackgroundJob, bool) {
	var row contracts.BackgroundJob
	id, rest, found := strings.Cut(body, " ")
	if !found || id == "" {
		return row, false
	}
	rest = strings.TrimPrefix(rest, "[")
	kind, rest, found := strings.Cut(rest, "]")
	if !found || kind == "" {
		return row, false
	}
	// The optional owner segment sits between the kind and state.
	if afterOwner, owned := strings.CutPrefix(rest, " owner="); owned {
		if _, afterState, ok := strings.Cut(afterOwner, " state="); ok {
			rest = afterState
		} else {
			return row, false
		}
	} else {
		var ok bool
		if rest, ok = strings.CutPrefix(rest, " state="); !ok {
			return row, false
		}
	}
	state, rest, ok := strings.Cut(rest, " status=")
	if !ok || state == "" {
		return row, false
	}
	status, rest, ok := strings.Cut(rest, " exit=")
	if !ok {
		return row, false
	}
	exit, rest, ok := strings.Cut(rest, " duration_ms=")
	if !ok {
		return row, false
	}
	exitCode, err := strconv.Atoi(exit)
	if err != nil {
		return row, false
	}
	duration, command, ok := strings.Cut(rest, " command=")
	if !ok {
		return row, false
	}
	durationMS, err := strconv.ParseInt(duration, 10, 64)
	if err != nil {
		return row, false
	}
	row.ID = id
	row.Kind = kind
	row.State = state
	row.Status = status
	row.ExitCode = exitCode
	row.Duration = time.Duration(durationMS) * time.Millisecond
	row.Command = command
	row.Running = state == "running"
	return row, true
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
