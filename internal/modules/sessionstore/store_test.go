package sessionstore

// Tests for the SQLite store: schema, session metadata, history, checkpoints,
// the event log and the concurrency/persistence guarantees.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// openTestStore opens a store in a fresh temporary data directory.
func openTestStore(t *testing.T) *store {
	t.Helper()
	return openTestStoreAt(t, t.TempDir())
}

// openTestStoreAt opens a store in dataDir and closes it on cleanup.
func openTestStoreAt(t *testing.T, dataDir string) *store {
	t.Helper()
	current, err := openStore(storeOptions{dataDir: dataDir})
	if err != nil {
		t.Fatalf("openStore(%q): %v", dataDir, err)
	}
	t.Cleanup(func() { _ = current.Close() })
	return current
}

// sampleMessages returns one message of every interesting shape.
func sampleMessages() []rig.Message {
	return []rig.Message{
		{Role: rig.RoleUser, Content: []rig.ContentPart{rig.TextPart("hello")}},
		{
			Role:    rig.RoleAssistant,
			Content: []rig.ContentPart{rig.TextPart("checking"), rig.ImagePart("https://example.com/a.png", "high")},
			ToolCalls: []rig.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.txt"}`},
			},
			Reasoning: "I should read the file first.",
		},
		{
			Role:       rig.RoleTool,
			Content:    []rig.ContentPart{rig.TextPart("file contents")},
			ToolCallID: "call-1",
			Name:       "read_file",
		},
	}
}

func metaOf(t *testing.T, current *store, id string) contracts.SessionMeta {
	t.Helper()
	meta, found, err := current.GetSessionMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSessionMeta(%q): %v", id, err)
	}
	if !found {
		t.Fatalf("session %q not found", id)
	}
	return meta
}

func TestOpenCreatesSchemaAndPragmas(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "nested", "data")
	current := openTestStoreAt(t, dataDir)
	if current.path != filepath.Join(dataDir, DBFileName) {
		t.Fatalf("store path = %q, want %q", current.path, filepath.Join(dataDir, DBFileName))
	}
	if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
		t.Fatalf("data dir was not created: %v", err)
	}
	if info, err := os.Stat(dataDir); err == nil {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("data dir perm = %o, want 700", perm)
		}
	}

	var journalMode string
	if err := current.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := current.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeout int
	if err := current.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != int(DefaultBusyTimeout.Milliseconds()) {
		t.Fatalf("busy_timeout = %d, want %d", busyTimeout, DefaultBusyTimeout.Milliseconds())
	}

	for _, table := range []string{"sessions", "messages", "events", "checkpoints"} {
		var name string
		err := current.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q is missing: %v", table, err)
		}
	}
}

func TestCreateAndGetSession(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()

	if err := current.CreateSession(ctx, "s1", "First title"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Creating again keeps the original title (idempotent).
	if err := current.CreateSession(ctx, "s1", "Changed title"); err != nil {
		t.Fatalf("CreateSession(again): %v", err)
	}
	meta := metaOf(t, current, "s1")
	if meta.Title != "First title" {
		t.Fatalf("title = %q, want the first title", meta.Title)
	}
	if meta.MessageCount != 0 {
		t.Fatalf("message count = %d, want 0", meta.MessageCount)
	}
	if meta.CreatedAt.IsZero() || meta.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = %+v, want non-zero", meta)
	}

	if _, found, err := current.GetSessionMeta(ctx, "missing"); err != nil || found {
		t.Fatalf("unknown session meta = found=%v err=%v, want not found", found, err)
	}
	if err := current.CreateSession(ctx, "", "x"); err == nil {
		t.Fatal("empty session ID accepted")
	}
}

