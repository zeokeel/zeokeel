package shell

// visual_regression_test.go is ZeoKeel's opt-in, same-machine brand visual
// regression smoke check. It renders the key screens headlessly and compares
// them with the committed preview goldens in docs/design/preview.
//
// It is OFF by default: ordinary test runs and CI skip it, so it never slows
// them down and never fails across machines where font stacks, font rasterizers
// or DPI differ. It is the ZeoKeel-owned regression check, NOT a return of the
// retired DSH pixel-parity gate (ADR 0001): the goldens are our own brand
// previews, and the tool lives in this repository, not in a DSH comparison.
//
// Run it on a machine that generated (or can reproduce) the previews:
//
//	ZEOKEEL_VISUAL=1 go test -count=1 -run TestVisualRegression ./internal/shell
//
// Knobs:
//
//	ZEOKEEL_VISUAL_THRESHOLD  max share of differing pixels (default 0.02 = 2%;
//	                          values >= 1 are read as percent, e.g. 3 = 3%)
//	ZEOKEEL_VISUAL_DIFF_DIR   where failure artifacts go (default /tmp/zeokeel-visual)
//	ZEOKEEL_VISUAL_PIXEL_TOL  per-channel tolerance, 0-255 (default 0)
//
// Clock-bearing screens are compared too, with their clock labels masked: the
// fresh render tells us where the "HH:MM"/"HH:MM:SS"/date labels are, and those
// rectangles are ignored in both images. Covered: Home, Sidebar, Settings,
// Plugins, Automation, Trajectory, Session. Refresh the goldens with the
// preview tests (ZEOKEEL_UPDATE_PREVIEWS=1) or scripts/visual-check.sh --update;
// see docs/design/preview/README.md and docs/design/known-limitations.md §4.

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// visualClockPattern matches the time and date text that drifts between runs:
// HH:MM, HH:MM:SS, and YYYY-MM-DD (optional time), which covers message
// footers, trajectory event times and automation next/last-run timestamps.
var visualClockPattern = regexp.MustCompile(`\d{2}:\d{2}|\d{4}-\d{2}-\d{2}`)

// visualCase is one compared screen: the golden name prefix and how to build
// the live fixture that the preview test builds.
type visualCase struct {
	name   string
	screen Screen
	build  func(t *testing.T, dark bool) *App
	mask   bool // mask clock/date labels before comparing
}

// visualCases are the compared screens. Session is included but masked: its
// footer shows the wall-clock time. Sidebar reuses the Session screen with the
// sidebar-tree fixture the preview test builds.
func visualCases(t *testing.T) []visualCase {
	return []visualCase{
		{"home-native", Home, func(t *testing.T, dark bool) *App {
			return visualSimpleApp(t, Home, dark)
		}, false},
		{"sidebar-native", Session, visualSidebarApp, false},
		{"settings-native", Settings, func(t *testing.T, dark bool) *App {
			return visualSimpleApp(t, Settings, dark)
		}, false},
		{"plugins-native", Plugins, func(t *testing.T, dark bool) *App {
			return visualSimpleApp(t, Plugins, dark)
		}, false},
		{"automation-native", Automation, visualAutomationApp, true},
		{"trajectory-native", Session, func(t *testing.T, dark bool) *App {
			return trajectoryPreviewApp(t, dark)
		}, true},
		{"session-native", Session, visualSessionApp, true},
	}
}

// visualSimpleApp is the default live fixture for a screen: a fresh app in the
// product's default language with the requested appearance.
func visualSimpleApp(t *testing.T, screen Screen, dark bool) *App {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	if err := h.store.SetSetting(session.SettingTheme, visualTheme(dark)); err != nil {
		t.Fatalf("theme setting: %v", err)
	}
	h.app.Screen = screen
	return h.app
}

