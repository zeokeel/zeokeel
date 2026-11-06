package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// m6Step scripts one model call. A step with a tool name asks for that call
// (optionally after Reasoning, matching how real providers think before a
// call); otherwise it streams Text as the answer.
type m6Step struct {
	text      string
	reasoning string
	tool      string
	args      string
	// usage, when set, is reported by this step's chunk (a real provider reports
	// token usage on the completion chunk).
	usage *runtime.Usage
}

// m6EchoTool is a tool the scripted provider can call; its result is the
// arguments, so tests can assert on the tool_start/end payloads.
type m6EchoTool struct {
	mu    sync.Mutex
	calls int
}

func (t *m6EchoTool) Name() string        { return "echo" }
func (t *m6EchoTool) Description() string { return "Echoes its arguments." }
func (t *m6EchoTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return string(args), nil
}

// m6Provider streams m6Steps in call order. Every call after the script ends
// answers "done", so a turn never hangs on an exhausted script.
type m6Provider struct {
	mu    sync.Mutex
	steps []m6Step
	calls int
}

func (p *m6Provider) Stream(_ context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	index := p.calls
	p.calls++
	step := m6Step{text: "done"}
	if index < len(p.steps) {
		step = p.steps[index]
	}
	p.mu.Unlock()

	chunk := runtime.Chunk{Reasoning: step.reasoning, Usage: step.usage, Done: true}
	if step.tool != "" {
		chunk.ToolCalls = []runtime.ToolCall{{
			ID:        fmt.Sprintf("call-%d", index+1),
			Name:      step.tool,
			Arguments: json.RawMessage(step.args),
		}}
	} else {
		chunk.Text = step.text
	}
	chunks := []runtime.Chunk{chunk}
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

// m6Rig is the real controller-backed fixture: a store, agent, controller and
// shell App with one open session. The inputs are kept so a test can close the
// controller and reopen the same session over the same store, exercising the
// persisted-event snapshot path instead of the live one.
type m6Rig struct {
	store       *store.Store
	agent       *runtime.Agent
	workspaceID string
	app         *App
}

// m6NewRig builds the fixture m6LiveApp returns: a real store, agent and
// controller behind the shell.
func m6NewRig(t *testing.T, provider runtime.Provider, toolset ...runtime.Tool) *m6Rig {
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

	registry := runtime.NewRegistry()
	for _, tool := range toolset {
		if err := registry.Register(tool); err != nil {
			t.Fatalf("register %s: %v", tool.Name(), err)
		}
	}
	agent, err := runtime.New(runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "test agent",
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
	// Tests allocate fresh Apps; clear any motion/UI state left at a reused
	// address by an earlier test (the maps are keyed by *App).
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	ctrl.OnUpdate(app.SetSnapshot)
	if _, err := ctrl.CreateSession(context.Background(), "live"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	app.SetSnapshot(ctrl.Snapshot())
	return &m6Rig{store: st, agent: agent, workspaceID: ws.ID, app: app}
}

// m6LiveApp builds a real controller + agent behind the shell, with the given
// tools registered, and opens one session.
func m6LiveApp(t *testing.T, provider runtime.Provider, toolset ...runtime.Tool) *App {
	t.Helper()
	return m6NewRig(t, provider, toolset...).app
}

// reopen closes the live controller and opens the app's current session
// through a fresh controller over the same store, then returns an App drawing
// that reopened snapshot. The transcript must keep its record order after the
// round trip through persisted events. The fixture's own cleanup stays
// registered; Controller.Close is idempotent.
func (r *m6Rig) reopen(t *testing.T) *App {
	t.Helper()
	sessionID := r.app.Ctrl.CurrentSessionID()
	if err := r.app.Ctrl.Close(); err != nil {
		t.Fatalf("close controller: %v", err)
	}
	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	ctrl, err := session.New(session.Config{
		Store:       r.store,
		Broker:      broker,
		Projector:   projector,
		Agent:       r.agent,
		WorkspaceID: r.workspaceID,
	})
	if err != nil {
		t.Fatalf("session.New (reopen): %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})
	if _, err := ctrl.OpenSession(context.Background(), sessionID); err != nil {
		t.Fatalf("OpenSession(%s): %v", sessionID, err)
	}
	app := &App{Screen: Session, Ctrl: ctrl}
	// A new App may reuse the dead one's address; drop the state maps keyed by
	// *App exactly as newLiveSnapshotApp does for a fresh fixture.
	sessionRenderStates.Delete(app)
	markdownStyles.Delete(app)
	toolResultStates.Delete(app)
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	liveMenuStates.Delete(app)
	shellUIStates.Delete(app)
	modeUIStates.Delete(app)
	pluginUIStates.Delete(app)
	startTabStates.Delete(app)
	ctrl.OnUpdate(app.SetSnapshot)
	app.SetSnapshot(ctrl.Snapshot())
	return app
}

func m6Submit(t *testing.T, app *App, text string) {
	t.Helper()
	if err := app.Ctrl.Submit(context.Background(), app.Ctrl.CurrentSessionID(), text); err != nil {
		t.Fatalf("Submit(%q): %v", text, err)
	}
	m6WaitIdle(t, app.Ctrl)
	app.Ctrl.Wait()
	app.SetSnapshot(app.Ctrl.Snapshot())
}

func m6WaitIdle(t *testing.T, ctrl *session.Controller) {
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

// m6Event builds a stored event for the pure timeline tests.
func m6Event(seq int64, typ string, payload any, ts time.Time) store.Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return store.Event{Seq: seq, Type: typ, Payload: raw, TS: ts}
}

func TestBuildTrajectoryOrdersAndAggregates(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	events := []store.Event{
		m6Event(1, session.EventMessage, map[string]any{
			"role": "user", "content": "hello",
		}, base),
		m6Event(2, session.EventToken, map[string]any{
			"msg_id": "m1", "text": "Hello ",
		}, base.Add(time.Second)),
		m6Event(3, session.EventToken, map[string]any{
			"msg_id": "m1", "text": "world",
		}, base.Add(2*time.Second)),
		m6Event(4, session.EventToolStart, map[string]any{
			"call_id": "call-1", "name": "echo", "args": map[string]any{"path": "README.md"},
		}, base.Add(3*time.Second)),
		m6Event(5, session.EventToolEnd, map[string]any{
			"call_id": "call-1", "result": "ok", "is_error": false, "duration_ns": int64(1200000),
		}, base.Add(4*time.Second)),
		m6Event(6, session.EventApprovalRequest, map[string]any{
			"call_id": "call-2", "name": "bash", "args": map[string]any{"cmd": "ls"}, "kind": "command",
		}, base.Add(5*time.Second)),
		m6Event(7, session.EventUsage, map[string]any{
			"turns": 1, "steps": 2, "in": 10, "out": 20, "cache_hit": 5, "context_pct": 12.5,
		}, base.Add(6*time.Second)),
		m6Event(8, session.EventStatus, map[string]any{"phase": "idle"}, base.Add(7*time.Second)),
		m6Event(9, session.EventError, map[string]any{"msg_id": "m1", "text": "boom"}, base.Add(8*time.Second)),
	}

	entries := buildTrajectory(events)
	if len(entries) != 8 {
		t.Fatalf("entries = %d, want 8 (two token events aggregate)", len(entries))
	}
	if entries[0].Label != "message · user" || entries[0].Detail != "hello" {
		t.Fatalf("message entry = %+v", entries[0])
	}
	if entries[1].Label != "assistant" || entries[1].Detail != "Hello world" || entries[1].Count != 2 {
		t.Fatalf("aggregated token entry = %+v", entries[1])
	}
	if entries[1].Seq != 3 || !entries[1].At.Equal(base.Add(2*time.Second)) {
		t.Fatalf("aggregated token kept first event position: %+v", entries[1])
	}
	if got := entries[1].Raw; got == "" || !containsAll(got, `"events":2`, `"text":"Hello world"`) {
		t.Fatalf("aggregated raw = %q", got)
	}
	if entries[2].Label != "tool_start · echo" || entries[2].Detail != `{"path":"README.md"}` {
		t.Fatalf("tool_start entry = %+v", entries[2])
	}
	if entries[3].Label != "tool_end · call-1" || entries[3].Detail != "ok" || entries[3].IsError {
		t.Fatalf("tool_end entry = %+v", entries[3])
	}
	if entries[4].Label != "approval · bash" || !containsAll(entries[4].Detail, `"cmd"`, "ls") {
		t.Fatalf("approval entry = %+v", entries[4])
	}
	if entries[5].Label != "usage" || !containsAll(entries[5].Detail, "turns=1", "in=10", "out=20", "ctx=12.5%") {
		t.Fatalf("usage entry = %+v", entries[5])
	}
	if entries[6].Label != "status · idle" {
		t.Fatalf("status entry = %+v", entries[6])
	}
	if entries[7].Label != "error" || entries[7].Detail != "boom" || !entries[7].IsError {
		t.Fatalf("error entry = %+v", entries[7])
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Seq < entries[i-1].Seq {
			t.Fatalf("entries not ordered oldest first: %+v", entries)
		}
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func TestTrajectoryTabShowsStoredEvents(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "echo", args: `{"path":"README.md"}`},
		{text: "final answer"},
	}}
	app := m6LiveApp(t, provider, &m6EchoTool{})
	m6Submit(t, app, "look at README.md")

	tester := ui.NewTester(app.View, 1600, 1000)
	if tester.HasText("tool_start · echo") {
		t.Fatal("trajectory timeline rendered on the Chat tab")
	}
	if err := tester.Click("Trajectory"); err != nil {
		t.Fatalf("click Trajectory: %v", err)
	}
	for _, want := range []string{
		"Trajectory", "message · user", "assistant", "final answer",
		"tool_start · echo", "tool_end · call-1", "status · idle", "usage",
	} {
		if !tester.HasText(want) {
			t.Fatalf("trajectory missing %q; texts: %v", want, tester.Texts())
		}
	}

	// The raw event payload (the full JSON including call_id) is collapsed
	// until the row is clicked; the compact args are always visible.
	if tester.HasText(`"call_id":"call-1"`) {
		t.Fatal("raw payload visible before expanding the row")
	}
	if err := tester.Click("tool_start · echo"); err != nil {
		t.Fatalf("expand tool_start: %v", err)
	}
	if !tester.HasText(`"call_id":"call-1"`) {
		t.Fatalf("raw payload missing after expanding; texts: %v", tester.Texts())
	}

	// Chat keeps the transcript and hides the timeline.
	if err := tester.Click("Chat"); err != nil {
		t.Fatalf("click Chat: %v", err)
	}
	if tester.HasText("tool_start · echo") {
		t.Fatal("trajectory timeline still visible on the Chat tab")
	}
	if !tester.HasText("echo · done") {
		t.Fatalf("tool card missing on Chat; texts: %v", tester.Texts())
	}
}

func TestTodoPanelRendersToolTasks(t *testing.T) {
	todo := tools.NewTodoTool()
	provider := &m6Provider{steps: []m6Step{
		{tool: "todo", args: `{"action":"add","task":"write tests"}`},
		{tool: "todo", args: `{"action":"add","task":"ship it"}`},
		{tool: "todo", args: `{"action":"complete","id":1}`},
		{text: "plan ready"},
	}}
	app := m6LiveApp(t, provider, todo)
	m6Submit(t, app, "plan the work")

	todos := app.Ctrl.Todos()
	if len(todos) != 2 {
		t.Fatalf("controller todos = %+v, want 2", todos)
	}
	if todos[0].Task != "write tests" || todos[0].Status != tools.TodoCompleted {
		t.Fatalf("first todo = %+v", todos[0])
	}
	if todos[1].Task != "ship it" || todos[1].Status != tools.TodoPending {
		t.Fatalf("second todo = %+v", todos[1])
	}

	tester := ui.NewTester(app.View, 1600, 1000)
	for _, want := range []string{"Todos", "1/2", "write tests", "ship it", "completed", "pending"} {
		if !tester.HasText(want) {
			t.Fatalf("todo panel missing %q; texts: %v", want, tester.Texts())
		}
	}
}

func TestQuestionCardAnswerFlow(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "ask_user", args: `{"question":"Which database should I use?","options":["sqlite","postgres"]}`},
		{text: "waiting for your answer"},
		{text: "Using sqlite."},
	}}
	app := m6LiveApp(t, provider, tools.NewAskUserTool())
	m6Submit(t, app, "choose a database")

	if got := app.Ctrl.Questions(); len(got) != 1 {
		t.Fatalf("pending questions = %+v, want 1", got)
	}
	tester := ui.NewTester(app.View, 1600, 1000)
	for _, want := range []string{
		"Question", "Which database should I use?",
		"Choose sqlite", "Choose postgres", "Answer input", "Send answer",
	} {
		if !tester.HasText(want) {
			t.Fatalf("question card missing %q; texts: %v", want, tester.Texts())
		}
	}

	if err := tester.Click("Answer input"); err != nil {
		t.Fatalf("click answer input: %v", err)
	}
	tester.Type("sqlite")
	if err := tester.Click("Send answer"); err != nil {
		t.Fatalf("click Send answer: %v", err)
	}
	// The answer starts the next turn; wait for it to finish and repaint.
	m6WaitIdle(t, app.Ctrl)
	app.Ctrl.Wait()
	app.SetSnapshot(app.Ctrl.Snapshot())
	tester.Frame()

	if got := app.Ctrl.Questions(); len(got) != 0 {
		t.Fatalf("questions after answering = %+v, want none", got)
	}
	if tester.HasText("Send answer") {
		t.Fatalf("question card still visible after answering; texts: %v", tester.Texts())
	}
	if !tester.HasText("sqlite") {
		t.Fatalf("answer missing from the transcript; texts: %v", tester.Texts())
	}
	if !tester.HasText("Using sqlite.") {
		t.Fatalf("second turn answer missing; texts: %v", tester.Texts())
	}
}

func TestArtifactCardListsPresentedArtifact(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "present", args: `{"title":"Test report","path":"reports/unit.md","markdown":"All tests passed."}`},
		{text: "report presented"},
	}}
	app := m6LiveApp(t, provider, tools.NewPresentTool())
	m6Submit(t, app, "present the report")

	artifacts := app.Ctrl.Artifacts()
	if len(artifacts) != 1 || artifacts[0].Title != "Test report" || artifacts[0].Path != "reports/unit.md" {
		t.Fatalf("controller artifacts = %+v", artifacts)
	}

	tester := ui.NewTester(app.View, 1600, 1000)
	for _, want := range []string{"Presented", "Test report", "reports/unit.md"} {
		if !tester.HasText(want) {
			t.Fatalf("artifact card missing %q; texts: %v", want, tester.Texts())
		}
	}
	callID := artifacts[0].CallID
	if app.uiState().artifactRaw[callID] {
		t.Fatal("artifact started expanded")
	}
	if err := tester.Click("Test report"); err != nil {
		t.Fatalf("expand artifact: %v", err)
	}
	if !app.uiState().artifactRaw[callID] {
		t.Fatal("clicking the artifact did not expand it")
	}
	if !tester.HasText("All tests passed.") {
		t.Fatalf("inline Markdown missing after expanding; texts: %v", tester.Texts())
	}
}

