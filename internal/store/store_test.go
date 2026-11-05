package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// openTestStore opens a store in a fresh temp directory and closes it at the
// end of the test.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func mustWorkspace(t *testing.T, s *Store, name, root string) *Workspace {
	t.Helper()
	w, err := s.CreateWorkspace(name, root)
	if err != nil {
		t.Fatalf("CreateWorkspace(%q): %v", name, err)
	}
	return w
}

func mustSession(t *testing.T, s *Store, workspaceID, title string) *Session {
	t.Helper()
	sess, err := s.CreateSession(workspaceID, title)
	if err != nil {
		t.Fatalf("CreateSession(%q): %v", workspaceID, err)
	}
	return sess
}

func TestOpenCreatesIndexAndPragmas(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested data", "dir with space?and#chars")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if got := s.Dir(); got != dir {
		t.Errorf("Dir() = %q, want %q", got, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, dbFileName)); err != nil {
		t.Fatalf("index file missing: %v", err)
	}

	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var foreignKeys int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeout int
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout <= 0 {
		t.Errorf("busy_timeout = %d, want > 0", busyTimeout)
	}
	if version, err := schemaVersion(s.db); err != nil || version != SchemaVersion {
		t.Errorf("schemaVersion() = %d, %v; want %d, nil", version, err, SchemaVersion)
	}
}

func TestOpenRejectsEmptyDir(t *testing.T) {
	if _, err := Open(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Open(\"\") error = %v, want ErrInvalidArgument", err)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	wantTables := []string{
		"schema_version", "workspaces", "sessions", "messages",
		"tool_calls", "events", "settings", "memories",
	}
	for i := 0; i < 3; i++ {
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		version, err := schemaVersion(s.db)
		if err != nil {
			t.Fatalf("Open #%d: schemaVersion: %v", i+1, err)
		}
		if version != SchemaVersion {
			t.Errorf("Open #%d: schema version = %d, want %d", i+1, version, SchemaVersion)
		}
		var rows int
		if err := s.db.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&rows); err != nil {
			t.Fatalf("Open #%d: count schema_version: %v", i+1, err)
		}
		if rows != 1 {
			t.Errorf("Open #%d: schema_version rows = %d, want 1", i+1, rows)
		}
		for _, name := range wantTables {
			ok, err := tableExists(s.db, name)
			if err != nil {
				t.Fatalf("Open #%d: tableExists(%q): %v", i+1, name, err)
			}
			if !ok {
				t.Errorf("Open #%d: table %q missing", i+1, name)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
}

func TestSchemaVersionGuard(t *testing.T) {
	cases := []struct {
		name   string
		mutate string
		want   error
	}{
		{"older", `UPDATE schema_version SET version = 0`, ErrSchemaTooOld},
		{"newer", `UPDATE schema_version SET version = 3`, ErrSchemaTooNew},
		{"empty", `DELETE FROM schema_version`, ErrSchemaTooOld},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, err := s.db.Exec(tc.mutate); err != nil {
				t.Fatalf("mutate schema version: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			for attempt := 1; attempt <= 2; attempt++ {
				_, err := Open(dir)
				if err == nil {
					t.Fatalf("Open attempt %d after %s schema: expected error", attempt, tc.name)
				}
				if !errors.Is(err, tc.want) {
					t.Fatalf("Open attempt %d error = %v, want errors.Is(..., %v)", attempt, err, tc.want)
				}
			}
		})
	}
}

func TestWorkspaceCRUD(t *testing.T) {
	s := openTestStore(t)

	w, err := s.CreateWorkspace("alpha", "/tmp/alpha")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if w.ID == "" {
		t.Error("CreateWorkspace returned an empty id")
	}
	if w.Name != "alpha" || w.Root != "/tmp/alpha" {
		t.Errorf("CreateWorkspace = %+v, want name alpha root /tmp/alpha", w)
	}
	if w.CreatedAt.IsZero() {
		t.Error("CreateWorkspace left CreatedAt zero")
	}

	if _, err := s.CreateWorkspace("  ", "/tmp/blank"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("CreateWorkspace(blank) error = %v, want ErrInvalidArgument", err)
	}

	w2, err := s.CreateWorkspace("beta", "/tmp/beta")
	if err != nil {
		t.Fatalf("CreateWorkspace(beta): %v", err)
	}
	list, err := s.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListWorkspaces returned %d, want 2", len(list))
	}
	byID := map[string]Workspace{}
	for _, got := range list {
		byID[got.ID] = got
	}
	for _, want := range []*Workspace{w, w2} {
		got, ok := byID[want.ID]
		if !ok {
			t.Errorf("workspace %q missing from ListWorkspaces", want.ID)
			continue
		}
		if got.Name != want.Name || got.Root != want.Root || !got.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("workspace %q round-trip = %+v, want %+v", want.ID, got, want)
		}
	}
}

