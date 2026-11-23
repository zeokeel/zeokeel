package uiroot

// Frame tests for the desktop startup workspace gate: the page replaces the
// shell on every launch, lists the recent folders (max 3), preselects the open
// one, continues on Enter, opens a clicked recent folder and registers a folder
// chosen through the native picker. The gate is skipped without a workspace
// backend and through Config.SkipWorkspaceGate.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// gateWorkspace is a scripted contracts.WorkspaceService for the gate: a fixed
// registry, one current folder, and the calls the gate made.
type gateWorkspace struct {
	mu       sync.Mutex
	entries  []contracts.Workspace
	current  string
	hasCurr  bool
	listErr  error
	opened   []string
	openErr  error
	created  []string
	createAs contracts.Workspace
	createOK bool
}

func newGateWorkspace(entries ...contracts.Workspace) *gateWorkspace {
	return &gateWorkspace{entries: entries}
}

func (w *gateWorkspace) withCurrent(id string) *gateWorkspace {
	w.current, w.hasCurr = id, true
	return w
}

func (w *gateWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.listErr != nil {
		return nil, w.listErr
	}
	return append([]contracts.Workspace(nil), w.entries...), nil
}

func (w *gateWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.hasCurr {
		return contracts.Workspace{}, false, nil
	}
	for _, entry := range w.entries {
		if entry.ID == w.current {
			return entry, true, nil
		}
	}
	return contracts.Workspace{ID: w.current}, true, nil
}

func (w *gateWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.created = append(w.created, name+"|"+root)
	if w.createOK {
		w.entries = append(w.entries, w.createAs)
		w.current, w.hasCurr = w.createAs.ID, true
		return w.createAs, nil
	}
	created := contracts.Workspace{ID: "created-1", Name: "chosen", Root: root}
	w.entries = append(w.entries, created)
	w.current, w.hasCurr = created.ID, true
	return created, nil
}

func (w *gateWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = append(w.opened, id)
	if w.openErr != nil {
		return contracts.Workspace{}, w.openErr
	}
	w.current, w.hasCurr = id, true
	for _, entry := range w.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return contracts.Workspace{ID: id}, nil
}

func (w *gateWorkspace) Delete(context.Context, string) error { return nil }
func (w *gateWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (w *gateWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }
func (w *gateWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *gateWorkspace) openedIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.opened...)
}

func (w *gateWorkspace) createCalls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.created...)
}

// gateChat is a minimal contracts.ChatService whose session list feeds the
// gate's recency ordering.
type gateChat struct{ sessions []contracts.ChatSessionInfo }

func (c *gateChat) Config() contracts.ChatConfig          { return contracts.ChatConfig{} }
func (c *gateChat) Sessions() []contracts.ChatSessionInfo { return c.sessions }
func (c *gateChat) Session(string) (contracts.ChatSession, bool) {
	return contracts.ChatSession{}, false
}
func (c *gateChat) ActiveSession() string                  { return "" }
func (c *gateChat) SetActiveSession(string) error          { return nil }
func (c *gateChat) NewSession(string) (string, error)      { return "", nil }
func (c *gateChat) DeleteSession(string) error             { return nil }
func (c *gateChat) Send(string, string) error              { return nil }
func (c *gateChat) Cancel(string) error                    { return nil }
func (c *gateChat) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// gateRoot builds a UI root whose kernel resolves the given workspace (and
// optional chat), with one screen registered so the shell has a destination.
func gateRoot(t *testing.T, cfg Config, workspace contracts.WorkspaceService, chat contracts.ChatService) (*Root, *ui.Tester) {
	t.Helper()
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	if workspace != nil {
		if _, err := kernel.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
			t.Fatalf("provide workspace: %v", err)
		}
	}
	if chat != nil {
		if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(chat)); err != nil {
			t.Fatalf("provide chat: %v", err)
		}
	}
	cfg.Kernel = kernel
	cfg.EnableWorkspaceGate = true
	if cfg.AppName == "" {
		cfg.AppName = "ZeoKeelTest"
	}
	if cfg.Language == "" {
		cfg.Language = "zh"
	}
	root := New(cfg)
	mustScreen(t, root, textScreen("session", "会话", "shell-body", 20))
	tester := newTestTester(t, root, 1400, 900)
	tester.Frame()
	return root, tester
}

// gateWorkspaces is the approved frame's registry: three recent folders.
func gateWorkspaces() []contracts.Workspace {
	return []contracts.Workspace{
		{ID: "w1", Name: "agent-workspace", Root: "/home/me/Projects/agent-workspace"},
		{ID: "w2", Name: "zeokeel", Root: "/home/me/Projects/zeokeel"},
		{ID: "w3", Name: "notes", Root: "/home/me/Documents/notes"},
	}
}