// sessionPreviewSnapshot is one complete, representative turn for the brand
// preview: a user bubble, the turn's process block (two finished tool calls
// above the answer) and a Markdown answer with its completion footer, plus a
// usage row. The Seq values put the tools between the question and the answer
// on the snapshot's record axis, so the screenshot exercises the Chat
// transcript's turn-inside order rather than the legacy session-tail block.
func sessionPreviewSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Seq: 1, Role: state.RoleUser, Text: "帮我把 README 更新成品牌主题说明，然后跑一下测试。"},
		{MsgID: "a1", Seq: 6, Role: state.RoleAssistant, Text: "## 已完成\n\n- 更新了 `README.md` 的品牌小节\n- 运行 `go test ./...`，全部通过\n\n```go\nfunc main() {\n\tfmt.Println(\"hello, ZeoKeel\")\n}\n```"},
	}
	snap.Tools = map[string]state.ToolState{
		"c1": {
			CallID: "c1", Name: "bash", Status: state.ToolDone,
			StartedSeq: 3, UpdatedSeq: 4,
			Args:   json.RawMessage(`{"cmd":"go test ./..."}`),
			Result: "ok  github.com/zeokeel/zeokeel/old/internal/shell  37s",
		},
		"c2": {
			CallID: "c2", Name: "read_file", Status: state.ToolDone,
			StartedSeq: 2, UpdatedSeq: 5,
			Args:   json.RawMessage(`{"path":"README.md"}`),
			Result: "# ZeoKeel\n\nThe native desktop agent.",
		},
	}
	snap.Usage = state.Usage{Turns: 1, Steps: 3, In: 1200, Out: 640, CacheHit: 860}
	return snap
}

