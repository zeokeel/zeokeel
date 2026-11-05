package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const transcriptTestSession = "sess-1"

func transcriptPath(dir, sessionID string) string {
	return filepath.Join(dir, transcriptDirName, sessionID, transcriptFileName)
}

func TestAppendAndReadTranscript(t *testing.T) {
	dir := t.TempDir()

	type record struct {
		Seq  int               `json:"seq"`
		Type string            `json:"type"`
		Meta map[string]string `json:"meta,omitempty"`
	}
	records := []record{
		{Seq: 1, Type: "turn.start", Meta: map[string]string{"turn": "t1"}},
		{Seq: 2, Type: "turn.end"},
	}
	for _, rec := range records {
		if err := AppendTranscript(dir, transcriptTestSession, rec); err != nil {
			t.Fatalf("AppendTranscript(%+v): %v", rec, err)
		}
	}

	path := transcriptPath(dir, transcriptTestSession)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript file: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("transcript does not end with a newline: %q", data)
	}
	if got := strings.Count(string(data), "\n"); got != len(records) {
		t.Errorf("transcript has %d lines, want %d", got, len(records))
	}

	got, err := ReadTranscript(dir, transcriptTestSession)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("ReadTranscript returned %d records, want %d", len(got), len(records))
	}
	for i, raw := range got {
		if !json.Valid(raw) {
			t.Fatalf("record %d is not valid JSON: %q", i, raw)
		}
		var decoded record
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("record %d unmarshal: %v", i, err)
		}
		if decoded.Seq != records[i].Seq || decoded.Type != records[i].Type {
			t.Errorf("record %d = %+v, want %+v", i, decoded, records[i])
		}
		if records[i].Meta != nil && decoded.Meta["turn"] != records[i].Meta["turn"] {
			t.Errorf("record %d meta = %+v, want %+v", i, decoded.Meta, records[i].Meta)
		}
	}

	// Appends after a read keep the previous records.
	if err := AppendTranscript(dir, transcriptTestSession, records[0]); err != nil {
		t.Fatalf("AppendTranscript(third): %v", err)
	}
	if got, err = ReadTranscript(dir, transcriptTestSession); err != nil {
		t.Fatalf("ReadTranscript(third): %v", err)
	} else if len(got) != 3 {
		t.Errorf("ReadTranscript after third append returned %d records, want 3", len(got))
	}
}

func TestTranscriptEventRoundTrip(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, 5, 6, 7, 8, 9, 10, time.UTC)
	want := Event{
		SessionID: transcriptTestSession,
		Seq:       1,
		Type:      "step",
		Payload:   json.RawMessage(`{"step":1}`),
		TS:        stamp,
	}
	if err := AppendTranscript(dir, transcriptTestSession, want); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}
	records, err := ReadTranscript(dir, transcriptTestSession)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("ReadTranscript returned %d records, want 1", len(records))
	}
	var got Event
	if err := json.Unmarshal(records[0], &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if got.Seq != want.Seq || got.Type != want.Type || string(got.Payload) != string(want.Payload) {
		t.Errorf("event round-trip = %+v, want %+v", got, want)
	}
	if !got.TS.Equal(want.TS) {
		t.Errorf("event ts = %v, want %v", got.TS, want.TS)
	}
}

func TestReadTranscriptMissingSession(t *testing.T) {
	records, err := ReadTranscript(t.TempDir(), "no-such-session")
	if err != nil {
		t.Fatalf("ReadTranscript(missing): %v", err)
	}
	if len(records) != 0 {
		t.Errorf("ReadTranscript(missing) returned %d records, want 0", len(records))
	}
}

