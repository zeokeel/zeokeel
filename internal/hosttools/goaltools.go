package hosttools

// Product-owned DSH goal tools: get_goal / create_goal / update_goal.
//
// Rig v0.16.2 (the published dependency) only exposes the goal trio through its
// host-tool set and binds it to a context-less hosttools.GoalRevisionStore via
// Options.GoalRevision (that option landed after v0.16.2). ZeoKeel therefore
// implements the three tools itself, calling the ctx-carrying GoalStore adapter
// in goalrevision.go. The model-visible contract (names, descriptions, JSON
// schemas and result JSON) mirrors the Rig DSH implementation byte-for-byte so
// the model and the UI see exactly the same surface as before; the adapter keeps
// the DSH semantics (session ctx, direct-human-turn rule, blocked>=3 rounds, CAS,
// activation, localized errors).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	righosttools "github.com/zeokeel/zeokeel-rig/tools/hosttools"
)

// DSH goal tool names (stable model-visible identifiers).
const (
	// ToolGetGoal is the read tool name.
	ToolGetGoal = "get_goal"
	// ToolCreateGoal is the create tool name.
	ToolCreateGoal = "create_goal"
	// ToolUpdateGoal is the update tool name.
	ToolUpdateGoal = "update_goal"
	// DefaultMaxGoalRounds is the DSH automatic-continuation cap applied when
	// create_goal omits max_goal_rounds.
	DefaultMaxGoalRounds = righosttools.DefaultMaxGoalRounds
)

// goalToolValue is one goal in the canonical model result.
type goalToolValue struct {
	ID            string                          `json:"id"`
	Revision      int                             `json:"revision"`
	Objective     string                          `json:"objective"`
	Phase         righosttools.GoalPhase          `json:"phase"`
	RoundsStarted int                             `json:"roundsStarted"`
	MaxGoalRounds int                             `json:"maxGoalRounds"`
	BlockedReason *righosttools.GoalBlockedReason `json:"blockedReason,omitempty"`
}

// goalToolOutput is the canonical model result: {"goal":null} or
// {"goal":{...},"activation":"..."}.
type goalToolOutput struct {
	Goal       *goalToolValue              `json:"goal"`
	Activation righosttools.GoalActivation `json:"activation,omitempty"`
}

// goalFuncTool is one context-carrying model-facing tool.
type goalFuncTool struct {
	name        string
	description string
	parameters  map[string]any
	run         func(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error)
}

var _ rig.Tool = (*goalFuncTool)(nil)

// Name implements rig.Tool.
func (t *goalFuncTool) Name() string { return t.name }

// Description implements rig.Tool.
func (t *goalFuncTool) Description() string { return t.description }

// Schema implements rig.Tool.
func (t *goalFuncTool) Schema() map[string]any { return t.parameters }

// Invoke implements rig.Tool.
func (t *goalFuncTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	return t.run(ctx, call)
}

// GoalTools returns the DSH goal trio bound to store. A nil store keeps the
// tools registered and answers with a readable model-facing error, mirroring
// the previous "no goal backend" behavior.
func GoalTools(store RevisionGoalStore) []rig.Tool {
	return []rig.Tool{
		getGoalTool(store),
		createGoalTool(store),
		updateGoalTool(store),
	}
}

// --- get_goal --------------------------------------------------------------

func getGoalTool(store RevisionGoalStore) rig.Tool {
	return &goalFuncTool{
		name: ToolGetGoal,
		description: "Read the current session goal, including the id and revision that update_goal requires. " +
			"Returns {\"goal\":null} when no goal is current.",
		parameters: goalObjectSchema(map[string]any{}),
		run: func(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
			var args struct{}
			if err := decodeGoalArgs(call, &args); err != nil {
				return rig.ToolResult{}, err
			}
			if store == nil {
				return goalErrorResultf("%s: the host did not provide a goal backend", ToolGetGoal), nil
			}
			if err := ctx.Err(); err != nil {
				return rig.ToolResult{}, err
			}
			snapshot, ok := store.CurrentGoal(ctx)
			return goalResult(ToolGetGoal, snapshot, ok)
		},
	}
}

// --- create_goal -----------------------------------------------------------

