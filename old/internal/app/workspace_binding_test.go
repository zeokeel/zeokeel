package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// workspaceBindingFixture wires the production app pieces that bind a session
// to a workspace: a real store with two workspaces, a controller whose agent is
// rebuilt through app.rebuildAgent (the same hook the window wires), a local
// OpenAI-compatible provider so turns can run, and the session-activation hook
// that keeps the working directory per session.
type workspaceBindingFixture struct {
	t     *testing.T
	a     *app
	ctrl  *session.Controller
	st    *store.Store
	wsA   *store.Workspace
	wsB   *store.Workspace
	log   *acceptanceRequestLog
	rootA string
	rootB string
}

func newWorkspaceBindingFixture(t *testing.T) *workspaceBindingFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	configuredProvider(t, st)
	srv, log := newAcceptanceProvider(t, "workspace-reply", "workspace-title")
	if err := st.SetSetting("provider.base_url", srv.URL+"/v1"); err != nil {
		t.Fatalf("set provider base url: %v", err)
	}

	f := &workspaceBindingFixture{t: t, st: st, log: log}
	f.rootA = t.TempDir()
	f.rootB = t.TempDir()
	wsA, err := st.CreateWorkspace("Alpha", f.rootA)
	if err != nil {
		t.Fatalf("CreateWorkspace(A): %v", err)
	}
	wsB, err := st.CreateWorkspace("Beta", f.rootB)
	if err != nil {
		t.Fatalf("CreateWorkspace(B): %v", err)
	}
	f.wsA, f.wsB = wsA, wsB
	if err := st.SetSetting(session.SettingWorkspace, wsA.ID); err != nil {
		t.Fatalf("select workspace A: %v", err)
	}

	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	ctrl, err := session.New(session.Config{
		Store: st, Broker: broker, Projector: projector, WorkspaceID: wsA.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	a := &app{services: ctrl}
	ctrl.SetAgentRebuilder(a.rebuildAgent)
	ctrl.SetOnSessionActivated(a.sessionActivated)
	t.Cleanup(func() {
		if a.bundle != nil && a.bundle.Close != nil {
			_ = a.bundle.Close()
		}
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})
	if err := a.rebuildAgent("test"); err != nil {
		t.Fatalf("rebuildAgent: %v", err)
	}
	f.a, f.ctrl = a, ctrl
	return f
}

// invokeAgentTool invokes a model-visible tool directly (bypassing the model)
// so the test can observe the root/cwd the turn would have used.
func invokeAgentTool(t *testing.T, agent *runtime.Agent, name, args string) string {
	t.Helper()
	tl, ok := agent.ToolByName(name)
	if !ok {
		t.Fatalf("agent exposes no tool %q", name)
	}
	out, err := tl.Invoke(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s.Invoke(%s): %v", name, args, err)
	}
	return out
}

// workingDirectoryResult is the decoded working_directory tool result.
type testWorkingDirectoryResult struct {
	Cwd      string `json:"cwd"`
	Relative string `json:"relative"`
	Root     string `json:"root"`
}

func workingDirectoryOf(t *testing.T, agent *runtime.Agent, args string) testWorkingDirectoryResult {
	t.Helper()
	var res testWorkingDirectoryResult
	if err := json.Unmarshal([]byte(invokeAgentTool(t, agent, "working_directory", args)), &res); err != nil {
		t.Fatalf("decode working_directory result: %v", err)
	}
	return res
}

// lastSystemPrompt returns the system instruction of the most recent ordinary
// provider request (the app's composed per-turn prompt).
func (f *workspaceBindingFixture) lastSystemPrompt() string {
	requests := f.log.snapshot()
	for i := len(requests) - 1; i >= 0; i-- {
		for _, msg := range requests[i].Messages {
			if msg.Role == string(runtime.RoleSystem) {
				return msg.Content
			}
		}
	}
	f.t.Fatal("provider received no request with a system prompt")
	return ""
}

func cleanPath(p string) string { return filepath.Clean(strings.TrimSpace(p)) }

// TestSubmitBindsSessionWorkspace proves the turn of a session in workspace B
// runs with B's root even when A was the selected workspace: Submit selects the
// session's workspace, rebuilds the agent for B's root, and the prompt WorkDir,
// working_directory root and bash cwd all point at B.
func TestSubmitBindsSessionWorkspace(t *testing.T) {
	f := newWorkspaceBindingFixture(t)
	ctx := context.Background()

	// A session created for B selects B immediately (create path fix).
	idB, err := f.ctrl.CreateSessionInWorkspace(ctx, f.wsB.ID, "B session")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace(B): %v", err)
	}
	if got := f.ctrl.CurrentWorkspaceID(); got != f.wsB.ID {
		t.Fatalf("selected workspace after creating a B session = %q, want B", got)
	}
	if got := cleanPath(f.ctrl.WorkspaceRoot()); got != cleanPath(f.rootB) {
		t.Fatalf("WorkspaceRoot after creating a B session = %q, want %q", got, f.rootB)
	}
	if instr := f.a.bundle.Agent.Instruction(); !strings.Contains(instr, cleanPath(f.rootB)) {
		t.Fatalf("agent instruction does not name B's root %q: %.400s", f.rootB, instr)
	}
	if got := workingDirectoryOf(t, f.a.bundle.Agent, "{}"); cleanPath(got.Root) != cleanPath(f.rootB) {
		t.Fatalf("working_directory root = %q, want B root %q", got.Root, f.rootB)
	}

	// The user switches back to A; a queued/background submit to the B session
	// must re-bind B before the turn, not run on A's root.
	if err := f.ctrl.SelectWorkspace(ctx, f.wsA.ID); err != nil {
		t.Fatalf("SelectWorkspace(A): %v", err)
	}
	if err := f.ctrl.Submit(ctx, idB, "work in B"); err != nil {
		t.Fatalf("Submit(B session): %v", err)
	}
	waitWiringIdle(t, f.ctrl)
	if got := f.ctrl.CurrentWorkspaceID(); got != f.wsB.ID {
		t.Fatalf("selected workspace after submitting the B session = %q, want B", got)
	}
	if got := cleanPath(f.ctrl.WorkspaceRoot()); got != cleanPath(f.rootB) {
		t.Fatalf("WorkspaceRoot after submitting the B session = %q, want %q", got, f.rootB)
	}
	sys := f.lastSystemPrompt()
	if !strings.Contains(sys, `Current working directory: "`+cleanPath(f.rootB)+`"`) {
		t.Fatalf("prompt WorkDir is not B's root; system prompt: %.600s", sys)
	}
	if strings.Contains(sys, cleanPath(f.rootA)) {
		t.Fatalf("prompt WorkDir still mentions A's root %q: %.600s", f.rootA, sys)
	}
	// bash's cwd is the working-directory state, i.e. B's root.
	if out := invokeAgentTool(t, f.a.bundle.Agent, "bash", `{"command":"pwd"}`); !strings.Contains(out, cleanPath(f.rootB)) {
		t.Fatalf("bash pwd = %q, want B root %q", string(out), f.rootB)
	}
}

