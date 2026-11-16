package toolkit

// Tests for the session header's background-jobs service (contracts.JobService).
// Rig's background manager lives inside the root-scoped build that is rebuilt on
// every workspace switch, so the service must resolve the live job tools on
// every call (never cache a retired manager) and must decode Rig's stable
// job_list text format into the UI read model.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
	"github.com/zeokeel/zeokeel-rig/tools/jobs"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// jobServiceStubTool is a deterministic rig.Tool standing in for one of the
// manager's job_* tools.
type jobServiceStubTool struct {
	name     string
	result   rig.ToolResult
	err      error
	calls    int
	lastCall rig.ToolCall
}

func (s *jobServiceStubTool) Name() string           { return s.name }
func (s *jobServiceStubTool) Description() string    { return "stub " + s.name }
func (s *jobServiceStubTool) Schema() map[string]any { return map[string]any{"type": "object"} }

func (s *jobServiceStubTool) Invoke(_ context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	s.calls++
	s.lastCall = call
	return s.result, s.err
}

// jobServiceScopedBuild builds a fake root-scoped tool build whose only tools
// are the given job tools, so the service can be exercised without spawning
// processes or touching a real workspace.
func jobServiceScopedBuild(root string, tools ...rig.Tool) *rootScoped {
	byName := make(map[string]rig.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	return &rootScoped{root: root, mode: sandbox.ModeWorkspaceWrite, tools: tools, byName: byName, done: make(chan struct{})}
}

// jobServiceListing is one realistic job_list payload with every documented
// variant: an owned running job, an exited job without owner, a killed terminal
// job and a UTF-8 command.
const jobServiceListing = `jobs: 4
- job-a [bash] owner=alice state=running status=running (running) exit=-1 duration_ms=1503 command=sleep 30
- job-b [pwsh] state=exited status=completed (exit code: 0) exit=0 duration_ms=42 command=echo done
- job-c [terminal] state=killed status=killed (killed) exit=-1 duration_ms=900 command=tail -f /dev/null
- job-d [bash] state=exited status=completed (exit code: 1) exit=1 duration_ms=7 command=echo 后台任务
`

// TestParseJobList pins the decoder for Rig's documented job_list format:
// "jobs: N" then one "- id [kind] owner=…? state=… status=… exit=… duration_ms=…
// command=…" line per retained job. The owner segment is optional and must not
// leak into state, status is free text up to " exit=", and command is the rest
// of the line.
func TestParseJobList(t *testing.T) {
	want := []contracts.BackgroundJob{
		{
			ID: "job-a", Kind: "bash", Command: "sleep 30",
			Status: "running (running)", State: "running",
			ExitCode: -1, Duration: 1503 * time.Millisecond, Running: true,
		},
		{
			ID: "job-b", Kind: "pwsh", Command: "echo done",
			Status: "completed (exit code: 0)", State: "exited",
			ExitCode: 0, Duration: 42 * time.Millisecond,
		},
		{
			ID: "job-c", Kind: "terminal", Command: "tail -f /dev/null",
			Status: "killed (killed)", State: "killed",
			ExitCode: -1, Duration: 900 * time.Millisecond,
		},
		{
			ID: "job-d", Kind: "bash", Command: "echo 后台任务",
			Status: "completed (exit code: 1)", State: "exited",
			ExitCode: 1, Duration: 7 * time.Millisecond,
		},
	}
	if got := parseJobList(jobServiceListing); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseJobList() = %#v, want %#v", got, want)
	}
}

// TestParseJobListEmpty covers the empty manager output ("no jobs", which is
// Rig's sentinel) and tolerant handling of blank/header-only text.
func TestParseJobListEmpty(t *testing.T) {
	for name, text := range map[string]string{
		"empty":        "",
		"blank":        "\n \t\n",
		"no jobs":      "no jobs",
		"no jobs crlf": "no jobs\r\n",
		"header only":  "jobs: 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := parseJobList(text); len(got) != 0 {
				t.Fatalf("parseJobList(%q) = %#v, want no rows", text, got)
			}
		})
	}
}