// visualSidebarApp mirrors the Sidebar preview fixture: several sessions in
// the open workspace, two folded workspaces, the newest session selected.
func visualSidebarApp(t *testing.T, dark bool) *App {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	first := h.ctrl.CurrentWorkspaceID()
	for _, title := range []string{"原生 UI 迁移计划", "Sidebar 原生化", "Settings 独立窗口", "发布前回归"} {
		if _, err := h.ctrl.CreateSession(ctx, title); err != nil {
			t.Fatalf("CreateSession %q: %v", title, err)
		}
	}
	if _, err := h.ctrl.CreateWorkspace(ctx, "ZeoKeel", t.TempDir()); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := h.ctrl.CreateWorkspace(ctx, "mygo", t.TempDir()); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := h.ctrl.SelectWorkspace(ctx, first); err != nil {
		t.Fatalf("SelectWorkspace: %v", err)
	}
	if sessions, err := h.ctrl.SessionsForWorkspace(ctx, first, false, false); err == nil && len(sessions) > 0 {
		if _, err := h.ctrl.OpenSession(ctx, sessions[len(sessions)-1].ID); err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
	}
	h.app.SetSnapshot(h.ctrl.Snapshot())
	h.app.Language = LangZH
	h.app.Screen = Session
	if err := h.store.SetSetting(session.SettingTheme, visualTheme(dark)); err != nil {
		t.Fatalf("theme setting: %v", err)
	}
	return h.app
}

// visualAutomationApp mirrors the Automation preview fixture: three persisted
// tasks with different schedules and states. The next-run timestamps drift
// with the clock, so this case is compared with masks.
func visualAutomationApp(t *testing.T, dark bool) *App {
	t.Helper()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangZH
	if err := h.store.SetSetting(session.SettingTheme, visualTheme(dark)); err != nil {
		t.Fatalf("theme setting: %v", err)
	}
	for _, task := range []store.Automation{
		{Title: "每日构建报告", Prompt: "总结昨天的主干提交与失败构建，生成一份简短报告。", Cron: "@daily", Enabled: true},
		{Title: "依赖安全巡检", Prompt: "检查依赖更新与已知漏洞，列出需要升级的模块。", Cron: "@hourly", Enabled: false},
		{Title: "发布说明草稿", Prompt: "根据最近合并的 PR 起草发布说明。", Cron: "*/15 * * * *", Enabled: true},
	} {
		if _, err := h.ctrl.Automations().Create(task); err != nil {
			t.Fatalf("create task %q: %v", task.Title, err)
		}
	}
	h.app.Screen = Automation
	return h.app
}

// visualSessionApp mirrors the Session preview fixture: one user message and
// one Markdown answer over a finished turn. The footer time drifts, so this
// case is compared with masks.
func visualSessionApp(t *testing.T, dark bool) *App {
	t.Helper()
	snap := sessionPreviewSnapshot()
	h := newLiveSnapshotApp(t, Session, snap)
	h.app.Language = LangZH
	// The preview gives the session a meaningful title instead of the
	// fixture's placeholder; the header and sidebar render it.
	id := h.ctrl.CurrentSessionID()
	if _, err := h.ctrl.RenameSession(context.Background(), id, "品牌主题与 Session 视觉"); err != nil {
		t.Fatalf("rename preview session: %v", err)
	}
	snap.SessionID = id
	if err := h.store.SetSetting(session.SettingTheme, visualTheme(dark)); err != nil {
		t.Fatalf("theme setting: %v", err)
	}
	h.app.SetSnapshot(snap)
	return h.app
}

// visualTheme is the appearance the previews are committed in.
func visualTheme(dark bool) string {
	if dark {
		return session.ThemeDark
	}
	return session.ThemeLight
}

