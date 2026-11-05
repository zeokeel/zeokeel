package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// requireGit skips a test when the system git executable is unavailable.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// worktreeTestEnv isolates git from user/system configuration and secrets.
func worktreeTestEnv() []string {
	env := scrubEnv(os.Environ())
	env = setEnv(env, "GIT_CONFIG_NOSYSTEM", "1")
	env = setEnv(env, "GIT_CONFIG_GLOBAL", os.DevNull)
	env = setEnv(env, "GIT_TERMINAL_PROMPT", "0")
	return env
}

// gitTestRun runs git in dir and fails the test on a non-zero exit.
func gitTestRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = worktreeTestEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initWorktreeRepo creates a committed repository and returns its root and HEAD.
func initWorktreeRepo(t *testing.T) (string, string) {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	gitTestRun(t, root, "init", "-q")
	gitTestRun(t, root, "config", "user.name", "ZeoKeel Test")
	gitTestRun(t, root, "config", "user.email", "test@example.com")
	writeTestFile(t, filepath.Join(root, "README.md"), "hello worktree\n")
	gitTestRun(t, root, "add", "README.md")
	gitTestRun(t, root, "commit", "-q", "-m", "initial")
	return root, gitTestRun(t, root, "rev-parse", "HEAD")
}

// worktreeToolMap builds the tools and indexes them by name.
func worktreeToolMap(t *testing.T, root string) map[string]runtime.Tool {
	t.Helper()
	list := NewWorktreeTools(WorktreeConfig{WorkspaceRoot: root})
	want := map[string]bool{"enter_worktree": true, "exit_worktree": true, "list_worktrees": true}
	if len(list) != len(want) {
		t.Fatalf("NewWorktreeTools returned %d tools, want %d", len(list), len(want))
	}
	out := make(map[string]runtime.Tool, len(list))
	for _, tool := range list {
		if !want[tool.Name()] {
			t.Fatalf("unexpected worktree tool %q", tool.Name())
		}
		if tool.Description() == "" {
			t.Fatalf("worktree tool %q has an empty description", tool.Name())
		}
		if _, duplicate := out[tool.Name()]; duplicate {
			t.Fatalf("duplicate worktree tool %q", tool.Name())
		}
		out[tool.Name()] = tool
	}
	return out
}

// decodeWorktreeJSON decodes one tool result.
func decodeWorktreeJSON(t *testing.T, out string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), dst); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
}

