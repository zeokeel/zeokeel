package rightpanel_test

// Regression tests for the workspace-switch caches: opened file tabs survive a
// workspace switch (the documented UX), but every path-keyed cache is reset and
// the content is re-read from the new workspace, so the same relative path can
// never show the previous workspace's bytes.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// switchCurrent makes another workspace the current one, as the host's
// workspace switcher does.
func (w *fakeWorkspace) switchCurrent(workspace contracts.Workspace) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.current = workspace
	w.hasCurrent = true
}

// waitFilePreviewValue waits until the read-only editor's accessibility value
// satisfies the wanted/absent substrings.
func waitFilePreviewValue(t *testing.T, tester *ui.Tester, want, absent string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last string
	for {
		tester.Frame()
		for _, label := range []string{"文件内容", "File content"} {
			if info, ok := tester.AccessByRole(ui.RoleTextField, label); ok {
				last = info.Value
				if strings.Contains(info.Value, want) && (absent == "" || !strings.Contains(info.Value, absent)) {
					return info.Value
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("file preview value = %q, want it to contain %q and not %q; texts = %v",
				last, want, absent, tester.Texts())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestWorkspaceSwitchRefreshesOpenFileTab: with the same relative file open,
// switching the current workspace must re-read the file (and rebuild the editor
// state), never paint the previous workspace's buffer. The tab itself stays
// open: switching workspaces does not throw the user's tabs away.
func TestWorkspaceSwitchRefreshesOpenFileTab(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("note.txt", false))
	workspace.setFile("note.txt", []byte("ALPHA_CONTENT\n"))

	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "note.txt")
	h.click(tester, "note.txt")
	h.assertTexts(tester, "关闭 note.txt")
	waitFilePreviewValue(t, tester, "ALPHA_CONTENT", "")

	// The host switches to another workspace whose same relative path holds
	// different bytes.
	workspace.switchCurrent(contracts.Workspace{ID: "beta", Name: "beta", Root: "/work/beta"})
	workspace.setFile("note.txt", []byte("BETA_CONTENT\n"))

	value := waitFilePreviewValue(t, tester, "BETA_CONTENT", "ALPHA_CONTENT")
	if strings.Contains(value, "ALPHA_CONTENT") {
		t.Fatalf("the editor still shows the previous workspace's content: %q", value)
	}
	// The tab survives the switch (documented): the user does not lose their
	// open files when the workspace changes.
	if !tester.HasText("关闭 note.txt") {
		t.Fatalf("the opened file tab disappeared on a workspace switch; texts = %v", tester.Texts())
	}
}

// TestWorkspaceSwitchReReadsMarkdownPreview: the Markdown preview is
// path-keyed too, so a switch must replace it with the new workspace's content.
func TestWorkspaceSwitchReReadsMarkdownPreview(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	workspace.setFile("README.md", []byte("# ALPHA_HEADING\n"))

	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.click(tester, "README.md")
	h.assertTexts(tester, "ALPHA_HEADING")

	workspace.switchCurrent(contracts.Workspace{ID: "beta", Name: "beta", Root: "/work/beta"})
	workspace.setFile("README.md", []byte("# BETA_HEADING\n"))
	h.assertTexts(tester, "BETA_HEADING")
	h.assertNoTexts(tester, "ALPHA_HEADING")
}