// createGoalArgs is the decoded create_goal argument object.
type createGoalArgs struct {
	Objective     string `json:"objective"`
	MaxGoalRounds int    `json:"max_goal_rounds"`
}

func createGoalTool(store RevisionGoalStore) rig.Tool {
	return &goalFuncTool{
		name: ToolCreateGoal,
		description: "Create a persisted goal that keeps this session working across automatic continuation rounds. " +
			"Use it when the direct human request is a long-running objective, even if the user did not say \"goal\"; " +
			"not for single-turn work.",
		parameters: goalObjectSchema(map[string]any{
			"objective": goalStringProp("The concrete completion objective inferred from the direct human request."),
			"max_goal_rounds": goalIntegerProp(fmt.Sprintf(
				"Optional positive integer limit on automatic continuation rounds; defaults to %d.", DefaultMaxGoalRounds)),
		}, "objective"),
		run: func(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
			var args createGoalArgs
			if err := decodeGoalArgs(call, &args); err != nil {
				return rig.ToolResult{}, err
			}
			objective := strings.TrimSpace(args.Objective)
			if objective == "" {
				return goalErrorResultf("%s: objective is required and must be non-empty after trimming", ToolCreateGoal), nil
			}
			if args.MaxGoalRounds < 0 {
				return goalErrorResultf("%s: max_goal_rounds must be a positive integer", ToolCreateGoal), nil
			}
			if store == nil {
				return goalErrorResultf("%s: the host did not provide a goal backend", ToolCreateGoal), nil
			}
			if err := ctx.Err(); err != nil {
				return rig.ToolResult{}, err
			}
			snapshot, err := store.CreateGoal(ctx, objective, args.MaxGoalRounds)
			if err != nil {
				return goalErrorResultf("%s: %v", ToolCreateGoal, err), nil
			}
			return goalResult(ToolCreateGoal, snapshot, true)
		},
	}
}

// --- update_goal -----------------------------------------------------------

// updateGoalArgs is the decoded update_goal argument object.
type updateGoalArgs struct {
	GoalID        string `json:"goal_id"`
	Revision      int    `json:"revision"`
	Action        string `json:"action"`
	Objective     string `json:"objective"`
	MaxGoalRounds int    `json:"max_goal_rounds"`
	BlockedReason string `json:"blocked_reason"`
}

func updateGoalTool(store RevisionGoalStore) rig.Tool {
	return &goalFuncTool{
		name: ToolUpdateGoal,
		description: "Update the current goal on its exact revision. get_goal first and copy the exact goal_id and revision; " +
			"a stale pair is rejected instead of overwriting newer state. action=edit replaces the objective and/or round cap, " +
			"pause/resume/complete move the lifecycle, and blocked requires blocked_reason.",
		parameters: goalObjectSchema(map[string]any{
			"goal_id":  goalStringProp("Exact id returned by get_goal."),
			"revision": goalIntegerProp("Exact positive revision returned by get_goal."),
			"action": map[string]any{
				"type":        "string",
				"description": "Lifecycle action to apply to the exact goal revision.",
				"enum":        []string{string(rig.GoalEdit), string(rig.GoalPause), string(rig.GoalResume), string(rig.GoalComplete), string(rig.GoalBlocked)},
			},
			"objective":       goalStringProp("Replacement objective; valid only with action edit."),
			"max_goal_rounds": goalIntegerProp("Replacement round cap; valid only with action edit."),
			"blocked_reason":  goalStringProp("Required only with action blocked: the concrete condition that persisted across rounds and blocks progress."),
		}, "goal_id", "revision", "action"),
		run: func(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
			var args updateGoalArgs
			if err := decodeGoalArgs(call, &args); err != nil {
				return rig.ToolResult{}, err
			}
			ref, update, err := parseGoalUpdate(args)
			if err != nil {
				return goalErrorResultf("%s: %v", ToolUpdateGoal, err), nil
			}
			if store == nil {
				return goalErrorResultf("%s: the host did not provide a goal backend", ToolUpdateGoal), nil
			}
			if err := ctx.Err(); err != nil {
				return rig.ToolResult{}, err
			}
			snapshot, err := store.UpdateGoal(ctx, ref, update)
			if err != nil {
				return goalErrorResultf("%s: %v", ToolUpdateGoal, err), nil
			}
			return goalResult(ToolUpdateGoal, snapshot, true)
		},
	}
}

