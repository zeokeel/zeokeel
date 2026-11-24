package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SettingsFileName is the settings file inside a product's data directory.
const SettingsFileName = "settings.json"

// Settings is the file-backed contracts.SettingsService: a flat JSON object of
// string keys and values, loaded once at startup and written atomically on
// every Set.
//
// Write path: encode -> create a temporary file in the same directory -> sync
// -> rename over settings.json -> sync the directory. A crash leaves either the
// previous complete file (before rename) or the new complete file (after); a
// truncated settings file is never observable.
type Settings struct {
	path string

	mu     sync.Mutex
	values map[string]string
	// onAutostart applies contracts.SettingAutostart when it changes (the host
	// installs it; see WatchAutostart).
	onAutostart func()
	// logf reports recovery actions (a quarantined settings file); the host
	// logger is installed by SetLogger, a nil keeps it silent.
	logf func(format string, args ...any)
}

// compile-time check: the process host provides this as contracts.KeySettings.
var _ contracts.SettingsService = (*Settings)(nil)

// NewSettings loads the settings in dataDir. A missing file (first run) is the
// empty/default set, not an error; a malformed file is an error, because
// silently resetting user settings would hide data loss.
func NewSettings(dataDir string) (*Settings, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, errors.New("host: settings data directory is required")
	}
	settings := &Settings{
		path:   filepath.Join(dataDir, SettingsFileName),
		values: make(map[string]string),
		logf:   func(string, ...any) {},
	}
	data, err := os.ReadFile(settings.path)
	switch {
	case err == nil:
		var values map[string]string
		if err := json.Unmarshal(data, &values); err != nil {
			// A malformed settings file used to make every product refuse to start
			// with no way out but deleting the file by hand (AUDIT-2026-11-24 B-4).
			// Keep the evidence, start on defaults, and say so: the user's rows are
			// still on disk under the .bad name.
			badPath, quarantineErr := quarantineSettings(settings.path)
			if quarantineErr != nil {
				return nil, fmt.Errorf("host: parse settings %q: %w (and it could not be set aside: %v)", settings.path, err, quarantineErr)
			}
			settings.logf("host: settings %q is malformed (%v); moved it to %q and started with defaults", settings.path, err, badPath)
			return settings, nil
		}
		if values != nil {
			settings.values = values
		}
	case errors.Is(err, fs.ErrNotExist):
		// First run: defaults. The file is created by the first Set.
	default:
		return nil, fmt.Errorf("host: read settings %q: %w", settings.path, err)
	}
	return settings, nil
}

// quarantineSettings renames a settings file that cannot be parsed to
// "<path>.bad-<unix time>", so the next start is clean and the damaged copy is
// kept as evidence.
func quarantineSettings(path string) (string, error) {
	target := fmt.Sprintf("%s.bad-%d", path, time.Now().Unix())
	if err := os.Rename(path, target); err != nil {
		return "", err
	}
	return target, nil
}

// SetLogger installs the host logger used for recovery notices.
func (s *Settings) SetLogger(logger *Logger) {
	if s == nil || logger == nil {
		return
	}
	s.mu.Lock()
	s.logf = logger.Printf
	s.mu.Unlock()
}

// WatchAutostart installs the callback that applies SettingAutostart when it
// changes. The host sets it after loading; nil means "no side effect".
func (s *Settings) WatchAutostart(hook func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onAutostart = hook
	s.mu.Unlock()
}

// Path returns the backing file path (diagnostics and tests).
func (s *Settings) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Get returns the value for key, or def when key is unset.
func (s *Settings) Get(key, def string) string {
	if s == nil {
		return def
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

// Set stores one setting and makes it durable before returning.
func (s *Settings) Set(key, value string) error {
	if s == nil {
		return errors.New("host: nil settings")
	}
	if key == "" {
		return errors.New("host: settings key is required")
	}
	s.mu.Lock()
	if current, ok := s.values[key]; ok && current == value {
		s.mu.Unlock()
		return nil
	}
	next := make(map[string]string, len(s.values)+1)
	for k, v := range s.values {
		next[k] = v
	}
	next[key] = value
	if err := writeJSONAtomic(s.path, next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.values = next
	hook := s.onAutostart
	s.mu.Unlock()
	// Toggling "launch at login" has to take effect now, not at the next start:
	// the value was written and nothing applied it (AUDIT-2026-11-24 A-7).
	// Outside the lock, best effort, and never failing a write that landed.
	if key == contracts.SettingAutostart && hook != nil {
		hook()
	}
	return nil
}

// Delete removes one setting and makes the removal durable. Deleting an unset
// key is a no-op; it is part of the optional contracts.SettingsDeleter cleanup
// surface.
func (s *Settings) Delete(key string) error {
	if s == nil {
		return errors.New("host: nil settings")
	}
	if key == "" {
		return errors.New("host: settings key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; !ok {
		return nil
	}
	next := make(map[string]string, len(s.values))
	for k, v := range s.values {
		if k != key {
			next[k] = v
		}
	}
	if err := writeJSONAtomic(s.path, next); err != nil {
		return err
	}
	s.values = next
	return nil
}

// All returns a copy of every setting.
func (s *Settings) All() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

// writeJSONAtomic writes values to path as indented JSON through a temporary
// file and a rename.
func writeJSONAtomic(path string, values map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("host: create settings directory %q: %w", dir, err)
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("host: encode settings: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("host: create temporary settings file: %w", err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("host: write temporary settings file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("host: sync temporary settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("host: close temporary settings file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("host: set settings file mode: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("host: replace settings %q: %w", path, err)
	}
	renamed = true
	// fsync the directory so the rename itself survives a power loss.
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
