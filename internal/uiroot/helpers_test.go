package uiroot

import (
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// testConfig is the deterministic host configuration the headless tests use.
func testConfig() Config {
	return Config{AppName: "ZeoKeelTest", Dark: false, Language: "zh"}
}

// newTestRoot creates a Root for a headless test. No invalidator is installed:
// the tester drives every frame explicitly, which keeps frame counts exact.
func newTestRoot(t *testing.T) *Root {
	t.Helper()
	return New(testConfig())
}

// newTestTester starts a headless tester over root.View. Reduce Motion is on so
// animated surfaces (modal/palette/disclosure) settle at once: the host tests
// drive frames explicitly and assert final states, not motion.
func newTestTester(t *testing.T, r *Root, width, height int) *ui.Tester {
	t.Helper()
	tt := ui.NewTesterWith(r.View, width, height, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})
	if tt == nil {
		t.Fatal("ui.NewTesterWith returned nil")
	}
	return tt
}

// mustScreen registers a screen and registers its unregister function with
// t.Cleanup so a failing test still unwinds the registry.
func mustScreen(t *testing.T, r *Root, spec contracts.ScreenSpec) func() {
	t.Helper()
	unregister, err := r.Runtime().RegisterScreen(spec)
	if err != nil {
		t.Fatalf("RegisterScreen(%q): %v", spec.ID, err)
	}
	t.Cleanup(func() { unregister() })
	return unregister
}

// mustSlot registers a slot item and unwinds it at the end of the test.
func mustSlot(t *testing.T, r *Root, item contracts.SlotItem) func() {
	t.Helper()
	unregister, err := r.Runtime().RegisterSlotItem(item)
	if err != nil {
		t.Fatalf("RegisterSlotItem(%q/%q): %v", item.Slot, item.ID, err)
	}
	t.Cleanup(func() { unregister() })
	return unregister
}

// mustPage registers a settings page and unwinds it at the end of the test.
func mustPage(t *testing.T, r *Root, page contracts.SettingsPageSpec) func() {
	t.Helper()
	unregister, err := r.Runtime().RegisterSettingsPage(page)
	if err != nil {
		t.Fatalf("RegisterSettingsPage(%q): %v", page.ID, err)
	}
	t.Cleanup(func() { unregister() })
	return unregister
}

// mustToolRenderer registers a tool renderer and unwinds it at the end of the
// test.
func mustToolRenderer(t *testing.T, r *Root, renderer contracts.ToolRenderer) func() {
	t.Helper()
	unregister, err := r.Runtime().RegisterToolRenderer(renderer)
	if err != nil {
		t.Fatalf("RegisterToolRenderer(%q/%q): %v", renderer.Tool, renderer.ID, err)
	}
	t.Cleanup(func() { unregister() })
	return unregister
}

// mustCommand registers a command and unwinds it at the end of the test.
func mustCommand(t *testing.T, r *Root, cmd contracts.CommandSpec) func() {
	t.Helper()
	unregister, err := r.Runtime().RegisterCommand(cmd)
	if err != nil {
		t.Fatalf("RegisterCommand(%q): %v", cmd.ID, err)
	}
	t.Cleanup(func() { unregister() })
	return unregister
}

// textScreen returns a ScreenSpec whose body shows one marker text.
func textScreen(id, title, text string, order int) contracts.ScreenSpec {
	return contracts.ScreenSpec{
		ID:    id,
		Title: title,
		Order: order,
		Build: func(c *ui.Context) { ui.Text(c, text) },
	}
}

// textSlotItem returns a SlotItem whose contribution shows one marker text.
func textSlotItem(slot, id, text string, order int) contracts.SlotItem {
	return contracts.SlotItem{
		Slot:  slot,
		ID:    id,
		Order: order,
		Build: func(c *ui.Context) { ui.Text(c, text) },
	}
}

// textToolRenderer returns a ToolRenderer whose card shows one marker text.
func textToolRenderer(tool, id, text string, order int) contracts.ToolRenderer {
	return contracts.ToolRenderer{
		Tool:  tool,
		ID:    id,
		Order: order,
		Build: func(c *ui.Context, call contracts.ToolCall) { ui.Text(c, text) },
	}
}

// indexOfText returns the position of the first label exactly matching text,
// or -1.
func indexOfText(texts []string, text string) int {
	for i, got := range texts {
		if got == text {
			return i
		}
	}
	return -1
}

// requireHasText asserts the last frame shows a label containing text.
func requireHasText(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if !tt.HasText(text) {
		t.Fatalf("frame does not show %q; texts: %v", text, tt.Texts())
	}
}

// requireNoText asserts the last frame shows no label containing text.
func requireNoText(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if tt.HasText(text) {
		t.Fatalf("frame unexpectedly shows %q; texts: %v", text, tt.Texts())
	}
}

// requireTextBefore asserts ordered texts appear in this order in the frame.
func requireTextBefore(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	all := tt.Texts()
	last := -1
	lastText := ""
	for _, want := range texts {
		idx := indexOfText(all, want)
		if idx < 0 {
			t.Fatalf("frame does not show %q; texts: %v", want, all)
		}
		if idx <= last {
			t.Fatalf("text order wrong: %q at %d must come after %q at %d; texts: %v",
				want, idx, lastText, last, all)
		}
		last, lastText = idx, want
	}
}

// requireRegionAbove asserts a's text sits above b's text.
func requireRegionAbove(t *testing.T, tt *ui.Tester, above, below string) {
	t.Helper()
	a, ok := tt.Find(above)
	if !ok {
		t.Fatalf("frame does not show %q; texts: %v", above, tt.Texts())
	}
	b, ok := tt.Find(below)
	if !ok {
		t.Fatalf("frame does not show %q; texts: %v", below, tt.Texts())
	}
	if a.Y+a.H > b.Y {
		t.Fatalf("%q (y=%v..%v) is not above %q (y=%v..%v)", above, a.Y, a.Y+a.H, below, b.Y, b.Y+b.H)
	}
}

// requireRegionLeftOf asserts a's text sits left of b's text.
func requireRegionLeftOf(t *testing.T, tt *ui.Tester, left, right string) {
	t.Helper()
	a, ok := tt.Find(left)
	if !ok {
		t.Fatalf("frame does not show %q; texts: %v", left, tt.Texts())
	}
	b, ok := tt.Find(right)
	if !ok {
		t.Fatalf("frame does not show %q; texts: %v", right, tt.Texts())
	}
	if a.X+a.W > b.X {
		t.Fatalf("%q (x=%v..%v) is not left of %q (x=%v..%v)", left, a.X, a.X+a.W, right, b.X, b.X+b.W)
	}
}

// requireContains fails unless err is non-nil and its message contains want.
func requireContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

// counter is an atomically maintained test state shared across the UI thread
// and test/worker goroutines.
type counter struct{ v atomic.Int64 }

func (c *counter) Inc()        { c.v.Add(1) }
func (c *counter) Load() int64 { return c.v.Load() }
func (c *counter) Set(n int64) { c.v.Store(n) }

// equalStrings reports whether two string slices hold the same elements in the
// same order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// itoa formats n for UI marker text.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestMain keeps expected module panic logs (the host contains them and writes
// them through the standard logger) out of the test output.
func TestMain(m *testing.M) {
	if osrunner.MaybeRunHelper(os.Args) {
		return
	}
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}
