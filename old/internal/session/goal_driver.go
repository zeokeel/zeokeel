package session

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/old/internal/tools"
)

const (
	// DefaultGoalRounds is the automatic continuation budget used when neither
	// the driver config nor the per-session setting names a limit.
	DefaultGoalRounds = 6
	// MaxGoalRounds is the hard ceiling for a session's continuation budget.
	// A configured or stored value above it is clamped, never rejected.
	MaxGoalRounds = 12

	// GoalRoundsSettingPrefix starts a per-session automatic goal-round key.
	GoalRoundsSettingPrefix = "session."
	// GoalRoundsSettingSuffix ends a per-session automatic goal-round key.
	GoalRoundsSettingSuffix = ".goal_rounds"
)

// SessionGoalRoundsKey renders the settings key that stores one session's
// maximum number of automatic goal continuation turns. sessionID is used
// verbatim: it is a store-assigned id, never user input.
func SessionGoalRoundsKey(sessionID string) string {
	return GoalRoundsSettingPrefix + sessionID + GoalRoundsSettingSuffix
}

// GoalRoundDriverConfig configures a GoalRoundDriver. The zero value is
// valid and uses DefaultGoalRounds.
type GoalRoundDriverConfig struct {
	// MaxRounds is the automatic continuation budget for one goal. Zero means
	// DefaultGoalRounds; values above MaxGoalRounds are clamped to it. A
	// per-session "session.<id>.goal_rounds" setting, when present and
	// positive, takes precedence over this default.
	MaxRounds int
}

// GoalRoundDriver turns a terminal turn with an active session goal into one
// automatic continuation turn. The Controller calls it exactly once per turn
// completion (idle or failed), never from a poller, so the driver cannot spin.
//
// The driver owns only the per-session continuation counters and the
// configured default budget; it owns no controller state and is safe for
// concurrent use. Controller.SetGoalDriver installs or disables it.
type GoalRoundDriver struct {
	mu        sync.Mutex
	maxRounds int            // configured default; 0 means DefaultGoalRounds
	rounds    map[string]int // session id -> automatic continuations submitted
}

// NewGoalRoundDriver returns a driver. The optional config makes the zero-arg
// form (all defaults) convenient; extra configs are ignored.
func NewGoalRoundDriver(configs ...GoalRoundDriverConfig) *GoalRoundDriver {
	cfg := GoalRoundDriverConfig{}
	if len(configs) > 0 {
		cfg = configs[0]
	}
	return &GoalRoundDriver{
		maxRounds: cfg.MaxRounds,
		rounds:    make(map[string]int),
	}
}

// Rounds returns how many automatic continuation turns have been submitted
// for sessionID. It is the value Controller.GoalRounds exposes.
func (d *GoalRoundDriver) Rounds(sessionID string) int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rounds[sessionID]
}

// Reset clears sessionID's continuation count. It runs when a new goal is set
// for the session (a different objective, or an active goal after a completed
// or abandoned one) so the new objective starts with a fresh budget.
func (d *GoalRoundDriver) Reset(sessionID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.rounds != nil {
		delete(d.rounds, sessionID)
	}
	d.mu.Unlock()
}

// SetMaxRounds changes the driver's default budget for goals started after
// the call. A value <= 0 restores DefaultGoalRounds; values above
// MaxGoalRounds are clamped. Per-session settings still take precedence.
func (d *GoalRoundDriver) SetMaxRounds(n int) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.maxRounds = n
	d.mu.Unlock()
}

// goalChanged resets the round count when next is a new goal rather than a
// progress update of the active one. It is called with Controller.mu held and
// only takes the driver lock, so the lock order is always Controller.mu then
// driver.mu.
func (d *GoalRoundDriver) goalChanged(sessionID string, previous, next *tools.Goal) {
	if d == nil || !isNewGoal(previous, next) {
		return
	}
	d.Reset(sessionID)
}

// isNewGoal reports whether next starts a fresh goal rather than updating the
// current one. A progress, step or objective edit for the same goal instance
// (same non-empty id) is not a new goal, so the automatic-continuation budget
// survives an in-place /goal edit; a /goal set with a new objective assigns a
// fresh id and is a new goal, as is reopening a completed/abandoned goal. A nil
// next clears the goal and is treated as a fresh start.
func isNewGoal(previous, next *tools.Goal) bool {
	if next == nil {
		return true
	}
	if next.Status != tools.GoalActive {
		return false
	}
	if previous == nil || previous.Status != tools.GoalActive {
		return true
	}
	// The id identifies a goal instance across every update that keeps it; an
	// objective edit keeps the same instance, so it must not reset the round
	// counter. Falls back to the objective comparison for goals persisted
	// without an id (older traces or hand-built states).
	if previous.ID != "" && previous.ID == next.ID {
		return false
	}
	return strings.TrimSpace(previous.Objective) != strings.TrimSpace(next.Objective)
}

