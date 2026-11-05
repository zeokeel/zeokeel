package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// ralphFakeRunner scripts fresh child-agent rounds for the ralph tests. It
// records every request and returns the output/error for the matching round.
type ralphFakeRunner struct {
	mu      sync.Mutex
	reqs    []runtime.SubagentRequest
	outputs []string
	errs    []error
	// cancel, when set, is invoked after the first round is recorded. It models
	// a parent context cancelled while a child is running.
	cancel context.CancelFunc
}

var _ runtime.SubagentRunner = (*ralphFakeRunner)(nil)

func (r *ralphFakeRunner) RunSubagent(_ context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	index := len(r.reqs)
	r.reqs = append(r.reqs, req)
	var output string
	if index < len(r.outputs) {
		output = r.outputs[index]
	}
	var err error
	if index < len(r.errs) {
		err = r.errs[index]
	}
	cancel := r.cancel
	r.mu.Unlock()

	if index == 0 && cancel != nil {
		cancel()
	}
	if err != nil {
		return runtime.SubagentResult{}, err
	}
	return runtime.SubagentResult{
		Agent:  req.Agent,
		Task:   req.Task,
		Output: output,
		Depth:  req.Depth,
	}, nil
}

func (r *ralphFakeRunner) requests() []runtime.SubagentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.SubagentRequest(nil), r.reqs...)
}

// newRalphRunnerTool builds the tool and returns both the runtime.Tool and the
// concrete pointer for assertions.
func newRalphRunnerTool(t *testing.T, cfg RalphConfig) *RalphTool {
	t.Helper()
	tool, ok := NewRalphTool(cfg).(*RalphTool)
	if !ok {
		t.Fatalf("NewRalphTool returned %T, want *RalphTool", NewRalphTool(cfg))
	}
	return tool
}

// decodeRalphResult unmarshals a ralph tool result.
func decodeRalphResult(t *testing.T, raw string) RalphResult {
	t.Helper()
	var res RalphResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("ralph result is not valid JSON: %v\n%s", err, raw)
	}
	return res
}

func TestRalphSingleRoundCompletion(t *testing.T) {
	runner := &ralphFakeRunner{outputs: []string{"Everything passes. DONE"}}
	tool := newRalphRunnerTool(t, RalphConfig{Runner: runner})

	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"  make the tests pass  ","agent":" worker "}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	res := decodeRalphResult(t, raw)
	if res.Kind != "ralph" {
		t.Fatalf("kind = %q, want ralph", res.Kind)
	}
	if res.Objective != "make the tests pass" {
		t.Fatalf("objective = %q, want the trimmed objective", res.Objective)
	}
	if !res.Completed {
		t.Fatalf("completed = false, want true")
	}
	if len(res.Rounds) != 1 {
		t.Fatalf("rounds = %d, want 1: %s", len(res.Rounds), raw)
	}
	round := res.Rounds[0]
	if round.Round != 1 || !round.Completed || round.Output != "Everything passes. DONE" {
		t.Fatalf("round = %+v", round)
	}
	if res.Report.Status != RalphStatusComplete || res.Report.RoundsStarted != 1 || res.Report.RoundsCompleted != 1 {
		t.Fatalf("report = %+v", res.Report)
	}
	if res.Report.LastOutput == "" {
		t.Fatalf("report.last_output is empty")
	}

	reqs := runner.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Agent != "worker" || reqs[0].Depth != ralphMaxDepth {
		t.Fatalf("request = %+v, want agent worker at depth 1", reqs[0])
	}
	if !strings.Contains(reqs[0].Task, "make the tests pass") {
		t.Fatalf("task does not carry the objective:\n%s", reqs[0].Task)
	}
	if !strings.Contains(reqs[0].Task, "Ralph round: 1 of 3") {
		t.Fatalf("task does not name the round:\n%s", reqs[0].Task)
	}
	if tool.Name() != "ralph" || strings.TrimSpace(tool.Description()) == "" {
		t.Fatalf("tool metadata = %q / %q", tool.Name(), tool.Description())
	}
}

