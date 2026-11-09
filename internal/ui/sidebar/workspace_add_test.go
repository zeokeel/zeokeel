package sidebar_test

// Tests for the sidebar's direct "Add workspace" flow: one click opens the
// native directory picker, the backend derives the workspace name from the
// chosen folder (there is no naming dialog), an already registered folder is
// reported as a workspace switch, and a cancelled picker or a failure is
// handled without hiding the workspace section.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// recordingWorkspace extends fakeWorkspace with Create/Open recording and
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

func TestSidebarAddWorkspacePicksFolderDirectly(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	var title string
	overrideSidebarPicker(t, func(t string) (string, error) {
		title = t
		return "/work/myproj", nil
	})
	tester := h.tester()
	h.assertTexts(tester, "添加工作区")

	h.click(tester, "添加工作区")

	// One click ran the native folder chooser: no naming dialog in between.
	if title != "选择工作区目录" {
		t.Fatalf("directory picker title = %q, want 选择工作区目录", title)
	}
	h.assertNoTexts(tester, "名称", "目录", "选择目录…", "未命名工作区")
	if workspace.createCalls != 1 || workspace.createName != "" || workspace.createRoot != "/work/myproj" {
		t.Fatalf("Create = %d calls name %q root %q, want the empty name and the chosen folder",
			workspace.createCalls, workspace.createName, workspace.createRoot)
	}
	// The backend derived the name from the folder and the group shows up
	// unfolded (its new-chat action is on the row).
	h.assertTexts(tester, "已添加工作区", "myproj", "0")
	if err := tester.Click(plusLabel("myproj")); err != nil {
		t.Fatalf("new workspace row's new-chat button missing or hidden: %v; texts = %v", err, tester.Texts())
	}
}

func TestSidebarAddWorkspaceCancelIsNoOp(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "", nil })
	tester := h.tester()

	h.click(tester, "添加工作区")

	if workspace.createCalls != 0 {
		t.Fatalf("cancelled picker called Create %d times", workspace.createCalls)
	}
	h.assertNoTexts(tester, "已添加工作区", "已切换工作区", "无法打开目录选择器", "添加工作区失败")
	h.assertTexts(tester, "工作区", "添加工作区")
}

func TestSidebarAddWorkspacePickerErrorIsVisible(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "", errors.New("picker failed") })
	tester := h.tester()

	h.click(tester, "添加工作区")

	h.assertTexts(tester, "无法打开目录选择器：picker failed")
	if workspace.createCalls != 0 {
		t.Fatalf("picker failure called Create %d times", workspace.createCalls)
	}
	// The persistent inline error row does not disappear on later frames.
	tester.Frame()
	h.assertTexts(tester, "无法打开目录选择器：picker failed")
}

func TestSidebarAddWorkspaceCreateErrorIsVisible(t *testing.T) {
	workspace := &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(),
		createErr:     errors.New("workspace exists"),
	}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/dup", nil })
	tester := h.tester()

	h.click(tester, "添加工作区")

	h.assertTexts(tester, "添加工作区失败：workspace exists")
	h.assertNoTexts(tester, "已添加工作区")
}

func TestSidebarAddWorkspaceReusesRegisteredFolder(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace(treeWorkspaces()...)}
	chat := &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "A 会话", WorkspaceID: "w1"},
	}}
	h := newHarnessWithWorkspace(t, chat, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/one", nil })
	tester := h.tester()
	// Fold the existing group first: the add flow must unfold the workspace it
	// switches to.
	h.click(tester, "项目一")
	h.assertNoTexts(tester, "A 会话")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一"); !ok || item.Expanded {
		t.Fatalf("the workspace group did not fold: %+v", item)
	}

	h.click(tester, "添加工作区")

	if len(workspace.openCalls) != 0 {
		t.Fatalf("the direct add flow opened a workspace separately: %v", workspace.openCalls)
	}
	if workspace.createName != "" || workspace.createRoot != "/work/one" {
		t.Fatalf("Create = name %q root %q", workspace.createName, workspace.createRoot)
	}
	// Already registered is a switch, not an error, and the group is unfolded
	// so the user can immediately start a chat in it.
	h.assertTexts(tester, "已切换工作区", "A 会话")
	h.assertNoTexts(tester, "已添加工作区", "添加工作区失败")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "项目一"); !ok || !item.Expanded {
		t.Fatalf("the switched workspace group is not unfolded: %+v", item)
	}
}

func TestSidebarAddWorkspaceNotifierReportsToasts(t *testing.T) {
	workspace := &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(),
		createErr:     errors.New("workspace exists"),
	}
	notifier := &plainNotifier{}
	h := newHarnessWithWorkspaceAndNotifier(t, &fakeChat{}, workspace, notifier, sidebar.Config{})
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/new", nil })
	tester := h.tester()

	h.click(tester, "添加工作区")

	if len(notifier.notifications) != 1 {
		t.Fatalf("notifications = %+v, want one error toast", notifier.notifications)
	}
	if got := notifier.notifications[0]; got.Level != contracts.NotifyError ||
		!strings.Contains(got.Text, "workspace exists") {
		t.Fatalf("error toast = %+v", got)
	}
	// With a notifier the error is a toast, not the header's inline row.
	h.assertNoTexts(tester, "添加工作区失败")

	// A later success is a success toast as well.
	workspace.createErr = nil
	notifier.notifications = nil
	h.click(tester, "添加工作区")
	if len(notifier.notifications) != 1 ||
		notifier.notifications[0].Level != contracts.NotifySuccess ||
		notifier.notifications[0].Text != "已添加工作区" {
		t.Fatalf("success toast = %+v", notifier.notifications)
	}
}

func TestSidebarAddWorkspaceHiddenWithoutBackend(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "添加工作区")
}

// TestAddWorkspaceDirectoryDialogTitleFollowsLocale pins that the native
// directory picker's dialog title is localized at click time, without a dialog
// carrying a stale language.
func TestAddWorkspaceDirectoryDialogTitleFollowsLocale(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newLocaleHarness(t, &fakeChat{}, sidebar.Config{}, "en", workspace, nil)
	captured := ""
	overrideSidebarPicker(t, func(title string) (string, error) {
		captured = title
		return "", nil // cancel: the add is a no-op and can run again
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Add workspace")
	if captured != "Choose workspace folder" {
		t.Fatalf("English directory dialog title = %q, want %q", captured, "Choose workspace folder")
	}

	// A language switch localizes the next picker invocation too.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.click(tester, "添加工作区")
	if captured != "选择工作区目录" {
		t.Fatalf("Chinese directory dialog title = %q, want %q", captured, "选择工作区目录")
	}
}
