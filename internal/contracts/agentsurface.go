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
}

// ChildAgentService is the read/control surface the session header uses for the
// subagent chip and the child conversation view. ParentSessionID scopes the
// list; empty means the active session.
type ChildAgentService interface {
	// ChildAgents lists the child agents of one parent session, oldest first.
	ChildAgents(parentSessionID string) []ChildAgent
	// ChildAgent returns one child agent by ID.
	ChildAgent(id string) (ChildAgent, bool)
	// SendChildAgent delivers a follow-up message to a continuable child and
	// returns the child's answer. One-shot children return an error.
	SendChildAgent(ctx context.Context, id, message string) (string, error)
}

// KeyChildAgents is provided by the agent module for the session UI.
var KeyChildAgents = cordis.NewServiceKey[ChildAgentService]("zeokeel/childagents")

// BackgroundJob is one row of the header's background-jobs popover.
type BackgroundJob struct {
	ID       string
	Kind     string
	Command  string
	Status   string
	State    string
	ExitCode int
	Duration time.Duration
	Running  bool
}

// JobService is the read/control surface behind the header's job chip. The
// toolkit module exposes the rig job manager through it (list/kill only; the
// tool-facing job_* tools stay the model's API).
type JobService interface {
	// Jobs lists the manager's retained jobs, oldest first.
	Jobs() []BackgroundJob
	// KillJob stops a running job. Unknown or finished ids return an error.
	KillJob(id string) error
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
	// OpenTerminal focuses the sidebar's terminal page.
	OpenTerminal() error
	// OpenNative opens the file in the platform's default application (the
	// secondary action on generated-file cards).
	OpenNative(path string) error
}

// KeyFilePreview is provided by the rightpanel module for the session surface.
var KeyFilePreview = cordis.NewServiceKey[FilePreviewer]("zeokeel/filepreview")
