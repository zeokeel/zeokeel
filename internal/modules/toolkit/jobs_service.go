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
	"regexp"
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

// jobListTailPattern matches the rig list format's numeric tail:
// " exit=<n> duration_ms=<n> command=<free text>". The status and command are
// free text, so the tail is located as the LAST such occurrence (the status
// can contain " exit=..." and the command everything after the real match).
var jobListTailPattern = regexp.MustCompile(` exit=(-?\d+) duration_ms=(\d+) command=`)

// parseJobListRow parses one "- id [kind] ..." body:
// "id [kind] owner=<o>? state=<s> status=<free text> exit=<n> duration_ms=<n>
// command=<free text>". Because status/command are free text, the numeric tail
// is found by its anchored pattern rather than by cutting from the left.
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
	stateAt := strings.Index(rest, " state=")
	if stateAt < 0 {
		return row, false
	}
	rest = rest[stateAt+len(" state="):]
	state, rest, ok := strings.Cut(rest, " status=")
	if !ok || state == "" {
		return row, false
	}
	matches := jobListTailPattern.FindAllStringSubmatchIndex(rest, -1)
	if len(matches) == 0 {
		return row, false
	}
	tail := matches[len(matches)-1]
	exitCode, err := strconv.Atoi(rest[tail[2]:tail[3]])
	if err != nil {
		return row, false
	}
	durationMS, err := strconv.ParseInt(rest[tail[4]:tail[5]], 10, 64)
	if err != nil {
		return row, false
	}
	row.ID = id
	row.Kind = kind
	row.State = state
	row.Status = rest[:tail[0]]
	row.ExitCode = exitCode
	row.Duration = time.Duration(durationMS) * time.Millisecond
	row.Command = rest[tail[1]:]
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
