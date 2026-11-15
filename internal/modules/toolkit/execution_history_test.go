package toolkit

// Tests for the execution-history read model (contracts.KeyExecutionHistory):
// the durable execution/* events are folded into one bounded record per job or
// terminal, the live managers overlay a truthful "running" state, and the
// toolkit provides the service so the session header can show the records after
// a restart (the events, not the output, are persisted).

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// executionHistoryStore is the minimal session log the service reads; it also
// records every read so the bounded-window rule is assertable.
type executionHistoryStore struct {
	contracts.Store
	mu       sync.Mutex
	events   []rig.SessionEvent
	readErr  error
	lastRead int
	lastID   string
}

func (s *executionHistoryStore) AppendEvent(_ context.Context, event rig.SessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *executionHistoryStore) Last(_ context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRead = limit
	s.lastID = sessionID
	if s.readErr != nil {
		return nil, s.readErr
	}
	out := make([]rig.SessionEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// executionLivenessStub is the live-manager overlay the service consults.
type executionLivenessStub struct {
	runningIDs map[string]bool
}

func (s executionLivenessStub) running(kind, id string) bool {
	return s.runningIDs[kind+"/"+id]
}

// executionEvent builds one durable event with a JSON payload.
func executionEvent(t *testing.T, sessionID, eventType string, at time.Time, payload map[string]any) rig.SessionEvent {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", eventType, err)
	}
	return rig.SessionEvent{SessionID: sessionID, Type: eventType, Time: at, Payload: encoded}
}

func TestExecutionHistoryFoldsJobLifecycle(t *testing.T) {
	started := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	store := &executionHistoryStore{events: []rig.SessionEvent{
		executionEvent(t, "sess-1", contracts.ExecutionJobStarted, started, map[string]any{
			"id": "job-1", "command": "npm test --watch", "cwd": "/tmp/ws",
			"root": "/tmp/ws", "mode": "workspace-write",
		}),
		executionEvent(t, "sess-1", contracts.ExecutionJobFinished, started.Add(4*time.Second), map[string]any{
			"id": "job-1", "state": "exited", "kind": "bash",
		}),
	}}
	service := newExecutionHistoryService(store, nil)
	records := service.ExecutionHistory("sess-1", 10)
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one folded job", records)
	}
	record := records[0]
	if record.ID != "job-1" || record.SessionID != "sess-1" || record.Kind != contracts.ExecutionKindJob {
		t.Fatalf("record identity = %+v", record)
	}
	if record.Command != "npm test --watch" || record.CWD != "/tmp/ws" || record.Root != "/tmp/ws" {
		t.Fatalf("record details = %+v", record)
	}
	if record.State != contracts.ExecutionStateFinished || record.Outcome != "exited" {
		t.Fatalf("record state/outcome = %q/%q, want finished/exited", record.State, record.Outcome)
	}
	if !record.Started.Equal(started) || !record.Ended.Equal(started.Add(4*time.Second)) {
		t.Fatalf("record times = %v..%v", record.Started, record.Ended)
	}
	if record.Duration() != 4*time.Second {
		t.Fatalf("record duration = %v, want 4s", record.Duration())
	}
}

func TestExecutionHistoryOverlaysLiveState(t *testing.T) {
	started := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	event := executionEvent(t, "sess-1", contracts.ExecutionJobStarted, started, map[string]any{
		"id": "job-1", "command": "tail -f /dev/null", "root": "/tmp/ws",
	})
	store := &executionHistoryStore{events: []rig.SessionEvent{event}}
	running := newExecutionHistoryService(store, executionLivenessStub{
		runningIDs: map[string]bool{contracts.ExecutionKindJob + "/job-1": true},
	})
	records := running.ExecutionHistory("sess-1", 0)
	if len(records) != 1 || records[0].State != contracts.ExecutionStateRunning {
		t.Fatalf("live job records = %+v, want one running record", records)
	}
	if !records[0].Ended.IsZero() {
		t.Fatalf("running record has an end time: %v", records[0].Ended)
	}

	// Without a live owner (an app restart, or a process killed with the app)
	// the same start event is an interrupted record: the end is unknown, not a
	// failure.
	restarted := newExecutionHistoryService(store, executionLivenessStub{})
	records = restarted.ExecutionHistory("sess-1", 0)
	if len(records) != 1 || records[0].State != contracts.ExecutionStateInterrupted {
		t.Fatalf("restarted records = %+v, want one interrupted record", records)
	}
	if records[0].Duration() != 0 {
		t.Fatalf("interrupted record duration = %v, want 0", records[0].Duration())
	}
}

