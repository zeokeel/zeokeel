package shell

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// trajectoryPreviewApp builds the live Session screen on its Trajectory tab
// after one completed turn with a tool call, so the preview shows the whole
// event-trace rhythm: user message, status, tool start/end, answer and usage.
func trajectoryPreviewApp(t *testing.T, dark bool) *App {
	t.Helper()
	provider := &m6Provider{steps: []m6Step{
		{tool: "echo", args: `{"path":"README.md"}`},
		{text: "README 已更新，测试全部通过。"},
	}}
	app := m6LiveApp(t, provider, &m6EchoTool{})
	m6Submit(t, app, "看一下 README 并跑一遍测试")
	theme := session.ThemeLight
	if dark {
		theme = session.ThemeDark
	}
	if err := app.Ctrl.Store().SetSetting(session.SettingTheme, theme); err != nil {
		t.Fatalf("theme setting: %v", err)
	}
	app.Language = LangZH
	app.uiState().tab = int(liveTrajectory)
	// Open the first tool row's raw payload so the preview shows the tinted
	// detail block as well as the collapsed rows.
	if snap := app.Snapshot(); snap != nil {
		events, err := app.trajectoryEvents(snap)
		if err != nil {
			t.Fatalf("trajectory events: %v", err)
		}
		for _, entry := range buildTrajectory(events) {
			if entry.Type == session.EventToolStart {
				app.uiState().expandedRaw[entry.Seq] = true
				break
			}
		}
	}
	return app
}

// TestTrajectoryBrandTimeline pins the brand trajectory visuals that the
// screen's behavior tests depend on: the localized header/count row, the
// clickable labeled event rows and the raw detail block.
func TestTrajectoryBrandTimeline(t *testing.T) {
	app := trajectoryPreviewApp(t, false)
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{
		"轨迹", "条记录", "消息 · user", "助手", "工具开始", "工具结束", "用量",
	} {
		if !tt.HasText(want) {
			t.Fatalf("trajectory missing %q: %v", want, tt.Texts())
		}
	}
	// The open tool_start row shows its raw payload inline.
	if !tt.HasText(`"call_id"`) {
		t.Fatalf("open trajectory row has no raw payload: %v", tt.Texts())
	}
}

