package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// readWindowStateFile decodes window.json the way the next launch (and an
// operator inspecting the data directory) reads it.
func readWindowStateFile(t *testing.T, dir string) map[string]mygo.WindowState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, windowJSONFile))
	if err != nil {
		t.Fatalf("read %s: %v", windowJSONFile, err)
	}
	var states map[string]mygo.WindowState
	if err := json.Unmarshal(data, &states); err != nil {
		t.Fatalf("decode %s: %v", windowJSONFile, err)
	}
	return states
}

// TestWindowStateStoreRoundTrip covers the launch-to-launch path: the main
// window's bounds and maximized flag survive a save and a fresh load.
func TestWindowStateStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := map[string]mygo.WindowState{
		windowStateKey: {X: 120, Y: 80, Width: 1440, Height: 900, Maximized: true},
		"secondary":    {X: -1280, Y: 40, Width: 800, Height: 600},
	}
	if err := newWindowStateStore(dir).SaveWindowStates(want); err != nil {
		t.Fatalf("SaveWindowStates: %v", err)
	}
	// The file keeps the documented JSON shape, readable by anyone debugging
	// the app data directory.
	if got := readWindowStateFile(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("saved states = %+v, want %+v", got, want)
	}
	// A new store reads what a previous launch wrote, exactly.
	loaded, err := newWindowStateStore(dir).LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates: %v", err)
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("loaded states = %+v, want %+v", loaded, want)
	}
	// The write is atomic: no temporary file may be left next to the state
	// file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temporary file %q survived the save", entry.Name())
		}
	}
	// The state file is private to the user.
	info, err := os.Stat(filepath.Join(dir, windowJSONFile))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("state file mode = %o, want no group/other access", perm)
	}
}

// TestWindowStateStoreMissingFile covers the first launch: no file yet means no
// states and no error, and the first save creates the data directory lazily.
func TestWindowStateStoreMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created-yet")
	store := newWindowStateStore(dir)
	states, err := store.LoadWindowStates()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("missing file returned %+v, want no states", states)
	}
	want := mygo.WindowState{X: 5, Y: 6, Width: 800, Height: 600}
	if err := store.SaveWindowStates(map[string]mygo.WindowState{windowStateKey: want}); err != nil {
		t.Fatalf("SaveWindowStates on a missing directory: %v", err)
	}
	if got := readWindowStateFile(t, dir)[windowStateKey]; got != want {
		t.Fatalf("saved state = %+v, want %+v", got, want)
	}
}

// TestWindowStateStoreCorruptFile covers a damaged file: the launch falls back
// to the WindowOptions defaults instead of failing, and the next save replaces
// the damaged content with valid JSON.
func TestWindowStateStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, windowJSONFile)
	if err := os.WriteFile(path, []byte("{not json at all"), 0o600); err != nil {
		t.Fatalf("write damaged file: %v", err)
	}
	store := newWindowStateStore(dir)
	states, err := store.LoadWindowStates()
	if err != nil {
		t.Fatalf("damaged file must fall back, got error: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("damaged file returned %+v, want no states", states)
	}
	want := map[string]mygo.WindowState{
		windowStateKey: {X: 10, Y: 20, Width: 1280, Height: 800},
	}
	if err := store.SaveWindowStates(want); err != nil {
		t.Fatalf("SaveWindowStates over a damaged file: %v", err)
	}
	loaded, err := store.LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates after repair: %v", err)
	}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("repaired states = %+v, want %+v", loaded, want)
	}
}

// TestWindowStateStoreDropsUnusableEntries covers boundary values: an entry
// mygo cannot restore (a non-positive size) is dropped, valid entries survive.
func TestWindowStateStoreDropsUnusableEntries(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "main": {"x": 10, "y": 20, "width": 0, "height": 900},
  "zero": {"width": 0, "height": 0},
  "negative": {"width": -800, "height": 600},
  "side": {"x": 30, "y": 40, "width": 640, "height": 480}
}`
	if err := os.WriteFile(filepath.Join(dir, windowJSONFile), []byte(raw), 0o600); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	loaded, err := newWindowStateStore(dir).LoadWindowStates()
	if err != nil {
		t.Fatalf("LoadWindowStates: %v", err)
	}
	for _, key := range []string{"main", "zero", "negative"} {
		if _, ok := loaded[key]; ok {
			t.Errorf("unusable state %q was loaded: %+v", key, loaded[key])
		}
	}
	want := mygo.WindowState{X: 30, Y: 40, Width: 640, Height: 480}
	if got := loaded["side"]; got != want {
		t.Fatalf("usable state = %+v, want %+v", got, want)
	}
}

// TestWindowStateStoreEmptyStates covers the empty map save: a valid empty file,
// not "null" or a write error.
func TestWindowStateStoreEmptyStates(t *testing.T) {
	dir := t.TempDir()
	store := newWindowStateStore(dir)
	if err := store.SaveWindowStates(map[string]mygo.WindowState{}); err != nil {
		t.Fatalf("SaveWindowStates(empty): %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, windowJSONFile))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "{}" {
		t.Fatalf("empty state file = %q, want {}", strings.TrimSpace(string(raw)))
	}
	loaded, err := store.LoadWindowStates()
	if err != nil || len(loaded) != 0 {
		t.Fatalf("LoadWindowStates(empty) = %+v, %v; want no states", loaded, err)
	}
}
