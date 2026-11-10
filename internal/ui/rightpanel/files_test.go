package rightpanel_test

// Tests for the inspector's Files pane: optional workspace backend, the
// open-folder flow, directory navigation/breadcrumb, read-only previews and the
// error states.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// tinyPNG is a valid 1x1 PNG for the image preview test.
var tinyPNG = func() []byte {
	data, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	return data
}()

// --- fake WorkspaceService --------------------------------------------------

type createCall struct {
	name string
	root string
}

type fakeWorkspace struct {
	mu sync.Mutex

	workspaces []contracts.Workspace
	current    contracts.Workspace
	hasCurrent bool
	dirs       map[string][]contracts.FileEntry
	files      map[string][]byte
	readErrs   map[string]error
	filesErr   error
	currentErr error
	createErr  error
	readErr    error

	createCalls []createCall
	reads       []string
}

func newFakeWorkspace() *fakeWorkspace {
	return &fakeWorkspace{
		dirs:     make(map[string][]contracts.FileEntry),
		files:    make(map[string][]byte),
		readErrs: make(map[string]error),
	}
}

func (w *fakeWorkspace) setCurrent(workspace contracts.Workspace) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workspaces = append(w.workspaces, workspace)
	w.current = workspace
	w.hasCurrent = true
}

func (w *fakeWorkspace) setDir(dir string, entries ...contracts.FileEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirs[dir] = entries
}

func (w *fakeWorkspace) setFile(path string, data []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.files[path] = data
}

func (w *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]contracts.Workspace(nil), w.workspaces...), nil
}

func (w *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.currentErr != nil {
		return contracts.Workspace{}, false, w.currentErr
	}
	return w.current, w.hasCurrent, nil
}

func (w *fakeWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.createCalls = append(w.createCalls, createCall{name: name, root: root})
	if w.createErr != nil {
		return contracts.Workspace{}, w.createErr
	}
	workspace := contracts.Workspace{ID: "ws-new", Name: name, Root: root, CreatedAt: time.Unix(1700000000, 0)}
	w.workspaces = append(w.workspaces, workspace)
	w.current = workspace
	w.hasCurrent = true
	return workspace, nil
}

func (w *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, workspace := range w.workspaces {
		if workspace.ID == id {
			w.current = workspace
			w.hasCurrent = true
			return workspace, nil
		}
	}
	return contracts.Workspace{}, fmt.Errorf("unknown workspace %q", id)
}

func (w *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (w *fakeWorkspace) Files(_ context.Context, dir string) ([]contracts.FileEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.filesErr != nil {
		return nil, w.filesErr
	}
	entries := append([]contracts.FileEntry(nil), w.dirs[dir]...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func (w *fakeWorkspace) ReadFile(_ context.Context, path string, limit int) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reads = append(w.reads, path)
	if w.readErr != nil {
		return nil, w.readErr
	}
	if err := w.readErrs[path]; err != nil {
		return nil, err
	}
	data, ok := w.files[path]
	if !ok {
		return nil, fmt.Errorf("no such file %q", path)
	}
	if limit > 0 && len(data) > limit {
		data = data[:limit]
	}
	return append([]byte(nil), data...), nil
}

func (w *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *fakeWorkspace) creates() []createCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]createCall(nil), w.createCalls...)
}

// --- helpers ----------------------------------------------------------------

func setDirectoryPicker(t *testing.T, fn func(title string) (string, error)) {
	t.Helper()
	previous := rightpanel.DirectoryPicker
	rightpanel.DirectoryPicker = fn
	t.Cleanup(func() { rightpanel.DirectoryPicker = previous })
}

// assertPreviewContains checks the official code editor's accessibility value:
// the source text is painted on the editor's canvas (not in Texts), so the a11y
// text field is the user-visible content's programmatic equivalent.
func assertPreviewContains(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	found := false
	settled := pumpFrames(tester, func() bool {
		for _, label := range []string{"文件内容", "File content"} {
			if info, ok := tester.AccessByRole(ui.RoleTextField, label); ok {
				found = true
				return strings.Contains(info.Value, want)
			}
		}
		return false
	}, 3*time.Second)
	if settled {
		return
	}
	if !found {
		t.Fatalf("file preview editor is not exposed as a text field; texts = %v", tester.Texts())
	}
	t.Fatalf("file preview does not contain %q; texts = %v", want, tester.Texts())
}

func joinedTexts(tester *ui.Tester) string {
	return strings.Join(tester.Texts(), "\n")
}

func assertContains(t *testing.T, tester *ui.Tester, fragments ...string) {
	t.Helper()
	settled := pumpFrames(tester, func() bool {
		frame := joinedTexts(tester)
		for _, fragment := range fragments {
			if !strings.Contains(frame, fragment) {
				return false
			}
		}
		return true
	}, 3*time.Second)
	if !settled {
		t.Fatalf("frame does not contain %v; texts = %v", fragments, tester.Texts())
	}
}

