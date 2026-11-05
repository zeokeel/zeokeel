package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// DefaultRalphMaxRounds is the round budget used when the caller omits
	// "max_rounds".
	DefaultRalphMaxRounds = 3

	// MaxRalphRounds is the absolute ceiling on a Ralph loop: no deployment
	// configuration and no model request may exceed it.
	MaxRalphRounds = 10

	// ralphMaxDepth is the deepest child depth a Ralph round may start at.
	// Ralph children run at depth 1, so a Ralph tool invoked from inside a
	// child (depth 1) would spawn depth 2 and is rejected; only the root agent
	// may start a loop.
	ralphMaxDepth = 1

	// ralphMaxOutput caps one Ralph tool result (64 KiB), matching the other
	// orchestration tools.
	ralphMaxOutput = DefaultMaxOutputBytes

	// ralphMaxObjectiveBytes caps the immutable objective. A Ralph prompt is
	// rebuilt every round, so a huge objective would be paid for repeatedly.
	ralphMaxObjectiveBytes = 8 * 1024

	// ralphDigestBytes caps the previous-round handoff embedded in the next
	// round's task. Only a compact digest crosses rounds.
	ralphDigestBytes = 2 * 1024

	// ralphReportDigestBytes caps the copy of the final output kept in the
	// structured report.
	ralphReportDigestBytes = 512

	// ralphErrorBytes caps a recorded per-round error message.
	ralphErrorBytes = 512
)

// Ralph report statuses.
const (
	// RalphStatusComplete means a round output carried a completion marker.
	RalphStatusComplete = "complete"
	// RalphStatusMaxRounds means the loop used its whole round budget without
	// seeing a completion marker.
	RalphStatusMaxRounds = "max_rounds"
	// RalphStatusFailed means a round's child agent failed.
	RalphStatusFailed = "failed"
)

const ralphDescription = `Run a foreground fresh-agent Ralph loop toward one immutable objective.

Call it with a JSON object:
  {"objective":"Make the parser tests pass and report the exact failures"}
  {"objective":"Repair the build","max_rounds":5,"agent":"builder"}

"objective" is required, must contain non-whitespace text and is capped at
8192 bytes. "max_rounds" is an optional round budget in [1,10]; it defaults to
3 and may not exceed the deployment ceiling. "agent" is an optional child agent
name used for every round.

Each round starts a fresh child agent with no parent conversation and no prior
child session. The child sees the immutable objective plus only a compact
digest of the previous round's output; the shared workspace is the durable
memory between rounds. A round is complete when its output contains DONE or
COMPLETE as a standalone case-insensitive word. The loop stops on completion,
on a failed round, on context cancellation, or when the round budget is spent.

The tool returns {"kind":"ralph","objective":...,"completed":...,
"rounds":[{"round":1,"output":...,"completed":false,"error":...}],
"report":{"status":"complete|max_rounds|failed","summary":...,...}}. It never
panics on a child failure: the failed round and its error are recorded in the
result. The complete JSON result is capped at 65536 bytes; oversized round
outputs are truncated and "report.truncated" is set.`

// RalphConfig configures a RalphTool.
type RalphConfig struct {
	// MaxRounds is the deployment ceiling for one loop. Zero means
	// DefaultRalphMaxRounds; values above MaxRalphRounds are clamped to it.
	// A caller's "max_rounds" can lower the budget but never raise it above
	// this ceiling.
	MaxRounds int

	// Runner executes each fresh child agent. It is required at call time;
	// runtime.New auto-binds itself through runtime.SubagentRunnerBinder when
	// the tool is registered, which solves the registry/agent cycle.
	Runner runtime.SubagentRunner
}

// RalphRound is one round of a Ralph loop.
type RalphRound struct {
	// Round is the 1-based round number.
	Round int `json:"round"`
	// Output is the child's final text, bounded by the result cap.
	Output string `json:"output"`
	// Completed marks the round whose output carried a completion marker.
	Completed bool `json:"completed"`
	// Error records a child-agent failure for this round. A failed round stops
	// the loop; it is data, not a panic and not a tool error.
	Error string `json:"error,omitempty"`
}

