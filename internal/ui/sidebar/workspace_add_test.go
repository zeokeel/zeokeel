package sidebar_test

// Frame tests for the sidebar's folder picker: the switch dialog's
// 选择其他文件夹… button opens the native directory dialog directly, registers
// the chosen folder through WorkspaceService.Create with an empty name (the
// backend derives it) and makes it the open folder. A cancelled picker changes
// nothing; failures stay visible in the dialog; a folder that is already
// registered is a switch, not an error.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// recordingWorkspace records Create/Open calls on top of the fake registry and
// mirrors the real backend: Create derives an empty name from the folder, an
// already registered root is reused and made current, and both paths leave the
// workspace listed and current.
type recordingWorkspace struct {
	*fakeWorkspace
	createName  string
	createRoot  string
	createErr   error
	createCalls int
	created     contracts.Workspace // optional preset return value
	openCalls   []string
	openErr     error
}

func (f *recordingWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.createName = name
	f.createRoot = root
	if f.createErr != nil {
		return contracts.Workspace{}, f.createErr
	}
	for _, ws := range f.workspaces {
		if filepath.Clean(strings.TrimSpace(ws.Root)) == filepath.Clean(strings.TrimSpace(root)) {
			f.current, f.hasCurrent = ws, true
			return ws, nil
		}
	}
	created := f.created
	if created.ID == "" {
		derived := strings.TrimSpace(name)
		if derived == "" {
			derived = filepath.Base(filepath.Clean(root))
		}
		created = contracts.Workspace{ID: "w-new", Name: derived, Root: root, CreatedAt: time.Now()}
	}
	f.workspaces = append(f.workspaces, created)
	f.current, f.hasCurrent = created, true
	return created, nil
}

func (f *recordingWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, id)
	if f.openErr != nil {
		return contracts.Workspace{}, f.openErr
	}
	for _, ws := range f.workspaces {
		if ws.ID == id {
			f.current, f.hasCurrent = ws, true
			return ws, nil
		}
	}
	if f.created.ID == id {
		f.current, f.hasCurrent = f.created, true
		return f.created, nil
	}
	return contracts.Workspace{ID: id}, nil
}

// overrideSidebarPicker replaces the workspace directory-picker seam for one
// test and restores it afterwards.
func overrideSidebarPicker(t *testing.T, fn func(title string) (string, error)) {
	t.Helper()
	previous := sidebar.DirectoryPicker
	sidebar.DirectoryPicker = fn
	t.Cleanup(func() { sidebar.DirectoryPicker = previous })
}

// pickFolderFromDialog opens the switch dialog and clicks its folder picker.
func pickFolderFromDialog(t *testing.T, h *harness) {
	t.Helper()
	tester := h.tester()
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if err := tester.Click("选择其他文件夹…"); err != nil {
		t.Fatalf("click the folder picker: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
}

func TestSwitchDialogPickerPicksFolderDirectly(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	var title string
	overrideSidebarPicker(t, func(t string) (string, error) {
		title = t
		return "/work/myproj", nil
	})
	tester := h.tester()
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()

	h.click(tester, "选择其他文件夹…")

	// One click ran the native folder chooser: no naming dialog in between.
	if title != "选择工作文件夹" {
		t.Fatalf("directory picker title = %q, want 选择工作文件夹", title)
	}
	h.assertNoTexts(tester, "名称", "目录", "选择目录…", "未命名工作文件夹")
	if workspace.createCalls != 1 || workspace.createName != "" || workspace.createRoot != "/work/myproj" {
		t.Fatalf("Create = %d calls name %q root %q, want the empty name and the chosen folder",
			workspace.createCalls, workspace.createName, workspace.createRoot)
	}
	// The backend derived the name from the folder; the dialog closed and the
	// header names the new folder.
	h.assertTexts(tester, "已添加工作区", "myproj")
	h.assertNoTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
}

func TestSwitchDialogPickerCancelIsNoOp(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "", nil })

	pickFolderFromDialog(t, h)
	if workspace.createCalls != 0 {
		t.Fatalf("cancelled picker called Create %d times", workspace.createCalls)
	}
	tester := h.tester()
	h.assertNoTexts(tester, "已添加工作区", "已切换工作区", "无法打开目录选择器", "添加工作区失败")
	// The dialog is still up (the picker cancelled, the decision did not).
	h.assertTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
}

