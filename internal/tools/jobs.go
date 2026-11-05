package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/tools/winenc"
)

// Background job tools.
//
// job_start launches "sh -c <command>" (cmd.exe on Windows) outside the tool
// call's lifetime and returns an id immediately. A manager owns the processes,
// their combined output ring, cancellation and status. job_output returns the
// retained output plus the current status; job_kill cancels the context and
// kills the whole process group.

const (
	// DefaultJobsMaxOutputBytes is the retained output cap per job.
	DefaultJobsMaxOutputBytes = 256 << 10

	// DefaultMaxJobs is the maximum number of concurrently running jobs.
	DefaultMaxJobs = 8

	jobKillWait     = 2 * time.Second
	jobPollInterval = 25 * time.Millisecond
)

// JobsConfig configures the background job tools.
type JobsConfig struct {
	// WorkspaceRoot is the directory jobs run in. Required.
	WorkspaceRoot string

	// MaxOutputBytes caps the retained output per job. Non-positive values use
	// DefaultJobsMaxOutputBytes.
	MaxOutputBytes int

	// MaxJobs caps concurrently running jobs. Non-positive values use
	// DefaultMaxJobs.
	MaxJobs int
}

// JobsManager owns the background jobs behind the four job_* tools. The app
// keeps one across agent rebuilds so a job started before a rebuild can still
// be read or killed.
type JobsManager struct{ m *jobManager }

// NewJobsManager builds a manager for cfg.
func NewJobsManager(cfg JobsConfig) *JobsManager {
	return &JobsManager{m: newJobManager(cfg)}
}

// Tools returns the four job tools bound to this manager.
func (jm *JobsManager) Tools() []runtime.Tool {
	if jm == nil || jm.m == nil {
		return nil
	}
	m := jm.m
	return []runtime.Tool{
		&jobStartTool{mgr: m},
		&jobListTool{mgr: m},
		&jobOutputTool{mgr: m},
		&jobKillTool{mgr: m},
	}
}

// Close cancels and reaps every retained job.
func (jm *JobsManager) Close() error {
	if jm == nil || jm.m == nil {
		return nil
	}
	for _, j := range jm.m.list() {
		j.mu.Lock()
		if j.status == jobRunning {
			j.killCause = "manager closed"
		}
		j.mu.Unlock()
		if j.ctx != nil {
			j.cancel()
		}
		select {
		case <-j.done:
		case <-time.After(jobKillWait):
		}
	}
	return nil
}

// NewJobsTools constructs job_start, job_list, job_output and job_kill with one
// shared manager. The constructor never fails: an invalid configuration is
// reported by each tool's Invoke call.
func NewJobsTools(cfg JobsConfig) []runtime.Tool {
	return NewJobsManager(cfg).Tools()
}

type jobStatus string

const (
	jobRunning jobStatus = "running"
	jobDone    jobStatus = "done"
	jobFailed  jobStatus = "failed"
	jobKilled  jobStatus = "killed"
)

type jobManager struct {
	mu     sync.Mutex
	sb     *sandbox
	root   string
	outCap int
	max    int
	jobs   map[string]*job
	order  []string
	// notify delivers a model-visible advisory when a job finishes (FP-15).
	// Nil disables notifications (tests and embedders without an agent).
	notify  func(string)
	nextID  int
	initErr error
}

func newJobManager(cfg JobsConfig) *jobManager {
	m := &jobManager{
		outCap: cfg.MaxOutputBytes,
		max:    cfg.MaxJobs,
		jobs:   make(map[string]*job),
	}
	if m.outCap <= 0 {
		m.outCap = DefaultJobsMaxOutputBytes
	}
	if m.max <= 0 {
		m.max = DefaultMaxJobs
	}
	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		m.initErr = fmt.Errorf("jobs: WorkspaceRoot is required")
		return m
	}
	sb, err := newSandbox(cfg.WorkspaceRoot)
	if err != nil {
		m.initErr = fmt.Errorf("jobs: %w", err)
		return m
	}
	m.sb = sb
	m.root = sb.root
	return m
}

type job struct {
	mgr     *jobManager
	id      string
	command string
	timeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	out    *outputRing
	done   chan struct{}

	mu        sync.Mutex
	status    jobStatus
	detail    string
	started   time.Time
	finished  time.Time
	exitCode  *int
	timedOut  bool
	killCause string
	timer     *time.Timer
}

