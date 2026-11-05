package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// GoalStatus is the lifecycle state of the session goal.
type GoalStatus string

const (
	// GoalActive is a goal the agent is still working on.
	GoalActive GoalStatus = "active"
	// GoalCompleted is a goal that was finished.
	GoalCompleted GoalStatus = "completed"
	// GoalAbandoned is a goal the agent or user gave up on.
	GoalAbandoned GoalStatus = "abandoned"
)

// GoalPhase is the finer lifecycle phase the user can command (/goal pause,
// block, resume). Status stays the two-state active/completed/abandoned axis the
// goal driver and older UIs read; Phase adds the paused/blocked states that must
// stop automatic continuation turns without abandoning the objective (FP-04).
type GoalPhase string

const (
	// GoalPhaseActive is a goal the agent should keep working on.
	GoalPhaseActive GoalPhase = "active"
	// GoalPhasePaused is a goal the user paused: it stays visible and can be
	// resumed, but no automatic continuation turns run.
	GoalPhasePaused GoalPhase = "paused"
	// GoalPhaseBlocked is a goal waiting on an external blocker: no automatic
	// continuation turns run until it is unblocked.
	GoalPhaseBlocked GoalPhase = "blocked"
	// GoalPhaseCompleted is a finished goal.
	GoalPhaseCompleted GoalPhase = "completed"
	// GoalPhaseAbandoned is a goal the user gave up on.
	GoalPhaseAbandoned GoalPhase = "abandoned"
)

const goalKind = "goal"

// Goal is the objective the agent is working towards in one session. It is
// reported by the goal tool and folded into the session by the controller so
// the team banner can show it without the tool holding any session state.
type Goal struct {
	// ID identifies this goal instance. It stays stable across progress,
	// step, pause/resume and block/unblock updates and changes when a new
	// objective starts, so an observer can tell "the same goal advanced" from
	// "a new goal replaced it" (FP-04).
	ID string `json:"id,omitempty"`
	// Revision increments on every mutation (set of a field, pause, resume,
	// block, unblock, complete, abandon). Clients can ignore an older revision.
	Revision  int        `json:"revision"`
	Phase     GoalPhase  `json:"phase,omitempty"`
	Objective string     `json:"objective"`
	Steps     []string   `json:"steps,omitempty"`
	Progress  int        `json:"progress"` // percent, 0-100
	Status    GoalStatus `json:"status"`
	UpdatedAt time.Time  `json:"updated_at,omitempty"`
}

// GoalResult is the JSON document GoalTool returns for every invocation. The
// controller folds a successful end event whose result has Kind "goal" back
// into the session, the same pattern todo and ask_user use.
type GoalResult struct {
	Kind    string `json:"kind"`
	Action  string `json:"action"`
	Goal    *Goal  `json:"goal,omitempty"`
	Message string `json:"message,omitempty"`
}

// goalDescription documents the tool and its JSON argument schema.
const goalDescription = `Set, inspect and close the session goal.

Call it with a JSON object:
  {"action":"set","objective":"Ship the release","steps":["Run the tests","Write notes"],"progress":25}
  {"action":"get"}
  {"action":"pause"}
  {"action":"resume"}
  {"action":"block","message":"Waiting for CI"}
  {"action":"unblock"}
  {"action":"complete"}
  {"action":"abandon","message":"Blocked on the vendor"}
  {"action":"clear"}

"action" is required and is one of set, get, pause, resume, block, unblock,
complete, abandon, clear.
"objective" is required for the first set and optional afterwards (the existing
objective is kept). "steps" is an optional ordered list for set; omit it to keep
the current steps and pass [] to clear them. "progress" is an optional percent
between 0 and 100. "message" is optional free text echoed back to you.

The tool returns {"kind":"goal","action":...,"goal":{...},"message":...}; get
returns no goal object when none is set. A goal carries an id, a revision that
increments on every change, and a phase (active, paused, blocked, completed,
abandoned). pause and block stop automatic continuation turns without
abandoning the objective; resume and unblock re-arm it. complete sets progress
to 100, abandon keeps the objective but marks the goal abandoned, and clear
removes it. The UI shows the goal as a banner above the conversation.`

// GoalTool is the per-agent goal store. All state lives in this instance,
// guarded by mu, so two agents (or two test runs) never share a goal.
// GoalStateStore persists one session's goal across tool rebuilds, so a model
// switch or plugin toggle does not lose the objective the UI still displays.
type GoalStateStore interface {
	LoadGoal(sessionID string) (*Goal, error)
	SaveGoal(sessionID string, goal *Goal) error
}