func TestSessionCRUD(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")

	sess := mustSession(t, s, ws.ID, "First session")
	if sess.Status != SessionIdle {
		t.Errorf("new session status = %q, want %q", sess.Status, SessionIdle)
	}
	if sess.WorkspaceID != ws.ID || sess.Title != "First session" {
		t.Errorf("CreateSession = %+v", sess)
	}
	if sess.CreatedAt.IsZero() || sess.UpdatedAt.IsZero() {
		t.Error("CreateSession left timestamps zero")
	}

	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != sess.ID || got.WorkspaceID != ws.ID || got.Title != sess.Title ||
		got.Status != sess.Status || !got.CreatedAt.Equal(sess.CreatedAt) ||
		!got.UpdatedAt.Equal(sess.UpdatedAt) {
		t.Errorf("GetSession = %+v, want %+v", got, sess)
	}

	if _, err := s.GetSession("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession(missing) error = %v, want ErrNotFound", err)
	}

	updated, err := s.UpdateSessionStatus(sess.ID, SessionRunning)
	if err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	if updated.Status != SessionRunning {
		t.Errorf("updated status = %q, want %q", updated.Status, SessionRunning)
	}
	if updated.UpdatedAt.Before(sess.UpdatedAt) {
		t.Errorf("updated_at went backwards: %v < %v", updated.UpdatedAt, sess.UpdatedAt)
	}
	got, err = s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after update: %v", err)
	}
	if got.Status != SessionRunning {
		t.Errorf("persisted status = %q, want %q", got.Status, SessionRunning)
	}

	if _, err := s.UpdateSessionStatus("does-not-exist", SessionRunning); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateSessionStatus(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateSessionStatus(sess.ID, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("UpdateSessionStatus(empty status) error = %v, want ErrInvalidArgument", err)
	}

	if _, err := s.CreateSession("missing-workspace", "orphan"); !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateSession(missing workspace) error = %v, want ErrNotFound", err)
	}

	second := mustSession(t, s, ws.ID, "Second session")
	otherWS := mustWorkspace(t, s, "beta", "/tmp/beta")
	if _, err := s.CreateSession(otherWS.ID, "Elsewhere"); err != nil {
		t.Fatalf("CreateSession(other workspace): %v", err)
	}

	list, err := s.ListSessions(ws.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListSessions(%q) returned %d, want 2", ws.ID, len(list))
	}
	seen := map[string]bool{}
	for _, got := range list {
		seen[got.ID] = true
	}
	if !seen[sess.ID] || !seen[second.ID] {
		t.Errorf("ListSessions(%q) = %+v, want both sessions", ws.ID, list)
	}

	if list, err = s.ListSessions("no-such-workspace"); err != nil {
		t.Fatalf("ListSessions(unknown): %v", err)
	} else if len(list) != 0 {
		t.Errorf("ListSessions(unknown) returned %d, want 0", len(list))
	}
	if list, err = s.ListSessions(""); err != nil {
		t.Fatalf("ListSessions(all): %v", err)
	} else if len(list) != 3 {
		t.Errorf("ListSessions(all) returned %d, want 3", len(list))
	}
}

