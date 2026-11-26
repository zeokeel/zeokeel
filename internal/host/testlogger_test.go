package host

// The recording logger used by the install/update tests. It used to live in the
// tray tests; the tray is gone, the tests that need a logger are not.

import (
	"fmt"
	"strings"
	"sync"
)

// trayRecordingLogger records log lines for assertions.
type trayRecordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *trayRecordingLogger) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

func (l *trayRecordingLogger) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}