type GoalTool struct {
	mu        sync.Mutex
	sessions  map[string]*Goal
	loaded    map[string]bool
	persister GoalStateStore
}

// NewGoalTool returns an empty goal tool.
func NewGoalTool() *GoalTool {
	return &GoalTool{sessions: make(map[string]*Goal)}
}

// NewGoalToolWithStore returns a goal tool that hydrates and saves its
// per-session goal through store.
func NewGoalToolWithStore(store GoalStateStore) *GoalTool {
	return &GoalTool{sessions: make(map[string]*Goal), persister: store}
}

// goalSessionKey names the bucket used by direct invocations without a
// session id.
func goalSessionKey(ctx context.Context) string {
	if id := strings.TrimSpace(runtime.SessionIDFromContext(ctx)); id != "" {
		return id
	}
	return ""
}

// Name implements runtime.Tool.
func (t *GoalTool) Name() string { return "goal" }

// Description implements runtime.Tool.
func (t *GoalTool) Description() string { return goalDescription }

// goalArgs is the decoded argument object. One struct covers every action so
// the model can omit fields that do not apply.
type goalArgs struct {
	Action    string   `json:"action"`
	Objective string   `json:"objective,omitempty"`
	Steps     []string `json:"steps,omitempty"`
	Progress  *int     `json:"progress,omitempty"`
	Message   string   `json:"message,omitempty"`
}

// Invoke implements runtime.Tool. It returns a GoalResult JSON document and
// never panics on bad arguments; a non-nil error is reported to the model as a
// tool failure. Every lifecycle action goes through ApplyGoalAction so the tool
// and the /goal controller API produce identical ids, revisions and phases.
func (t *GoalTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in goalArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("goal: invalid arguments: %w", err)
		}
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		return "", errors.New(`goal: "action" is required (set, get, pause, resume, block, unblock, complete, abandon, clear)`)
	}

	sessionID := goalSessionKey(ctx)
	t.mu.Lock()
	if t.sessions == nil {
		t.sessions = make(map[string]*Goal)
	}
	if t.loaded == nil {
		t.loaded = make(map[string]bool)
	}
	if t.persister != nil && !t.loaded[sessionID] {
		t.loaded[sessionID] = true
		if saved, err := t.persister.LoadGoal(sessionID); err == nil && saved != nil {
			t.sessions[sessionID] = cloneGoal(saved)
		}
	}
	previous := t.sessions[sessionID]
	next, message, err := ApplyGoalAction(previous, action, in.Objective, in.Steps, in.Progress, in.Message)
	if err != nil {
		t.mu.Unlock()
		return "", err
	}
	if next == nil {
		delete(t.sessions, sessionID)
	} else {
		t.sessions[sessionID] = next
	}
	if t.persister != nil && action != "get" {
		// SaveGoal(nil) persists the cleared state; a persistence failure is
		// not fatal (the in-memory goal is authoritative for this process).
		_ = t.persister.SaveGoal(sessionID, cloneGoal(next))
	}
	result := cloneGoal(next)
	t.mu.Unlock()

	out, err := json.Marshal(GoalResult{
		Kind:    goalKind,
		Action:  action,
		Goal:    result,
		Message: message,
	})
	if err != nil {
		return "", fmt.Errorf("goal: encode result: %w", err)
	}
	return string(out), nil
}

