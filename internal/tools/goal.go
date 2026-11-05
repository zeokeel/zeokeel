package tools

import (
	"context"
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

const goalKind = "goal"

// Goal is the objective the agent is working towards in one session. It is
// reported by the goal tool and folded into the session by the controller so
// the team banner can show it without the tool holding any session state.
type Goal struct {
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
  {"action":"complete"}
  {"action":"abandon","message":"Blocked on the vendor"}

"action" is required and is one of set, get, complete, abandon.
"objective" is required for the first set and optional afterwards (the existing
objective is kept). "steps" is an optional ordered list for set; omit it to keep
the current steps and pass [] to clear them. "progress" is an optional percent
between 0 and 100. "message" is optional free text echoed back to you.

The tool returns {"kind":"goal","action":...,"goal":{...},"message":...}; get
returns no goal object when none is set. complete sets progress to 100 and
abandon keeps the objective but marks the goal abandoned. The UI shows the goal
as a banner above the conversation.`

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
// tool failure.
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
		return "", errors.New(`goal: "action" is required (set, get, complete, abandon)`)
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
	goal := t.sessions[sessionID]
	var message string
	switch action {
	case "set":
		objective := strings.TrimSpace(in.Objective)
		if objective == "" && goal != nil {
			objective = goal.Objective
		}
		if objective == "" {
			t.mu.Unlock()
			return "", errors.New(`goal: "objective" is required for set`)
		}
		var steps []string
		progress := 0
		if goal != nil {
			steps = goal.Steps
			progress = goal.Progress
		}
		if in.Steps != nil {
			steps = cleanSteps(in.Steps)
		}
		if in.Progress != nil {
			p, err := clampProgress(*in.Progress)
			if err != nil {
				t.mu.Unlock()
				return "", err
			}
			progress = p
		}
		t.sessions[sessionID] = &Goal{
			Objective: objective,
			Steps:     append([]string(nil), steps...),
			Progress:  progress,
			Status:    GoalActive,
			UpdatedAt: time.Now().UTC(),
		}
		goal = t.sessions[sessionID]
		message = strings.TrimSpace(in.Message)
		if message == "" {
			message = "goal set"
		}
	case "get":
		if goal == nil {
			message = "no goal set"
		} else {
			message = "goal"
		}
	case "complete":
		if goal == nil {
			t.mu.Unlock()
			return "", errors.New("goal: no goal to complete")
		}
		goal.Status = GoalCompleted
		goal.Progress = 100
		goal.UpdatedAt = time.Now().UTC()
		message = strings.TrimSpace(in.Message)
		if message == "" {
			message = "goal completed"
		}
	case "abandon":
		if goal == nil {
			t.mu.Unlock()
			return "", errors.New("goal: no goal to abandon")
		}
		goal.Status = GoalAbandoned
		goal.UpdatedAt = time.Now().UTC()
		message = strings.TrimSpace(in.Message)
		if message == "" {
			message = "goal abandoned"
		}
	default:
		t.mu.Unlock()
		return "", fmt.Errorf("goal: unknown action %q (want set, get, complete, abandon)", in.Action)
	}
	result := cloneGoal(goal)
	if t.persister != nil && action != "get" {
		if goal != nil {
			_ = t.persister.SaveGoal(sessionID, cloneGoal(goal))
		}
	}
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
