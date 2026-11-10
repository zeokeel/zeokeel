package uimodtest

// Workspace changes end-to-end: a real git repository with uncommitted edits
// feeds the right inspector's 变更 tab, which lists the changes and expands the
// unified diff.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

	// The right sidebar belongs to the session screen (it reads the session
	// read model), so open the session screen and dock it before browsing.
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("push session screen: %v", err)
	}
	uiRoot.SetRightPanelOpen(true)
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	// The sidebar's 文件 page is the tree of the current workspace: the
	// modified tracked file and the untracked file both show up there.
	clickRightSidebarPage(t, tester, "文件")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !tester.HasText("app.go") {
		tester.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"app.go", "notes.txt"} {
		if !tester.HasText(want) {
			t.Fatalf("workspace tree missing %q; texts = %v", want, tester.Texts())
		}
	}

	// Clicking the changed file opens its preview tab. The uncommitted diff is
	// folded into that tab (the retired Changes pane): 查看改动 waits for the
	// workspace differ's off-thread read.
	if err := tester.Click("app.go"); err != nil {
		t.Fatalf("open changed file: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !frameUntil(tester, "查看改动", 5*time.Second) {
		t.Fatalf("file tab has no 查看改动 fold; texts = %v", tester.Texts())
	}
	if !tester.HasText("关闭 app.go") {
		t.Fatalf("opened file is not its own tab; texts = %v", tester.Texts())
	}
	if err := tester.Click("查看改动"); err != nil {
		t.Fatalf("expand diff: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	// The official editor.DiffView paints the changed lines on the canvas, so
	// the text layer only carries the hunk headers; the lines are asserted
	// through the viewer's accessibility value (mygo docs/ui/diff-viewer.md;
	// Tester.Access), which screen readers read.
	newSide, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("expanded diff's new side is not exposed to assistive technology; texts = %v", tester.Texts())
	}
	if !containsText(newSide.Value, "brand new line") {
		t.Fatalf("expanded diff missing the changed line; a11y value = %q; texts = %v",
			newSide.Value, tester.Texts())
	}
}
