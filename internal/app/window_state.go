package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
)

// windowStateKey is the WindowOptions.StateKey of the main window; it is also
// the key of the window's entry in the state file.
const windowStateKey = "main"

// windowJSONFile is the JSON file in DataDir that keeps the main window's
// bounds and state. It replaces mygo's per-app user-data window-state.json so
// a portable install (ZEOKEEL_DATA_DIR) carries its window state along.
const windowJSONFile = "window.json"

// fileWindowStateStore is the mygo.WindowStateStore that keeps window states
// as JSON in the app data directory, e.g.
//
//	{"main":{"x":100,"y":80,"width":1600,"height":1000,"maximized":false}}
//
// mygo owns the lifecycle: it loads the states when the first window with a
// StateKey is created and saves them when that window closes and when the app
// quits. The store only has to be safe and lossless, and to fall back to the
// WindowOptions defaults when the file is missing or damaged.
type fileWindowStateStore struct {
	path string

	// mu guards the file operations, although mygo calls the interface on the
	// main thread; a mutex costs nothing and keeps the store safe if a future
	// caller reads it from elsewhere.
	mu sync.Mutex
}

// newWindowStateStore returns the store for dir. The file is created lazily on
// the first save, so construction has no side effects.
func newWindowStateStore(dir string) *fileWindowStateStore {
	return &fileWindowStateStore{path: filepath.Join(dir, windowJSONFile)}
}

// LoadWindowStates returns the saved states. A missing file is not an error:
// mygo then opens the window with its WindowOptions. A damaged file is ignored
// (and replaced by the next save) instead of keeping the app from starting; the
// same fallback mygo's built-in file store applies.
func (s *fileWindowStateStore) LoadWindowStates() (map[string]mygo.WindowState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states map[string]mygo.WindowState
	if err := json.Unmarshal(data, &states); err != nil {
		log.Printf("zeokeel: ignoring damaged window state file %s: %v", s.path, err)
		return nil, nil
	}
	return usableWindowStates(states), nil
}

// SaveWindowStates writes the states atomically: the JSON goes to a temporary
// file first and is renamed into place, so a crash mid-write cannot leave a
// truncated file that the next launch would have to treat as damaged.
func (s *fileWindowStateStore) SaveWindowStates(states map[string]mygo.WindowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return fmt.Errorf("encode window states: %w", err)
	}
	// Append a newline so the file reads as a normal text file and diff tools
	// do not flag the missing final newline.
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create window state directory: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write window state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		// Leave no temporary file behind when the rename fails.
		_ = os.Remove(tmp)
		return fmt.Errorf("replace window state: %w", err)
	}
	return nil
}

// usableWindowStates drops entries mygo cannot restore to a visible window: a
// non-positive size is ignored by restoreWindowState anyway, and keeping it out
// of the map keeps the saved file honest about what the next launch will do.
func usableWindowStates(states map[string]mygo.WindowState) map[string]mygo.WindowState {
	if len(states) == 0 {
		return states
	}
	usable := make(map[string]mygo.WindowState, len(states))
	for key, state := range states {
		if state.Width <= 0 || state.Height <= 0 {
			continue
		}
		usable[key] = state
	}
	return usable
}