func (m *jobManager) start(command string, timeout time.Duration) (*job, error) {
	m.mu.Lock()
	if m.initErr != nil {
		err := m.initErr
		m.mu.Unlock()
		return nil, err
	}
	running := 0
	for _, j := range m.jobs {
		if j.currentStatus() == jobRunning {
			running++
		}
	}
	if running >= m.max {
		m.mu.Unlock()
		return nil, fmt.Errorf("job_start: maximum of %d running jobs reached; wait for one to finish or kill it with job_kill", m.max)
	}
	m.pruneFinishedLocked()
	m.nextID++
	id := fmt.Sprintf("job-%d", m.nextID)

	jobCtx, cancel := context.WithCancel(context.Background())
	cmd := shellCommand(jobCtx, command)
	cmd.Dir = m.root
	cmd.Env = setEnv(scrubEnv(os.Environ()), "PWD", m.root)
	out := newOutputRing(m.outCap)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcessGroup(cmd.Process.Pid)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Unlock()
		return nil, fmt.Errorf("job_start: start command: %w", err)
	}
	j := &job{
		mgr:     m,
		id:      id,
		command: command,
		timeout: timeout,
		ctx:     jobCtx,
		cancel:  cancel,
		cmd:     cmd,
		out:     out,
		done:    make(chan struct{}),
		status:  jobRunning,
		started: time.Now(),
	}
	if timeout > 0 {
		j.timer = time.AfterFunc(timeout, func() { m.expire(j) })
	}
	m.jobs[id] = j
	m.order = append(m.order, id)
	m.mu.Unlock()

	go j.run()
	return j, nil
}

// pruneFinishedLocked bounds the retained job history so a long-lived manager
// does not grow without limit. Running jobs are never pruned.
func (m *jobManager) pruneFinishedLocked() {
	limit := m.max * 4
	if limit < 16 {
		limit = 16
	}
	for len(m.jobs) >= limit {
		removed := false
		for i, id := range m.order {
			j := m.jobs[id]
			if j != nil && j.currentStatus() != jobRunning {
				delete(m.jobs, id)
				m.order = append(m.order[:i], m.order[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			return
		}
	}
}

func (m *jobManager) get(id string) (*job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initErr != nil {
		return nil, m.initErr
	}
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("jobs: job %q not found", id)
	}
	return j, nil
}

func (m *jobManager) list() []*job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*job, 0, len(m.order))
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok {
			out = append(out, j)
		}
	}
	return out
}

// setNotify installs the model-visible completion-notification sink. The
// runtime binds it once through the job tools, so a finished job no longer
// depends on the model remembering to poll.
func (m *jobManager) setNotify(fn func(string)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.notify = fn
	m.mu.Unlock()
}

// notifyFinished emits the completion advisory for j. It is called exactly
// once, from the goroutine that reaps the process.
func (m *jobManager) notifyFinished(j *job) {
	if m == nil || j == nil {
		return
	}
	m.mu.Lock()
	notify := m.notify
	m.mu.Unlock()
	if notify == nil {
		return
	}
	notify(j.completionNotice())
}

// completionNotice renders the one-line model-facing advisory for a terminal
// job. It names the job id so the model can fetch the retained output with
// job_output instead of starting the same command again.
func (j *job) completionNotice() string {
	j.mu.Lock()
	status := j.status
	detail := j.detail
	id := j.id
	j.mu.Unlock()
	_, _, _, total := j.out.snapshot(0)
	return fmt.Sprintf(
		"Background job %s finished: %s (%s; output %d bytes). Read it with job_output id %q; do not start the same command again.",
		id, status, detail, total, id)
}

func (m *jobManager) expire(j *job) {
	j.mu.Lock()
	if j.status != jobRunning {
		j.mu.Unlock()
		return
	}
	j.timedOut = true
	pid := 0
	if j.cmd.Process != nil {
		pid = j.cmd.Process.Pid
	}
	j.mu.Unlock()

	j.cancel()
	if pid > 0 {
		_ = killProcessGroup(pid)
	}
}

func (j *job) run() {
	err := j.cmd.Wait()
	code := -1
	if err == nil {
		code = 0
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}

	j.mu.Lock()
	if j.timer != nil {
		j.timer.Stop()
	}
	j.exitCode = &code
	j.mu.Unlock()

	j.settle(err)
	close(j.done)
	j.mgr.notifyFinished(j)
}