func TestMessageCRUD(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "chat")

	user, err := s.AppendMessage(Message{
		SessionID: sess.ID,
		Role:      "user",
		Content:   "hello",
		TurnID:    "turn-1",
	})
	if err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	if user.ID == "" {
		t.Error("AppendMessage returned an empty id")
	}
	if user.CreatedAt.IsZero() {
		t.Error("AppendMessage left CreatedAt zero")
	}

	assistant, err := s.AppendMessage(Message{
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   "hi there",
		Reasoning: "greeting",
		TurnID:    "turn-1",
	})
	if err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	if assistant.ID == user.ID {
		t.Error("AppendMessage reused the id of an earlier message")
	}

	list, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListMessages returned %d, want 2", len(list))
	}
	if list[0].ID != user.ID || list[1].ID != assistant.ID {
		t.Errorf("ListMessages order = %q, %q; want %q, %q",
			list[0].ID, list[1].ID, user.ID, assistant.ID)
	}
	if list[0].Content != "hello" || list[0].Role != "user" || list[0].TurnID != "turn-1" {
		t.Errorf("user message round-trip = %+v", list[0])
	}
	if list[1].Content != "hi there" || list[1].Reasoning != "greeting" || list[1].TurnID != "turn-1" {
		t.Errorf("assistant message round-trip = %+v", list[1])
	}
	if !list[0].CreatedAt.Equal(user.CreatedAt) {
		t.Errorf("created_at round-trip = %v, want %v", list[0].CreatedAt, user.CreatedAt)
	}

	// Caller-supplied ids and timestamps are preserved.
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	custom, err := s.AppendMessage(Message{
		ID: "msg-custom", SessionID: sess.ID, Role: "system", Content: "ctx", CreatedAt: stamp,
	})
	if err != nil {
		t.Fatalf("AppendMessage(custom id): %v", err)
	}
	if custom.ID != "msg-custom" || !custom.CreatedAt.Equal(stamp) {
		t.Errorf("AppendMessage(custom) = %+v", custom)
	}

	if _, err := s.AppendMessage(Message{SessionID: "missing", Role: "user"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendMessage(missing session) error = %v, want ErrNotFound", err)
	}
	if _, err := s.AppendMessage(Message{SessionID: sess.ID, Role: " "}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("AppendMessage(empty role) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.ListMessages(""); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("ListMessages(empty) error = %v, want ErrInvalidArgument", err)
	}
	if list, err = s.ListMessages("other-session"); err != nil {
		t.Fatalf("ListMessages(other): %v", err)
	} else if len(list) != 0 {
		t.Errorf("ListMessages(other) returned %d, want 0", len(list))
	}
}

func TestSettingsCRUD(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, err := s.GetSetting("theme")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "dark" {
		t.Errorf("GetSetting = %q, want dark", got)
	}

	if err := s.SetSetting("theme", "light"); err != nil {
		t.Fatalf("SetSetting(overwrite): %v", err)
	}
	if got, err = s.GetSetting("theme"); err != nil || got != "light" {
		t.Errorf("GetSetting after overwrite = %q, %v; want light, nil", got, err)
	}

	if err := s.SetSetting("empty", ""); err != nil {
		t.Fatalf("SetSetting(empty value): %v", err)
	}
	if got, err = s.GetSetting("empty"); err != nil || got != "" {
		t.Errorf("GetSetting(empty value) = %q, %v; want \"\", nil", got, err)
	}

	if _, err := s.GetSetting("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSetting(missing) error = %v, want ErrNotFound", err)
	}
	if err := s.SetSetting("", "x"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("SetSetting(empty key) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.GetSetting(""); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("GetSetting(empty key) error = %v, want ErrInvalidArgument", err)
	}
}

func TestEventsOrdering(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "chat")

	types := []string{"turn.start", "step", "turn.end"}
	payloads := []string{`{"turn":"t1"}`, "", `{"tokens":3}`}
	appended := make([]*Event, 0, len(types))
	for i, typ := range types {
		ev, err := s.AppendEvent(sess.ID, Event{Type: typ, Payload: json.RawMessage(payloads[i])})
		if err != nil {
			t.Fatalf("AppendEvent(%q): %v", typ, err)
		}
		if ev.Seq != int64(i+1) {
			t.Errorf("AppendEvent(%q).Seq = %d, want %d", typ, ev.Seq, i+1)
		}
		if ev.TS.IsZero() {
			t.Errorf("AppendEvent(%q) left TS zero", typ)
		}
		appended = append(appended, ev)
	}

	list, err := s.ListEvents(sess.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(list) != len(types) {
		t.Fatalf("ListEvents returned %d, want %d", len(list), len(types))
	}
	for i, ev := range list {
		if ev.Seq != int64(i+1) {
			t.Errorf("ListEvents[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
		if ev.Type != types[i] {
			t.Errorf("ListEvents[%d].Type = %q, want %q", i, ev.Type, types[i])
		}
		if string(ev.Payload) != payloads[i] {
			t.Errorf("ListEvents[%d].Payload = %q, want %q", i, ev.Payload, payloads[i])
		}
		if !ev.TS.Equal(appended[i].TS) {
			t.Errorf("ListEvents[%d].TS = %v, want %v", i, ev.TS, appended[i].TS)
		}
	}

	// Sequences are per session and independent of other sessions.
	other := mustSession(t, s, ws.ID, "other")
	otherEv, err := s.AppendEvent(other.ID, Event{Type: "turn.start"})
	if err != nil {
		t.Fatalf("AppendEvent(other session): %v", err)
	}
	if otherEv.Seq != 1 {
		t.Errorf("first event of second session Seq = %d, want 1", otherEv.Seq)
	}

	// Caller-supplied Seq is ignored; caller-supplied TS is preserved.
	stamp := time.Date(2026, 2, 3, 4, 5, 6, 7, time.UTC)
	ev, err := s.AppendEvent(sess.ID, Event{Seq: 99, Type: "custom", TS: stamp})
	if err != nil {
		t.Fatalf("AppendEvent(custom ts): %v", err)
	}
	if ev.Seq != 4 {
		t.Errorf("AppendEvent with caller Seq 99 got Seq %d, want 4", ev.Seq)
	}
	if !ev.TS.Equal(stamp) {
		t.Errorf("AppendEvent TS = %v, want %v", ev.TS, stamp)
	}

	// Invalid events are rejected without consuming a sequence number.
	if _, err := s.AppendEvent(sess.ID, Event{}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("AppendEvent(no type) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.AppendEvent(sess.ID, Event{Type: "bad", Payload: json.RawMessage(`{oops`)}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("AppendEvent(bad payload) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.AppendEvent("missing-session", Event{Type: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendEvent(missing session) error = %v, want ErrNotFound", err)
	}
	if list, err = s.ListEvents(sess.ID); err != nil {
		t.Fatalf("ListEvents after rejects: %v", err)
	} else if len(list) != 4 || list[3].Seq != 4 {
		t.Errorf("ListEvents after rejects has %d events, want 4 ending at seq 4", len(list))
	}
	if _, err := s.ListEvents(""); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("ListEvents(empty) error = %v, want ErrInvalidArgument", err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)

	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open #1: %v", err)
	}
	ws := mustWorkspace(t, s1, "alpha", "/srv/alpha")
	sess := mustSession(t, s1, ws.ID, "persisted")
	msg, err := s1.AppendMessage(Message{
		ID: "msg-1", SessionID: sess.ID, Role: "user", Content: "remember me",
		TurnID: "turn-1", CreatedAt: stamp,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	ev, err := s1.AppendEvent(sess.ID, Event{
		Type: "turn.start", Payload: json.RawMessage(`{"turn":"t1"}`), TS: stamp,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := s1.SetSetting("model", "mygo"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if _, err := s1.UpdateSessionStatus(sess.ID, SessionPaused); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close #1 twice: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	defer s2.Close()

	workspaces, err := s2.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].ID != ws.ID || workspaces[0].Name != ws.Name {
		t.Fatalf("ListWorkspaces = %+v, want workspace %+v", workspaces, ws)
	}

	gotSession, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if gotSession.Status != SessionPaused {
		t.Errorf("persisted status = %q, want %q", gotSession.Status, SessionPaused)
	}
	if !gotSession.CreatedAt.Equal(sess.CreatedAt) {
		t.Errorf("persisted created_at = %v, want %v", gotSession.CreatedAt, sess.CreatedAt)
	}

	messages, err := s2.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != msg.ID || messages[0].Content != "remember me" {
		t.Errorf("persisted messages = %+v, want message %+v", messages, msg)
	}
	if !messages[0].CreatedAt.Equal(stamp) {
		t.Errorf("persisted message created_at = %v, want %v", messages[0].CreatedAt, stamp)
	}

	events, err := s2.ListEvents(sess.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Seq != ev.Seq || events[0].Type != "turn.start" ||
		string(events[0].Payload) != `{"turn":"t1"}` {
		t.Errorf("persisted events = %+v, want event %+v", events, ev)
	}
	if !events[0].TS.Equal(stamp) {
		t.Errorf("persisted event ts = %v, want %v", events[0].TS, stamp)
	}

	if got, err := s2.GetSetting("model"); err != nil || got != "mygo" {
		t.Errorf("persisted setting = %q, %v; want mygo, nil", got, err)
	}
}

func TestPersistenceWriteSurvivesReopenWithoutExplicitClose(t *testing.T) {
	// Simulates a crash: the process never calls Close. WAL + fsync must still
	// make the committed transaction visible on the next Open.
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open #1: %v", err)
	}
	t.Cleanup(func() { _ = s1.Close() }) // close only after the assertions
	ws := mustWorkspace(t, s1, "alpha", "/srv/alpha")
	if _, err := s1.CreateSession(ws.ID, "crashy"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	s2, err := Open(dir) // leak s1 on purpose: no Close, like a crash
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	defer s2.Close()

	list, err := s2.ListSessions(ws.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListSessions after unclean reopen returned %d, want 1", len(list))
	}
}

func TestAppendEventConcurrentOrdering(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "concurrent")

	const goroutines, perGoroutine = 8, 5
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := s.AppendEvent(sess.ID, Event{Type: "step"}); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent AppendEvent: %v", err)
	}

	list, err := s.ListEvents(sess.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(list) != goroutines*perGoroutine {
		t.Fatalf("ListEvents returned %d events, want %d", len(list), goroutines*perGoroutine)
	}
	for i, ev := range list {
		want := int64(i + 1)
		if ev.Seq != want {
			t.Fatalf("event[%d].Seq = %d, want %d (sequence gap or duplicate)", i, ev.Seq, want)
		}
	}
}
