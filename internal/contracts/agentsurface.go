// Agent-surface read models shared by the shell: child agents (subagents and
// team members), background jobs and the right sidebar's preview entry points.
//
// The DSH-aligned session shell presents subagents and team members as child
// conversations of the active session: same header breadcrumb, content area
// switch, parent crumb to return. The backend keeps its existing runners; this
// file is the single read model the session UI consumes so the two agent kinds
// render through one code path (P2 unification at the presentation layer) and
// can later gain durable child sessions without changing the UI again.
package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ChildAgentKind distinguishes the two child-agent families.
const (
	// ChildAgentSubagent is a synchronous delegation from the subagent tool.
	ChildAgentSubagent = "subagent"
	// ChildAgentTeammate is a durable team member (spawn_teammate).
	ChildAgentTeammate = "teammate"
)

// ChildAgentMode is the follow-up capability of a child agent.
const (
	ChildAgentOneShot     = "one-shot"
	ChildAgentContinuable = "continuable"
)

// ChildAgentStatus is the run state shown in the child header and switcher.
const (
	ChildAgentRunning     = "running"
	ChildAgentIdle        = "idle"
	ChildAgentCompleted   = "completed"
	ChildAgentFailed      = "failed"
	ChildAgentInterrupted = "interrupted"
)

// ChildAgentActivity is one bounded timeline entry of a child agent (tool
// activity, messages, spawn/finish events).
type ChildAgentActivity struct {
	ID    string
	Kind  string
	Text  string
	Time  time.Time
	Error bool
}

// ChildAgent is the read-only conversation view of one subagent or teammate.
type ChildAgent struct {
	ID              string
	ParentSessionID string
	Name            string
	Kind            string
	Mode            string
	Status          string
	Model           string
	// Prompt is the bounded task the parent handed over.
	Prompt string
	// Output is the latest finished answer ("" while running or after failure).
	Output string
	// Error is the latest failure text ("" when the latest run did not fail).
	Error    string
	Started  time.Time
	Finished time.Time
	// UsageTokens/Iterations accumulate the completed runs.
	UsageTokens int
	Iterations  int
	// Activity is the bounded recent timeline, oldest first.
	Activity []ChildAgentActivity
	// Historical marks a child restored from the durable child-agent history:
	// its run is over and the backend no longer owns a live subagent for it.
	Historical bool
	// Resumable reports that a historical child can still be continued: a
	// follow-up message cold-resumes it from its durable child session (the
	// backend rebuilds the run from the stored history). Live rows report false.
	Resumable bool
}

// ChildAgentService is the read/control surface the session header uses for the
// subagent chip and the child conversation view. ParentSessionID scopes the
// list; empty means the active session. Finished children restored from the
// durable history arrive as Historical rows, scoped to the session that ran
// them and read-only (no follow-up can be delivered).
type ChildAgentService interface {
	// ChildAgents lists the child agents of one parent session, oldest first.
	ChildAgents(parentSessionID string) []ChildAgent
	// ChildAgent returns one child agent by ID.
	ChildAgent(id string) (ChildAgent, bool)
	// SendChildAgent delivers a follow-up message to a continuable child and
	// returns the child's answer. One-shot children return an error.
	SendChildAgent(ctx context.Context, id, message string) (string, error)
}

// ChildHistoryMaintainer is the optional maintenance surface over the durable
// child-agent history. The product UI does not call it; the headless driver
// resets the retained records between scripted scenes so one scene's finished
// children do not leak into the next. Backends without a durable history need
// not implement it.
type ChildHistoryMaintainer interface {
	// ClearChildAgentHistory drops every retained child-agent record.
	ClearChildAgentHistory()
}

// KeyChildAgents is provided by the agent module for the session UI.
var KeyChildAgents = cordis.NewServiceKey[ChildAgentService]("zeokeel/childagents")

// BackgroundJob is one row of the header's background-jobs popover.
type BackgroundJob struct {
	ID      string
	Kind    string
	Command string
	// State is the manager's lifecycle word: running, exited, killed, failed or
	// timed_out (the same value rig's job_list prints as state=).
	State    string
	ExitCode int
	Duration time.Duration
	Running  bool
}

const (
	// JobOutputMaxBytes bounds one JobService.JobOutput read (64 KiB): the
	// output panel keeps the newest bytes of a huge job rather than shipping
	// megabytes through a frame.
	JobOutputMaxBytes = 64 * 1024
	// JobOutputTruncatedMarker prefixes a bounded read whose older bytes were
	// dropped. The UI strips it and shows its own localized truncation hint;
	// the machine marker never reaches the user.
	JobOutputTruncatedMarker = "…[earlier output truncated]\n"
)

var (
	// ErrJobNotFound reports a job id the backend no longer retains (evicted
	// after finishing, or owned by another workspace build). The UI localizes
	// it instead of showing the backend's diagnostic.
	ErrJobNotFound = errors.New("job not found")
	// ErrJobUnavailable reports a product without the background-job backend;
	// the output panel explains that instead of failing the frame.
	ErrJobUnavailable = errors.New("background jobs are unavailable")
)

// JobService is the read/control surface behind the header's job chip. The
// toolkit module exposes the rig job manager through it (list/output/kill; the
// tool-facing job_* tools stay the model's API).
type JobService interface {
	// Jobs lists the retained jobs of every build of the caller's workspace
	// root: build creation order across builds, start-time ascending inside one
	// manager (a permission/runner change keeps the previous build's jobs
	// visible and controllable).
	Jobs() []BackgroundJob
	// KillJob stops a running job. Unknown or finished ids return an error.
	KillJob(id string) error
	// JobOutput returns the newest output of one retained job, bounded to
	// JobOutputMaxBytes with JobOutputTruncatedMarker when the older bytes were
	// cut. An empty string means the job produced no output yet. Unknown ids
	// wrap ErrJobNotFound and a missing backend wraps ErrJobUnavailable.
	JobOutput(id string) (string, error)
}

// KeyJobs is provided by the toolkit module and consumed by the session header.
var KeyJobs = cordis.NewServiceKey[JobService]("zeokeel/jobs")

// FilePreviewer is provided by the right-sidebar module so conversation cards
// and file references can open a workspace file directly in a sidebar tab.
type FilePreviewer interface {
	// PreviewFile opens (or focuses) one workspace file as a right-sidebar tab.
	PreviewFile(path string) error
	// OpenFiles focuses the sidebar's files page.
	OpenFiles() error
	// OpenNative opens the file in the platform's default application (the
	// secondary action on generated-file cards).
	OpenNative(path string) error
}

// KeyFilePreview is provided by the rightpanel module for the session surface.
var KeyFilePreview = cordis.NewServiceKey[FilePreviewer]("zeokeel/filepreview")