// TestVisualRegression compares the key screens with the committed brand
// previews. It is skipped unless ZEOKEEL_VISUAL=1 so ordinary runs and CI stay
// fast and deterministic; see the package comment above for the knobs.
func TestVisualRegression(t *testing.T) {
	if os.Getenv("ZEOKEEL_VISUAL") != "1" {
		t.Skip("visual regression is opt-in: set ZEOKEEL_VISUAL=1 to run it (see docs/design/preview/README.md)")
	}
	threshold := visualThreshold(t)
	pixelTol := visualPixelTolerance(t)
	diffDir := os.Getenv("ZEOKEEL_VISUAL_DIFF_DIR")
	if diffDir == "" {
		diffDir = "/tmp/zeokeel-visual"
	}
	if err := os.MkdirAll(diffDir, 0o755); err != nil {
		t.Fatalf("create diff dir %s: %v", diffDir, err)
	}

	goldenDir := filepath.Join("..", "..", "docs", "design", "preview")
	start := time.Now()
	for _, c := range visualCases(t) {
		for _, dark := range []bool{false, true} {
			tone := "light"
			if dark {
				tone = "dark"
			}
			name := c.name + "-" + tone
			t.Run(name, func(t *testing.T) {
				app := c.build(t, dark)
				img, masks := visualRender(t, app, c.mask)

				goldenPath := filepath.Join(goldenDir, name+".png")
				want, err := readPreviewPNG(goldenPath)
				if err != nil {
					t.Fatalf("read golden %s: %v (run scripts/visual-check.sh --update or the preview tests with ZEOKEEL_UPDATE_PREVIEWS=1)", goldenPath, err)
				}
				if img.Bounds() != want.Bounds() {
					t.Fatalf("size %v, golden %s is %v", img.Bounds(), name, want.Bounds())
				}
				_, ratio := visualDiff(img, want, masks, pixelTol)
				if ratio > threshold {
					gotPath, diffPath := writeVisualFailure(t, diffDir, name, img, want)
					t.Fatalf("%s drifted: %.3f%% of pixels differ (> %.3f%%); got %s diff %s",
						name, ratio*100, threshold*100, gotPath, diffPath)
				}
				t.Logf("%s: %.3f%% differ (threshold %.3f%%)", name, ratio*100, threshold*100)
			})
		}
	}
	t.Logf("visual regression checked %d screens in %s", len(visualCases(t))*2, time.Since(start).Round(time.Millisecond))
}

// visualRender renders the app once exactly as the preview tests do — a fresh
// tester whose settle frames are part of NewTester — and returns the frame and
// the clock-label masks when the case asks for them.
func visualRender(t *testing.T, app *App, masked bool) (*image.RGBA, []image.Rectangle) {
	t.Helper()
	tt := ui.NewTester(app.View, 1600, 1000)
	img := tt.Image()
	if !masked {
		return img, nil
	}
	var masks []image.Rectangle
	for _, text := range uniqueStrings(tt.Texts()) {
		if !visualClockPattern.MatchString(text) {
			continue
		}
		if r, ok := tt.Find(text); ok {
			masks = append(masks, paddedRect(r, 3))
		}
	}
	return img, masks
}

// uniqueStrings returns the distinct non-empty strings of s, preserving order,
// so a repeated label is looked up once.
func uniqueStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// paddedRect pads a UI rect by pad DIPs on every side and converts it to the
// integer image rectangle the masks use.
func paddedRect(r ui.Rect, pad float32) image.Rectangle {
	x0 := int(r.X) - int(pad)
	y0 := int(r.Y) - int(pad)
	x1 := int(r.X+r.W) + int(pad)
	y1 := int(r.Y+r.H) + int(pad)
	return image.Rect(x0, y0, x1, y1)
}

