package toolkit

// Permission-rebuild routing regressions (2026-11 audit): every background job
// must stay reachable by id after a permission/workspace rebuild — including
// pwsh jobs (the only shell on Windows) and foreground bash calls that Rig
// promotes to background on timeout (PromoteOnTimeout defaults on, so the
// default 30s shell timeout hits this on every platform). Both used to miss the
// owner registration: job_output/job_kill fell back to the CURRENT build and
// reported "unknown job" after any rebuild while job_list still listed them.
// The UI's job chip (contracts.JobService) routes through the same table, so
// these tests cover the model-facing proxies AND the host-facing service.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
)

// routingRebuildGroup builds a rebind group whose sandbox mode can be flipped
// to force a rebuild identity change (the permission-preset switch path).
func routingRebuildGroup(t *testing.T, cfg Config, timeout time.Duration) (*rebindGroup, *sandbox.Mode) {
	t.Helper()
	root := cfg.Root
	mode := sandbox.ModeWorkspaceWrite
	sandboxOf := func() (sandbox.Mode, sandbox.Runner) { return mode, nil }
	group := newRebindGroup(cfg, timeout, func() string { return root }, sandboxOf, t.Logf)
	t.Cleanup(group.close)
	return group, &mode
}

// proxiesByName maps one build's proxy list by tool name.
func proxiesByName(t *testing.T, group *rebindGroup, scoped *rootScoped) map[string]rig.Tool {
	t.Helper()
	byName := make(map[string]rig.Tool)
	for _, proxy := range group.proxies(scoped) {
		byName[proxy.Name()] = proxy
	}
	return byName
}

// installFakePwsh puts a minimal pwsh stand-in on PATH: Rig's pwsh tool passes
// the script as the last argument (-Command), so the fake execs it via sh.
// This makes the pwsh routing test platform-independent (no real PowerShell
// needed on the Linux test host).
func installFakePwsh(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	fake := "#!/bin/sh\nfor last; do :; done\nexec /bin/sh -c \"$last\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "pwsh"), []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake pwsh: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestPwshJobRoutesAcrossPermissionRebuild: a pwsh background job must stay
// readable and killable after a permission rebuild, exactly like a bash job.
func TestPwshJobRoutesAcrossPermissionRebuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake pwsh is a /bin/sh script; Windows has a real PowerShell")
	}
	installFakePwsh(t)
	cfg := Config{
		Root:                t.TempDir(),
		CommandAllowlist:    []string{"pwsh", "bash", "sh", "sleep", "echo"},
		AllowShellOperators: true,
	}
	group, mode := routingRebuildGroup(t, cfg, 30*time.Second)
	initial := group.initial()
	byName := proxiesByName(t, group, initial)
	if _, ok := byName["pwsh"]; !ok {
		t.Fatal("pwsh tool not registered although a pwsh binary is on PATH")
	}

	pwshStarted := executionCall(t, byName["pwsh"], "", `{"command":"sleep 3; echo pwsh-done","run_in_background":true}`)
	pwshID := executionArgumentID(t, pwshStarted, "started background job")
	bashStarted := executionCall(t, byName["bash"], "", `{"command":"sleep 3; echo bash-done","run_in_background":true}`)
	bashID := executionArgumentID(t, bashStarted, "started background job")

	// Before the rebuild the fallback build IS the owner, so reads work even
	// without registration — the failure must be rebuild-specific.
	pre := executionCall(t, byName["job_output"], "", `{"job_id":"`+pwshID+`"}`)
	if strings.Contains(pre.Content, "unknown job") {
		t.Fatalf("pre-rebuild pwsh job_output broken: %q", pre.Content)
	}

	*mode = sandbox.ModeDangerFullAccess // global permission change → new build
	if group.forRoot() == initial {
		t.Fatal("permission change did not rebuild")
	}

	// Model-facing path: job_output routes to the owner build.
	out := executionCall(t, byName["job_output"], "", `{"job_id":"`+pwshID+`"}`)
	if strings.Contains(out.Content, "unknown job") {
		t.Errorf("post-rebuild pwsh job_output = %q: pwsh job lost its owner registration", out.Content)
	}
	// Bash control: the explicitly-registered shell keeps routing.
	bashOut := executionCall(t, byName["job_output"], "", `{"job_id":"`+bashID+`"}`)
	if strings.Contains(bashOut.Content, "unknown job") {
		t.Errorf("post-rebuild bash job_output = %q: control broken", bashOut.Content)
	}
	// job_list still merges both retained builds.
	list := executionCall(t, byName["job_list"], "", `{}`)
	if !strings.Contains(list.Content, pwshID) || !strings.Contains(list.Content, bashID) {
		t.Errorf("post-rebuild job_list lost a job: %q", list.Content)
	}

	// UI-facing path: the header chip reads/kills through contracts.JobService.
	service := newJobService(group)
	if _, err := service.JobOutput(pwshID); err != nil {
		t.Errorf("post-rebuild JobOutput(%s): %v", pwshID, err)
	}
	if err := service.KillJob(pwshID); err != nil {
		t.Errorf("post-rebuild KillJob(%s): %v — the UI stop button cannot reach the pwsh job", pwshID, err)
	}
}

