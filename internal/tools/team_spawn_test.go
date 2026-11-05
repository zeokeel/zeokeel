package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/agentteam"
	"github.com/zeokeel/zeokeel/internal/runtime"
)

// teamSpawnFakeRunner is a scripted runtime.SubagentRunner. When release is
// non-nil every call blocks until the test closes it or the member context is
// canceled, which lets tests observe running members and timeouts.
type teamSpawnFakeRunner struct {
	mu      sync.Mutex
	reqs    []runtime.SubagentRequest
	started chan string
	release <-chan struct{}
	err     error
}

var _ runtime.SubagentRunner = (*teamSpawnFakeRunner)(nil)

func (r *teamSpawnFakeRunner) RunSubagent(ctx context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	started := r.started
	release := r.release
	err := r.err
	r.mu.Unlock()

	if started != nil {
		select {
		case started <- req.Task:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return runtime.SubagentResult{}, ctx.Err()
		}
	}
	if err != nil {
		return runtime.SubagentResult{}, err
	}
	return runtime.SubagentResult{
		Agent:  req.Agent,
		Task:   req.Task,
		Output: "done:" + req.Task,
		Depth:  req.Depth,
	}, nil
}

func (r *teamSpawnFakeRunner) requests() []runtime.SubagentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtime.SubagentRequest(nil), r.reqs...)
}

func teamSpawnTestTools(t *testing.T, cfg TeamSpawnConfig) map[string]runtime.Tool {
	t.Helper()
	out := make(map[string]runtime.Tool)
	for _, tool := range NewTeamSpawnTools(cfg) {
		out[tool.Name()] = tool
	}
	for _, name := range []string{"spawn_team", "list_teams", "team_status", "close_team", "wait_team"} {
		tool := out[name]
		if tool == nil {
			t.Fatalf("missing tool %q (got %v)", name, out)
		}
		if strings.TrimSpace(tool.Description()) == "" {
			t.Fatalf("tool %q has an empty description", name)
		}
		if _, ok := tool.(runtime.SubagentRunnerBinder); !ok {
			t.Fatalf("tool %q does not implement runtime.SubagentRunnerBinder", name)
		}
	}
	if len(out) != 5 {
		t.Fatalf("tools = %v, want five", out)
	}
	return out
}

func teamSpawnInvoke(t *testing.T, tool runtime.Tool, args string) (string, error) {
	t.Helper()
	return tool.Invoke(context.Background(), json.RawMessage(args))
}

