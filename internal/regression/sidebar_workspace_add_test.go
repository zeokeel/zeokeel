package regression

// Adversarial audit: the sidebar's "添加工作区" dialog. Negative/boundary cases:
// the directory basename is the fallback name, Create receives the trimmed
// name/root and the new workspace is opened, a failing Create keeps the dialog
// open with a readable error, an empty directory is rejected, a picker
// cancel/error behaves, and a product without a workspace backend hides the
// action entirely.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// auditWorkspace is the recording workspace backend for the sidebar tests.
type auditWorkspace struct {
	mu         sync.Mutex
	created    []contracts.Workspace
	createErr  error
	createName string
	createRoot string
	openCalls  []string
	listErr    error
}

func (w *auditWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.listErr != nil {
		return nil, w.listErr
	}
	return append([]contracts.Workspace(nil), w.created...), nil
}

func (w *auditWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.created) == 0 {
		return contracts.Workspace{}, false, nil
	}
	return w.created[len(w.created)-1], true, nil
}

func (w *auditWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.createErr != nil {
		return contracts.Workspace{}, w.createErr
	}
	w.createName = name
	w.createRoot = root
	created := contracts.Workspace{ID: "w-" + itoa(int64(len(w.created)+1)), Name: name, Root: root}
	w.created = append(w.created, created)
	return created, nil
}

func (w *auditWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.openCalls = append(w.openCalls, id)
	return contracts.Workspace{ID: id}, nil
}

func (w *auditWorkspace) Delete(context.Context, string) error { return nil }

func (w *auditWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *auditWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (w *auditWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// createRecord returns the recorded Create arguments.
func (w *auditWorkspace) createRecord() (calls int, name, root string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.created), w.createName, w.createRoot
}

// overrideSidebarPicker replaces the workspace directory-picker seam.
func overrideSidebarPicker(t *testing.T, fn func() (string, error)) {
	t.Helper()
	previous := sidebar.DirectoryPicker
	sidebar.DirectoryPicker = fn
	t.Cleanup(func() { sidebar.DirectoryPicker = previous })
}

// startSidebarUI activates the sidebar (optionally with a workspace backend).
func startSidebarUI(t *testing.T, workspace contracts.WorkspaceService) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	if workspace != nil {
		provide(t, h.ctx, contracts.KeyWorkspace, workspace)
	}
	activate(t, h.ctx, sidebar.Module(), sidebar.Config{})
	h.registerScreen("home", "首页", "home-body")
	h.push("home")
	tester := h.tester(1280, 800)
	tester.Frame()
	return h, tester
}

// requireHasTexts requires every wanted label in the current frame.
func requireHasTexts(t *testing.T, tester *ui.Tester, wants ...string) {
	t.Helper()
	for _, want := range wants {
		requireHasText(t, tester, want)
	}
}

// clickSidebar clicks one sidebar label and repaints.
func clickSidebar(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// typeSidebar types into a dialog input after focusing it.
func typeSidebar(t *testing.T, tester *ui.Tester, input, text string) {
	t.Helper()
	clickSidebar(t, tester, input)
	tester.Type(text)
	tester.Frame()
}

// TestSidebarAddWorkspaceBasenameAndCreate covers the happy path: the picker
// supplies the directory, an empty name falls back to the basename, Create gets
// the trimmed pair and the created workspace is opened; a typed name wins.
func TestSidebarAddWorkspaceBasenameAndCreate(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)
	requireHasText(t, tester, "添加工作区")

	overrideSidebarPicker(t, func() (string, error) { return "/tmp/myproj/", nil })
	clickSidebar(t, tester, "添加工作区")
	requireHasTexts(t, tester, "选择目录…", "名称", "目录")
	clickSidebar(t, tester, "选择目录…")
	clickSidebar(t, tester, "添加")

	calls, name, root := workspace.createRecord()
	if calls != 1 || name != "myproj" || root != "/tmp/myproj/" {
		t.Fatalf("Create = calls %d name %q root %q, want myproj//tmp/myproj/", calls, name, root)
	}
	if len(workspace.openCalls) != 1 || workspace.openCalls[0] != "w-1" {
		t.Fatalf("Open calls = %v, want the created workspace", workspace.openCalls)
	}
	requireNoText(t, tester, "选择目录…") // the dialog closed
	requireHasText(t, tester, "已添加工作区")

	// A typed name overrides the directory basename. A fresh UI keeps the
	// dialog lifecycle independent of the first create (the sidebar's add
	// dialog is a single overlay instance).
	workspace2 := &auditWorkspace{}
	_, tester2 := startSidebarUI(t, workspace2)
	overrideSidebarPicker(t, func() (string, error) { return "/tmp/other", nil })
	clickSidebar(t, tester2, "添加工作区")
	typeSidebar(t, tester2, "名称 输入", "自定义工作区")
	clickSidebar(t, tester2, "选择目录…")
	clickSidebar(t, tester2, "添加")
	calls, name, root = workspace2.createRecord()
	if calls != 1 || name != "自定义工作区" || root != "/tmp/other" {
		t.Fatalf("custom Create = calls %d name %q root %q", calls, name, root)
	}
}

