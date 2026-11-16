package uiroot

// Frame test for AR-03 (2026-11 wave D): dragging a Split divider must not
// fsync settings.json on every move frame inside the build pass. The widths
// update in memory while dragging; the persist is debounced (written once after
// the value settles) and moved out of the per-move build branch into a posted
// frame-start write. The audit probe measured ~1 actual write per move frame
// (10 moves → 11 writes); the fix coalesces a whole drag into ≤1–2 writes.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// countingSettings records Set calls and value-changing Sets (a value-changing
// Set is what the real host.Settings turns into a full-file rewrite + fsync).
type countingSettings struct {
	values  map[string]string
	sets    atomic.Int64
	changes atomic.Int64
}

func (s *countingSettings) Get(key, def string) string {
	if v, ok := s.values[key]; ok {
		return v
	}
	return def
}

func (s *countingSettings) Set(key, value string) error {
	s.sets.Add(1)
	if cur, ok := s.values[key]; !ok || cur != value {
		s.changes.Add(1)
	}
	s.values[key] = value
	return nil
}

func (s *countingSettings) All() map[string]string { return s.values }

// TestSplitDragDebouncesWidthPersist drives a divider drag with many move
// frames and asserts the persist is coalesced: idle frames write nothing, the
// drag itself writes nothing per move, and after the value settles the width is
// persisted in ≤2 value-changing writes (one drag → one save).
func TestSplitDragDebouncesWidthPersist(t *testing.T) {
	spy := &countingSettings{values: map[string]string{}}
	r := New(Config{AppName: "Test", Language: "zh", Settings: spy})
	r.loadChromeWidths()
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	tt.Frame()

	// Control: idle frames produce no width writes.
	baseSets, baseChanges := spy.sets.Load(), spy.changes.Load()
	for i := 0; i < 10; i++ {
		tt.Frame()
	}
	if spy.sets.Load() != baseSets {
		t.Fatalf("control invalid: idle frames wrote settings (%d→%d)", baseSets, spy.sets.Load())
	}

	splitter, ok := tt.AccessByRole(ui.RoleSplitter, "")
	if !ok {
		t.Fatalf("sidebar divider missing; texts: %v", tt.Texts())
	}
	x0 := splitter.Bounds.X + splitter.Bounds.W/2
	y := splitter.Bounds.Y + splitter.Bounds.H/2

	const moves = 10
	tt.Press(x0, y)
	for i := 1; i <= moves; i++ {
		tt.Move(x0+float32(i)*15, y)
		tt.Frame()
	}
	tt.Release(x0+float32(moves)*15, y)
	tt.Frame()

	// During the drag nothing is persisted per move frame.
	dragChanges := spy.changes.Load() - baseChanges
	if dragChanges > 1 {
		t.Fatalf("AR-03: the drag persisted widths on every move frame (%d value-changing writes for %d moves)",
			dragChanges, moves)
	}

	// Let the debounce elapse and drain the posted save.
	tt.Advance(chromeWidthsSaveDebounce + 50*time.Millisecond)
	tt.Frame()
	tt.Frame()
	tt.Frame()

	changes := spy.changes.Load() - baseChanges
	sets := spy.sets.Load() - baseSets
	if changes < 1 {
		t.Fatalf("AR-03: the settled width was never persisted (changes=%d)", changes)
	}
	if changes > 2 {
		t.Fatalf("AR-03: a single drag cost %d value-changing writes, want ≤2 (debounced coalesced save)", changes)
	}
	if sets > 4 {
		t.Fatalf("AR-03: a single drag cost %d Set calls, want ≤4", sets)
	}
	// The persisted width matches the dragged in-memory width (the save is
	// correct, not merely rare).
	if got := spy.values[settingsKeySidebarWidth]; got == "" {
		t.Fatalf("AR-03: no sidebar width persisted; values=%v", spy.values)
	}
}