func TestTeamSpawnToolsSpawnListStatusWaitClose(t *testing.T) {
	runner := &teamSpawnFakeRunner{}
	mgr := agentteam.New(agentteam.Config{Runner: runner})
	defer mgr.Close()
	tools := teamSpawnTestTools(t, TeamSpawnConfig{Manager: mgr})

	out, err := teamSpawnInvoke(t, tools["spawn_team"], `{"tasks":["alpha","beta"],"agent":" worker "}`)
	if err != nil {
		t.Fatalf("spawn_team: %v", err)
	}
	var spawned teamSpawnResult
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("decode spawn_team: %v\n%s", err, out)
	}
	if spawned.Kind != "team_spawn" || spawned.Count != 2 || spawned.Agent != "worker" || len(spawned.Members) != 2 {
		t.Fatalf("spawned = %+v", spawned)
	}
	wantTasks := []string{"alpha", "beta"}
	for i, member := range spawned.Members {
		if member.ID == "" || member.Name != "worker" || member.Task != wantTasks[i] {
			t.Fatalf("member[%d] = %+v, want id/name/task", i, member)
		}
	}

	// A whole-team wait settles both members.
	out, err = teamSpawnInvoke(t, tools["wait_team"], `{"timeout_seconds":5}`)
	if err != nil {
		t.Fatalf("wait_team: %v", err)
	}
	var waited teamWaitResult
	if err := json.Unmarshal([]byte(out), &waited); err != nil {
		t.Fatalf("decode wait_team: %v\n%s", err, out)
	}
	if waited.Kind != "team_wait" || waited.TimedOut || len(waited.Members) != 2 {
		t.Fatalf("waited = %+v", waited)
	}
	for i, member := range waited.Members {
		if member.Status != agentteam.StatusCompleted || member.Output != "done:"+wantTasks[i] {
			t.Fatalf("waited member[%d] = %+v", i, member)
		}
	}
	// Members run concurrently, so the runner observes the batch's requests in
	// scheduling order rather than spawn order: compare them as a multiset.
	requests := runner.requests()
	if len(requests) != len(wantTasks) {
		t.Fatalf("runner requests = %d, want %d", len(requests), len(wantTasks))
	}
	gotTasks := make([]string, 0, len(requests))
	for i, req := range requests {
		if req.Agent != "worker" || req.Depth != 1 {
			t.Fatalf("runner request[%d] = %+v, want agent worker depth 1", i, req)
		}
		gotTasks = append(gotTasks, req.Task)
	}
	slices.Sort(gotTasks)
	if !slices.Equal(gotTasks, wantTasks) {
		t.Fatalf("runner request tasks = %v, want %v", gotTasks, wantTasks)
	}

	out, err = teamSpawnInvoke(t, tools["list_teams"], `{}`)
	if err != nil {
		t.Fatalf("list_teams: %v", err)
	}
	var listed teamListResult
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode list_teams: %v\n%s", err, out)
	}
	if listed.Kind != "team_list" || listed.Count != 2 || len(listed.Members) != 2 {
		t.Fatalf("listed = %+v", listed)
	}
	for _, member := range listed.Members {
		if member.Status != agentteam.StatusCompleted {
			t.Fatalf("listed member = %+v, want completed", member)
		}
	}

	firstID := spawned.Members[0].ID
	out, err = teamSpawnInvoke(t, tools["team_status"], fmt.Sprintf(`{"target":%q}`, firstID))
	if err != nil {
		t.Fatalf("team_status: %v", err)
	}
	var status teamStatusResult
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("decode team_status: %v\n%s", err, out)
	}
	if status.Kind != "team_status" || status.Target != firstID || status.Member.ID != firstID ||
		status.Member.Status != agentteam.StatusCompleted || status.Member.Output != "done:alpha" {
		t.Fatalf("status = %+v", status)
	}
	if _, err := teamSpawnInvoke(t, tools["team_status"], `{"target":"worker"}`); err == nil {
		t.Fatal("team_status with a duplicated name = nil error, want ambiguous target")
	}

	out, err = teamSpawnInvoke(t, tools["close_team"], `{}`)
	if err != nil {
		t.Fatalf("close_team: %v", err)
	}
	var closed teamCloseResult
	if err := json.Unmarshal([]byte(out), &closed); err != nil {
		t.Fatalf("decode close_team: %v\n%s", err, out)
	}
	if closed.Kind != "team_close" || closed.Closed != 2 || len(closed.Members) != 2 {
		t.Fatalf("closed = %+v", closed)
	}
	// Idempotent: a second close succeeds and reports the same team size.
	out, err = teamSpawnInvoke(t, tools["close_team"], `{}`)
	if err != nil {
		t.Fatalf("second close_team: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &closed); err != nil {
		t.Fatalf("decode second close_team: %v", err)
	}
	if closed.Closed != 2 || closed.Stopped != 0 {
		t.Fatalf("second close = %+v, want no running turns stopped", closed)
	}
}