// TestSessionBrandComposer pins the Session pilot's brand composer: it is the
// Home column width (720 DIPs at the 1600 design size), built from the theme's
// 4 DIP grid, and the native controls and labels the screens depend on are
// still there.
func TestSessionBrandComposer(t *testing.T) {
	h := newLiveSnapshotApp(t, Session, sessionPreviewSnapshot())
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	composer, ok := tt.Find("Session composer")
	if !ok {
		t.Fatalf("Session composer is not labeled: %q", tt.Texts())
	}
	if want := 180 * design.Space1; composer.W != want {
		t.Fatalf("Session composer width = %v, want the brand column %v", composer.W, want)
	}
	for _, want := range []string{"Composer input", "Add files or run commands", "Send", "Session usage"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("Session composer lost %q: %q", want, tt.Texts())
		}
	}
	// The header stays one piece: title, mode chip and the Chat tab row.
	for _, want := range []string{"Chat", "live"} {
		if !tt.HasText(want) {
			t.Fatalf("Session header lost %q: %q", want, tt.Texts())
		}
	}
}

// TestRenderSessionNativePreview renders the Session screen for brand review:
// light and dark at the 1600x1000 design size, written to docs/design/preview
// so the design is reproducible straight from the test.
func TestRenderSessionNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name  string
		theme string
	}{
		{"light", session.ThemeLight},
		{"dark", session.ThemeDark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := sessionPreviewSnapshot()
			h := newLiveSnapshotApp(t, Session, snap)
			// Chinese is the product default language; the preview shows the
			// shipped first-run look, with a meaningful session title.
			h.app.Language = LangZH
			id := h.ctrl.CurrentSessionID()
			if _, err := h.ctrl.RenameSession(context.Background(), id, "品牌主题与 Session 视觉"); err != nil {
				t.Fatalf("rename preview session: %v", err)
			}
			snap.SessionID = id
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			h.app.SetSnapshot(snap)
			img := ui.Render(h.app.View, 1600, 1000, 1)
			path := filepath.Join(dir, "session-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close %s: %v", path, err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("preview %s is empty: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}

// TestChatInterleavesTurnToolsBeforeFinalAnswer is the Chat order regression:
// a two-turn session with one tool call per turn must render
//
//	user1 -> turn1 process block -> answer1+footer -> user2 -> turn2 block -> answer2
//
// so the cards a turn produced sit above that turn's final answer and the first
// turn's block sits above the second question, never accumulated at the session
// tail. The reopened snapshot (rebuilt from persisted events) keeps the same
// order.
func TestChatInterleavesTurnToolsBeforeFinalAnswer(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "bash", args: `{"cmd":"first-cmd"}`},
		{text: "first answer"},
		{tool: "read_file", args: `{"path":"later.txt"}`},
		{text: "second answer"},
	}}
	rig := m6NewRig(t, provider, summaryTool{name: "bash"}, summaryTool{name: "read_file"})
	// Collapsed cards keep both turns inside the 1000-DIP window; the order
	// assertion reads the card title rows, which render in both modes.
	if err := rig.store.SetSetting(session.SettingWorkDetails, session.WorkDetailsStandard); err != nil {
		t.Fatalf("work details setting: %v", err)
	}
	m6Submit(t, rig.app, "first question")
	m6Submit(t, rig.app, "second question")
	assertSeqAxis(t, rig.app.Snapshot())
	assertChatTurnOrder(t, rig.app)

	// Reopen: the snapshot is rebuilt from the messages and events tables, so
	// the Seq axis is the store's event seq instead of the live fold counter.
	// The transcript order must be identical.
	reopened := rig.reopen(t)
	assertSeqAxis(t, reopened.Snapshot())
	assertChatTurnOrder(t, reopened)
}

// assertSeqAxis pins the input the record-ordered transcript relies on: both
// turns sit on one strictly increasing Seq axis, with each call between the
// question and the answer it belongs to (live fold counter and reopened store
// event seq axes are each internally consistent; they need not match).
func assertSeqAxis(t *testing.T, snap *state.Snapshot) {
	t.Helper()
	if !transcriptOrderEnabled(snap) {
		t.Fatal("snapshot has no Message.Seq axis")
	}
	msgSeq := func(text string) uint64 {
		t.Helper()
		for i := range snap.Messages {
			if snap.Messages[i].Text == text {
				if snap.Messages[i].Seq == 0 {
					t.Fatalf("message %q has no Seq", text)
				}
				return snap.Messages[i].Seq
			}
		}
		t.Fatalf("message %q not in snapshot", text)
		return 0
	}
	toolSeq := func(name string) uint64 {
		t.Helper()
		for _, tool := range snap.Tools {
			if tool.Name == name {
				if tool.StartedSeq == 0 {
					t.Fatalf("tool %q has no StartedSeq", name)
				}
				return tool.StartedSeq
			}
		}
		t.Fatalf("tool %q not in snapshot", name)
		return 0
	}
	axis := []struct {
		label string
		seq   uint64
	}{
		{"first question", msgSeq("first question")},
		{"bash call", toolSeq("bash")},
		{"first answer", msgSeq("first answer")},
		{"second question", msgSeq("second question")},
		{"read_file call", toolSeq("read_file")},
		{"second answer", msgSeq("second answer")},
	}
	for i := 1; i < len(axis); i++ {
		if axis[i].seq <= axis[i-1].seq {
			t.Fatalf("Seq axis is not increasing at %s: %v <= %v (%+v)",
				axis[i].label, axis[i].seq, axis[i-1].seq, axis)
		}
	}
}

// TestChatHandBuiltSnapshotKeepsLegacyToolTail guards the degradation path:
// a fixture that never set Message.Seq (hand-built snapshots) must keep the
// old layout — all messages first, then the session-wide tool block — instead
// of sorting the zero positions to the top.
func TestChatHandBuiltSnapshotKeepsLegacyToolTail(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "legacy question"},
		{MsgID: "a1", Role: state.RoleAssistant, Text: "legacy answer"},
	}
	snap.Tools["c1"] = state.ToolState{
		CallID: "c1", Name: "bash", Status: state.ToolDone,
		Args: json.RawMessage(`{"cmd":"ls"}`), Result: "ok",
	}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if transcriptOrderEnabled(snap) {
		t.Fatal("the fixture unexpectedly has a Seq axis")
	}
	answer, ok := tt.Find("legacy answer")
	if !ok {
		t.Fatalf("legacy answer missing: %v", tt.Texts())
	}
	card, ok := tt.Find("bash · done")
	if !ok {
		t.Fatalf("legacy tool card missing: %v", tt.Texts())
	}
	if card.Y <= answer.Y {
		t.Fatalf("legacy layout moved the tool block above the answer: card y=%.1f answer y=%.1f", card.Y, answer.Y)
	}
	if !tt.HasText("Ran 1 command") {
		t.Fatalf("legacy summary line missing: %v", tt.Texts())
	}
}

