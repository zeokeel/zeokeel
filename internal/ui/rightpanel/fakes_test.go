package rightpanel_test

// Scripted backend services for the right sidebar frame tests: a workspace
// service (with an optional diff extension), a terminal runner and an artifact
// presenter/history lister. They mirror the real contract semantics, run no
// I/O, and are safe to call from the module's off-thread loads.

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fileEntry builds one workspace listing entry.
func fileEntry(name string, dir bool) contracts.FileEntry {
	if dir {
		return contracts.FileEntry{Name: name, Path: name, Dir: true}
	}
	return contracts.FileEntry{Name: name, Path: name}
}

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
	listCalls   int
}

func newFakeWorkspace() *fakeWorkspace {
	return &fakeWorkspace{
		dirs:     make(map[string][]contracts.FileEntry),
		files:    make(map[string][]byte),
		readErrs: make(map[string]error),
	}
}

func (w *fakeWorkspace) setCurrent() {
	w.mu.Lock()
	defer w.mu.Unlock()
	workspace := contracts.Workspace{ID: "ws", Name: "proj", Root: "/work/proj"}
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
	w.listCalls++
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

func (w *fakeWorkspace) listed() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.listCalls
}

// --- fake differ (workspace changes) ----------------------------------------

// differWorkspace is a fake workspace that also implements
// contracts.WorkspaceDiffer with scripted changes and an error.
type differWorkspace struct {
	*fakeWorkspace
	mu      sync.Mutex
	changes []contracts.FileChange
	err     error
	calls   int
}

func newDifferWorkspace(changes ...contracts.FileChange) *differWorkspace {
	base := newFakeWorkspace()
	base.setCurrent()
	return &differWorkspace{fakeWorkspace: base, changes: changes}
}

func (d *differWorkspace) Changes(context.Context) ([]contracts.FileChange, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return append([]contracts.FileChange(nil), d.changes...), nil
}

func (d *differWorkspace) setChanges(changes ...contracts.FileChange) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.changes = changes
}

func (d *differWorkspace) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// --- fake TerminalService ---------------------------------------------------

// fakeTerminal is a scripted contracts.TerminalService. Run can block until a
// command is released (or the context is canceled) to exercise the async path.
type fakeTerminal struct {
	mu sync.Mutex

	dir      string
	shell    string
	shells   []string
	setShell []string
	setErr   error

	runCalls []string
	runOut   contracts.TerminalOutput
	runErr   error
	runBlock bool
	started  chan string
	release  chan struct{}
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{
		dir:     "/work/proj",
		shell:   "bash",
		shells:  []string{"bash", "zsh"},
		started: make(chan string, 8),
		release: make(chan struct{}, 8),
	}
}

func (f *fakeTerminal) WorkingDir(context.Context) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dir
}

func (f *fakeTerminal) Shell() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shell
}

func (f *fakeTerminal) Shells() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.shells...)
}

func (f *fakeTerminal) SetShell(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setShell = append(f.setShell, name)
	if f.setErr != nil {
		return f.setErr
	}
	f.shell = name
	return nil
}

func (f *fakeTerminal) Run(ctx context.Context, command string) (contracts.TerminalOutput, error) {
	f.mu.Lock()
	f.runCalls = append(f.runCalls, command)
	block := f.runBlock
	result, err := f.runOut, f.runErr
	f.mu.Unlock()

	if block {
		select {
		case f.started <- command:
		default:
		}
		select {
		case <-ctx.Done():
			return contracts.TerminalOutput{}, ctx.Err()
		case <-f.release:
		}
	}
	return result, err
}

func (f *fakeTerminal) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.runCalls...)
}

func (f *fakeTerminal) shellCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.setShell...)
}

func (f *fakeTerminal) setRunOutput(result contracts.TerminalOutput) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runOut = result
}

func (f *fakeTerminal) setRunErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runErr = err
}

func (f *fakeTerminal) setRunBlock(block bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runBlock = block
}

// --- fake artifact presenter/lister -----------------------------------------

// fakeArtifactService is both the presenter and the history lister the host
// provides on contracts.KeyArtifactPresenter.
type fakeArtifactService struct {
	history []contracts.Artifact
}

func (f *fakeArtifactService) SetArtifactHandler(func(contracts.Artifact)) {}

func (f *fakeArtifactService) Present(context.Context, string, string) (string, error) {
	return "artifact-test", nil
}

func (f *fakeArtifactService) Artifacts() []contracts.Artifact {
	return append([]contracts.Artifact(nil), f.history...)
}

// newArtifactsHarness builds the right sidebar frame with the optional artifact
// presenter/lister.
func newArtifactsHarness(t *testing.T, artifacts contracts.ArtifactPresenter) *harness {
	t.Helper()
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	return newHarness(t, harnessOptions{Artifacts: artifacts, Workspace: workspace})
}

func artifactAt(id, title, body string, at time.Time) contracts.Artifact {
	return contracts.Artifact{ID: id, Title: title, Body: body, CreatedAt: at}
}