func TestWorktreeCreateListRemove(t *testing.T) {
	root, head := initWorktreeRepo(t)
	tools := worktreeToolMap(t, root)

	out := mustInvoke(t, tools["enter_worktree"], map[string]any{"name": "feature"})
	var entered struct {
		Kind           string `json:"kind"`
		Path           string `json:"path"`
		Branch         string `json:"branch"`
		BaseCommit     string `json:"base_commit"`
		RepositoryRoot string `json:"repository_root"`
		WorktreeRoot   string `json:"worktree_root"`
	}
	decodeWorktreeJSON(t, out, &entered)
	wantPath := filepath.Join(root, worktreeDirName, "feature")
	if entered.Kind != "worktree_enter" {
		t.Errorf("kind = %q, want worktree_enter", entered.Kind)
	}
	if canonicalPath(entered.Path) != canonicalPath(wantPath) {
		t.Errorf("path = %q, want %q", entered.Path, wantPath)
	}
	if entered.Branch != "feature" {
		t.Errorf("branch = %q, want feature", entered.Branch)
	}
	if entered.BaseCommit != head {
		t.Errorf("base_commit = %q, want %q", entered.BaseCommit, head)
	}
	if canonicalPath(entered.RepositoryRoot) != canonicalPath(root) {
		t.Errorf("repository_root = %q, want %q", entered.RepositoryRoot, root)
	}
	if canonicalPath(entered.WorktreeRoot) != canonicalPath(filepath.Join(root, worktreeDirName)) {
		t.Errorf("worktree_root = %q", entered.WorktreeRoot)
	}
	if _, err := os.Stat(filepath.Join(entered.Path, "README.md")); err != nil {
		t.Errorf("new worktree does not contain the committed file: %v", err)
	}
	if branches := gitTestRun(t, root, "branch", "--list", "feature"); !strings.Contains(branches, "feature") {
		t.Errorf("branch feature was not created: %q", branches)
	}

	// list_worktrees reports the main checkout and the managed one.
	listOut := mustInvoke(t, tools["list_worktrees"], map[string]any{})
	var listed struct {
		Kind      string `json:"kind"`
		Count     int    `json:"count"`
		Worktrees []struct {
			Path     string `json:"path"`
			Head     string `json:"head"`
			Branch   string `json:"branch"`
			Detached bool   `json:"detached"`
			Bare     bool   `json:"bare"`
			Managed  bool   `json:"managed"`
		} `json:"worktrees"`
	}
	decodeWorktreeJSON(t, listOut, &listed)
	if listed.Kind != "worktree_list" {
		t.Errorf("kind = %q, want worktree_list", listed.Kind)
	}
	if listed.Count != 2 || len(listed.Worktrees) != 2 {
		t.Fatalf("list count = %d (worktrees %d), want 2: %s", listed.Count, len(listed.Worktrees), listOut)
	}
	var managed, main int
	for _, view := range listed.Worktrees {
		switch {
		case view.Managed:
			managed++
			if canonicalPath(view.Path) != canonicalPath(entered.Path) {
				t.Errorf("managed path = %q, want %q", view.Path, entered.Path)
			}
			if view.Branch != "feature" || view.Head != head || view.Detached || view.Bare {
				t.Errorf("managed worktree = %+v", view)
			}
		default:
			main++
			if canonicalPath(view.Path) != canonicalPath(root) {
				t.Errorf("main worktree path = %q, want %q", view.Path, root)
			}
			if view.Branch == "" || view.Head != head {
				t.Errorf("main worktree = %+v", view)
			}
		}
	}
	if managed != 1 || main != 1 {
		t.Errorf("managed=%d main=%d, want 1 and 1", managed, main)
	}

	// exit_worktree by the returned path removes the checkout and the branch.
	exitOut := mustInvoke(t, tools["exit_worktree"], map[string]any{"path": entered.Path})
	var exited struct {
		Kind          string `json:"kind"`
		Path          string `json:"path"`
		Branch        string `json:"branch"`
		BranchDeleted bool   `json:"branch_deleted"`
		BranchKept    bool   `json:"branch_kept"`
		Warning       string `json:"warning"`
	}
	decodeWorktreeJSON(t, exitOut, &exited)
	if exited.Kind != "worktree_exit" {
		t.Errorf("kind = %q, want worktree_exit", exited.Kind)
	}
	if canonicalPath(exited.Path) != canonicalPath(entered.Path) {
		t.Errorf("exit path = %q, want %q", exited.Path, entered.Path)
	}
	if !exited.BranchDeleted || exited.BranchKept {
		t.Errorf("branch_deleted=%v branch_kept=%v, want true/false", exited.BranchDeleted, exited.BranchKept)
	}
	if exited.Warning != "" {
		t.Errorf("unexpected warning: %s", exited.Warning)
	}
	if _, err := os.Stat(entered.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after exit: %v", err)
	}
	if branches := gitTestRun(t, root, "branch", "--list", "feature"); strings.TrimSpace(branches) != "" {
		t.Errorf("branch feature still exists: %q", branches)
	}
	listOut = mustInvoke(t, tools["list_worktrees"], map[string]any{})
	decodeWorktreeJSON(t, listOut, &listed)
	if listed.Count != 1 || len(listed.Worktrees) != 1 || listed.Worktrees[0].Managed {
		t.Errorf("after exit: %+v", listed)
	}
}