// TestRenderTrajectoryNativePreview renders the Trajectory tab for brand
// review: light and dark at the 1600x1000 design size. Ordinary runs write to
// the test's temp dir; set ZEOKEEL_UPDATE_PREVIEWS=1 to refresh
// docs/design/preview.
func TestRenderTrajectoryNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{"light", false},
		{"dark", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := trajectoryPreviewApp(t, tc.dark)
			tt := ui.NewTester(app.View, 1600, 1000)
			tt.Frame()
			path := filepath.Join(dir, "trajectory-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
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

// appendTrajectoryRows appends events to the app's current session and drops
// the Trajectory pane's event cache so the next frame reads them.
func appendTrajectoryRows(t *testing.T, app *App, events []store.Event) {
	t.Helper()
	sessionID := app.Ctrl.CurrentSessionID()
	if sessionID == "" {
		t.Fatal("no current session")
	}
	for _, event := range events {
		if _, err := app.Ctrl.Store().AppendEvent(sessionID, event); err != nil {
			t.Fatalf("AppendEvent(%s): %v", event.Type, err)
		}
	}
	st := app.uiState()
	st.trajLoaded, st.trajEvents, st.trajErr = false, nil, nil
}

// longTrajectoryApp is a live Session on its Trajectory tab with a synthetic
// trace of total rows.
func longTrajectoryApp(t *testing.T, total int) *App {
	t.Helper()
	app := m6LiveApp(t, &m6Provider{})
	app.Language = LangEN
	app.uiState().tab = int(liveTrajectory)
	appendTrajectoryRows(t, app, syntheticTrajectoryRows(total))
	return app
}

// TestTrajectoryVirtualizedLongTrace covers the v1.1 virtualization: a 1200
// row trace renders the header count and the first rows, builds only the rows
// in view (the tail row is absent until scrolled to), and the list's own
// ScrollToEnd brings the tail into the built window.
func TestTrajectoryVirtualizedLongTrace(t *testing.T) {
	const total = 1200
	app := longTrajectoryApp(t, total)
	events, err := app.Ctrl.Events(context.Background(), app.Ctrl.CurrentSessionID())
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	entries := buildTrajectory(events)
	if len(entries) != total {
		t.Fatalf("entries = %d, want %d", len(entries), total)
	}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	// The header still counts the whole trace, not just the built rows.
	if !tt.HasText(fmt.Sprintf("%d entries", len(entries))) {
		t.Fatalf("trajectory header lost the full count %d: %v", len(entries), tt.Texts())
	}
	// The oldest rows are on screen...
	if !tt.HasText("tool_end · head-call") || !tt.HasText("head-result-marker") {
		t.Fatalf("first trajectory rows are missing: %v", tt.Texts())
	}
	// ...and the tail is not: the list built only the window it shows.
	if tt.HasText("tail-result-marker") || tt.HasText("tool_end · tail-call") {
		t.Fatalf("the whole 1200-row trace was built before scrolling: %v", tt.Texts())
	}
	if got := len(tt.Texts()); got > 300 {
		t.Fatalf("built texts = %d, want a bounded visible window for %d rows", got, total)
	}
	// The list reports the same bound directly: a screenful of rows, not the
	// 1200-event history.
	if first, last := app.uiState().trajList.Visible(); last-first+1 <= 0 || last-first+1 > 80 {
		t.Fatalf("visible rows = %d..%d, want a bounded window", first, last)
	}

	// Jump to the end through the ListState the pane owns: the frame then
	// builds the tail and drops the head.
	app.uiState().trajList.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("tool_end · tail-call") || !tt.HasText("tail-result-marker") {
		t.Fatalf("ScrollToEnd did not build the tail rows: %v", tt.Texts())
	}
	if tt.HasText("head-result-marker") {
		t.Fatalf("the head row is still built after scrolling to the end: %v", tt.Texts())
	}
}

// TestTrajectoryVirtualizedInteractions covers the behavior the virtual list
// must keep: wheel scrolling moves the window, clicking a row opens its raw
// payload, the same row keeps its expansion state by event id when rows are
// appended, and Enter/Space on the focused row toggles it as the native
// ButtonBase did before virtualization.
func TestTrajectoryVirtualizedInteractions(t *testing.T) {
	app := longTrajectoryApp(t, 400)
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	// Clicking the first row opens its raw payload inline.
	if err := tt.Click("tool_end · head-call"); err != nil {
		t.Fatalf("click head trajectory row: %v", err)
	}
	tt.Frame()
	if !tt.HasText(`"head-call"`) {
		t.Fatalf("click did not open the raw payload: %v", tt.Texts())
	}
	// The row is a native focusable control with its accessible name, as in
	// the virtual list's predecessors.
	if !tt.Focused("tool_end · head-call") {
		t.Fatalf("clicked trajectory row is not the focused control: %v", tt.Texts())
	}
	// The expanded state is keyed by the event sequence, so appending rows
	// leaves this row open and the list keeps its anchor at the top.
	headSeq := int64(1)
	if !app.uiState().expandedRaw[headSeq] {
		t.Fatalf("expanded raw state is not keyed by the event id %d: %v", headSeq, app.uiState().expandedRaw)
	}
	appendTrajectoryRows(t, app, syntheticTrajectoryRows(100))
	tt.Frame()
	if !tt.HasText(`"head-call"`) {
		t.Fatalf("appended rows dropped the expanded head row: %v", tt.Texts())
	}

	// Enter on the focused row toggles it closed, as before.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tt.HasText(`"head-call"`) {
		t.Fatalf("Enter on the focused row did not close it: %v", tt.Texts())
	}

	// A wheel scroll over the list moves the window and the rows leave the
	// built set: see TestTrajectoryVirtualizedWheelScroll for the pointer
	// path on its own.
}

// TestTrajectoryVirtualizedWheelScroll covers pointer scrolling of the virtual
// list, without a focused row pinning the first rows: the wheel notches move
// the window, the oldest rows leave the built set entirely and the built set
// stays bounded.
func TestTrajectoryVirtualizedWheelScroll(t *testing.T) {
	app := longTrajectoryApp(t, 400)
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	marker, ok := tt.Find("head-result-marker")
	if !ok {
		t.Fatalf("head marker row not found: %v", tt.Texts())
	}
	x, y := marker.X+marker.W/2, marker.Y+marker.H/2
	tt.Move(x, y)
	for i := 0; i < 12 && tt.HasText("head-result-marker"); i++ {
		tt.Scroll(x, y, 0, 200)
		tt.Frame()
	}
	if tt.HasText("head-result-marker") {
		t.Fatalf("wheel did not scroll the virtual list: %v", tt.Texts())
	}
	if !tt.HasText("tool_start · bench_tool") && !tt.HasText("status · running") {
		t.Fatalf("wheel left the list empty: %v", tt.Texts())
	}
	if got := len(tt.Texts()); got > 300 {
		t.Fatalf("built texts = %d after scrolling a 400-row trace", got)
	}
}
