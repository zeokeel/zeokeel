package agent

// Tests for the raw session-log archive (contracts.SessionLogExporter): a ZIP
// with events.jsonl (the raw durable events) and meta.json (identity and export
// header), honest errors for an unknown session or a missing store, and a
// bounded archive.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/export"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// logExportStore is the narrow durable view the exporter reads: metadata plus
// the full event log. It lets the archive tests stay independent of SQLite.
type logExportStore struct {
	meta      contracts.SessionMeta
	found     bool
	metaErr   error
	events    []rig.SessionEvent
	eventsErr error
}

func (s *logExportStore) GetSessionMeta(context.Context, string) (contracts.SessionMeta, bool, error) {
	return s.meta, s.found, s.metaErr
}

func (s *logExportStore) Events(context.Context, string, int64, int64) ([]rig.SessionEvent, error) {
	return s.events, s.eventsErr
}

// archiveEntries reads a ZIP archive into name -> content.
func archiveEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive is not a zip: %v", err)
	}
	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(opened)
		opened.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		entries[file.Name] = content
	}
	return entries
}

// logArchiveEvent is the JSON shape of one events.jsonl line.
type logArchiveEvent struct {
	Seq     int64           `json:"seq"`
	Type    string          `json:"type"`
	Time    time.Time       `json:"time"`
	Payload json.RawMessage `json:"payload"`
}

// TestSessionLogArchiveContainsRawEventsAndMeta covers the archive: the two
// documented entries, meta.json's identity/export header, and one raw event per
// JSONL line with payload bytes preserved.
func TestSessionLogArchiveContainsRawEventsAndMeta(t *testing.T) {
	userAt := time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC)
	endAt := userAt.Add(90 * time.Second)
	exportedAt := time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)
	store := &logExportStore{
		meta:  contracts.SessionMeta{ID: "sess-log", Title: "发布检查"},
		found: true,
		events: []rig.SessionEvent{
			{Seq: 1, SessionID: "sess-log", Type: "user_message", Time: userAt,
				Payload: json.RawMessage(`{"text":"继续发布","goalRound":1}`)},
			{Seq: 2, SessionID: "sess-log", Type: "run_end", Time: endAt,
				Payload: json.RawMessage(`{"ok":true}`)},
			{Seq: 3, SessionID: "sess-log", Type: "tool_call", Time: endAt,
				Payload: json.RawMessage(`{"tool":"bash","ok":true}`)},
		},
	}
	exporter := newSessionLogExporter(store)
	exporter.now = func() time.Time { return exportedAt }

	data, err := exporter.SessionLogArchive(context.Background(), "sess-log")
	if err != nil {
		t.Fatalf("SessionLogArchive: %v", err)
	}
	entries := archiveEntries(t, data)
	if len(entries) != 2 {
		t.Fatalf("archive entries = %v, want events.jsonl + meta.json", entries)
	}
	jsonl, ok := entries["events.jsonl"]
	if !ok {
		t.Fatalf("archive lacks events.jsonl: %v", entries)
	}
	rawMeta, ok := entries["meta.json"]
	if !ok {
		t.Fatalf("archive lacks meta.json: %v", entries)
	}

	lines := strings.Split(strings.TrimSpace(string(jsonl)), "\n")
	if len(lines) != 3 {
		t.Fatalf("events.jsonl lines = %d, want one per event (3):\n%s", len(lines), jsonl)
	}
	for index, line := range lines {
		var event logArchiveEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not event JSON: %v", index, err)
		}
		if event.Seq != int64(index+1) {
			t.Fatalf("line %d seq = %d, want %d", index, event.Seq, index+1)
		}
		if event.Type != store.events[index].Type {
			t.Fatalf("line %d type = %q, want %q", index, event.Type, store.events[index].Type)
		}
		if !event.Time.Equal(store.events[index].Time) {
			t.Fatalf("line %d time = %v, want %v", index, event.Time, store.events[index].Time)
		}
		if string(event.Payload) != string(store.events[index].Payload) {
			t.Fatalf("line %d payload = %s, want raw %s", index, event.Payload, store.events[index].Payload)
		}
		// The session id belongs to meta.json, not to every line.
		if strings.Contains(line, "sessionId") || strings.Contains(line, "sess-log") {
			t.Fatalf("line %d leaks the session id: %s", index, line)
		}
	}

	var meta map[string]any
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		t.Fatalf("meta.json is not JSON: %v", err)
	}
	if meta["id"] != "sess-log" || meta["title"] != "发布检查" {
		t.Fatalf("meta identity = %v/%v", meta["id"], meta["title"])
	}
	exported, err := time.Parse(time.RFC3339, meta["exportedAt"].(string))
	if err != nil || !exported.Equal(exportedAt) {
		t.Fatalf("meta exportedAt = %v (%v), want %v", meta["exportedAt"], err, exportedAt)
	}
	if meta["version"] == "" || meta["version"] == nil {
		t.Fatalf("meta version missing: %v", meta)
	}
	if schema, ok := meta["schema"].(float64); !ok || schema != 1 {
		t.Fatalf("meta schema = %v, want 1", meta["schema"])
	}
}

