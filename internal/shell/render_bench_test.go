package shell

// render_bench_test.go benchmarks the headless live render — App.View drawn
// through ui.Render — for every top-level screen at the desktop design size
// (1600x1000) and at the window's narrow minimum (900x600). It is the
// reproducible source of the numbers in docs/design/perf.md; the command is
//
//	go test -run '^$' -bench 'BenchmarkRender' -benchmem ./internal/shell
//
// Each benchmark builds the same controller-backed fixture the shell tests use
// (a real store, agent, controller and snapshot) once, outside the timed loop,
// and then measures warm-frame renders. A warm frame is what the user pays for
// on every input and stream tick; the first cold frame of an app is dominated
// by one-time parse/layout caches and is not what the benchmark reports.

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"

	runtimepkg "github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// newBenchApp builds the live controller fixture for one screen, the
// *testing.B counterpart of newLiveApp: a real SQLite store in the
// benchmark's temp dir, an agent over the scripted test provider, a session
// controller and the initial snapshot.
func newBenchApp(b *testing.B, screen Screen, snap *state.Snapshot) *App {
	b.Helper()
	dir := b.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	ws, err := st.CreateWorkspace("bench", dir)
	if err != nil {
		b.Fatalf("CreateWorkspace: %v", err)
	}

	tool := &shellTool{}
	registry := runtimepkg.NewRegistry()
	if err := registry.Register(tool); err != nil {
		b.Fatalf("register echo: %v", err)
	}
	gate := runtimepkg.NewApprovalQueue(func(name string, _ json.RawMessage) bool {
		return name == "echo"
	})
	agent, err := runtimepkg.New(runtimepkg.Config{
		Provider:    &shellProvider{},
		Tools:       registry,
		Instruction: "bench agent",
		Gate:        gate,
	})
	if err != nil {
		b.Fatalf("runtime.New: %v", err)
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
		b.Fatalf("session.New: %v", err)
	}
	b.Cleanup(func() {
		_ = ctrl.Close()
		projector.Close()
		broker.Close()
	})

	app := &App{Screen: screen, Ctrl: ctrl, Language: LangZH}
	ctrl.OnUpdate(app.SetSnapshot)
	if _, err := ctrl.CreateSession(context.Background(), "bench session"); err != nil {
		b.Fatalf("CreateSession: %v", err)
	}
	if snap == nil {
		snap = ctrl.Snapshot()
	}
	app.SetSnapshot(snap)
	return app
}

// syntheticTrajectoryRows builds a deterministic trace: alternating tool_start
// and status rows with uniquely marked first and last rows, so a test can tell
// the rows the virtual list built from the rest of the history.
func syntheticTrajectoryRows(total int) []store.Event {
	rows := make([]store.Event, 0, total)
	for i := 0; i < total; i++ {
		switch i {
		case 0:
			rows = append(rows, store.Event{Type: session.EventToolEnd,
				Payload: json.RawMessage(`{"call_id":"head-call","result":"head-result-marker"}`)})
		case total - 1:
			rows = append(rows, store.Event{Type: session.EventToolEnd,
				Payload: json.RawMessage(`{"call_id":"tail-call","result":"tail-result-marker"}`)})
		default:
			if i%2 == 0 {
				rows = append(rows, store.Event{Type: session.EventToolStart,
					Payload: json.RawMessage(`{"name":"bench_tool"}`)})
			} else {
				rows = append(rows, store.Event{Type: session.EventStatus,
					Payload: json.RawMessage(`{"phase":"running"}`)})
			}
		}
	}
	return rows
}

// benchSessionSnapshot is the Session benchmark's transcript: a user message,
// a Markdown answer with a heading, list, inline code and a highlighted code
// block, two finished tool calls and the usage footer — the render mix a real
// session frame draws.
func benchSessionSnapshot() *state.Snapshot {
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

// seedBenchAutomation gives the Automation benchmark a representative task
// list: enabled and disabled rows with different schedules.
func seedBenchAutomation(b *testing.B, app *App) {
	b.Helper()
	for _, task := range []store.Automation{
		{Title: "每日构建报告", Prompt: "总结昨天的主干提交与失败构建。", Cron: "@daily", Enabled: true},
		{Title: "依赖安全巡检", Prompt: "检查依赖更新与已知漏洞。", Cron: "@hourly", Enabled: false},
		{Title: "发布说明草稿", Prompt: "根据最近合并的 PR 起草发布说明。", Cron: "*/15 * * * *", Enabled: true},
	} {
		if _, err := app.Ctrl.Automations().Create(task); err != nil {
			b.Fatalf("create task %q: %v", task.Title, err)
		}
	}
}

// seedBenchSidebar gives the Sidebar benchmark a workspace tree worth drawing:
// several workspaces with sessions under each, grouped as the tree view.
func seedBenchSidebar(b *testing.B, app *App) {
	b.Helper()
	ctx := context.Background()
	for w := 0; w < 3; w++ {
		ws, err := app.Ctrl.CreateWorkspace(ctx, "bench workspace "+string(rune('A'+w)), b.TempDir())
		if err != nil {
			b.Fatalf("CreateWorkspace: %v", err)
		}
		for s := 0; s < 4; s++ {
			if _, err := app.Ctrl.CreateSessionInWorkspace(ctx, ws.ID, "bench session in workspace"); err != nil {
				b.Fatalf("CreateSessionInWorkspace: %v", err)
			}
		}
	}
	if err := app.Ctrl.Store().SetSetting(settingSessionsGroupBy, groupByTree); err != nil {
		b.Fatalf("set group-by: %v", err)
	}
	app.SetSnapshot(app.Ctrl.Snapshot())
}

// benchRender measures warm App.View renders at the two reference window
// sizes. Fixture setup is outside the timed loop; every iteration draws one
// full frame with ui.Render, the same entrypoint a window frame uses.
func benchRender(b *testing.B, app *App) {
	b.Helper()
	for _, size := range []struct {
		name string
		w, h int
	}{
		{"1600x1000", 1600, 1000},
		{"900x600", 900, 600},
	} {
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				img := ui.Render(app.View, size.w, size.h, 1)
				runtime.KeepAlive(img)
			}
		})
	}
}

