package uikit_test

// 2026-11 UI review P3: the compact tool row's status is a fixed-width column (a
// reserved spinner slot + a fixed status-text area), so the params/preview left
// edge is identical across the four states — the running spinner and a
// shorter/longer status word must not shift it (the old 125–157 DIP jitter).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func toolRowStateTester(t *testing.T, status string, running bool, kind uikit.StatusKind, locale string) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		tk := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key: "t1", Icon: icons.Terminal(), Name: "运行命令", RawName: "bash",
			Status: status, StatusColor: tk.LabelSecondary, Running: running,
			State: kind, Preview: "npm test -- --watch",
			Sections: []uikit.ToolPanelSection{{Label: "x", Text: "y"}},
		})
	}, 760, 300)
	tester.SetLocale(locale)
	tester.Frame()
	return tester
}

func TestToolRowStatusColumnKeepsParamsAligned(t *testing.T) {
	states := []struct {
		status  string
		running bool
		kind    uikit.StatusKind
	}{
		{"运行中", true, uikit.StatusRunning},
		{"完成", false, uikit.StatusSuccess},
		{"失败", false, uikit.StatusError},
		{"已停止", false, uikit.StatusCancelled},
	}
	firstX := float32(-1)
	for _, s := range states {
		tester := toolRowStateTester(t, s.status, s.running, s.kind, "zh-Hans")
		preview, ok := tester.Find("npm test -- --watch")
		if !ok {
			t.Fatalf("state %q: preview missing; texts = %v", s.status, tester.Texts())
		}
		if !tester.HasText(s.status) {
			t.Fatalf("state %q: status word truncated/missing; texts = %v", s.status, tester.Texts())
		}
		if firstX < 0 {
			firstX = preview.X
		}
		if preview.X != firstX {
			t.Fatalf("state %q params x=%.1f, want %.1f (one fixed left edge across states)",
				s.status, preview.X, firstX)
		}
	}
}

// TestToolRowStatusColumnFitsEnglishLabels guards the fixed text area against the
// longer English status words: they must read in full, not ellipsize.
func TestToolRowStatusColumnFitsEnglishLabels(t *testing.T) {
	for _, s := range []struct {
		status  string
		running bool
		kind    uikit.StatusKind
	}{
		{"Running", true, uikit.StatusRunning},
		{"Completed", false, uikit.StatusSuccess},
		{"Cancelled", false, uikit.StatusCancelled},
	} {
		tester := toolRowStateTester(t, s.status, s.running, s.kind, "en")
		if !tester.HasText(s.status) {
			t.Fatalf("English status %q truncated; texts = %v", s.status, tester.Texts())
		}
	}
}
