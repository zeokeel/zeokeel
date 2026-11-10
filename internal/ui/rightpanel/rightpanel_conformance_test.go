package rightpanel_test

// Frame conformance tests for the inspector's mygo v0.8.3 migration:
//   - the Files listing is cached (no os.ReadDir from rendering, P1),
//   - the Files/Tools lists are the official ui.List and take the keyboard
//     (docs/ui/list.md: Up/Down/Home/End choose, Enter submits),
//   - the Terminal's shell picker is the official ui.Select
//     (docs/ui/select.md), with a keyboard path,
//   - the lists and the shell select are named for assistive technology
//     (docs/ui/accessibility.md).
// The official diff viewer (docs/ui/diff-viewer.md) is covered by the internal
// state tests and by TestChangesListStatsAndExpand through Tester.Access.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// countingWorkspace wraps the scripted workspace and counts its directory
// listings, to pin that the Files pane does not read the filesystem per frame.
type countingWorkspace struct {
	*fakeWorkspace
	mu         sync.Mutex
	filesCalls int
}

func newCountingWorkspace() *countingWorkspace {
	return &countingWorkspace{fakeWorkspace: newFakeWorkspace()}
}

func (w *countingWorkspace) Files(ctx context.Context, dir string) ([]contracts.FileEntry, error) {
	w.mu.Lock()
	w.filesCalls++
	w.mu.Unlock()
	return w.fakeWorkspace.Files(ctx, dir)
}

func (w *countingWorkspace) listings() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.filesCalls
}

func TestFilesListingIsCachedBetweenFrames(t *testing.T) {
	workspace := newCountingWorkspace()
	workspace.setCurrent(contracts.Workspace{ID: "ws-cache", Name: "cache", Root: "/work/cache"})
	workspace.setDir("",
		contracts.FileEntry{Name: "docs", Path: "docs", Dir: true},
		contracts.FileEntry{Name: "main.go", Path: "main.go", Size: 13},
	)
	workspace.setDir("docs", contracts.FileEntry{Name: "readme.md", Path: "docs/readme.md", Size: 20})
	workspace.setFile("main.go", []byte("package main\n"))

	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "docs", "main.go")
	if got := workspace.listings(); got != 1 {
		t.Fatalf("directory listings = %d after the first frame, want 1", got)
	}

	// Rendering more frames reuses the cache: no per-frame os.ReadDir.
	for index := 0; index < 3; index++ {
		tester.Frame()
	}
	if got := workspace.listings(); got != 1 {
		t.Fatalf("directory listings = %d after extra frames, want 1 (no render-time I/O)", got)
	}

	// The native Refresh re-reads exactly once.
	h.click(tester, "刷新")
	tester.Frame()
	if got := workspace.listings(); got != 2 {
		t.Fatalf("directory listings = %d after Refresh, want 2", got)
	}

	// Entering a directory reads the new directory exactly once. A click only
	// chooses the row now; the list's Submitted action opens the directory.
	openEntry(t, tester, "docs")
	if got := workspace.listings(); got != 3 {
		t.Fatalf("directory listings = %d after navigating, want 3", got)
	}
	h.assertTexts(tester, "readme.md")
	if got := workspace.listings(); got != 3 {
		t.Fatalf("directory listings = %d after a rendered frame, want 3", got)
	}
}

// filesKeyboardWorkspace is a flat directory of previewable files, for the
// Files list's keyboard path.
func filesKeyboardWorkspace() *fakeWorkspace {
	workspace := newFakeWorkspace()
	workspace.setCurrent(contracts.Workspace{ID: "ws-kb", Name: "kb", Root: "/work/kb"})
	workspace.setDir("",
		contracts.FileEntry{Name: "alpha.txt", Path: "alpha.txt", Size: 5},
		contracts.FileEntry{Name: "bravo.txt", Path: "bravo.txt", Size: 5},
		contracts.FileEntry{Name: "charlie.txt", Path: "charlie.txt", Size: 5},
	)
	workspace.setFile("alpha.txt", []byte("alpha content"))
	workspace.setFile("bravo.txt", []byte("bravo content"))
	workspace.setFile("charlie.txt", []byte("charlie content"))
	return workspace
}