func TestExecutionHistoryFoldsTerminalLifecycle(t *testing.T) {
	started := time.Date(2026, 11, 15, 10, 0, 0, 0, time.Local)
	store := &executionHistoryStore{events: []rig.SessionEvent{
		executionEvent(t, "sess-1", contracts.ExecutionTerminalOpened, started, map[string]any{
			"id": "term-1", "cwd": "/tmp/ws", "transport": "pipe", "root": "/tmp/ws", "mode": "workspace-write",
		}),
		executionEvent(t, "sess-1", contracts.ExecutionTerminalClosed, started.Add(time.Minute), map[string]any{
			"id": "term-1", "root": "/tmp/ws",
		}),
	}}
	service := newExecutionHistoryService(store, nil)
	records := service.ExecutionHistory("sess-1", 0)
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one folded terminal", records)
	}
	record := records[0]
	if record.Kind != contracts.ExecutionKindTerminal || record.ID != "term-1" || record.Transport != "pipe" {
		t.Fatalf("terminal record = %+v", record)
	}
	if record.CWD != "/tmp/ws" || record.State != contracts.ExecutionStateFinished {
		t.Fatalf("terminal record cwd/state = %q/%q", record.CWD, record.State)
	}
}

func TestExecutionHistoryBoundedAndNewestFirst(t *testing.T) {
	base := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	store := &executionHistoryStore{}
	for index := 0; index < 5; index++ {
		at := base.Add(time.Duration(index) * time.Minute)
		store.events = append(store.events,
			executionEvent(t, "sess-1", contracts.ExecutionJobStarted, at, map[string]any{
				"id": "job-" + string(rune('a'+index)), "command": "cmd",
			}),
			executionEvent(t, "sess-1", contracts.ExecutionJobFinished, at.Add(time.Second), map[string]any{
				"id": "job-" + string(rune('a'+index)), "state": "exited",
			}),
		)
	}
	service := newExecutionHistoryService(store, nil)
	records := service.ExecutionHistory("sess-1", 2)
	if len(records) != 2 {
		t.Fatalf("records = %+v, want the 2 newest", records)
	}
	if records[0].ID != "job-e" || records[1].ID != "job-d" {
		t.Fatalf("order = %q,%q, want newest first", records[0].ID, records[1].ID)
	}
	if store.lastRead != executionHistoryEvents {
		t.Fatalf("store read window = %d, want the bounded %d", store.lastRead, executionHistoryEvents)
	}
	if got := service.ExecutionHistory("sess-1", 0); len(got) != 5 {
		t.Fatalf("default limit = %d records, want all 5", len(got))
	}
}

func TestExecutionHistoryWithoutStoreIsEmpty(t *testing.T) {
	if records := newExecutionHistoryService(nil, nil).ExecutionHistory("sess-1", 0); len(records) != 0 {
		t.Fatalf("records without a store = %+v, want none", records)
	}
	store := &executionHistoryStore{}
	service := newExecutionHistoryService(store, nil)
	if records := service.ExecutionHistory("", 0); len(records) != 0 {
		t.Fatalf("records without a session = %+v, want none", records)
	}
	if records := service.ExecutionHistory("sess-unknown", 0); len(records) != 0 {
		t.Fatalf("records of an unknown session = %+v, want none", records)
	}
	if store.lastRead != executionHistoryEvents {
		t.Fatalf("session-less read touched the store (window %d)", store.lastRead)
	}
}