// parseGoalUpdate validates update_goal arguments and builds the CAS request.
// Only meaningful optional fields may accompany their action; blocked requires a
// reason.
func parseGoalUpdate(args updateGoalArgs) (righosttools.GoalRef, righosttools.GoalUpdate, error) {
	goalID := args.GoalID
	if goalID == "" || strings.TrimSpace(goalID) != goalID {
		return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("goal_id must be non-empty without surrounding whitespace")
	}
	if args.Revision < 1 {
		return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("revision must be a positive integer")
	}
	action := rig.GoalUpdateAction(strings.ToLower(strings.TrimSpace(args.Action)))
	switch action {
	case rig.GoalEdit, rig.GoalPause, rig.GoalResume, rig.GoalComplete, rig.GoalBlocked:
	default:
		return righosttools.GoalRef{}, righosttools.GoalUpdate{}, fmt.Errorf("unknown action %q (want edit, pause, resume, complete or blocked)", args.Action)
	}
	objective := args.Objective
	hasObjective := objective != ""
	if hasObjective && strings.TrimSpace(objective) == "" {
		return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("objective must be non-empty after trimming")
	}
	hasRoundCap := args.MaxGoalRounds != 0
	if args.MaxGoalRounds < 0 {
		return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("max_goal_rounds must be a positive integer")
	}
	blockedReason := args.BlockedReason
	hasBlockedReason := blockedReason != ""
	switch action {
	case rig.GoalEdit:
		if hasBlockedReason {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("blocked_reason is valid only with action blocked")
		}
		if !hasObjective && !hasRoundCap {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("action edit requires objective and/or max_goal_rounds")
		}
	case rig.GoalPause, rig.GoalResume:
		if hasObjective || hasRoundCap || hasBlockedReason {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("objective and max_goal_rounds are valid only with action edit; blocked_reason is valid only with action blocked")
		}
	case rig.GoalComplete:
		if hasObjective || hasRoundCap {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("objective and max_goal_rounds are valid only with action edit")
		}
		if hasBlockedReason {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("blocked_reason is valid only with action blocked")
		}
	case rig.GoalBlocked:
		if hasObjective || hasRoundCap {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("objective and max_goal_rounds are valid only with action edit")
		}
		if strings.TrimSpace(blockedReason) == "" {
			return righosttools.GoalRef{}, righosttools.GoalUpdate{}, errors.New("blocked_reason is required with action blocked")
		}
	}
	return righosttools.GoalRef{ID: goalID, Revision: args.Revision}, righosttools.GoalUpdate{
		Action:        action,
		Objective:     objective,
		MaxGoalRounds: args.MaxGoalRounds,
		BlockedReason: blockedReason,
	}, nil
}

// goalResult renders one canonical DSH goal result.
func goalResult(toolName string, snapshot righosttools.GoalSnapshot, ok bool) (rig.ToolResult, error) {
	output := goalToolOutput{}
	if ok {
		output.Goal = &goalToolValue{
			ID:            snapshot.ID,
			Revision:      snapshot.Revision,
			Objective:     snapshot.Objective,
			Phase:         snapshot.Phase,
			RoundsStarted: snapshot.RoundsStarted,
			MaxGoalRounds: snapshot.MaxGoalRounds,
			BlockedReason: snapshot.BlockedReason,
		}
		output.Activation = snapshot.Activation
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return goalErrorResultf("%s: %v", toolName, err), nil
	}
	return rig.ToolResult{Content: string(encoded)}, nil
}

// --- small JSON helpers (mirroring Rig's internal toolutil) -----------------

// decodeGoalArgs decodes one tool-call argument JSON; empty input is an empty
// object.
func decodeGoalArgs(call rig.ToolCall, target any) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}
	return nil
}

// goalObjectSchema builds an object schema with no additional properties.
func goalObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// goalStringProp builds one string property schema.
func goalStringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// goalIntegerProp builds one integer property schema.
func goalIntegerProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

// goalErrorResultf builds a model-facing tool failure result.
func goalErrorResultf(format string, args ...any) rig.ToolResult {
	return rig.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}
