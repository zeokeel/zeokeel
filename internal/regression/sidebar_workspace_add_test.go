package regression

// Adversarial audit: the sidebar's direct "添加工作区" flow. Negative/boundary
// cases: the native folder picker runs on the add click (no naming dialog and
// no dialog state to get stuck), Create receives an empty name and the exact
// picked root (the backend derives the name), a cancelled picker is a no-op, a
// picker/Create failure is visible, an already registered folder is reported as
// a quiet switch instead of an error, and a product without a workspace backend
// hides the action entirely.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// auditWorkspace is the recording workspace backend shared by the sidebar
// audit tests (other audits preload it, so its zero value stays usable). It
// mirrors the real registry: Create derives an empty name from the folder and
// reuses an already registered (canonical) root, making it current either way.
type auditWorkspace struct {
	mu          sync.Mutex
	created     []contracts.Workspace
	currentID   string
	createErr   error
	createCalls int
	createName  string
	createRoot  string
	openCalls   []string
	listErr     error
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
	if w.currentID != "" {
		for _, workspace := range w.created {
			if workspace.ID == w.currentID {
				return workspace, true, nil
			}
		}
	}
	// A preloaded double with no explicit current: the last registered
	// workspace is current, as the earlier audits relied on.
	if len(w.created) > 0 {
		return w.created[len(w.created)-1], true, nil
	}
	return contracts.Workspace{}, false, nil
}

func (w *auditWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.createCalls++
	w.createName = name
	w.createRoot = root
	if w.createErr != nil {
		return contracts.Workspace{}, w.createErr
	}
	for _, workspace := range w.created {
		if filepath.Clean(strings.TrimSpace(workspace.Root)) == filepath.Clean(strings.TrimSpace(root)) {
			w.currentID = workspace.ID
			return workspace, nil
		}
	}
	derived := strings.TrimSpace(name)
	if derived == "" {
		derived = filepath.Base(filepath.Clean(root))
	}
	created := contracts.Workspace{ID: "w-" + itoa(int64(len(w.created)+1)), Name: derived, Root: root}
	w.created = append(w.created, created)
	w.currentID = created.ID
	return created, nil
}

func (w *auditWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.openCalls = append(w.openCalls, id)
	for _, workspace := range w.created {
		if workspace.ID == id {
			w.currentID = workspace.ID
			return workspace, nil
		}
	}
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
	return w.createCalls, w.createName, w.createRoot
}

// overrideSidebarPicker replaces the workspace directory-picker seam.
func overrideSidebarPicker(t *testing.T, fn func(title string) (string, error)) {
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

// TestSidebarAddWorkspaceDirectPicker covers the happy path: the add button
// runs the picker itself, Create gets the empty name and the picked folder, the
// backend-derived workspace appears current and unfolded, and a quiet status is
// shown. None of the retired naming-dialog controls may appear.
func TestSidebarAddWorkspaceDirectPicker(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)
	requireHasText(t, tester, "添加工作区")

	var titles []string
	overrideSidebarPicker(t, func(title string) (string, error) {
		titles = append(titles, title)
		return "/tmp/myproj", nil
	})
	clickSidebar(t, tester, "添加工作区")

	if len(titles) != 1 || titles[0] != "选择工作区目录" {
		t.Fatalf("directory picker calls = %v, want one with the Chinese title", titles)
	}
	for _, gone := range []string{"选择目录…", "名称", "目录 输入"} {
		requireNoText(t, tester, gone)
	}
	calls, name, root := workspace.createRecord()
	if calls != 1 || name != "" || root != "/tmp/myproj" {
		t.Fatalf("Create = calls %d name %q root %q, want the empty name and the exact picked folder",
			calls, name, root)
	}
	requireHasTexts(t, tester, "已添加工作区", "myproj")
	requireNoText(t, tester, "添加工作区失败")
	// The new workspace is current and unfolded: its tree item is expanded and
	// its row carries the per-workspace new-chat action.
	item, ok := tester.AccessByRole(ui.RoleTreeItem, "myproj")
	if !ok || !item.Expanded {
		t.Fatalf("new workspace tree item missing or folded: %+v", item)
	}
	requireHasText(t, tester, "在“myproj”中新建会话")
}

