package regression

// UD-01 cross-wave integration (2026-11 audit, wave F): the first-launch landing
// (zero sessions, Home + sidebar) keeps AP-20's ≤1 accent-filled primary per
// screen. The sidebar's 新对话 is the one primary; Home's nextSteps 新对话 was
// demoted to a secondary (wave D), so the two same-named buttons no longer
// compete. Promoted from audit/probes/ui/p9_design_test.go (pixel-sampled).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/modules/appinfo"
	homeui "github.com/zeokeel/zeokeel/internal/ui/home"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
	sidebarui "github.com/zeokeel/zeokeel/internal/ui/sidebar"
	toastsui "github.com/zeokeel/zeokeel/internal/ui/toasts"
)

// landingPixel samples one frame pixel as 8-bit RGBA.
func landingPixel(tester *ui.Tester, x, y int) [4]uint8 {
	img := tester.Image()
	r, g, b, a := img.At(x, y).RGBA()
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// TestLandingSinglePrimaryWholeShell boots the shell with Home + sidebar and zero
// sessions and counts the accent-filled 新对话 primaries across the WHOLE frame.
func TestLandingSinglePrimaryWholeShell(t *testing.T) {
	h := newBareHarness(t)
	h.ui.SetKernelContext(h.ctx)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat())) // no sessions
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(h.ui.Notifier()))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	activate(t, h.ctx, toastsui.Module(), toastsui.Config{})
	activate(t, h.ctx, appinfo.Module(), appinfo.Config{
		Name: "ZeoKeel", Version: "0.1.0", Commit: "test", BuildTime: "2026-01-01T00:00:00Z",
	})
	activate(t, h.ctx, homeui.Module(), homeui.Config{})
	activate(t, h.ctx, sidebarui.Module(), sidebarui.Config{})
	tester := h.tester(1280, 800)
	tester.Frame()
	if err := h.ui.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tester.Frame()
	tester.Frame()
	requireHasText(t, tester, "还没有会话")

	accent := design.Light().Accent
	primaryCount := 0
	buttonCount := 0
	for _, info := range tester.AccessAll() {
		if info.Role != ui.RoleButton || info.Name != "新对话" {
			continue
		}
		buttonCount++
		fill := landingPixel(tester, int(info.Bounds.X)+6, int(info.Bounds.Y+info.Bounds.H/2))
		if fill[0] == accent.R && fill[1] == accent.G && fill[2] == accent.B {
			primaryCount++
		}
	}
	if buttonCount == 0 {
		t.Fatalf("no 新对话 button on the landing; texts = %v", tester.Texts())
	}
	if primaryCount != 1 {
		t.Fatalf("UD-01: %d accent-filled primary 新对话 buttons share the landing (AP-20 wants exactly 1); texts = %v",
			primaryCount, tester.Texts())
	}
}