// ApplyGoalAction applies one lifecycle action to the previous goal and returns
// the next goal (nil means "no goal"), a human-readable message, and an error
// for invalid transitions. It is the single implementation shared by the goal
// tool and the Controller.GoalAction /goal API.
//
// set keeps the steps/progress of an update of the same objective and assigns a
// fresh id when the objective changes or a completed/abandoned goal is reopened.
// pause/block move the phase without abandoning the objective; resume/unblock
// return it to active. complete and abandon are terminal; clear deletes the
// goal. Every mutating action increments Revision, and get is side-effect free.
func ApplyGoalAction(previous *Goal, action, objective string, steps []string, progress *int, message string) (*Goal, string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	now := time.Now().UTC()
	switch action {
	case "get":
		if previous == nil {
			return nil, "no goal set", nil
		}
		return cloneGoal(previous), "goal", nil
	case "clear":
		if previous == nil {
			return nil, "no goal set", nil
		}
		return nil, "goal cleared", nil
	case "set":
		next := &Goal{Status: GoalActive, Phase: GoalPhaseActive, UpdatedAt: now}
		if previous != nil {
			next.Objective = previous.Objective
			next.Steps = append([]string(nil), previous.Steps...)
			next.Progress = previous.Progress
			next.ID = previous.ID
			next.Revision = previous.Revision
		}
		if obj := strings.TrimSpace(objective); obj != "" {
			next.Objective = obj
		}
		if strings.TrimSpace(next.Objective) == "" {
			return nil, "", errors.New(`goal: "objective" is required for set`)
		}
		if steps != nil {
			next.Steps = cleanSteps(steps)
		}
		if progress != nil {
			p, err := clampProgress(*progress)
			if err != nil {
				return nil, "", fmt.Errorf("goal: %w", err)
			}
			next.Progress = p
		}
		newObjective := previous == nil || strings.TrimSpace(previous.Objective) != strings.TrimSpace(next.Objective)
		reopened := previous != nil && previous.Status != GoalActive
		if next.ID == "" || newObjective || reopened {
			// A new goal instance starts a fresh id and revision sequence.
			next.ID = newGoalID()
			next.Revision = 0
		}
		next.Revision++
		msg := strings.TrimSpace(message)
		if msg == "" {
			msg = "goal set"
		}
		return next, msg, nil
	}

	if previous == nil {
		return nil, "", fmt.Errorf("goal: no goal to %s", action)
	}
	next := cloneGoal(previous)
	next.UpdatedAt = now
	msg := strings.TrimSpace(message)
	switch action {
	case "pause":
		if next.Status != GoalActive {
			return nil, "", fmt.Errorf("goal: cannot pause a %s goal", next.Status)
		}
		next.Phase = GoalPhasePaused
		if msg == "" {
			msg = "goal paused"
		}
	case "resume":
		if next.Status != GoalActive {
			return nil, "", fmt.Errorf("goal: cannot resume a %s goal", next.Status)
		}
		next.Phase = GoalPhaseActive
		if msg == "" {
			msg = "goal resumed"
		}
	case "block":
		if next.Status != GoalActive {
			return nil, "", fmt.Errorf("goal: cannot block a %s goal", next.Status)
		}
		next.Phase = GoalPhaseBlocked
		if msg == "" {
			msg = "goal blocked"
		}
	case "unblock":
		if next.Status != GoalActive {
			return nil, "", fmt.Errorf("goal: cannot unblock a %s goal", next.Status)
		}
		next.Phase = GoalPhaseActive
		if msg == "" {
			msg = "goal unblocked"
		}
	case "complete":
		next.Status = GoalCompleted
		next.Phase = GoalPhaseCompleted
		next.Progress = 100
		if msg == "" {
			msg = "goal completed"
		}
	case "abandon":
		next.Status = GoalAbandoned
		next.Phase = GoalPhaseAbandoned
		if msg == "" {
			msg = "goal abandoned"
		}
	default:
		return nil, "", fmt.Errorf("goal: unknown action %q (want set, get, pause, resume, block, unblock, complete, abandon, clear)", action)
	}
	next.Revision++
	return next, msg, nil
}

// newGoalID returns a random goal instance id.
func newGoalID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("goal-%d", time.Now().UnixNano())
	}
	return "goal-" + hex.EncodeToString(b[:])
}

// Current returns a copy of the current goal, or nil. The view reads it only
// through the controller; it is exposed for tests and future wiring.
func (t *GoalTool) Current() *Goal { return t.GoalFor("") }

// GoalFor returns one session's goal. The live shell reads through the
// controller; this is the regression-test seam for session isolation.
func (t *GoalTool) GoalFor(sessionID string) *Goal {
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneGoal(t.sessions[sessionID])
}

// cloneGoal returns a copy whose Steps slice is independent of the source.
func cloneGoal(goal *Goal) *Goal {
	if goal == nil {
		return nil
	}
	out := *goal
	out.Steps = append([]string(nil), goal.Steps...)
	return &out
}

// cleanSteps trims blank steps and preserves the order of the rest.
func cleanSteps(steps []string) []string {
	if len(steps) == 0 {
		return nil
	}
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		out = append(out, step)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clampProgress validates a percentage.
func clampProgress(progress int) (int, error) {
	if progress < 0 || progress > 100 {
		return 0, fmt.Errorf("goal: progress %d is out of range 0-100", progress)
	}
	return progress, nil
}