// visualDiff counts the pixels that differ beyond tol outside the masks and
// returns them with their share of the compared area.
func visualDiff(got, want *image.RGBA, masks []image.Rectangle, tol int) (int, float64) {
	b := got.Bounds()
	if b != want.Bounds() {
		return 0, 1
	}
	masked := make([]bool, b.Dx()*b.Dy())
	for _, m := range masks {
		m = m.Intersect(b)
		for y := m.Min.Y; y < m.Max.Y; y++ {
			row := (y - b.Min.Y) * b.Dx()
			for x := m.Min.X; x < m.Max.X; x++ {
				masked[row+x-b.Min.X] = true
			}
		}
	}
	differing, counted := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := (y - b.Min.Y) * b.Dx()
		for x := b.Min.X; x < b.Max.X; x++ {
			if masked[row+x-b.Min.X] {
				continue
			}
			counted++
			c1, c2 := got.RGBAAt(x, y), want.RGBAAt(x, y)
			if channelDiff(c1.R, c2.R) > tol || channelDiff(c1.G, c2.G) > tol ||
				channelDiff(c1.B, c2.B) > tol || channelDiff(c1.A, c2.A) > tol {
				differing++
			}
		}
	}
	if counted == 0 {
		return 0, 1
	}
	return differing, float64(differing) / float64(counted)
}

func channelDiff(a, b uint8) int {
	if a > b {
		return int(a) - int(b)
	}
	return int(b) - int(a)
}

// visualThreshold parses ZEOKEEL_VISUAL_THRESHOLD: a ratio (0.02) or, when >= 1,
// a percent (2). The default 2% tolerates platform serif/AA differences while
// still catching layout or color regressions.
func visualThreshold(t *testing.T) float64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("ZEOKEEL_VISUAL_THRESHOLD"))
	if raw == "" {
		return 0.02
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		t.Fatalf("ZEOKEEL_VISUAL_THRESHOLD=%q is not a positive number", raw)
	}
	if v >= 1 {
		v /= 100
	}
	return v
}

// visualPixelTolerance parses the optional per-channel tolerance (0-255).
func visualPixelTolerance(t *testing.T) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("ZEOKEEL_VISUAL_PIXEL_TOL"))
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 || v > 255 {
		t.Fatalf("ZEOKEEL_VISUAL_PIXEL_TOL=%q is not 0-255", raw)
	}
	return v
}

// readPreviewPNG loads one committed preview golden.
func readPreviewPNG(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		// Re-encode into RGBA so the pixel comparison is uniform.
		b := img.Bounds()
		rgba = image.NewRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				rgba.Set(x, y, img.At(x, y))
			}
		}
	}
	return rgba, nil
}

// writeVisualFailure writes the fresh frame and a red-highlighted difference
// image (differing pixels red over a dimmed frame) so a failing run can be
// reviewed without re-running the test.
func writeVisualFailure(t *testing.T, dir, name string, got, want *image.RGBA) (string, string) {
	t.Helper()
	gotPath := filepath.Join(dir, name+"-got.png")
	diffPath := filepath.Join(dir, name+"-diff.png")
	if err := writeRGBA(gotPath, got); err != nil {
		t.Fatalf("write %s: %v", gotPath, err)
	}
	diff := image.NewRGBA(got.Bounds())
	red := color.RGBA{R: 0xDC, G: 0x26, B: 0x26, A: 0xFF}
	for y := got.Bounds().Min.Y; y < got.Bounds().Max.Y; y++ {
		for x := got.Bounds().Min.X; x < got.Bounds().Max.X; x++ {
			c := got.RGBAAt(x, y)
			c2 := want.RGBAAt(x, y)
			if channelDiff(c.R, c2.R) > 0 || channelDiff(c.G, c2.G) > 0 ||
				channelDiff(c.B, c2.B) > 0 || channelDiff(c.A, c2.A) > 0 {
				diff.Set(x, y, red)
				continue
			}
			diff.Set(x, y, color.RGBA{R: c.R / 3, G: c.G / 3, B: c.B / 3, A: 0xFF})
		}
	}
	if err := writeRGBA(diffPath, diff); err != nil {
		t.Fatalf("write %s: %v", diffPath, err)
	}
	return gotPath, diffPath
}

// writeRGBA encodes img to path.
func writeRGBA(path string, img *image.RGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