func TestWorktreeExitByNameKeepBranch(t *testing.T) {
	root, _ := initWorktreeRepo(t)
	tools := worktreeToolMap(t, root)

	out := mustInvoke(t, tools["enter_worktree"], map[string]any{"name": "keepme"})
	var entered struct {
		Path string `json:"path"`
	}
	decodeWorktreeJSON(t, out, &entered)

	out = mustInvoke(t, tools["exit_worktree"], map[string]any{"name": "keepme", "keep_branch": true})
	var exited struct {
		Branch        string `json:"branch"`
		BranchDeleted bool   `json:"branch_deleted"`
		BranchKept    bool   `json:"branch_kept"`
	}
	decodeWorktreeJSON(t, out, &exited)
	if exited.Branch != "keepme" {
		t.Errorf("branch = %q, want keepme", exited.Branch)
	}
	if exited.BranchDeleted || !exited.BranchKept {
		t.Errorf("branch_deleted=%v branch_kept=%v, want false/true", exited.BranchDeleted, exited.BranchKept)
	}
	if _, err := os.Stat(entered.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after exit: %v", err)
	}
	if branches := gitTestRun(t, root, "branch", "--list", "keepme"); !strings.Contains(branches, "keepme") {
		t.Errorf("keep_branch did not keep the branch: %q", branches)
	}
}

func TestWorktreeGeneratedName(t *testing.T) {
	root, _ := initWorktreeRepo(t)
	tools := worktreeToolMap(t, root)

	out := mustInvoke(t, tools["enter_worktree"], map[string]any{})
	var entered struct {
		Path   string `json:"path"`
		Branch string `json:"branch"`
	}
	decodeWorktreeJSON(t, out, &entered)
	if !worktreeNamePattern.MatchString(entered.Branch) {
		t.Errorf("generated branch %q is not a safe worktree name", entered.Branch)
	}
	if filepath.Base(entered.Path) != entered.Branch {
		t.Errorf("path %q does not end in branch %q", entered.Path, entered.Branch)
	}
	if !strings.HasPrefix(canonicalPath(entered.Path), canonicalPath(filepath.Join(root, worktreeDirName))+string(filepath.Separator)) {
		t.Errorf("generated path %q is not below the managed directory", entered.Path)
	}

	// exit by name removes it.
	mustInvoke(t, tools["exit_worktree"], map[string]any{"name": entered.Branch})
	if _, err := os.Stat(entered.Path); !os.IsNotExist(err) {
		t.Errorf("generated worktree still exists after exit: %v", err)
	}
}