// TestTurnProcessBlocksFoldIndependently pins the per-block disclosure added
// with the turn-inside order: collapsing one turn's summary line hides only
// that block's cards, not every turn's.
func TestTurnProcessBlocksFoldIndependently(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{tool: "bash", args: `{"cmd":"first-cmd"}`},
		{text: "first answer"},
		{tool: "read_file", args: `{"path":"later.txt"}`},
		{text: "second answer"},
	}}
	rig := m6NewRig(t, provider, summaryTool{name: "bash"}, summaryTool{name: "read_file"})
	if err := rig.store.SetSetting(session.SettingWorkDetails, session.WorkDetailsStandard); err != nil {
		t.Fatalf("work details setting: %v", err)
	}
	rig.app.Language = LangEN
	m6Submit(t, rig.app, "first question")
	m6Submit(t, rig.app, "second question")

	tt := ui.NewTester(rig.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("bash · done") || !tt.HasText("read_file · done") {
		t.Fatalf("both turns' cards must start visible: %v", tt.Texts())
	}
	if err := tt.Click("Ran 1 command"); err != nil {
		t.Fatalf("collapse turn 1's process block: %v", err)
	}
	tt.Frame()
	if tt.HasText("bash · done") {
		t.Fatalf("turn 1's cards are still visible after collapsing: %v", tt.Texts())
	}
	if !tt.HasText("read_file · done") {
		t.Fatalf("collapsing turn 1 also hid turn 2's cards: %v", tt.Texts())
	}
	if err := tt.Click("Ran 1 command"); err != nil {
		t.Fatalf("expand turn 1's process block: %v", err)
	}
	tt.Frame()
	if !tt.HasText("bash · done") {
		t.Fatalf("turn 1's cards did not return: %v", tt.Texts())
	}
}

