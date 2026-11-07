package sidebar_test

// Tests for the sidebar's "Add workspace" action and its native dialog.

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// recordingWorkspace extends fakeWorkspace with Create/Open recording.
type recordingWorkspace struct {
	*fakeWorkspace
	createName  string
	createRoot  string
	createErr   error
	createCalls int
	created     contracts.Workspace
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
	created := f.created
	if created.ID == "" {
		created = contracts.Workspace{ID: "w-new", Name: name, Root: root}
	}
	return created, nil
}

func (f *recordingWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, id)
	if f.openErr != nil {
		return contracts.Workspace{}, f.openErr
	}
	return f.created, nil
}

func TestSidebarAddWorkspaceButtonAndDialog(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()
	h.assertTexts(tester, "添加工作区")
	h.assertNoTexts(tester, "选择目录…")

	h.click(tester, "添加工作区")
	h.assertTexts(tester, "添加工作区", "名称", "目录", "选择目录…", "取消", "添加")
	h.assertNoTexts(tester, "选择目录…\x00")
}

func TestSidebarAddWorkspaceButtonHiddenWithoutWorkspace(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	h.assertNoTexts(tester, "添加工作区")
}

func TestSidebarAddWorkspaceEmptyDirectoryValidation(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	tester := h.tester()
	h.click(tester, "添加工作区")
	h.click(tester, "添加")
	h.assertTexts(tester, "目录不能为空")
	if workspace.createCalls != 0 {
		t.Fatalf("Create calls = %d, want 0", workspace.createCalls)
	}
}

func TestSidebarAddWorkspaceChoosesDirectoryAndDefaultsName(t *testing.T) {
	workspace := &recordingWorkspace{
		fakeWorkspace: newFakeWorkspace(),
		created:       contracts.Workspace{ID: "w-42", Name: "myproj", Root: "/tmp/myproj"},
	}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	oldPicker := sidebar.DirectoryPicker
	sidebar.DirectoryPicker = func() (string, error) { return "/tmp/myproj", nil }
	t.Cleanup(func() { sidebar.DirectoryPicker = oldPicker })
	tester := h.tester()
	h.click(tester, "添加工作区")
	h.click(tester, "选择目录…")
	h.click(tester, "添加")

	if workspace.createCalls != 1 || workspace.createName != "myproj" || workspace.createRoot != "/tmp/myproj" {
		t.Fatalf("Create = %d %q %q", workspace.createCalls, workspace.createName, workspace.createRoot)
	}
	if len(workspace.openCalls) != 1 || workspace.openCalls[0] != "w-42" {
		t.Fatalf("Open calls = %v, want the created workspace", workspace.openCalls)
	}
	h.assertNoTexts(tester, "选择目录…")
	h.assertTexts(tester, "已添加工作区")
}

func TestSidebarAddWorkspaceCustomNameAndFailure(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	oldPicker := sidebar.DirectoryPicker
	sidebar.DirectoryPicker = func() (string, error) { return "/tmp/other", nil }
	t.Cleanup(func() { sidebar.DirectoryPicker = oldPicker })
	tester := h.tester()
	h.click(tester, "添加工作区")
	h.click(tester, "名称 输入")
	tester.Type("自定义工作区")
	tester.Frame()
	h.click(tester, "选择目录…")
	h.click(tester, "添加")
	if workspace.createName != "自定义工作区" || workspace.createRoot != "/tmp/other" {
		t.Fatalf("custom Create = %q/%q", workspace.createName, workspace.createRoot)
	}

	// A failing Create keeps the modal open and shows the danger text.
	workspace.createErr = errors.New("workspace exists")
	h.click(tester, "添加工作区")
	h.click(tester, "选择目录…")
	h.click(tester, "添加")
	h.assertTexts(tester, "workspace exists", "选择目录…")
}

func TestSidebarAddWorkspacePickerErrorKeepsDialog(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	oldPicker := sidebar.DirectoryPicker
	sidebar.DirectoryPicker = func() (string, error) { return "", errors.New("picker failed") }
	t.Cleanup(func() { sidebar.DirectoryPicker = oldPicker })
	tester := h.tester()
	h.click(tester, "添加工作区")
	h.click(tester, "选择目录…")
	h.assertTexts(tester, "picker failed", "选择目录…")
}

func TestSidebarAddWorkspaceEnglish(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Add workspace")
	h.click(tester, "Add workspace")
	h.assertTexts(tester, "Add workspace", "Name", "Directory", "Choose folder…", "Cancel", "Add")
}

var _ = ui.KeyEscape

// TestAddWorkspaceDirectoryDialogTitleFollowsLocale pins that the native
// directory picker's dialog title is localized before the seam runs.
func TestAddWorkspaceDirectoryDialogTitleFollowsLocale(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newLocaleHarness(t, &fakeChat{}, sidebar.Config{}, "en", workspace, nil)
	captured := ""
	oldPicker := sidebar.DirectoryPicker
	sidebar.DirectoryPicker = func() (string, error) {
		captured = sidebar.WorkspaceDirectoryDialogTitle
		return "", nil // cancel keeps the modal open for the second invocation
	}
	t.Cleanup(func() { sidebar.DirectoryPicker = oldPicker })
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Add workspace")
	h.click(tester, "Choose folder…")
	if captured != "Choose workspace folder" {
		t.Fatalf("English directory dialog title = %q, want %q", captured, "Choose workspace folder")
	}

	// A language switch localizes the next dialog invocation too.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.click(tester, "选择目录…")
	if captured != "选择工作区目录" {
		t.Fatalf("Chinese directory dialog title = %q, want %q", captured, "选择工作区目录")
	}
}
