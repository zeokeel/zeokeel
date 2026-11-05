package shell

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// shellProvider streams a gated echo call on its first model call and a short
// final answer afterwards. With no gate needed (the slash and mention tests)
// the first chunk is the final answer.
type shellProvider struct {
	callTool bool
	block    bool
	// hang keeps the stream open without sending a chunk until the turn's
	// context is cancelled, so the session stays PhaseRunning for a test that
	// needs a stable running turn.
	hang bool

	mu       sync.Mutex
	calls    int
	requests []runtime.Request
}

func (p *shellProvider) Stream(ctx context.Context, req runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	if p.hang {
		out := make(chan runtime.Chunk)
		go func() {
			defer close(out)
			<-ctx.Done()
		}()
		return out, nil
	}

	var chunks []runtime.Chunk
	if p.callTool && call == 0 {
		chunks = []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        "call-1",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
			Done: true,
		}}
	} else {
		chunks = []runtime.Chunk{{Text: "done", Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	if p.block {
		go func() {
			defer close(out)
			<-ctx.Done()
		}()
	} else {
		close(out)
	}
	return out, nil
}

func (p *shellProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// requestAt returns the provider request of one model call.
func (p *shellProvider) requestAt(i int) (runtime.Request, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.requests) {
		return runtime.Request{}, false
	}
	return p.requests[i], true
}

type shellTool struct{ calls atomic.Int64 }

func (t *shellTool) Name() string        { return "echo" }
func (t *shellTool) Description() string { return "Echoes its arguments." }
func (t *shellTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	t.calls.Add(1)
	return string(args), nil
}

type liveApp struct {
	app   *App
	ctrl  *session.Controller
	store *store.Store
	tool  *shellTool
	dir   string
}

func newLiveApp(t *testing.T, provider *shellProvider) *liveApp {
	t.Helper()
	return newLiveAppWithTitle(t, provider, "live")
}

// newLiveAppWithTitle is newLiveApp with control over the initial session
// title: auto-title tests need a placeholder because a deliberately named
// session is never renamed automatically.
func newLiveAppWithTitle(t *testing.T, provider *shellProvider, title string) *liveApp {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ws, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	tool := &shellTool{}
	registry := runtime.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	gate := runtime.NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "echo"
	})
	agent, err := runtime.New(runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "test agent",
		Gate:        gate,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	ctrl, err := session.New(session.Config{
		Store:       st,
		Broker:      broker,
		Projector:   projector,
		Agent:       agent,
		Gate:        gate,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})

	app := &App{Screen: Session, Ctrl: ctrl}
	ctrl.OnUpdate(app.SetSnapshot)
	if _, err := ctrl.CreateSession(context.Background(), title); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	app.SetSnapshot(ctrl.Snapshot())
	return &liveApp{app: app, ctrl: ctrl, store: st, tool: tool, dir: dir}
}

func waitPendingApproval(t *testing.T, ctrl *session.Controller) state.ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pending := ctrl.PendingApprovals(); len(pending) > 0 {
			return pending[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a pending approval; snapshot: %+v", ctrl.Snapshot())
	return state.ApprovalRequest{}
}

func waitNoPending(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(ctrl.PendingApprovals()) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pending approvals did not clear: %+v", ctrl.PendingApprovals())
}

func waitRunning(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().Phase == state.PhaseRunning {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for running; snapshot: %+v", ctrl.Snapshot())
}

func waitIdle(t *testing.T, ctrl *session.Controller) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().Phase == state.PhaseIdle {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for idle; snapshot: %+v", ctrl.Snapshot())
}

func TestSlashCommandPalette(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("/")
	if h.app.draft != "/" {
		t.Fatalf("draft = %q, want %q", h.app.draft, "/")
	}
	for _, command := range []string{"/clear", "/stop", "/help"} {
		if !tt.HasText(command) {
			t.Fatalf("command palette missing %q; texts: %v", command, tt.Texts())
		}
	}
	if err := tt.Click("/clear"); err != nil {
		t.Fatalf("click /clear: %v", err)
	}
	if h.app.draft != "" {
		t.Fatalf("draft after /clear = %q, want empty", h.app.draft)
	}
	if tt.HasText("/clear") {
		t.Fatal("command palette still visible after clearing the draft")
	}

	// Normal typing that does not start with "/" never opens the palette.
	tt.Type("hello")
	if tt.HasText("/stop") {
		t.Fatal("command palette shown for a normal draft")
	}
}

func TestSlashStopCallsController(t *testing.T) {
	h := newLiveApp(t, &shellProvider{block: true})
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "hold the turn"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRunning(t, h.ctrl)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type("/stop")
	if !tt.HasText("/stop") {
		t.Fatalf("command palette missing /stop; texts: %v", tt.Texts())
	}
	if err := tt.Click("/stop"); err != nil {
		t.Fatalf("click /stop: %v", err)
	}
	if h.app.draft != "" {
		t.Fatalf("draft after /stop = %q, want empty", h.app.draft)
	}
	if tt.HasText("/stop") {
		t.Fatal("command palette still visible after choosing /stop")
	}
	if got := h.ctrl.Snapshot().Phase; got != state.PhaseIdle {
		t.Fatalf("phase right after /stop = %q, want idle", got)
	}
	h.ctrl.Wait()
}

func TestApprovalCardAboveComposer(t *testing.T) {
	provider := &shellProvider{callTool: true}
	h := newLiveApp(t, provider)
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "needs approval"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	req := waitPendingApproval(t, h.ctrl)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("Approval required") {
		t.Fatalf("approval card missing; texts: %v", tt.Texts())
	}
	if !tt.HasText(req.Name) {
		t.Fatalf("approval card missing tool %q; texts: %v", req.Name, tt.Texts())
	}
	if !tt.HasText("README.md") {
		t.Fatalf("approval card missing compact args; texts: %v", tt.Texts())
	}
	if !tt.HasText("Allow") || !tt.HasText("Deny") {
		t.Fatalf("approval card missing decisions; texts: %v", tt.Texts())
	}

	if err := tt.Click("Allow"); err != nil {
		t.Fatalf("click Allow: %v", err)
	}
	waitNoPending(t, h.ctrl)
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := h.tool.calls.Load(); got != 1 {
		t.Fatalf("gated tool calls = %d, want 1", got)
	}
	if got := provider.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (tool loop completed)", got)
	}
}

func TestApprovalDenyCard(t *testing.T) {
	provider := &shellProvider{callTool: true}
	h := newLiveApp(t, provider)
	id := h.ctrl.CurrentSessionID()
	if err := h.ctrl.Submit(context.Background(), id, "needs approval"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitPendingApproval(t, h.ctrl)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Deny"); err != nil {
		t.Fatalf("click Deny: %v", err)
	}
	waitNoPending(t, h.ctrl)
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := h.tool.calls.Load(); got != 0 {
		t.Fatalf("denied tool calls = %d, want 0", got)
	}
	// The denial is a tool result, not a failed turn: the model got a second
	// call and the conversation stays usable.
	if got := provider.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 after a denial", got)
	}
	if last := h.ctrl.Snapshot().LastError; last != nil {
		t.Fatalf("LastError = %+v, want none after a denial", last)
	}
}

func TestLiveSessionFallsBackToControllerSnapshot(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	// Drop the pushed snapshot: the view must still pick up the controller's
	// current snapshot instead of painting the static demo screen.
	h.app.snap.Store(nil)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("Send") {
		t.Fatalf("static fallback painted with a live controller; texts: %v", tt.Texts())
	}
}

func TestMentionMenuListsWorkspaceEntries(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if err := os.WriteFile(filepath.Join(h.dir, "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	h.app.draft = "read @note"
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("notes.md") {
		t.Fatalf("mention menu missing workspace entry; texts: %v", tt.Texts())
	}
	if err := tt.Click("notes.md"); err != nil {
		t.Fatalf("click mention: %v", err)
	}
	if want := "read @notes.md "; h.app.draft != want {
		t.Fatalf("draft after mention = %q, want %q", h.app.draft, want)
	}
}

func TestAttachmentMenu(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)
	clickAddControl(t, tt)
	if !tt.HasText("Files") || !tt.HasText("Commands") {
		t.Fatalf("attachment menu missing entries; texts: %v", tt.Texts())
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	if h.app.attachOpen {
		t.Fatal("attachment menu still open after choosing an entry")
	}
}

// typeComposer types text into the focused composer and submits it with
// Enter, the typed-slash-command path.
func typeComposer(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("click composer: %v", err)
	}
	tt.Type(text)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
}

// TestSlashCommandsExecuteLocally drives every catalog command through the
// composer's Enter path and checks it runs locally, never as a model prompt.
func TestSlashCommandsExecuteLocally(t *testing.T) {
	t.Run("/clear", func(t *testing.T) {
		provider := &shellProvider{}
		h := newLiveApp(t, provider)
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/clear")
		if h.app.draft != "" {
			t.Fatalf("/clear left draft %q", h.app.draft)
		}
		if provider.callCount() != 0 {
			t.Fatal("/clear was sent to the model")
		}
	})
	t.Run("/help", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/help")
		if !strings.HasPrefix(h.app.draft, "Commands:") {
			t.Fatalf("/help draft = %q", h.app.draft)
		}
		for _, name := range []string{"/clear", "/model", "/plan"} {
			if !strings.Contains(h.app.draft, name) {
				t.Fatalf("/help text missing %q: %q", name, h.app.draft)
			}
		}
	})
	t.Run("/plan", func(t *testing.T) {
		provider := &shellProvider{}
		h := newLiveApp(t, provider)
		// Plan mode is persisted in the session's settings, not sent to the
		// model.
		if h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
			t.Fatal("new session starts in plan mode")
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/plan")
		if !h.ctrl.PlanMode(h.ctrl.CurrentSessionID()) {
			t.Fatal("/plan did not turn plan mode on")
		}
		if h.app.draft != "" {
			t.Fatalf("/plan left draft %q", h.app.draft)
		}
		if got := provider.callCount(); got != 0 {
			t.Fatalf("/plan model calls = %d, want 0", got)
		}
		tt.Frame()
		if !tt.HasText(planBadgeLabelEN) {
			t.Fatalf("plan badge missing after /plan: %v", tt.Texts())
		}
	})
	t.Run("/new", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		before, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/new")
		after, err := h.ctrl.ListSessions(context.Background())
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		if len(after) != len(before)+1 {
			t.Fatalf("/new sessions = %d, want %d", len(after), len(before)+1)
		}
		if h.app.Screen != Session {
			t.Fatalf("/new screen = %v, want Session", h.app.Screen)
		}
	})
	t.Run("/settings", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/settings")
		if h.app.Screen != Settings {
			t.Fatalf("/settings screen = %v, want Settings", h.app.Screen)
		}
	})
	t.Run("/automate", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/automate")
		if h.app.Screen != Automation {
			t.Fatalf("/automate screen = %v, want Automation", h.app.Screen)
		}
	})
	t.Run("/plugins", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/plugins")
		if h.app.Screen != Plugins {
			t.Fatalf("/plugins screen = %v, want Plugins", h.app.Screen)
		}
	})
	t.Run("/model", func(t *testing.T) {
		h := newLiveApp(t, &shellProvider{})
		tt := ui.NewTester(h.app.View, 1600, 1000)
		typeComposer(t, tt, "/model")
		if !h.app.liveMenus().modelOpen {
			t.Fatalf("/model did not open the model menu: %v", tt.Texts())
		}
	})
}