func TestWorkspaceGateReplacesTheShellAndEnterContinues(t *testing.T) {
	workspace := newGateWorkspace(gateWorkspaces()...).withCurrent("w1")
	_, tester := gateRoot(t, Config{}, workspace, &gateChat{})

	// The page, not the shell: the folder decision comes first.
	for _, want := range []string{
		"ZeoKeelTest", "打开工作文件夹",
		"选择一个文件夹作为这次会话的工作区。会话、文件和记忆都只属于它。",
		"最近使用", "agent-workspace", "zeokeel", "notes",
		"继续使用 agent-workspace", "选择其他文件夹…",
		"按 Enter 继续；每次启动都会询问。",
	} {
		if !tester.HasText(want) {
			t.Fatalf("gate does not show %q; texts = %v", want, tester.Texts())
		}
	}
	if tester.HasText("shell-body") {
		t.Fatalf("the shell rendered behind the gate; texts = %v", tester.Texts())
	}
	// The open folder is preselected and marked in words, not only by color.
	if !tester.HasText("上次使用") {
		t.Fatalf("the current folder is not marked; texts = %v", tester.Texts())
	}

	// Enter continues the preselected folder and reveals the shell.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 1 || opened[0] != "w1" {
		t.Fatalf("Enter opened %v, want [w1]", opened)
	}
	if !tester.HasText("shell-body") {
		t.Fatalf("the shell did not render after continuing; texts = %v", tester.Texts())
	}
	if tester.HasText("打开工作文件夹") {
		t.Fatalf("the gate is still up after continuing; texts = %v", tester.Texts())
	}

	// The gate asked once: a later frame does not bring it back.
	tester.Frame()
	if tester.HasText("打开工作文件夹") {
		t.Fatalf("the gate returned on a later frame; texts = %v", tester.Texts())
	}
}

func TestWorkspaceGatePreselectsTheOpenFolderAndListsByRecency(t *testing.T) {
	base := time.Now()
	workspaces := append(gateWorkspaces(),
		contracts.Workspace{ID: "w4", Name: "old", Root: "/home/me/old", CreatedAt: base.Add(-100 * time.Hour)})
	chat := &gateChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", WorkspaceID: "w2", UpdatedAt: base.Add(-2 * time.Hour)},
		{ID: "s2", WorkspaceID: "w1", UpdatedAt: base.Add(-time.Hour)},
		{ID: "s3", WorkspaceID: "w3", UpdatedAt: base.Add(-30 * time.Hour)},
		{ID: "s4", WorkspaceID: "w4", Archived: true, UpdatedAt: base.Add(time.Hour)},
	}}
	// The open folder is the oldest used one: it is still preselected (the
	// decision is "this folder or another"), while the list stays by recency.
	workspace := newGateWorkspace(workspaces...).withCurrent("w1")
	_, tester := gateRoot(t, Config{}, workspace, chat)

	for _, want := range []string{"agent-workspace", "zeokeel", "notes", "上次使用"} {
		if !tester.HasText(want) {
			t.Fatalf("gate does not show %q; texts = %v", want, tester.Texts())
		}
	}
	// Four folders, three listed: the archived-session folder is not lifted and
	// the oldest registered one falls outside the three.
	if tester.HasText("old") {
		t.Fatalf("the gate lists more than three folders; texts = %v", tester.Texts())
	}
	texts := tester.Texts()
	first := indexOfText(texts, "agent-workspace")
	second := indexOfText(texts, "zeokeel")
	third := indexOfText(texts, "notes")
	if !(first >= 0 && first < second && second < third) {
		t.Fatalf("recent order = agent-workspace:%d zeokeel:%d notes:%d; texts = %v", first, second, third, texts)
	}
	if !tester.HasText("继续使用 agent-workspace") {
		t.Fatalf("the open folder is not the preselection; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 1 || opened[0] != "w1" {
		t.Fatalf("Enter opened %v, want the preselected open folder w1", opened)
	}
}