// TestParseJobListSkipsMalformedRows keeps the header chip usable when the
// tool output has trailing noise or an unparseable row: valid rows survive.
func TestParseJobListSkipsMalformedRows(t *testing.T) {
	text := "jobs: 2\n" +
		"- not a job row\n" +
		"- job-ok [bash] state=running status=running (running) exit=-1 duration_ms=5 command=sleep 1\n" +
		"trailing noise\n"
	got := parseJobList(text)
	if len(got) != 1 || got[0].ID != "job-ok" || got[0].Command != "sleep 1" {
		t.Fatalf("parseJobList() = %#v, want only job-ok", got)
	}
}

// TestJobServiceJobsReadsLiveManager proves the read path invokes the rig
// job_list tool of the build that is current and maps its rows.
func TestJobServiceJobsReadsLiveManager(t *testing.T) {
	list := &jobServiceStubTool{name: jobs.ToolJobList, result: rig.ToolResult{Content: jobServiceListing}}
	group := &rebindGroup{
		rootOf:  func() string { return "" },
		current: jobServiceScopedBuild("", list),
	}
	service := newJobService(group)
	var _ contracts.JobService = service

	got := service.Jobs()
	if len(got) != 4 {
		t.Fatalf("Jobs() rows = %d, want 4 (%#v)", len(got), got)
	}
	if got[0].ID != "job-a" || !got[0].Running || got[0].ExitCode != -1 {
		t.Fatalf("first row = %#v, want the running job-a", got[0])
	}
	if got[1].Running {
		t.Fatalf("exited job %q marked running", got[1].ID)
	}
	if list.calls != 1 {
		t.Fatalf("job_list invoked %d times, want 1", list.calls)
	}
	if list.lastCall.Name != jobs.ToolJobList || strings.TrimSpace(list.lastCall.Arguments) != "{}" {
		t.Fatalf("job_list call = %+v, want empty JSON args", list.lastCall)
	}
}

// TestJobServiceJobsDegradesOnFailure: the header chip is read-only chrome and
// must show nothing instead of panicking when the listing tool errors.
func TestJobServiceJobsDegradesOnFailure(t *testing.T) {
	tests := map[string]*jobServiceStubTool{
		"tool error result": {name: jobs.ToolJobList, result: rig.ToolResult{Content: "boom", IsError: true}},
		"invoke error":      {name: jobs.ToolJobList, err: errors.New("invoke exploded")},
		"garbage output":    {name: jobs.ToolJobList, result: rig.ToolResult{Content: "totally not a listing"}},
	}
	for name, list := range tests {
		t.Run(name, func(t *testing.T) {
			group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", list)}
			if got := newJobService(group).Jobs(); len(got) != 0 {
				t.Fatalf("Jobs() = %#v, want no rows", got)
			}
		})
	}
}

// TestJobServiceKillJobPassesID pins the control path: KillJob drives the
// model-facing job_kill tool with a {"job_id":…} payload.
func TestJobServiceKillJobPassesID(t *testing.T) {
	kill := &jobServiceStubTool{
		name:   jobs.ToolJobKill,
		result: rig.ToolResult{Content: "outcome: cancellation-requested\njob_id: job-7\nstate: killed\n"},
	}
	group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", kill)}

	if err := newJobService(group).KillJob("job-7"); err != nil {
		t.Fatalf("KillJob: %v", err)
	}
	if kill.calls != 1 || kill.lastCall.Name != jobs.ToolJobKill {
		t.Fatalf("job_kill calls = %d (call %+v), want 1", kill.calls, kill.lastCall)
	}
	var args struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal([]byte(kill.lastCall.Arguments), &args); err != nil {
		t.Fatalf("job_kill arguments %q are not JSON: %v", kill.lastCall.Arguments, err)
	}
	if args.JobID != "job-7" {
		t.Fatalf("job_kill job_id = %q, want job-7", args.JobID)
	}
}