func TestRalphMultiRoundUntilMarker(t *testing.T) {
	runner := &ralphFakeRunner{outputs: []string{
		"round one: parser ported, tests still failing",
		"round two: fixed the lexer",
		"all green now -- COMPLETE",
	}}
	tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 5})

	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"repair the parser","max_rounds":5,"agent":"builder"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	res := decodeRalphResult(t, raw)
	if !res.Completed || res.Report.Status != RalphStatusComplete {
		t.Fatalf("completed = %v, report = %+v", res.Completed, res.Report)
	}
	if len(res.Rounds) != 3 {
		t.Fatalf("rounds = %d, want 3: %s", len(res.Rounds), raw)
	}
	for i, round := range res.Rounds {
		wantRound := i + 1
		if round.Round != wantRound {
			t.Fatalf("round[%d].round = %d, want %d", i, round.Round, wantRound)
		}
		if wantCompleted := i == 2; round.Completed != wantCompleted {
			t.Fatalf("round[%d].completed = %v, want %v", i, round.Completed, wantCompleted)
		}
	}

	reqs := runner.requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	for i, req := range reqs {
		if req.Depth != ralphMaxDepth {
			t.Fatalf("request[%d].depth = %d, want %d", i, req.Depth, ralphMaxDepth)
		}
		if req.Agent != "builder" {
			t.Fatalf("request[%d].agent = %q, want builder", i, req.Agent)
		}
		if !strings.Contains(req.Task, "repair the parser") {
			t.Fatalf("request[%d].task lost the immutable objective", i)
		}
		if i > 0 && !strings.Contains(req.Task, runner.outputs[i-1]) {
			t.Fatalf("request[%d].task does not carry the previous output digest:\n%s", i, req.Task)
		}
	}
	if strings.Contains(reqs[2].Task, runner.outputs[0]) {
		t.Fatalf("round 3 task should carry only the previous round, not the whole transcript:\n%s", reqs[2].Task)
	}
	if !strings.Contains(reqs[0].Task, "first round") {
		t.Fatalf("round 1 task does not mark itself as the first round:\n%s", reqs[0].Task)
	}
}

func TestRalphMaxRoundsCap(t *testing.T) {
	t.Run("default budget", func(t *testing.T) {
		runner := &ralphFakeRunner{outputs: []string{"still going 1", "still going 2", "still going 3", "still going 4"}}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner})

		raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"keep improving"}`))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		res := decodeRalphResult(t, raw)
		if res.Completed {
			t.Fatalf("completed = true without a marker")
		}
		if len(res.Rounds) != DefaultRalphMaxRounds || len(runner.requests()) != DefaultRalphMaxRounds {
			t.Fatalf("rounds = %d / requests = %d, want %d", len(res.Rounds), len(runner.requests()), DefaultRalphMaxRounds)
		}
		if res.Report.Status != RalphStatusMaxRounds {
			t.Fatalf("report.status = %q, want %q", res.Report.Status, RalphStatusMaxRounds)
		}
		if res.Report.RoundsStarted != DefaultRalphMaxRounds {
			t.Fatalf("report.rounds_started = %d", res.Report.RoundsStarted)
		}
	})

	t.Run("deployment ceiling", func(t *testing.T) {
		runner := &ralphFakeRunner{outputs: []string{"still going 1", "still going 2", "still going 3"}}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 2})

		raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"keep improving","max_rounds":2}`))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		res := decodeRalphResult(t, raw)
		if len(res.Rounds) != 2 || len(runner.requests()) != 2 {
			t.Fatalf("rounds = %d / requests = %d, want 2", len(res.Rounds), len(runner.requests()))
		}
		if res.Report.Status != RalphStatusMaxRounds {
			t.Fatalf("report.status = %q", res.Report.Status)
		}
	})

	t.Run("absolute cap", func(t *testing.T) {
		runner := &ralphFakeRunner{outputs: []string{"DONE"}}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 99})

		raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"bounded","max_rounds":10}`))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if res := decodeRalphResult(t, raw); !res.Completed || len(res.Rounds) != 1 {
			t.Fatalf("result = %+v", res)
		}
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"bounded","max_rounds":11}`)); err == nil {
			t.Fatalf("max_rounds 11 was accepted above the absolute cap")
		}
	})
}

func TestRalphDepthRejection(t *testing.T) {
	for _, depth := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			runner := &ralphFakeRunner{outputs: []string{"DONE"}}
			tool := newRalphRunnerTool(t, RalphConfig{Runner: runner})
			ctx := runtime.WithSubagentDepth(context.Background(), depth)

			_, err := tool.Invoke(ctx, json.RawMessage(`{"objective":"nested loop"}`))
			if err == nil {
				t.Fatalf("depth %d was accepted", depth)
			}
			if !strings.Contains(err.Error(), "depth limit") {
				t.Fatalf("error = %v, want a depth-limit error", err)
			}
			if got := len(runner.requests()); got != 0 {
				t.Fatalf("requests = %d, want 0 before depth rejection", got)
			}
		})
	}
}