func TestReadTranscriptToleratesTruncatedTail(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 2; i++ {
		if err := AppendTranscript(dir, transcriptTestSession, map[string]any{"seq": i}); err != nil {
			t.Fatalf("AppendTranscript(%d): %v", i, err)
		}
	}
	// Simulate a crash in the middle of the third append: a torn JSON line
	// with no trailing newline.
	f, err := os.OpenFile(transcriptPath(dir, transcriptTestSession), os.O_WRONLY|os.O_APPEND, transcriptFileMode)
	if err != nil {
		t.Fatalf("open transcript for torn tail: %v", err)
	}
	if _, err := f.WriteString(`{"seq":3,"type":"turn.`); err != nil {
		t.Fatalf("write torn tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close torn tail: %v", err)
	}

	records, err := ReadTranscript(dir, transcriptTestSession)
	if err != nil {
		t.Fatalf("ReadTranscript with truncated tail: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("ReadTranscript returned %d records, want 2 (torn tail ignored)", len(records))
	}

	// A file holding only a torn tail reads as empty, not as an error.
	onlyTail := filepath.Join(dir, transcriptDirName, "only-tail")
	if err := os.MkdirAll(onlyTail, dirPerm); err != nil {
		t.Fatalf("mkdir only-tail: %v", err)
	}
	if err := os.WriteFile(filepath.Join(onlyTail, transcriptFileName), []byte(`{"partial":`), transcriptFileMode); err != nil {
		t.Fatalf("write only-tail: %v", err)
	}
	if records, err = ReadTranscript(dir, "only-tail"); err != nil {
		t.Fatalf("ReadTranscript(only torn tail): %v", err)
	} else if len(records) != 0 {
		t.Errorf("ReadTranscript(only torn tail) returned %d records, want 0", len(records))
	}
}

func TestReadTranscriptRejectsCorruptMiddleLine(t *testing.T) {
	dir := t.TempDir()
	if err := AppendTranscript(dir, transcriptTestSession, map[string]any{"seq": 1}); err != nil {
		t.Fatalf("AppendTranscript(1): %v", err)
	}
	f, err := os.OpenFile(transcriptPath(dir, transcriptTestSession), os.O_WRONLY|os.O_APPEND, transcriptFileMode)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := f.WriteString("not json\n"); err != nil {
		t.Fatalf("write corrupt line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close corrupt line: %v", err)
	}
	if err := AppendTranscript(dir, transcriptTestSession, map[string]any{"seq": 3}); err != nil {
		t.Fatalf("AppendTranscript(3): %v", err)
	}

	if _, err := ReadTranscript(dir, transcriptTestSession); err == nil {
		t.Fatal("ReadTranscript with corrupt middle line: expected an error")
	} else if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("ReadTranscript error = %v, want mention of line 2", err)
	}
}

func TestTranscriptRejectsUnsafeSessionIDs(t *testing.T) {
	dir := t.TempDir()
	for _, sessionID := range []string{"", "../escape", "a/b", `a\b`, "..", "a..b"} {
		if err := AppendTranscript(dir, sessionID, map[string]any{"x": 1}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("AppendTranscript(%q) error = %v, want ErrInvalidArgument", sessionID, err)
		}
		if _, err := ReadTranscript(dir, sessionID); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("ReadTranscript(%q) error = %v, want ErrInvalidArgument", sessionID, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unsafe session id escaped the sessions directory: %v", err)
	}
}

func TestTranscriptRejectsBadArguments(t *testing.T) {
	if err := AppendTranscript("", transcriptTestSession, map[string]any{"x": 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("AppendTranscript(empty dir) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := ReadTranscript("", transcriptTestSession); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("ReadTranscript(empty dir) error = %v, want ErrInvalidArgument", err)
	}
	if err := AppendTranscript(t.TempDir(), transcriptTestSession, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("AppendTranscript(nil record) error = %v, want ErrInvalidArgument", err)
	}

	dir := t.TempDir()
	if err := AppendTranscript(dir, transcriptTestSession, make(chan int)); err == nil {
		t.Fatal("AppendTranscript(unmarshalable record): expected an error")
	}
	if _, err := os.Stat(transcriptPath(dir, transcriptTestSession)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed append left a transcript file behind: %v", err)
	}
}