// TestSidebarAddWorkspaceFailuresKeepModal covers validation and backend
// failures: an empty directory is rejected locally, a failing Create and a
// failing picker keep the dialog open with readable errors.
func TestSidebarAddWorkspaceFailuresKeepModal(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)

	// Empty directory: rejected locally, no Create.
	clickSidebar(t, tester, "添加工作区")
	clickSidebar(t, tester, "添加")
	requireHasText(t, tester, "目录不能为空")
	if calls, _, _ := workspace.createRecord(); calls != 0 {
		t.Fatalf("Create calls after empty directory = %d, want 0", calls)
	}

	// Picker failure keeps the dialog open with the error.
	overrideSidebarPicker(t, func() (string, error) { return "", errors.New("picker failed") })
	clickSidebar(t, tester, "选择目录…")
	requireHasTexts(t, tester, "无法打开目录选择器：picker failed", "选择目录…")

	// Picker cancel leaves the directory empty; Create is not called.
	overrideSidebarPicker(t, func() (string, error) { return "", nil })
	clickSidebar(t, tester, "选择目录…")
	clickSidebar(t, tester, "添加")
	requireHasText(t, tester, "目录不能为空")
	if calls, _, _ := workspace.createRecord(); calls != 0 {
		t.Fatalf("Create calls after a cancelled picker = %d, want 0", calls)
	}

	// Backend failure keeps the dialog open.
	workspace.mu.Lock()
	workspace.createErr = errors.New("workspace exists")
	workspace.mu.Unlock()
	overrideSidebarPicker(t, func() (string, error) { return "/tmp/dup", nil })
	clickSidebar(t, tester, "选择目录…")
	clickSidebar(t, tester, "添加")
	requireHasTexts(t, tester, "workspace exists", "选择目录…", "添加")
}

// TestSidebarAddWorkspaceHiddenWithoutBackend covers the optional backend: no
// workspace service means no add action (the sidebar falls back to the flat
// session list), and the dialog API is not reachable.
func TestSidebarAddWorkspaceHiddenWithoutBackend(t *testing.T) {
	_, tester := startSidebarUI(t, nil)
	requireNoText(t, tester, "添加工作区")
	requireNoText(t, tester, "选择目录…")
}

// TestSidebarAddWorkspaceReopensAfterCreate guards the dialog lifecycle: after
// a successful create (which closes the overlay), the add button must open the
// dialog again (the header used to squeeze the button to zero width once the
// status badge appeared).
func TestSidebarAddWorkspaceReopensAfterCreate(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)
	overrideSidebarPicker(t, func() (string, error) { return "/tmp/first", nil })
	clickSidebar(t, tester, "添加工作区")
	clickSidebar(t, tester, "选择目录…")
	clickSidebar(t, tester, "添加")
	requireHasText(t, tester, "已添加工作区")

	clickSidebar(t, tester, "添加工作区")
	requireHasText(t, tester, "选择目录…")
}

// TestSidebarAddWorkspaceReopensAfterCancel isolates whether the reopen issue
// is specific to the successful-submit path or to modal dismissal in general.
func TestSidebarAddWorkspaceReopensAfterCancel(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)
	clickSidebar(t, tester, "添加工作区")
	requireHasText(t, tester, "选择目录…")
	clickSidebar(t, tester, "取消")
	requireNoText(t, tester, "选择目录…")
	clickSidebar(t, tester, "添加工作区")
	requireHasText(t, tester, "选择目录…")
}
