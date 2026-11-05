package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

const (
	// logDirName is the subdirectory of the data directory that holds the
	// application log and crash reports.
	logDirName = "logs"
	// logFilePrefix is the per-day log file name prefix:
	// app-YYYYMMDD.log (rotated files get a .1 suffix).
	logFilePrefix = "app-"
	// maxAppLogBytes is the per-file size cap before app-*.log is renamed to
	// app-*.log.1 and a fresh file is opened. It is a var so tests can lower it.
	maxAppLogBytes int64 = 8 << 20 // 8 MiB
	// logFileMode keeps the log private: it can contain session ids, model
	// errors and file paths.
	logFileMode = 0o600
	// logDirMode matches the rest of the data directory.
	logDirMode = 0o700
)

// logWriter is an io.Writer for the standard logger that keeps one file per
// local day (app-YYYYMMDD.log) and rotates it to <name>.1 when it grows past
// maxBytes. It is safe for concurrent use: log.Printf may be called from any
// goroutine, and every write re-checks the day so a long-running app rolls
// over at midnight.
type logWriter struct {
	dir      string
	maxBytes int64
	now      func() time.Time

	mu   sync.Mutex
	file *os.File
	day  string
	path string
}

func newLogWriter(dir string, maxBytes int64) *logWriter {
	return &logWriter{dir: dir, maxBytes: maxBytes, now: time.Now}
}

// currentPath returns the log path for the given local day.
func (w *logWriter) currentPath(now time.Time) string {
	return filepath.Join(w.dir, logFilePrefix+now.Format("20060102")+".log")
}

// Write implements io.Writer; every write goes to the current day's file,
// opening it lazily and rotating an oversized file first.
func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	day := now.Format("20060102")
	if w.file == nil || w.day != day {
		if w.file != nil {
			_ = w.file.Close()
			w.file = nil
		}
		if err := os.MkdirAll(w.dir, logDirMode); err != nil {
			return 0, fmt.Errorf("create log directory %q: %w", w.dir, err)
		}
		path := w.currentPath(now)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
		if err != nil {
			return 0, fmt.Errorf("open log %q: %w", path, err)
		}
		w.file, w.day, w.path = f, day, path
	}

	if w.maxBytes > 0 {
		if st, err := w.file.Stat(); err == nil && st.Size()+int64(len(p)) > w.maxBytes {
			_ = w.file.Close()
			w.file = nil
			_ = os.Rename(w.path, w.path+".1")
			f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
			if err != nil {
				return 0, fmt.Errorf("rotate log %q: %w", w.path, err)
			}
			w.file = f
		}
	}
	if w.file == nil {
		// Recover from a failed rotation by reopening the day's file.
		f, err := os.OpenFile(w.currentPath(now), os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
		if err != nil {
			return 0, fmt.Errorf("reopen log: %w", err)
		}
		w.file, w.day, w.path = f, day, w.currentPath(now)
	}
	return w.file.Write(p)
}

// Path returns the path of the file currently being written, or the path the
// next write would use when no file is open yet.
func (w *logWriter) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path != "" {
		return w.path
	}
	return w.currentPath(w.now())
}

// File returns the currently open file (nil before the first write). It is
// only used to hand the descriptor to debug.SetCrashOutput, which duplicates
// it.
func (w *logWriter) File() *os.File {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file
}

// Close closes the open day file. Safe to call more than once.
func (w *logWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// setupLogging configures the standard logger to write to stderr and to a
// rotated file under <dataDir>/logs. It returns the writer (so the caller can
// close it and hand its file to debug.SetCrashOutput) and the log path.
//
// A failure to create the log directory or file is reported but does not stop
// the app: logging falls back to stderr only.
func setupLogging(dataDir string) (*logWriter, error) {
	dir := filepath.Join(dataDir, logDirName)
	w := newLogWriter(dir, maxAppLogBytes)

	// Touch the file once so a log exists before the first error, and so an
	// unwritable data directory is detected here instead of silently dropping
	// the log later.
	if _, err := w.Write(nil); err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, w))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return w, nil
}

// installCrashOutput routes the runtime's fatal crash report (unhandled panic,
// fatal signal, out-of-memory throw) into the log file. SetCrashOutput
// duplicates the descriptor, so the log writer may keep using the file.
func installCrashOutput(w *logWriter) error {
	if w == nil {
		return nil
	}
	f := w.File()
	if f == nil {
		return nil
	}
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

// crashReportPath returns the crash report path for now:
// <dataDir>/logs/crash-YYYYMMDD-HHMMSS.txt.
func crashReportPath(dataDir string, now time.Time) string {
	return filepath.Join(dataDir, logDirName, "crash-"+now.Format("20060102-150405")+".txt")
}

// writeCrashReport writes a human-readable crash file for a recovered panic
// (value + stack) and returns its path. The report carries the build identity
// and platform facts so a user can attach one file to a bug report even when
// the app was built without a console (Windows GUI subsystem).
func writeCrashReport(dataDir string, recovered any, stack []byte, now time.Time) (string, error) {
	dir := filepath.Join(dataDir, logDirName)
	if err := os.MkdirAll(dir, logDirMode); err != nil {
		return "", fmt.Errorf("create crash directory %q: %w", dir, err)
	}
	path := crashReportPath(dataDir, now)
	body := fmt.Sprintf(`ZeoKeel crash report
time: %s
version: %s
platform: %s/%s
toolchain: %s

panic: %v

%s
`, now.Format(time.RFC3339), versionString(), runtime.GOOS, runtime.GOARCH,
		runtime.Version(), recovered, stack)
	if err := os.WriteFile(path, []byte(body), logFileMode); err != nil {
		return "", fmt.Errorf("write crash report %q: %w", path, err)
	}
	return path, nil
}
