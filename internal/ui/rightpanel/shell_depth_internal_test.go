package rightpanel

// Internal tests for the shell-depth additions that are hard to reach through a
// frame: the Log pane filter/count helpers and the off-UI-thread reads of the
// Files listing/preview and the workspace changes.

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- Log pane filter and honest count --------------------------------------

func TestLogFilterMatchesTypeAndPayload(t *testing.T) {
	events := []rig.SessionEvent{
		{Seq: 1, Type: "run_start", Payload: []byte(`{"model":"snapshot-model"}`)},
		{Seq: 2, Type: "tool_call", Payload: []byte(`{"name":"working_directory"}`)},
		{Seq: 3, Type: "tool_result", Payload: []byte(`{"content":"ok"}`)},
		{Seq: 4, Type: "run_end", Payload: []byte(`{"ok":true}`)},
	}
	if got := filterLogEvents(events, ""); len(got) != len(events) {
		t.Fatalf("empty filter kept %d events, want all %d", len(got), len(events))
	}
	if got := filterLogEvents(events, "TOOL"); len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("type filter kept %+v, want the two tool events", got)
	}
	if got := filterLogEvents(events, "working_directory"); len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("payload filter kept %+v, want the working_directory event", got)
	}
	if got := filterLogEvents(events, "no-such-event"); len(got) != 0 {
		t.Fatalf("no-match filter kept %d events, want 0", len(got))
	}
}

func TestTerminalFollowPausesOnlyWhileScrolledAway(t *testing.T) {
	scrolled := ui.ScrollState{Y: 10, MaxY: 100}
	if !terminalFollowPaused("line\n", scrolled) {
		t.Fatal("a scrolled-away terminal was not reported as paused")
	}
	atEnd := ui.ScrollState{Y: math.MaxFloat32, MaxY: 100}
	if terminalFollowPaused("line\n", atEnd) {
		t.Fatal("a terminal at the end was reported as paused")
	}
	if terminalFollowPaused("", scrolled) {
		t.Fatal("an empty terminal was reported as paused")
	}
}

func TestLogCountLabelIsHonestAboutTheWindow(t *testing.T) {
	// Under the read limit the count is exact.
	if got := logCountLabel(nil, 6, 6, false); got != "6 条会话事件" {
		t.Fatalf("exact count = %q, want %q", got, "6 条会话事件")
	}
	// At the limit the store window is full: the copy must not claim these are
	// all the session's events.
	windowed := logCountLabel(nil, logLimit, logLimit, false)
	if !strings.Contains(windowed, "最近") {
		t.Fatalf("windowed count = %q, want the honest \"latest\" wording", windowed)
	}
	filtered := logCountLabel(nil, 30, 4, true)
	if !strings.Contains(filtered, "4") || !strings.Contains(filtered, "30") {
		t.Fatalf("filtered count = %q, want shown/total", filtered)
	}
}

// --- Off-UI-thread reads ----------------------------------------------------

// fakeRuntime is a contracts.UIRuntime whose Post queue the test drains itself:
// the async helpers must hand their result to the UI thread, never write state
// from the read goroutine.
type fakeRuntime struct {
	mu       sync.Mutex
	posted   []func()
	invalids int
}

func (f *fakeRuntime) Post(fn func()) {
	f.mu.Lock()
	f.posted = append(f.posted, fn)
	f.mu.Unlock()
}

func (f *fakeRuntime) Invalidate() {
	f.mu.Lock()
	f.invalids++
	f.mu.Unlock()
}

func (f *fakeRuntime) runPosted() int {
	f.mu.Lock()
	posted := f.posted
	f.posted = nil
	f.mu.Unlock()
	for _, fn := range posted {
		fn()
	}
	return len(posted)
}

func (f *fakeRuntime) invalidationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.invalids
}

func (f *fakeRuntime) Frame() uint64                     { return 0 }
func (f *fakeRuntime) Commands() []contracts.CommandSpec { return nil }
func (f *fakeRuntime) RegisterScreen(contracts.ScreenSpec) (func(), error) {
	return func() {}, nil
}
func (f *fakeRuntime) RegisterSlotItem(contracts.SlotItem) (func(), error) {
	return func() {}, nil
}
func (f *fakeRuntime) RegisterSettingsPage(contracts.SettingsPageSpec) (func(), error) {
	return func() {}, nil
}
func (f *fakeRuntime) RegisterToolRenderer(contracts.ToolRenderer) (func(), error) {
	return func() {}, nil
}
func (f *fakeRuntime) RegisterCommand(contracts.CommandSpec) (func(), error) {
	return func() {}, nil
}
func (f *fakeRuntime) RenderSlot(*ui.Context, string)                  {}
func (f *fakeRuntime) RenderTool(*ui.Context, contracts.ToolCall) bool { return false }
func (f *fakeRuntime) Slots() []string                                 { return nil }

// asyncWorkspace is a scripts.WorkspaceService whose Files/ReadFile block until
// the test releases them, proving the read runs off the UI thread.
type asyncWorkspace struct {
	mu       sync.Mutex
	calls    int
	started  chan struct{}
	release  chan struct{}
	files    []contracts.FileEntry
	contents string
	current  contracts.Workspace
}