// RalphReport is the short structured summary of one Ralph loop.
type RalphReport struct {
	// Status is complete, max_rounds or failed.
	Status string `json:"status"`
	// Summary is a one-line account of why the loop stopped.
	Summary string `json:"summary"`
	// RoundsStarted is how many rounds were attempted.
	RoundsStarted int `json:"rounds_started"`
	// RoundsCompleted is how many rounds produced output without a runner
	// error (the marker round included).
	RoundsCompleted int `json:"rounds_completed"`
	// LastOutput is a bounded digest of the final round's output.
	LastOutput string `json:"last_output,omitempty"`
	// Error is the recorded child failure when Status is failed.
	Error string `json:"error,omitempty"`
	// Truncated is true when the result had to be shrunk to fit the cap.
	Truncated bool `json:"truncated,omitempty"`
}

// RalphResult is the JSON document the ralph tool returns.
type RalphResult struct {
	Kind      string       `json:"kind"`
	Objective string       `json:"objective"`
	Rounds    []RalphRound `json:"rounds"`
	Completed bool         `json:"completed"`
	Report    RalphReport  `json:"report"`
}

// ralphArgs is the decoded argument object.
type ralphArgs struct {
	Objective string `json:"objective"`
	MaxRounds *int   `json:"max_rounds,omitempty"`
	Agent     string `json:"agent,omitempty"`
}

// RalphTool is the "ralph" tool. All state lives in this instance, guarded by
// mu, so concurrent invocations are safe.
type RalphTool struct {
	mu        sync.Mutex
	runner    runtime.SubagentRunner
	maxRounds int
}

var (
	_ runtime.Tool                 = (*RalphTool)(nil)
	_ runtime.SubagentRunnerBinder = (*RalphTool)(nil)
)

// NewRalphTool returns the ralph tool. When cfg.Runner is nil the tool reports
// a clear error until a runner is bound (runtime.New binds itself).
func NewRalphTool(cfg RalphConfig) runtime.Tool {
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = DefaultRalphMaxRounds
	}
	if maxRounds > MaxRalphRounds {
		maxRounds = MaxRalphRounds
	}
	return &RalphTool{runner: cfg.Runner, maxRounds: maxRounds}
}

// Name implements runtime.Tool.
func (t *RalphTool) Name() string { return "ralph" }

// Description implements runtime.Tool.
func (t *RalphTool) Description() string { return ralphDescription }

// SetSubagentRunner implements runtime.SubagentRunnerBinder. runtime.New calls
// it for every registered tool after the Agent exists.
func (t *RalphTool) SetSubagentRunner(runner runtime.SubagentRunner) {
	t.mu.Lock()
	t.runner = runner
	t.mu.Unlock()
}

