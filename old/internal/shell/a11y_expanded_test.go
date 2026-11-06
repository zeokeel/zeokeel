package shell

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/ui"
)

// accessExpandedState is mygo's platform.AccessExpanded bit (the sixth
// AccessStates bit), read from the tester's accessibility tree below.
const accessExpandedState = 1 << 5

// testerAccessNodeInfo is one node of the tester's accessibility tree, the
// part of platform.AccessNode the shell tests assert on.
type testerAccessNodeInfo struct {
	Role   uint8
	Label  string
	Value  string
	States uint64
	// Parent is the index of the parent node in the slice, -1 at the top.
	Parent int
}

// testerAccessNodes returns the tester's last accessibility tree. The Tester
// keeps the tree private, so the test reaches it through the same struct
// layout the ui package uses; this is test-only introspection of the
// dependency, and the build pins mygo v0.7.4.
func testerAccessNodes(t *testing.T, tt *ui.Tester) []testerAccessNodeInfo {
	t.Helper()
	// The headless Tester only describes frames for assistive technology
	// after a platform.AccessibilityOn event, which it does not expose. Set
	// the engine's access flag and render one frame, exactly as that event
	// does.
	rv := reflect.ValueOf(tt).Elem().FieldByName("rt")
	if !rv.IsValid() {
		t.Fatal("ui.Tester has no rt field; update the accessibility test")
	}
	rt := reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem()
	flag := rt.Elem().FieldByName("access")
	if flag.Kind() != reflect.Bool {
		t.Fatal("ui engine has no access flag; update the accessibility test")
	}
	*(*bool)(unsafe.Pointer(flag.UnsafeAddr())) = true
	tt.Frame()

	v := reflect.ValueOf(tt).Elem().FieldByName("h")
	if !v.IsValid() {
		t.Fatal("ui.Tester has no h field; update the accessibility test")
	}
	h := reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	access := h.Elem().FieldByName("access")
	if !access.IsValid() || access.IsNil() {
		t.Fatal("ui.Tester did not build an accessibility tree")
	}
	tree := reflect.NewAt(access.Type(), unsafe.Pointer(access.UnsafeAddr())).Elem().Elem()
	nodes := tree.FieldByName("Nodes")
	out := make([]testerAccessNodeInfo, nodes.Len())
	for i := 0; i < nodes.Len(); i++ {
		n := nodes.Index(i)
		out[i] = testerAccessNodeInfo{
			Role:   uint8(n.FieldByName("Role").Uint()),
			Label:  n.FieldByName("Label").String(),
			Value:  n.FieldByName("Value").String(),
			States: n.FieldByName("States").Uint(),
			Parent: int(n.FieldByName("Parent").Int()),
		}
	}
	return out
}

// testerAccessStates returns label -> OR of every accessibility node's states
// with that label in the tester's last frame.
func testerAccessStates(t *testing.T, tt *ui.Tester) map[string]uint64 {
	t.Helper()
	nodes := testerAccessNodes(t, tt)
	out := make(map[string]uint64, len(nodes))
	for _, n := range nodes {
		if n.Label == "" {
			continue
		}
		out[n.Label] |= n.States
	}
	return out
}

// assertExpanded checks that the popover anchor called Expanded(open): before
// opening no node with the label carries AccessExpanded, after opening one
// does. A missing label fails rather than silently passing.
func assertExpanded(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	before, ok := testerAccessStates(t, tt)[label]
	if !ok {
		t.Fatalf("accessibility tree has no node labeled %q", label)
	}
	if before&accessExpandedState != 0 {
		t.Fatalf("%q is expanded before it was opened", label)
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("open %q: %v", label, err)
	}
	tt.Frame()
	after, ok := testerAccessStates(t, tt)[label]
	if !ok {
		t.Fatalf("accessibility tree lost %q after opening it", label)
	}
	if after&accessExpandedState == 0 {
		t.Fatalf("%q did not expose AccessExpanded while its popover is open", label)
	}
}

// TestPopoverAnchorsExposeExpanded covers every popover opener that used to
// omit the expanded state: the session mode popup, the session More actions,
// the collapsed-rail More, the right-panel New tab picker and the Settings
// font controls.
func TestPopoverAnchorsExposeExpanded(t *testing.T) {
	newApp := func(t *testing.T) *liveApp {
		h := newLiveApp(t, &shellProvider{})
		h.app.Language = LangEN
		return h
	}

	t.Run("session mode", func(t *testing.T) {
		h := newApp(t)
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		assertExpanded(t, tt, "Standard mode")
	})

	t.Run("session more", func(t *testing.T) {
		h := newApp(t)
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		assertExpanded(t, tt, "More actions")
	})

	t.Run("collapsed rail more", func(t *testing.T) {
		h := newApp(t)
		h.app.Screen = Home
		h.app.sidebarCollapsed = true
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		assertExpanded(t, tt, "More")
	})

	t.Run("right new tab", func(t *testing.T) {
		h := newApp(t)
		h.app.Screen = Session
		h.app.rightOpen = true
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		assertExpanded(t, tt, "New tab")
	})

	t.Run("font size value", func(t *testing.T) {
		h := newApp(t)
		h.app.Screen = Settings
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		assertExpanded(t, tt, fontSizeLabel(h.app.currentSettings().FontSize)+" px")
	})

	t.Run("more font settings", func(t *testing.T) {
		h := newApp(t)
		h.app.Screen = Settings
		tt := ui.NewTester(h.app.View, 1600, 1000)
		tt.Frame()
		assertExpanded(t, tt, "More font settings")
	})
}