func TestTeamSpawnToolsCapAndValidation(t *testing.T) {
	runner := &teamSpawnFakeRunner{}
	mgr := agentteam.New(agentteam.Config{Runner: runner})
	defer mgr.Close()
	tools := teamSpawnTestTools(t, TeamSpawnConfig{Manager: mgr})

	nine := make([]string, 9)
	for i := range nine {
		nine[i] = fmt.Sprintf("task-%d", i)
	}
	args, err := json.Marshal(map[string]any{"tasks": nine})
	if err != nil {
		t.Fatalf("marshal nine tasks: %v", err)
	}
	if _, err := teamSpawnInvoke(t, tools["spawn_team"], string(args)); err == nil {
		t.Fatal("spawn_team with nine tasks = nil error")
	}
	if got := len(mgr.Members()); got != 0 {
		t.Fatalf("members after rejected batch = %d, want 0", got)
	}

	eight := nine[:8]
	args, err = json.Marshal(map[string]any{"tasks": eight})
	if err != nil {
		t.Fatalf("marshal eight tasks: %v", err)
	}
	out, err := teamSpawnInvoke(t, tools["spawn_team"], string(args))
	if err != nil {
		t.Fatalf("spawn_team with eight tasks: %v", err)
	}
	var spawned teamSpawnResult
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("decode spawn_team: %v", err)
	}
	if spawned.Count != 8 || len(spawned.Members) != 8 {
		t.Fatalf("spawned = %+v, want eight members", spawned)
	}

	for _, args := range []string{
		`{}`,
		`{"tasks":[]}`,
		`{"tasks":["ok"," "]}`, // blank task rejects the whole batch
		`{"tasks":["ok"]} trailing`,
		`[]`,
	} {
		if _, err := teamSpawnInvoke(t, tools["spawn_team"], args); err == nil {
			t.Fatalf("spawn_team(%s) = nil error", args)
		}
	}
	if _, err := teamSpawnInvoke(t, tools["team_status"], `{}`); err == nil {
		t.Fatal("team_status without target = nil error")
	}
	if _, err := teamSpawnInvoke(t, tools["team_status"], `{"target":"missing"}`); err == nil {
		t.Fatal("team_status for an unknown target = nil error")
	}
	if _, err := teamSpawnInvoke(t, tools["wait_team"], `{"target":"missing"}`); err == nil {
		t.Fatal("wait_team for an unknown target = nil error")
	}
	for _, args := range []string{`{"timeout_seconds":-1}`, `{"timeout_seconds":3601}`} {
		if _, err := teamSpawnInvoke(t, tools["wait_team"], args); err == nil {
			t.Fatalf("wait_team(%s) = nil error", args)
		}
	}
	if _, err := teamSpawnInvoke(t, tools["list_teams"], `[]`); err == nil {
		t.Fatal("list_teams with a JSON array = nil error")
	}
	if _, err := teamSpawnInvoke(t, tools["close_team"], `[]`); err == nil {
		t.Fatal("close_team with a JSON array = nil error")
	}

	// A configured manager limit rejects a batch atomically.
	limited := agentteam.New(agentteam.Config{Runner: runner, MaxAgents: 2})
	defer limited.Close()
	limitedTools := teamSpawnTestTools(t, TeamSpawnConfig{Manager: limited})
	if _, err := teamSpawnInvoke(t, limitedTools["spawn_team"], `{"tasks":["a","b","c"]}`); err == nil {
		t.Fatal("spawn_team above the manager limit = nil error")
	}
	if got := len(limited.Members()); got != 0 {
		t.Fatalf("limited members = %d, want 0", got)
	}

	// A missing manager gets a private one; spawning without a runner is a
	// clear error rather than a panic.
	privateTools := teamSpawnTestTools(t, TeamSpawnConfig{})
	if _, err := teamSpawnInvoke(t, privateTools["spawn_team"], `{"tasks":["x"]}`); err == nil {
		t.Fatal("spawn_team without a runner = nil error")
	} else if !strings.Contains(err.Error(), agentteam.ErrNoRunner.Error()) {
		t.Fatalf("spawn_team without a runner error = %v, want ErrNoRunner", err)
	}
}