// --- tests ------------------------------------------------------------------

func TestFilesPaneWithoutBackend(t *testing.T) {
	h := newHarness(t, newFakeChat(), rightpanel.Config{})
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "工作区未启用")
}

func TestFilesOpenWorkspaceFlow(t *testing.T) {
	workspace := newFakeWorkspace()
	setDirectoryPicker(t, func(string) (string, error) { return "/tmp/proj-alpha", nil })
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "还没有打开工作区", "打开文件夹")

	h.click(tester, "打开文件夹")
	tester.Frame()
	creates := workspace.creates()
	if len(creates) != 1 || creates[0].name != "proj-alpha" || creates[0].root != "/tmp/proj-alpha" {
		t.Fatalf("Create calls = %+v, want proj-alpha//tmp/proj-alpha", creates)
	}
	h.assertTexts(tester, "工作区", "proj-alpha", "/tmp/proj-alpha", "刷新",
		"/proj-alpha", "目录为空", "选择文件以预览")
}

func TestFilesOpenWorkspaceErrors(t *testing.T) {
	t.Run("picker error", func(t *testing.T) {
		workspace := newFakeWorkspace()
		setDirectoryPicker(t, func(string) (string, error) { return "", errors.New("dialog failed") })
		h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
		tester := h.tester()
		h.selectPane(tester, "文件")
		h.click(tester, "打开文件夹")
		h.assertTexts(tester, "无法打开文件夹选择器：dialog failed")
	})

	t.Run("create error", func(t *testing.T) {
		workspace := newFakeWorkspace()
		workspace.createErr = errors.New("permission denied")
		setDirectoryPicker(t, func(string) (string, error) { return "/root/forbidden", nil })
		h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
		tester := h.tester()
		h.selectPane(tester, "文件")
		h.click(tester, "打开文件夹")
		h.assertTexts(tester, "打开工作区失败：permission denied")
	})

	t.Run("cancel keeps the empty state", func(t *testing.T) {
		workspace := newFakeWorkspace()
		setDirectoryPicker(t, func(string) (string, error) { return "", nil })
		h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
		tester := h.tester()
		h.selectPane(tester, "文件")
		h.click(tester, "打开文件夹")
		if creates := workspace.creates(); len(creates) != 0 {
			t.Fatalf("Create calls = %+v, want none on cancel", creates)
		}
		h.assertTexts(tester, "还没有打开工作区")
	})
}

func filesTreeWorkspace() *fakeWorkspace {
	workspace := newFakeWorkspace()
	workspace.setCurrent(contracts.Workspace{ID: "ws-1", Name: "proj", Root: "/work/proj"})
	workspace.setDir("",
		contracts.FileEntry{Name: "docs", Path: "docs", Dir: true},
		contracts.FileEntry{Name: "main.go", Path: "main.go", Size: 13},
		contracts.FileEntry{Name: "chart.png", Path: "chart.png", Size: int64(len(tinyPNG))},
	)
	workspace.setDir("docs",
		contracts.FileEntry{Name: "api", Path: "docs/api", Dir: true},
		contracts.FileEntry{Name: "readme.md", Path: "docs/readme.md", Size: 20},
	)
	workspace.setDir("docs/api",
		contracts.FileEntry{Name: "spec.md", Path: "docs/api/spec.md", Size: 9},
	)
	workspace.setFile("main.go", []byte("package main\n"))
	workspace.setFile("docs/readme.md", []byte("# 文档标题\n正文内容"))
	workspace.setFile("docs/api/spec.md", []byte("# API 规范\n"))
	workspace.setFile("chart.png", tinyPNG)
	return workspace
}

// clickCrumb clicks a breadcrumb link by its accessible role and name: the
// crumb's text (the workspace name) may also appear in the pane header, so the
// role/name pair is the unambiguous target.
func clickCrumb(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleLink, name)
	if !ok {
		t.Fatalf("breadcrumb link %q missing; texts = %v", name, tester.Texts())
	}
	tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tester.Frame()
	waitFilesSettled(t, tester)
}