// TestChatReasoningBeforeToolKeepsProcessAboveAnswer is the real-runtime
// regression for the ordering bug users hit: the provider thinks (Reasoning),
// calls a tool, and only then streams the visible answer. The runtime
// publishes one assistant msgID per turn, so the message is created by the
// pre-tool Reasoning event; the Chat view must still render
// user -> process block -> answer, live and after the store-backed reopen.
func TestChatReasoningBeforeToolKeepsProcessAboveAnswer(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{
		{reasoning: "Let me run the first command.", tool: "bash", args: `{"cmd":"first-cmd"}`},
		{text: "first answer"},
		{reasoning: "Now read the file.", tool: "read_file", args: `{"path":"later.txt"}`},
		{text: "second answer"},
	}}
	rig := m6NewRig(t, provider, summaryTool{name: "bash"}, summaryTool{name: "read_file"})
	// Collapsed cards keep both turns inside the 1000-DIP window; the order
	// assertion reads the card title rows, which render in both modes.
	if err := rig.store.SetSetting(session.SettingWorkDetails, session.WorkDetailsStandard); err != nil {
		t.Fatalf("work details setting: %v", err)
	}
	m6Submit(t, rig.app, "first question")
	m6Submit(t, rig.app, "second question")

	assertReasoningFirstAxis(t, rig.app.Snapshot())
	assertChatTurnOrder(t, rig.app)

	// Reopen: the seq axis becomes the persisted event seq and LastSeq must be
	// recovered from the last token event, not from the reasoning that created
	// the message.
	reopened := rig.reopen(t)
	assertReasoningFirstAxis(t, reopened.Snapshot())
	assertChatTurnOrder(t, reopened)
}