// Invoke implements runtime.Tool. Child failures are recorded in the result;
// only argument, configuration, depth and context errors are returned as Go
// errors.
func (t *RalphTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in ralphArgs
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	objective, err := validateRalphObjective(in.Objective)
	if err != nil {
		return "", err
	}
	agent := strings.TrimSpace(in.Agent)

	t.mu.Lock()
	runner := t.runner
	ceiling := t.maxRounds
	t.mu.Unlock()
	if runner == nil {
		return "", errors.New("ralph: no agent runner configured")
	}
	if ceiling <= 0 {
		ceiling = DefaultRalphMaxRounds
	}
	if ceiling > MaxRalphRounds {
		ceiling = MaxRalphRounds
	}
	maxRounds, err := resolveRalphMaxRounds(in.MaxRounds, ceiling)
	if err != nil {
		return "", err
	}

	// A Ralph child runs at depth 1. Starting a loop from inside a child would
	// create depth 2 and is rejected before any agent runs.
	depth := runtime.SubagentDepth(ctx) + 1
	if depth > ralphMaxDepth {
		return "", fmt.Errorf("ralph: depth limit %d reached; start the loop from the root agent", ralphMaxDepth)
	}

	res := RalphResult{
		Kind:      "ralph",
		Objective: objective,
		Rounds:    make([]RalphRound, 0, maxRounds),
	}
	previous := ""
	for round := 1; round <= maxRounds; round++ {
		// Cancellation is honored between rounds and before the first one.
		if err := ctx.Err(); err != nil {
			return "", err
		}

		childCtx := runtime.WithSubagentDepth(ctx, depth)
		task := ralphRoundTask(objective, round, maxRounds, previous)
		result, runErr := runner.RunSubagent(childCtx, runtime.SubagentRequest{
			Task:  task,
			Agent: agent,
			Depth: depth,
		})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}

		current := RalphRound{Round: round}
		if runErr != nil {
			current.Error = truncateBytes(runErr.Error(), ralphErrorBytes, "error truncated")
			res.Rounds = append(res.Rounds, current)
			res.Report = RalphReport{
				Status:          RalphStatusFailed,
				Summary:         fmt.Sprintf("Ralph round %d failed before reporting; no completion marker was seen.", round),
				RoundsStarted:   round,
				RoundsCompleted: round - 1,
				Error:           current.Error,
			}
			return encodeRalphResult(res, ralphMaxOutput)
		}

		current.Output = result.Output
		current.Completed = ralphCompletionMarker(result.Output)
		res.Rounds = append(res.Rounds, current)
		previous = result.Output

		if current.Completed {
			res.Completed = true
			res.Report = RalphReport{
				Status:          RalphStatusComplete,
				Summary:         fmt.Sprintf("Ralph worker reported completion after %d round(s).", round),
				RoundsStarted:   round,
				RoundsCompleted: round,
				LastOutput:      truncateBytes(result.Output, ralphReportDigestBytes, "output truncated"),
			}
			return encodeRalphResult(res, ralphMaxOutput)
		}
	}

	res.Report = RalphReport{
		Status:          RalphStatusMaxRounds,
		Summary:         fmt.Sprintf("Ralph loop reached its %d-round limit without a completion marker.", maxRounds),
		RoundsStarted:   maxRounds,
		RoundsCompleted: maxRounds,
		LastOutput:      truncateBytes(previous, ralphReportDigestBytes, "output truncated"),
	}
	return encodeRalphResult(res, ralphMaxOutput)
}

// validateRalphObjective trims and checks the immutable objective.
func validateRalphObjective(objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", errors.New(`ralph: "objective" is required and must contain non-whitespace text`)
	}
	if strings.ContainsRune(objective, '\x00') {
		return "", errors.New(`ralph: "objective" must not contain NUL bytes`)
	}
	if len(objective) > ralphMaxObjectiveBytes {
		return "", fmt.Errorf("ralph: objective is %d bytes; the limit is %d", len(objective), ralphMaxObjectiveBytes)
	}
	return objective, nil
}

// resolveRalphMaxRounds resolves the model-selected budget against the
// deployment ceiling. An omitted value uses the smaller of the default and the
// ceiling; an explicit value may lower the ceiling but never exceed it.
func resolveRalphMaxRounds(requested *int, ceiling int) (int, error) {
	if ceiling < 1 {
		ceiling = DefaultRalphMaxRounds
	}
	if requested == nil {
		if ceiling < DefaultRalphMaxRounds {
			return ceiling, nil
		}
		return DefaultRalphMaxRounds, nil
	}
	if *requested < 1 {
		return 0, fmt.Errorf("ralph: \"max_rounds\" must be a positive integer, got %d", *requested)
	}
	if *requested > ceiling {
		return 0, fmt.Errorf("ralph: \"max_rounds\" %d exceeds the deployment ceiling %d", *requested, ceiling)
	}
	return *requested, nil
}

