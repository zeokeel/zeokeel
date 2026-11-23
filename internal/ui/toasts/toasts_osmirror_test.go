package toasts

// End-to-end OS-mirror localization (2026-11-17 UI review P3): the module
// installs its render-time body localizer on the desktop notifier bridge, so
// an approval notice whose payload is a stable tool id reaches the OS
// notification center as the friendly localized name — in the language active
// at delivery, not a name frozen at generation time.

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// TestOSMirrorApprovalBodyLocalizes drives the real desktop notifier (not the
// fake): the module's optional SetLocalizer hook must translate the tool-id
// payload for the OS sink, and a language switch must change the copy the next
// notification mirrors.
func TestOSMirrorApprovalBodyLocalizes(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: "zh"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, uinative.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, uiRoot.Notifier())
	if _, err := uiRoot.Runtime().RegisterScreen(uinative.ScreenSpec{
		ID: "home", Title: "首页",
		Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home screen: %v", err)
	}

	var mu sync.Mutex
	var bodies []string
	uiRoot.SetOSNotifier(func(_, _, body string) error {
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		return nil
	})
	activate(t, ctx, Module(), Config{})
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()

	waitForBodies := func(want int) []string {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(bodies)
			mu.Unlock()
			if n >= want {
				mu.Lock()
				defer mu.Unlock()
				return append([]string(nil), bodies...)
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("OS mirror delivered %d bodies, want %d", len(bodies), want)
		return nil
	}

	// The generation-time text carries the stable id; the OS body must read
	// the friendly Chinese name, not "bash".
	uiRoot.Notifier().NotifySystemFor(contracts.NotifyWarning, "审批等待：bash", "sess-1")
	tester.Frame()
	if got := waitForBodies(1)[0]; got != "审批等待：运行命令" {
		t.Fatalf("zh OS mirror body = %q, want the localized tool name", got)
	}
	if !tester.HasText("审批等待：运行命令") {
		t.Fatalf("in-app toast does not show the localized name; texts = %v", tester.Texts())
	}

	// After the language switch the next mirror reads English — the same
	// stable-id text, localized at delivery time.
	uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	uiRoot.Notifier().NotifySystemFor(contracts.NotifyWarning, "审批等待：bash", "sess-1")
	tester.Frame()
	if got := waitForBodies(2)[1]; got != "Approval waiting: Run command" {
		t.Fatalf("en OS mirror body = %q, want the English tool name", got)
	}
}