func TestRalphRunnerErrorHandling(t *testing.T) {
	t.Run("failure on first round", func(t *testing.T) {
		runner := &ralphFakeRunner{errs: []error{errors.New("model unavailable")}}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 4})

		raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"do work"}`))
		if err != nil {
			t.Fatalf("Invoke returned an error for a child failure: %v", err)
		}
		res := decodeRalphResult(t, raw)
		if res.Completed {
			t.Fatalf("completed = true after a child failure")
		}
		if len(res.Rounds) != 1 || res.Rounds[0].Error == "" {
			t.Fatalf("round = %+v, want one round recording the error", res.Rounds)
		}
		if res.Report.Status != RalphStatusFailed || res.Report.Error == "" {
			t.Fatalf("report = %+v, want failed with an error", res.Report)
		}
		if len(runner.requests()) != 1 {
			t.Fatalf("requests = %d, want the loop to stop after the failure", len(runner.requests()))
		}
	})

	t.Run("failure stops after progress", func(t *testing.T) {
		runner := &ralphFakeRunner{
			outputs: []string{"some progress", ""},
			errs:    []error{nil, errors.New("boom")},
		}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 5})

		raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"do work","max_rounds":5}`))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		res := decodeRalphResult(t, raw)
		if len(res.Rounds) != 2 {
			t.Fatalf("rounds = %d, want 2: %s", len(res.Rounds), raw)
		}
		if res.Rounds[1].Error == "" || !strings.Contains(res.Rounds[1].Error, "boom") {
			t.Fatalf("round 2 error = %q", res.Rounds[1].Error)
		}
		if res.Report.Status != RalphStatusFailed || res.Report.RoundsStarted != 2 || res.Report.RoundsCompleted != 1 {
			t.Fatalf("report = %+v", res.Report)
		}
		if len(runner.requests()) != 2 {
			t.Fatalf("requests = %d, want 2", len(runner.requests()))
		}
	})

	t.Run("missing runner", func(t *testing.T) {
		tool := newRalphRunnerTool(t, RalphConfig{})
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"do work"}`)); err == nil {
			t.Fatalf("Invoke succeeded without a runner")
		}
	})
}

func TestRalphCancellation(t *testing.T) {
	t.Run("before first round", func(t *testing.T) {
		runner := &ralphFakeRunner{outputs: []string{"DONE"}}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		raw, err := tool.Invoke(ctx, json.RawMessage(`{"objective":"do work"}`))
		if err != nil {
			t.Fatalf("Invoke error = %v, want a structured cancelled result", err)
		}
		res := decodeRalphResult(t, raw)
		if res.Report.Status != RalphStatusCancelled {
			t.Fatalf("status = %q, want %q", res.Report.Status, RalphStatusCancelled)
		}
		if len(runner.requests()) != 0 {
			t.Fatalf("requests = %d, want 0", len(runner.requests()))
		}
	})

	t.Run("between rounds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runner := &ralphFakeRunner{
			outputs: []string{"first round still working", "second round should never run"},
			cancel:  cancel,
		}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 5})

		raw, err := tool.Invoke(ctx, json.RawMessage(`{"objective":"do work"}`))
		if err != nil {
			t.Fatalf("Invoke error = %v, want a structured cancelled result", err)
		}
		res := decodeRalphResult(t, raw)
		if res.Report.Status != RalphStatusCancelled || res.Report.RoundsStarted != 1 || res.Report.RoundsCompleted != 1 {
			t.Fatalf("report = %+v, want cancelled after one completed round", res.Report)
		}
		if got := len(runner.requests()); got != 1 {
			t.Fatalf("requests = %d, want 1 (the loop must stop after cancellation)", got)
		}
	})

	t.Run("during a round", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runner := &ralphBlockingRunner{started: make(chan struct{}, 1)}
		tool := newRalphRunnerTool(t, RalphConfig{Runner: runner})

		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			raw, err := tool.Invoke(ctx, json.RawMessage(`{"objective":"do work"}`))
			resultCh <- raw
			errCh <- err
		}()
		select {
		case <-runner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("child did not start")
		}
		cancel()
		var raw string
		select {
		case raw = <-resultCh:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled ralph did not return")
		}
		if err := <-errCh; err != nil {
			t.Fatalf("Invoke error = %v, want a structured cancelled result", err)
		}
		res := decodeRalphResult(t, raw)
		if res.Report.Status != RalphStatusCancelled {
			t.Fatalf("status = %q, want %q", res.Report.Status, RalphStatusCancelled)
		}
		if runner.calls != 1 {
			t.Fatalf("child calls = %d, want 1", runner.calls)
		}
	})
}

// ralphBlockingRunner blocks one round until its context is cancelled, proving
// a stopped loop also stops the running child.
type ralphBlockingRunner struct {
	started chan struct{}
	calls   int
}

func (r *ralphBlockingRunner) RunSubagent(ctx context.Context, _ runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.calls++
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return runtime.SubagentResult{}, ctx.Err()
}

func TestRalphOutputCap(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "plain", output: strings.Repeat("x", 200*1024)},
		{name: "escape heavy", output: strings.Repeat("\"\\\n\t", 50*1024)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &ralphFakeRunner{outputs: []string{tc.output}}
			tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 1})

			raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"huge output","max_rounds":1}`))
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if len(raw) > DefaultMaxOutputBytes {
				t.Fatalf("result is %d bytes, want at most %d", len(raw), DefaultMaxOutputBytes)
			}
			res := decodeRalphResult(t, raw)
			if res.Kind != "ralph" || len(res.Rounds) != 1 {
				t.Fatalf("result lost its envelope: %+v", res)
			}
			if !res.Report.Truncated {
				t.Fatalf("report.truncated = false, want true")
			}
			if len(res.Rounds[0].Output) >= len(tc.output) {
				t.Fatalf("round output is not truncated (%d bytes)", len(res.Rounds[0].Output))
			}
		})
	}
}