// TestUnknownSlashFallsThroughToModel checks DSH's rule for an unrecognized
// slash line: it is a normal prompt, not a silent local no-op.
func TestUnknownSlashFallsThroughToModel(t *testing.T) {
	provider := &shellProvider{}
	h := newLiveApp(t, provider)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	typeComposer(t, tt, "/nonesuch")
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	if got := provider.callCount(); got != 1 {
		t.Fatalf("unknown slash model calls = %d, want 1", got)
	}
	if h.app.draft != "" {
		t.Fatalf("unknown slash left draft %q", h.app.draft)
	}
}

// TestCatalogCommandsAreKnown pins the full catalog, including the commands
// added after the first three.
func TestCatalogCommandsAreKnown(t *testing.T) {
	for _, name := range []string{
		"/clear", "/stop", "/plan", "/help", "/new", "/settings",
		"/automate", "/plugins", "/model",
	} {
		if !isSlashCommand(name) {
			t.Errorf("%s is not a known command", name)
		}
	}
	// /compact is deliberately not in the palette: compaction is automatic
	// and the help text says so. It falls through as model prose.
	for _, name := range []string{"/bogus", "/compact", "hello", "", "/"} {
		if isSlashCommand(name) {
			t.Errorf("%q reported as a known command", name)
		}
	}
	// Arguments after whitespace still resolve to the command.
	if !isSlashCommand("/stop now") {
		t.Error("/stop now did not resolve to /stop")
	}
	// /plan takes an optional argument, so its forms must dispatch locally
	// instead of falling through to the model.
	for _, line := range []string{"/plan", "/plan off", "/plan OFF", "/plan 写测试"} {
		if !isSlashCommand(line) {
			t.Errorf("%q is not a known command", line)
		}
	}
	// It resolves to /plan itself and keeps the rest as the argument.
	for _, tc := range []struct{ line, arg string }{
		{"/plan", ""},
		{"/plan off", "off"},
		{"/plan 写测试", "写测试"},
		{" /plan   two words  ", "two words"},
	} {
		if got := planCommandArg(tc.line); got != tc.arg {
			t.Errorf("planCommandArg(%q) = %q, want %q", tc.line, got, tc.arg)
		}
	}
	if got := planCommandArg("/planning"); got != "" {
		t.Errorf("planCommandArg(%q) = %q, want empty (not a /plan line)", "/planning", got)
	}
}