func TestFilesListKeyboardChoosesEntries(t *testing.T) {
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, filesKeyboardWorkspace())
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "alpha.txt", "bravo.txt", "charlie.txt", "选择文件以预览")

	// A click focuses the list (and previews), the arrows choose the rows and
	// the preview follows the choice.
	if err := tester.Click("alpha.txt"); err != nil {
		t.Fatalf("click alpha.txt: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	assertPreviewContains(t, tester, "alpha content")

	tester.Key(0, ui.KeyDown)
	tester.Frame()
	assertPreviewContains(t, tester, "bravo content")

	tester.Key(0, ui.KeyDown)
	tester.Frame()
	assertPreviewContains(t, tester, "charlie content")

	tester.Key(0, ui.KeyUp)
	tester.Frame()
	assertPreviewContains(t, tester, "bravo content")

	tester.Key(0, ui.KeyHome)
	tester.Frame()
	assertPreviewContains(t, tester, "alpha content")

	// Enter submits the chosen row; a file's submit keeps its preview.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	assertPreviewContains(t, tester, "alpha content")

	// The list exposes the chosen entry to assistive technology.
	info, ok := tester.AccessByRole(ui.RoleList, "工作区文件")
	if !ok {
		t.Fatalf("the Files list is not exposed as a named list; texts = %v", tester.Texts())
	}
	if info.Role != ui.RoleList {
		t.Fatalf("Files list role = %v, want RoleList", info.Role)
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, "alpha.txt"); !ok {
		t.Fatalf("the Files list rows are not exposed by name; texts = %v", tester.Texts())
	}
}

// TestFilesRowContextMenu covers the Files row's right-click menu: it offers
// the same open action as a click and copies the entry's path through the
// official Clipboard service (mygo context-menu.md: one menu, opened by the
// pointer's right button and by the menu key).
func TestFilesRowContextMenu(t *testing.T) {
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, filesKeyboardWorkspace())
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "alpha.txt", "bravo.txt")

	if err := tester.RightClick("bravo.txt"); err != nil {
		t.Fatalf("right-click bravo.txt: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	items := tester.Menu()
	for _, want := range []string{"打开", "复制路径"} {
		if !menuHasItem(items, want) {
			t.Fatalf("context menu = %v, want item %q", items, want)
		}
	}

	if err := tester.ChooseMenuItem("复制路径"); err != nil {
		t.Fatalf("choose Copy path: %v; menu = %v", err, tester.Menu())
	}
	tester.Frame()
	if got := tester.Clipboard(); got != "bravo.txt" {
		t.Fatalf("clipboard = %q, want bravo.txt", got)
	}

	// The open item follows the same path as a click: the preview shows.
	if err := tester.RightClick("alpha.txt"); err != nil {
		t.Fatalf("right-click alpha.txt: %v", err)
	}
	tester.Frame()
	if err := tester.ChooseMenuItem("打开"); err != nil {
		t.Fatalf("choose Open: %v; menu = %v", err, tester.Menu())
	}
	tester.Frame()
	assertPreviewContains(t, tester, "alpha content")
}

// menuHasItem reports whether the tester's menu items contain want (items may
// carry their shortcut or check mark after the label).
func menuHasItem(items []string, want string) bool {
	for _, item := range items {
		if item == want || strings.HasPrefix(item, want+" ") || strings.HasPrefix(item, want+"\t") {
			return true
		}
	}
	return false
}

// TestToolsListKeyboardTogglesSummary pins the Tools pane's keyboard path:
// Up/Down choose a call, Enter opens or closes its summary.
func TestToolsListKeyboardTogglesSummary(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID:    "s1",
		Title: "工具会话",
		Messages: []contracts.ChatMessage{{
			ID:   "m1",
			Role: contracts.ChatRoleAssistant,
			Tools: []contracts.ToolCall{
				{ID: "t1", Name: "bash", Status: "running", Summary: "stdout: all tests passed"},
				{ID: "t2", Name: "read_file", Status: "ok"},
			},
		}},
	}, true)
	h := newHarness(t, chat, rightpanel.Config{})
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "工具")
	tester.Frame()

	if err := tester.Click("bash"); err != nil {
		t.Fatalf("choose bash: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "stdout: all tests passed") // folded by default

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	assertContains(t, tester, "stdout: all tests passed")

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	h.assertNoTexts(tester, "stdout: all tests passed")
}

// TestTerminalShellSelectKeyboardSwitches pins the official ui.Select keyboard
// path of the shell picker: Enter opens the popup on the current shell, the
// arrows move, Enter chooses and calls SetShell.
func TestTerminalShellSelectKeyboardSwitches(t *testing.T) {
	terminal := newFakeTerminal()
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.selectPane(tester, "终端")

	// Focus the select, open it, move to the second shell and choose it.
	if err := tester.Click("选择 shell"); err != nil {
		t.Fatalf("focus the shell select: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("当前") {
		t.Fatalf("the shell select lacks its current label; texts = %v", tester.Texts())
	}
	if !tester.HasText("zsh") {
		t.Fatalf("the select popup does not list the shells; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	calls := terminal.shellCalls()
	if len(calls) != 1 || calls[0] != "zsh" {
		t.Fatalf("SetShell calls = %v, want [zsh] from the select's keyboard path", calls)
	}
	if !strings.Contains(strings.Join(tester.Texts(), " "), "zsh") {
		t.Fatalf("the select value did not follow the choice; texts = %v", tester.Texts())
	}
}