// assertReasoningFirstAxis pins the shape that broke Seq-only ordering: each
// turn's assistant message starts at its pre-tool Reasoning (Seq before the
// call) and ends at its visible text (LastSeq after the call).
func assertReasoningFirstAxis(t *testing.T, snap *state.Snapshot) {
	t.Helper()
	find := func(text string) state.Message {
		t.Helper()
		for _, m := range snap.Messages {
			if m.Text == text {
				return m
			}
		}
		t.Fatalf("message %q missing from snapshot: %+v", text, snap.Messages)
		return state.Message{}
	}
	for _, turn := range []struct {
		answer string
		callID string
	}{
		{"first answer", "call-1"},
		{"second answer", "call-3"}, // provider call index 2, so its ID is call-3
	} {
		msg := find(turn.answer)
		tool, ok := snap.Tools[turn.callID]
		if !ok {
			t.Fatalf("tool %q missing from snapshot: %+v", turn.callID, snap.Tools)
		}
		if !(msg.Seq < tool.StartedSeq && tool.StartedSeq < msg.LastSeq) {
			t.Fatalf("%q axis = Seq %d, tool %q StartedSeq %d, LastSeq %d; want Seq < tool < LastSeq",
				turn.answer, msg.Seq, turn.callID, tool.StartedSeq, msg.LastSeq)
		}
	}
}