func TestRalphValidation(t *testing.T) {
	runner := &ralphFakeRunner{outputs: []string{"DONE"}}
	tool := newRalphRunnerTool(t, RalphConfig{Runner: runner, MaxRounds: 10})

	tests := []struct {
		name string
		args string
	}{
		{name: "empty args", args: ``},
		{name: "missing objective", args: `{}`},
		{name: "blank objective", args: `{"objective":"   \n\t "}`},
		{name: "wrong objective type", args: `{"objective":42}`},
		{name: "non-object args", args: `[]`},
		{name: "zero rounds", args: `{"objective":"ok","max_rounds":0}`},
		{name: "negative rounds", args: `{"objective":"ok","max_rounds":-1}`},
		{name: "fractional rounds", args: `{"objective":"ok","max_rounds":1.5}`},
		{name: "over ceiling", args: `{"objective":"ok","max_rounds":11}`},
		{name: "huge objective", args: `{"objective":"` + strings.Repeat("a", ralphMaxObjectiveBytes+1) + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if raw, err := tool.Invoke(context.Background(), json.RawMessage(tc.args)); err == nil {
				t.Fatalf("Invoke accepted %q and returned %q", tc.args, raw)
			}
		})
	}
	if got := len(runner.requests()); got != 0 {
		t.Fatalf("validation failures started %d child rounds", got)
	}

	// The deployment ceiling, not the absolute cap, rejects a request above it.
	small := newRalphRunnerTool(t, RalphConfig{Runner: runner})
	if _, err := small.Invoke(context.Background(), json.RawMessage(`{"objective":"ok","max_rounds":4}`)); err == nil {
		t.Fatalf("max_rounds 4 was accepted above the default ceiling of 3")
	}
	// A lower explicit budget is honored.
	if raw, err := small.Invoke(context.Background(), json.RawMessage(`{"objective":"ok","max_rounds":1}`)); err != nil {
		t.Fatalf("Invoke with max_rounds 1: %v", err)
	} else if res := decodeRalphResult(t, raw); len(res.Rounds) != 1 {
		t.Fatalf("rounds = %d, want 1", len(res.Rounds))
	}
}

func TestRalphCompletionMarkerDetection(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{output: "DONE", want: true},
		{output: "finished; done.", want: true},
		{output: "COMPLETE", want: true},
		{output: "The build is completed", want: true},
		{output: "Done with the migration", want: true},
		{output: "incomplete work remains", want: false},
		{output: "abandoned by the previous worker", want: false},
		{output: "still running", want: false},
		{output: "", want: false},
	}
	for _, tc := range tests {
		if got := ralphCompletionMarker(tc.output); got != tc.want {
			t.Fatalf("ralphCompletionMarker(%q) = %v, want %v", tc.output, got, tc.want)
		}
	}
}

func TestRalphBinderAndDefaults(t *testing.T) {
	var _ runtime.SubagentRunnerBinder = (*RalphTool)(nil)

	runner := &ralphFakeRunner{outputs: []string{"COMPLETE"}}
	rawTool := NewRalphTool(RalphConfig{})
	tool, ok := rawTool.(*RalphTool)
	if !ok {
		t.Fatalf("NewRalphTool returned %T", rawTool)
	}
	if tool.maxRounds != DefaultRalphMaxRounds {
		t.Fatalf("default maxRounds = %d, want %d", tool.maxRounds, DefaultRalphMaxRounds)
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"bind me"}`)); err == nil {
		t.Fatalf("Invoke succeeded before SetSubagentRunner")
	}
	tool.SetSubagentRunner(runner)
	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"objective":"bind me"}`))
	if err != nil {
		t.Fatalf("Invoke after SetSubagentRunner: %v", err)
	}
	if res := decodeRalphResult(t, raw); !res.Completed {
		t.Fatalf("result = %+v", res)
	}

	capped := NewRalphTool(RalphConfig{Runner: runner, MaxRounds: 99}).(*RalphTool)
	if capped.maxRounds != MaxRalphRounds {
		t.Fatalf("maxRounds = %d, want the absolute cap %d", capped.maxRounds, MaxRalphRounds)
	}
}
