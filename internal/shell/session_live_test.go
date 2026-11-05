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

	"github.com/zeokeel/zeokeel/internal/design"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// m6Step scripts one model call. A step with a tool name asks for that call;
// otherwise it streams Text as the answer.
type m6Step struct {
	text string
	tool string
	args string
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

	var chunks []runtime.Chunk
	if step.tool != "" {
		chunks = []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{{
				ID:        fmt.Sprintf("call-%d", index+1),
				Name:      step.tool,
				Arguments: json.RawMessage(step.args),
			}},
			Done: true,
		}}
	} else {
		chunks = []runtime.Chunk{{Text: step.text, Done: true}}
	}
	out := make(chan runtime.Chunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out, nil
}

// m6LiveApp builds a real controller + agent behind the shell, with the given
// tools registered, and opens one session.
func m6LiveApp(t *testing.T, provider runtime.Provider, toolset ...runtime.Tool) *App {
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
		"Suggested: sqlite · postgres", "Answer input", "Send answer",
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
// preview: a user bubble, a Markdown answer, two finished tool calls and a
// usage row, so the screenshot shows the message and tool-card surfaces.
func sessionPreviewSnapshot() *state.Snapshot {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "帮我把 README 更新成品牌主题说明，然后跑一下测试。"},
		{MsgID: "a1", Role: state.RoleAssistant, Text: "## 已完成\n\n- 更新了 `README.md` 的品牌小节\n- 运行 `go test ./...`，全部通过\n\n```go\nfunc main() {\n\tfmt.Println(\"hello, ZeoKeel\")\n}\n```"},
	}
	snap.Tools = map[string]state.ToolState{
		"c1": {
			CallID: "c1", Name: "bash", Status: state.ToolDone,
			Args:   json.RawMessage(`{"cmd":"go test ./..."}`),
			Result: "ok  github.com/zeokeel/zeokeel/internal/shell  37s",
		},
		"c2": {
			CallID: "c2", Name: "read_file", Status: state.ToolDone,
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
	dir := filepath.Join("..", "..", "docs", "design", "preview")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create preview dir: %v", err)
	}
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