// TestSidebarAddWorkspaceNotifier covers the host-toast path: with a notifier
// the add reports success/error as toasts and the header keeps no badge row.
func TestSidebarAddWorkspaceNotifier(t *testing.T) {
	workspace := &auditWorkspace{createErr: errors.New("workspace exists")}
	h := newBareHarness(t)
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	provide(t, h.ctx, contracts.KeyWorkspace, contracts.WorkspaceService(workspace))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	activate(t, h.ctx, sidebar.Module(), sidebar.Config{})
	h.registerScreen("home", "首页", "home-body")
	h.push("home")
	tester := h.tester(1280, 800)
	tester.Frame()

	overrideSidebarPicker(t, func(string) (string, error) { return "/tmp/dup", nil })
	clickSidebar(t, tester, "添加工作区")
	notifier.mu.Lock()
	delivered := append([]contracts.Notification(nil), notifier.delivered...)
	notifier.mu.Unlock()
	if len(delivered) != 1 || delivered[0].Level != contracts.NotifyError ||
		!strings.Contains(delivered[0].Text, "workspace exists") {
		t.Fatalf("error toast = %+v", delivered)
	}
	requireNoText(t, tester, "添加工作区失败")

	workspace.mu.Lock()
	workspace.createErr = nil
	workspace.mu.Unlock()
	clickSidebar(t, tester, "添加工作区")
	notifier.mu.Lock()
	delivered = append([]contracts.Notification(nil), notifier.delivered...)
	notifier.mu.Unlock()
	if len(delivered) != 2 || delivered[1].Level != contracts.NotifySuccess ||
		delivered[1].Text != "已添加工作区" {
		t.Fatalf("success toast = %+v", delivered)
	}
	requireNoText(t, tester, "已添加工作区")
}

// TestSidebarAddWorkspaceCancelIsNoOp covers the cancelled native dialog: no
// Create, no status, no error.
func TestSidebarAddWorkspaceCancelIsNoOp(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "", nil })
	clickSidebar(t, tester, "添加工作区")

	if calls, _, _ := workspace.createRecord(); calls != 0 {
		t.Fatalf("cancelled picker called Create %d times", calls)
	}
	requireNoText(t, tester, "已添加工作区")
	requireNoText(t, tester, "无法打开目录选择器")
}

// TestSidebarAddWorkspaceErrorsVisible covers picker and backend failures: both
// surface in the header's inline error row without a dialog.
func TestSidebarAddWorkspaceErrorsVisible(t *testing.T) {
	workspace := &auditWorkspace{}
	_, tester := startSidebarUI(t, workspace)

	overrideSidebarPicker(t, func(string) (string, error) { return "", errors.New("picker failed") })
	clickSidebar(t, tester, "添加工作区")
	requireHasText(t, tester, "无法打开目录选择器：picker failed")
	if calls, _, _ := workspace.createRecord(); calls != 0 {
		t.Fatalf("picker failure called Create %d times", calls)
	}

	overrideSidebarPicker(t, func(string) (string, error) { return "/tmp/dup", nil })
	workspace.mu.Lock()
	workspace.createErr = errors.New("workspace exists")
	workspace.mu.Unlock()
	clickSidebar(t, tester, "添加工作区")
	requireHasText(t, tester, "添加工作区失败：workspace exists")
	requireNoText(t, tester, "选择目录…")
}

// TestSidebarAddWorkspaceReusesRegisteredFolder covers the boundary where the
// picked folder is already registered: it is a quiet switch ("已切换工作区"),
// not a duplicate error, and the folded workspace group is unfolded.
func TestSidebarAddWorkspaceReusesRegisteredFolder(t *testing.T) {
	workspace := &auditWorkspace{created: []contracts.Workspace{
		{ID: "w-1", Name: "myproj", Root: "/tmp/myproj"},
	}, currentID: "w-1"}
	_, tester := startSidebarUI(t, workspace)
	// Fold the group first: the switch must unfold it.
	clickSidebar(t, tester, "myproj")
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "myproj"); !ok || item.Expanded {
		t.Fatalf("workspace group did not fold: %+v", item)
	}

	overrideSidebarPicker(t, func(string) (string, error) { return "/tmp/myproj/", nil })
	clickSidebar(t, tester, "添加工作区")

	calls, name, root := workspace.createRecord()
	if calls != 1 || name != "" || root != "/tmp/myproj/" {
		t.Fatalf("Create = calls %d name %q root %q", calls, name, root)
	}
	requireHasText(t, tester, "已切换工作区")
	requireNoText(t, tester, "已添加工作区")
	requireNoText(t, tester, "添加工作区失败")
	// The reused workspace is current and unfolded.
	if item, ok := tester.AccessByRole(ui.RoleTreeItem, "myproj"); !ok || !item.Expanded {
		t.Fatalf("reused workspace group was not unfolded: %+v", item)
	}
}

// TestSidebarAddWorkspaceHiddenWithoutBackend covers the optional backend: no
// workspace service means no add action (the sidebar falls back to the flat
// session list), and the picker is never reached.
func TestSidebarAddWorkspaceHiddenWithoutBackend(t *testing.T) {
	_, tester := startSidebarUI(t, nil)
	requireNoText(t, tester, "添加工作区")
	requireNoText(t, tester, "选择目录…")
}