// BenchmarkRenderHome measures the Home screen: hero, composer card and the
// sidebar.
func BenchmarkRenderHome(b *testing.B) {
	benchRender(b, newBenchApp(b, Home, nil))
}

// BenchmarkRenderSession measures the Session screen: header/tabs, the
// Markdown transcript, two tool cards, the composer and the usage footer.
func BenchmarkRenderSession(b *testing.B) {
	benchRender(b, newBenchApp(b, Session, benchSessionSnapshot()))
}

// BenchmarkRenderPlugins measures the Plugins page: two section cards with
// rows, native switches and the page header.
func BenchmarkRenderPlugins(b *testing.B) {
	benchRender(b, newBenchApp(b, Plugins, nil))
}

// BenchmarkRenderAutomation measures the Automation page: header, segmented
// filter, search field and three task cards.
func BenchmarkRenderAutomation(b *testing.B) {
	app := newBenchApp(b, Automation, nil)
	seedBenchAutomation(b, app)
	benchRender(b, app)
}

// BenchmarkRenderSettings measures the Settings overlay over its background
// screen: panel, navigation column and the settings body.
func BenchmarkRenderSettings(b *testing.B) {
	benchRender(b, newBenchApp(b, Settings, nil))
}

// seedBenchTrajectory gives the Trajectory benchmarks a long trace: synthetic
// events appended to the benchmark session, so the frame cost is the virtual
// list's built window rather than the whole history.
func seedBenchTrajectory(b *testing.B, app *App, rows int) {
	b.Helper()
	sessionID := app.Ctrl.CurrentSessionID()
	for _, event := range syntheticTrajectoryRows(rows) {
		if _, err := app.Ctrl.Store().AppendEvent(sessionID, event); err != nil {
			b.Fatalf("AppendEvent(%s): %v", event.Type, err)
		}
	}
	app.Language = LangZH
	app.uiState().tab = int(liveTrajectory)
	app.uiState().trajLoaded = false
}

// BenchmarkRenderTrajectory measures the Trajectory screen with a 1000-event
// trace: the cold ui.Render of a frame whose virtual list builds only the
// rows in view.
func BenchmarkRenderTrajectory(b *testing.B) {
	app := newBenchApp(b, Session, nil)
	seedBenchTrajectory(b, app, 1000)
	benchRender(b, app)
}

// BenchmarkRenderSidebar measures a sidebar-heavy frame: several workspaces
// with their sessions drawn as the workspace tree.
func BenchmarkRenderSidebar(b *testing.B) {
	app := newBenchApp(b, Home, nil)
	seedBenchSidebar(b, app)
	benchRender(b, app)
}

// BenchmarkWarmFrame measures the steady-state cost of one headless frame of
// the same screens: the tester and its runtime are built once, outside the
// timed loop, so every iteration pays only the build/layout/paint of a frame
// with warm caches (fonts, Markdown parse, element state). This is the number
// to compare against a 60fps budget; BenchmarkRender* above includes the
// one-time cost of starting a renderer, which is why it is larger.
func BenchmarkWarmFrame(b *testing.B) {
	cases := []struct {
		name  string
		build func(*testing.B) *App
	}{
		{"Home", func(b *testing.B) *App { return newBenchApp(b, Home, nil) }},
		{"Session", func(b *testing.B) *App { return newBenchApp(b, Session, benchSessionSnapshot()) }},
		{"Plugins", func(b *testing.B) *App { return newBenchApp(b, Plugins, nil) }},
		{"Automation", func(b *testing.B) *App {
			app := newBenchApp(b, Automation, nil)
			seedBenchAutomation(b, app)
			return app
		}},
		{"Settings", func(b *testing.B) *App { return newBenchApp(b, Settings, nil) }},
		{"Sidebar", func(b *testing.B) *App {
			app := newBenchApp(b, Home, nil)
			seedBenchSidebar(b, app)
			return app
		}},
		{"Trajectory", func(b *testing.B) *App {
			app := newBenchApp(b, Session, nil)
			seedBenchTrajectory(b, app, 1000)
			return app
		}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			app := c.build(b)
			for _, size := range []struct {
				name string
				w, h int
			}{
				{"1600x1000", 1600, 1000},
				{"900x600", 900, 600},
			} {
				b.Run(size.name, func(b *testing.B) {
					tt := ui.NewTester(app.View, size.w, size.h)
					tt.Frame()
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						tt.Frame()
					}
					runtime.KeepAlive(tt)
				})
			}
		})
	}
}