// TestMentionMenuIncludesSessions checks the "@" menu lists sessions next to
// files and inserts this build's documented "#<title>" fallback token.
func TestMentionMenuIncludesSessions(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "lively.md", "# lively\n")
	h.app.draft = "read @liv"
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("lively.md") {
		t.Fatalf("mention menu missing the file: %v", tt.Texts())
	}
	if !tt.HasText("session · live") {
		t.Fatalf("mention menu missing the session: %v", tt.Texts())
	}
	if err := tt.Click("session · live"); err != nil {
		t.Fatalf("click session mention: %v", err)
	}
	if want := "read #live "; h.app.draft != want {
		t.Fatalf("draft after session mention = %q, want %q", h.app.draft, want)
	}

	// Files keep the "@path" form.
	h.app.draft = "read @live"
	tt.Frame()
	if err := tt.Click("lively.md"); err != nil {
		t.Fatalf("click file mention: %v", err)
	}
	if want := "read @lively.md "; h.app.draft != want {
		t.Fatalf("draft after file mention = %q, want %q", h.app.draft, want)
	}
}

// TestMentionSessionsWithNoFileMatches checks the menu still opens when only
// a session matches, which the old file-only gate would have hidden.
func TestMentionSessionsWithNoFileMatches(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.draft = "ask @zh"
	if _, err := h.ctrl.RenameSession(context.Background(), h.ctrl.CurrentSessionID(), "zh-plan"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if !tt.HasText("session · zh-plan") {
		t.Fatalf("session-only mention menu missing: %v", tt.Texts())
	}
}
