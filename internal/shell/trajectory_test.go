package shell

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
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
