package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixedNow returns a controllable clock for the log writer tests.
func fixedNow(t *testing.T, at time.Time) func() time.Time {
	t.Helper()
	cur := at
	return func() time.Time { return cur }
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestLogWriterCreatesDailyFile covers the AR-01 requirement that a log file
// exists under <data dir>/logs named by day.
func TestLogWriterCreatesDailyFile(t *testing.T) {
	dir := t.TempDir()
	w := newLogWriter(dir, 0)
	w.now = fixedNow(t, time.Date(2026, 11, 5, 21, 30, 0, 0, time.Local))

	if _, err := w.Write([]byte("hello log\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	path := filepath.Join(dir, "app-20261105.log")
	if got := w.Path(); got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
	if got := readFile(t, path); got != "hello log\n" {
		t.Fatalf("log content = %q", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if st.Mode().Perm() != logFileMode {
		t.Fatalf("log mode = %v, want %v", st.Mode().Perm(), os.FileMode(logFileMode))
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestLogWriterRotatesBySize covers the size rotation: app-YYYYMMDD.log is
// renamed to *.1 and a fresh file starts.
func TestLogWriterRotatesBySize(t *testing.T) {
	dir := t.TempDir()
	w := newLogWriter(dir, 16)
	w.now = fixedNow(t, time.Date(2026, 11, 5, 21, 30, 0, 0, time.Local))
	defer w.Close() //nolint:errcheck // test cleanup

	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := w.Write([]byte("abcdefg")); err != nil { // 10+7 > 16 -> rotate
		t.Fatalf("second write: %v", err)
	}
	live := filepath.Join(dir, "app-20261105.log")
	rotated := live + ".1"
	if got := readFile(t, live); got != "abcdefg" {
		t.Fatalf("live log after rotate = %q, want %q", got, "abcdefg")
	}
	if got := readFile(t, rotated); got != "0123456789" {
		t.Fatalf("rotated log = %q, want %q", got, "0123456789")
	}
}

// TestLogWriterRollsOverByDay covers the day rollover: a long-running app
// keeps writing the current day's file after midnight.
func TestLogWriterRollsOverByDay(t *testing.T) {
	dir := t.TempDir()
	cur := time.Date(2026, 11, 5, 23, 59, 0, 0, time.Local)
	w := newLogWriter(dir, 0)
	w.now = func() time.Time { return cur }
	defer w.Close() //nolint:errcheck // test cleanup

	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatalf("write day one: %v", err)
	}
	cur = cur.Add(2 * time.Minute) // crosses midnight into 2026-11-06
	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatalf("write day two: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "app-20261105.log")); got != "day one\n" {
		t.Fatalf("day one log = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "app-20261106.log")); got != "day two\n" {
		t.Fatalf("day two log = %q", got)
	}
}

// TestSetupLoggingWritesFileAndStderr ensures the standard logger is wired to
// both the file and stderr, and that the default flags are set.
func TestSetupLoggingWritesFileAndStderr(t *testing.T) {
	oldOut, oldFlags := log.Writer(), log.Flags()
	defer func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	}()

	dir := t.TempDir()
	w, err := setupLogging(dir)
	if err != nil {
		t.Fatalf("setupLogging: %v", err)
	}
	defer w.Close() //nolint:errcheck // test cleanup
	log.Printf("hello from the test logger")

	matches, err := filepath.Glob(filepath.Join(dir, logDirName, "app-*.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("log files = %v (err %v), want exactly one", matches, err)
	}
	body := readFile(t, matches[0])
	if !strings.Contains(body, "hello from the test logger") {
		t.Fatalf("log file missing log line: %q", body)
	}
	if log.Flags()&log.LstdFlags == 0 || log.Flags()&log.Lmicroseconds == 0 {
		t.Fatalf("log flags = %v, want date/time + microseconds", log.Flags())
	}
}

// TestWriteCrashReport covers the AR-01 crash file: a recovered panic leaves a
// self-contained report with build identity and stack.
func TestWriteCrashReport(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 5, 22, 1, 2, 0, time.Local)
	path, err := writeCrashReport(dir, "boom", []byte("goroutine 1 [running]:\nmain.run()"), at)
	if err != nil {
		t.Fatalf("writeCrashReport: %v", err)
	}
	want := filepath.Join(dir, logDirName, "crash-20261105-220102.txt")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	body := readFile(t, path)
	for _, want := range []string{"ZeoKeel crash report", "panic: boom", "goroutine 1 [running]", versionString()} {
		if !strings.Contains(body, want) {
			t.Fatalf("crash report missing %q:\n%s", want, body)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat crash report: %v", err)
	}
	if st.Mode().Perm() != logFileMode {
		t.Fatalf("crash report mode = %v, want %v", st.Mode().Perm(), os.FileMode(logFileMode))
	}
}

// TestCrashReportPathIsUniquePerSecond documents that two crashes in the same
// second would collide; the report keeps a timestamp, which is enough for a
// single-process crash file.
func TestCrashReportPathIsUniquePerSecond(t *testing.T) {
	a := time.Date(2026, 11, 5, 22, 1, 2, 0, time.Local)
	if got, want := crashReportPath("/data", a), filepath.Join("/data", logDirName, "crash-20261105-220102.txt"); got != want {
		t.Fatalf("crashReportPath = %q, want %q", got, want)
	}
}