func TestSwitchDialogPickerErrorIsVisible(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "", errors.New("picker failed") })

	pickFolderFromDialog(t, h)
	tester := h.tester()
	h.assertTexts(tester, "无法打开目录选择器：picker failed")
	if workspace.createCalls != 0 {
		t.Fatalf("picker failure called Create %d times", workspace.createCalls)
	}
	// The persistent inline error row does not disappear on later frames.
	tester.Frame()
	h.assertTexts(tester, "无法打开目录选择器：picker failed")
}

func TestSwitchDialogPickerCreateErrorIsVisible(t *testing.T) {
	workspace := &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(),
		createErr:     errors.New("workspace exists"),
	}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/dup", nil })

	pickFolderFromDialog(t, h)
	tester := h.tester()
	h.assertTexts(tester, "添加工作区失败：workspace exists")
	h.assertNoTexts(tester, "已添加工作区")
	// The dialog stays up so the user can retry or cancel.
	h.assertTexts(tester, "切换后侧栏、文件、记忆和自动化都跟着这个文件夹。")
}

func TestSwitchDialogPickerReusesRegisteredFolder(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/one", nil })

	pickFolderFromDialog(t, h)
	if len(workspace.openCalls) != 0 {
		t.Fatalf("the direct pick flow opened a workspace separately: %v", workspace.openCalls)
	}
	if workspace.createName != "" || workspace.createRoot != "/work/one" {
		t.Fatalf("Create = name %q root %q", workspace.createName, workspace.createRoot)
	}
	// Already registered is a switch, not an error, and the list shows the
	// folder's conversation at once.
	tester := h.tester()
	h.assertTexts(tester, "已切换工作区", "A 会话", "项目一")
	h.assertNoTexts(tester, "已添加工作区", "添加工作区失败")
}

func TestSwitchDialogPickerNotifierReportsToasts(t *testing.T) {
	workspace := &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(),
		createErr:     errors.New("workspace exists"),
	}
	notifier := &plainNotifier{}
	h := newHarnessWithWorkspaceAndNotifier(t, &fakeChat{}, workspace, notifier, sidebar.Config{})
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/new", nil })

	pickFolderFromDialog(t, h)
	tester := h.tester()
	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %+v, want one error toast", notifier.notifications)
	}
	if got := notifier.notifications[0]; got.Level != contracts.NotifyError ||
		!strings.Contains(got.Text, "workspace exists") {
		t.Fatalf("error toast = %+v", got)
	}
	// With a notifier the error is a toast, not the inline row.
	h.assertNoTexts(tester, "添加工作区失败")

	// A later success is a success toast as well. The failed pick kept the
	// dialog up, so the picker is clicked again directly.
	workspace.createErr = nil
	notifier.notifications = nil
	h.click(tester, "选择其他文件夹…")
	if len(notifier.notifications) != 1 ||
		notifier.notifications[0].Level != contracts.NotifySuccess ||
		notifier.notifications[0].Text != "已添加工作区" {
		t.Fatalf("success toast = %+v", notifier.notifications)
	}
}

func TestSwitchDialogPickerHiddenWithoutBackend(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "切换工作文件夹", "选择其他文件夹…", "工作文件夹")
}

// TestWorkspacePickerTitleFollowsLocale pins that the native directory picker's
// dialog title is localized at click time, without a dialog carrying a stale
// language.
func TestWorkspacePickerTitleFollowsLocale(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newLocaleHarness(t, &fakeChat{}, sidebar.Config{}, "en", workspace, nil)
	captured := ""
	overrideSidebarPicker(t, func(title string) (string, error) {
		captured = title
		return "", nil // cancel: the pick is a no-op and can run again
	})
	tester := h.tester()
	tester.Frame()
	clickLabel(t, tester, "Switch workspace folder")
	h.click(tester, "Choose another folder…")
	if captured != "Choose a workspace folder" {
		t.Fatalf("English directory dialog title = %q, want %q", captured, "Choose a workspace folder")
	}

	// A language switch localizes the next picker invocation too.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.click(tester, "选择其他文件夹…")
	if captured != "选择工作文件夹" {
		t.Fatalf("Chinese directory dialog title = %q, want %q", captured, "选择工作文件夹")
	}
}

// TestSwitchDialogPickerAddsToAnEmptyRegistry covers a fresh install: the
// dialog's empty state and the picker are its two offers, and the picked folder
// becomes the open one.
func TestSwitchDialogPickerAddsToAnEmptyRegistry(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/first", nil })

	tester := h.tester()
	if err := tester.Click("切换工作文件夹"); err != nil {
		t.Fatalf("open the switch dialog: %v", err)
	}
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "还没有工作文件夹", "选择其他文件夹…")
	h.click(tester, "选择其他文件夹…")
	h.assertTexts(tester, "first")
}