func TestRenameSession(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "s1", "Original"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	before := metaOf(t, current, "s1").UpdatedAt
	time.Sleep(2 * time.Millisecond)
	if err := current.RenameSession(ctx, "s1", "Renamed"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	meta := metaOf(t, current, "s1")
	if meta.Title != "Renamed" {
		t.Fatalf("title = %q, want Renamed", meta.Title)
	}
	if !meta.UpdatedAt.After(before) {
		t.Fatalf("updated_at did not advance: %v -> %v", before, meta.UpdatedAt)
	}
	if err := current.RenameSession(ctx, "missing", "x"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("rename unknown error = %v, want ErrUnknownSession", err)
	}
}

func TestSessionsOrderingAndCounts(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := current.CreateSession(ctx, id, "title-"+id); err != nil {
			t.Fatalf("CreateSession(%q): %v", id, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	// Touching "a" makes it the most recently updated session.
	if err := current.History().Append(ctx, "a", sampleMessages()...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	metas, err := current.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("sessions = %+v, want 3", metas)
	}
	if metas[0].ID != "a" {
		t.Fatalf("first session = %q, want a (newest updated)", metas[0].ID)
	}
	if metas[0].MessageCount != len(sampleMessages()) {
		t.Fatalf("message count = %d, want %d", metas[0].MessageCount, len(sampleMessages()))
	}
	// A checkpoint does not count as a message.
	if err := current.CheckpointStore().Save(ctx, "b", &rig.Checkpoint{SessionID: "b"}); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}
	meta := metaOf(t, current, "b")
	if meta.MessageCount != 0 {
		t.Fatalf("message count = %d, want 0", meta.MessageCount)
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	history := current.History()

	if messages, err := history.Load(ctx, "missing"); err != nil || messages != nil {
		t.Fatalf("Load(unknown) = %v, %v; want nil, nil", messages, err)
	}
	want := sampleMessages()
	if err := history.Append(ctx, "s1", want...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := history.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %#v, want %#v", got, want)
	}
	// Appending auto-creates the session row and counts the messages.
	meta := metaOf(t, current, "s1")
	if meta.MessageCount != len(want) {
		t.Fatalf("message count = %d, want %d", meta.MessageCount, len(want))
	}
	// A second append continues the ordinal sequence.
	if err := history.Append(ctx, "s1", rig.NewMessage(rig.RoleUser, "second turn")); err != nil {
		t.Fatalf("second Append: %v", err)
	}
	got, err = history.Load(ctx, "s1")
	if err != nil || len(got) != len(want)+1 {
		t.Fatalf("Load after second append = %d messages, %v", len(got), err)
	}
	if got[len(got)-1].Text() != "second turn" {
		t.Fatalf("last message = %#v", got[len(got)-1])
	}
	// An empty append is a no-op and does not create a session.
	if err := history.Append(ctx, "empty-session"); err != nil {
		t.Fatalf("empty Append: %v", err)
	}
	if _, found, err := current.GetSessionMeta(ctx, "empty-session"); err != nil || found {
		t.Fatalf("empty append created a session: found=%v err=%v", found, err)
	}
}

func TestReplaceHistory(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	history := current.History()
	if err := history.Append(ctx, "s1", sampleMessages()...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	replacement := []rig.Message{
		rig.NewMessage(rig.RoleUser, "compacted summary"),
		rig.NewMessage(rig.RoleAssistant, "acknowledged"),
	}
	if err := current.ReplaceHistory(ctx, "s1", replacement); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	got, err := history.Load(ctx, "s1")
	if err != nil || !reflect.DeepEqual(got, replacement) {
		t.Fatalf("Load after replace = %#v, %v", got, err)
	}
	if count := metaOf(t, current, "s1").MessageCount; count != len(replacement) {
		t.Fatalf("message count = %d, want %d", count, len(replacement))
	}
	// Replacing with an empty history keeps the session but clears messages.
	if err := current.ReplaceHistory(ctx, "s1", nil); err != nil {
		t.Fatalf("ReplaceHistory(nil): %v", err)
	}
	if got, err := history.Load(ctx, "s1"); err != nil || len(got) != 0 {
		t.Fatalf("Load after empty replace = %#v, %v", got, err)
	}
	if count := metaOf(t, current, "s1").MessageCount; count != 0 {
		t.Fatalf("message count = %d, want 0", count)
	}
	// Replacing an unknown session creates it (Save-on-write semantics).
	if err := current.ReplaceHistory(ctx, "new-session", replacement); err != nil {
		t.Fatalf("ReplaceHistory(new): %v", err)
	}
	if count := metaOf(t, current, "new-session").MessageCount; count != len(replacement) {
		t.Fatalf("new session message count = %d, want %d", count, len(replacement))
	}
	if err := current.ReplaceHistory(ctx, "", nil); err == nil {
		t.Fatal("empty session ID accepted")
	}
}

func TestCheckpointStore(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	checkpoints := current.CheckpointStore()

	if checkpoint, err := checkpoints.Load(ctx, "s1"); err != nil || checkpoint != nil {
		t.Fatalf("Load(no checkpoint) = %v, %v; want nil, nil", checkpoint, err)
	}
	if err := checkpoints.Save(ctx, "s1", nil); err == nil {
		t.Fatal("Save(nil checkpoint) accepted")
	}
	want := &rig.Checkpoint{
		SessionID:     "s1",
		TurnID:        "turn-1",
		Phase:         rig.CheckpointPhase("tools"),
		Messages:      sampleMessages(),
		PriorMessages: 1,
	}
	if err := checkpoints.Save(ctx, "s1", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := checkpoints.Load(ctx, "s1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %#v, %v; want %#v", got, err, want)
	}
	// Save overwrites (upsert).
	want.TurnID = "turn-2"
	if err := checkpoints.Save(ctx, "s1", want); err != nil {
		t.Fatalf("Save(overwrite): %v", err)
	}
	if got, err := checkpoints.Load(ctx, "s1"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load after overwrite = %#v, %v", got, err)
	}
	if err := checkpoints.Clear(ctx, "s1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got, err := checkpoints.Load(ctx, "s1"); err != nil || got != nil {
		t.Fatalf("Load after clear = %#v, %v", got, err)
	}
	// Clear is idempotent and unknown sessions are fine.
	if err := checkpoints.Clear(ctx, "s1"); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
	if err := checkpoints.Clear(ctx, "missing"); err != nil {
		t.Fatalf("Clear(unknown): %v", err)
	}
	// Saving a checkpoint for a new session creates the session row.
	if err := checkpoints.Save(ctx, "checkpoint-only", &rig.Checkpoint{SessionID: "checkpoint-only"}); err != nil {
		t.Fatalf("Save(new session): %v", err)
	}
	metaOf(t, current, "checkpoint-only")
}

func TestEventLog(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "s1", "events"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := current.CreateSession(ctx, "s2", "other"); err != nil {
		t.Fatalf("CreateSession(s2): %v", err)
	}
	events := []rig.SessionEvent{
		{SessionID: "s1", Type: "user", Payload: json.RawMessage(`{"text":"alpha"}`)},
		{SessionID: "s1", Type: "tool", Payload: json.RawMessage(`{"text":"beta 100% done"}`)},
		{SessionID: "s1", Type: "assistant"},
		{SessionID: "s2", Type: "user", Payload: json.RawMessage(`{"text":"other session"}`)},
	}
	for index, event := range events {
		event.Time = time.Unix(int64(1700000000+index), 0).UTC()
		events[index] = event
		if err := current.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent(%d): %v", index, err)
		}
	}

	all, err := current.Events(ctx, "s1", 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("Events(s1) = %d, want 3", len(all))
	}
	for index := 1; index < len(all); index++ {
		if all[index].Seq <= all[index-1].Seq {
			t.Fatalf("events are not in ascending seq order: %+v", all)
		}
	}
	if all[0].Time.IsZero() || !all[0].Time.Equal(events[0].Time) {
		t.Fatalf("event time = %v, want %v", all[0].Time, events[0].Time)
	}
	if all[2].Payload != nil {
		t.Fatalf("nil payload round-trip = %q, want nil", all[2].Payload)
	}
	// Range bounds are inclusive.
	window, err := current.Events(ctx, "s1", all[1].Seq, all[2].Seq)
	if err != nil || len(window) != 2 || window[0].Seq != all[1].Seq {
		t.Fatalf("range events = %+v, %v", window, err)
	}
	if earlier, err := current.Events(ctx, "s1", 0, all[0].Seq); err != nil || len(earlier) != 1 {
		t.Fatalf("upper-bound events = %+v, %v", earlier, err)
	}
	if beyond, err := current.Events(ctx, "s1", all[len(all)-1].Seq+10, 0); err != nil || len(beyond) != 0 {
		t.Fatalf("from beyond end = %+v, %v", beyond, err)
	}
	// Other sessions are isolated.
	if other, err := current.Events(ctx, "s2", 0, 0); err != nil || len(other) != 1 {
		t.Fatalf("Events(s2) = %+v, %v", other, err)
	}
}

func TestEventSearch(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	for _, event := range []rig.SessionEvent{
		{SessionID: "s1", Type: "user", Payload: json.RawMessage(`{"text":"alpha"}`)},
		{SessionID: "s1", Type: "tool", Payload: json.RawMessage(`{"text":"beta 100% done"}`)},
		{SessionID: "s2", Type: "assistant", Payload: json.RawMessage(`{"text":"gamma"}`)},
	} {
		if err := current.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	search := func(query string, limit int) []rig.SessionEvent {
		t.Helper()
		events, err := current.Search(ctx, query, limit)
		if err != nil {
			t.Fatalf("Search(%q, %d): %v", query, limit, err)
		}
		return events
	}
	if matches := search("alpha", 0); len(matches) != 1 || matches[0].SessionID != "s1" {
		t.Fatalf("payload search = %+v", matches)
	}
	if matches := search("ASSISTANT", 0); len(matches) != 1 || matches[0].Type != "assistant" {
		t.Fatalf("type search (case-insensitive) = %+v", matches)
	}
	if matches := search("gamma", 0); len(matches) != 1 || matches[0].SessionID != "s2" {
		t.Fatalf("cross-session search = %+v", matches)
	}
	// An empty query returns every event.
	if matches := search("  ", 0); len(matches) != 3 {
		t.Fatalf("empty query = %d matches, want 3", len(matches))
	}
	// Limit truncates.
	if matches := search("", 2); len(matches) != 2 {
		t.Fatalf("limited search = %d matches, want 2", len(matches))
	}
	// LIKE wildcards in the query are literal.
	if matches := search("100%", 0); len(matches) != 1 {
		t.Fatalf("percent search = %+v", matches)
	}
	if matches := search("%", 0); len(matches) != 1 {
		t.Fatalf("lone percent should match the literal payload only: %+v", matches)
	}
	if matches := search("no-such-text", 0); len(matches) != 0 {
		t.Fatalf("miss = %+v", matches)
	}
}

func TestEventLast(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	for _, event := range []rig.SessionEvent{
		{SessionID: "s1", Type: "one"},
		{SessionID: "s1", Type: "two"},
		{SessionID: "s1", Type: "three"},
	} {
		if err := current.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	last, err := current.Last(ctx, "s1", 2)
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if len(last) != 2 || last[0].Type != "two" || last[1].Type != "three" {
		t.Fatalf("Last = %+v, want the two newest in ascending order", last)
	}
	if all, err := current.Last(ctx, "s1", 10); err != nil || len(all) != 3 {
		t.Fatalf("Last(all) = %+v, %v", all, err)
	}
	if none, err := current.Last(ctx, "s1", 0); err != nil || len(none) != 0 {
		t.Fatalf("Last(0) = %+v, %v", none, err)
	}
	if unknown, err := current.Last(ctx, "missing", 5); err != nil || len(unknown) != 0 {
		t.Fatalf("Last(unknown) = %+v, %v", unknown, err)
	}
}

func TestAppendEventUpdatesSession(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "s1", "events"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	before := metaOf(t, current, "s1").UpdatedAt
	time.Sleep(2 * time.Millisecond)
	if err := current.AppendEvent(ctx, rig.SessionEvent{SessionID: "s1", Type: "user"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if after := metaOf(t, current, "s1").UpdatedAt; !after.After(before) {
		t.Fatalf("session updated_at did not advance: %v -> %v", before, after)
	}
	// AppendEvent for an unknown session creates it.
	if err := current.AppendEvent(ctx, rig.SessionEvent{SessionID: "created-by-event", Type: "user"}); err != nil {
		t.Fatalf("AppendEvent(new session): %v", err)
	}
	metaOf(t, current, "created-by-event")
	if err := current.AppendEvent(ctx, rig.SessionEvent{Type: "user"}); err == nil {
		t.Fatal("event without session ID accepted")
	}
}

func TestDeleteSessionCascades(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	if err := current.CreateSession(ctx, "keep", "keep"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := current.History().Append(ctx, "doomed", sampleMessages()...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := current.AppendEvent(ctx, rig.SessionEvent{SessionID: "doomed", Type: "user"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := current.CheckpointStore().Save(ctx, "doomed", &rig.Checkpoint{SessionID: "doomed"}); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}
	if err := current.DeleteSession(ctx, "doomed"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, found, err := current.GetSessionMeta(ctx, "doomed"); err != nil || found {
		t.Fatalf("deleted session meta = found=%v err=%v", found, err)
	}
	if messages, err := current.History().Load(ctx, "doomed"); err != nil || len(messages) != 0 {
		t.Fatalf("history after delete = %v, %v", messages, err)
	}
	if events, err := current.Events(ctx, "doomed", 0, 0); err != nil || len(events) != 0 {
		t.Fatalf("events after delete = %v, %v", events, err)
	}
	if checkpoint, err := current.CheckpointStore().Load(ctx, "doomed"); err != nil || checkpoint != nil {
		t.Fatalf("checkpoint after delete = %v, %v", checkpoint, err)
	}
	if metas, err := current.Sessions(ctx); err != nil || len(metas) != 1 || metas[0].ID != "keep" {
		t.Fatalf("sessions after delete = %+v, %v", metas, err)
	}
	// Deleting an unknown session is a no-op.
	if err := current.DeleteSession(ctx, "doomed"); err != nil {
		t.Fatalf("second DeleteSession: %v", err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()

	first, err := openStore(storeOptions{dataDir: dataDir})
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if err := first.CreateSession(ctx, "s1", "persisted"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := first.History().Append(ctx, "s1", sampleMessages()...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := first.AppendEvent(ctx, rig.SessionEvent{SessionID: "s1", Type: "user", Payload: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	checkpoint := &rig.Checkpoint{SessionID: "s1", TurnID: "turn-1", Messages: sampleMessages()}
	if err := first.CheckpointStore().Save(ctx, "s1", checkpoint); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := openStore(storeOptions{dataDir: dataDir})
	if err != nil {
		t.Fatalf("reopenStore: %v", err)
	}
	defer second.Close()
	metas, err := second.Sessions(ctx)
	if err != nil || len(metas) != 1 || metas[0].Title != "persisted" || metas[0].MessageCount != len(sampleMessages()) {
		t.Fatalf("reopened sessions = %+v, %v", metas, err)
	}
	messages, err := second.History().Load(ctx, "s1")
	if err != nil || !reflect.DeepEqual(messages, sampleMessages()) {
		t.Fatalf("reopened history = %#v, %v", messages, err)
	}
	events, err := second.Events(ctx, "s1", 0, 0)
	if err != nil || len(events) != 1 || events[0].Type != "user" {
		t.Fatalf("reopened events = %+v, %v", events, err)
	}
	gotCheckpoint, err := second.CheckpointStore().Load(ctx, "s1")
	if err != nil || !reflect.DeepEqual(gotCheckpoint, checkpoint) {
		t.Fatalf("reopened checkpoint = %#v, %v", gotCheckpoint, err)
	}
}

func TestConcurrentStoreAccess(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers*4)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			id := "session-" + string(rune('a'+worker))
			if err := current.CreateSession(ctx, id, "worker"); err != nil {
				errs <- err
				return
			}
			for round := 0; round < 5; round++ {
				message := rig.NewMessage(rig.RoleUser, "message")
				if err := current.History().Append(ctx, id, message); err != nil {
					errs <- err
					return
				}
				if _, err := current.History().Load(ctx, id); err != nil {
					errs <- err
					return
				}
				if err := current.AppendEvent(ctx, rig.SessionEvent{SessionID: id, Type: "round"}); err != nil {
					errs <- err
					return
				}
			}
			if err := current.RenameSession(ctx, id, "renamed"); err != nil {
				errs <- err
				return
			}
			if _, _, err := current.GetSessionMeta(ctx, id); err != nil {
				errs <- err
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent access failed: %v", err)
	}
	metas, err := current.Sessions(ctx)
	if err != nil || len(metas) != workers {
		t.Fatalf("sessions = %+v, %v", metas, err)
	}
	for _, meta := range metas {
		if meta.MessageCount != 5 {
			t.Fatalf("session %q message count = %d, want 5", meta.ID, meta.MessageCount)
		}
		if meta.Title != "renamed" {
			t.Fatalf("session %q title = %q", meta.ID, meta.Title)
		}
	}
}

func TestBusyTimeoutAcrossStores(t *testing.T) {
	dataDir := t.TempDir()
	first := openTestStoreAt(t, dataDir)
	second := openTestStoreAt(t, dataDir)
	ctx := context.Background()
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			current := first
			if worker%2 == 1 {
				current = second
			}
			sessionID := "busy-" + string(rune('a'+worker))
			for round := 0; round < 5; round++ {
				if err := current.History().Append(ctx, sessionID, rig.NewMessage(rig.RoleUser, "x")); err != nil {
					errs <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write across stores failed (busy timeout should absorb contention): %v", err)
	}
	metas, err := first.Sessions(ctx)
	if err != nil || len(metas) != workers {
		t.Fatalf("sessions after concurrent writes = %+v, %v", metas, err)
	}
}

func TestCustomBusyTimeoutConfig(t *testing.T) {
	current, err := openStore(storeOptions{dataDir: t.TempDir(), busyTimeout: 1234 * time.Millisecond})
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer current.Close()
	var busyTimeout int
	if err := current.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != 1234 {
		t.Fatalf("busy_timeout = %d, want 1234", busyTimeout)
	}
}

func TestSessionIDValidation(t *testing.T) {
	current := openTestStore(t)
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
	}{
		{"CreateSession", func() error { return current.CreateSession(ctx, " ", "") }},
		{"DeleteSession", func() error { return current.DeleteSession(ctx, "") }},
		{"RenameSession", func() error { return current.RenameSession(ctx, "", "x") }},
		{"GetSessionMeta", func() error { _, _, err := current.GetSessionMeta(ctx, ""); return err }},
		{"History.Load", func() error { _, err := current.History().Load(ctx, ""); return err }},
		{"History.Append", func() error { return current.History().Append(ctx, "") }},
		{"ReplaceHistory", func() error { return current.ReplaceHistory(ctx, "", nil) }},
		{"Checkpoint.Load", func() error { _, err := current.CheckpointStore().Load(ctx, ""); return err }},
		{"Checkpoint.Save", func() error {
			return current.CheckpointStore().Save(ctx, "", &rig.Checkpoint{})
		}},
		{"Checkpoint.Clear", func() error { return current.CheckpointStore().Clear(ctx, "") }},
		{"Events", func() error { _, err := current.Events(ctx, "", 0, 0); return err }},
		{"Last", func() error { _, err := current.Last(ctx, "", 5); return err }},
		{"AppendEvent", func() error { return current.AppendEvent(ctx, rig.SessionEvent{}) }},
	}
	for _, testCase := range cases {
		if err := testCase.call(); err == nil {
			t.Fatalf("%s accepted an empty session ID", testCase.name)
		}
	}
}

func TestCancelledContextIsRespected(t *testing.T) {
	current := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := current.CreateSession(ctx, "s1", "title"); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateSession with cancelled ctx = %v, want context.Canceled", err)
	}
	if _, err := current.Sessions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sessions with cancelled ctx = %v", err)
	}
}