// TestNewSessionResetsWorkingDirectory proves one session's cd no longer leaks
// into the next session in the same workspace: A enters a subdirectory, a new
// session B starts at the workspace root (prompt WorkDir and tool cwd), and
// reopening A restores A's own directory.
func TestNewSessionResetsWorkingDirectory(t *testing.T) {
	f := newWorkspaceBindingFixture(t)
	ctx := context.Background()
	sub := filepath.Join(f.rootA, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	idA, err := f.ctrl.CreateSession(ctx, "A session")
	if err != nil {
		t.Fatalf("CreateSession(A): %v", err)
	}
	// A cd's into the subdirectory (as the working_directory tool would).
	if got := workingDirectoryOf(t, f.a.bundle.Agent, `{"subdir":"sub"}`); cleanPath(got.Cwd) != cleanPath(sub) {
		t.Fatalf("working_directory cwd = %q, want %q", got.Cwd, sub)
	}
	if got := f.a.workingDirState(f.rootA).Rel(); got != "sub" {
		t.Fatalf("shared working-directory state = %q, want sub", got)
	}

	// A new session B in the same workspace starts at the workspace root.
	if _, err := f.ctrl.CreateSession(ctx, "B session"); err != nil {
		t.Fatalf("CreateSession(B): %v", err)
	}
	if got := f.a.workingDirState(f.rootA).Rel(); got != "." {
		t.Fatalf("new session inherited cwd %q, want workspace root", got)
	}
	if got := workingDirectoryOf(t, f.a.bundle.Agent, "{}"); cleanPath(got.Cwd) != cleanPath(f.rootA) || got.Relative != "." {
		t.Fatalf("new session working_directory = %+v, want workspace root %q", got, f.rootA)
	}
	// Its first turn's prompt names the root, not the subdirectory.
	if err := f.ctrl.Submit(ctx, f.ctrl.CurrentSessionID(), "work in B"); err != nil {
		t.Fatalf("Submit(B session): %v", err)
	}
	waitWiringIdle(t, f.ctrl)
	sys := f.lastSystemPrompt()
	if !strings.Contains(sys, `Current working directory: "`+cleanPath(f.rootA)+`"`) {
		t.Fatalf("new session prompt WorkDir is not the workspace root; system prompt: %.600s", sys)
	}
	if strings.Contains(sys, cleanPath(sub)) {
		t.Fatalf("new session prompt still names the previous session's cwd %q: %.600s", sub, sys)
	}

	// Reopening A restores A's own directory (saved when it stopped being
	// current), so switching sessions does not lose track of A's cd.
	if _, err := f.ctrl.OpenSession(ctx, idA); err != nil {
		t.Fatalf("OpenSession(A): %v", err)
	}
	if got := f.a.workingDirState(f.rootA).Rel(); got != "sub" {
		t.Fatalf("reopened A working directory = %q, want sub", got)
	}
	if got := workingDirectoryOf(t, f.a.bundle.Agent, "{}"); cleanPath(got.Cwd) != cleanPath(sub) {
		t.Fatalf("reopened A working_directory cwd = %q, want %q", got.Cwd, sub)
	}
}