func newAsyncWorkspace() *asyncWorkspace {
	return &asyncWorkspace{
		started:  make(chan struct{}, 8),
		release:  make(chan struct{}),
		files:    []contracts.FileEntry{{Name: "README.md", Path: "README.md"}, {Name: "main.go", Path: "main.go"}},
		contents: "# readme\n",
		current:  contracts.Workspace{ID: "w1", Name: "proj", Root: "/proj"},
	}
}

func (w *asyncWorkspace) wait(t *testing.T) {
	t.Helper()
	select {
	case <-w.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the read never started")
	}
}

func (w *asyncWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return []contracts.Workspace{w.current}, nil
}
func (w *asyncWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return w.current, true, nil
}
func (w *asyncWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (w *asyncWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return w.current, nil
}
func (w *asyncWorkspace) Delete(context.Context, string) error { return nil }
func (w *asyncWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	select {
	case w.started <- struct{}{}:
	default:
	}
	<-w.release
	return w.files, nil
}
func (w *asyncWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	<-w.release
	return []byte(w.contents), nil
}
func (w *asyncWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *asyncWorkspace) fileCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func TestFilesListingRunsOffUIThread(t *testing.T) {
	workspace := newAsyncWorkspace()
	runtime := &fakeRuntime{}
	st := &state{}

	entries, err, loading := st.filesEntriesFor(workspace, workspace.current, runtime)
	if !loading {
		t.Fatal("the first read did not report a loading state")
	}
	if len(entries) != 0 || err != nil {
		t.Fatalf("loading read returned entries=%v err=%v, want none", entries, err)
	}
	workspace.wait(t)
	close(workspace.release)
	deadline := time.Now().Add(2 * time.Second)
	for runtime.runPosted() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the posted result never arrived")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if st.filesLoading {
		t.Fatal("filesLoading stayed true after the result landed")
	}
	if !st.filesLoaded || len(st.filesEntries) != 2 {
		t.Fatalf("cache after the read = loaded=%v entries=%d, want true/2", st.filesLoaded, len(st.filesEntries))
	}
	// A second frame uses the cache: no new read.
	if _, _, loading := st.filesEntriesFor(workspace, workspace.current, runtime); loading {
		t.Fatal("a cached listing reported loading")
	}
	if got := workspace.fileCalls(); got != 1 {
		t.Fatalf("Files calls = %d, want 1", got)
	}
}

func TestFilePreviewRunsOffUIThread(t *testing.T) {
	workspace := newAsyncWorkspace()
	runtime := &fakeRuntime{}
	st := &state{runtime: runtime, filesPreview: make(map[string]*filePreview)}

	preview := st.filePreviewFor(workspace, "README.md")
	if !preview.loading {
		t.Fatal("the preview request did not report loading")
	}
	workspace.wait(t)
	close(workspace.release)
	deadline := time.Now().Add(2 * time.Second)
	for runtime.runPosted() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the posted preview never arrived")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if preview.loading {
		t.Fatal("preview.loading stayed true after the result landed")
	}
	if got := string(preview.data); got != "# readme\n" {
		t.Fatalf("preview data = %q, want the file contents", got)
	}
}

// blockingDiffer is a WorkspaceDiffer whose Changes blocks until released.
type blockingDiffer struct {
	*asyncWorkspace
	started chan struct{}
	release chan struct{}
	changes []contracts.FileChange
}

func newBlockingDiffer() *blockingDiffer {
	return &blockingDiffer{
		asyncWorkspace: newAsyncWorkspace(),
		started:        make(chan struct{}, 1),
		release:        make(chan struct{}),
		changes:        []contracts.FileChange{{Path: "main.go", Status: "M", Diff: sampleUnifiedDiff}},
	}
}

func (d *blockingDiffer) Changes(context.Context) ([]contracts.FileChange, error) {
	select {
	case d.started <- struct{}{}:
	default:
	}
	<-d.release
	return d.changes, nil
}

func TestChangesReadRunsOffUIThread(t *testing.T) {
	differ := newBlockingDiffer()
	runtime := &fakeRuntime{}
	st := &state{}

	loading := st.ensureChangesLoaded(differ.asyncWorkspace, differ, runtime)
	if !loading {
		t.Fatal("the changes read did not report a loading state")
	}
	select {
	case <-differ.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the changes read never started")
	}
	close(differ.release)
	deadline := time.Now().Add(2 * time.Second)
	for runtime.runPosted() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the posted changes never arrived")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if st.changesLoading {
		t.Fatal("changesLoading stayed true after the result landed")
	}
	if !st.changesLoaded || len(st.changes) != 1 {
		t.Fatalf("changes cache = loaded=%v len=%d, want true/1", st.changesLoaded, len(st.changes))
	}
	if st.ensureChangesLoaded(differ.asyncWorkspace, differ, runtime) {
		t.Fatal("cached changes reported loading")
	}
}