func TestExecutionHistorySkipsOrphansAndMalformedPayloads(t *testing.T) {
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	store := &executionHistoryStore{events: []rig.SessionEvent{
		// A finish without its start in the bounded window: no record to attach
		// it to and no fabricated one.
		executionEvent(t, "sess-1", contracts.ExecutionJobFinished, at, map[string]any{"id": "job-x", "state": "exited"}),
		// Malformed payloads are skipped, never a panic or a half record.
		{SessionID: "sess-1", Type: contracts.ExecutionJobStarted, Time: at, Payload: []byte(`{`)},
		executionEvent(t, "sess-1", contracts.ExecutionJobStarted, at.Add(time.Second), map[string]any{
			"id": "job-ok", "command": "go test ./...",
		}),
	}}
	records := newExecutionHistoryService(store, nil).ExecutionHistory("sess-1", 0)
	if len(records) != 1 || records[0].ID != "job-ok" {
		t.Fatalf("records = %+v, want only the readable start", records)
	}
}

func TestExecutionHistoryReadErrorIsEmpty(t *testing.T) {
	store := &executionHistoryStore{readErr: errors.New("db is closed")}
	records := newExecutionHistoryService(store, nil).ExecutionHistory("sess-1", 0)
	if len(records) != 0 {
		t.Fatalf("records after a read error = %+v, want none", records)
	}
}

func TestExecutionListLivenessHelpers(t *testing.T) {
	jobList := "jobs: 2\n" +
		"- job-1 [bash] state=running status=running exit=-1 duration_ms=1200 command=tail -f /dev/null\n" +
		"- job-2 [bash] state=exited status=completed exit=0 duration_ms=100 command=go build ./...\n"
	if !executionListHasRunningJob(jobList, "job-1") {
		t.Fatalf("running job not detected in:\n%s", jobList)
	}
	if executionListHasRunningJob(jobList, "job-2") {
		t.Fatalf("finished job detected as running")
	}
	terminalList := "session_id: term-1\n" +
		"transport: pipe\ncwd: /tmp/ws\nrunning: true\nexit: -\n\n" +
		"session_id: term-2\ntransport: pipe\ncwd: /tmp/ws\nrunning: false\nexit: 0\n"
	if !executionListHasRunningTerminal(terminalList, "term-1") {
		t.Fatalf("running terminal not detected in:\n%s", terminalList)
	}
	if executionListHasRunningTerminal(terminalList, "term-2") {
		t.Fatalf("closed terminal detected as running")
	}
}

// TestModuleProvidesExecutionHistory pins the toolkit wiring: the module
// provides contracts.KeyExecutionHistory backed by the durable store, so the
// session header can require it without importing the toolkit.
func TestModuleProvidesExecutionHistory(t *testing.T) {
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	store := &executionHistoryStore{events: []rig.SessionEvent{
		executionEvent(t, "sess-1", contracts.ExecutionJobStarted, at, map[string]any{
			"id": "job-1", "command": "npm test",
		}),
		executionEvent(t, "sess-1", contracts.ExecutionJobFinished, at.Add(time.Second), map[string]any{
			"id": "job-1", "state": "exited",
		}),
	}}
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	if _, err := root.Provide(contracts.KeyStore, contracts.Store(store)); err != nil {
		t.Fatalf("provide store: %v", err)
	}
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	history, err := root.Require(contracts.KeyExecutionHistory)
	if err != nil {
		t.Fatalf("require execution history: %v", err)
	}
	if history == nil {
		t.Fatal("execution history service is nil")
	}
	records := history.ExecutionHistory("sess-1", 0)
	if len(records) != 1 || records[0].ID != "job-1" || records[0].State != contracts.ExecutionStateFinished {
		t.Fatalf("records = %+v, want the folded job", records)
	}
	if got := history.ExecutionHistory("sess-2", 0); len(got) != 0 {
		t.Fatalf("records of another session = %+v, want none", got)
	}
}
