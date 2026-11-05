package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// TestProductionToolsInvocable builds the production registry exactly as the
// app does, then walks every tool: it must carry a name/description and must
// either succeed with representative arguments or fail for one of the
// explicitly listed external-state reasons. Discarding errors for every tool
// (the previous shape of this test) let always-failing tools such as the MCP
// resource tools pass; the expected-error list is deliberate and small.
func TestProductionToolsInvocable(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Minimal valid PNG for read_image.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0d, 'I', 'D', 'A', 'T', 8, 0xd7, 0x63, 0xf8, 0x0f, 0, 0x01, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d, 0xb0, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
	if err := os.WriteFile(filepath.Join(dir, "px.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	// A real Git repository makes the worktree tools invocable instead of
	// silently failing on "not a Git repository".
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ZeoKeel", "GIT_AUTHOR_EMAIL=zeokeel@example.com",
			"GIT_COMMITTER_NAME=ZeoKeel", "GIT_COMMITTER_EMAIL=zeokeel@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "hello.txt")
	git("commit", "-qm", "init", "--allow-empty")

	// The experimental toggles default off; enable them so this test covers
	// the full registry. TestPluginTogglesRemoveTools covers the off state.
	for _, key := range []string{"plugin.agent_teams.enabled", "plugin.developer_tools.enabled"} {
		if err := st.SetSetting(key, "true"); err != nil {
			t.Fatal(err)
		}
	}
	get := func(key, env, def string) string { return def }
	reg, closeFn, err := registerAllTools(st, dir, get)
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	list := reg.List()
	if len(list) < 40 {
		t.Fatalf("registry has %d tools, want >= 40", len(list))
	}
	ctx := context.Background()
	invoke := func(name string, args any) (string, error) {
		for _, tl := range list {
			if tl.Name() != name {
				continue
			}
			raw, _ := json.Marshal(args)
			return tl.Invoke(ctx, raw)
		}
		t.Fatalf("tool %q not registered", name)
		return "", nil
	}
	// Functional subset: each of these must be registered and succeed.
	must := map[string]any{
		"read_file":            map[string]any{"path": "hello.txt"},
		"write_file":           map[string]any{"path": "new.txt", "content": "x"},
		"edit_file":            map[string]any{"path": "hello.txt", "old": "alpha", "new": "ALPHA"},
		"glob":                 map[string]any{"pattern": "*.txt"},
		"grep":                 map[string]any{"pattern": "beta"},
		"str_replace_editor":   map[string]any{"command": "view", "path": "hello.txt"},
		"read_image":           map[string]any{"file_path": "px.png"},
		"bash":                 map[string]any{"command": "echo hi"},
		"todo":                 map[string]any{"action": "list"},
		"goal":                 map[string]any{"action": "get"},
		"present":              map[string]any{"title": "t", "path": "hello.txt"},
		"skill":                map[string]any{"action": "list"},
		"session_search":       map[string]any{"query": "x"},
		"schedule_create":      map[string]any{"title": "t", "prompt": "p", "cron": "@daily"},
		"schedule_list":        map[string]any{},
		"working_directory":    map[string]any{"action": "get"},
		"report":               map[string]any{"summary": "ok"},
		"job_start":            map[string]any{"command": "echo hi"},
		"job_list":             map[string]any{},
		"terminal_open":        map[string]any{},
		"terminal_list":        map[string]any{},
		"list_subagent_models": map[string]any{},
		"inspect":              map[string]any{"section": "all"},
		"list_worktrees":       map[string]any{},
		"close_team":           map[string]any{},
		"wait_team":            map[string]any{},
		"list_teams":           map[string]any{},
		"ask_user":             map[string]any{"question": "continue?"},
	}
	for name, args := range must {
		out, err := invoke(name, args)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(out) == 0 && name != "working_directory" {
			t.Errorf("%s returned empty output", name)
		}
	}
	// Terminal round trip.
	out, err := invoke("terminal_open", map[string]any{})
	if err != nil {
		t.Fatalf("terminal_open: %v", err)
	}
	var term struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &term)
	if term.ID != "" {
		if _, err := invoke("terminal_read", map[string]any{"id": term.ID}); err != nil {
			t.Errorf("terminal_read: %v", err)
		}
		if _, err := invoke("terminal_send", map[string]any{"id": term.ID, "input": "echo hi\n"}); err != nil {
			t.Errorf("terminal_send: %v", err)
		}
		if _, err := invoke("terminal_signal", map[string]any{"id": term.ID, "signal": "SIGINT"}); err != nil {
			t.Errorf("terminal_signal: %v", err)
		}
		if _, err := invoke("terminal_close", map[string]any{"id": term.ID}); err != nil {
			t.Errorf("terminal_close: %v", err)
		}
	}
	// Job round trip.
	out, err = invoke("job_start", map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	var job struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &job)
	if job.ID != "" {
		if _, err := invoke("job_output", map[string]any{"id": job.ID}); err != nil {
			t.Errorf("job_output: %v", err)
		}
		if _, err := invoke("job_kill", map[string]any{"id": job.ID}); err != nil {
			t.Errorf("job_kill: %v", err)
		}
	}
	// Schedule round trip.
	out, err = invoke("schedule_create", map[string]any{"title": "probe", "prompt": "p", "cron": "@daily"})
	if err != nil {
		t.Fatalf("schedule_create: %v", err)
	}
	var sched struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &sched)
	if sched.ID != "" {
		if _, err := invoke("schedule_update", map[string]any{"id": sched.ID, "title": "probe2"}); err != nil {
			t.Errorf("schedule_update: %v", err)
		}
		if _, err := invoke("schedule_delete", map[string]any{"id": sched.ID}); err != nil {
			t.Errorf("schedule_delete: %v", err)
		}
	}

	// Worktree round trip: enter creates the worktree before exit removes it.
	if _, err := invoke("enter_worktree", map[string]any{"name": "probe-wt"}); err != nil {
		t.Errorf("enter_worktree: %v", err)
	} else if _, err := invoke("exit_worktree", map[string]any{"name": "probe-wt"}); err != nil {
		t.Errorf("exit_worktree: %v", err)
	}

	// Tools whose success needs an external dependency this test does not
	// provide. Each one must fail with the stated reason (not silently pass),
	// and is listed so a future tool that starts succeeding is noticed too.
	expectedErrors := map[string]string{
		"subagent":             "runner",
		"workflow":             "runner",
		"ralph":                "runner",
		"spawn_team":           "runner",
		"pwsh":                 "pwsh",
		"web_fetch":            "web_fetch",
		"team_status":          "target",
		"send_message":         "target",
		"interrupt_agent":      "target",
		"session_trace":        "session",
		"session_event_search": "session",
		"session_event_read":   "session",
		"session_event_trace":  "session",
	}
	for name, want := range expectedErrors {
		out, err := invoke(name, map[string]any{"task": "x", "objective": "x", "command": "echo hi", "url": "http://127.0.0.1:1/", "steps": []string{"x"}, "tasks": []string{"x"}, "target": "missing", "message": "hi", "query": "x", "session_id": "missing"})
		if err == nil {
			t.Errorf("%s: expected a %s error, got success: %.80s", name, want, out)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
			t.Errorf("%s: error = %q, want it to mention %q", name, err, want)
		}
	}
	// Every registered tool has metadata.
	for _, tl := range list {
		if tl.Name() == "" || tl.Description() == "" {
			t.Errorf("tool %q missing name/description", tl.Name())
		}
		if strings.TrimSpace(tl.Description()) == "" {
			t.Errorf("tool %q has a blank description", tl.Name())
		}
	}
	_ = tools.DefaultMaxOutputBytes
}
