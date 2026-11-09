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
	for _, label := range []string{"文件内容", "File content"} {
		if info, ok := tester.AccessByRole(ui.RoleTextField, label); ok {
			if !strings.Contains(info.Value, want) {
				t.Fatalf("file preview does not contain %q; value = %q", want, info.Value)
			}
			return
		}
	}
	t.Fatalf("file preview editor is not exposed as a text field; texts = %v", tester.Texts())
}

func joinedTexts(tester *ui.Tester) string {
	return strings.Join(tester.Texts(), "\n")
}

func assertContains(t *testing.T, tester *ui.Tester, fragments ...string) {
	t.Helper()
	frame := joinedTexts(tester)
	for _, fragment := range fragments {
		if !strings.Contains(frame, fragment) {
			t.Fatalf("frame does not contain %q; texts = %v", fragment, tester.Texts())
		}
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

func TestFilesNavigationAndBreadcrumb(t *testing.T) {
	workspace := filesTreeWorkspace()
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	h.assertTexts(tester, "proj", "/work/proj", "/proj", "docs", "main.go", "chart.png")

	// Enter a directory, then a nested one; the breadcrumb grows.
	h.click(tester, "docs")
	h.assertTexts(tester, "api", "readme.md", "/proj", "/docs")
	h.click(tester, "api")
	h.assertTexts(tester, "spec.md", "/docs", "/api")

	// Clicking a breadcrumb segment returns to that directory.
	h.click(tester, "/docs")
	h.assertTexts(tester, "readme.md")
	h.assertNoTexts(tester, "spec.md")
	h.click(tester, "/proj")
	h.assertTexts(tester, "docs", "main.go")
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
	h.click(tester, "docs")
	if err := tester.Click("readme.md"); err != nil {
		t.Fatalf("open readme: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	assertContains(t, tester, "文档标题", "正文内容")
}

func TestFilesImagePreview(t *testing.T) {
	workspace := filesTreeWorkspace()
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "文件")
	if err := tester.Click("chart.png"); err != nil {
		t.Fatalf("open image: %v; texts = %v", err, tester.Texts())
	}
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
	if err := tester.Click("data.bin"); err != nil {
		t.Fatalf("open bin: %v; texts = %v", err, tester.Texts())
	}
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