// openEntry activates one directory row the way the Files list now opens it:
// a single click only chooses the row, and the list's Submitted action (Enter,
// or a double click) opens it. The pointer click picks the row first so the
// keyboard focus is on the native list.
func openEntry(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	settled := pumpFrames(tester, func() bool {
		for _, loading := range paneLoadingLabels {
			if tester.HasText(loading) {
				return false
			}
		}
		return true
	}, 3*time.Second)
	if !settled {
		t.Fatalf("files pane did not settle before opening %q; texts = %v", label, tester.Texts())
	}
	if !pumpFrames(tester, func() bool { _, ok := tester.Find(label); return ok }, 3*time.Second) {
		t.Fatalf("row %q not found; texts = %v", label, tester.Texts())
	}
	if err := tester.Click(label); err != nil {
		t.Fatalf("choose row %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	waitFilesSettled(t, tester)
}

// clickFileRow waits for one Files row to build, then clicks it: the off-thread
// listing load and the row build are separate frames on a slow runner.
func clickFileRow(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if !pumpFrames(tester, func() bool { _, ok := tester.Find(label); return ok }, 3*time.Second) {
		t.Fatalf("file row %q not found; texts = %v", label, tester.Texts())
	}
	if err := tester.Click(label); err != nil {
		t.Fatalf("open %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// waitFilesSettled waits until the Files pane's off-thread listing read landed:
// the pane shows a loading line while the read is in flight.
func waitFilesSettled(t *testing.T, tester *ui.Tester) {
	t.Helper()
	settled := pumpFrames(tester, func() bool {
		return !tester.HasText("正在读取目录…") && !tester.HasText("Reading the directory…")
	}, 3*time.Second)
	if !settled {
		t.Fatalf("directory listing did not settle; texts = %v", tester.Texts())
	}
}

func TestFilesNavigationAndBreadcrumb(t *testing.T) {
	workspace := filesTreeWorkspace()
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "proj", "/work/proj", "docs", "main.go", "chart.png")
	// The trail is the official Breadcrumbs: a navigation landmark named 路径.
	if _, ok := tester.AccessByRole(ui.RoleNavigation, "路径"); !ok {
		t.Fatalf("breadcrumb is not a navigation landmark; texts = %v", tester.Texts())
	}

	// Enter a directory, then a nested one; the breadcrumb grows and the last
	// crumb is the current page (the earlier ones are links that navigate).
	openEntry(t, tester, "docs")
	h.assertTexts(tester, "api", "readme.md")
	if _, ok := tester.AccessByRole(ui.RoleLink, "proj"); !ok {
		t.Fatalf("root crumb is not a link; texts = %v", tester.Texts())
	}
	page, ok := tester.AccessByRole(ui.RoleText, "docs")
	if !ok || page.Current != ui.CurrentPage {
		t.Fatalf("current crumb = %+v found %v, want the current page", page, ok)
	}
	openEntry(t, tester, "api")
	h.assertTexts(tester, "spec.md")
	if _, ok := tester.AccessByRole(ui.RoleText, "api"); !ok {
		t.Fatalf("nested current crumb missing; texts = %v", tester.Texts())
	}

	// Clicking a breadcrumb segment returns to that directory.
	clickCrumb(t, tester, "docs")
	h.assertTexts(tester, "readme.md")
	h.assertNoTexts(tester, "spec.md")
	clickCrumb(t, tester, "proj")
	h.assertTexts(tester, "docs", "main.go")
}

// TestFilesBreadcrumbDeepPathCollapses covers the compact middle-collapse of
// deep paths: the first segment, an ellipsis crumb that jumps to the parent of
// the shown tail, then the last two segments.
func TestFilesBreadcrumbDeepPathCollapses(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent(contracts.Workspace{ID: "ws-deep", Name: "deep", Root: "/work/deep"})
	parent := func(path string) string {
		if cut := strings.LastIndex(path, "/"); cut >= 0 {
			return path[:cut]
		}
		return ""
	}
	for depth, path := range []string{"a", "a/b", "a/b/c", "a/b/c/d", "a/b/c/d/e"} {
		name := []string{"a", "b", "c", "d", "e"}[depth]
		workspace.setDir(parent(path), contracts.FileEntry{Name: name, Path: path, Dir: true})
	}
	workspace.setDir("a/b/c/d/e", contracts.FileEntry{Name: "leaf.md", Path: "a/b/c/d/e/leaf.md", Size: 4})
	workspace.setFile("a/b/c/d/e/leaf.md", []byte("leaf"))

	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		openEntry(t, tester, name)
	}
	h.assertTexts(tester, "leaf.md", "…", "d", "e")
	if _, ok := tester.AccessByRole(ui.RoleLink, "a"); !ok {
		t.Fatalf("first segment crumb missing; texts = %v", tester.Texts())
	}
	// The ellipsis is a link that jumps up to a/b/c, the parent of the tail.
	if _, ok := tester.AccessByRole(ui.RoleLink, "…"); !ok {
		t.Fatalf("ellipsis crumb is not a link; texts = %v", tester.Texts())
	}
	clickCrumb(t, tester, "…")
	h.assertTexts(tester, "d")
	h.assertNoTexts(tester, "leaf.md")
}

func TestFilesTextAndMarkdownPreview(t *testing.T) {
	workspace := filesTreeWorkspace()
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "选择文件以预览")

	// Plain text file: the preview shows its content as native text.
	h.click(tester, "main.go")
	assertPreviewContains(t, tester, "package main")
	h.assertTexts(tester, "关闭预览")
	if err := tester.Click("关闭预览"); err != nil {
		t.Fatalf("close preview: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "选择文件以预览")

	// Markdown file: rendered by the official view (heading/list markers gone).
	openEntry(t, tester, "docs")
	clickFileRow(t, tester, "readme.md")
	tester.Frame()
	assertContains(t, tester, "文档标题", "正文内容")
}

func TestFilesImagePreview(t *testing.T) {
	workspace := filesTreeWorkspace()
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	clickFileRow(t, tester, "chart.png")
	tester.Frame()
	h.assertTexts(tester, "chart.png", "关闭预览")
	h.assertNoTexts(tester, "暂不支持预览", "读取文件失败")
}

func TestFilesUnsupportedPreview(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent(contracts.Workspace{ID: "ws-1", Name: "proj", Root: "/work/proj"})
	workspace.setDir("", contracts.FileEntry{Name: "data.bin", Path: "data.bin", Size: 4})
	workspace.setFile("data.bin", []byte{0x00, 0x01, 0x02, 0x03})
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	clickFileRow(t, tester, "data.bin")
	tester.Frame()
	h.assertTexts(tester, "暂不支持预览", "大小", "4 B")
}

func TestFilesErrors(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		workspace := newFakeWorkspace()
		workspace.currentErr = errors.New("registry unavailable")
		h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
		tester := h.tester()
		h.selectPane(tester, "文件")
		h.assertTexts(tester, "读取工作区失败：registry unavailable")
	})

	t.Run("list", func(t *testing.T) {
		workspace := filesTreeWorkspace()
		workspace.filesErr = errors.New("permission denied")
		h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
		tester := h.tester()
		h.selectPane(tester, "文件")
		h.assertTexts(tester, "列目录失败：permission denied")
	})

	t.Run("read", func(t *testing.T) {
		workspace := filesTreeWorkspace()
		workspace.readErrs["main.go"] = errors.New("file vanished")
		h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
		tester := h.tester()
		h.selectPane(tester, "文件")
		if err := tester.Click("main.go"); err != nil {
			t.Fatalf("open main.go: %v", err)
		}
		tester.Frame()
		h.assertTexts(tester, "读取文件失败：file vanished")
	})
}

func TestFilesCustomTabLabel(t *testing.T) {
	h := newHarness(t, newFakeChat(), rightpanel.Config{FilesTab: "Files"})
	tester := h.tester()
	h.selectPane(tester, "Files")
	h.assertTexts(tester, "Files", "工作区未启用")
}

// TestFilesDirectoryDialogTitleFollowsLocale pins that the native folder
// picker's dialog title is localized before the seam runs.
func TestFilesDirectoryDialogTitleFollowsLocale(t *testing.T) {
	workspace := newFakeWorkspace()
	captured := ""
	setDirectoryPicker(t, func(title string) (string, error) {
		captured = title
		return "", nil // cancel keeps the empty state for the second invocation
	})
	h := newLocaleHarness(t, "en", newFakeChat(), rightpanel.Config{}, workspace, nil)
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "Files")
	h.click(tester, "Open folder")
	if captured != "Choose workspace folder" {
		t.Fatalf("English directory dialog title = %q, want %q", captured, "Choose workspace folder")
	}

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.click(tester, "打开文件夹")
	if captured != "选择工作区文件夹" {
		t.Fatalf("Chinese directory dialog title = %q, want %q", captured, "选择工作区文件夹")
	}
}

// TestFilesHiddenEntriesAndFilter covers H21: dot entries are folded by default,
// the "show hidden" switch reveals them, and the name filter narrows the list.
func TestFilesHiddenEntriesAndFilter(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setDir("",
		contracts.FileEntry{Name: ".git", Path: ".git", Dir: true},
		contracts.FileEntry{Name: ".env", Path: ".env", Size: 5},
		contracts.FileEntry{Name: "notes.txt", Path: "notes.txt", Size: 3},
		contracts.FileEntry{Name: "main.go", Path: "main.go", Size: 9},
	)
	if _, err := workspace.Create(context.Background(), "proj", "/tmp/proj"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "notes.txt", "main.go")
	h.assertNoTexts(tester, ".git", ".env")

	// The switch is an accessible control named for the toggle, and reveals the
	// dot entries.
	if err := tester.Click("显示隐藏文件"); err != nil {
		t.Fatalf("toggle hidden files: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, ".git", ".env", "notes.txt")

	// The filter narrows the visible entries by name.
	if err := tester.Click("筛选文件"); err != nil {
		t.Fatalf("focus file filter: %v; texts = %v", err, tester.Texts())
	}
	tester.Type("notes")
	tester.Frame()
	h.assertTexts(tester, "notes.txt")
	h.assertNoTexts(tester, "main.go", ".git")

	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的条目")
}