// ralphRoundTask builds the self-contained task for one fresh child. Only the
// objective and a compact digest of the previous output cross the round
// boundary.
func ralphRoundTask(objective string, round, maxRounds int, previous string) string {
	prior := "(none - this is the first round)"
	if round > 1 {
		prior = ralphDigest(previous)
	}
	var b strings.Builder
	b.Grow(len(objective) + len(prior) + 700)
	fmt.Fprintf(&b, "You are one fresh worker in a foreground Ralph loop. You receive no parent conversation and no prior child session. Do not call the ralph tool: this round already is its worker.\n\n")
	fmt.Fprintf(&b, "Immutable objective:\n%s\n\n", objective)
	fmt.Fprintf(&b, "Ralph round: %d of %d.\n\n", round, maxRounds)
	fmt.Fprintf(&b, "The shared workspace and its current working tree are the long-term memory and source of truth. Inspect them before acting, preserve existing work, perform concrete in-scope work, and verify what you change. Treat the previous round output only as a bounded handoff; confirm it against the workspace.\n\n")
	fmt.Fprintf(&b, "Previous round output:\n%s\n\n", prior)
	fmt.Fprintf(&b, "When the objective is fully achieved, include the exact word DONE or COMPLETE in your final answer. Otherwise report concrete remaining work.\n")
	return b.String()
}

// ralphDigest compacts one round output for the next round's handoff.
func ralphDigest(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "(no output)"
	}
	return truncateBytes(output, ralphDigestBytes, "previous output truncated")
}

// ralphCompletionMarker reports whether output contains DONE or COMPLETE as a
// standalone, case-insensitive word. Requiring a word start keeps words such as
// "incomplete" from being read as completion.
func ralphCompletionMarker(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range [...]string{"done", "complete"} {
		offset := 0
		for offset < len(lower) {
			i := strings.Index(lower[offset:], marker)
			if i < 0 {
				break
			}
			i += offset
			if i == 0 || !ralphWordByte(lower[i-1]) {
				return true
			}
			offset = i + 1
		}
	}
	return false
}

// ralphWordByte reports whether b belongs to a word. It is only used to require
// a boundary before a completion marker.
func ralphWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	default:
		return b == '_'
	}
}

// encodeRalphResult marshals res and guarantees the complete JSON document
// stays at or below limit bytes. When the document is too large it truncates
// the largest round outputs first (setting report.truncated), so the JSON
// envelope and the round list stay readable instead of collapsing to an error
// stub.
func encodeRalphResult(res RalphResult, limit int) (string, error) {
	if res.Rounds == nil {
		res.Rounds = []RalphRound{}
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("ralph: encode result: %w", err)
	}
	if limit <= 0 || len(encoded) <= limit {
		return string(encoded), nil
	}

	res.Report.Truncated = true
	for attempt := 0; attempt < 16; attempt++ {
		excess := len(encoded) - limit + 128
		if excess <= 0 {
			break
		}
		idx := -1
		for i := range res.Rounds {
			if len(res.Rounds[i].Output) == 0 {
				continue
			}
			if idx < 0 || len(res.Rounds[i].Output) > len(res.Rounds[idx].Output) {
				idx = i
			}
		}
		if idx < 0 {
			break
		}
		keep := len(res.Rounds[idx].Output) - excess
		// Keep a readable remnant instead of erasing a huge round output in
		// one step; the next iteration trims the remaining excess.
		if keep < 512 {
			keep = 512
		}
		if keep >= len(res.Rounds[idx].Output) {
			keep = len(res.Rounds[idx].Output) - 1
		}
		res.Rounds[idx].Output = truncateBytes(res.Rounds[idx].Output, keep, "output truncated")
		encoded, err = json.Marshal(res)
		if err != nil {
			return "", fmt.Errorf("ralph: encode result: %w", err)
		}
	}
	if limit <= 0 || len(encoded) <= limit {
		return string(encoded), nil
	}

	// Last resort: drop the bulky payloads and keep a valid envelope.
	for i := range res.Rounds {
		res.Rounds[i].Output = ""
	}
	res.Report.LastOutput = ""
	res.Report.Summary = truncateBytes(res.Report.Summary, 256, "summary truncated")
	encoded, err = json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("ralph: encode result: %w", err)
	}
	if limit <= 0 || len(encoded) <= limit {
		return string(encoded), nil
	}
	return boundedToolJSON(res, limit, "ralph")
}