// assertChatTurnOrder pins the record order of the two-turn transcript: each
// turn's tool block is above that turn's answer, and turn 2's rows are below
// turn 1's block. It also pins that each summary counts only its own turn.
func assertChatTurnOrder(t *testing.T, app *App) {
	t.Helper()
	app.Language = LangEN
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	at := func(label string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing from the Chat transcript: %v", label, tt.Texts())
		}
		return r
	}
	user1, answer1 := at("first question"), at("first answer")
	card1 := at("bash · done")
	user2, answer2 := at("second question"), at("second answer")
	card2 := at("read_file · done")

	if user1.Y >= card1.Y {
		t.Fatalf("turn 1 user message (y=%.1f) is not above its tool block (y=%.1f)", user1.Y, card1.Y)
	}
	if card1.Y >= answer1.Y {
		t.Fatalf("turn 1 tool card (y=%.1f) is not above turn 1 answer (y=%.1f)", card1.Y, answer1.Y)
	}
	if answer1.Y >= user2.Y {
		t.Fatalf("turn 1 answer (y=%.1f) is not above the second user message (y=%.1f)", answer1.Y, user2.Y)
	}
	if card1.Y >= user2.Y {
		t.Fatalf("turn 1 tool card (y=%.1f) is not above the second turn's user message (y=%.1f); the tool block is still a session tail", card1.Y, user2.Y)
	}
	if card2.Y <= user2.Y || card2.Y >= answer2.Y {
		t.Fatalf("turn 2 tool card (y=%.1f) must sit between its user message (y=%.1f) and answer (y=%.1f)", card2.Y, user2.Y, answer2.Y)
	}

	// The summaries recount per turn: turn 1 ran one command, turn 2 read one
	// file. A session-wide accumulator would print one combined line instead.
	for _, want := range []string{"Ran 1 command", "read 1 file"} {
		if !tt.HasText(want) {
			t.Fatalf("per-turn summary %q missing: %v", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"Ran 2 commands", "read 2 files", "Ran 1 command, read 1 file"} {
		if tt.HasText(unwanted) {
			t.Fatalf("summary is still session-cumulative: found %q in %v", unwanted, tt.Texts())
		}
	}
}

// ---------------------------------------------------------------------------
// Composer draft and element identity across session tabs (P2-3).
// ---------------------------------------------------------------------------

// sessionTextNode returns the accessibility node of one labeled text field,
// including its stable element ID and current value.
func sessionTextNode(t *testing.T, tt *ui.Tester, label string) memoryAccessNode {
	t.Helper()
	for _, node := range memoryAccessNodes(t, tt) {
		if node.Label == label && node.Role == nativeRoleTextField {
			return node
		}
	}
	t.Fatalf("accessibility tree has no text field labeled %q", label)
	return memoryAccessNode{}
}

// TestComposerDraftAndIdentityAcrossTabs pins the shared sessionBody frame: a
// draft typed on Chat survives Chat → Trajectory → Memory → Chat, the composer
// keeps its element identity (no rebuild, so its one-shot AutoFocus cannot
// re-fire and steal the focus from the clicked tab), and opening Settings and
// returning keeps both the draft and the session tab selection.
func TestComposerDraftAndIdentityAcrossTabs(t *testing.T) {
	h := newMemoryApp(t)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Type a draft on Chat without sending it.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus the composer: %v", err)
	}
	const draft = "draft that must survive tab switches"
	tt.Type(draft)
	tt.Frame()
	composer := sessionTextNode(t, tt, "Composer input")
	if composer.Value != draft {
		t.Fatalf("composer value after typing = %q, want %q", composer.Value, draft)
	}
	if !tt.Focused("Composer input") {
		t.Fatal("the composer is not focused after typing")
	}

	// assertKept checks the shared composer after a tab switch: same element
	// ID (not rebuilt), same draft, and the focus stayed on the clicked tab so
	// the composer's one-shot AutoFocus did not fire again.
	assertKept := func(step string) {
		t.Helper()
		node := sessionTextNode(t, tt, "Composer input")
		if node.ID != composer.ID {
			t.Fatalf("%s: composer element rebuilt (id %d → %d)", step, composer.ID, node.ID)
		}
		if node.Value != draft {
			t.Fatalf("%s: composer value = %q, want the draft %q", step, node.Value, draft)
		}
		if h.app.draft != draft {
			t.Fatalf("%s: app draft = %q, want %q", step, h.app.draft, draft)
		}
		if tt.Focused("Composer input") {
			t.Fatalf("%s: the rebuilt composer's AutoFocus stole the focus from the tab", step)
		}
	}

	for _, step := range []struct {
		tab     string
		pane    string
		visible bool
	}{
		{"Trajectory", "Trajectory", true},
		{"Memory", "Workspace memory", true},
		{"Chat", "session-chat", false},
	} {
		step := step
		if err := tt.Click(step.tab); err != nil {
			t.Fatalf("switch to %s: %v", step.tab, err)
		}
		tt.Frame()
		tt.Frame() // an extra frame would expose a re-fired one-shot AutoFocus
		if !tt.Focused(step.tab) {
			t.Fatalf("after clicking %s, the focus is not on the tab", step.tab)
		}
		if step.tab == "Memory" && !tt.HasText(step.pane) {
			t.Fatalf("Memory pane not rendered: %v", tt.Texts())
		}
		if step.tab == "Trajectory" && !tt.HasText("Trajectory") {
			t.Fatalf("Trajectory pane not rendered: %v", tt.Texts())
		}
		assertKept("after switching to " + step.tab)
	}

	// Opening Settings from the Memory tab and returning keeps the tab
	// selection and the composer draft/identity.
	if err := tt.Click("Memory"); err != nil {
		t.Fatalf("select the Memory tab: %v", err)
	}
	tt.Frame()
	openMemorySettings(t, h, tt)
	if h.app.Screen != Settings {
		t.Fatalf("settings overlay did not open: screen %v", h.app.Screen)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("Escape left screen %v, want Session", h.app.Screen)
	}
	if want, got := h.app.liveTabPosition(liveMemory), h.app.uiState().tab; got != want {
		t.Fatalf("session tab after returning from Settings = %d, want Memory (%d)", got, want)
	}
	if !tt.HasText("Workspace memory") {
		t.Fatalf("Memory pane not restored after Settings: %v", tt.Texts())
	}
	assertKept("after returning from Settings")
}