// TestSessionLogArchiveEmptySession covers a session with no events: a valid
// archive with an empty JSONL and the metadata still says who it is.
func TestSessionLogArchiveEmptySession(t *testing.T) {
	store := &logExportStore{meta: contracts.SessionMeta{ID: "sess-empty"}, found: true}
	exporter := newSessionLogExporter(store)
	data, err := exporter.SessionLogArchive(context.Background(), "sess-empty")
	if err != nil {
		t.Fatalf("SessionLogArchive: %v", err)
	}
	entries := archiveEntries(t, data)
	if len(entries["events.jsonl"]) != 0 {
		t.Fatalf("empty session JSONL = %q, want empty", entries["events.jsonl"])
	}
	if !strings.Contains(string(entries["meta.json"]), `"sess-empty"`) {
		t.Fatalf("meta.json does not name the session: %s", entries["meta.json"])
	}
}

// TestSessionLogArchiveUnknownSession covers the honest unknown-session error.
func TestSessionLogArchiveUnknownSession(t *testing.T) {
	store := &logExportStore{found: false}
	exporter := newSessionLogExporter(store)
	data, err := exporter.SessionLogArchive(context.Background(), "missing")
	if data != nil {
		t.Fatalf("unknown session returned an archive anyway: %d bytes", len(data))
	}
	if !errors.Is(err, contracts.ErrSessionLogNotFound) {
		t.Fatalf("unknown session error = %v, want ErrSessionLogNotFound", err)
	}
}

// TestSessionLogArchiveWithoutStore covers a host without a durable store.
func TestSessionLogArchiveWithoutStore(t *testing.T) {
	exporter := newSessionLogExporter(nil)
	data, err := exporter.SessionLogArchive(context.Background(), "sess-1")
	if data != nil || !errors.Is(err, contracts.ErrSessionLogNoStore) {
		t.Fatalf("no-store export = %d bytes, %v; want ErrSessionLogNoStore", len(data), err)
	}
}

// TestSessionLogArchiveIsBounded covers the bound: a log past the archive limit
// errors instead of producing an unbounded archive.
func TestSessionLogArchiveIsBounded(t *testing.T) {
	events := make([]rig.SessionEvent, export.MaxEvents+1)
	for index := range events {
		events[index] = rig.SessionEvent{Seq: int64(index + 1), Type: "tool_call",
			Payload: json.RawMessage(`{"tool":"bash"}`)}
	}
	store := &logExportStore{meta: contracts.SessionMeta{ID: "sess-huge"}, found: true, events: events}
	exporter := newSessionLogExporter(store)
	data, err := exporter.SessionLogArchive(context.Background(), "sess-huge")
	if data != nil {
		t.Fatalf("oversized log returned %d bytes, want no archive", len(data))
	}
	if !errors.Is(err, contracts.ErrSessionLogTooLarge) {
		t.Fatalf("oversized log error = %v, want ErrSessionLogTooLarge", err)
	}
}
