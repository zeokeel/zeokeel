package regression

// Shared workspace test double and sidebar frame assertions. The single-folder
// sidebar no longer manages folders (the rail, the switch dialog, the folder
// picker and the color select are gone), but other audits still need a
// contracts.WorkspaceService double for their own modules.

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// auditWorkspace is the recording workspace backend shared by the regression
// tests (other audits preload it, so its zero value stays usable). It mirrors
// the real registry: Create derives an empty name from the folder and reuses an
// already registered (canonical) root, making it current either way.
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