// TestJobServiceKillJobErrors covers the contract's error cases: unknown and
// finished ids report an error, an empty id is rejected without a tool call,
// and tool/invoke failures surface.
func TestJobServiceKillJobErrors(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		result    rig.ToolResult
		invokeErr error
		want      string
		wantCalls int
	}{
		{
			name: "empty id",
			id:   "   ",
			want: "required",
		},
		{
			name:      "unknown id",
			id:        "nope",
			result:    rig.ToolResult{Content: `jobs: unknown job "nope"`, IsError: true},
			want:      "unknown job",
			wantCalls: 1,
		},
		{
			name:      "already finished",
			id:        "job-done",
			result:    rig.ToolResult{Content: "outcome: already-finished\nstate: exited\n"},
			want:      "already finished",
			wantCalls: 1,
		},
		{
			name:      "invoke failure",
			id:        "job-a",
			invokeErr: errors.New("invoke exploded"),
			want:      "invoke exploded",
			wantCalls: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			kill := &jobServiceStubTool{name: jobs.ToolJobKill, result: testCase.result, err: testCase.invokeErr}
			group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", kill)}
			err := newJobService(group).KillJob(testCase.id)
			if err == nil {
				t.Fatalf("KillJob(%q) error = nil, want one containing %q", testCase.id, testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("KillJob(%q) error = %q, want one containing %q", testCase.id, err, testCase.want)
			}
			if kill.calls != testCase.wantCalls {
				t.Fatalf("job_kill invoked %d times, want %d", kill.calls, testCase.wantCalls)
			}
		})
	}
}

// TestJobServiceResolvesTheLiveBuildPerCall is the stale-manager regression: a
// workspace switch installs a fresh root-scoped build (and jobs manager); the
// service must read the new one on the next call, while the header's service
// value itself is never re-provided.
func TestJobServiceResolvesTheLiveBuildPerCall(t *testing.T) {
	listA := &jobServiceStubTool{name: jobs.ToolJobList, result: rig.ToolResult{
		Content: "jobs: 1\n- job-a [bash] state=running status=running (running) exit=-1 duration_ms=1 command=sleep 1\n",
	}}
	listB := &jobServiceStubTool{name: jobs.ToolJobList, result: rig.ToolResult{
		Content: "jobs: 1\n- job-b [bash] state=running status=running (running) exit=-1 duration_ms=2 command=sleep 2\n",
	}}
	scopedA := jobServiceScopedBuild("root-a", listA)
	scopedB := jobServiceScopedBuild("root-b", listB)

	root := "root-a"
	group := &rebindGroup{rootOf: func() string { return root }, current: scopedA}
	service := newJobService(group)
	if rows := service.Jobs(); len(rows) != 1 || rows[0].ID != "job-a" {
		t.Fatalf("Jobs() before rebind = %#v, want job-a", rows)
	}

	// This is exactly what forRoot installs on a workspace switch; the service
	// is deliberately not told about it.
	root = "root-b"
	group.mu.Lock()
	group.current = scopedB
	group.mu.Unlock()

	if rows := service.Jobs(); len(rows) != 1 || rows[0].ID != "job-b" {
		t.Fatalf("Jobs() after rebind = %#v, want job-b (stale manager)", rows)
	}
	if listA.calls != 1 || listB.calls != 1 {
		t.Fatalf("job_list calls = A:%d B:%d, want one each", listA.calls, listB.calls)
	}
}

// TestJobServiceFollowsRealWorkspaceRebind exercises the real forRoot path:
// after the workspace root changes, the service talks to the newly built
// manager and the old build is released.
func TestJobServiceFollowsRealWorkspaceRebind(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return current }, nil, nil)
	defer group.close()
	// Module activation builds the initial root-scoped tools; the service then
	// lists that build's retained jobs.
	group.initial()

	service := newJobService(group)
	if rows := service.Jobs(); len(rows) != 0 {
		t.Fatalf("Jobs() on empty manager = %#v, want no rows", rows)
	}
	group.mu.Lock()
	scopedA := group.current
	group.mu.Unlock()
	if scopedA == nil || scopedA.root != rootA {
		t.Fatalf("first build root = %+v, want %q", scopedA, rootA)
	}

	current = rootB
	// A tool call under the new root creates its build; listing then reflects
	// that root only.
	group.forRoot()
	if rows := service.Jobs(); len(rows) != 0 {
		t.Fatalf("Jobs() after workspace switch = %#v, want no rows", rows)
	}
	group.mu.Lock()
	scopedB := group.current
	group.mu.Unlock()
	if scopedB == scopedA {
		t.Fatal("workspace switch reused the previous build's jobs manager")
	}
	// The previous build is retained: its jobs stay controllable by id after the
	// switch, and only module unload releases its manager.
	if scopedA.isClosed() {
		t.Fatal("workspace switch closed the previous build; its jobs would be orphaned")
	}
	group.close()
	if !scopedA.waitClosed(5 * time.Second) {
		t.Fatal("group.close did not release the retained build")
	}
}