// turnFinished is the Controller's turn-completion callback. It submits at
// most one continuation turn, and only when the session still has an active,
// non-empty goal, no user Stop was requested, and the per-session round budget
// is not exhausted. It never retries: another turn in flight, a closed
// controller or any other Submit failure just ends the automatic sequence.
//
// The caller runs while the finished turn still owns its slot: Controller's
// reserve performs an atomic handoff for an automatic continuation (it replaces
// the terminal turn with this one under a single lock), so a wait-idle observer
// never sees the session without a registered turn between the two rounds.
func (d *GoalRoundDriver) turnFinished(c *Controller, sessionID string, stopped bool) {
	if d == nil || c == nil || stopped || strings.TrimSpace(sessionID) == "" {
		return
	}

	goal := c.SessionGoal(sessionID)
	if !goalContinuable(goal) {
		return
	}

	limit := d.limit(c, sessionID)

	d.mu.Lock()
	if d.rounds == nil {
		d.rounds = make(map[string]int)
	}
	used := d.rounds[sessionID]
	if used < 0 {
		used = 0
	}
	if used >= limit {
		d.mu.Unlock()
		return
	}
	// Reserve the round before submitting. If Submit is rejected (a turn was
	// already running, the controller closed, or Stop landed in the window),
	// roll the reservation back so a failed attempt never consumes budget.
	d.rounds[sessionID] = used + 1
	d.mu.Unlock()

	prompt := goalContinuationPrompt(goal)
	if err := c.submit(context.Background(), sessionID, prompt, true); err != nil {
		d.mu.Lock()
		if d.rounds[sessionID] == used+1 {
			if used == 0 {
				delete(d.rounds, sessionID)
			} else {
				d.rounds[sessionID] = used
			}
		}
		d.mu.Unlock()
	}
}

// limit resolves the continuation budget: an explicit positive per-session
// setting wins, then the configured default, then DefaultGoalRounds. The hard
// cap always applies.
func (d *GoalRoundDriver) limit(c *Controller, sessionID string) int {
	limit := 0
	if d != nil {
		d.mu.Lock()
		limit = d.maxRounds
		d.mu.Unlock()
	}
	if limit <= 0 {
		limit = DefaultGoalRounds
	}
	if c != nil && c.store != nil {
		if raw, err := c.store.GetSetting(SessionGoalRoundsKey(sessionID)); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
				limit = n
			}
		}
	}
	if limit > MaxGoalRounds {
		return MaxGoalRounds
	}
	if limit < 1 {
		return 1
	}
	return limit
}

// goalContinuable reports whether goal is an active objective worth another
// turn. An empty objective is treated as no goal even if the status says
// active.
func goalContinuable(goal *tools.Goal) bool {
	// A paused or blocked goal keeps its objective and stays visible, but the
	// user explicitly asked for no more automatic turns until it is resumed
	// (FP-04 /goal pause|block).
	if goal != nil && (goal.Phase == tools.GoalPhasePaused || goal.Phase == tools.GoalPhaseBlocked) {
		return false
	}
	if goal == nil || goal.Status != tools.GoalActive {
		return false
	}
	return strings.TrimSpace(goal.Objective) != ""
}

// goalContinuationPrompt is the exact user prompt the driver submits. It
// carries the objective and the last remembered progress/steps so the
// continuation is grounded in the goal the tool reported.
func goalContinuationPrompt(goal *tools.Goal) string {
	var b strings.Builder
	b.WriteString("Continue working toward the goal. Objective: ")
	b.WriteString(strings.TrimSpace(goal.Objective))
	b.WriteString(". Progress: ")
	if goal.Progress < 0 {
		b.WriteString("0%")
	} else if goal.Progress > 100 {
		b.WriteString("100%")
	} else {
		fmt.Fprintf(&b, "%d%%", goal.Progress)
	}
	if len(goal.Steps) > 0 {
		b.WriteString(" (steps: ")
		b.WriteString(strings.Join(goal.Steps, "; "))
		b.WriteString(")")
	}
	b.WriteString(". Mark the goal complete when done.")
	return b.String()
}