// settle records the terminal state unless job_kill already did. Non-zero exit
// codes are reported as failures, while an explicit kill stays "killed".
func (j *job) settle(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	if j.finished.IsZero() {
		j.finished = now
	}
	if j.status != jobRunning {
		return
	}
	switch {
	case j.timedOut:
		j.status = jobFailed
		j.detail = fmt.Sprintf("timed out after %s", j.timeout)
	case err == nil:
		j.status = jobDone
		j.detail = "exit code 0"
	default:
		j.status = jobFailed
		if j.exitCode != nil && *j.exitCode >= 0 {
			j.detail = fmt.Sprintf("exit code %d", *j.exitCode)
		} else {
			j.detail = err.Error()
		}
	}
}

func (j *job) currentStatus() jobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

func (j *job) pid() int {
	if j.cmd.Process == nil {
		return 0
	}
	return j.cmd.Process.Pid
}

func (j *job) statusLine() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "[status: %s", j.status)
	switch j.status {
	case jobRunning:
		fmt.Fprintf(&b, ", pid=%d, elapsed=%s", j.pid(), time.Since(j.started).Round(time.Millisecond))
	default:
		if !j.finished.IsZero() {
			fmt.Fprintf(&b, ", duration=%s", j.finished.Sub(j.started).Round(time.Millisecond))
		}
		if j.detail != "" {
			fmt.Fprintf(&b, ", %s", j.detail)
		}
	}
	b.WriteByte(']')
	return b.String()
}

// kill cancels a running job and waits briefly for its process group to
// settle. requested reports whether this call performed the transition;
// previous is the status observed before the call.
func (j *job) kill(reason string) (requested bool, previous jobStatus, err error) {
	j.mu.Lock()
	if j.status != jobRunning {
		status := j.status
		j.mu.Unlock()
		return false, status, nil
	}
	previous = j.status
	j.status = jobKilled
	j.finished = time.Now()
	j.killCause = reason
	if reason == "" {
		j.detail = "killed"
	} else {
		j.detail = "killed: " + reason
	}
	pid := 0
	if j.cmd.Process != nil {
		pid = j.cmd.Process.Pid
	}
	j.mu.Unlock()

	j.cancel()
	if pid > 0 {
		_ = killProcessGroup(pid)
	}
	select {
	case <-j.done:
	case <-time.After(jobKillWait):
		if pid > 0 {
			_ = killProcessGroup(pid)
		}
	}
	return true, previous, nil
}

func (j *job) renderOutput() string {
	data, start, end, total := j.out.snapshot(0)
	var b strings.Builder
	if start > 0 {
		fmt.Fprintf(&b, "[output truncated: earliest retained output starts at byte %d]\n", start)
	}
	if len(data) == 0 {
		if j.currentStatus() == jobRunning {
			b.WriteString("(no output yet)")
		} else {
			b.WriteString("(no output)")
		}
	} else {
		// Decode before appending: Windows jobs run through cmd.exe, whose
		// built-ins emit the OEM code page (GBK on Chinese Windows).
		text := winenc.DecodeProcessOutput(data)
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "%s\n[bytes: %d-%d of %d]", j.statusLine(), start, end, total)
	return b.String()
}

const jobStartDescription = `Start a background shell command in the workspace root and return a job id immediately.

The command runs as "sh -c <command>" (cmd.exe on Windows) with ZEOKEEL_* and DEEPSEEK* environment variables removed. Its combined stdout and stderr are retained (newest bytes first dropped beyond the cap). The process group is killed on timeout or job_kill. The timeout is optional; when given it is capped at 300 seconds. Jobs do not inherit the tool call's context, so they keep running until they finish or are killed. Poll with job_output; do not start the same work twice.

Args JSON schema:
{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute."},"timeout_seconds":{"type":"integer","description":"Optional timeout in seconds (default none, maximum 300).","minimum":1,"maximum":300}},"required":["command"],"additionalProperties":false}`

type jobStartTool struct{ mgr *jobManager }

// SetAdvisorySink implements runtime.AdvisorySinkBinder: the runtime installs
// its external-notice sink so a finished job is reported to the model in-turn.
func (t *jobStartTool) SetAdvisorySink(sink runtime.AdvisorySink) {
	if t == nil || t.mgr == nil || sink == nil {
		return
	}
	t.mgr.setNotify(func(text string) { sink(text) })
}

func (t *jobStartTool) Name() string        { return "job_start" }
func (t *jobStartTool) Description() string { return jobStartDescription }