func TestTeamSpawnToolsWaitTimeoutAndCloseActive(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	runner := &teamSpawnFakeRunner{started: make(chan string, 8), release: release}
	mgr := agentteam.New(agentteam.Config{Runner: runner})
	defer mgr.Close()
	tools := teamSpawnTestTools(t, TeamSpawnConfig{Manager: mgr})

	out, err := teamSpawnInvoke(t, tools["spawn_team"], `{"tasks":["long-a","long-b"],"agent":"worker"}`)
	if err != nil {
		t.Fatalf("spawn_team: %v", err)
	}
	var spawned teamSpawnResult
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("decode spawn_team: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-runner.started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the fake runner to start")
		}
	}

	out, err = teamSpawnInvoke(t, tools["wait_team"],
		fmt.Sprintf(`{"target":%q,"timeout_seconds":0.1}`, spawned.Members[0].ID))
	if err != nil {
		t.Fatalf("wait_team target timeout: %v", err)
	}
	var waited teamWaitResult
	if err := json.Unmarshal([]byte(out), &waited); err != nil {
		t.Fatalf("decode targeted wait: %v\n%s", err, out)
	}
	if !waited.TimedOut || waited.Member == nil || waited.Member.Status != agentteam.StatusRunning {
		t.Fatalf("targeted wait = %+v, want timed out while running", waited)
	}

	out, err = teamSpawnInvoke(t, tools["wait_team"], `{"timeout_seconds":0.1}`)
	if err != nil {
		t.Fatalf("wait_team whole team timeout: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &waited); err != nil {
		t.Fatalf("decode whole-team wait: %v\n%s", err, out)
	}
	if !waited.TimedOut || len(waited.Members) != 2 {
		t.Fatalf("whole-team wait = %+v, want timed out with two members", waited)
	}
	for _, member := range waited.Members {
		if member.Status != agentteam.StatusRunning {
			t.Fatalf("whole-team wait member = %+v, want running", member)
		}
	}

	out, err = teamSpawnInvoke(t, tools["close_team"], `{}`)
	if err != nil {
		t.Fatalf("close_team: %v", err)
	}
	var closed teamCloseResult
	if err := json.Unmarshal([]byte(out), &closed); err != nil {
		t.Fatalf("decode close_team: %v\n%s", err, out)
	}
	if closed.Closed != 2 || closed.Stopped != 2 || len(closed.Members) != 2 {
		t.Fatalf("closed = %+v, want two closed and two stopped", closed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, member := range spawned.Members {
		settled, err := mgr.Wait(ctx, member.ID)
		if err != nil {
			t.Fatalf("Wait(%s) after close: %v", member.ID, err)
		}
		if settled.Status != agentteam.StatusInterrupted {
			t.Fatalf("settled = %+v, want interrupted", settled)
		}
	}
	if calls := len(runner.requests()); calls != 2 {
		t.Fatalf("runner calls = %d, want 2", calls)
	}

	out, err = teamSpawnInvoke(t, tools["close_team"], `{}`)
	if err != nil {
		t.Fatalf("second close_team: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &closed); err != nil {
		t.Fatalf("decode second close_team: %v", err)
	}
	if closed.Closed != 2 || closed.Stopped != 0 {
		t.Fatalf("second close = %+v, want idempotent no-op", closed)
	}
}

func TestTeamSpawnToolsWaitReportsFailure(t *testing.T) {
	runner := &teamSpawnFakeRunner{err: errors.New("boom")}
	mgr := agentteam.New(agentteam.Config{Runner: runner})
	defer mgr.Close()
	tools := teamSpawnTestTools(t, TeamSpawnConfig{Manager: mgr})

	out, err := teamSpawnInvoke(t, tools["spawn_team"], `{"tasks":["fails"]}`)
	if err != nil {
		t.Fatalf("spawn_team: %v", err)
	}
	var spawned teamSpawnResult
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("decode spawn_team: %v", err)
	}
	out, err = teamSpawnInvoke(t, tools["wait_team"],
		fmt.Sprintf(`{"target":%q,"timeout_seconds":5}`, spawned.Members[0].ID))
	if err != nil {
		t.Fatalf("wait_team: %v", err)
	}
	var waited teamWaitResult
	if err := json.Unmarshal([]byte(out), &waited); err != nil {
		t.Fatalf("decode wait_team: %v\n%s", err, out)
	}
	if waited.TimedOut || waited.Member == nil || waited.Member.Status != agentteam.StatusFailed ||
		waited.Member.Error != "boom" {
		t.Fatalf("waited = %+v, want failed member with error", waited)
	}
	if waited.Member.Output != "" {
		t.Fatalf("failed member output = %q, want empty", waited.Member.Output)
	}

	out, err = teamSpawnInvoke(t, tools["list_teams"], `{}`)
	if err != nil {
		t.Fatalf("list_teams: %v", err)
	}
	var listed teamListResult
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode list_teams: %v", err)
	}
	if len(listed.Members) != 1 || listed.Members[0].Status != agentteam.StatusFailed {
		t.Fatalf("listed = %+v, want one failed member", listed)
	}
}

func TestTeamSpawnToolsCloseAfterManagerCloseIsIdempotent(t *testing.T) {
	runner := &teamSpawnFakeRunner{}
	mgr := agentteam.New(agentteam.Config{Runner: runner})
	tools := teamSpawnTestTools(t, TeamSpawnConfig{Manager: mgr})

	if _, err := teamSpawnInvoke(t, tools["spawn_team"], `{"tasks":["work"]}`); err != nil {
		t.Fatalf("spawn_team: %v", err)
	}
	mgr.Close()

	out, err := teamSpawnInvoke(t, tools["close_team"], `{}`)
	if err != nil {
		t.Fatalf("close_team after manager close: %v", err)
	}
	var closed teamCloseResult
	if err := json.Unmarshal([]byte(out), &closed); err != nil {
		t.Fatalf("decode close_team: %v\n%s", err, out)
	}
	if closed.Kind != "team_close" || closed.Closed != 1 || len(closed.Members) != 1 {
		t.Fatalf("closed = %+v, want one closed member", closed)
	}
	if _, err := teamSpawnInvoke(t, tools["close_team"], `{}`); err != nil {
		t.Fatalf("second close_team after manager close: %v", err)
	}
}
