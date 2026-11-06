package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
)

// registryInvoke finds one tool by name and calls it.
func registryInvoke(t *testing.T, reg *runtime.Registry, name string, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	for _, tool := range reg.List() {
		if tool.Name() == name {
			return tool.Invoke(context.Background(), raw)
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return "", nil
}

// TestWorkingDirectorySurvivesRebuild covers the fresh
// WorkingDirectoryState created per registry build: a cwd set before a rebuild
// must still be current afterwards.
func TestWorkingDirectorySurvivesRebuild(t *testing.T) {
	st, dir := newToolStore(t)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	opts := a.toolOptions(st, dir, "")
	get := func(key, env, def string) string { return def }

	reg1, close1, err := registerAllToolsWithOptions(st, dir, get, opts)
	if err != nil {
		t.Fatalf("first registry: %v", err)
	}
	defer func() {
		if close1 != nil {
			_ = close1()
		}
	}()
	out, err := registryInvoke(t, reg1, "working_directory", map[string]string{"subdir": "src"})
	if err != nil {
		t.Fatalf("working_directory: %v", err)
	}
	if !strings.Contains(out, `"src"`) {
		t.Fatalf("working_directory did not change: %s", out)
	}

	// A rebuild (model/plugin/permission change) must reuse the same state;
	// the app resolves fresh options for each build.
	opts2 := a.toolOptions(st, dir, "")
	reg2, close2, err := registerAllToolsWithOptions(st, dir, get, opts2)
	if err != nil {
		t.Fatalf("second registry: %v", err)
	}
	if close2 != nil {
		t.Cleanup(func() { _ = close2() })
	}
	out, err = registryInvoke(t, reg2, "working_directory", map[string]string{})
	if err != nil {
		t.Fatalf("working_directory read: %v", err)
	}
	if !strings.Contains(out, `"relative":"src"`) {
		t.Fatalf("rebuild lost the working directory: %s", out)
	}
}

// TestJobManagerSurvivesRebuild covers the fresh job manager per registry
// build: an id learned before a rebuild must still resolve after it.
func TestJobManagerSurvivesRebuild(t *testing.T) {
	st, dir := newToolStore(t)
	a := &app{}
	opts := a.toolOptions(st, dir, "")
	t.Cleanup(a.closeToolManagers)
	get := func(key, env, def string) string { return def }

	reg1, close1, err := registerAllToolsWithOptions(st, dir, get, opts)
	if err != nil {
		t.Fatalf("first registry: %v", err)
	}
	if close1 != nil {
		defer func() { _ = close1() }()
	}
	started, err := registryInvoke(t, reg1, "job_start", map[string]any{"command": "echo alive"})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	var id string
	for _, field := range strings.Fields(started) {
		if strings.HasPrefix(field, "job-") {
			id = field
			break
		}
	}
	if id == "" {
		t.Fatalf("job_start returned no id: %s", started)
	}

	// Rebuild, as a model/plugin/permission change does.
	opts2 := a.toolOptions(st, dir, "")
	reg2, close2, err := registerAllToolsWithOptions(st, dir, get, opts2)
	if err != nil {
		t.Fatalf("second registry: %v", err)
	}
	if close2 != nil {
		t.Cleanup(func() { _ = close2() })
	}
	out, err := registryInvoke(t, reg2, "job_output", map[string]any{"id": id, "wait": true})
	if err != nil {
		t.Fatalf("job %q disappeared across a rebuild: %v", id, err)
	}
	if !strings.Contains(out, "alive") {
		t.Fatalf("job output after rebuild = %q", out)
	}
}

// TestShellOutputCapReachesJobTools covers plugin.shell.output_cap_kb: the
// configured cap must reach the job/terminal/pwsh tools, not only tools.Register.
func TestShellOutputCapReachesJobTools(t *testing.T) {
	st, dir := newToolStore(t)
	if err := st.SetSetting("plugin.shell.output_cap_kb", "1"); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	t.Cleanup(a.closeToolManagers)
	opts := a.toolOptions(st, dir, "")
	get := func(key, env, def string) string { return def }
	reg, closeFn, err := registerAllToolsWithOptions(st, dir, get, opts)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	started, err := registryInvoke(t, reg, "job_start", map[string]any{
		"command": "head -c 5000 /dev/zero | tr '\\0' x",
	})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	var id string
	for _, field := range strings.Fields(started) {
		if strings.HasPrefix(field, "job-") {
			id = field
			break
		}
	}
	out, err := registryInvoke(t, reg, "job_output", map[string]any{"id": id, "wait": true})
	if err != nil {
		t.Fatalf("job_output: %v", err)
	}
	if len(out) > 3000 {
		t.Fatalf("1KB output cap retained %d bytes", len(out))
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("capped output does not report truncation: %q", out[:min(200, len(out))])
	}
}

// startJobOutput starts command in reg's workspace and returns its output.
func startJobOutput(t *testing.T, reg *runtime.Registry, command string) string {
	t.Helper()
	started, err := registryInvoke(t, reg, "job_start", map[string]any{"command": command})
	if err != nil {
		t.Fatalf("job_start: %v", err)
	}
	var id string
	for _, field := range strings.Fields(started) {
		if strings.HasPrefix(field, "job-") {
			id = field
			break
		}
	}
	if id == "" {
		t.Fatalf("job_start returned no id: %s", started)
	}
	out, err := registryInvoke(t, reg, "job_output", map[string]any{"id": id, "wait": true})
	if err != nil {
		t.Fatalf("job_output: %v", err)
	}
	return out
}

// TestManagersNotReusedAcrossWorkspaceAfterReadOnlyBuild covers the stale
// manager adoption: a read-only build at root B used to record B as the
// manager root while still holding A's managers, so the next writable build
// at B reused A's terminal/job managers and ran commands in the wrong
// directory.
func TestManagersNotReusedAcrossWorkspaceAfterReadOnlyBuild(t *testing.T) {
	st, rootA := newToolStore(t)
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "marker.txt"), []byte("from-A"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "marker.txt"), []byte("from-B"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	t.Cleanup(a.closeToolManagers)
	get := func(key, env, def string) string { return def }
	build := func(root, permission string) *runtime.Registry {
		t.Helper()
		opts := a.toolOptions(st, root, permission)
		reg, closeFn, err := registerAllToolsWithOptions(st, root, get, opts)
		if err != nil {
			t.Fatalf("registry at %s: %v", root, err)
		}
		if closeFn != nil {
			t.Cleanup(func() { _ = closeFn() })
		}
		return reg
	}

	regA := build(rootA, "")
	if out := startJobOutput(t, regA, "cat marker.txt"); !strings.Contains(out, "from-A") {
		t.Fatalf("root A job = %q, want from-A", out)
	}
	managerA := a.termMgr
	if managerA == nil || a.managerRoot != filepath.Clean(rootA) {
		t.Fatalf("root A managers not adopted: %p root=%q", managerA, a.managerRoot)
	}

	// A read-only rebuild at the other workspace must not claim that root.
	_ = build(rootB, session.PermissionAsk)
	if a.termMgr != managerA {
		t.Fatalf("read-only build replaced the A managers")
	}
	if a.managerRoot != filepath.Clean(rootA) {
		t.Fatalf("read-only build recorded root %q, want %q", a.managerRoot, filepath.Clean(rootA))
	}

	// The next writable build at B must create B managers, not reuse A's.
	regB := build(rootB, "")
	if a.termMgr == managerA {
		t.Fatalf("writable rebuild at B reused A's stale managers")
	}
	if a.managerRoot != filepath.Clean(rootB) {
		t.Fatalf("B managers recorded root %q, want %q", a.managerRoot, filepath.Clean(rootB))
	}
	if out := startJobOutput(t, regB, "cat marker.txt"); !strings.Contains(out, "from-B") {
		t.Fatalf("root B job = %q, want from-B", out)
	}
}

// TestManagersPickUpOutputCapAfterReadOnlyBuild covers the second stale-cap
// probe: a read-only rebuild after raising the cap recorded the new cap while
// keeping the old 256 KiB managers, so the next writable build reused them and
// returned 5 KB of untruncated output under a 1 KiB setting.
func TestManagersPickUpOutputCapAfterReadOnlyBuild(t *testing.T) {
	st, dir := newToolStore(t)
	a := &app{}
	t.Cleanup(a.closeToolManagers)
	get := func(key, env, def string) string { return def }
	build := func(permission string) *runtime.Registry {
		t.Helper()
		opts := a.toolOptions(st, dir, permission)
		reg, closeFn, err := registerAllToolsWithOptions(st, dir, get, opts)
		if err != nil {
			t.Fatalf("registry: %v", err)
		}
		if closeFn != nil {
			t.Cleanup(func() { _ = closeFn() })
		}
		return reg
	}

	_ = build("")
	oldManager := a.termMgr
	oldCap := a.managerCap
	if oldManager == nil || oldCap != 256<<10 {
		t.Fatalf("default managers = %p cap=%d, want the 256 KiB default", oldManager, oldCap)
	}

	if err := st.SetSetting("plugin.shell.output_cap_kb", "1"); err != nil {
		t.Fatal(err)
	}
	// Read-only rebuild must keep the old managers AND the old recorded cap.
	_ = build(session.PermissionAsk)
	if a.termMgr != oldManager {
		t.Fatal("read-only build replaced the managers")
	}
	if a.managerCap != oldCap {
		t.Fatalf("read-only build recorded cap %d, want unchanged %d", a.managerCap, oldCap)
	}

	// The writable rebuild notices the cap change and starts fresh managers.
	reg := build("")
	if a.termMgr == oldManager {
		t.Fatal("writable rebuild reused managers created for the old cap")
	}
	if a.managerCap != 1<<10 {
		t.Fatalf("manager cap = %d, want the saved 1 KiB", a.managerCap)
	}
	out := startJobOutput(t, reg, "head -c 5000 /dev/zero | tr '\\0' x")
	if len(out) > 3000 {
		t.Fatalf("1 KiB cap retained %d bytes of job output", len(out))
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("capped output does not report truncation: %q", out[:min(200, len(out))])
	}
}