// TestModuleProvidesJobService pins the toolkit wiring: the module provides
// contracts.KeyJobs so the session header can require it, and the service is
// backed by the live root-scoped job manager.
func TestModuleProvidesJobService(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	jobsService, err := root.Require(contracts.KeyJobs)
	if err != nil {
		t.Fatalf("require jobs service: %v", err)
	}
	if jobsService == nil {
		t.Fatal("jobs service is nil")
	}
	if rows := jobsService.Jobs(); len(rows) != 0 {
		t.Fatalf("Jobs() on a fresh toolkit = %#v, want no rows", rows)
	}
	if err := jobsService.KillJob("missing-job"); err == nil {
		t.Fatal("KillJob(missing) error = nil, want an unknown-job error")
	}
}

// TestJobServiceAgainstRealRigManager is the end-to-end format check: a real
// root-scoped build starts a real Rig background job, and the service must list
// it (proving the parser matches the actual job_list output, not just the
// fixture) and kill it by id.
func TestJobServiceAgainstRealRigManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which Rig only has on Unix")
	}
	root := t.TempDir()
	scoped := buildRootScopedFor(Config{}, root, 5*time.Second, sandbox.ModeWorkspaceWrite, nil, "linux",
		func(string) (string, error) { return "", errors.New("not found") })
	group := &rebindGroup{rootOf: func() string { return root }, current: scoped}
	defer group.close()

	started, err := scoped.byName["bash"].Invoke(context.Background(), rig.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"tail -f /dev/null","run_in_background":true}`,
	})
	if err != nil || started.IsError {
		t.Fatalf("bash run_in_background = %q (err %v)", started.Content, err)
	}
	match := jobIDPattern.FindStringSubmatch(started.Content)
	if match == nil {
		t.Fatalf("bash run_in_background output %q has no job id", started.Content)
	}
	jobID := match[1]

	service := newJobService(group)
	rows := service.Jobs()
	if len(rows) != 1 {
		t.Fatalf("Jobs() = %#v, want the one real background job", rows)
	}
	row := rows[0]
	if row.ID != jobID || !row.Running || row.State != "running" || row.ExitCode != -1 {
		t.Fatalf("real running job row = %#v (want id %q, running)", row, jobID)
	}
	if row.Command != "tail -f /dev/null" {
		t.Fatalf("real job command = %q, want the full command", row.Command)
	}
	if row.Kind == "" || !strings.Contains(row.Status, "running") {
		t.Fatalf("real job kind/status = %q/%q", row.Kind, row.Status)
	}

	if err := service.KillJob(jobID); err != nil {
		t.Fatalf("KillJob(%q): %v", jobID, err)
	}
	rows = service.Jobs()
	if len(rows) != 1 || rows[0].ID != jobID {
		t.Fatalf("Jobs() after kill = %#v, want the killed job still listed", rows)
	}
	if rows[0].Running || rows[0].State != "killed" {
		t.Fatalf("killed job row = %#v, want state killed and not running", rows[0])
	}
}

// TestParseJobListFreeTextFields covers free-text status/command values that
// contain the later field names: a kill reason like "exit=1 duration_ms=2
// command=x" must not shift the real exit/duration/command fields.
func TestParseJobListFreeTextFields(t *testing.T) {
	text := "jobs: 1\n- job-x [bash] state=killed status=killed (reason: exit=1 duration_ms=2 command=fake) exit=-1 duration_ms=1503 command=echo command=ok\n"
	rows := parseJobList(text)
	if len(rows) != 1 {
		t.Fatalf("parseJobList() = %+v, want one row", rows)
	}
	got := rows[0]
	if got.State != "killed" || got.Status != "killed (reason: exit=1 duration_ms=2 command=fake)" ||
		got.ExitCode != -1 || got.Duration != 1503*time.Millisecond ||
		got.Command != "echo command=ok" {
		t.Fatalf("row = %+v, want the real trailing fields", got)
	}
}

// --- JobService.JobOutput: the UI's bounded output read --------------------

// TestJobServiceJobOutputPassesIDAndTail pins the read path: JobOutput drives
// the model-facing job_output tool with a tail request (so the model's own
// output cursor is never consumed), and returns just the output body.
func TestJobServiceJobOutputPassesIDAndTail(t *testing.T) {
	output := &jobServiceStubTool{
		name: jobs.ToolJobOutput,
		result: rig.ToolResult{Content: "job_id: job-a\nkind: bash\ncommand: sleep 30\n" +
			"state: running\nstatus: running (running)\nexit_code: -1\nduration_ms: 1503\n" +
			"output:\nline one\nline two\n"},
	}
	group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", output)}

	text, err := newJobService(group).JobOutput("job-a")
	if err != nil {
		t.Fatalf("JobOutput: %v", err)
	}
	if text != "line one\nline two\n" {
		t.Fatalf("JobOutput = %q, want the output body", text)
	}
	if output.calls != 1 || output.lastCall.Name != jobs.ToolJobOutput {
		t.Fatalf("job_output calls = %d (call %+v), want 1", output.calls, output.lastCall)
	}
	var args struct {
		JobID string `json:"job_id"`
		Tail  int    `json:"tail"`
	}
	if err := json.Unmarshal([]byte(output.lastCall.Arguments), &args); err != nil {
		t.Fatalf("job_output arguments %q are not JSON: %v", output.lastCall.Arguments, err)
	}
	if args.JobID != "job-a" {
		t.Fatalf("job_output job_id = %q, want job-a", args.JobID)
	}
	if args.Tail <= 0 {
		t.Fatalf("job_output tail = %d, want a positive tail (a plain read would consume the model's cursor)", args.Tail)
	}
}

// TestJobServiceJobOutputEmpty covers a job with no output yet: the manager's
// "output: (none)" sentinel is the empty string, not that literal text.
func TestJobServiceJobOutputEmpty(t *testing.T) {
	output := &jobServiceStubTool{
		name:   jobs.ToolJobOutput,
		result: rig.ToolResult{Content: "job_id: job-b\nstate: running\noutput: (none)\n"},
	}
	group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", output)}
	text, err := newJobService(group).JobOutput("job-b")
	if err != nil {
		t.Fatalf("JobOutput: %v", err)
	}
	if text != "" {
		t.Fatalf("JobOutput with no output = %q, want empty", text)
	}
}

// TestJobServiceJobOutputBounds covers the bounded read: output larger than the
// contract's cap keeps its newest bytes and starts with the truncation marker,
// so the panel can say the start is missing without shipping megabytes.
func TestJobServiceJobOutputBounds(t *testing.T) {
	// Enough "line NNNNNN" lines to exceed the byte cap several times over.
	var body strings.Builder
	const lineCount = 9000
	for index := 0; index < lineCount; index++ {
		fmt.Fprintf(&body, "line %06d\n", index)
	}
	output := &jobServiceStubTool{
		name:   jobs.ToolJobOutput,
		result: rig.ToolResult{Content: "job_id: job-c\nstate: running\noutput:\n" + body.String()},
	}
	group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", output)}
	text, err := newJobService(group).JobOutput("job-c")
	if err != nil {
		t.Fatalf("JobOutput: %v", err)
	}
	if !strings.HasPrefix(text, contracts.JobOutputTruncatedMarker) {
		t.Fatalf("bounded JobOutput does not start with the truncation marker: %.40q", text)
	}
	trimmed := strings.TrimPrefix(text, contracts.JobOutputTruncatedMarker)
	if len(trimmed) > contracts.JobOutputMaxBytes {
		t.Fatalf("bounded JobOutput keeps %d bytes, want at most %d", len(trimmed), contracts.JobOutputMaxBytes)
	}
	// The newest output survives, the oldest bytes are what was dropped.
	if !strings.Contains(trimmed, fmt.Sprintf("line %06d\n", lineCount-1)) {
		t.Fatalf("bounded JobOutput lost its newest line: ...%.60q", trimmed[len(trimmed)-60:])
	}
	if strings.Contains(trimmed, "line 000000\n") {
		t.Fatal("bounded JobOutput kept the oldest bytes instead of the newest")
	}
}

// TestJobServiceJobOutputErrors covers the read's error cases: an empty id is
// rejected without a tool call, an unknown job is the contract's sentinel (the
// UI localizes it), a missing backend is the unavailable sentinel and tool /
// invoke failures surface.
func TestJobServiceJobOutputErrors(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		result    rig.ToolResult
		invokeErr error
		wantErr   error
		want      string
		wantCalls int
	}{
		{name: "empty id", id: "  ", want: "required"},
		{
			name:      "unknown job",
			id:        "nope",
			result:    rig.ToolResult{Content: `jobs: unknown job "nope"`, IsError: true},
			wantErr:   contracts.ErrJobNotFound,
			want:      "unknown job",
			wantCalls: 1,
		},
		{
			name:      "tool failure",
			id:        "job-a",
			result:    rig.ToolResult{Content: "manager exploded", IsError: true},
			want:      "manager exploded",
			wantCalls: 1,
		},
		{
			name:      "invoke failure",
			id:        "job-a",
			invokeErr: errors.New("invoke exploded"),
			want:      "invoke exploded",
			wantCalls: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			output := &jobServiceStubTool{name: jobs.ToolJobOutput, result: testCase.result, err: testCase.invokeErr}
			group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", output)}
			_, err := newJobService(group).JobOutput(testCase.id)
			if err == nil {
				t.Fatalf("JobOutput(%q) error = nil, want one containing %q", testCase.id, testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("JobOutput(%q) error = %q, want one containing %q", testCase.id, err, testCase.want)
			}
			if testCase.wantErr != nil && !errors.Is(err, testCase.wantErr) {
				t.Fatalf("JobOutput(%q) error = %v, want it to wrap %v", testCase.id, err, testCase.wantErr)
			}
			if output.calls != testCase.wantCalls {
				t.Fatalf("job_output invoked %d times, want %d", output.calls, testCase.wantCalls)
			}
		})
	}
}

// TestJobServiceJobOutputUnavailable covers a stripped product: no invoker is
// the contract's unavailable sentinel, so the overlay can explain itself.
func TestJobServiceJobOutputUnavailable(t *testing.T) {
	if _, err := newJobService(nil).JobOutput("job-a"); !errors.Is(err, contracts.ErrJobUnavailable) {
		t.Fatalf("JobOutput without a backend error = %v, want ErrJobUnavailable", err)
	}
}

// TestJobServiceJobOutputStripsMetadataAndCRLF covers the parser boundary: the
// metadata header before "output:" never leaks into the panel and CRLF line
// endings are normalized to LF.
func TestJobServiceJobOutputStripsMetadataAndCRLF(t *testing.T) {
	output := &jobServiceStubTool{
		name: jobs.ToolJobOutput,
		// The command itself ends with the word "output:", which must not be
		// mistaken for the body's header.
		result: rig.ToolResult{Content: "job_id: job-d\ncommand: echo output:\nstate: exited\noutput:\r\n" +
			"build ok\r\n"},
	}
	group := &rebindGroup{rootOf: func() string { return "" }, current: jobServiceScopedBuild("", output)}
	text, err := newJobService(group).JobOutput("job-d")
	if err != nil {
		t.Fatalf("JobOutput: %v", err)
	}
	if text != "build ok\n" {
		t.Fatalf("JobOutput = %q, want the LF-normalized output body", text)
	}
}
