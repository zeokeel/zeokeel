package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Teammate run states. The rig team manager's Member.Status only reports
// availability ("active" or "interrupted"), never whether a run is in flight;
// the agent backend tracks the actual run lifecycle and surfaces it here so the
// inspector can show a truthful badge instead of drawing "active" as "running".
const (
	// TeamRunIdle is a spawned teammate with no run in flight that has not
	// completed a run yet (or whose completed run is old); it is available for
	// a follow-up message.
	TeamRunIdle = "idle"
	// TeamRunRunning is a teammate whose current run is in flight.
	TeamRunRunning = "running"
	// TeamRunDone is a teammate whose latest run returned an answer.
	TeamRunDone = "done"
	// TeamRunFailed is a teammate whose latest run failed.
	TeamRunFailed = "failed"
	// TeamRunInterrupted is a teammate whose current run was interrupted (the
	// member stays available until it is closed).
	TeamRunInterrupted = "interrupted"
	// TeamRunClosed is a teammate that has been stopped and removed; a member
	// snapshot normally does not carry it, but a read model may.
	TeamRunClosed = "closed"
)

// TeamActivity kinds. They name what happened to one teammate or to the team,
// in the bounded activity read model the inspector shows as a timeline.
const (
	// TeamActivitySpawn is a teammate (or a tool) creating a member.
	TeamActivitySpawn = "spawn"
	// TeamActivitySend is a follow-up message sent to a member.
	TeamActivitySend = "send"
	// TeamActivityWait is a wait for a member's current run.
	TeamActivityWait = "wait"
	// TeamActivityFinish is a run that returned an answer.
	TeamActivityFinish = "finish"
	// TeamActivityError is a run that failed.
	TeamActivityError = "error"
	// TeamActivityInterrupt is an interrupt of a member's current run.
	TeamActivityInterrupt = "interrupt"
	// TeamActivityClose is a member stopped and removed.
	TeamActivityClose = "close"
	// TeamActivityTool is any other team tool invocation (task board calls).
	TeamActivityTool = "tool"
)

// TeamMember is one teammate as shown by the Agents inspector and the team
// tools: a live in-process member, or (after a restart) a historical row
// restored from the durable child-agent history.
type TeamMember struct {
	// ID is the manager-generated reference ("member-1").
	ID string
	// Name is the display name passed on spawn.
	Name string
	// Status is the availability reported by the rig manager: "active" or
	// "interrupted". It is not a run state; use RunState for that.
	Status string
	// Created is when the member was spawned.
	Created time.Time
	// Last is the latest finished answer ("" while none finished).
	Last string

	// RunState is one of the TeamRun* values tracked by the agent backend.
	RunState string
	// Error is the latest run error ("" when the latest run succeeded or none
	// finished). It never carries the answer text.
	Error string
	// Model is the model the teammate runs with (the spawn override or the
	// parent session model).
	Model string
	// Started is when the current (or latest) run started; zero before the
	// first run.
	Started time.Time
	// Finished is when the latest run returned; zero while running or before
	// the first run.
	Finished time.Time
	// UsageTokens is the accumulated total tokens of the completed runs.
	UsageTokens int
	// Iterations is the accumulated model/tool rounds of the completed runs.
	Iterations int
	// Activity is the bounded recent activity of this member, oldest first.
	Activity []TeamActivity
	// SessionID is the session that spawned the member ("" when the backend
	// could not attribute it). The durable history uses it to scope a restored
	// member to the session the user reopens.
	SessionID string
	// Historical marks a member restored from the durable child-agent history:
	// its live run is gone, so it is read-only (open/inspect only), it never
	// counts as running and its ID is a history key, not a manager reference.
	Historical bool
}

// TeamActivity is one event of the team activity read model: a spawn, a
// message, a finished/failed run, an interrupt/close, or another team tool
// call. The inspector renders them as a bounded timeline.
type TeamActivity struct {
	// ID is a process-unique activity ID (stable row identity).
	ID string
	// MemberID is the member the activity belongs to; "" for a team-level
	// event the backend could not attribute to one member.
	MemberID string
	// Kind is one of the TeamActivity* values.
	Kind string
	// Text is a bounded, whitespace-collapsed human-readable summary.
	Text string
	// Time is when the event happened.
	Time time.Time
	// Error reports whether the event is a failure.
	Error bool
}

// TeamActivityLister is the optional read-only surface of the team backend: the
// newest team activity events (bounded by the backend), oldest first. The
// inspector discovers it with a type assertion on the TeamService and degrades
// gracefully when the backend does not implement it.
type TeamActivityLister interface {
	TeamActivities() []TeamActivity
}

// TeamService is the product-side view/control surface over the rig team
// manager: the inspector lists members, steers them with follow-up messages and
// interrupts them; the agent module registers the model-facing team tools.
type TeamService interface {
	// Members lists the teammates, oldest first: the live manager members
	// followed by the restored historical rows (Historical == true, read-only,
	// no live backend behind their IDs).
	Members() []TeamMember
	// Spawn starts a named teammate with an initial prompt.
	Spawn(ctx context.Context, name, prompt, model string) (TeamMember, error)
	// SendAsync queues a follow-up message without waiting for its answer.
	SendAsync(id, message string) error
	// Send delivers a follow-up message and returns the teammate's answer.
	Send(ctx context.Context, id, message string) (string, error)
	// Wait blocks until the teammate finishes its current run.
	Wait(ctx context.Context, id string) (string, error)
	// Interrupt cancels the teammate's current run.
	Interrupt(id string) error
	// Close stops and removes the teammate.
	Close(id string) error
}

// KeyTeam is provided by the agent module (rig team.NewManager).
var KeyTeam = cordis.NewServiceKey[TeamService]("zeokeel/team")
