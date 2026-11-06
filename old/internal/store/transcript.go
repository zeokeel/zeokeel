package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	transcriptDirName  = "sessions"
	transcriptFileName = "session.jsonl"
	transcriptFileMode = 0o600
)

// validSessionIDPattern keeps a session id safe to use as a single path
// element, so transcript paths can never escape <dir>/sessions.
var validSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// validateSessionID rejects ids that are empty or could traverse the
// filesystem when used as a directory name.
func validateSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty session id", ErrInvalidArgument)
	}
	if !validSessionIDPattern.MatchString(id) || strings.Contains(id, "..") {
		return fmt.Errorf("%w: invalid session id %q", ErrInvalidArgument, id)
	}
	return nil
}

// sessionDir returns <dir>/sessions/<sessionID>, validating sessionID first.
func sessionDir(dir, sessionID string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", err
	}
	return filepath.Join(dir, transcriptDirName, sessionID), nil
}

// RemoveSessionDir deletes <dir>/sessions/<sessionID> and everything in it.
// Deleting a session's rows is not enough: the JSONL transcript and any
// artifacts the session left there would otherwise outlive "delete session".
func (s *Store) RemoveSessionDir(sessionID string) error {
	dir, err := sessionDir(s.Dir(), sessionID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("store: remove session directory %q: %w", dir, err)
	}
	return nil
}

// AppendTranscript appends record as one JSON line to
// <dir>/sessions/<sessionID>/session.jsonl, creating the directory tree when
// needed. The record is written with a single append and fsync'd before
// AppendTranscript returns, so an acknowledged line survives a crash.
//
// The JSONL transcript is the durable, human-readable mirror of the events
// table (docs/PLAN.md §9.3); records are otherwise opaque to the store.
func AppendTranscript(dir, sessionID string, record any) error {
	return AppendTranscriptRecords(dir, sessionID, []any{record})
}

// AppendTranscriptRecords appends records as consecutive JSON lines to the
// session transcript. All lines are written with one open, one write and one
// fsync, which keeps high-frequency callers (streamed token events) from
// paying a file open plus durability barrier per record. The atomicity
// guarantee matches AppendTranscript: after it returns the lines are durable,
// and a crash during the write can only tear the final line.
//
// An empty record list is a no-op.
func AppendTranscriptRecords(dir, sessionID string, records []any) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("%w: append transcript: empty directory", ErrInvalidArgument)
	}
	if len(records) == 0 {
		return nil
	}
	sessDir, err := sessionDir(dir, sessionID)
	if err != nil {
		return fmt.Errorf("store: append transcript: %w", err)
	}

	var buf bytes.Buffer
	for _, record := range records {
		if record == nil {
			return fmt.Errorf("%w: append transcript: nil record", ErrInvalidArgument)
		}
		line, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("store: append transcript: encode record: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	if err := os.MkdirAll(sessDir, dirPerm); err != nil {
		return fmt.Errorf("store: append transcript: create session directory: %w", err)
	}
	path := filepath.Join(sessDir, transcriptFileName)
	_, statErr := os.Stat(path)
	newFile := errors.Is(statErr, fs.ErrNotExist)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, transcriptFileMode)
	if err != nil {
		return fmt.Errorf("store: append transcript %q: %w", path, err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close() //nolint:errcheck // write error wins
		return fmt.Errorf("store: append transcript %q: write: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close() //nolint:errcheck // sync error wins
		return fmt.Errorf("store: append transcript %q: fsync: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store: append transcript %q: close: %w", path, err)
	}
	if newFile {
		// Make the new file's directory entry durable too. Platforms that do
		// not support directory fsync (e.g. Windows) are ignored: the record
		// itself is already synced.
		syncDir(sessDir)
	}
	return nil
}

// ReadTranscript reads <dir>/sessions/<sessionID>/session.jsonl in append
// order. A missing file is an empty transcript (nil, nil): not every session
// has recorded a transcript yet.
//
// A truncated or otherwise invalid final line is ignored. That is the shape a
// crash during append leaves behind (a torn tail), so it must not make the
// history unreadable. An invalid line followed by more content means real
// corruption and is reported as an error.
func ReadTranscript(dir, sessionID string) ([]json.RawMessage, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("%w: read transcript: empty directory", ErrInvalidArgument)
	}
	sessDir, err := sessionDir(dir, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: read transcript: %w", err)
	}
	path := filepath.Join(sessDir, transcriptFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read transcript %q: %w", path, err)
	}

	lines := bytes.Split(data, []byte{'\n'})
	records := make([]json.RawMessage, 0, len(lines))
	for i, raw := range lines {
		line := bytes.TrimSpace(bytes.TrimSuffix(raw, []byte{'\r'}))
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			if !hasContentAfter(lines, i+1) {
				break // torn tail from an interrupted append
			}
			return nil, fmt.Errorf("store: read transcript %q: line %d is not valid JSON", path, i+1)
		}
		records = append(records, append(json.RawMessage(nil), line...))
	}
	return records, nil
}

// hasContentAfter reports whether any non-blank line exists at or after from.
func hasContentAfter(lines [][]byte, from int) bool {
	for _, raw := range lines[from:] {
		if len(bytes.TrimSpace(raw)) > 0 {
			return true
		}
	}
	return false
}

// syncDir fsyncs a directory entry, best effort. Errors are ignored because
// not every platform supports opening or syncing directories.
func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}
