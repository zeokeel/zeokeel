package uimodtest

// Workspace changes end-to-end: a real git repository with uncommitted edits
// feeds the right inspector's 变更 tab, which lists the changes and expands the
// unified diff.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=ZeoKeel", "GIT_AUTHOR_EMAIL=test@zeokeel.local",
		"GIT_COMMITTER_NAME=ZeoKeel", "GIT_COMMITTER_EMAIL=test@zeokeel.local")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}

func TestWorkspaceChangesEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspaceRoot := t.TempDir()
	appPath := filepath.Join(workspaceRoot, "app.go")
	if err := os.WriteFile(appPath, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write app.go: %v", err)
	}
	runGit(t, workspaceRoot, "init")
	runGit(t, workspaceRoot, "add", ".")
	runGit(t, workspaceRoot, "commit", "-m", "initial")
	// One modified tracked file and one untracked file.
	if err := os.WriteFile(appPath, []byte("package main\n\nfunc main() {\n\tprintln(\"brand new line\")\n}\n"), 0o600); err != nil {
		t.Fatalf("modify app.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "notes.txt"), []byte("scratch\n"), 0o600); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	root, uiRoot, loader := bootDesktop(t, t.TempDir(), &memSettings{values: map[string]string{}})
	defer func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	}()
	workspaces, err := root.Require(contracts.KeyWorkspace)
	if err != nil {
		t.Fatalf("require workspace: %v", err)
	}
	if _, err := workspaces.Create(t.Context(), "repo", workspaceRoot); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	uiRoot.SetRightPanelOpen(true)
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	if err := tester.Click("变更"); err != nil {
		t.Fatalf("click changes tab: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	for _, want := range []string{"app.go", "notes.txt", "修改", "未跟踪"} {
		if !tester.HasText(want) {
			t.Fatalf("changes tab missing %q; texts = %v", want, tester.Texts())
		}
	}
	if err := tester.Click("查看 diff"); err != nil {
		t.Fatalf("expand diff: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	joined := ""
	for _, text := range tester.Texts() {
		joined += text + "\n"
	}
	if !containsText(joined, "brand new line") {
		t.Fatalf("expanded diff missing the changed line; texts = %v", tester.Texts())
	}
}