func TestWorkspaceGateClickRecentOpensThatFolder(t *testing.T) {
	workspace := newGateWorkspace(gateWorkspaces()...).withCurrent("w1")
	_, tester := gateRoot(t, Config{}, workspace, &gateChat{})

	if err := tester.Click("notes"); err != nil {
		t.Fatalf("click the recent row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if opened := workspace.openedIDs(); len(opened) != 1 || opened[0] != "w3" {
		t.Fatalf("clicking notes opened %v, want [w3]", opened)
	}
	if !tester.HasText("shell-body") {
		t.Fatalf("clicking a recent folder did not reveal the shell; texts = %v", tester.Texts())
	}
}

func TestWorkspaceGatePickerRegistersTheChosenFolder(t *testing.T) {
	previous := workspaceGateDirectoryPicker
	t.Cleanup(func() { workspaceGateDirectoryPicker = previous })
	workspaceGateDirectoryPicker = func(title string) (string, error) {
		if !strings.Contains(title, "工作文件夹") {
			t.Fatalf("picker title = %q, want the localized folder title", title)
		}
		return "/home/me/Projects/chosen", nil
	}
	workspace := newGateWorkspace()
	_, tester := gateRoot(t, Config{}, workspace, &gateChat{})

	// An empty registry lists no recent folders; the picker is the one action.
	if tester.HasText("最近使用") {
		t.Fatalf("the gate lists recents for an empty registry; texts = %v", tester.Texts())
	}
	if err := tester.Click("选择其他文件夹…"); err != nil {
		t.Fatalf("click the picker: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if calls := workspace.createCalls(); len(calls) != 1 || calls[0] != "|/home/me/Projects/chosen" {
		t.Fatalf("Create calls = %v, want one with the derived name and the chosen root", calls)
	}
	if !tester.HasText("shell-body") {
		t.Fatalf("choosing a folder did not reveal the shell; texts = %v", tester.Texts())
	}
}

func TestWorkspaceGateCancelledPickerStaysOnThePage(t *testing.T) {
	previous := workspaceGateDirectoryPicker
	t.Cleanup(func() { workspaceGateDirectoryPicker = previous })
	workspaceGateDirectoryPicker = func(string) (string, error) { return "", nil }
	workspace := newGateWorkspace(gateWorkspaces()...)
	_, tester := gateRoot(t, Config{}, workspace, &gateChat{})

	if err := tester.Click("选择其他文件夹…"); err != nil {
		t.Fatalf("click the picker: %v", err)
	}
	tester.Frame()
	if calls := workspace.createCalls(); len(calls) != 0 {
		t.Fatalf("a cancelled picker created %v", calls)
	}
	if !tester.HasText("打开工作文件夹") {
		t.Fatalf("a cancelled picker left the page; texts = %v", tester.Texts())
	}
}

func TestWorkspaceGateOpenFailureStaysInline(t *testing.T) {
	workspace := newGateWorkspace(gateWorkspaces()...).withCurrent("w1")
	workspace.openErr = context.DeadlineExceeded
	_, tester := gateRoot(t, Config{}, workspace, &gateChat{})

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if !tester.HasText("无法打开工作文件夹：") {
		t.Fatalf("a failed open is not reported on the page; texts = %v", tester.Texts())
	}
	if tester.HasText("shell-body") {
		t.Fatalf("a failed open revealed the shell; texts = %v", tester.Texts())
	}
}

func TestWorkspaceGateEnterWithoutRecentsOpensThePicker(t *testing.T) {
	previous := workspaceGateDirectoryPicker
	t.Cleanup(func() { workspaceGateDirectoryPicker = previous })
	called := 0
	workspaceGateDirectoryPicker = func(string) (string, error) {
		called++
		return "", nil
	}
	workspace := newGateWorkspace()
	_, tester := gateRoot(t, Config{}, workspace, &gateChat{})

	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if called != 1 {
		t.Fatalf("Enter on an empty gate opened the picker %d times, want 1", called)
	}
}

func TestWorkspaceGateSkippedWithoutBackendOrByConfig(t *testing.T) {
	// No workspace backend: the shell is the product (headless/ACP style hosts).
	_, tester := gateRoot(t, Config{}, nil, &gateChat{})
	if tester.HasText("打开工作文件夹") {
		t.Fatalf("a host without the workspace backend showed the gate; texts = %v", tester.Texts())
	}
	if !tester.HasText("shell-body") {
		t.Fatalf("the shell did not render without a workspace backend; texts = %v", tester.Texts())
	}

	// The embedder opt-out.
	workspace := newGateWorkspace(gateWorkspaces()...).withCurrent("w1")
	_, optedOut := gateRoot(t, Config{SkipWorkspaceGate: true}, workspace, &gateChat{})
	if optedOut.HasText("打开工作文件夹") {
		t.Fatalf("Config.SkipWorkspaceGate did not skip the gate; texts = %v", optedOut.Texts())
	}
	if !optedOut.HasText("shell-body") {
		t.Fatalf("the shell did not render with SkipWorkspaceGate; texts = %v", optedOut.Texts())
	}
}

func TestWorkspaceGateEnglishCopy(t *testing.T) {
	workspace := newGateWorkspace(gateWorkspaces()...).withCurrent("w1")
	_, tester := gateRoot(t, Config{Language: "en"}, workspace, &gateChat{})
	for _, want := range []string{
		"Open a workspace folder", "Recently used", "Continue with agent-workspace",
		"Choose another folder…", "Press Enter to continue; you'll be asked on every launch.",
	} {
		if !tester.HasText(want) {
			t.Fatalf("English gate does not show %q; texts = %v", want, tester.Texts())
		}
	}
}