func TestWorktreeRejectsInvalidNamesAndPaths(t *testing.T) {
	root, _ := initWorktreeRepo(t)
	tools := worktreeToolMap(t, root)

	out := mustInvoke(t, tools["enter_worktree"], map[string]any{"name": "dup"})
	var entered struct {
		Path string `json:"path"`
	}
	decodeWorktreeJSON(t, out, &entered)
	if _, err := invokeTool(t, tools["enter_worktree"], map[string]any{"name": "dup"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate enter error = %v, want an already-exists error", err)
	}

	// An existing branch is rejected even when the path does not exist.
	gitTestRun(t, root, "branch", "taken")
	if _, err := invokeTool(t, tools["enter_worktree"], map[string]any{"name": "taken"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing-branch enter error = %v, want an already-exists error", err)
	}

	for _, name := range []string{"../escape", "a/b", `a\\b`, "..", "-option", "with space"} {
		if _, err := invokeTool(t, tools["enter_worktree"], map[string]any{"name": name}); err == nil || !strings.Contains(err.Error(), "invalid worktree name") {
			t.Errorf("enter name %q error = %v, want invalid-name", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !os.IsNotExist(err) {
		t.Errorf("traversal name created %q outside the workspace", filepath.Join(filepath.Dir(root), "escape"))
	}

	for _, path := range []string{"../../etc", filepath.Join(t.TempDir(), "elsewhere")} {
		if _, err := invokeTool(t, tools["exit_worktree"], map[string]any{"path": path}); err == nil || !strings.Contains(err.Error(), "outside the workspace root") {
			t.Errorf("exit path %q error = %v, want outside-root", path, err)
		}
	}
	if _, err := invokeTool(t, tools["exit_worktree"], map[string]any{"name": "../escape"}); err == nil || !strings.Contains(err.Error(), "invalid worktree name") {
		t.Errorf("exit traversal name error = %v, want invalid-name", err)
	}
	if _, err := invokeTool(t, tools["exit_worktree"], map[string]any{}); err == nil || !strings.Contains(err.Error(), `"path" or "name"`) {
		t.Errorf("empty exit args error = %v, want a missing-argument error", err)
	}

	// A plain directory below the managed directory is not a registered
	// worktree and must not be deleted.
	plain := filepath.Join(root, worktreeDirName, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeTool(t, tools["exit_worktree"], map[string]any{"name": "plain"}); err == nil || !strings.Contains(err.Error(), "not a registered worktree") {
		t.Errorf("plain-directory exit error = %v, want not-registered", err)
	}
	if _, err := os.Stat(plain); err != nil {
		t.Errorf("plain directory was removed: %v", err)
	}

	// The main checkout can never be removed.
	if _, err := invokeTool(t, tools["exit_worktree"], map[string]any{"path": root}); err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Errorf("main-checkout exit error = %v, want a refusal", err)
	}
}

func TestWorktreeNonRepository(t *testing.T) {
	root := t.TempDir()
	tools := worktreeToolMap(t, root)
	for _, name := range []string{"enter_worktree", "exit_worktree", "list_worktrees"} {
		args := map[string]any{}
		if name == "enter_worktree" {
			args["name"] = "x"
		}
		if name == "exit_worktree" {
			args["name"] = "x"
		}
		if _, err := invokeTool(t, tools[name], args); err == nil || !strings.Contains(err.Error(), "not a Git repository") {
			t.Errorf("%s in non-repository error = %v, want not-a-Git-repository", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, worktreeDirName)); !os.IsNotExist(err) {
		t.Errorf("non-repository call created the managed directory")
	}

	// An empty workspace root is a clear configuration error, not a panic.
	empty := worktreeToolMap(t, "")
	if _, err := invokeTool(t, empty["list_worktrees"], map[string]any{}); err == nil || !strings.Contains(err.Error(), "workspace root is required") {
		t.Errorf("empty-root error = %v", err)
	}
}

func TestWorktreeUnbornHead(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	gitTestRun(t, root, "init", "-q")
	tools := worktreeToolMap(t, root)

	// A repository with no commit cannot create a worktree; report that
	// explicitly instead of surfacing an opaque git failure.
	if _, err := invokeTool(t, tools["enter_worktree"], map[string]any{"name": "first"}); err == nil || !strings.Contains(err.Error(), "no usable HEAD commit") {
		t.Errorf("unborn-HEAD error = %v, want no-usable-HEAD", err)
	}
	if _, err := os.Stat(filepath.Join(root, worktreeDirName)); !os.IsNotExist(err) {
		t.Errorf("failed enter created the managed directory")
	}
}

func TestWorktreePorcelainParsing(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "repo", worktreeDirName)
	raw := strings.Join([]string{
		"worktree /repo",
		"HEAD aaaa",
		"branch refs/heads/main",
		"",
		"worktree /repo/.zeokeel-worktrees/one",
		"HEAD bbbb",
		"detached",
		"locked maintenance",
		"",
		"worktree /repo/.zeokeel-worktrees/two",
		"HEAD cccc",
		"branch refs/heads/two",
		"prunable gitdir file points to non-existent location",
		"",
	}, "\n")
	views := parseWorktreePorcelain(raw, base)
	if len(views) != 3 {
		t.Fatalf("parsed %d worktrees, want 3: %+v", len(views), views)
	}
	if views[0].Managed || views[0].Branch != "main" || views[0].BranchRef != "refs/heads/main" || views[0].Head != "aaaa" {
		t.Errorf("main worktree = %+v", views[0])
	}
	if !views[1].Managed || !views[1].Detached || !views[1].Locked || views[1].LockReason != "maintenance" {
		t.Errorf("detached worktree = %+v", views[1])
	}
	if !views[2].Managed || !views[2].Prunable || views[2].PrunableReason != "gitdir file points to non-existent location" {
		t.Errorf("prunable worktree = %+v", views[2])
	}
	if views[2].Branch != "two" {
		t.Errorf("branch = %q, want two", views[2].Branch)
	}
}