// TestPromotedJobRoutesAcrossPermissionRebuild: a foreground bash call that
// exceeds the shell timeout is promoted to a background job by Rig
// (renderPromoted text). Its id must be registered too, so the job stays
// controllable after a rebuild — the model is told to use job_output/job_kill.
func TestPromotedJobRoutesAcrossPermissionRebuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background shell jobs need /bin/sh, which is Unix-only in Rig")
	}
	cfg := Config{
		Root:                t.TempDir(),
		CommandAllowlist:    []string{"bash", "sh", "sleep", "echo"},
		AllowShellOperators: true,
	}
	group, mode := routingRebuildGroup(t, cfg, 1*time.Second) // 1s timeout forces promotion
	initial := group.initial()
	byName := proxiesByName(t, group, initial)

	// Foreground call (no run_in_background): sleep outruns the 1s timeout.
	result := executionCall(t, byName["bash"], "", `{"command":"sleep 3; echo promoted-done"}`)
	const marker = "moved to background job "
	index := strings.Index(result.Content, marker)
	if index < 0 {
		t.Fatalf("expected a promoted foreground result, got %q", result.Content)
	}
	rest := result.Content[index+len(marker):]
	if end := strings.IndexAny(rest, "]\n"); end >= 0 {
		rest = rest[:end]
	}
	jobID := strings.TrimSpace(rest)
	if jobID == "" {
		t.Fatalf("no promoted job id in %q", result.Content)
	}

	*mode = sandbox.ModeDangerFullAccess
	if group.forRoot() == initial {
		t.Fatal("permission change did not rebuild")
	}

	out := executionCall(t, byName["job_output"], "", `{"job_id":"`+jobID+`"}`)
	if strings.Contains(out.Content, "unknown job") {
		t.Errorf("post-rebuild promoted job_output = %q: promoted job lost its owner registration", out.Content)
	}
	kill := executionCall(t, byName["job_kill"], "", `{"job_id":"`+jobID+`"}`)
	if kill.IsError || strings.Contains(kill.Content, "unknown job") {
		t.Errorf("post-rebuild promoted job_kill = %q (isError=%v)", kill.Content, kill.IsError)
	}
}

// TestExecutionResultIDPromotedShape pins the text-shape extraction contract:
// the promotion marker sits mid-text inside Rig's "[still running after Nms;
// moved to background job <id>]" line, after whatever output the command
// already produced.
func TestExecutionResultIDPromotedShape(t *testing.T) {
	promoted := "first-line\n[still running after 1000ms; moved to background job bash-2232b814eb75]\n" +
		"The command keeps running in the background. You will be notified when it finishes; " +
		"read newer output with job_output, stop it with job_kill."
	cases := []struct {
		name    string
		content string
		key     string
		want    string
	}{
		{"promoted with output", promoted, "moved to background job", "bash-2232b814eb75"},
		{"promoted pwsh", "[still running after 30000ms; moved to background job pwsh-18f9d7d720e5]\n", "moved to background job", "pwsh-18f9d7d720e5"},
		{"promoted absent", "plain output\n[exit code: 1]", "moved to background job", ""},
		{"explicit background", "started background job bash-6364cae01c13", "started background job", "bash-6364cae01c13"},
		{"job_id line", "job_id: bash-abc\nstate: running", "job_id", "bash-abc"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := executionResultID(testCase.content, testCase.key); got != testCase.want {
				t.Errorf("executionResultID(%q, %q) = %q, want %q", testCase.content, testCase.key, got, testCase.want)
			}
		})
	}
}