func (t *jobStartTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("job_start: command is required")
	}
	var timeout time.Duration
	if a.TimeoutSeconds > 0 {
		seconds := a.TimeoutSeconds
		if maxSeconds := int(maxBashTimeout / time.Second); seconds > maxSeconds {
			seconds = maxSeconds
		}
		timeout = time.Duration(seconds) * time.Second
	}
	j, err := t.mgr.start(a.Command, timeout)
	if err != nil {
		return "", err
	}
	suffix := ""
	if timeout > 0 {
		suffix = fmt.Sprintf(" timeout=%s", timeout)
	}
	return fmt.Sprintf("started background job %s (pid=%d)%s; read it with job_output {\"id\":%q}", j.id, j.pid(), suffix, j.id), nil
}

const jobListDescription = `List background jobs (running and finished) in creation order with their ids, statuses and commands.

Args JSON schema:
{"type":"object","properties":{},"additionalProperties":false}`

type jobListTool struct{ mgr *jobManager }

func (t *jobListTool) Name() string        { return "job_list" }
func (t *jobListTool) Description() string { return jobListDescription }

func (t *jobListTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	if err := decodeArgs(t.Name(), args, &struct{}{}); err != nil {
		return "", err
	}
	if t.mgr.initErr != nil {
		return "", t.mgr.initErr
	}
	jobs := t.mgr.list()
	if len(jobs) == 0 {
		return "(no background jobs)", nil
	}
	var b strings.Builder
	for i, j := range jobs {
		if i > 0 {
			b.WriteByte('\n')
		}
		command := j.command
		if len(command) > 100 {
			command = command[:100] + "..."
		}
		fmt.Fprintf(&b, "%s [%s] %s", j.id, j.currentStatus(), command)
	}
	return b.String(), nil
}

const jobOutputDescription = `Read a background job's retained output (up to the configured cap) and its current status. Reads are non-consuming: every call returns the retained output again.

Statuses are running, done, failed (spawn error, non-zero exit or timeout) and killed (job_kill).

Set "wait" to true to block until the job reaches a terminal status instead of
returning immediately. The wait is bounded: it defaults to 30 seconds and is
capped at 300 seconds from "timeout_seconds". There is no push notification
when a job finishes, so poll with this tool (or wait) rather than assuming one.

Args JSON schema:
{"type":"object","properties":{"id":{"type":"string","description":"Job id returned by job_start or job_list."},"wait":{"type":"boolean","description":"Block until the job finishes (default false)."},"timeout_seconds":{"type":"integer","description":"Maximum seconds to wait when wait is true (default 30, maximum 300).","minimum":1,"maximum":300}},"required":["id"],"additionalProperties":false}`

type jobOutputTool struct{ mgr *jobManager }

func (t *jobOutputTool) Name() string        { return "job_output" }
func (t *jobOutputTool) Description() string { return jobOutputDescription }

func (t *jobOutputTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID             string `json:"id"`
		Wait           bool   `json:"wait"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("job_output: id is required")
	}
	j, err := t.mgr.get(a.ID)
	if err != nil {
		return "", err
	}
	if a.Wait {
		timeout := 30 * time.Second
		if a.TimeoutSeconds > 0 {
			seconds := a.TimeoutSeconds
			if maxSeconds := int(maxBashTimeout / time.Second); seconds > maxSeconds {
				seconds = maxSeconds
			}
			timeout = time.Duration(seconds) * time.Second
		}
		if ctx == nil {
			ctx = context.Background()
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-j.done:
		case <-timer.C:
			return j.renderOutput() + fmt.Sprintf("\n[wait: timed out after %s; status: %s]", timeout, j.currentStatus()), nil
		case <-ctx.Done():
			return "", fmt.Errorf("job_output: %w", ctx.Err())
		}
	}
	return j.renderOutput(), nil
}

const jobKillDescription = `Request cancellation of a running background job and kill its process group. Finished jobs are left untouched.

Args JSON schema:
{"type":"object","properties":{"id":{"type":"string","description":"Job id returned by job_start or job_list."},"reason":{"type":"string","description":"Optional short reason recorded in the job status."}},"required":["id"],"additionalProperties":false}`

type jobKillTool struct{ mgr *jobManager }

func (t *jobKillTool) Name() string        { return "job_kill" }
func (t *jobKillTool) Description() string { return jobKillDescription }

func (t *jobKillTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ID) == "" {
		return "", fmt.Errorf("job_kill: id is required")
	}
	j, err := t.mgr.get(a.ID)
	if err != nil {
		return "", err
	}
	requested, previous, err := j.kill(a.Reason)
	if err != nil {
		return "", fmt.Errorf("job_kill: %w", err)
	}
	if requested {
		return fmt.Sprintf("requested cancellation of job %s %s", j.id, j.statusLine()), nil
	}
	return fmt.Sprintf("job %s had already finished [status: %s]", j.id, previous), nil
}
